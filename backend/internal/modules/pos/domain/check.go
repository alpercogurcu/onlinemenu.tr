package domain

import (
	"time"

	"github.com/google/uuid"
)

// CheckStatus is the lifecycle state of a dine-in tab.
type CheckStatus string

const (
	CheckStatusOpen      CheckStatus = "open"
	CheckStatusClosed    CheckStatus = "closed"
	CheckStatusCancelled CheckStatus = "cancelled"
	// CheckStatusMerged marks the source check of a merge (docs/pos-ux-spec.md
	// §3c): its orders now hang off another check. It is deliberately not
	// CheckStatusCancelled — the day-end report counts cancellations, and
	// every table merge would otherwise be reported as a cancelled sale. A
	// merged check carries no closed_at, so it never enters a report window
	// (see pos/000008).
	CheckStatusMerged CheckStatus = "merged"
)

func (s CheckStatus) Valid() bool {
	switch s {
	case CheckStatusOpen, CheckStatusClosed, CheckStatusCancelled, CheckStatusMerged:
		return true
	}
	return false
}

// OpenedByKind discriminates who opened a check. It exists because
// checks.opened_by is nullable since ADR-ARCH-006: a QR diner has no person
// row, and encoding that as a sentinel UUID would make every downstream read
// treat an anonymous guest as staff.
type OpenedByKind string

const (
	OpenedByKindStaff   OpenedByKind = "staff"
	OpenedByKindGuestQR OpenedByKind = "guest_qr"
)

func (k OpenedByKind) Valid() bool {
	switch k {
	case OpenedByKindStaff, OpenedByKindGuestQR:
		return true
	}
	return false
}

// Source discriminates which surface created a row. It is orthogonal to
// OrderChannel (dine_in/takeaway/delivery), which describes fulfillment: a QR
// order is channel dine_in AND source online_qr. Conflating the two is the
// mistake this type exists to prevent.
type Source string

const (
	SourcePOS      Source = "pos"
	SourceOnlineQR Source = "online_qr"
)

func (s Source) Valid() bool {
	switch s {
	case SourcePOS, SourceOnlineQR:
		return true
	}
	return false
}

// Check represents a dine-in table session (adisyon) that accumulates orders.
type Check struct {
	ID uuid.UUID
	// TableID is set when the check was opened against a floor-plan Table
	// (domain/table.go). It is nil for masasız satış (takeaway/paket servis)
	// checks — TableLabel keeps rendering unchanged in that case.
	TableID    *uuid.UUID
	TenantID   uuid.UUID
	BranchID   uuid.UUID
	TableLabel string
	// Pax is the number of guests (kisi sayisi) seated at the check.
	// CheckService.Open defaults it to 1 for any caller that omits it or
	// supplies a non-positive value — see that method's doc comment; the DB
	// column itself has no CHECK constraint (repo-level tests construct
	// domain.Check{} directly, bypassing that default).
	Pax    int
	Status CheckStatus
	// OpenedBy is nil exactly when OpenedByKind is OpenedByKindGuestQR; the
	// checks_opened_by_kind_chk constraint enforces that pairing in the DB.
	OpenedBy     *uuid.UUID
	OpenedByKind OpenedByKind
	// Source defaults to SourcePOS when left empty — see CheckRepo.Create,
	// which normalizes it rather than letting a zero-value Go string hit the
	// column's CHECK constraint.
	Source   Source
	ClosedBy *uuid.UUID
	// MergedIntoCheckID is non-nil exactly when Status is CheckStatusMerged
	// (checks_merged_into_chk enforces the pairing): it names the check that
	// absorbed this one's orders, so "bu adisyona ne oldu" is answerable from
	// the row rather than only from the outbox event.
	MergedIntoCheckID *uuid.UUID
	Note              string
	OpenedAt          time.Time
	ClosedAt          *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
