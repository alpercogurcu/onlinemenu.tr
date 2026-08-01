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

// ErrBranchForbidden is returned when a principal acts on a branch its context
// does not cover (ADR-AUTH-001 layer 3). Callers map it to HTTP 403.
var ErrBranchForbidden = errors.New("payment: branch forbidden")

// ErrCashSessionAlreadyOpen is returned by CashSessionService.Open when the
// branch already has an open cash session (ADR-DATA-008 Karar 1: at most one
// open session per branch). Callers map it to HTTP 409.
var ErrCashSessionAlreadyOpen = errors.New("payment: branch already has an open cash session")

// ErrInvalidInput marks a caller-supplied value the service rejects (a
// non-positive amount, an empty reason, an unknown enum). Callers map it to
// HTTP 422.
//
// It exists because the alternative — a bare fmt.Errorf — falls through the
// handlers' error switch to the generic arm and is reported as 500 "internal
// server error" while also being logged at Error level. That is wrong twice:
// the client is told the server broke when the client sent bad input, and the
// error log fills with false alarms that mask real faults.
var ErrInvalidInput = errors.New("payment: invalid input")

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
