package main

import (
	"fmt"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
)

// This file adds the Wails bindings for the cashier flow: check list -> open
// check -> place order -> cash payment -> close check. Every method here
// only translates between apiclient's Go-shaped types and this file's DTOs
// (JSON-tagged for the frontend) — apiclient.Client remains the sole HTTP
// and token authority (see app.go / README.md "Tek token-refresh otoritesi").

// CategoryDTO mirrors apiclient.Category.
type CategoryDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SortOrder   int16  `json:"sort_order"`
}

// ProductDTO mirrors apiclient.Product.
type ProductDTO struct {
	ID          string `json:"id"`
	CategoryID  string `json:"category_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	PriceAmount int64  `json:"price_amount"`
	Currency    string `json:"currency"`
	Unit        string `json:"unit"`
	TaxRateBPS  int    `json:"tax_rate_bps"`
	SortOrder   int16  `json:"sort_order"`
}

// CheckDTO mirrors apiclient.Check.
type CheckDTO struct {
	ID         string `json:"id"`
	BranchID   string `json:"branch_id"`
	TableLabel string `json:"table_label"`
	Status     string `json:"status"`
	Note       string `json:"note"`
	OpenedAt   string `json:"opened_at"`
	ClosedAt   string `json:"closed_at,omitempty"`
}

// OrderItemDTO mirrors apiclient.OrderItem.
type OrderItemDTO struct {
	ID              string `json:"id"`
	ProductID       string `json:"product_id"`
	ProductName     string `json:"product_name"`
	Quantity        int    `json:"quantity"`
	UnitPriceAmount int64  `json:"unit_price_amount"`
	Note            string `json:"note"`
}

// OrderDTO mirrors apiclient.Order.
type OrderDTO struct {
	ID        string         `json:"id"`
	CheckID   string         `json:"check_id,omitempty"`
	Status    string         `json:"status"`
	Note      string         `json:"note"`
	Items     []OrderItemDTO `json:"items"`
	CreatedAt string         `json:"created_at"`
}

// OrderItemInputDTO is one product+quantity line the frontend sends to
// PlaceOrder — a snapshot of the product's current catalog price, taken at
// the moment it's added to the cart.
type OrderItemInputDTO struct {
	ProductID          string `json:"product_id"`
	ProductName        string `json:"product_name"`
	ProductPriceAmount int64  `json:"product_price_amount"`
	ProductCurrency    string `json:"product_currency"`
	TaxRateBPS         int    `json:"tax_rate_bps"`
	Quantity           int    `json:"quantity"`
	UnitPriceAmount    int64  `json:"unit_price_amount"`
	Note               string `json:"note"`
}

// PaymentDTO mirrors apiclient.Payment.
type PaymentDTO struct {
	ID          string `json:"id"`
	CheckID     string `json:"check_id,omitempty"`
	Method      string `json:"method"`
	Status      string `json:"status"`
	AmountTotal int64  `json:"amount_total"`
	Currency    string `json:"currency"`
}

// ListCategories returns the tenant's catalog categories for the category
// tab strip.
func (a *App) ListCategories() ([]CategoryDTO, error) {
	cats, err := a.api.ListCategories(a.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CategoryDTO, len(cats))
	for i, c := range cats {
		out[i] = CategoryDTO{ID: c.ID, Name: c.Name, Description: c.Description, SortOrder: c.SortOrder}
	}
	return out, nil
}

// ListProducts returns the products in one category for the product grid.
func (a *App) ListProducts(categoryID string) ([]ProductDTO, error) {
	products, err := a.api.ListProducts(a.ctx, categoryID)
	if err != nil {
		return nil, err
	}
	out := make([]ProductDTO, len(products))
	for i, p := range products {
		out[i] = ProductDTO{
			ID:          p.ID,
			CategoryID:  p.CategoryID,
			Name:        p.Name,
			Description: p.Description,
			PriceAmount: p.PriceAmount,
			Currency:    p.Currency,
			Unit:        p.Unit,
			TaxRateBPS:  p.TaxRateBPS,
			SortOrder:   p.SortOrder,
		}
	}
	return out, nil
}

// ListOpenChecks returns every open (unclosed, uncancelled) check for the
// current session's branch, for the left rail's adisyon list. A chain-wide
// staff session (no branch) sees every branch's open checks — see
// apiclient.Client.ListOpenChecks for why this filter matters beyond
// tidiness.
func (a *App) ListOpenChecks() ([]CheckDTO, error) {
	checks, err := a.api.ListOpenChecks(a.ctx, a.api.CurrentBranchID())
	if err != nil {
		return nil, err
	}
	out := make([]CheckDTO, len(checks))
	for i, c := range checks {
		out[i] = toCheckDTO(c)
	}
	return out, nil
}

// OpenCheck opens a new check for the given table. branchID should come
// from the current session (SessionDTO.BranchID) — a chain-wide staff
// session (empty BranchID) must prompt the cashier for a branch before
// calling this, since the backend requires a non-nil branch_id.
func (a *App) OpenCheck(branchID, tableLabel, note string) (CheckDTO, error) {
	if branchID == "" {
		return CheckDTO{}, fmt.Errorf("şube seçilmeden adisyon açılamaz")
	}
	c, err := a.api.OpenCheck(a.ctx, branchID, tableLabel, note)
	if err != nil {
		return CheckDTO{}, err
	}
	return toCheckDTO(c), nil
}

// GetCheck returns a single check by id (e.g. after selecting it from the
// open-checks list).
func (a *App) GetCheck(checkID string) (CheckDTO, error) {
	c, err := a.api.GetCheck(a.ctx, checkID)
	if err != nil {
		return CheckDTO{}, err
	}
	return toCheckDTO(c), nil
}

// ListCheckOrders returns every order placed on a check so far, used to
// rebuild the receipt when a cashier reopens an existing adisyon.
func (a *App) ListCheckOrders(checkID string) ([]OrderDTO, error) {
	orders, err := a.api.ListCheckOrders(a.ctx, checkID)
	if err != nil {
		return nil, err
	}
	out := make([]OrderDTO, len(orders))
	for i, o := range orders {
		out[i] = toOrderDTO(o)
	}
	return out, nil
}

// PlaceOrder adds a round of items to a check (one kitchen ticket).
func (a *App) PlaceOrder(branchID, checkID string, items []OrderItemInputDTO) (OrderDTO, error) {
	if branchID == "" {
		return OrderDTO{}, fmt.Errorf("şube bilgisi eksik — oturum yeniden açılmalı")
	}
	in := make([]apiclient.OrderItemInput, len(items))
	for i, it := range items {
		in[i] = apiclient.OrderItemInput{
			ProductID:          it.ProductID,
			ProductName:        it.ProductName,
			ProductPriceAmount: it.ProductPriceAmount,
			ProductCurrency:    it.ProductCurrency,
			TaxRateBPS:         it.TaxRateBPS,
			Quantity:           it.Quantity,
			UnitPriceAmount:    it.UnitPriceAmount,
			Note:               it.Note,
		}
	}
	o, err := a.api.PlaceOrder(a.ctx, branchID, checkID, in)
	if err != nil {
		return OrderDTO{}, err
	}
	return toOrderDTO(o), nil
}

// RegisterCashPayment registers a cash sale against a check. amountTotal is
// the receipt total in kuruş (smallest currency unit) — the frontend
// derives it by summing every placed order's item lines
// (quantity * unit_price_amount), the same computation
// pos/repo.CheckRepo.GetTotal performs server-side for CloseCheck's paid-in-full
// check (there is no server-side "check total" endpoint to read it from).
func (a *App) RegisterCashPayment(branchID, checkID string, amountTotal int64) (PaymentDTO, error) {
	if branchID == "" {
		return PaymentDTO{}, fmt.Errorf("şube bilgisi eksik — oturum yeniden açılmalı")
	}
	p, err := a.api.RegisterCashPayment(a.ctx, branchID, checkID, amountTotal)
	if err != nil {
		return PaymentDTO{}, err
	}
	dto := PaymentDTO{
		ID:          p.ID,
		Method:      p.Method,
		Status:      p.Status,
		AmountTotal: p.AmountTotal,
		Currency:    p.Currency,
	}
	if p.CheckID != nil {
		dto.CheckID = *p.CheckID
	}
	return dto, nil
}

// CloseCheck closes a check once it has been paid in full. See
// apiclient.Client.CloseCheck's doc comment for the underpaid-check error
// shape caveat (backend returns an opaque 500, not a distinguishable 4xx).
func (a *App) CloseCheck(checkID string) (CheckDTO, error) {
	c, err := a.api.CloseCheck(a.ctx, checkID)
	if err != nil {
		return CheckDTO{}, err
	}
	return toCheckDTO(c), nil
}

func toCheckDTO(c apiclient.Check) CheckDTO {
	dto := CheckDTO{
		ID:         c.ID,
		BranchID:   c.BranchID,
		TableLabel: c.TableLabel,
		Status:     c.Status,
		Note:       c.Note,
		OpenedAt:   c.OpenedAt.Format(rfc3339Millis),
	}
	if c.ClosedAt != nil {
		dto.ClosedAt = c.ClosedAt.Format(rfc3339Millis)
	}
	return dto
}

func toOrderDTO(o apiclient.Order) OrderDTO {
	items := make([]OrderItemDTO, len(o.Items))
	for i, it := range o.Items {
		items[i] = OrderItemDTO{
			ID:              it.ID,
			ProductID:       it.ProductID,
			ProductName:     it.ProductName,
			Quantity:        it.Quantity,
			UnitPriceAmount: it.UnitPriceAmount,
			Note:            it.Note,
		}
	}
	dto := OrderDTO{
		ID:        o.ID,
		Status:    o.Status,
		Note:      o.Note,
		Items:     items,
		CreatedAt: o.CreatedAt.Format(rfc3339Millis),
	}
	if o.CheckID != nil {
		dto.CheckID = *o.CheckID
	}
	return dto
}

// rfc3339Millis formats timestamps for the frontend. Plain RFC3339 (Go's
// default json time.Time encoding would already be this) — named here only
// because toCheckDTO/toOrderDTO format explicitly rather than relying on
// domain.Check/Order's zero-value handling upstream.
const rfc3339Millis = "2006-01-02T15:04:05.000Z07:00"
