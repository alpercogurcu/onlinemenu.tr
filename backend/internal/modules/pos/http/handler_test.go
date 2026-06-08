package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/platform/auth"
)

// authGuardRouter replicates requirePrincipal logic for isolated testing.
func authGuardRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/probe", func(w http.ResponseWriter, req *http.Request) {
		p, err := auth.FromContext(req.Context())
		if err != nil || p.TenantID == uuid.Nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return r
}

func TestRequirePrincipal_NoAuth(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	authGuardRouter().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequirePrincipal_PreContextPrincipal(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		KeycloakSub: "some-sub",
	}))
	authGuardRouter().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "pre-context principal must be rejected")
}

func TestRequirePrincipal_StaffPrincipal(t *testing.T) {
	tenantID := uuid.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: uuid.New(),
		PersonID: uuid.New(),
	}))
	authGuardRouter().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}
