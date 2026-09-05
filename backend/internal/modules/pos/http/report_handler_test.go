package http

// Table-driven unit tests for saleDetails' input-validation branches — pure
// httptest, no service/DB involved (h.reports is left nil; a test that
// somehow reached it would panic, which is itself evidence the branch under
// test failed to short-circuit before calling the service, matching
// order_ids_test.go's "servise çağrılmadan dönmeli" contract for its own
// batch-read validation).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

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
		name    string
		query   string
		wantMsg string
	}{
		{
			name:    "missing branch_id",
			query:   "from=2026-09-04T00:00:00Z&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantMsg: "invalid branch_id",
		},
		{
			name:    "malformed branch_id",
			query:   "branch_id=not-a-uuid&from=2026-09-04T00:00:00Z&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantMsg: "invalid branch_id",
		},
		{
			name:    "missing from",
			query:   "branch_id=" + branchID + "&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantMsg: "from and to are required (RFC3339)",
		},
		{
			name:    "missing to",
			query:   "branch_id=" + branchID + "&from=2026-09-04T00:00:00Z&tz=Europe/Istanbul",
			wantMsg: "from and to are required (RFC3339)",
		},
		{
			name:    "malformed from",
			query:   "branch_id=" + branchID + "&from=not-a-time&to=2026-09-05T00:00:00Z&tz=Europe/Istanbul",
			wantMsg: "from and to are required (RFC3339)",
		},
		{
			name:    "malformed to",
			query:   "branch_id=" + branchID + "&from=2026-09-04T00:00:00Z&to=not-a-time&tz=Europe/Istanbul",
			wantMsg: "from and to are required (RFC3339)",
		},
		{
			name:    "non-RFC3339 date-only from/to",
			query:   "branch_id=" + branchID + "&from=2026-09-04&to=2026-09-05&tz=Europe/Istanbul",
			wantMsg: "from and to are required (RFC3339)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.saleDetails(rec, newSaleDetailsRequest(tt.query))

			require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
			assert.Equal(t, tt.wantMsg+"\n", rec.Body.String())
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
