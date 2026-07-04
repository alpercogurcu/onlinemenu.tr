package service

import "onlinemenu.tr/internal/modules/inventory/domain"

// ValidateMovementForTest exposes the unexported validateMovement for unit tests.
func ValidateMovementForTest(req RecordMovementRequest) error {
	return validateMovement(req)
}

// SignedDeltaForTest exposes the unexported signedDelta for unit tests.
func SignedDeltaForTest(t domain.MovementType, quantity float64) float64 {
	return signedDelta(t, quantity)
}
