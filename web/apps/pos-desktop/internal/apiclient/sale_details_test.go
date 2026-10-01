package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

func TestClient_GetSaleDetails_DecodesPOSSubset(t *testing.T) {
	// The full backend response, including sections the POS deliberately does
	// not mirror (cancellations, by_tax_rate, …) — decoding must ignore them
	// rather than choke, since the wire struct is a subset by design (see
	// sale_details.go's header comment).
	const body = `{
	  "branch_id": "22222222-2222-2222-2222-222222222222",
	  "from": "2026-10-01T06:00:00Z",
	  "to": "2026-10-01T20:00:00Z",
	  "tz": "Europe/Istanbul",
	  "sales": {"closed_check_count": 18, "gross": 1482000, "item_count": 64, "average_check": 82333},
	  "cancellations": {"check_count": 1, "amount": 12000},
	  "by_tax_rate": [{"rate_bps": 1000, "gross": 1482000, "base": 1347273, "tax": 134727}],
	  "by_day": [{"date": "2026-10-01", "gross": 1482000, "check_count": 18}],
	  "by_source": [{"source": "pos", "check_count": 18, "gross": 1482000}],
	  "payments": [
	    {"method": "cash", "status": "completed", "count": 9, "total": 624000},
	    {"method": "terminal", "status": "completed", "count": 9, "total": 858000},
	    {"method": "cash", "status": "voided", "count": 1, "total": 5000}
	  ],
	  "cash_sessions": [{"id": "44444444-4444-4444-4444-444444444444", "status": "opened"}]
	}`

	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	from := time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)
	got, err := c.GetSaleDetails(context.Background(), "22222222-2222-2222-2222-222222222222", from, to)
	if err != nil {
		t.Fatalf("GetSaleDetails: %v", err)
	}

	if gotPath != "/api/v1/pos/reports/sale-details" {
		t.Errorf("path = %q, want /api/v1/pos/reports/sale-details", gotPath)
	}
	if gotQuery.Get("branch_id") != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("branch_id = %q", gotQuery.Get("branch_id"))
	}
	// The backend's handler rejects anything time.Parse(time.RFC3339) cannot
	// read with a 422 — assert the client sends exactly that format.
	if gotQuery.Get("from") != "2026-10-01T06:00:00Z" || gotQuery.Get("to") != "2026-10-01T20:00:00Z" {
		t.Errorf("from/to = %q / %q, want RFC3339", gotQuery.Get("from"), gotQuery.Get("to"))
	}

	if got.Sales.ClosedCheckCount != 18 || got.Sales.Gross != 1482000 {
		t.Errorf("sales = %+v", got.Sales)
	}
	if len(got.Payments) != 3 {
		t.Fatalf("payments len = %d, want 3: %+v", len(got.Payments), got.Payments)
	}
	if got.Payments[1].Method != "terminal" || got.Payments[1].Status != "completed" || got.Payments[1].Total != 858000 {
		t.Errorf("payments[1] = %+v", got.Payments[1])
	}
	if got.Payments[2].Status != "voided" {
		t.Errorf("payments[2].Status = %q, want voided (statuses pass through verbatim)", got.Payments[2].Status)
	}
}

func TestClient_GetSaleDetails_RejectsMissingArgsBeforeCallingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("must not reach the server with missing args")
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	now := time.Now()

	if _, err := c.GetSaleDetails(context.Background(), "", now, now); err == nil {
		t.Error("expected an error for an empty branch_id")
	}
	if _, err := c.GetSaleDetails(context.Background(), "b-1", time.Time{}, now); err == nil {
		t.Error("expected an error for a zero from")
	}
	if _, err := c.GetSaleDetails(context.Background(), "b-1", now, time.Time{}); err == nil {
		t.Error("expected an error for a zero to")
	}
}
