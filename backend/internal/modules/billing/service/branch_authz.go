package service

import (
	"context"

	"github.com/google/uuid"

	pub "onlinemenu.tr/internal/modules/billing/public"
	"onlinemenu.tr/internal/platform/auth"
)

// requireBranch enforces ADR-AUTH-001 layer 3 (Service/Scope) branch
// authorization for billing's branch-scoped write actions (docs/lessons-
// from-b2b.md item 6): invoice generation and retry submission. RLS
// (layer 1) only isolates by tenant_id, and OPA (layer 2) only decides
// whether the action is allowed at all plus a coarse scope — it never sees
// the specific branch_id of the invoice being acted on. That check belongs
// here.
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
// When no tenant-wide exemption applies, the principal must directly cover
// branchID per auth.Principal.HasBranchAccess (exact match, or chain-wide
// staff whose Principal.BranchID == uuid.Nil).
//
// NOTE (inert-but-correct, Faz 1): configs/opa/bundles/authz.rego currently
// grants billing.* actions to the "manager" system role only (no seeded
// permission rows for any other role — see the rego's comment at the
// "back-office modules" allow rule). Manager always resolves to
// scope=="tenant", so in production today no principal can reach this
// check with anything other than the exemption path. This mirrors the
// inventory module's "warehouse" role forward-declaration in the same rego
// file: the rule is added now so it takes effect the moment a branch-scoped
// billing role is seeded, with no further service-layer change required.
//
// Callers acting on an already-persisted invoice (RetrySubmission) MUST
// invoke this after loading the entity (its branch_id is only known once
// loaded) but BEFORE any status-eligibility check, so a branch-forbidden
// caller receives 403, never a 422/409 that would otherwise leak the
// resource's current status to someone who has no business acting on it.
// Callers creating a new invoice (GenerateInvoice) have no persisted entity
// to load yet, so they validate the client-supplied branch_id directly.
func requireBranch(ctx context.Context, principal auth.Principal, branchID uuid.UUID) error {
	if scope, ok := auth.ScopeFromContext(ctx); ok && scope == "tenant" {
		return nil
	}
	if principal.HasBranchAccess(branchID) {
		return nil
	}
	return pub.ErrBranchForbidden
}
