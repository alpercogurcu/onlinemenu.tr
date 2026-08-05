package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// QRCodeStatus is the lifecycle state of a printed QR code.
type QRCodeStatus string

const (
	QRCodeStatusActive  QRCodeStatus = "active"
	QRCodeStatusRevoked QRCodeStatus = "revoked"
)

func (s QRCodeStatus) Valid() bool {
	switch s {
	case QRCodeStatusActive, QRCodeStatusRevoked:
		return true
	}
	return false
}

// ErrInvalidTransition is returned when a QR code status change is not allowed
// from its current status.
var ErrInvalidTransition = errors.New("storefront/domain: invalid qr code status transition")

// allowedQRCodeTransitions is the single source of truth for the QR code
// status machine. revoked is terminal and irreversible: the physical sticker
// is already in the wild, so "un-revoking" would silently re-arm a code that
// was retired precisely because its token may have leaked. Reinstating a table
// means issuing a new code (a new token, a new sticker), never reviving this
// row — which is also what keeps the audit trail honest.
var allowedQRCodeTransitions = map[QRCodeStatus][]QRCodeStatus{
	QRCodeStatusActive: {QRCodeStatusRevoked},
}

// TransitionQRCodeStatus validates a proposed status transition and returns
// ErrInvalidTransition if it is not allowed from the current status.
func TransitionQRCodeStatus(from, to QRCodeStatus) error {
	if !to.Valid() {
		return fmt.Errorf("storefront/domain: invalid target status %q: %w", to, ErrInvalidTransition)
	}
	for _, next := range allowedQRCodeTransitions[from] {
		if next == to {
			return nil
		}
	}
	return fmt.Errorf("storefront/domain: %s -> %s: %w", from, to, ErrInvalidTransition)
}

// QRCode is a table-bound QR code that lets an anonymous diner reach the menu.
//
// There is no field for the raw token, by design: only TokenHash is ever
// persisted or loaded (migrations/storefront/000001). The raw token exists
// exactly once, as the return value of QRService.Issue/Rotate, and putting it
// on this struct would eventually get it logged or serialized.
type QRCode struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	BranchID   uuid.UUID
	TableID    uuid.UUID
	TableLabel string
	TokenHash  string
	Status     QRCodeStatus
	CreatedBy  uuid.UUID
	RevokedAt  *time.Time
	RevokedBy  *uuid.UUID
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// IsActive reports whether the code may still start a guest session.
func (q QRCode) IsActive() bool { return q.Status == QRCodeStatusActive }
