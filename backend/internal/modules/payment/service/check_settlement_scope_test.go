package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/service"
)

// TestBranchScopeFilter is ADR-AUTH-001 layer 3 for the per-check settlement
// read. It is the counterpart to TestRequireBranch: that endpoint takes a
// client-supplied branch_id and REJECTS a mismatch, this one takes only a
// check_id and FILTERS instead — rejecting would confirm the check id exists in
// another branch of the chain.
//
// The repo tests prove the SQL honours whatever filter it is handed; this
// proves the right filter is handed to it. Nothing else exercises that
// decision, and getting it wrong in the permissive direction (nil for a
// cashier) hands every cashier the whole chain's money with no test failing.
//
// The scope comes from the real OPA engine via scopedCtx, not a hand-planted
// string, so a change to authz.rego's scope rule surfaces here.
func TestBranchScopeFilter(t *testing.T) {
	ownBranch := uuid.New()

	t.Run("cashier is pinned to their own branch", func(t *testing.T) {
		p := staffPrincipal(ownBranch, cashierRoleID)
		ctx, reached := scopedCtx(t, p)
		require.True(t, reached, "cashier must be allowed the fiscal status action")

		got := service.BranchScopeFilterForTest(ctx, p)

		require.NotNil(t, got, "a nil filter means every branch — the leak this guards")
		assert.Equal(t, ownBranch, *got)
	})

	t.Run("manager sees the whole tenant", func(t *testing.T) {
		p := staffPrincipal(ownBranch, managerRoleID)
		ctx, reached := scopedCtx(t, p)
		require.True(t, reached)

		assert.Nil(t, service.BranchScopeFilterForTest(ctx, p),
			"tenant scope must drop the branch predicate so a manager can reconcile across branches")
	})

	t.Run("missing scope fails closed to the principal's branch", func(t *testing.T) {
		// No OPA decision in ctx at all — the shape a future caller would
		// produce by invoking the service outside the permission middleware.
		// The safe default is the narrow one; widening to tenant here would
		// turn any such caller into a chain-wide money read.
		p := staffPrincipal(ownBranch, cashierRoleID)

		got := service.BranchScopeFilterForTest(context.Background(), p)

		require.NotNil(t, got, "absent scope must not be read as tenant scope")
		assert.Equal(t, ownBranch, *got)
	})

	t.Run("branch-scoped principal without a branch matches nothing", func(t *testing.T) {
		// auth.Principal.HasBranchAccess reads a nil BranchID as "every
		// branch", which is right for a chain owner but unsafe here:
		// memberships.branch_id is nullable with nothing tying a branch-scoped
		// system role (cashier) to a non-null branch. A mis-provisioned
		// chain-wide cashier must read as an unpaid check, not as every
		// branch's money.
		p := staffPrincipal(uuid.Nil, cashierRoleID)
		ctx, reached := scopedCtx(t, p)
		require.True(t, reached)

		got := service.BranchScopeFilterForTest(ctx, p)

		require.NotNil(t, got, "must stay a filter on uuid.Nil, which matches no NOT NULL branch_id")
		assert.Equal(t, uuid.Nil, *got)
	})
}
