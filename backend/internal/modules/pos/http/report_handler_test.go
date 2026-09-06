package http

// Table-driven unit tests for saleDetails' input-validation branches — pure
// httptest, no service/DB involved (h.reports is left nil; a test that
// somehow reached it would panic, which is itself evidence the branch under
// test failed to short-circuit before calling the service, matching
// order_ids_test.go's "servise çağrılmadan dönmeli" contract for its own
// batch-read validation).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/pos/domain"
	pub "onlinemenu.tr/internal/modules/pos/public"
	"onlinemenu.tr/internal/modules/pos/service"
	"onlinemenu.tr/internal/platform/auth"
)

func newSaleDetailsRequest(query string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/reports/sale-details?"+query, nil)
	return req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{
		PersonID: uuid.New(),
		Ctx:      auth.ContextStaff,
		TenantID: uuid.New(),
		BranchID: uuid.New(),
	}))
}

func TestSaleDetails_ValidationErrors(t *testing.T) {
	h := &Handler{logger: zap.NewNop()}
	branchID := uuid.New().String()

	tests := []struct {
		name     string
		query    string
		wantCode string
		wantMsg  string
	}{
		{
			name:     "missing branch_id",
			query:    "from=2026-09-04T00:00:00Z&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantCode: codeInvalidBranchID,
			wantMsg:  "invalid branch_id",
		},
		{
			name:     "malformed branch_id",
			query:    "branch_id=not-a-uuid&from=2026-09-04T00:00:00Z&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantCode: codeInvalidBranchID,
			wantMsg:  "invalid branch_id",
		},
		{
			name:     "missing from",
			query:    "branch_id=" + branchID + "&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantCode: codeInvalidDateParams,
			wantMsg:  "from and to are required (RFC3339)",
		},
		{
			name:     "missing to",
			query:    "branch_id=" + branchID + "&from=2026-09-04T00:00:00Z&tz=Europe/Istanbul",
			wantCode: codeInvalidDateParams,
			wantMsg:  "from and to are required (RFC3339)",
		},
		{
			name:     "malformed from",
			query:    "branch_id=" + branchID + "&from=not-a-time&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantCode: codeInvalidDateParams,
			wantMsg:  "from and to are required (RFC3339)",
		},
		{
			name:     "malformed to",
			query:    "branch_id=" + branchID + "&from=2026-09-04T00:00:00Z&to=not-a-time&tz=Europe/Istanbul",
			wantCode: codeInvalidDateParams,
			wantMsg:  "from and to are required (RFC3339)",
		},
		{
			name:     "non-RFC3339 date-only from/to",
			query:    "branch_id=" + branchID + "&from=2026-09-04&to=2026-09-05&tz=Europe/Istanbul",
			wantCode: codeInvalidDateParams,
			wantMsg:  "from and to are required (RFC3339)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.saleDetails(rec, newSaleDetailsRequest(tt.query))

			require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
			var body errorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, tt.wantCode, body.Code)
			assert.Equal(t, tt.wantMsg, body.Error)
		})
	}
}

// TestSaleDetails_Unauthorized pins the shared requirePrincipal guard (same
// as every other handler in this package): no principal in context -> 401,
// before any query parsing happens.
func TestSaleDetails_Unauthorized(t *testing.T) {
	h := &Handler{logger: zap.NewNop()}
	req := httptest.NewRequest(http.MethodGet, "/reports/sale-details", nil)
	rec := httptest.NewRecorder()

	h.saleDetails(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestReportError_BranchForbidden pins the report endpoint's own 403 body:
// unlike every other endpoint (h.error's generic text/plain "forbidden",
// left unchanged), a branch-forbidden SaleDetails error gets a
// machine-readable code here (see reportError's doc comment in
// report_handler.go) — tested directly against h.reportError rather than
// through h.saleDetails, since h.reports is a concrete *service.ReportService
// with no DB-free fake available in this package (same constraint noted at
// the top of this file).
func TestReportError_BranchForbidden(t *testing.T) {
	h := &Handler{logger: zap.NewNop()}
	rec := httptest.NewRecorder()
	req := newSaleDetailsRequest("branch_id=" + uuid.New().String())

	h.reportError(rec, req, pub.ErrBranchForbidden)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var body errorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "branch_forbidden", body.Code)
	assert.Equal(t, "forbidden", body.Error)
}

// TestResolveReportTZ pins the tz-optional contract (task-4 fix round 1: tz
// is optional per the brief, defaulting to service.DefaultReportTZ, not a
// 422 "invalid tz" — that error is now reserved for a non-empty garbage
// value, see ReportService.SaleDetails / TestReportService_SaleDetails_
// InvalidTimezone). Tested as a pure function here — same layering as
// TestParseOrderIDs in order_ids_test.go — rather than via a full
// saleDetails request, because h.reports is a concrete *service.ReportService
// with no DB-free fake available in this package; the "empty tz actually
// reaches the service as the default and the store sees it" half of the
// contract is covered instead by
// TestReportService_SaleDetails_DefaultsEmptyTZ in the service package.
func TestResolveReportTZ(t *testing.T) {
	assert.Equal(t, service.DefaultReportTZ, resolveReportTZ(""))
	assert.Equal(t, "Europe/Istanbul", resolveReportTZ(""))
	assert.Equal(t, "UTC", resolveReportTZ("UTC"), "a non-empty value passes through unchanged, even one ReportService will later reject")
	assert.Equal(t, "Not/AZone", resolveReportTZ("Not/AZone"), "resolveReportTZ does not validate — that is ReportService's job")
}

// TestToSaleDetailsResponse_ArraysAreNeverNull pins the JSON array contract
// (task-2-review follow-up): paymentpub.SalesSummaryReader returns nil
// slices for an empty window/branch, and domain.SalesSummary's own
// ByTaxRate/ByDay/BySource can in principle be nil too (its zero value is);
// toSaleDetailsResponse must normalize every one of the five array fields to
// an empty JSON array, never `null`, regardless of what upstream returns —
// a POS/admin client iterating `by_tax_rate`/`payments`/etc without a nil
// check must never see null. Verified by round-tripping through
// encoding/json rather than string-matching: decoding null into interface{}
// yields a Go nil, decoding [] yields a non-nil empty slice, so the
// distinction survives the round trip exactly the way a real client's JSON
// parser would see it.
// TestToSaleDetailsResponse_FullBody pins the ENTIRE 200 response body — not
// just the array-nullness slice (see TestToSaleDetailsResponse_ArraysAreNeverNull
// below) — against the brief's example JSON shape (task-4 brief, Step 3's
// worked example), using the exact same figures. Every field name and every
// value round-trips through the real toSaleDetailsResponse + encoding/json,
// so a field rename, a wrong json tag, or a dropped value in
// toSaleDetailsResponse/toCashSessionResponse fails this test — the five
// individual DTO-conversion loops are exercised together here, not in
// isolation.
func TestToSaleDetailsResponse_FullBody(t *testing.T) {
	branchID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	sessionID := uuid.MustParse("66666666-7777-8888-9999-aaaaaaaaaaaa")
	from := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	openedAt := time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC)
	closedAt := time.Date(2026, 9, 4, 20, 0, 0, 0, time.UTC)
	closingCounted := int64(52500)
	difference := int64(0)

	details := service.SaleDetails{
		SalesSummary: domain.SalesSummary{
			ClosedCheckCount:    2,
			CancelledCheckCount: 1,
			GrossSales:          28000,
			CancelledAmount:     4000,
			ItemCount:           4,
			ByTaxRate:           []domain.TaxLine{{RateBPS: 1000, Gross: 23000, Base: 20909, Tax: 2091}},
			ByDay:               []domain.DayLine{{Date: "2026-09-04", Gross: 25000, CheckCount: 1}},
			BySource:            []domain.SourceLine{{Source: "pos", CheckCount: 1, Gross: 25000}},
		},
		AverageCheck: 14000,
		Payments:     []service.PaymentTotal{{Method: "cash", Status: "completed", Count: 2, Total: 3500}},
		CashSessions: []service.CashSessionSummary{{
			ID:                   sessionID,
			Status:               "closed",
			OpenedAt:             openedAt,
			ClosedAt:             &closedAt,
			OpeningCountedAmount: 50000,
			CashPaymentsTaken:    3500,
			MovementsNet:         -1000,
			ExpectedClose:        52500,
			ClosingCountedAmount: &closingCounted,
			Difference:           &difference,
		}},
	}

	resp := toSaleDetailsResponse(branchID, from, to, "Europe/Istanbul", details)
	raw, err := json.Marshal(resp)
	require.NoError(t, err)

	wantJSON := fmt.Sprintf(`{
		"branch_id": %q, "from": %q, "to": %q, "tz": "Europe/Istanbul",
		"sales": {"closed_check_count": 2, "gross": 28000, "item_count": 4, "average_check": 14000},
		"cancellations": {"check_count": 1, "amount": 4000},
		"by_tax_rate": [{"rate_bps": 1000, "gross": 23000, "base": 20909, "tax": 2091}],
		"by_day": [{"date": "2026-09-04", "gross": 25000, "check_count": 1}],
		"by_source": [{"source": "pos", "check_count": 1, "gross": 25000}],
		"payments": [{"method": "cash", "status": "completed", "count": 2, "total": 3500}],
		"cash_sessions": [{
			"id": %q, "status": "closed",
			"opened_at": %q, "closed_at": %q,
			"opening_counted_amount": 50000, "cash_payments_taken": 3500,
			"movements_net": -1000, "expected_close": 52500,
			"closing_counted_amount": 52500, "difference": 0
		}]
	}`,
		branchID, from.Format(time.RFC3339Nano), to.Format(time.RFC3339Nano),
		sessionID, openedAt.Format(time.RFC3339Nano), closedAt.Format(time.RFC3339Nano))

	assert.JSONEq(t, wantJSON, string(raw))
}

func TestToSaleDetailsResponse_ArraysAreNeverNull(t *testing.T) {
	// service.SaleDetails{} zero value: domain.SalesSummary's ByTaxRate/
	// ByDay/BySource and Payments/CashSessions are all nil, matching the
	// "empty window / branch with no payments" case in practice.
	resp := toSaleDetailsResponse(uuid.New(), time.Now(), time.Now(), "Europe/Istanbul", service.SaleDetails{})

	raw, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	for _, field := range []string{"by_tax_rate", "by_day", "by_source", "payments", "cash_sessions"} {
		v, ok := decoded[field]
		require.Truef(t, ok, "field %q must be present in the response", field)
		require.NotNilf(t, v, "field %q must serialize as [] not null", field)
		arr, ok := v.([]any)
		require.Truef(t, ok, "field %q must be a JSON array, got %T", field, v)
		assert.Emptyf(t, arr, "field %q must be empty for a zero-value SaleDetails", field)
	}
}
