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
