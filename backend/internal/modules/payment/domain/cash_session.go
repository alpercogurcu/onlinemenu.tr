package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CashSessionStatus is the lifecycle state of a branch's cash session
// (ADR-DATA-008). See allowedTransitions for the full state machine.
type CashSessionStatus string

const (
	CashSessionOpeningControl CashSessionStatus = "opening_control"
	CashSessionOpened         CashSessionStatus = "opened"
	CashSessionClosingControl CashSessionStatus = "closing_control"
	CashSessionClosed         CashSessionStatus = "closed"
)

// Valid reports whether s is a recognised cash session status.
func (s CashSessionStatus) Valid() bool {
	switch s {
	case CashSessionOpeningControl, CashSessionOpened, CashSessionClosingControl, CashSessionClosed:
		return true
	}
	return false
}

// ErrInvalidCashSessionTransition is returned when a cash session status
// transition is not allowed from its current status.
var ErrInvalidCashSessionTransition = errors.New("payment/domain: invalid cash session status transition")

// allowedTransitions is the single source of truth for the cash session status
// machine (ADR-DATA-008): opening_control -> opened -> closing_control ->
// closed. closed is terminal.
//
// closing_control -> closing_control is a deliberate self-loop, not an
// oversight: it is the "recount" path (submitting a corrected closing count
// before the session is actually closed), mirroring Odoo's
// post_closing_cash_details being callable more than once while a session sits
// in closing_control. There is no path back to opened or opening_control —
// once a closing count has been submitted, the only way out is a corrected
// closing count or a successful close.
var allowedTransitions = map[CashSessionStatus][]CashSessionStatus{
	CashSessionOpeningControl: {CashSessionOpened},
	CashSessionOpened:         {CashSessionClosingControl},
	CashSessionClosingControl: {CashSessionClosingControl, CashSessionClosed},
}

// Transition validates a proposed cash session status change. Every mutation
// that changes a cash session's status — including the initial opening_control
// -> opened write performed at creation — must call this first
// (docs/lessons-from-b2b.md item 1: scattered ad hoc status assignment is a
// repeat defect class). It never persists anything itself; callers apply the
// new status only after this returns nil.
func Transition(from, to CashSessionStatus) error {
	if !to.Valid() {
		return fmt.Errorf("payment/domain: invalid target cash session status %q: %w", to, ErrInvalidCashSessionTransition)
	}
	for _, next := range allowedTransitions[from] {
		if next == to {
			return nil
		}
	}
	return fmt.Errorf("payment/domain: cash session %s -> %s: %w", from, to, ErrInvalidCashSessionTransition)
}

// CashMovementDirection is the direction of an in-shift cash movement.
type CashMovementDirection string

const (
	CashMovementIn  CashMovementDirection = "in"  // bozuk para koyma / drawer top-up
	CashMovementOut CashMovementDirection = "out" // kasadan para alma / drawer withdrawal
)

// Valid reports whether d is a recognised movement direction.
func (d CashMovementDirection) Valid() bool {
	return d == CashMovementIn || d == CashMovementOut
}

// ErrDenominationSumMismatch is returned when a submitted denomination
// breakdown's total does not equal the closing counted amount it accompanies.
var ErrDenominationSumMismatch = errors.New("payment/domain: denomination breakdown does not sum to the closing counted amount")

// DenominationCount is one line of a kupur dokumu (denomination breakdown):
// "200'luk x 3" is {DenominationMinor: 20000, Count: 3}. The Turkish
// denomination list itself is fixed and intentionally not modelled here or as
// a tenant-configurable table (ADR-DATA-008) — the POS client is responsible
// for offering the fixed set of banknotes/coins; this struct only carries
// whatever it submits.
type DenominationCount struct {
	DenominationMinor int64 `json:"denomination_minor"`
	Count             int   `json:"count"`
}

// ValidateDenominations checks that a non-empty breakdown sums to
// closingCountedAmount. An empty/nil breakdown is valid on its own — the
// denomination breakdown is an optional aid, not a mandatory input — but a
// breakdown that IS supplied must reconcile against the total the cashier
// entered, or the "sayım" the audit trail records is internally inconsistent.
func ValidateDenominations(breakdown []DenominationCount, closingCountedAmount int64) error {
	if len(breakdown) == 0 {
		return nil
	}
	var sum int64
	for _, d := range breakdown {
		if d.DenominationMinor <= 0 {
			return fmt.Errorf("payment/domain: denomination value must be positive, got %d", d.DenominationMinor)
		}
		if d.Count < 0 {
			return fmt.Errorf("payment/domain: denomination count must not be negative, got %d", d.Count)
		}
		sum += d.DenominationMinor * int64(d.Count)
	}
	if sum != closingCountedAmount {
		return fmt.Errorf("payment/domain: denomination sum %d != closing counted amount %d: %w",
			sum, closingCountedAmount, ErrDenominationSumMismatch)
	}
	return nil
}

// CashSession is the aggregate root for a branch's shift reconciliation
// (ADR-DATA-008). ExpectedClose and Difference are deliberately absent from
// this struct: they are never persisted (see the migration's column
// comments) and are computed on demand by the service layer, which also needs
// data this struct does not carry (completed cash payments, movement sum).
type CashSession struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	BranchID uuid.UUID
	Status   CashSessionStatus

	OpeningCountedAmount int64
	OpeningNotes         string
	OpenedBy             uuid.UUID
	OpenedAt             time.Time

	ClosingCountedAmount *int64
	ClosingDenominations []DenominationCount
	ClosingNotes         string
	ClosingSubmittedAt   *time.Time

	ClosedBy *uuid.UUID
	ClosedAt *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// CashMovement is one in-shift cash in/out record.
type CashMovement struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	BranchID    uuid.UUID
	SessionID   uuid.UUID
	Direction   CashMovementDirection
	AmountMinor int64
	Reason      string
	CreatedBy   uuid.UUID
	CreatedAt   time.Time
}
