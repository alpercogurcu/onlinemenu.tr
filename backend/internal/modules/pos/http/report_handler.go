package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/service"
)

// saleDetails handles GET /api/v1/pos/reports/sale-details — the day-end
// sales report (ReportService.SaleDetails). Query parsing here is limited to
// presence/format (uuid.Parse, time.Parse RFC3339); range/timezone/business
// validation is ReportService's job (see its doc comment on validation
// order), reached via h.error below.
func (h *Handler) saleDetails(w http.ResponseWriter, r *http.Request) {
	p, ok := requirePrincipal(w, r)
	if !ok {
		return
	}

	branchID, err := uuid.Parse(r.URL.Query().Get("branch_id"))
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, codeInvalidBranchID, "invalid branch_id")
		return
	}

	fromRaw := r.URL.Query().Get("from")
	toRaw := r.URL.Query().Get("to")
	from, fromErr := time.Parse(time.RFC3339, fromRaw)
	to, toErr := time.Parse(time.RFC3339, toRaw)
	if fromRaw == "" || toRaw == "" || fromErr != nil || toErr != nil {
		respondError(w, http.StatusUnprocessableEntity, codeInvalidDateParams, "from and to are required (RFC3339)")
		return
	}

	tz := resolveReportTZ(r.URL.Query().Get("tz"))

	details, err := h.reports.SaleDetails(r.Context(), p, service.SaleDetailsRequest{
		BranchID: branchID,
		From:     from,
		To:       to,
		TZ:       tz,
	})
	if err != nil {
		h.reportError(w, r, err)
		return
	}

	respondJSON(w, http.StatusOK, toSaleDetailsResponse(branchID, from, to, tz, details))
}

// reportError maps a SaleDetails error to a response. Its 403 is handled
// here, not in h.error: every other endpoint's ErrBranchForbidden stays a
// generic text/plain 403 (h.error's existing mapping, left untouched), but
// this endpoint's 422s already carry a machine-readable code (see
// saleDetails above), so its 403 gets one too rather than mixing conventions
// within one response contract. Split out from saleDetails so the mapping is
// unit-testable without a live *service.ReportService (see
// report_handler_test.go).
func (h *Handler) reportError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, pub.ErrBranchForbidden) {
		respondError(w, http.StatusForbidden, "branch_forbidden", "forbidden")
		return
	}
	h.error(w, r, err)
}

// resolveReportTZ applies service.DefaultReportTZ (tz is optional — pilot
// scope is Turkey-only) when the query omitted it, so the value used to
// build both the service request AND the echoed response "tz" field is the
// same resolved zone, not the raw (possibly empty) query string.
// ReportService.SaleDetails applies the identical default internally for any
// other caller of the service — see DefaultReportTZ's doc comment — this
// mirrors it here only so the handler has a concrete value to echo back.
func resolveReportTZ(raw string) string {
	if raw == "" {
		return service.DefaultReportTZ
	}
	return raw
}

// ---------------------------------------------------------------------------
// Response DTOs
// ---------------------------------------------------------------------------

type saleDetailsResponse struct {
	BranchID      uuid.UUID              `json:"branch_id"`
	From          time.Time              `json:"from"`
	To            time.Time              `json:"to"`
	TZ            string                 `json:"tz"`
	Sales         saleTotalsResponse     `json:"sales"`
	Cancellations cancellationsResponse  `json:"cancellations"`
	ByTaxRate     []taxLineResponse      `json:"by_tax_rate"`
	ByDay         []dayLineResponse      `json:"by_day"`
	BySource      []sourceLineResponse   `json:"by_source"`
	Payments      []paymentTotalResponse `json:"payments"`
	CashSessions  []cashSessionResponse  `json:"cash_sessions"`
}

type saleTotalsResponse struct {
	ClosedCheckCount int64 `json:"closed_check_count"`
	Gross            int64 `json:"gross"`
	ItemCount        int64 `json:"item_count"`
	AverageCheck     int64 `json:"average_check"`
}

type cancellationsResponse struct {
	CheckCount int64 `json:"check_count"`
	Amount     int64 `json:"amount"`
}

type taxLineResponse struct {
	RateBPS int   `json:"rate_bps"`
	Gross   int64 `json:"gross"`
	Base    int64 `json:"base"`
	Tax     int64 `json:"tax"`
}

type dayLineResponse struct {
	Date       string `json:"date"`
	Gross      int64  `json:"gross"`
	CheckCount int64  `json:"check_count"`
}

type sourceLineResponse struct {
	Source     string `json:"source"`
	CheckCount int64  `json:"check_count"`
	Gross      int64  `json:"gross"`
}

type paymentTotalResponse struct {
	Method string `json:"method"`
	Status string `json:"status"`
	Count  int64  `json:"count"`
	Total  int64  `json:"total"`
}

type cashSessionResponse struct {
	ID                   uuid.UUID  `json:"id"`
	Status               string     `json:"status"`
	OpenedAt             time.Time  `json:"opened_at"`
	ClosedAt             *time.Time `json:"closed_at"`
	OpeningCountedAmount int64      `json:"opening_counted_amount"`
	CashPaymentsTaken    int64      `json:"cash_payments_taken"`
	MovementsNet         int64      `json:"movements_net"`
	ExpectedClose        int64      `json:"expected_close"`
	ClosingCountedAmount *int64     `json:"closing_counted_amount"`
	Difference           *int64     `json:"difference"`
}

func toSaleDetailsResponse(branchID uuid.UUID, from, to time.Time, tz string, d service.SaleDetails) saleDetailsResponse {
	byTaxRate := make([]taxLineResponse, len(d.ByTaxRate))
	for i, l := range d.ByTaxRate {
		byTaxRate[i] = taxLineResponse{RateBPS: l.RateBPS, Gross: l.Gross, Base: l.Base, Tax: l.Tax}
	}
	byDay := make([]dayLineResponse, len(d.ByDay))
	for i, l := range d.ByDay {
		byDay[i] = dayLineResponse{Date: l.Date, Gross: l.Gross, CheckCount: l.CheckCount}
	}
	bySource := make([]sourceLineResponse, len(d.BySource))
	for i, l := range d.BySource {
		bySource[i] = sourceLineResponse{Source: l.Source, CheckCount: l.CheckCount, Gross: l.Gross}
	}
	payments := make([]paymentTotalResponse, len(d.Payments))
	for i, pmt := range d.Payments {
		payments[i] = paymentTotalResponse{Method: pmt.Method, Status: pmt.Status, Count: pmt.Count, Total: pmt.Total}
	}
	sessions := make([]cashSessionResponse, len(d.CashSessions))
	for i, cs := range d.CashSessions {
		sessions[i] = toCashSessionResponse(cs)
	}

	return saleDetailsResponse{
		BranchID: branchID,
		From:     from,
		To:       to,
		TZ:       tz,
		Sales: saleTotalsResponse{
			ClosedCheckCount: d.ClosedCheckCount,
			Gross:            d.GrossSales,
			ItemCount:        d.ItemCount,
			AverageCheck:     d.AverageCheck,
		},
		Cancellations: cancellationsResponse{
			CheckCount: d.CancelledCheckCount,
			Amount:     d.CancelledAmount,
		},
		ByTaxRate:    byTaxRate,
		ByDay:        byDay,
		BySource:     bySource,
		Payments:     payments,
		CashSessions: sessions,
	}
}

func toCashSessionResponse(cs service.CashSessionSummary) cashSessionResponse {
	return cashSessionResponse{
		ID:                   cs.ID,
		Status:               cs.Status,
		OpenedAt:             cs.OpenedAt,
		ClosedAt:             cs.ClosedAt,
		OpeningCountedAmount: cs.OpeningCountedAmount,
		CashPaymentsTaken:    cs.CashPaymentsTaken,
		MovementsNet:         cs.MovementsNet,
		ExpectedClose:        cs.ExpectedClose,
		ClosingCountedAmount: cs.ClosingCountedAmount,
		Difference:           cs.Difference,
	}
}
