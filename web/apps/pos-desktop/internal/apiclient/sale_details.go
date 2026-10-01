package apiclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Day-end sales report (GET /api/v1/pos/reports/sale-details) — the source of
// the kasa kapanış screen's GÜN ÖZETİ section. The wire structs below mirror
// the backend's pos/http report_handler.go response DTOs 1:1 for the fields
// the POS consumes (same verified-against-source discipline as pos.go's
// file-level comment); the response's remaining report sections
// (cancellations, by_tax_rate, by_day, by_source, cash_sessions) are
// deliberately not mirrored — json.Unmarshal ignores them, and the POS has no
// screen that reads them. Add them here the day a screen does.

// SaleTotals mirrors report_handler.go's saleTotalsResponse. All amounts are
// kuruş.
type SaleTotals struct {
	ClosedCheckCount int64 `json:"closed_check_count"`
	Gross            int64 `json:"gross"`
	ItemCount        int64 `json:"item_count"`
	AverageCheck     int64 `json:"average_check"`
}

// PaymentTotal mirrors report_handler.go's paymentTotalResponse — one
// (method, status) bucket of the window's payments. Method/Status values are
// payment/domain's PaymentMethod/PaymentStatus strings ("cash", "terminal",
// …; "completed", "voided", …), passed through verbatim.
type PaymentTotal struct {
	Method string `json:"method"`
	Status string `json:"status"`
	Count  int64  `json:"count"`
	Total  int64  `json:"total"`
}

// SaleDetails is the POS-consumed subset of report_handler.go's
// saleDetailsResponse — see this file's header comment for why it is a
// subset.
type SaleDetails struct {
	Sales    SaleTotals     `json:"sales"`
	Payments []PaymentTotal `json:"payments"`
}

// GetSaleDetails calls GET /api/v1/pos/reports/sale-details for branchID over
// [from, to]. The backend requires both bounds as RFC3339 (422
// invalid_date_params otherwise) and authorizes per-branch (403
// branch_forbidden) — both surface as *APIError here, unchanged. tz is left
// to the backend's default (service.DefaultReportTZ — pilot scope is
// Turkey-only, same reasoning as the handler's resolveReportTZ).
func (c *Client) GetSaleDetails(ctx context.Context, branchID string, from, to time.Time) (SaleDetails, error) {
	if branchID == "" {
		return SaleDetails{}, fmt.Errorf("apiclient: get sale details: branch_id is required")
	}
	if from.IsZero() || to.IsZero() {
		return SaleDetails{}, fmt.Errorf("apiclient: get sale details: from and to are required")
	}
	q := url.Values{
		"branch_id": {branchID},
		"from":      {from.Format(time.RFC3339)},
		"to":        {to.Format(time.RFC3339)},
	}
	var out SaleDetails
	path := "/api/v1/pos/reports/sale-details?" + q.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return SaleDetails{}, fmt.Errorf("apiclient: get sale details: %w", err)
	}
	return out, nil
}
