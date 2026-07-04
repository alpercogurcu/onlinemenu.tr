package service

import (
	"context"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/platform/auth"
)

// RequireBranchForTest exposes the unexported requireBranch (ADR-AUTH-001
// layer 3 branch authorization, security sprint) for unit tests that need to
// exercise the OPA-scope exemption path directly, without going through a
// full service method + DB-backed entity load.
func RequireBranchForTest(ctx context.Context, principal auth.Principal, branchID uuid.UUID) error {
	return requireBranch(ctx, principal, branchID)
}
