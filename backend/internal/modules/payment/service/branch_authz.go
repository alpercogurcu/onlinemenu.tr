package service

import (
	"context"

	"github.com/google/uuid"

	pub "onlinemenu.tr/internal/modules/payment/public"
	"onlinemenu.tr/internal/platform/auth"
)

// requireBranch enforces ADR-AUTH-001 layer 3 (Service/Scope) branch
// authorization for payment reads that take a client-supplied branch_id.
//
// RLS (layer 1) isolates by tenant_id only, and OPA (layer 2) decides whether
// the action is allowed plus a coarse scope — it never sees the branch_id in
// the query string. Without this check a cashier of branch A could poll
// branch B of the same chain and watch its counter traffic.
//
// The branches table lives in the tenant module, which payment may not import
// (module isolation) and does not need to: branch membership is already
// resolved into auth.Principal at context selection, so validating against the
// principal is both sufficient and free. A tenant-scoped principal passing a
// branch_id from another tenant is harmless — RLS scopes the query to their
// own tenant, so the result is simply empty.
//
// Exemption is resolved from the OPA-derived scope in ctx rather than by
// inspecting RoleIDs, so role-to-scope mapping stays in authz.rego and cannot
// drift here. This mirrors pos/service.requireBranch deliberately: the two are
// separate copies because payment must not import pos.
//
// It is deliberately STRICTER than pos's copy in one respect: a branch-scoped
// principal must match branchID exactly, and a nil Principal.BranchID does not
// grant chain-wide access on its own. auth.Principal.HasBranchAccess treats
// nil as "every branch", which is right for a chain owner but unsafe here,
// because nothing in the schema stops it from applying to a counter role:
// memberships.branch_id is nullable with no constraint tying branch-scoped
// system roles (cashier) to a non-null branch, so a mis-provisioned chain-wide
// cashier would otherwise be able to watch every branch's money in the chain.
// Legitimate chain-wide staff are unaffected — they hold the manager role and
// exit at the tenant-scope check above.
func requireBranch(ctx context.Context, principal auth.Principal, branchID uuid.UUID) error {
	if scope, ok := auth.ScopeFromContext(ctx); ok && scope == "tenant" {
		return nil
	}
	if principal.IsStaff() && principal.BranchID != uuid.Nil && principal.BranchID == branchID {
		return nil
	}
	return pub.ErrBranchForbidden
}
