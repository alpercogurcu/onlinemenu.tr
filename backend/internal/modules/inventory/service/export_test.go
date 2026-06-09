package service

// ValidateAdjustmentForTest exposes the unexported validateAdjustment for unit tests.
func ValidateAdjustmentForTest(req RecordAdjustmentRequest) error {
	return validateAdjustment(req)
}
