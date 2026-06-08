package domain

import (
	"time"

	"github.com/google/uuid"
)

// ModifierGroup is a logical grouping of options that can be applied to a product
// (e.g. "Soslar", "Pişirme Şekli", "İçecek Boyutu").
type ModifierGroup struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	Name           string
	SelectionType  SelectionType
	MinSelections  int16
	MaxSelections  *int16 // nil = unlimited
	IsRequired     bool
	SortOrder      int16
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SelectionType controls how many options a customer may pick from a group.
type SelectionType string

const (
	SelectionSingle   SelectionType = "single"
	SelectionMultiple SelectionType = "multiple"
)

// Valid reports whether st is a recognised selection type.
func (st SelectionType) Valid() bool {
	return st == SelectionSingle || st == SelectionMultiple
}

// Modifier is a single option within a ModifierGroup (e.g. "Acı Sos", "İyi Pişmiş").
type Modifier struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	GroupID    uuid.UUID
	Name       string
	PriceDelta int64 // kuruş; may be negative (discount modifier)
	IsActive   bool
	SortOrder  int16
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
