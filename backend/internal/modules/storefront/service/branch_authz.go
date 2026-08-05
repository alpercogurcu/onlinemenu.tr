package service

import (
	"context"

	"github.com/google/uuid"

	pub "onlinemenu.tr/internal/modules/storefront/public"
	"onlinemenu.tr/internal/platform/auth"
)

// requireBranch enforces ADR-AUTH-001 layer 3 for storefront's branch-scoped
// admin actions (QR issue/revoke/rotate). It is a deliberate copy of
// pos/service.requireBranch rather than an import: modules may not reach into
// each other's service layer, and the rule it encodes is identical —
//
//   - a tenant-scoped principal (OPA scope "tenant", i.e. manager) is exempt,
//     resolved from the OPA-derived scope in ctx, never from role UUIDs;
//   - otherwise the principal must be staff whose own BranchID is set and
//     equals branchID exactly. This is stricter than
//     auth.Principal.HasBranchAccess, which reads a nil BranchID as "every
//     branch" — safe for a chain owner, unsafe here.
//
// A caller that never went through auth.RequirePermission has no scope in ctx
// and therefore fails CLOSED.
func requireBranch(ctx context.Context, principal auth.Principal, branchID uuid.UUID) error {
	if scope, ok := auth.ScopeFromContext(ctx); ok && scope == "tenant" {
		return nil
	}
	if principal.IsStaff() && principal.BranchID != uuid.Nil && principal.BranchID == branchID {
		return nil
	}
	return pub.ErrBranchForbidden
}
