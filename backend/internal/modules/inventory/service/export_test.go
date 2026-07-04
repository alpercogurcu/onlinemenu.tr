package service

import (
	"context"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/inventory/domain"
	"onlinemenu.tr/internal/platform/auth"
)

// ValidateMovementForTest exposes the unexported validateMovement for unit tests.
func ValidateMovementForTest(req RecordMovementRequest) error {
	return validateMovement(req)
}

// SignedDeltaForTest exposes the unexported signedDelta for unit tests.
func SignedDeltaForTest(t domain.MovementType, quantity float64) float64 {
	return signedDelta(t, quantity)
}

// RequireBranchForTest exposes the unexported requireBranch (ADR-AUTH-001
// layer 3 branch authorization, security sprint) for unit tests that need to
// exercise the OPA-scope exemption path directly, without going through a
// full service method + DB-backed entity load.
func RequireBranchForTest(ctx context.Context, principal auth.Principal, branchID uuid.UUID) error {
	return requireBranch(ctx, principal, branchID)
}

// ValidateSupplyPolicyForTest exposes the unexported validateSupplyPolicy
// (ADR-DATA-007) for unit tests.
func ValidateSupplyPolicyForTest(req CreateSupplyPolicyRequest) error {
	return validateSupplyPolicy(req)
}
