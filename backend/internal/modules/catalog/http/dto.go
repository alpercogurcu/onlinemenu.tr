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
	ID                uuid.UUID  `json:"id"`
	TenantID          uuid.UUID  `json:"tenant_id"`
	CategoryID        *uuid.UUID `json:"category_id"`
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	ImageKey          string     `json:"image_key"`
	PriceAmount       int64      `json:"price_amount"`
	Currency          string     `json:"currency"`
	SKU               string     `json:"sku"`
	Unit              string     `json:"unit"`
	TaxRateBPS        int        `json:"tax_rate_bps"`
	IsActive          bool       `json:"is_active"`
	SortOrder         int16      `json:"sort_order"`
	SourceStockItemID *uuid.UUID `json:"source_stock_item_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	// BranchPriceOverridden says PriceAmount is this branch's own price, not
	// the tenant's (ADR-DATA-009). Always false unless the request named a
	// branch_id; the field is always present so a client never has to tell
	// "absent" from "false".
	BranchPriceOverridden bool `json:"branch_price_overridden"`
}

func toProductResponse(p domain.Product) productResponse {
	return productResponse{
		ID:                p.ID,
		TenantID:          p.TenantID,
		CategoryID:        p.CategoryID,
		Name:              p.Name,
		Description:       p.Description,
		ImageKey:          p.ImageKey,
		PriceAmount:       p.PriceAmount,
		Currency:          p.Currency,
		SKU:               p.SKU,
		Unit:              p.Unit,
		TaxRateBPS:        p.TaxRateBPS,
		IsActive:          p.IsActive,
		SortOrder:         p.SortOrder,
		SourceStockItemID: p.SourceStockItemID,
		CreatedAt:         p.CreatedAt,
		UpdatedAt:         p.UpdatedAt,
	}
}

// toBranchProductResponse is toProductResponse plus the branch flag. The
// embedded Product already carries the EFFECTIVE price, so a caller that
// ignores the flag still shows and bills the right number.
func toBranchProductResponse(bp domain.BranchProduct) productResponse {
	out := toProductResponse(bp.Product)
	out.BranchPriceOverridden = bp.BranchPriceOverridden
	return out
}

// --- ModifierGroup ---

type modifierGroupResponse struct {
	ID            uuid.UUID `json:"id"`
	TenantID      uuid.UUID `json:"tenant_id"`
	Name          string    `json:"name"`
	SelectionType string    `json:"selection_type"`
	MinSelections int16     `json:"min_selections"`
	MaxSelections *int16    `json:"max_selections"`
	IsRequired    bool      `json:"is_required"`
	SortOrder     int16     `json:"sort_order"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

// productOptionsResponse is one product's option tree for an order-taking
// client (GET /catalog/products/modifier-groups). Deliberately lean — no
// timestamps or tenant ids — because a POS reads it for every sellable product
// in one call. max_selections null means "no cap"; a group whose options are
// all inactive comes with an empty modifiers list.
type productOptionsResponse struct {
	ProductID uuid.UUID              `json:"product_id"`
	Groups    []groupOptionsResponse `json:"groups"`
}

type groupOptionsResponse struct {
	ID            uuid.UUID        `json:"id"`
	Name          string           `json:"name"`
	SelectionType string           `json:"selection_type"`
	MinSelections int16            `json:"min_selections"`
	MaxSelections *int16           `json:"max_selections"`
	IsRequired    bool             `json:"is_required"`
	SortOrder     int16            `json:"sort_order"`
	Modifiers     []optionResponse `json:"modifiers"`
}

type optionResponse struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	PriceDelta int64     `json:"price_delta"`
	SortOrder  int16     `json:"sort_order"`
}

func toProductOptionsResponse(p domain.ProductOptions) productOptionsResponse {
	groups := make([]groupOptionsResponse, len(p.Groups))
	for i, g := range p.Groups {
		mods := make([]optionResponse, len(g.Modifiers))
		for j, m := range g.Modifiers {
			mods[j] = optionResponse{ID: m.ID, Name: m.Name, PriceDelta: m.PriceDelta, SortOrder: m.SortOrder}
		}
		groups[i] = groupOptionsResponse{
			ID:            g.Group.ID,
			Name:          g.Group.Name,
			SelectionType: string(g.Group.SelectionType),
			MinSelections: g.Group.MinSelections,
			MaxSelections: g.Group.MaxSelections,
			IsRequired:    g.Group.IsRequired,
			SortOrder:     g.Group.SortOrder,
			Modifiers:     mods,
		}
	}
	return productOptionsResponse{ProductID: p.ProductID, Groups: groups}
}
