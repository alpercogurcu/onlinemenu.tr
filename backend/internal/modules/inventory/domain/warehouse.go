package domain

import (
	"time"

	"github.com/google/uuid"
)

// WarehouseType classifies a warehouse's operational role.
type WarehouseType string

const (
	WarehouseTypeDepo   WarehouseType = "depo"
	WarehouseTypeImalat WarehouseType = "imalat"
)

// Valid reports whether t is a recognised warehouse type.
func (t WarehouseType) Valid() bool {
	switch t {
	case WarehouseTypeDepo, WarehouseTypeImalat:
		return true
	}
	return false
}

// Warehouse is a depo (distribution warehouse) or imalat (manufacturing site)
// operated by a branch. All stock is warehouse-scoped (ADR-DATA-005).
type Warehouse struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	BranchID      uuid.UUID
	Name          string
	WarehouseType WarehouseType
	IsActive      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
