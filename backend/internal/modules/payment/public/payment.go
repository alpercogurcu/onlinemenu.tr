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

// ErrNoCashSessionOpen is returned by PaymentService.RegisterSale when a
// cash-method payment is registered for a branch with no open cash session
// (ADR-DATA-008). The expected-close formula sums completed cash payments
// inside the session's [OpenedAt, ClosedAt) window; a cash payment taken
// while no session exists falls outside every window a session will ever
// have and is permanently invisible to reconciliation — the drawer can never
// balance. Refusing it at the source is the only fix; there is no later
// point where the money can be reattached to a session. Callers map it to
// HTTP 409 (a caller-actionable state conflict, not a server fault).
var ErrNoCashSessionOpen = errors.New("payment: branch has no open cash session")

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

// ErrCashSessionClosed is returned by CashSessionPinService.Join/Switch when
// the target session is not open (ADR-DATA-008 PIN akışı §4: participation
// and switching only make sense against an open drawer). Callers map it to
// HTTP 409.
var ErrCashSessionClosed = errors.New("payment: cash session is closed")

// ErrSessionScopedPrincipal is returned by CashSessionPinService.Join when
// the caller's own principal was itself issued via PIN-switching
// (auth.Principal.SessionID != uuid.Nil). ADR-DATA-008 PIN akışı §2 requires
// setting a PIN to happen at the "trusted moment" of a fresh Keycloak
// authentication — a principal that is itself an impersonated identity is
// not that moment, even though every other authorization check it carries
// (branch, roles) is otherwise valid. Callers map it to HTTP 403.
var ErrSessionScopedPrincipal = errors.New("payment: caller must be freshly Keycloak-authenticated (not pin-switched) to do this")

// ErrPinVerificationFailed is returned by CashSessionPinService.Switch for
// EVERY negative verification outcome: wrong PIN, PIN never set, person not
// a participant of the session, and (as far as the caller can tell) a
// nonexistent person all collapse into this one sentinel — see
// identity/public.ErrPinVerificationFailed, which the service wraps here so
// payment_http never needs to import identity/public directly (module
// isolation: payment_http may only see payment_public). Callers map it to
// HTTP 401 with a generic message.
var ErrPinVerificationFailed = errors.New("payment: pin verification failed")

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
