package http_test

// Branch-settings endpoint wiring tests (SEC-005 + OPA layer): the services
// are deliberately nil, so getting PAST a guard panics and recoverMiddleware
// turns that into a 500 — "not 403" reads as exactly "the guard let it
// through". Same technique as branch_read_authz_test.go. Value semantics
// (defaults, partial upsert) are covered by the pos/service integration tests.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	poshttp "onlinemenu.tr/internal/modules/pos/http"
	"onlinemenu.tr/internal/platform/auth"
)

func newBranchSettingsMux(t *testing.T) *chi.Mux {
	t.Helper()
	engine := newSmokeTestEngine(t)
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1})
	hwc := poshttp.NewHandler(poshttp.Params{Logger: zap.NewNop(), Engine: engine, Cache: cache})
	mux := chi.NewMux()
	mux.Use(recoverMiddleware)
	hwc.RegisterRoutes(mux)
	return mux
}

func putAs(mux http.Handler, p auth.Principal, path, body string) int {
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

// TestBranchSettings_Get_BranchScope: a branch-scoped reader (waiter — the
// screen this endpoint exists for) may only read its own branch's settings; a
// foreign branch is 403 before any service/database work.
func TestBranchSettings_Get_BranchScope(t *testing.T) {
	mux := newBranchSettingsMux(t)
	ownBranch := uuid.New()

	code := getAs(mux, branchScopedPrincipal(tablePolicyWaiterID, ownBranch),
		"/api/v1/pos/branch-settings?branch_id="+uuid.NewString())
	assert.Equal(t, http.StatusForbidden, code, "a waiter naming a foreign branch must be refused")

	code = getAs(mux, branchScopedPrincipal(tablePolicyWaiterID, ownBranch),
		"/api/v1/pos/branch-settings?branch_id="+ownBranch.String())
	assert.Equal(t, http.StatusInternalServerError, code,
		"own branch must pass the guard (nil service panic = guard passed)")

	code = getAs(mux, branchScopedPrincipal(tablePolicyKitchenID, ownBranch),
		"/api/v1/pos/branch-settings?branch_id="+ownBranch.String())
	assert.Equal(t, http.StatusForbidden, code,
		"kitchen holds no pos.check.read today — pin it so widening the grant is an explicit OPA decision")

	code = getAs(mux, branchScopedPrincipal(tablePolicyWaiterID, ownBranch),
		"/api/v1/pos/branch-settings?branch_id=not-a-uuid")
	assert.Equal(t, http.StatusUnprocessableEntity, code)
}

// TestBranchSettings_Put_RequiresManageAndBranch: writing is management-only
// (pos.table.manage), and even a shift manager is pinned to their own branch.
func TestBranchSettings_Put_RequiresManageAndBranch(t *testing.T) {
	mux := newBranchSettingsMux(t)
	ownBranch := uuid.New()

	code := putAs(mux, branchScopedPrincipal(tablePolicyCashierID, ownBranch),
		"/api/v1/pos/branch-settings", `{"branch_id":"`+ownBranch.String()+`","order_flow":"simple"}`)
	assert.Equal(t, http.StatusForbidden, code, "a cashier must not reconfigure the branch")

	code = putAs(mux, branchScopedPrincipal(tablePolicyShiftManagerID, ownBranch),
		"/api/v1/pos/branch-settings", `{"branch_id":"`+uuid.NewString()+`","order_flow":"simple"}`)
	assert.Equal(t, http.StatusForbidden, code, "a foreign branch must be refused before any write")

	code = putAs(mux, branchScopedPrincipal(tablePolicyShiftManagerID, ownBranch),
		"/api/v1/pos/branch-settings", `{"branch_id":"`+ownBranch.String()+`","order_flow":"simple"}`)
	assert.Equal(t, http.StatusInternalServerError, code,
		"own branch must pass both guards (nil service panic = guards passed)")

	code = putAs(mux, branchScopedPrincipal(tablePolicyShiftManagerID, ownBranch),
		"/api/v1/pos/branch-settings", `{"order_flow":"simple"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, code, "branch_id is required")
}
