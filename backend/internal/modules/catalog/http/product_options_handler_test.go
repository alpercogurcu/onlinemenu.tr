package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	cataloghttp "onlinemenu.tr/internal/modules/catalog/http"
	"onlinemenu.tr/internal/platform/auth"
)

// GET /catalog/products/modifier-groups rejects before touching the service
// (no ModifierService is wired here — reaching it would panic): a malformed
// branch_id is a 400 and a branch-bound principal aiming at another branch is
// a 403. The 400 body also proves chi routed the static segment to this
// handler rather than to GET /products/{id}, whose bad-id answer differs.
func productOptionsRequest(t *testing.T, principal auth.Principal, query string) *httptest.ResponseRecorder {
	t.Helper()
	h := cataloghttp.NewHandler(cataloghttp.Params{Logger: zap.NewNop(), Engine: newSmokeTestEngine(t)})
	mux := chi.NewMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/products/modifier-groups"+query, nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestListProductOptions_MalformedBranchIs400(t *testing.T) {
	rec := productOptionsRequest(t, staffPrincipal(uuid.New(), cashierRoleID), "?branch_id=not-a-uuid")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.True(t, strings.Contains(rec.Body.String(), "invalid branch_id"), rec.Body.String())
}

func TestListProductOptions_CashierCannotReadAnotherBranch(t *testing.T) {
	rec := productOptionsRequest(t, staffPrincipal(uuid.New(), cashierRoleID), "?branch_id="+uuid.NewString())
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
