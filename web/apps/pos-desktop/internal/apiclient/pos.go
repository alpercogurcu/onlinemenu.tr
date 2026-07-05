package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
// All three payment endpoints (registerSale, getPayment, listPayments) now
// respond with the same snake_case paymentResponse DTO — the previous
// PascalCase-vs-snake_case asymmetry (registerSale/getPayment serializing
// the raw untagged domain.Payment struct) was a backend bug, fixed
// alongside this client update.

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
	BranchID   string  `json:"branch_id"`
	TableID    *string `json:"table_id,omitempty"`
	TableLabel string  `json:"table_label"`
	Note       string  `json:"note"`
}

// OpenCheck calls POST /api/v1/pos/checks. tableID is optional (Sprint-5
// Wave 2's masa planı — a table selected from ListTables); pass "" for
// masasız satış (takeaway/delivery), which the backend leaves TableID nil
// for. TableID is sent as *string rather than *uuid.UUID so an empty tableID
// omits the JSON key entirely (json:",omitempty" on a *uuid.UUID would still
// need a valid UUID string if set to a non-nil zero value — using *string
// makes "no table" unambiguous instead of risking a 400 from an empty-string
// uuid decode on the backend's *uuid.UUID field).
//
// Not idempotency-key-gated on the backend (pos/http.RegisterRoutes: only
// close and order-place carry httpx.Idempotency) and has no natural
// client-side dedup key today, so a retry here is left to the caller rather
// than silently risking a duplicate check on a timeout.
func (c *Client) OpenCheck(ctx context.Context, branchID, tableID, tableLabel, note string) (Check, error) {
	var out Check
	req := openCheckRequest{BranchID: branchID, TableLabel: tableLabel, Note: note}
	if tableID != "" {
		req.TableID = &tableID
	}
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
// required — ADR-SEC-003). When the check is underpaid, the backend now
// returns 409 Conflict (pos/service.CheckService.Close's
// ErrInsufficientPayment, mapped by the HTTP handler) — a distinguishable
// *APIError with StatusCode 409, not the opaque 500 an earlier backend
// version returned. Since 409 < 500, doIdempotent correctly does not retry
// it (see that method's doc comment): retrying a still-underpaid check
// cannot change the outcome.
func (c *Client) CloseCheck(ctx context.Context, checkID string) (Check, error) {
	var out Check
	path := fmt.Sprintf("/api/v1/pos/checks/%s/close", checkID)
	if err := c.doIdempotent(ctx, http.MethodPost, path, nil, &out); err != nil {
		return Check{}, fmt.Errorf("apiclient: close check: %w", err)
	}
	return out, nil
}

// --- Table plan (masa planı, Sprint-5 Wave 2) ---

// Table mirrors pos/http tableResponse — one floor-plan row: the table
// itself plus the id of the check currently open against it (nil when the
// table is not occupied). LayoutPosition is decoded (kept 1:1 with the
// backend shape per this file's doc comment) but is not consumed by the
// pos-desktop UI yet — the cash register draws a grid layout, not a free
// placement editor; that is a separate, later piece of work.
type Table struct {
	ID             string          `json:"id"`
	BranchID       string          `json:"branch_id"`
	ZoneID         string          `json:"zone_id"`
	Name           string          `json:"name"`
	Capacity       int             `json:"capacity"`
	Status         string          `json:"status"`
	LayoutPosition json.RawMessage `json:"layout_position"`
	IsActive       bool            `json:"is_active"`
	ActiveCheckID  *string         `json:"active_check_id"`
}

// ZonePlan mirrors pos/http zonePlanResponse — GET /tables's actual response
// shape: the branch's floor plan grouped by zone, in the backend's own
// (floor, zone name, then table name) order.
type ZonePlan struct {
	ZoneID   string  `json:"zone_id"`
	ZoneName string  `json:"zone_name"`
	Floor    int     `json:"floor"`
	Tables   []Table `json:"tables"`
}

// ListTables calls GET /api/v1/pos/tables?branch_id={branchID}, returning
// the whole branch floor plan (zones + tables) in one request — the shape
// the cash register's masa planı screen draws directly, no client-side
// grouping needed (see toZonePlanResponse's ordering guarantee on the
// backend). branchID is required — the handler 422s without it.
func (c *Client) ListTables(ctx context.Context, branchID string) ([]ZonePlan, error) {
	if branchID == "" {
		return nil, fmt.Errorf("apiclient: list tables: branch_id is required")
	}
	var out []ZonePlan
	path := "/api/v1/pos/tables?" + url.Values{"branch_id": {branchID}}.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, fmt.Errorf("apiclient: list tables: %w", err)
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

// Payment mirrors payment/http paymentResponse — the single snake_case DTO
// now shared by registerSale, getPayment, and listPayments alike.
type Payment struct {
	ID              string     `json:"id"`
	BranchID        string     `json:"branch_id"`
	CheckID         *string    `json:"check_id"`
	Method          string     `json:"method"`
	Status          string     `json:"status"`
	AmountTotal     int64      `json:"amount_total"`
	Currency        string     `json:"currency"`
	FiscalReceiptID *string    `json:"fiscal_receipt_id"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at"`
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

// listPaymentsResponse mirrors payment/http listPayments' envelope
// (map[string]any{"payments": out}).
type listPaymentsResponse struct {
	Payments []Payment `json:"payments"`
}

// ListCheckPayments calls GET /api/v1/payments?check_id={checkID} — completed
// payments already recorded against a check. This is the read a cashier
// needs before accepting a new cash payment on a reopened adisyon: without
// it, a check that was already partially or fully paid (e.g. the station
// crashed/restarted after RegisterCashPayment succeeded but before
// CloseCheck) offers no way to see that from the client, risking the
// cashier registering the same amount twice. Requires "payment.payment.read"
// — see pos.go's ListCheckPayments doc comment for the role-grant caveat.
func (c *Client) ListCheckPayments(ctx context.Context, checkID string) ([]Payment, error) {
	if checkID == "" {
		return nil, fmt.Errorf("apiclient: list check payments: check_id is required")
	}
	var out listPaymentsResponse
	path := "/api/v1/payments?" + url.Values{"check_id": {checkID}}.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, fmt.Errorf("apiclient: list check payments: %w", err)
	}
	return out.Payments, nil
}
