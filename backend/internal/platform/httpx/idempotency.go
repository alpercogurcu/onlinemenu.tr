// Package httpx provides HTTP middleware components shared across all chi routers.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
}

// Idempotency returns a chi middleware that enforces ADR-SEC-003.
//
// On the first request with a given Idempotency-Key it acquires a short-lived
// Redis lock, executes the handler, then records the response for 24 hours.
// Concurrent duplicate requests (same key, same tenant) receive 409 Conflict
// while the first request is in-flight. Subsequent retries receive the cached
// response without re-executing the handler.
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

			cacheKey := fmt.Sprintf("%s%s:%s", idempotencyCachePrefix, principal.TenantID, key)
			lockKey := cacheKey + idempotencyLockSuffix

			// Return a previously cached response if one exists.
			if replayed := tryReplay(r.Context(), cache, cacheKey, w); replayed {
				return
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
				}
				if data, marshalErr := json.Marshal(entry); marshalErr == nil {
					_ = cache.Set(context.Background(), cacheKey, data, idempotencyTTL).Err()
				}
			}
		})
	}
}

// tryReplay checks Redis for a cached response and writes it to w.
// Returns true if a cached response was found and written.
func tryReplay(ctx context.Context, cache *redis.Client, cacheKey string, w http.ResponseWriter) bool {
	existing, err := cache.Get(ctx, cacheKey).Result()
	if err != nil {
		return false
	}
	var entry idempotencyEntry
	if err := json.Unmarshal([]byte(existing), &entry); err != nil {
		return false
	}
	for h, v := range entry.Headers {
		w.Header().Set(h, v)
	}
	w.Header().Set("Idempotency-Replayed", "true")
	w.WriteHeader(entry.StatusCode)
	_, _ = w.Write(entry.Body)
	return true
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
