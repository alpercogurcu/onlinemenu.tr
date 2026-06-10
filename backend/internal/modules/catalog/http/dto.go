package http

import (
	"time"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/catalog/domain"
)

// --- Category ---

type categoryResponse struct {
	ID          uuid.UUID  `json:"id"`
	TenantID    uuid.UUID  `json:"tenant_id"`
	BranchID    *uuid.UUID `json:"branch_id"`
	ParentID    *uuid.UUID `json:"parent_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	IsActive    bool       `json:"is_active"`
	SortOrder   int16      `json:"sort_order"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toCategoryResponse(c domain.Category) categoryResponse {
	return categoryResponse{
		ID:          c.ID,
		TenantID:    c.TenantID,
		BranchID:    c.BranchID,
		ParentID:    c.ParentID,
		Name:        c.Name,
		Description: c.Description,
		IsActive:    c.IsActive,
		SortOrder:   c.SortOrder,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

// --- Product ---

type productResponse struct {
	ID          uuid.UUID  `json:"id"`
	TenantID    uuid.UUID  `json:"tenant_id"`
	CategoryID  *uuid.UUID `json:"category_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	ImageKey    string     `json:"image_key"`
	PriceAmount int64      `json:"price_amount"`
	Currency    string     `json:"currency"`
	SKU         string     `json:"sku"`
	Unit        string     `json:"unit"`
	TaxRateBPS  int        `json:"tax_rate_bps"`
	IsActive    bool       `json:"is_active"`
	SortOrder   int16      `json:"sort_order"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toProductResponse(p domain.Product) productResponse {
	return productResponse{
		ID:          p.ID,
		TenantID:    p.TenantID,
		CategoryID:  p.CategoryID,
		Name:        p.Name,
		Description: p.Description,
		ImageKey:    p.ImageKey,
		PriceAmount: p.PriceAmount,
		Currency:    p.Currency,
		SKU:         p.SKU,
		Unit:        p.Unit,
		TaxRateBPS:  p.TaxRateBPS,
		IsActive:    p.IsActive,
		SortOrder:   p.SortOrder,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// --- ModifierGroup ---

type modifierGroupResponse struct {
	ID            uuid.UUID  `json:"id"`
	TenantID      uuid.UUID  `json:"tenant_id"`
	Name          string     `json:"name"`
	SelectionType string     `json:"selection_type"`
	MinSelections int16      `json:"min_selections"`
	MaxSelections *int16     `json:"max_selections"`
	IsRequired    bool       `json:"is_required"`
	SortOrder     int16      `json:"sort_order"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func toModifierGroupResponse(mg domain.ModifierGroup) modifierGroupResponse {
	return modifierGroupResponse{
		ID:            mg.ID,
		TenantID:      mg.TenantID,
		Name:          mg.Name,
		SelectionType: string(mg.SelectionType),
		MinSelections: mg.MinSelections,
		MaxSelections: mg.MaxSelections,
		IsRequired:    mg.IsRequired,
		SortOrder:     mg.SortOrder,
		CreatedAt:     mg.CreatedAt,
		UpdatedAt:     mg.UpdatedAt,
	}
}

// --- Modifier ---

type modifierResponse struct {
	ID         uuid.UUID `json:"id"`
	TenantID   uuid.UUID `json:"tenant_id"`
	GroupID    uuid.UUID `json:"group_id"`
	Name       string    `json:"name"`
	PriceDelta int64     `json:"price_delta"`
	IsActive   bool      `json:"is_active"`
	SortOrder  int16     `json:"sort_order"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toModifierResponse(m domain.Modifier) modifierResponse {
	return modifierResponse{
		ID:         m.ID,
		TenantID:   m.TenantID,
		GroupID:    m.GroupID,
		Name:       m.Name,
		PriceDelta: m.PriceDelta,
		IsActive:   m.IsActive,
		SortOrder:  m.SortOrder,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}

// --- Menu ---

type menuResponse struct {
	ID          uuid.UUID  `json:"id"`
	TenantID    uuid.UUID  `json:"tenant_id"`
	BranchID    *uuid.UUID `json:"branch_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	IsActive    bool       `json:"is_active"`
	SortOrder   int16      `json:"sort_order"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toMenuResponse(m domain.Menu) menuResponse {
	return menuResponse{
		ID:          m.ID,
		TenantID:    m.TenantID,
		BranchID:    m.BranchID,
		Name:        m.Name,
		Description: m.Description,
		IsActive:    m.IsActive,
		SortOrder:   m.SortOrder,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

// --- MenuItem ---

type menuItemResponse struct {
	MenuID        uuid.UUID `json:"menu_id"`
	ProductID     uuid.UUID `json:"product_id"`
	TenantID      uuid.UUID `json:"tenant_id"`
	PriceOverride *int64    `json:"price_override"`
	IsActive      bool      `json:"is_active"`
}

func toMenuItemResponse(mi domain.MenuItem) menuItemResponse {
	return menuItemResponse{
		MenuID:        mi.MenuID,
		ProductID:     mi.ProductID,
		TenantID:      mi.TenantID,
		PriceOverride: mi.PriceOverride,
		IsActive:      mi.IsActive,
	}
}
