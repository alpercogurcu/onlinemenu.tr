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

	// PendingTotalForCheck returns the sum of payment amounts (in kuruş) whose
	// fiscal registration is still in flight for the given check — money the
	// cashier has already collected but that TotalPaidForCheck does not yet
	// count. Returns 0 when nothing is pending.
	//
	// It lets callers distinguish "the check is genuinely underpaid" from
	// "the fiscal device has not confirmed yet, wait a moment"; a boolean
	// would not, because a check can have a pending payment AND still be
	// short of its total.
	PendingTotalForCheck(ctx context.Context, tenantID, checkID uuid.UUID) (int64, error)
}
