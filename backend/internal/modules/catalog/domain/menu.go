package domain

import (
	"time"

	"github.com/google/uuid"
)

// Menu groups products for a specific context (e.g. öğle menüsü, akşam, teslimat).
type Menu struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	BranchID    *uuid.UUID // nil = all branches
	Name        string
	Description string
	IsActive    bool
	ValidFrom   *time.Time
	ValidUntil  *time.Time
	SortOrder   int16
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// MenuItem links a product to a menu with an optional price override.
type MenuItem struct {
	MenuID        uuid.UUID
	ProductID     uuid.UUID
	TenantID      uuid.UUID
	PriceOverride *int64 // nil = use product's base price
	IsActive      bool
	SortOrder     int16
}
