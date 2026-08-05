// Package httpx provides HTTP middleware components shared across all chi routers.
package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"onlinemenu.tr/internal/platform/auth"
)

const (
	idempotencyHeader = "Idempotency-Key"

	// idempotencyTTL matches the window during which clients may retry safely.
	// 24 hours covers all realistic network-timeout retry scenarios.
	idempotencyTTL = 24 * time.Hour

	// inFlightTTL is the maximum time a single request is allowed to hold
	// the idempotency lock. Requests taking longer than this will be treated
	// as failed by concurrent duplicates. 30 s covers the longest expected
	// handler execution under normal load.
	inFlightTTL = 30 * time.Second

	idempotencyCachePrefix = "idem:"
	idempotencyLockSuffix  = ":lock"
)

// idempotencyEntry is the cached response stored for a previously seen key.
type idempotencyEntry struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
	// BodyHash is the SHA-256 (hex) of the request body that produced this
	// entry. It lets a later request reusing the same Idempotency-Key with a
	// different body be rejected instead of silently replaying (or worse,
	// re-executing against) an unrelated request.
	BodyHash string `json:"body_hash"`
}

// cacheLookup is the outcome of comparing an incoming request against a
// previously cached entry for the same Idempotency-Key.
type cacheLookup int

const (
	// cacheMiss means no entry exists yet (or the entry could not be read/
	// decoded, which is treated the same as a miss — the handler runs and a
	// fresh entry is written).
	cacheMiss cacheLookup = iota
	// cacheHitSameBody means a cached response exists for a request with the
	// same key AND the same body: the cached response should be replayed.
	cacheHitSameBody
	// cacheHitDifferentBody means the key was reused with a different body:
	// this must be rejected, not replayed or re-executed.
	cacheHitDifferentBody
)

// IdempotencyScopeFunc derives the namespace an Idempotency-Key is unique
// within. It returns an error when the request carries no usable identity, in
// which case the middleware answers 401 without touching Redis.
//
// The scope is what keeps two callers' identical keys apart, so it must
// include every identity dimension that could legitimately collide: tenant
// for staff, and additionally the QR code and guest session for anonymous
// diners (two diners at the same table are different callers).
type IdempotencyScopeFunc func(*http.Request) (string, error)

// ErrNoIdempotencyScope is returned by a scope function when the request has
// no identity to scope on.
var ErrNoIdempotencyScope = errors.New("httpx: no idempotency scope in request context")

// principalTenantScope is the staff scope: the authenticated tenant.
//
// The produced string is part of the Redis key format ("idem:<scope>:<key>"),
// which entries written by earlier releases already use — changing it would
// orphan every in-flight entry inside the 24h TTL window, so it must stay
// exactly the tenant UUID.
func principalTenantScope(r *http.Request) (string, error) {
	principal, err := auth.FromContext(r.Context())
	if err != nil {
		return "", ErrNoIdempotencyScope
	}
	return principal.TenantID.String(), nil
}

// GuestSessionScope scopes an Idempotency-Key to one anonymous diner
// (ADR-ARCH-006 §5 + ADR-SEC-003).
//
// tenant + qr code + guest session, in that order: tenant keeps two
// restaurants apart, the QR code keeps two tables apart, and the session id
// keeps two phones at the SAME table apart — without it, two diners
// submitting their carts with a client-generated key that happened to collide
// would see one order silently swallowed as a "replay" of the other's.
//
// It lives in platform/httpx rather than the storefront module because the
// middleware it feeds does, and a platform package cannot import a module.
func GuestSessionScope(r *http.Request) (string, error) {
	g, ok := auth.GuestFromContext(r.Context())
	if !ok {
		return "", ErrNoIdempotencyScope
	}
	return "guest:" + g.TenantID.String() + ":" + g.QRCodeID.String() + ":" + g.SessionID.String(), nil
}

// Idempotency returns a chi middleware that enforces ADR-SEC-003 for
// authenticated staff callers, scoped to the principal's tenant.
//
// It is a thin alias for IdempotencyWithScope so that every existing pos /
// payment call site — and the wire format of every cached entry — is
// unaffected by the storefront's addition of a second scope.
//
// This middleware must be placed after auth.Middleware in the chain so that
// the principal is available in the request context.
func Idempotency(cache *redis.Client) func(http.Handler) http.Handler {
	return IdempotencyWithScope(cache, principalTenantScope)
}

// IdempotencyWithScope returns a chi middleware that enforces ADR-SEC-003
// within the namespace produced by scope.
//
// On the first request with a given Idempotency-Key it acquires a short-lived
// Redis lock, executes the handler, then records the response — together with
// a hash of the request body — for 24 hours. Concurrent duplicate requests
// (same key, same scope) receive 409 Conflict while the first request is
// in-flight. A subsequent retry with the same key AND the same body receives
// the cached response without re-executing the handler. A subsequent request
// reusing the same key with a DIFFERENT body is rejected with 422: an
// Idempotency-Key identifies one logical request, not a slot that can be
// silently repointed at different input.
//
// Whatever chain populates the identity the scope reads (auth.Middleware for
// staff, the storefront guest guard for diners) must run BEFORE this
// middleware; a request that reaches it without one is refused with 401
// rather than sharing a global namespace.
func IdempotencyWithScope(cache *redis.Client, scope IdempotencyScopeFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(idempotencyHeader)
			if key == "" {
				http.Error(w, "Idempotency-Key header required", http.StatusUnprocessableEntity)
				return
			}

			scopeValue, err := scope(r)
			if err != nil {
				// The identity chain must precede idempotency middleware.
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			// Consume the body once to hash it, then restore it so the wrapped
			// handler can still decode it — the handler has not run yet.
			bodyHash, bodyBytes, err := hashAndRestoreBody(r)
			if err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

			cacheKey := fmt.Sprintf("%s%s:%s", idempotencyCachePrefix, scopeValue, key)
			lockKey := cacheKey + idempotencyLockSuffix

			switch entry, result := lookupCachedEntry(r.Context(), cache, cacheKey, bodyHash); result {
			case cacheHitSameBody:
				writeReplay(entry, w)
				return
			case cacheHitDifferentBody:
				http.Error(w, "Idempotency-Key was already used with a different request body", http.StatusUnprocessableEntity)
				return
			case cacheMiss:
				// fall through to normal handling below.
			}

			// Acquire an in-flight lock to prevent concurrent duplicate execution
			// (TOCTOU: two requests with the same key hitting cache miss simultaneously).
			locked, err := cache.SetNX(r.Context(), lockKey, "1", inFlightTTL).Result()
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if !locked {
				// Another in-flight request is already processing this key.
				http.Error(w, "Idempotency-Key is already being processed", http.StatusConflict)
				return
			}
			// Release the lock after the handler completes regardless of outcome.
			// Use a background context: the request context may already be cancelled.
			defer func() {
				_ = cache.Del(context.Background(), lockKey).Err()
			}()

			// Execute the handler and capture its response.
			rec := &responseRecorder{
				ResponseWriter: w,
				buf:            &bytes.Buffer{},
				statusCode:     http.StatusOK,
			}
			next.ServeHTTP(rec, r)

			// Only cache successful mutations; server errors must be retryable.
			if rec.statusCode >= 200 && rec.statusCode < 300 {
				entry := idempotencyEntry{
					StatusCode: rec.statusCode,
					Headers:    captureHeaders(w.Header()),
					Body:       rec.buf.Bytes(),
					BodyHash:   bodyHash,
				}
				if data, marshalErr := json.Marshal(entry); marshalErr == nil {
					_ = cache.Set(context.Background(), cacheKey, data, idempotencyTTL).Err()
				}
			}
		})
	}
}

// hashAndRestoreBody reads r.Body fully, returning its SHA-256 hex digest
// and the raw bytes so the caller can restore r.Body for downstream readers.
// A nil or already-drained body hashes as the digest of an empty byte slice,
// which is stable and comparable across requests.
func hashAndRestoreBody(r *http.Request) (hash string, body []byte, err error) {
	if r.Body == nil || r.Body == http.NoBody {
		sum := sha256.Sum256(nil)
		return hex.EncodeToString(sum[:]), nil, nil
	}
	body, err = io.ReadAll(r.Body)
	if err != nil {
		return "", nil, fmt.Errorf("httpx: read request body: %w", err)
	}
	_ = r.Body.Close()
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), body, nil
}

// lookupCachedEntry checks Redis for a cached response under cacheKey and
// classifies the result against the incoming request's bodyHash. Any Redis
// error or decode failure is treated as cacheMiss — the safe default is to
// let the handler run rather than block or misreport on infrastructure noise.
func lookupCachedEntry(ctx context.Context, cache *redis.Client, cacheKey, bodyHash string) (idempotencyEntry, cacheLookup) {
	existing, err := cache.Get(ctx, cacheKey).Result()
	if err != nil {
		return idempotencyEntry{}, cacheMiss
	}
	var entry idempotencyEntry
	if err := json.Unmarshal([]byte(existing), &entry); err != nil {
		return idempotencyEntry{}, cacheMiss
	}
	// entry.BodyHash == "" identifies an entry written before this field
	// existed. Since new entries always carry a non-empty SHA-256 digest
	// (even an empty body hashes to a fixed non-empty value), this check is
	// unambiguous. Treating it as a match preserves the pre-existing
	// same-key/same-body replay behavior for any entry still alive in the
	// 24h TTL window at deploy time, instead of spuriously rejecting it.
	if entry.BodyHash != "" && entry.BodyHash != bodyHash {
		return entry, cacheHitDifferentBody
	}
	return entry, cacheHitSameBody
}

// writeReplay writes a previously cached response to w.
func writeReplay(entry idempotencyEntry, w http.ResponseWriter) {
	for h, v := range entry.Headers {
		w.Header().Set(h, v)
	}
	w.Header().Set("Idempotency-Replayed", "true")
	w.WriteHeader(entry.StatusCode)
	_, _ = w.Write(entry.Body)
}

// responseRecorder captures the status code and body without buffering headers
// from the underlying ResponseWriter twice.
type responseRecorder struct {
	http.ResponseWriter
	buf        *bytes.Buffer
	statusCode int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	// Write to the underlying ResponseWriter first; this is the client-observable path.
	n, err := r.ResponseWriter.Write(b)
	// Mirror only successfully written bytes to the capture buffer.
	if n > 0 {
		r.buf.Write(b[:n])
	}
	return n, err
}

func captureHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}
