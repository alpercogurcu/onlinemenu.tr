package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/modules/payment/service"
	"onlinemenu.tr/internal/platform/auth"
)

// Well-known system role ids from migrations/identity/000006_seed_system_roles.
const (
	cashierRoleID = "00000001-0000-0000-0000-000000000001"
	managerRoleID = "00000001-0000-0000-0000-000000000006"
)

// scopedCtx runs the real RequirePermission middleware for the fiscal status
// action and returns the context it produced, so the test observes the scope
// authz.rego actually resolves rather than a hand-planted string. A denied
// principal never reaches the handler, which the returned bool reports.
func scopedCtx(t *testing.T, principal auth.Principal) (context.Context, bool) {
	t.Helper()
	engine, err := auth.NewEngine(
		auth.EngineConfig{BundlePath: "../../../../configs/opa/bundles"},
		redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1}),
		zap.NewNop(),
	)
	require.NoError(t, err)

	var (
		captured context.Context
		reached  bool
	)
	handler := auth.RequirePermission(engine, "payment.fiscal_status.read")(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			captured, reached = r.Context(), true
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/fiscal-pending", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return captured, reached
}

func staffPrincipal(branchID uuid.UUID, roleIDs ...string) auth.Principal {
	ids := make([]uuid.UUID, len(roleIDs))
	for i, r := range roleIDs {
		ids[i] = uuid.MustParse(r)
	}
	return auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: branchID,
		RoleIDs:  ids,
	}
}

// TestRequireBranch is ADR-AUTH-001 layer 3 for the branch-scoped fiscal
// status poll. RLS (layer 1) isolates tenants only, so a cashier of branch A
// reading branch B of the SAME chain is stopped here or nowhere.
func TestRequireBranch(t *testing.T) {
	ownBranch := uuid.New()
	otherBranch := uuid.New()

	tests := []struct {
		name        string
		principal   auth.Principal
		target      uuid.UUID
		withMwScope bool
		wantErr     error
	}{
		{
			name:        "cashier reading its own branch",
			principal:   staffPrincipal(ownBranch, cashierRoleID),
			target:      ownBranch,
			withMwScope: true,
		},
		{
			name:        "cashier reading a sibling branch of the same chain",
			principal:   staffPrincipal(ownBranch, cashierRoleID),
			target:      otherBranch,
			withMwScope: true,
			wantErr:     pub.ErrBranchForbidden,
		},
		{
			// Tenant scope is checked BEFORE the branch match, so a manager
			// whose membership happens to name one branch is not rejected on a
			// coincidental mismatch with the branch being read.
			name:        "manager (tenant scope) reading another branch",
			principal:   staffPrincipal(ownBranch, managerRoleID),
			target:      otherBranch,
			withMwScope: true,
		},
		{
			// Stricter than platform HasBranchAccess on purpose: a nil BranchID
			// alone does not open every branch. memberships.branch_id is
			// nullable with no constraint keeping a counter role branch-bound,
			// so a mis-provisioned chain-wide cashier must not be able to watch
			// the whole chain's money. Real chain-wide staff hold manager and
			// pass via tenant scope (case above).
			name:        "chain-wide cashier (nil branch) is refused without tenant scope",
			principal:   staffPrincipal(uuid.Nil, cashierRoleID),
			target:      otherBranch,
			withMwScope: true,
			wantErr:     pub.ErrBranchForbidden,
		},
		{
			name:        "branch-scoped principal cannot read the nil branch either",
			principal:   staffPrincipal(uuid.Nil, cashierRoleID),
			target:      uuid.Nil,
			withMwScope: true,
			wantErr:     pub.ErrBranchForbidden,
		},
		{
			// No RequirePermission ran, so no scope is in ctx: the check must
			// fall back to the principal rather than fail open.
			name:      "no scope in context falls back to the principal",
			principal: staffPrincipal(ownBranch, cashierRoleID),
			target:    otherBranch,
			wantErr:   pub.ErrBranchForbidden,
		},
		{
			name:      "customer principal has no branch context at all",
			principal: auth.Principal{PersonID: uuid.New(), Ctx: auth.ContextCustomer},
			target:    ownBranch,
			wantErr:   pub.ErrBranchForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.withMwScope {
				scoped, reached := scopedCtx(t, tt.principal)
				require.True(t, reached, "principal must pass OPA before layer 3 is reachable")
				ctx = scoped
			}

			err := service.RequireBranchForTest(ctx, tt.principal, tt.target)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}
