// Package httpx provides HTTP middleware components shared across all chi routers.
package httpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// Idempotency returns a chi middleware that enforces ADR-SEC-003.
//
// On the first request with a given Idempotency-Key it acquires a short-lived
// Redis lock, executes the handler, then records the response — together with
// a hash of the request body — for 24 hours. Concurrent duplicate requests
// (same key, same tenant) receive 409 Conflict while the first request is
// in-flight. A subsequent retry with the same key AND the same body receives
// the cached response without re-executing the handler. A subsequent request
// reusing the same key with a DIFFERENT body is rejected with 422: an
// Idempotency-Key identifies one logical request, not a slot that can be
// silently repointed at different input.
//
// The cache key is scoped to the authenticated tenant to prevent cross-tenant
// key collisions in the multi-tenant environment.
//
// This middleware must be placed after auth.Middleware in the chain so that
// the principal is available in the request context.
func Idempotency(cache *redis.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(idempotencyHeader)
			if key == "" {
				http.Error(w, "Idempotency-Key header required", http.StatusUnprocessableEntity)
				return
			}

			principal, err := auth.FromContext(r.Context())
			if err != nil {
				// Auth middleware must precede idempotency middleware in the chain.
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

			cacheKey := fmt.Sprintf("%s%s:%s", idempotencyCachePrefix, principal.TenantID, key)
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
