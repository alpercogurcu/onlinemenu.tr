package domain

import (
	"time"

	"github.com/google/uuid"
)

// Product is a sellable item in the tenant's catalog.
type Product struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	CategoryID           *uuid.UUID
	Name                 string
	Description          string
	ImageKey             string
	PriceAmount          int64  // kuruş (1/100 TL)
	Currency             string // ISO 4217, default "TRY"
	SKU                  string
	Barcode              string
	Unit                 string // adet, kg, lt, porsiyon
	TaxRateBPS           int    // basis points, e.g. 1800 = %18
	IsActive             bool
	AutoCloseOnZeroStock bool // POS closes product when stock reaches 0
	StockQuantity        *int // nil = unlimited
	SortOrder            int16
	// SourceStockItemID optionally links this sellable product to its
	// canonical stocked entity in inventory.stock_items (ADR-DATA-005). Only
	// products backed by tracked stock (e.g. a franchise-sold finished good)
	// set this; pure service/combo products leave it nil. No FK: cross-module
	// reference (migrations/catalog/000002).
	SourceStockItemID *uuid.UUID
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ChannelAvailability controls product visibility per order channel.
type ChannelAvailability struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	ProductID      uuid.UUID
	OrderChannel   OrderChannel
	IntegratorSlug string // empty = all integrators for this channel
	IsAvailable    bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// OrderChannel enumerates the contexts in which an order can originate.
type OrderChannel string

const (
	ChannelDineIn   OrderChannel = "dine_in"
	ChannelTakeaway OrderChannel = "takeaway"
	ChannelDelivery OrderChannel = "delivery"
)

// Valid reports whether oc is a recognised order channel.
func (oc OrderChannel) Valid() bool {
	switch oc {
	case ChannelDineIn, ChannelTakeaway, ChannelDelivery:
		return true
	}
	return false
}
