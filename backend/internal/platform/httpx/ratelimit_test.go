package httpx_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/httpx"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func ipRequest(remoteAddr string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/menu", nil)
	req.RemoteAddr = remoteAddr
	return req
}

func TestPublicIPRateLimit_AllowsUpToLimitThen429(t *testing.T) {
	cache := newTestCache(t)
	h := httpx.PublicIPRateLimit(cache, httpx.RateLimitConfig{Limit: 3, Window: time.Minute})(okHandler())

	for i := range 3 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, ipRequest("203.0.113.9:1234"))
		require.Equalf(t, http.StatusOK, rec.Code, "request %d must be admitted", i+1)
		assert.Equal(t, "3", rec.Header().Get("X-RateLimit-Limit"))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, ipRequest("203.0.113.9:1234"))

	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	assert.Equal(t, "0", rec.Header().Get("X-RateLimit-Remaining"))
	assert.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))

	var problem struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	assert.Equal(t, http.StatusTooManyRequests, problem.Status)
	assert.Contains(t, problem.Type, "rate-limit-exceeded")
}

func TestPublicIPRateLimit_BucketsPerAddress(t *testing.T) {
	cache := newTestCache(t)
	h := httpx.PublicIPRateLimit(cache, httpx.RateLimitConfig{Limit: 1, Window: time.Minute})(okHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, ipRequest("198.51.100.1:9000"))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, ipRequest("198.51.100.1:9001"))
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "same host, different source port is the same caller")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, ipRequest("198.51.100.2:9000"))
	assert.Equal(t, http.StatusOK, rec.Code, "a different address must have its own budget")
}

// TestPublicIPRateLimit_WindowSlides is the property that separates a sliding
// window log from the fixed window ADR-OPS-003 rejected: budget is returned
// gradually as individual entries age out, not all at once at a boundary.
func TestPublicIPRateLimit_WindowSlides(t *testing.T) {
	mr := miniredis.RunT(t)
	cache := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	h := httpx.PublicIPRateLimit(cache, httpx.RateLimitConfig{Limit: 2, Window: time.Minute})(okHandler())

	for range 2 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, ipRequest("203.0.113.10:1234"))
		require.Equal(t, http.StatusOK, rec.Code)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, ipRequest("203.0.113.10:1234"))
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	// Advancing past the window ages both entries out; the caller is admitted
	// again without any key having expired as a whole.
	mr.FastForward(61 * time.Second)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, ipRequest("203.0.113.10:1234"))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestPublicIPRateLimit_RedisDown_FailsClosedWith503(t *testing.T) {
	// Address with no listener: every command errors immediately.
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: time.Millisecond})
	handlerRan := false
	h := httpx.PublicIPRateLimit(cache, httpx.RateLimitConfig{Limit: 10, Window: time.Minute})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			handlerRan = true
			w.WriteHeader(http.StatusOK)
		}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, ipRequest("203.0.113.11:1234"))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"an unreachable limiter must fail closed on a public surface, not wave traffic through")
	assert.False(t, handlerRan, "the wrapped handler must not run when the limiter cannot decide")
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
}

func guestRequest(t *testing.T, tenantID, sessionID uuid.UUID) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/orders", nil)
	return req.WithContext(auth.WithGuestSession(req.Context(), auth.GuestSession{
		TenantID:  tenantID,
		BranchID:  uuid.New(),
		TableID:   uuid.New(),
		QRCodeID:  uuid.New(),
		SessionID: sessionID,
		ExpiresAt: time.Now().Add(time.Hour),
	}))
}

func TestGuestRateLimit_PerSessionBudget(t *testing.T) {
	cache := newTestCache(t)
	h := httpx.GuestRateLimit(cache, httpx.RateLimitConfig{Limit: 1, Window: time.Minute})(okHandler())

	tenantID := uuid.New()
	sessionA, sessionB := uuid.New(), uuid.New()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, guestRequest(t, tenantID, sessionA))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, guestRequest(t, tenantID, sessionA))
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, guestRequest(t, tenantID, sessionB))
	assert.Equal(t, http.StatusOK, rec.Code, "a second diner must not inherit another session's exhausted budget")
}

func TestGuestRateLimit_NoSession_Returns401(t *testing.T) {
	cache := newTestCache(t)
	handlerRan := false
	h := httpx.GuestRateLimit(cache, httpx.RateLimitConfig{Limit: 10, Window: time.Minute})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			handlerRan = true
			w.WriteHeader(http.StatusOK)
		}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/orders", nil))

	assert.Equal(t, http.StatusUnauthorized, rec.Code,
		"without a session there is nothing to key on; a shared fallback bucket would be drainable by anyone")
	assert.False(t, handlerRan)
}
