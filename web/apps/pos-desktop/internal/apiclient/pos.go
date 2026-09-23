package apiclient

import (
	"context"
	"encoding/json"
	"errors"
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
	// IsActive is false for a deactivated product. The category listing still
	// returns those, and the server refuses to sell them (invalid_order_line),
	// so the POS must not offer them (see sellableProducts in the pos-desktop
	// package).
	IsActive bool `json:"is_active"`
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
//
// branchID makes the response branch-effective (ADR-DATA-009): the grid shows
// this branch's own prices and hides what it does not sell, which is the same
// resolution POST /pos/orders re-runs server-side — without it the cashier
// would read the tenant price aloud and then get 422 price_mismatch. An empty
// branchID (chain-wide session) falls back to the tenant catalog.
func (c *Client) ListProducts(ctx context.Context, categoryID, branchID string) ([]Product, error) {
	var out []Product
	path := fmt.Sprintf("/api/v1/catalog/categories/%s/products", categoryID)
	if branchID != "" {
		path += "?branch_id=" + url.QueryEscape(branchID)
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, fmt.Errorf("apiclient: list products: %w", err)
	}
	return out, nil
}

// GetProduct calls GET /api/v1/catalog/products/{id}. The payment flow needs a
// product's tax rate, category and unit for the fiscal basket, and an order
// item carries none of them.
func (c *Client) GetProduct(ctx context.Context, productID string) (Product, error) {
	var out Product
	path := fmt.Sprintf("/api/v1/catalog/products/%s", url.PathEscape(productID))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Product{}, fmt.Errorf("apiclient: get product: %w", err)
	}
	return out, nil
}

// ModifierGroup mirrors catalog/http modifierGroupResponse (the subset the
// POS needs to render an option picker).
type ModifierGroup struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	SelectionType string `json:"selection_type"`
	MinSelections int16  `json:"min_selections"`
	MaxSelections *int16 `json:"max_selections"`
	IsRequired    bool   `json:"is_required"`
	SortOrder     int16  `json:"sort_order"`
}

// Modifier mirrors catalog/http modifierResponse. PriceDelta is the signed
// price difference in kuruş added to the product's base price.
type Modifier struct {
	ID         string `json:"id"`
	GroupID    string `json:"group_id"`
	Name       string `json:"name"`
	PriceDelta int64  `json:"price_delta"`
	IsActive   bool   `json:"is_active"`
	SortOrder  int16  `json:"sort_order"`
}

// ProductOptions mirrors catalog/http productOptionsResponse: one product's
// option groups in assignment order, each with its ACTIVE options. A group
// whose options are all inactive arrives with an empty Modifiers list.
type ProductOptions struct {
	ProductID string               `json:"product_id"`
	Groups    []ProductOptionGroup `json:"groups"`
}

// ProductOptionGroup is a ModifierGroup plus its active options.
type ProductOptionGroup struct {
	ModifierGroup
	Modifiers []Modifier `json:"modifiers"`
}

// ListProductOptions calls GET /api/v1/catalog/products/modifier-groups: the
// option tree of every sellable product in ONE request. It replaced the
// per-product group-id lookup plus per-group modifier lookup, which fanned a
// 30-tile grid out into dozens of requests against the production per-IP
// rate limit. branchID drops products that branch has switched off; "" asks
// for the principal's default branch.
func (c *Client) ListProductOptions(ctx context.Context, branchID string) ([]ProductOptions, error) {
	path := "/api/v1/catalog/products/modifier-groups"
	if branchID != "" {
		path += "?branch_id=" + url.QueryEscape(branchID)
	}
	var out []ProductOptions
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, fmt.Errorf("apiclient: list product options: %w", err)
	}
	// The endpoint only serves active options and omits the flag.
	for i := range out {
		for j := range out[i].Groups {
			for k := range out[i].Groups[j].Modifiers {
				out[i].Groups[j].Modifiers[k].IsActive = true
			}
		}
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
	// Total is in kuruş and only present on list/get responses (the backend
	// leaves it out of open/close/cancel/transfer/merge answers).
	Total *int64 `json:"total,omitempty"`
	// MergedIntoCheckID names the adisyon that absorbed this one when its
	// status is "merged".
	MergedIntoCheckID *string `json:"merged_into_check_id,omitempty"`
}

// ListOpenChecks calls GET /api/v1/pos/checks and filters to status "open"
// AND (when branchID is non-empty) to that branch.
//
// Since the 2026-09-20 branch-isolation fix the backend already forces a
// branch-scoped principal's own branch onto this list (pos/http listChecks +
// service.BranchScopeFilter), so for a counter station the branch filter here
// is now belt-and-braces. It stays because it is still load-bearing for a
// chain-wide session (branchID != "" while OPA resolves scope "tenant"), and
// because the endpoint remains status-agnostic — "open" is filtered here.
//
// The branch filter keeps the station from OFFERING a check it cannot use:
// PlaceOrder/RegisterCashPayment always send the calling station's own
// branchID alongside whatever check_id the cashier selected, and the backend
// now cross-validates both the check's status and its branch at write time
// (409 check_not_open / check_branch_mismatch). Filtering here means the
// cashier never picks a check that is about to be refused, rather than
// finding out after tapping "öde". Pass branchID="" only for a chain-wide
// staff session, which legitimately sees every branch.
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

// TransferCheck calls POST /api/v1/pos/checks/{id}/transfer: moves an open
// adisyon to another table. The backend flips both table statuses in the same
// transaction (the client cannot — that route needs pos.table.manage, which a
// cashier lacks). Idempotency-Key required (ADR-SEC-003); an identical retry
// returns the same result rather than moving twice. A conflict answers 409 with
// a machine-readable code ("table_occupied", "check_not_open", ...), which stays
// in the returned error text for the frontend's Turkish mapping.
func (c *Client) TransferCheck(ctx context.Context, checkID, tableID string) (Check, error) {
	if checkID == "" || tableID == "" {
		return Check{}, fmt.Errorf("apiclient: transfer check: check_id and table_id are required")
	}
	var out Check
	path := fmt.Sprintf("/api/v1/pos/checks/%s/transfer", url.PathEscape(checkID))
	if err := c.doIdempotent(ctx, http.MethodPost, path, map[string]string{"table_id": tableID}, &out); err != nil {
		return Check{}, fmt.Errorf("apiclient: transfer check: %w", err)
	}
	return out, nil
}

// MergeChecks calls POST /api/v1/pos/checks/{targetCheckID}/merge: folds the
// source adisyon into the target one. The path id is the SURVIVING check; the
// source becomes "merged". A source with any payment is refused (409
// "payments_present"). Idempotency-Key required.
func (c *Client) MergeChecks(ctx context.Context, targetCheckID, sourceCheckID string) (Check, error) {
	if targetCheckID == "" || sourceCheckID == "" {
		return Check{}, fmt.Errorf("apiclient: merge checks: target and source check ids are required")
	}
	var out Check
	path := fmt.Sprintf("/api/v1/pos/checks/%s/merge", url.PathEscape(targetCheckID))
	if err := c.doIdempotent(ctx, http.MethodPost, path, map[string]string{"source_check_id": sourceCheckID}, &out); err != nil {
		return Check{}, fmt.Errorf("apiclient: merge checks: %w", err)
	}
	return out, nil
}

// MoveCheckItems calls POST /api/v1/pos/checks/{sourceCheckID}/move-items: moves
// the chosen order items onto the target adisyon and answers the TARGET check.
// Idempotency-Key required.
func (c *Client) MoveCheckItems(ctx context.Context, sourceCheckID, targetCheckID string, orderItemIDs []string) (Check, error) {
	if sourceCheckID == "" || targetCheckID == "" {
		return Check{}, fmt.Errorf("apiclient: move items: source and target check ids are required")
	}
	if len(orderItemIDs) == 0 {
		return Check{}, fmt.Errorf("apiclient: move items: at least one order item is required")
	}
	var out Check
	path := fmt.Sprintf("/api/v1/pos/checks/%s/move-items", url.PathEscape(sourceCheckID))
	body := map[string]any{"target_check_id": targetCheckID, "order_item_ids": orderItemIDs}
	if err := c.doIdempotent(ctx, http.MethodPost, path, body, &out); err != nil {
		return Check{}, fmt.Errorf("apiclient: move items: %w", err)
	}
	return out, nil
}

// --- Table plan (masa planı, Sprint-5 Wave 2) ---

// SetTableStatus calls POST /api/v1/pos/tables/{id}/status. The counter uses it
// for one thing: turning a "cleaning" table (what closing an adisyon leaves
// behind) back to "empty" once it is wiped. Whether the caller's role may do
// that is the server's decision — a 403 stays in the error text ("status 403")
// so the frontend can say who may.
func (c *Client) SetTableStatus(ctx context.Context, tableID, status string) (Table, error) {
	if tableID == "" || status == "" {
		return Table{}, fmt.Errorf("apiclient: set table status: table id and status are required")
	}
	var out Table
	path := fmt.Sprintf("/api/v1/pos/tables/%s/status", url.PathEscape(tableID))
	if err := c.do(ctx, http.MethodPost, path, map[string]string{"status": status}, &out); err != nil {
		return Table{}, fmt.Errorf("apiclient: set table status: %w", err)
	}
	return out, nil
}

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

// GetOrder calls GET /api/v1/pos/orders/{id}. Used to rebuild a kitchen ticket
// for an order that was already placed (auto-print after PlaceOrder, or a
// reprint from the adisyon) without trusting whatever the UI still holds.
func (c *Client) GetOrder(ctx context.Context, orderID string) (Order, error) {
	if orderID == "" {
		return Order{}, fmt.Errorf("apiclient: get order: order id is required")
	}
	var out Order
	if err := c.do(ctx, http.MethodGet, "/api/v1/pos/orders/"+url.PathEscape(orderID), nil, &out); err != nil {
		return Order{}, fmt.Errorf("apiclient: get order: %w", err)
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
	// ModifierIDs lists the chosen options so the server can validate
	// UnitPriceAmount == product price + sum of their price deltas. Ignored
	// (inert) by a server that does not read it yet.
	ModifierIDs []string `json:"modifier_ids,omitempty"`
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

// FiscalLine mirrors payment/http fiscalLineRequest: one line of the fiscal
// basket. Quantity is in thousandths (1000 = 1 unit) and the tax rate in
// permyriad (1000 = 10.00%).
type FiscalLine struct {
	Name             string `json:"name"`
	UnitPriceMinor   int64  `json:"unit_price_minor"`
	QuantityMilli    int64  `json:"quantity_milli"`
	TaxRatePermyriad int    `json:"tax_rate_permyriad"`
	// CategoryID is omitted when empty: the backend field is a uuid.UUID, and
	// an empty string would fail to parse rather than mean "none".
	CategoryID string `json:"category_id,omitempty"`
	Unit       string `json:"unit,omitempty"`
}

type fiscalMetaRequest struct {
	TableLabel string `json:"table_label,omitempty"`
	WaiterName string `json:"waiter_name,omitempty"`
}

type registerSaleRequest struct {
	BranchID    string            `json:"branch_id"`
	CheckID     string            `json:"check_id"`
	Method      string            `json:"method"`
	AmountTotal int64             `json:"amount_total"`
	Currency    string            `json:"currency"`
	Lines       []FiscalLine      `json:"lines,omitempty"`
	Meta        fiscalMetaRequest `json:"meta"`
}

// ErrLinesTotalMismatch reports fiscal lines that do not add up to the payment
// amount. A device rejects such a basket ("TotalMinor must equal lines"), so it
// is refused here — before money is recorded — instead of surfacing later as a
// failed fiscal registration on an already-taken payment.
var ErrLinesTotalMismatch = errors.New("fiscal lines do not add up to the payment amount")

// RegisterPaymentInput is one payment against a check. Method is the backend's
// payment method ("cash", "terminal", ...). Lines are optional; when present
// they must add up to AmountTotal.
type RegisterPaymentInput struct {
	BranchID    string
	CheckID     string
	Method      string
	AmountTotal int64
	Lines       []FiscalLine
	TableLabel  string
}

func linesTotal(lines []FiscalLine) (int64, bool) {
	var total int64
	for _, l := range lines {
		product := l.UnitPriceMinor * l.QuantityMilli
		if product%1000 != 0 {
			return 0, false
		}
		total += product / 1000
	}
	return total, true
}

// RegisterPayment calls POST /api/v1/payments (Idempotency-Key required —
// ADR-SEC-003). branch_id is required by the handler (422 if empty);
// AmountTotal must be > 0. AmountTotal is ONE installment against the check —
// for a split/partial payment it is less than the check's full total (see
// pos/repo.CheckRepo.GetTotal's sum(quantity*unit_price), the same computation
// the frontend uses to derive the check's total and remaining balance — there
// is no server-side "check total" endpoint). Calling this more than once for
// the same check is expected and supported: the backend records each call as a
// separate payment, and CloseCheck only succeeds once the sum of all of them
// reaches the check total (payment/service.CheckService).
//
// The backend does NOT reject an amount above the check's remaining balance (no
// overpayment guard) — callers must clamp to the remaining balance themselves
// (see frontend/src/lib/payment.ts's clampToRemaining).
//
// Without Lines the backend synthesizes a single "Satis" line, which a real
// device rejects (payment_service.go buildFiscalSale); the payment screen
// therefore always sends them.
func (c *Client) RegisterPayment(ctx context.Context, in RegisterPaymentInput) (Payment, error) {
	if in.CheckID == "" {
		return Payment{}, fmt.Errorf("apiclient: register payment: check_id is required")
	}
	if in.AmountTotal <= 0 {
		return Payment{}, fmt.Errorf("apiclient: register payment: amount_total must be positive")
	}
	if len(in.Lines) > 0 {
		total, exact := linesTotal(in.Lines)
		if !exact || total != in.AmountTotal {
			return Payment{}, fmt.Errorf("apiclient: register payment: %w (lines %d, amount %d)", ErrLinesTotalMismatch, total, in.AmountTotal)
		}
	}
	var out Payment
	req := registerSaleRequest{
		BranchID:    in.BranchID,
		CheckID:     in.CheckID,
		Method:      in.Method,
		AmountTotal: in.AmountTotal,
		Currency:    "TRY",
		Lines:       in.Lines,
		Meta:        fiscalMetaRequest{TableLabel: in.TableLabel},
	}
	if err := c.doIdempotent(ctx, http.MethodPost, "/api/v1/payments", req, &out); err != nil {
		return Payment{}, fmt.Errorf("apiclient: register payment: %w", err)
	}
	return out, nil
}

// RegisterCashPayment registers a cash sale without a fiscal basket. Kept for
// callers that have no items to attach; the cashier flow uses RegisterPayment.
func (c *Client) RegisterCashPayment(ctx context.Context, branchID, checkID string, amountTotal int64) (Payment, error) {
	return c.RegisterPayment(ctx, RegisterPaymentInput{BranchID: branchID, CheckID: checkID, Method: "cash", AmountTotal: amountTotal})
}

// GetPayment calls GET /api/v1/payments/{id} — the ONLY way a client can
// observe the outcome of the asynchronous fiscal registration (ADR-FISCAL-002):
// POST /api/v1/payments now returns immediately with status "pending" and the
// ÖKC receipt is cut out-of-band, moving the payment to
// completed | failed | voided some time later (milliseconds for the mock
// adapter, minutes for a real device where a waiter must physically take the
// payment).
//
// ListCheckPayments cannot substitute for this: payment/repo.PaymentRepo's
// ListByCheck filters `status = 'completed'`, so a still-pending payment is
// invisible there — it simply does not appear in the list until it has already
// finished. Polling this endpoint per payment id is the only observation path.
//
// PERMISSION GAP (blocking for the plain "cashier" role — see pos.go's
// GetPayment doc comment): this requires "payment.payment.read", which
// backend/configs/opa/bundles/authz.rego grants to shift_manager/manager only.
// A cashier-only session gets 403 here, and therefore cannot observe its own
// payment reaching "completed". Callers must degrade rather than block.
func (c *Client) GetPayment(ctx context.Context, paymentID string) (Payment, error) {
	if paymentID == "" {
		return Payment{}, fmt.Errorf("apiclient: get payment: payment id is required")
	}
	var out Payment
	if err := c.do(ctx, http.MethodGet, "/api/v1/payments/"+url.PathEscape(paymentID), nil, &out); err != nil {
		return Payment{}, fmt.Errorf("apiclient: get payment: %w", err)
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
