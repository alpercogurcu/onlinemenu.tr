// Package public exposes the POS module's read interface for consumption
// by other modules (e.g., payment). No direct DB access across module boundaries.
package public

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"onlinemenu.tr/internal/modules/pos/domain"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("pos: not found")

// ErrInvalidTransition is returned when a requested status change is not
// allowed from the resource's current status.
var ErrInvalidTransition = errors.New("pos: invalid status transition")

// ErrBranchForbidden is returned when the acting principal attempts a
// branch-scoped action (check open/close/cancel, order place/accept/reject/
// advance) on a resource belonging to a branch it does not have access to
// (ADR-AUTH-001, layer 3 / docs/lessons-from-b2b.md item 6). Tenant-wide
// principals (OPA scope "tenant", e.g. manager) are exempt. The resource is
// already known to be tenant-visible (RLS, layer 1, already passed), so this
// is not treated as a not-found — the HTTP layer maps it to 403 Forbidden.
var ErrBranchForbidden = errors.New("pos: forbidden for this branch")

// ErrTableOccupied is returned by CheckService.Open when the requested
// table_id is not currently "empty" or "reserved" — i.e. another open check
// already occupies it (including the case where two concurrent Open calls
// race for the same table; the table row lock in CheckRepo/TableRepo makes
// exactly one of them win, and the other observes this error).
var ErrTableOccupied = errors.New("pos: table is already occupied")

// ErrTableBranchMismatch is returned by CheckService.Open when the supplied
// table_id belongs to a different branch than the check's branch_id.
var ErrTableBranchMismatch = errors.New("pos: table does not belong to the check's branch")

// ErrTableNotFound is returned when a table id resolves to no row visible to
// the tenant. It is distinct from ErrNotFound so the storefront can answer a
// stale QR sticker (its table was deleted) with a message about the table
// rather than about the order.
var ErrTableNotFound = errors.New("pos: table not found")

// ErrTableNotReady is returned when a guest scans the QR of a table that is
// being cleaned. It is deliberately NOT ErrTableOccupied: "masa dolu" is the
// wrong thing to tell someone standing at an empty, uncleared table, and the
// right advice ("birkaç dakika içinde tekrar deneyin") only makes sense for
// this one status. Staff-facing CheckService.Open keeps returning
// ErrTableOccupied for cleaning tables — its caller is a cashier who can act
// on the floor plan directly.
var ErrTableNotReady = errors.New("pos: table not ready")

// ---------------------------------------------------------------------------
// Guest (QR dine-in) entry points — ADR-ARCH-006 §8
//
// These take NO auth.Principal. Calling the staff-facing OrderService.Place /
// CheckService.Open with a nil or synthesized principal is forbidden: those
// paths run requireBranch, whose whole job is to answer "may THIS member of
// staff act on that branch" — a question an anonymous diner cannot answer,
// and which a fake principal would answer "yes" to. A separately named entry
// point makes the anonymous path visible in every stack trace and audit.
// ---------------------------------------------------------------------------

// GuestTable is the narrow table projection the storefront needs to validate
// a scanned QR code before minting a session.
type GuestTable struct {
	ID       uuid.UUID
	BranchID uuid.UUID
	Label    string
	Status   domain.TableStatus
	IsActive bool
}

// GuestTableReader lets the storefront confirm that a QR code's table still
// exists and still belongs to the branch encoded in the code, before it hands
// out a guest session.
//
// It takes no principal for the same reason the placer does not; the caller
// is responsible for having established which tenant the request belongs to
// (the storefront does so by resolving the QR token hash first), exactly as
// OrderService.ListActiveByBranch documents for the kitchen WS hub.
type GuestTableReader interface {
	GetGuestTable(ctx context.Context, tenantID, tableID uuid.UUID) (GuestTable, error)
}

// GuestOrderLine is one line of a guest order, already priced by the caller
// from the catalog's storefront read model. pos does not re-price: it records
// what it is given, exactly as it does for staff orders.
type GuestOrderLine struct {
	ProductID uuid.UUID
	Name      string
	// BasePriceAmount is the product's list price; UnitPriceAmount is what is
	// actually billed (list price plus any modifier deltas), because pos bills
	// a line as quantity × unit_price_amount.
	BasePriceAmount int64
	UnitPriceAmount int64
	Currency        string
	TaxRateBPS      int
	Quantity        int
	Note            string
}

// GuestOrderRequest is a complete anonymous order placement.
type GuestOrderRequest struct {
	TenantID       uuid.UUID
	BranchID       uuid.UUID
	TableID        uuid.UUID
	QRCodeID       uuid.UUID
	GuestSessionID uuid.UUID
	Lines          []GuestOrderLine
	Note           string
}

// GuestOrderResult is what the caller needs to track the placed order.
type GuestOrderResult struct {
	OrderID uuid.UUID
	CheckID uuid.UUID
	Status  string
}

// GuestOrderLinker runs INSIDE pos's placement transaction, after the order
// and its items are written and before the transaction commits.
//
// It exists so the storefront's guest-session binding
// (storefront_guest_orders) commits atomically with the order itself. Without
// it, a failure between two separate transactions would leave a real order in
// the kitchen that the diner who placed it can never see — and whose retry
// would place a second one. The callback receives the same tx, whose RLS
// tenant is already the request's tenant.
type GuestOrderLinker func(ctx context.Context, tx pgx.Tx, placed GuestOrderResult) error

// GuestOrderPlacer places an anonymous QR order onto the table's check,
// opening a guest check when the table has none.
type GuestOrderPlacer interface {
	PlaceGuestOrder(ctx context.Context, req GuestOrderRequest, link GuestOrderLinker) (GuestOrderResult, error)
}

// GuestOrderItemView is one line of a guest's own order as they may read it
// back. It carries no cost, tax or internal fields.
type GuestOrderItemView struct {
	ProductName     string
	Quantity        int
	UnitPriceAmount int64
	Note            string
}

// GuestOrderView is a diner-facing order projection.
type GuestOrderView struct {
	ID        uuid.UUID
	CheckID   *uuid.UUID
	Status    string
	Note      string
	Items     []GuestOrderItemView
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GuestOrderReader reads back a single order for status polling. Authorization
// ("is this order this session's?") is the storefront's job — it answers it
// from storefront_guest_orders before calling here, never from the order id
// alone.
type GuestOrderReader interface {
	GetGuestOrder(ctx context.Context, tenantID, orderID uuid.UUID) (GuestOrderView, error)
}

// CheckReader allows other modules to read check state without importing POS internals.
type CheckReader interface {
	GetByID(ctx context.Context, tenantID, checkID uuid.UUID) (Check, error)
}

// Check is a read-only projection of the POS check for cross-module consumption.
type Check struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	BranchID   uuid.UUID
	TableLabel string
	Status     domain.CheckStatus
	OpenedAt   time.Time
}
