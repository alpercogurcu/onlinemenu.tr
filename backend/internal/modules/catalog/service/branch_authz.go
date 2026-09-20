package service

import (
	"context"

	"github.com/google/uuid"

	pub "onlinemenu.tr/internal/modules/catalog/public"
	"onlinemenu.tr/internal/platform/auth"
)

// RequireBranchAccess is ADR-AUTH-001 layer 3 for the catalog's branch-scoped
// surface (ADR-SEC-005): OPA has already said the ACTION is allowed, and this
// decides whether this principal may aim it at THIS branch.
//
// It is a copy of the identical guards in pos, payment and storefront rather
// than a shared platform helper, deliberately: a single cross-module guard
// would have to know every module's error type, and the one place this logic
// may not drift is the decision itself, which is three lines long and
// identically asserted in each module's tests.
//
// A "tenant" scope (the chain-wide manager) passes for any branch; a
// branch-bound staff member passes only for their own. Anything else — a
// guest token, a staff member with no branch — is refused.
func RequireBranchAccess(ctx context.Context, principal auth.Principal, branchID uuid.UUID) error {
	if scope, ok := auth.ScopeFromContext(ctx); ok && scope == "tenant" {
		return nil
	}
	if principal.IsStaff() && principal.BranchID != uuid.Nil && principal.BranchID == branchID {
		return nil
	}
	return pub.ErrBranchForbidden
}
