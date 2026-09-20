package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	cataloghttp "onlinemenu.tr/internal/modules/catalog/http"
	"onlinemenu.tr/internal/platform/auth"
)

var cashierRoleID = uuid.MustParse("00000001-0000-0000-0000-000000000001")

// resolveBranch runs GET /probe?<rawQuery> behind the real catalog.product.read
// permission gate, so the OPA scope that production sets on the context is the
// one branchIDFromQuery sees.
func resolveBranch(t *testing.T, principal auth.Principal, rawQuery string) (branchID uuid.UUID, status int) {
	t.Helper()
	h := cataloghttp.NewHandler(cataloghttp.Params{Logger: zap.NewNop(), Engine: newSmokeTestEngine(t)})

	mux := chi.NewMux()
	mux.With(auth.RequirePermission(newSmokeTestEngine(t), "catalog.product.read")).
		Get("/probe", func(w http.ResponseWriter, r *http.Request) {
			id, ok := h.BranchIDFromQuery(w, r)
			if !ok {
				return
			}
			branchID = id
			w.WriteHeader(http.StatusOK)
		})

	req := httptest.NewRequest(http.MethodGet, "/probe?"+rawQuery, nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return branchID, rec.Code
}

func staffPrincipal(branchID uuid.UUID, roleID uuid.UUID) auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{roleID},
	}
}

// A branch-bound principal that forgets ?branch_id= must be priced at its own
// branch, not at the tenant default — otherwise the POS shows a price the
// order endpoint then rejects with price_mismatch.
func TestBranchIDFromQuery_BranchScopedPrincipalDefaultsToOwnBranch(t *testing.T) {
	own := uuid.New()

	got, status := resolveBranch(t, staffPrincipal(own, cashierRoleID), "")

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, own, got)
}

func TestBranchIDFromQuery_ChainWideManagerKeepsTenantDefault(t *testing.T) {
	got, status := resolveBranch(t, staffPrincipal(uuid.New(), managerRoleID), "")

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, uuid.Nil, got)
}

func TestBranchIDFromQuery_ExplicitBranchWinsForManager(t *testing.T) {
	target := uuid.New()

	got, status := resolveBranch(t, staffPrincipal(uuid.New(), managerRoleID), "branch_id="+target.String())

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, target, got)
}

func TestBranchIDFromQuery_ExplicitOwnBranchAllowedForCashier(t *testing.T) {
	own := uuid.New()

	got, status := resolveBranch(t, staffPrincipal(own, cashierRoleID), "branch_id="+own.String())

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, own, got)
}

func TestBranchIDFromQuery_CashierCannotAimAtAnotherBranch(t *testing.T) {
	_, status := resolveBranch(t, staffPrincipal(uuid.New(), cashierRoleID), "branch_id="+uuid.NewString())

	assert.Equal(t, http.StatusForbidden, status)
}

// A staff principal without a branch has nothing to default to; the tenant
// default (the pre-DATA-009 behaviour) is the only sane answer.
func TestBranchIDFromQuery_BranchlessPrincipalFallsBackToTenantDefault(t *testing.T) {
	got, status := resolveBranch(t, staffPrincipal(uuid.Nil, cashierRoleID), "")

	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, uuid.Nil, got)
}
