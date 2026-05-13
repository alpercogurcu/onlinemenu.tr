package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/platform/auth"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	return &Handler{logger: zap.NewNop()}
}

func routerWithMiddleware(h *Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/{tenantID}", func(r chi.Router) {
		r.Use(h.tenantAccessMiddleware)
		r.Get("/test", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})
	return r
}

func TestTenantAccessMiddleware_ValidStaff(t *testing.T) {
	h := newTestHandler(t)
	tenantID := uuid.New()
	r := routerWithMiddleware(h)

	principal := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: uuid.New(),
		RoleIDs:  []uuid.UUID{uuid.New()},
	}
	req := httptest.NewRequest(http.MethodGet, "/"+tenantID.String()+"/test", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), principal))

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestTenantAccessMiddleware_RejectsUUIDNil(t *testing.T) {
	h := newTestHandler(t)
	r := routerWithMiddleware(h)

	// uuid.Nil in the URL path activates platform-admin RLS bypass — must be blocked.
	req := httptest.NewRequest(http.MethodGet, "/"+uuid.Nil.String()+"/test", nil)
	principal := auth.Principal{PersonID: uuid.New(), Ctx: auth.ContextStaff, TenantID: uuid.Nil, BranchID: uuid.New()}
	req = req.WithContext(auth.WithPrincipal(req.Context(), principal))

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestTenantAccessMiddleware_RejectsMismatchedTenant(t *testing.T) {
	h := newTestHandler(t)
	tenantA := uuid.New()
	tenantB := uuid.New()
	r := routerWithMiddleware(h)

	// Principal belongs to tenantA; request targets tenantB.
	req := httptest.NewRequest(http.MethodGet, "/"+tenantB.String()+"/test", nil)
	principal := auth.Principal{PersonID: uuid.New(), Ctx: auth.ContextStaff, TenantID: tenantA, BranchID: uuid.New()}
	req = req.WithContext(auth.WithPrincipal(req.Context(), principal))

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestTenantAccessMiddleware_RejectsCustomer(t *testing.T) {
	h := newTestHandler(t)
	tenantID := uuid.New()
	r := routerWithMiddleware(h)

	req := httptest.NewRequest(http.MethodGet, "/"+tenantID.String()+"/test", nil)
	customer := auth.Principal{PersonID: uuid.New(), Ctx: auth.ContextCustomer}
	req = req.WithContext(auth.WithPrincipal(req.Context(), customer))

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestTenantAccessMiddleware_RejectsPreContext(t *testing.T) {
	h := newTestHandler(t)
	tenantID := uuid.New()
	r := routerWithMiddleware(h)

	req := httptest.NewRequest(http.MethodGet, "/"+tenantID.String()+"/test", nil)
	pre := auth.Principal{KeycloakSub: "kc-sub-123"}
	req = req.WithContext(auth.WithPrincipal(req.Context(), pre))

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestTenantAccessMiddleware_MissingPrincipal(t *testing.T) {
	h := newTestHandler(t)
	tenantID := uuid.New()
	r := routerWithMiddleware(h)

	// No principal in context — as if auth middleware was bypassed.
	req := httptest.NewRequest(http.MethodGet, "/"+tenantID.String()+"/test", nil)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
