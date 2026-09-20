package service_test

// BranchScopeFilter is requireBranch's counterpart for pos READS that take no
// usable branch_id: a list endpoint cannot answer "forbidden" when the caller
// named no branch, so it filters instead. This is the single line deciding
// whether GET /api/v1/pos/checks shows one branch's open adisyons or the whole
// chain's — the 2026-09-20 leak, where a branch B cashier saw every table in
// every branch because branch_id was documented as "narrowing, not
// restricting".
//
// Getting it wrong in the permissive direction (nil for a cashier) reopens
// that leak with no other test failing, so the scope is taken from the real
// OPA engine through auth.RequirePermission — exactly as production middleware
// does — rather than hand-planted, and a change to authz.rego's scope rule
// surfaces here.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/pos/service"
	"onlinemenu.tr/internal/platform/auth"
)

// scopedCtx runs the real permission middleware for action and returns the
// request context it produced — the only way to obtain a genuine OPA-derived
// scope, since platform/auth exposes no exported inverse of ScopeFromContext.
func scopedCtx(t *testing.T, p auth.Principal, action string) context.Context {
	t.Helper()
	engine := newScopeTestEngine(t)

	var got context.Context
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Context()
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	auth.RequirePermission(engine, action)(next).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "principal must be allowed %s for this test to mean anything", action)
	require.NotNil(t, got)
	return got
}

func TestBranchScopeFilter(t *testing.T) {
	ownBranch := uuid.New()

	staffAt := func(branchID uuid.UUID, roleID uuid.UUID) auth.Principal {
		return auth.Principal{
			PersonID: uuid.New(),
			Ctx:      auth.ContextStaff,
			TenantID: uuid.New(),
			BranchID: branchID,
			RoleIDs:  []uuid.UUID{roleID},
		}
	}

	t.Run("cashier is pinned to their own branch", func(t *testing.T) {
		p := staffAt(ownBranch, cashierRoleID)
		got := service.BranchScopeFilter(scopedCtx(t, p, "pos.check.read"), p)

		require.NotNil(t, got, "a nil filter means every branch — the leak this guards")
		assert.Equal(t, ownBranch, *got)
	})

	t.Run("chain manager sees every branch", func(t *testing.T) {
		p := staffAt(ownBranch, managerRoleID)
		assert.Nil(t, service.BranchScopeFilter(scopedCtx(t, p, "pos.check.read"), p),
			"OPA scope=tenant must stay unfiltered — the day-end report and the admin check list depend on it")
	})

	t.Run("no scope in context fails closed", func(t *testing.T) {
		// A caller that never went through auth.RequirePermission has no
		// scope, so it must land on its own branch, never on nil.
		p := staffAt(ownBranch, managerRoleID)
		got := service.BranchScopeFilter(context.Background(), p)

		require.NotNil(t, got, "a missing scope must not be read as chain-wide")
		assert.Equal(t, ownBranch, *got)
	})

	t.Run("branchless branch-scoped principal filters to nil uuid", func(t *testing.T) {
		// checks.branch_id is NOT NULL, so uuid.Nil matches no row. Failing
		// to an empty list is recoverable; auth.Principal.HasBranchAccess's
		// "nil means every branch" would be a chain-wide leak here.
		p := staffAt(uuid.Nil, cashierRoleID)
		got := service.BranchScopeFilter(scopedCtx(t, p, "pos.check.read"), p)

		require.NotNil(t, got)
		assert.Equal(t, uuid.Nil, *got)
	})
}
