package service_test

// This file isolates the OPA scope=="tenant" exemption path of requireBranch
// from the chain-wide-BranchID path exercised in branch_authz_test.go. It
// needs no database: it drives the real OPA engine (configs/opa/bundles)
// through auth.RequirePermission exactly as production HTTP middleware does,
// then calls the exposed requireBranch directly inside the next handler.
//
// The discriminating scenario: a "manager" principal whose OWN
// Principal.BranchID is set to some branch OTHER than the resource's branch
// (a branch-scoped manager membership is possible in the data model even
// though most managers are chain-wide) must still be exempt, because OPA
// resolves scope="tenant" for the manager role regardless of BranchID. If
// requireBranch only checked principal.HasBranchAccess (the chain-wide
// path), this exact case would wrongly be denied.
//
// Unlike inventory's branch_authz_scope_test.go, there is no
// "branch role must match resource branch" case here: configs/opa/bundles/
// authz.rego grants billing.* actions to the "manager" system role only
// (no seeded permission rows for any other role — see the rego's
// "back-office modules" comment). No non-manager principal can pass the
// billing.invoice.* permission check at all, so scope=="branch" is
// unreachable for this module in Faz 1. Fabricating a billing-specific
// branch role here would test a policy shape that does not exist in
// production; the requireBranch function itself is already exercised for
// that path by the inventory/pos suites (shared helper, same package
// contract).

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

	pub "onlinemenu.tr/internal/modules/billing/public"
	"onlinemenu.tr/internal/modules/billing/service"
	"onlinemenu.tr/internal/platform/auth"
)

// managerRoleID mirrors the well-known system role id in
// configs/opa/bundles/authz.rego (see billing/http/authz_smoke_test.go for
// the same policy bundle used against the HTTP-facing tests).
var managerRoleID = uuid.MustParse("00000001-0000-0000-0000-000000000006")

func newScopeTestEngine(t *testing.T) *auth.Engine {
	t.Helper()
	eng, err := auth.NewEngine(
		auth.EngineConfig{BundlePath: "../../../../configs/opa/bundles"},
		redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 1}),
		zap.NewNop(),
	)
	require.NoError(t, err)
	return eng
}

func TestRequireBranch_ManagerScopeExemptsEvenWithMismatchedPrincipalBranch(t *testing.T) {
	engine := newScopeTestEngine(t)
	tenantID := uuid.New()
	principalOwnBranch := uuid.New() // the manager's own membership branch
	resourceBranch := uuid.New()     // a DIFFERENT branch the invoice belongs to

	manager := auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: tenantID,
		BranchID: principalOwnBranch,
		RoleIDs:  []uuid.UUID{managerRoleID},
	}

	var gotErr error
	mw := auth.RequirePermission(engine, "billing.invoice.create")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotErr = service.RequireBranchForTest(r.Context(), manager, resourceBranch)
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), manager))
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "RequirePermission must allow the manager through to the handler")
	assert.NoError(t, gotErr, "manager's OPA scope=tenant must exempt even though principal.BranchID != resourceBranch")
}

// TestRequireBranch_NoScopeInContext_FallsBackToHasBranchAccess proves the
// requireBranch helper degrades gracefully (does not panic or silently
// allow) when called without going through auth.RequirePermission at all —
// e.g. a future direct-service-call caller that forgot the middleware. In
// that case only auth.Principal.HasBranchAccess governs.
func TestRequireBranch_NoScopeInContext_FallsBackToHasBranchAccess(t *testing.T) {
	own := uuid.New()
	other := uuid.New()
	p := auth.Principal{PersonID: uuid.New(), Ctx: auth.ContextStaff, TenantID: uuid.New(), BranchID: own, RoleIDs: []uuid.UUID{uuid.New()}}

	assert.NoError(t, service.RequireBranchForTest(context.Background(), p, own))
	assert.ErrorIs(t, service.RequireBranchForTest(context.Background(), p, other), pub.ErrBranchForbidden)
}
