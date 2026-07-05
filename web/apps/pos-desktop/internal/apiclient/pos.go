package apiclient

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// This file adds the catalog/pos/payment calls needed for the cashier flow:
// check list -> open check -> place order -> cash payment -> close check.
// Every wire struct below is a 1:1 mirror of the backend's actual JSON
// shape, verified against source (not assumed) at:
//   - backend/internal/modules/catalog/http/{handler,dto}.go
//   - backend/internal/modules/pos/http/handler.go
//   - backend/internal/modules/payment/http/handler.go + domain/payment.go
//
// One deliberate asymmetry, called out where it applies: POST /api/v1/payments
// (registerSale) and GET /api/v1/payments/{id} (getPayment) respond with the
// raw domain.Payment struct (no json tags -> Go's default PascalCase field
// names), while GET /api/v1/payments (listPayments) responds with the
// snake_case paymentResponse DTO. registerSaleResponse below matches the
// PascalCase shape because that's the endpoint this client calls.

// --- Catalog ---

// Category mirrors catalog/http categoryResponse.
type Category struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SortOrder   int16  `json:"sort_order"`
}

// Product mirrors catalog/http productResponse.
type Product struct {
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

// ListCategories calls GET /api/v1/catalog/categories.
func (c *Client) ListCategories(ctx context.Context) ([]Category, error) {
	var out []Category
	if err := c.do(ctx, http.MethodGet, "/api/v1/catalog/categories", nil, &out); err != nil {
		return nil, fmt.Errorf("apiclient: list categories: %w", err)
	}
	return out, nil
}

// ListProducts calls GET /api/v1/catalog/categories/{id}/products — the
// catalog module's product listing has no unfiltered "list all with a
// category query param" route (listProducts returns every tenant product,
// listByCategory is the category-scoped one); this uses the latter since
// the POS product grid is always browsed by category tab.
func (c *Client) ListProducts(ctx context.Context, categoryID string) ([]Product, error) {
	var out []Product
	path := fmt.Sprintf("/api/v1/catalog/categories/%s/products", categoryID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, fmt.Errorf("apiclient: list products: %w", err)
	}
	return out, nil
}

// --- Checks (adisyon) ---

// Check mirrors pos/http checkResponse.
type Check struct {
	ID         string     `json:"id"`
	BranchID   string     `json:"branch_id"`
	TableLabel string     `json:"table_label"`
	Status     string     `json:"status"`
	Note       string     `json:"note"`
	OpenedAt   time.Time  `json:"opened_at"`
	ClosedAt   *time.Time `json:"closed_at"`
}

// ListOpenChecks calls GET /api/v1/pos/checks and filters to status "open"
// AND (when branchID is non-empty) to that branch. The backend endpoint
// (listChecks) returns every check for the WHOLE TENANT regardless of
// status or branch (verified against pos/repo/check_repo.go List — no WHERE
// clause beyond RLS's tenant scoping, ordered open-first); this client
// filters client-side rather than assuming a query param the handler
// doesn't have.
//
// The branch filter is not cosmetic: PlaceOrder/RegisterCashPayment always
// send the calling station's own branchID alongside whatever check_id the
// cashier selected, and the backend does not cross-validate that the
// check's branch matches the order/payment's branch_id at write time — only
// CloseCheck's requireBranch catches the mismatch, and only at close time.
// Without this filter, a station could select another branch's open check
// from the list and place orders/payments against it under its own
// branch_id before CloseCheck finally 403s. Pass branchID="" only for a
// chain-wide staff session, which legitimately sees every branch.
func (c *Client) ListOpenChecks(ctx context.Context, branchID string) ([]Check, error) {
	var all []Check
	if err := c.do(ctx, http.MethodGet, "/api/v1/pos/checks", nil, &all); err != nil {
		return nil, fmt.Errorf("apiclient: list open checks: %w", err)
	}

	open := make([]Check, 0, len(all))
	for _, chk := range all {
		if chk.Status != "open" {
			continue
		}
		if branchID != "" && chk.BranchID != branchID {
			continue
		}
		open = append(open, chk)
	}
	return open, nil
}

type openCheckRequest struct {
	BranchID   string `json:"branch_id"`
	TableLabel string `json:"table_label"`
	Note       string `json:"note"`
}

// OpenCheck calls POST /api/v1/pos/checks. Not idempotency-key-gated on the
// backend (pos/http.RegisterRoutes: only close and order-place carry
// httpx.Idempotency) and has no natural client-side dedup key today, so a
// retry here is left to the caller rather than silently risking a
// duplicate check on a timeout.
func (c *Client) OpenCheck(ctx context.Context, branchID, tableLabel, note string) (Check, error) {
	var out Check
	req := openCheckRequest{BranchID: branchID, TableLabel: tableLabel, Note: note}
	if err := c.do(ctx, http.MethodPost, "/api/v1/pos/checks", req, &out); err != nil {
		return Check{}, fmt.Errorf("apiclient: open check: %w", err)
	}
	return out, nil
}

// GetCheck calls GET /api/v1/pos/checks/{id}.
func (c *Client) GetCheck(ctx context.Context, checkID string) (Check, error) {
	var out Check
	path := fmt.Sprintf("/api/v1/pos/checks/%s", checkID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Check{}, fmt.Errorf("apiclient: get check: %w", err)
	}
	return out, nil
}

// CloseCheck calls POST /api/v1/pos/checks/{id}/close (Idempotency-Key
// required — ADR-SEC-003). The backend rejects this with a plain 500 (not
// a distinguishable 4xx) when the check is underpaid — see
// pos/service.CheckService.Close's ErrInsufficientPayment, which is not
// mapped through wrapErr's pub.Err* sentinels the HTTP handler recognizes.
// That is a backend gap outside this app's scope; callers here only get an
// opaque *APIError with StatusCode 500 for that case, same as any other
// server error.
func (c *Client) CloseCheck(ctx context.Context, checkID string) (Check, error) {
	var out Check
	path := fmt.Sprintf("/api/v1/pos/checks/%s/close", checkID)
	if err := c.doIdempotent(ctx, http.MethodPost, path, nil, &out); err != nil {
		return Check{}, fmt.Errorf("apiclient: close check: %w", err)
	}
	return out, nil
}

// --- Orders ---

// OrderItem mirrors pos/http orderItemResponse.
type OrderItem struct {
	ID              string `json:"id"`
	ProductID       string `json:"product_id"`
	ProductName     string `json:"product_name"`
	Quantity        int    `json:"quantity"`
	UnitPriceAmount int64  `json:"unit_price_amount"`
	Note            string `json:"note"`
}

// Order mirrors pos/http orderResponse.
type Order struct {
	ID           string      `json:"id"`
	BranchID     string      `json:"branch_id"`
	CheckID      *string     `json:"check_id"`
	OrderChannel string      `json:"order_channel"`
	Status       string      `json:"status"`
	Note         string      `json:"note"`
	Items        []OrderItem `json:"items"`
	CreatedAt    time.Time   `json:"created_at"`
}

// ListCheckOrders calls GET /api/v1/pos/checks/{id}/orders. Not in the
// original binding list but required to hydrate a check that already has
// orders on it (e.g. the cashier reopens an existing adisyon) — PlaceOrder's
// own response is only enough to build the receipt for orders placed in the
// current session.
func (c *Client) ListCheckOrders(ctx context.Context, checkID string) ([]Order, error) {
	var out []Order
	path := fmt.Sprintf("/api/v1/pos/checks/%s/orders", checkID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, fmt.Errorf("apiclient: list check orders: %w", err)
	}
	return out, nil
}

// OrderItemInput is one line of a PlaceOrder request — a snapshot of the
// product data at order time (pos/http orderItemInput / domain.OrderItem).
type OrderItemInput struct {
	ProductID          string `json:"product_id"`
	ProductName        string `json:"product_name"`
	ProductPriceAmount int64  `json:"product_price_amount"`
	ProductCurrency    string `json:"product_currency"`
	TaxRateBPS         int    `json:"tax_rate_bps"`
	Quantity           int    `json:"quantity"`
	UnitPriceAmount    int64  `json:"unit_price_amount"`
	Note               string `json:"note"`
}

type placeOrderRequest struct {
	BranchID     string           `json:"branch_id"`
	CheckID      string           `json:"check_id"`
	OrderChannel string           `json:"order_channel"`
	Items        []OrderItemInput `json:"items"`
}

// PlaceOrder calls POST /api/v1/pos/orders (Idempotency-Key required —
// ADR-SEC-003). branch_id is required by the handler (422 if empty) and
// order_channel must be one of pos/domain's OrderChannel values — this
// always sends "dine_in", the only channel that applies to a table check
// (takeaway/delivery orders are not opened against a check in this flow).
func (c *Client) PlaceOrder(ctx context.Context, branchID, checkID string, items []OrderItemInput) (Order, error) {
	if checkID == "" {
		return Order{}, fmt.Errorf("apiclient: place order: check_id is required")
	}
	if len(items) == 0 {
		return Order{}, fmt.Errorf("apiclient: place order: items is required")
	}
	var out Order
	req := placeOrderRequest{
		BranchID:     branchID,
		CheckID:      checkID,
		OrderChannel: "dine_in",
		Items:        items,
	}
	if err := c.doIdempotent(ctx, http.MethodPost, "/api/v1/pos/orders", req, &out); err != nil {
		return Order{}, fmt.Errorf("apiclient: place order: %w", err)
	}
	return out, nil
}

// --- Payments ---

// Payment is what POST /api/v1/payments (registerSale) actually returns —
// domain.Payment has no json tags, so its field names serialize verbatim in
// PascalCase. This intentionally does NOT match GET /api/v1/payments'
// snake_case paymentResponse; see the file-level doc comment.
type Payment struct {
	ID              string     `json:"ID"`
	BranchID        string     `json:"BranchID"`
	CheckID         *string    `json:"CheckID"`
	Method          string     `json:"Method"`
	Status          string     `json:"Status"`
	AmountTotal     int64      `json:"AmountTotal"`
	Currency        string     `json:"Currency"`
	FiscalReceiptID *string    `json:"FiscalReceiptID"`
	CreatedAt       time.Time  `json:"CreatedAt"`
	CompletedAt     *time.Time `json:"CompletedAt"`
}

type registerSaleRequest struct {
	BranchID    string `json:"branch_id"`
	CheckID     string `json:"check_id"`
	Method      string `json:"method"`
	AmountTotal int64  `json:"amount_total"`
	Currency    string `json:"currency"`
}

// RegisterCashPayment calls POST /api/v1/payments (Idempotency-Key
// required — ADR-SEC-003) with method "cash". branch_id is required by the
// handler (422 if empty); amount_total must be > 0 (payment/service
// validation) and is always the check total in kuruş (smallest currency
// unit), matching pos/repo.CheckRepo.GetTotal's sum(quantity*unit_price) —
// there is no server-side "check total" endpoint, so the caller (App) is
// responsible for deriving it the same way.
func (c *Client) RegisterCashPayment(ctx context.Context, branchID, checkID string, amountTotal int64) (Payment, error) {
	if checkID == "" {
		return Payment{}, fmt.Errorf("apiclient: register cash payment: check_id is required")
	}
	if amountTotal <= 0 {
		return Payment{}, fmt.Errorf("apiclient: register cash payment: amount_total must be positive")
	}
	var out Payment
	req := registerSaleRequest{
		BranchID:    branchID,
		CheckID:     checkID,
		Method:      "cash",
		AmountTotal: amountTotal,
		Currency:    "TRY",
	}
	if err := c.doIdempotent(ctx, http.MethodPost, "/api/v1/payments", req, &out); err != nil {
		return Payment{}, fmt.Errorf("apiclient: register cash payment: %w", err)
	}
	return out, nil
}
