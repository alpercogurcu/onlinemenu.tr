package service

import (
	"context"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/platform/auth"
)

// RequireBranchForTest exposes requireBranch to the external test package so
// ADR-AUTH-001 layer 3 can be asserted without routing through a database.
func RequireBranchForTest(ctx context.Context, principal auth.Principal, branchID uuid.UUID) error {
	return requireBranch(ctx, principal, branchID)
}

// BranchScopeFilterForTest exposes branchScopeFilter, requireBranch's
// counterpart for reads that take no client-supplied branch_id and therefore
// filter rather than reject. It is the single line deciding whether a principal
// sees one branch's money or the whole chain's.
func BranchScopeFilterForTest(ctx context.Context, principal auth.Principal) *uuid.UUID {
	return branchScopeFilter(ctx, principal)
}
