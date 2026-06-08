// Package public exposes the payment module's cross-module contracts.
// Only types and interfaces defined here may be imported by other modules.
package public

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a payment record does not exist.
var ErrNotFound = errors.New("payment: not found")

// SaleReader is consumed by POS (and any other module that needs to verify
// payment totals for a check).  Dependency direction: pos → payment.public.
type SaleReader interface {
	// TotalPaidForCheck returns the sum of completed payment amounts (in kuruş)
	// for the given check.  Returns 0 if no payments exist for the check.
	TotalPaidForCheck(ctx context.Context, tenantID, checkID uuid.UUID) (int64, error)
}
