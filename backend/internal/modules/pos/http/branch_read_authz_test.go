package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	poshttp "onlinemenu.tr/internal/modules/pos/http"
	"onlinemenu.tr/internal/platform/auth"
)

// Regression for the 2026-09-20 branch-isolation finding on the READ side:
// GET /api/v1/pos/checks answered a branch B cashier with every open adisyon
// in the chain, and ?branch_id=A handed them branch A's outright. The handler
// documented branch_id as "narrowing, not restricting" — RLS bounds the read
// to the tenant, and nothing bounded it to the branch.
//
// Only the explicit ?branch_id path can be asserted here: that guard runs
// before the (nil) CheckService, so the verdict is the response. The forced
// filter for a caller who names no branch is a service argument, covered by
// TestBranchScopeFilter in pos/service.
//
// CheckService is deliberately nil: getting past the guard panics on it and
// recoverMiddleware turns that into a 500, so "not 403" reads as exactly "the
// branch guard let it through" — same technique as order_cancel_authz_test.go.
func newCheckReadMux(t *testing.T) *chi.Mux {
	t.Helper()
	engine := newSmokeTestEngine(t)
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1})
	hwc := poshttp.NewHandler(poshttp.Params{Logger: zap.NewNop(), Engine: engine, Cache: cache})
	mux := chi.NewMux()
	mux.Use(recoverMiddleware)
	hwc.RegisterRoutes(mux)
	return mux
}

func branchScopedPrincipal(roleID, branchID uuid.UUID) auth.Principal {
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.MustParse("aaaaaaaa-0000-0000-0000-00000000aaaa"),
		BranchID: branchID,
		RoleIDs:  []uuid.UUID{roleID},
	}
}

func getAs(mux http.Handler, p auth.Principal, path string) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

func TestListChecks_ForeignBranchFilterRefused(t *testing.T) {
	mux := newCheckReadMux(t)
	branchA := uuid.MustParse("11111111-0000-0000-0000-0000000000aa")
	branchB := uuid.MustParse("22222222-0000-0000-0000-0000000000bb")

	for _, role := range []struct {
		name string
		id   uuid.UUID
	}{
		{"cashier", tablePolicyCashierID},
		{"waiter", tablePolicyWaiterID},
		{"kitchen", tablePolicyKitchenID},
	} {
		code := getAs(mux, branchScopedPrincipal(role.id, branchB), "/api/v1/pos/checks?branch_id="+branchA.String())
		assert.Equalf(t, http.StatusForbidden, code,
			"%s of branch B must not list branch A's checks (got %d)", role.name, code)
	}
}

func TestListChecks_OwnBranchAndChainManagerPassTheGuard(t *testing.T) {
	branchA := uuid.MustParse("11111111-0000-0000-0000-0000000000aa")
	branchB := uuid.MustParse("22222222-0000-0000-0000-0000000000bb")

	tests := []struct {
		name   string
		roleID uuid.UUID
		query  string
	}{
		{
			name:   "cashier names their own branch",
			roleID: tablePolicyCashierID,
			query:  "?branch_id=" + branchB.String(),
		},
		{
			name:   "cashier names no branch at all",
			roleID: tablePolicyCashierID,
			query:  "",
		},
		{
			// OPA scope "tenant": a chain manager reviews every branch, and
			// the day-end flow depends on it (reports, e2e manager specs).
			name:   "chain manager names another branch",
			roleID: tablePolicyManagerID,
			query:  "?branch_id=" + branchA.String(),
		},
		{
			name:   "chain manager names no branch",
			roleID: tablePolicyManagerID,
			query:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := newCheckReadMux(t)
			code := getAs(mux, branchScopedPrincipal(tt.roleID, branchB), "/api/v1/pos/checks"+tt.query)
			assert.NotEqual(t, http.StatusForbidden, code,
				"the branch guard must not refuse this caller (got %d)", code)
		})
	}
}
