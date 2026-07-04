package domain

import (
	"time"

	"github.com/google/uuid"
)

// PurchaseReceipt is an elden fiş / faturasız alım belgesi (ADR-DATA-007
// karar 3): a cash/no-invoice purchase document (market, pazar), distinct
// from the (future, Faz 2+) invoiced purchase_orders path. Immutable by
// convention — there is no Update in PurchaseReceiptRepo/PurchaseReceiptService;
// a correction is a new receipt.
type PurchaseReceipt struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	WarehouseID     uuid.UUID
	SupplierPartyID *uuid.UUID // party.suppliers(id); no FK (cross-module). nil = no registered supplier.
	SupplierName    string     // free-text identification when there is no party record ("X Pazarı")
	ReceiptNo       string     // printed/assigned receipt number, if any
	ReceiptDate     time.Time
	Total           float64
	Currency        string
	Note            string
	CreatedBy       *uuid.UUID
	CreatedAt       time.Time
}

// PurchaseReceiptItem is a line item on a PurchaseReceipt. UnitPrice is the
// branch-local cost source written to StockLevel.LastUnitCost
// (ADR-DATA-007, source=purchase_receipt) at receipt create time.
type PurchaseReceiptItem struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	ReceiptID   uuid.UUID
	StockItemID uuid.UUID
	Quantity    float64
	Unit        string // write-time canonical unit (ADR-DATA-005)
	UnitPrice   float64
	LineTotal   float64
	Brand       string // nullable; line-level brand diversity (ADR-DATA-007 point 5)
}
