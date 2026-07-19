package service

import (
	"context"

	"github.com/google/uuid"

	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/platform/auth"
)

// requireBranch enforces ADR-AUTH-001 layer 3 (Service/Scope) branch
// authorization for pos's branch-scoped write actions (docs/lessons-from-b2b.md
// item 6). Same-tenant isolation across branches is the gap this closes: RLS
// (layer 1) only isolates by tenant_id, and OPA (layer 2) only decides
// whether the action is allowed at all plus a coarse scope — it never sees
// the specific branch_id of the check/order being acted on. That check
// belongs here.
//
// Exemption is resolved via the OPA-derived Scope stored in ctx by
// auth.RequirePermission (auth.ScopeFromContext), not by inspecting
// principal.RoleIDs directly — role-to-scope mapping is OPA's job (see
// configs/opa/bundles/authz.rego), and hard-coding role UUIDs here would
// duplicate and could drift from that policy. A principal is exempt
// tenant-wide when scope == "tenant" (system role: manager). This is
// checked BEFORE the direct branch match so a principal whose own
// Principal.BranchID happens to be set to some other branch is still
// correctly recognised as exempt, rather than rejected on a coincidental
// mismatch.
//
// When no tenant-wide exemption applies, the principal must be staff whose
// own Principal.BranchID is set AND equals branchID exactly. This is
// deliberately STRICTER than auth.Principal.HasBranchAccess, which reads a
// nil BranchID as "every branch": correct for a chain owner, unsafe here,
// because nothing in the schema stops that shape from applying to a
// branch-scoped role — memberships.branch_id is nullable with no constraint
// tying a branch-scoped system role (cashier, waiter) to a non-null branch,
// so a mis-provisioned chain-wide cashier would otherwise be able to act on
// every branch of the chain. Legitimate chain-wide staff are unaffected:
// they hold the manager role and exit at the tenant-scope check above.
// A caller that reached this function without auth.RequirePermission has no
// scope in ctx and therefore fails CLOSED. This mirrors
// payment/service.requireBranch deliberately: the copies are separate
// because pos must not import payment (module isolation).
//
// Callers acting on an already-persisted check/order MUST invoke this after
// loading the entity (its branch_id is only known once loaded) but BEFORE
// any state-machine transition check, so a branch-forbidden caller receives
// 403, never a 409 that would otherwise leak the resource's current status
// to someone who has no business acting on it. Callers creating a new
// check/order (Open, Place) have no persisted entity to load yet, so they
// validate the client-supplied branch_id directly.
func requireBranch(ctx context.Context, principal auth.Principal, branchID uuid.UUID) error {
	if scope, ok := auth.ScopeFromContext(ctx); ok && scope == "tenant" {
		return nil
	}
	if principal.IsStaff() && principal.BranchID != uuid.Nil && principal.BranchID == branchID {
		return nil
	}
	return pub.ErrBranchForbidden
}

// RequireBranchAccess exports requireBranch for cross-package callers within
// the pos module (e.g. ws.Hub's kitchen WebSocket handshake) that need the
// same ADR-AUTH-001 layer 3 branch check without duplicating the OPA-scope
// exemption logic. See requireBranch for the full rationale.
func RequireBranchAccess(ctx context.Context, principal auth.Principal, branchID uuid.UUID) error {
	return requireBranch(ctx, principal, branchID)
}
