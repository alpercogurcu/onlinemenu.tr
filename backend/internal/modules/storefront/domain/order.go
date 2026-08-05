package domain

import "github.com/google/uuid"

// GuestCartLine is one line of a diner's cart as submitted by the client.
//
// There is no price field, deliberately. The unit price is re-derived
// server-side from the same narrow menu read model the diner was shown
// (ADR-ARCH-006 §6), so a tampered request body has nothing to tamper with:
// the type cannot carry a price, therefore no handler can be written that
// trusts one.
type GuestCartLine struct {
	ProductID   uuid.UUID
	Quantity    int
	ModifierIDs []uuid.UUID
	Note        string
}

// GuestCart is a diner's submitted cart.
type GuestCart struct {
	Lines []GuestCartLine
	Note  string
}

// GuestOrderLink binds a placed pos order to the guest session that placed it
// (storefront_guest_orders). It is the only thing that makes "my orders"
// answerable for an anonymous diner: order visibility is keyed on the session
// claim in the guest JWT, never on a client-supplied order id alone.
type GuestOrderLink struct {
	OrderID        uuid.UUID
	TenantID       uuid.UUID
	QRCodeID       uuid.UUID
	GuestSessionID uuid.UUID
	CheckID        uuid.UUID
}
