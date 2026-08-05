package http

import (
	"time"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/storefront/domain"
	"onlinemenu.tr/internal/modules/storefront/service"
)

// The response types below are the public wire contract. Domain and service
// structs are never serialized directly: a field added to one of them for an
// internal reason would otherwise appear on an anonymous, unauthenticated
// endpoint the moment it was added, with no code change to review.

// ---------------------------------------------------------------------------
// Session
// ---------------------------------------------------------------------------

// startSessionRequest carries the raw QR token in the BODY.
//
// It is never a path or query parameter: otelhttp records the raw request
// path as a span attribute before chi routes it (see cmd/api's tracing
// filter), and a browser would put a token-bearing URL in Referer and
// history. The body reaches neither.
type startSessionRequest struct {
	Token string `json:"token"`
}

// sessionResponse deliberately omits the branch NAME: the storefront module
// may only depend on catalog/public and pos/public (go-arch-lint), and neither
// exposes branch metadata. The menu app renders the table label, which the QR
// row and the pos table row both carry.
type sessionResponse struct {
	BranchID   uuid.UUID `json:"branch_id"`
	TableID    uuid.UUID `json:"table_id"`
	TableLabel string    `json:"table_label"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// ---------------------------------------------------------------------------
// Menu
// ---------------------------------------------------------------------------

type menuResponse struct {
	Categories []menuCategoryResponse `json:"categories"`
}

// menuCategoryResponse's Name is empty for the synthetic "uncategorised"
// bucket (a product whose category was deleted or deactivated). The menu app
// renders an empty name as "Diğer" — the label is a display decision and does
// not belong in the API.
type menuCategoryResponse struct {
	ID        uuid.UUID             `json:"id"`
	Name      string                `json:"name"`
	SortOrder int16                 `json:"sort_order"`
	Products  []menuProductResponse `json:"products"`
}

// menuProductResponse has no cost, stock, supplier or internal-note field —
// and no source struct that carries one. ImageKey is an object-storage key,
// not a URL; the menu app composes the URL from its own media base.
type menuProductResponse struct {
	ID             uuid.UUID                   `json:"id"`
	Name           string                      `json:"name"`
	Description    string                      `json:"description"`
	PriceAmount    int64                       `json:"price_amount"`
	Currency       string                      `json:"currency"`
	ImageKey       string                      `json:"image_key"`
	Allergens      []string                    `json:"allergens"`
	IsAvailable    bool                        `json:"is_available"`
	ModifierGroups []menuModifierGroupResponse `json:"modifier_groups"`
}

type menuModifierGroupResponse struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	SelectionType string    `json:"selection_type"`
	MinSelect     int       `json:"min_select"`
	// MaxSelect 0 means "no upper bound".
	MaxSelect int                    `json:"max_select"`
	Modifiers []menuModifierResponse `json:"modifiers"`
}

type menuModifierResponse struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	PriceDelta int64     `json:"price_delta"`
}

func toMenuResponse(categories []domain.GuestCategory) menuResponse {
	out := menuResponse{Categories: make([]menuCategoryResponse, 0, len(categories))}
	for _, c := range categories {
		category := menuCategoryResponse{
			ID:        c.ID,
			Name:      c.Name,
			SortOrder: c.SortOrder,
			Products:  make([]menuProductResponse, 0, len(c.Products)),
		}
		for _, p := range c.Products {
			allergens := p.Allergens
			if allergens == nil {
				allergens = []string{}
			}
			product := menuProductResponse{
				ID:             p.ID,
				Name:           p.Name,
				Description:    p.Description,
				PriceAmount:    p.PriceAmount,
				Currency:       p.Currency,
				ImageKey:       p.ImageKey,
				Allergens:      allergens,
				IsAvailable:    p.IsAvailable,
				ModifierGroups: make([]menuModifierGroupResponse, 0, len(p.ModifierGroups)),
			}
			for _, g := range p.ModifierGroups {
				group := menuModifierGroupResponse{
					ID:            g.ID,
					Name:          g.Name,
					SelectionType: g.SelectionType,
					MinSelect:     g.MinSelect,
					MaxSelect:     g.MaxSelect,
					Modifiers:     make([]menuModifierResponse, 0, len(g.Modifiers)),
				}
				for _, m := range g.Modifiers {
					group.Modifiers = append(group.Modifiers, menuModifierResponse{
						ID:         m.ID,
						Name:       m.Name,
						PriceDelta: m.PriceDelta,
					})
				}
				product.ModifierGroups = append(product.ModifierGroups, group)
			}
			category.Products = append(category.Products, product)
		}
		out.Categories = append(out.Categories, category)
	}
	return out
}

// ---------------------------------------------------------------------------
// Orders
// ---------------------------------------------------------------------------

// placeOrderRequest has no price field anywhere, by design: the server
// re-derives every amount from the menu read model (ADR-ARCH-006 §6). A price
// sent here would not be "rejected" — it would be undecodable.
type placeOrderRequest struct {
	Lines []placeOrderLine `json:"lines"`
	Note  string           `json:"note"`
}

type placeOrderLine struct {
	ProductID   uuid.UUID   `json:"product_id"`
	Quantity    int         `json:"quantity"`
	ModifierIDs []uuid.UUID `json:"modifier_ids"`
	Note        string      `json:"note"`
}

type placeOrderResponse struct {
	OrderID uuid.UUID `json:"order_id"`
	CheckID uuid.UUID `json:"check_id"`
	Status  string    `json:"status"`
	Total   int64     `json:"total"`
}

type orderStatusResponse struct {
	OrderID   uuid.UUID                 `json:"order_id"`
	Status    string                    `json:"status"`
	Note      string                    `json:"note"`
	Items     []orderStatusItemResponse `json:"items"`
	Total     int64                     `json:"total"`
	CreatedAt time.Time                 `json:"created_at"`
	UpdatedAt time.Time                 `json:"updated_at"`
}

type orderStatusItemResponse struct {
	Name            string `json:"name"`
	Quantity        int    `json:"quantity"`
	UnitPriceAmount int64  `json:"unit_price_amount"`
	Note            string `json:"note"`
}

type orderListResponse struct {
	Orders []orderStatusResponse `json:"orders"`
}

func toOrderStatusResponse(o service.GuestOrderStatus) orderStatusResponse {
	items := make([]orderStatusItemResponse, 0, len(o.Items))
	for _, it := range o.Items {
		items = append(items, orderStatusItemResponse{
			Name:            it.Name,
			Quantity:        it.Quantity,
			UnitPriceAmount: it.UnitPriceAmount,
			Note:            it.Note,
		})
	}
	return orderStatusResponse{
		OrderID:   o.OrderID,
		Status:    o.Status,
		Note:      o.Note,
		Items:     items,
		Total:     o.Total,
		CreatedAt: o.CreatedAt,
		UpdatedAt: o.UpdatedAt,
	}
}
