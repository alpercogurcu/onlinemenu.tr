package httpx_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/platform/auth"
	"onlinemenu.tr/internal/platform/httpx"
)

// newTestCache spins up an in-process miniredis server so the middleware can
// be unit tested without a real Redis dependency.
func newTestCache(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

// countingHandler echoes the request body and counts how many times it ran,
// so tests can assert whether the wrapped handler was re-executed or the
// middleware served a cached/rejected response instead.
type countingHandler struct {
	calls int
}

func (h *countingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls++
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(body)
}

func newTenantRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		Ctx:      auth.ContextStaff,
		TenantID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		BranchID: uuid.New(),
		PersonID: uuid.New(),
	}))
	return req
}

func newRouter(cache *redis.Client, h http.Handler) http.Handler {
	r := chi.NewRouter()
	r.With(httpx.Idempotency(cache)).Post("/orders", h.ServeHTTP)
	return r
}

func TestIdempotency_MissingKey_Returns422(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newRouter(cache, h)

	req := newTenantRequest(t, http.MethodPost, "/orders", `{"a":1}`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, 0, h.calls)
}

func TestIdempotency_SameKeySameBody_ReplaysWithoutReexecuting(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newRouter(cache, h)

	body := `{"amount":100}`
	req1 := newTenantRequest(t, http.MethodPost, "/orders", body)
	req1.Header.Set("Idempotency-Key", "key-1")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusCreated, rec1.Code)
	require.Equal(t, 1, h.calls)
	assert.Equal(t, body, rec1.Body.String())

	req2 := newTenantRequest(t, http.MethodPost, "/orders", body)
	req2.Header.Set("Idempotency-Key", "key-1")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusCreated, rec2.Code)
	assert.Equal(t, body, rec2.Body.String())
	assert.Equal(t, "true", rec2.Header().Get("Idempotency-Replayed"))
	assert.Equal(t, 1, h.calls, "handler must not be re-executed on an identical retry")
}

func TestIdempotency_SameKeyDifferentBody_Returns422WithoutReexecuting(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newRouter(cache, h)

	req1 := newTenantRequest(t, http.MethodPost, "/orders", `{"amount":100}`)
	req1.Header.Set("Idempotency-Key", "key-2")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusCreated, rec1.Code)
	require.Equal(t, 1, h.calls)

	req2 := newTenantRequest(t, http.MethodPost, "/orders", `{"amount":999}`)
	req2.Header.Set("Idempotency-Key", "key-2")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusUnprocessableEntity, rec2.Code,
		"reusing a key with a different body must be rejected, not replayed or re-executed")
	assert.Equal(t, 1, h.calls, "handler must not run again for a body-mismatched key reuse")
}

func TestIdempotency_DifferentKeysSameBody_BothExecute(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newRouter(cache, h)

	body := `{"amount":100}`

	req1 := newTenantRequest(t, http.MethodPost, "/orders", body)
	req1.Header.Set("Idempotency-Key", "key-a")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusCreated, rec1.Code)

	req2 := newTenantRequest(t, http.MethodPost, "/orders", body)
	req2.Header.Set("Idempotency-Key", "key-b")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusCreated, rec2.Code)

	assert.Equal(t, 2, h.calls, "distinct keys must each execute independently, even with identical bodies")
}

func TestIdempotency_ConcurrentInFlight_SecondCallerGets409(t *testing.T) {
	cache := newTestCache(t)

	release := make(chan struct{})
	started := make(chan struct{})
	blockingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	})
	router := newRouter(cache, blockingHandler)

	body := `{"amount":100}`
	req1 := newTenantRequest(t, http.MethodPost, "/orders", body)
	req1.Header.Set("Idempotency-Key", "key-inflight")
	rec1 := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		router.ServeHTTP(rec1, req1)
		close(done)
	}()
	<-started

	req2 := newTenantRequest(t, http.MethodPost, "/orders", body)
	req2.Header.Set("Idempotency-Key", "key-inflight")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusConflict, rec2.Code, "a concurrent duplicate must be rejected while the first is in-flight")

	close(release)
	<-done
	assert.Equal(t, http.StatusCreated, rec1.Code)
}

func TestIdempotency_BodyRestoredForHandler(t *testing.T) {
	cache := newTestCache(t)
	var seenBody string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		seenBody = string(b)
		w.WriteHeader(http.StatusCreated)
	})
	router := newRouter(cache, handler)

	req := newTenantRequest(t, http.MethodPost, "/orders", `{"hello":"world"}`)
	req.Header.Set("Idempotency-Key", "key-body-restore")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.JSONEq(t, `{"hello":"world"}`, seenBody, "middleware must restore the body after hashing it")
}

// TestIdempotency_LegacyEntryWithoutBodyHash_StillReplays guards backward
// compatibility for entries written before the body_hash field existed: any
// such entry surviving the 24h TTL at deploy time must still replay on a
// same-key/same-body retry rather than being spuriously rejected as a
// body mismatch (an empty BodyHash must not be compared as a real hash).
func TestIdempotency_LegacyEntryWithoutBodyHash_StillReplays(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newRouter(cache, h)

	tenantID := "11111111-1111-1111-1111-111111111111"
	key := "legacy-key"
	cacheKey := "idem:" + tenantID + ":" + key

	legacyEntry := `{"status_code":201,"headers":{},"body":"eyJhbW91bnQiOjEwMH0="}`
	require.NoError(t, cache.Set(context.Background(), cacheKey, legacyEntry, 0).Err())

	req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{"amount":100}`))
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		Ctx:      auth.ContextStaff,
		TenantID: uuid.MustParse(tenantID),
		PersonID: uuid.New(),
	}))
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code, "a legacy entry (no body_hash) must replay, not 422")
	assert.Equal(t, `{"amount":100}`, rec.Body.String())
	assert.Equal(t, 0, h.calls, "a replayed legacy entry must not re-execute the handler")
}

func TestIdempotency_NoPrincipal_Returns401(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newRouter(cache, h)

	req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{}`))
	req.Header.Set("Idempotency-Key", "key-no-principal")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, 0, h.calls)
}

// ---------------------------------------------------------------------------
// Guest scope (ADR-ARCH-006 §5): the storefront's anonymous diners reuse the
// same middleware through IdempotencyWithScope, so these cases assert the
// scope swap did not weaken any ADR-SEC-003 guarantee.
// ---------------------------------------------------------------------------

func newGuestRouter(cache *redis.Client, h http.Handler) http.Handler {
	r := chi.NewRouter()
	r.With(httpx.IdempotencyWithScope(cache, httpx.GuestSessionScope)).Post("/orders", h.ServeHTTP)
	return r
}

func newGuestOrderRequest(t *testing.T, session auth.GuestSession, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(body))
	return req.WithContext(auth.WithGuestSession(req.Context(), session))
}

func newGuestSession(tenantID uuid.UUID) auth.GuestSession {
	return auth.GuestSession{
		TenantID:  tenantID,
		BranchID:  uuid.New(),
		TableID:   uuid.New(),
		QRCodeID:  uuid.New(),
		SessionID: uuid.New(),
	}
}

func TestIdempotency_GuestScope_SameKeySameBody_Replays(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newGuestRouter(cache, h)

	session := newGuestSession(uuid.New())
	body := `{"lines":[{"product_id":"p","quantity":1}]}`

	req1 := newGuestOrderRequest(t, session, body)
	req1.Header.Set("Idempotency-Key", "guest-key-1")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusCreated, rec1.Code)
	require.Equal(t, 1, h.calls)

	req2 := newGuestOrderRequest(t, session, body)
	req2.Header.Set("Idempotency-Key", "guest-key-1")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusCreated, rec2.Code)
	assert.Equal(t, "true", rec2.Header().Get("Idempotency-Replayed"))
	assert.Equal(t, 1, h.calls, "a retried cart submission must not place a second order")
}

func TestIdempotency_GuestScope_SameKeyDifferentBody_Returns422(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newGuestRouter(cache, h)

	session := newGuestSession(uuid.New())

	req1 := newGuestOrderRequest(t, session, `{"lines":[{"quantity":1}]}`)
	req1.Header.Set("Idempotency-Key", "guest-key-2")
	router.ServeHTTP(httptest.NewRecorder(), req1)
	require.Equal(t, 1, h.calls)

	req2 := newGuestOrderRequest(t, session, `{"lines":[{"quantity":99}]}`)
	req2.Header.Set("Idempotency-Key", "guest-key-2")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusUnprocessableEntity, rec2.Code)
	assert.Equal(t, 1, h.calls)
}

// TestIdempotency_GuestScope_TwoSessionsSameKey_BothExecute is the collision
// case guest scoping exists for: two phones at (potentially) the same table
// generate keys independently, so an overlap must not swallow one order.
func TestIdempotency_GuestScope_TwoSessionsSameKey_BothExecute(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newGuestRouter(cache, h)

	tenantID := uuid.New()
	sessionA := newGuestSession(tenantID)
	sessionB := newGuestSession(tenantID)
	sessionB.QRCodeID = sessionA.QRCodeID
	sessionB.TableID = sessionA.TableID
	body := `{"lines":[{"quantity":1}]}`

	reqA := newGuestOrderRequest(t, sessionA, body)
	reqA.Header.Set("Idempotency-Key", "shared-key")
	recA := httptest.NewRecorder()
	router.ServeHTTP(recA, reqA)
	require.Equal(t, http.StatusCreated, recA.Code)

	reqB := newGuestOrderRequest(t, sessionB, body)
	reqB.Header.Set("Idempotency-Key", "shared-key")
	recB := httptest.NewRecorder()
	router.ServeHTTP(recB, reqB)

	assert.Equal(t, http.StatusCreated, recB.Code)
	assert.Empty(t, recB.Header().Get("Idempotency-Replayed"))
	assert.Equal(t, 2, h.calls, "two diners at one table are two callers, not one retrying caller")
}

func TestIdempotency_GuestScope_TwoTenantsSameKey_DoNotCollide(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newGuestRouter(cache, h)

	body := `{"lines":[{"quantity":1}]}`

	for range 2 {
		req := newGuestOrderRequest(t, newGuestSession(uuid.New()), body)
		req.Header.Set("Idempotency-Key", "cross-tenant-key")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code)
	}
	assert.Equal(t, 2, h.calls)
}

// TestIdempotency_GuestScope_StaffPrincipalOnly_Returns401 proves the guest
// scope cannot be satisfied by a staff token: the two identities live under
// different context keys and do not convert.
func TestIdempotency_GuestScope_StaffPrincipalOnly_Returns401(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newGuestRouter(cache, h)

	req := newTenantRequest(t, http.MethodPost, "/orders", `{}`)
	req.Header.Set("Idempotency-Key", "staff-on-guest-route")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, 0, h.calls)
}

// TestIdempotency_StaffScope_GuestSessionOnly_Returns401 is the mirror image:
// a guest session must not satisfy a staff-scoped idempotency guard either.
func TestIdempotency_StaffScope_GuestSessionOnly_Returns401(t *testing.T) {
	cache := newTestCache(t)
	h := &countingHandler{}
	router := newRouter(cache, h)

	req := newGuestOrderRequest(t, newGuestSession(uuid.New()), `{}`)
	req.Header.Set("Idempotency-Key", "guest-on-staff-route")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, 0, h.calls)
}

// Sanity: context.Background() is used for the async cache writes in the
// middleware, so this test only needs to confirm no goroutine/context panic
// occurs when the request context is cancelled immediately after the
// handler returns (a common client-disconnect scenario).
func TestIdempotency_CancelledRequestContext_DoesNotPanic(t *testing.T) {
	cache := newTestCache(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	router := newRouter(cache, handler)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{}`)).WithContext(ctx)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		PersonID: uuid.New(),
	}))
	req.Header.Set("Idempotency-Key", "key-cancel")
	cancel()

	rec := httptest.NewRecorder()
	assert.NotPanics(t, func() {
		router.ServeHTTP(rec, req)
	})
}
