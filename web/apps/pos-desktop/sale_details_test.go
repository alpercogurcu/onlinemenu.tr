package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// Exercises App.GetSaleDetails against an httptest fake backend, built as an
// App literal like app_test.go's newTestApp (no Wails runtime needed — this
// binding never touches it).
func newSaleDetailsTestApp(t *testing.T, backendBaseURL string) *App {
	t.Helper()
	return &App{
		ctx: context.Background(),
		api: apiclient.New(backendBaseURL, tokenstore.New(t.TempDir(), nil)),
	}
}

func TestApp_GetSaleDetails_TranslatesAndKeepsPaymentsNonNil(t *testing.T) {
	const body = `{
	  "sales": {"closed_check_count": 18, "gross": 1482000, "item_count": 64, "average_check": 82333},
	  "payments": [
	    {"method": "cash", "status": "completed", "count": 9, "total": 624000},
	    {"method": "terminal", "status": "completed", "count": 9, "total": 858000}
	  ]
	}`

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	a := newSaleDetailsTestApp(t, srv.URL)
	// opened_at arrives from CashSessionDTO formatted as rfc3339Millis — the
	// binding must accept fractional seconds, not just bare RFC3339.
	got, err := a.GetSaleDetails("22222222-2222-2222-2222-222222222222", "2026-10-01T06:00:00.000Z", "2026-10-01T20:15:30.123Z")
	if err != nil {
		t.Fatalf("GetSaleDetails: %v", err)
	}

	if !strings.Contains(gotQuery, "branch_id=22222222-2222-2222-2222-222222222222") {
		t.Errorf("query = %q, want branch_id", gotQuery)
	}
	if got.Sales.ClosedCheckCount != 18 || got.Sales.Gross != 1482000 {
		t.Errorf("sales = %+v", got.Sales)
	}
	if len(got.Payments) != 2 || got.Payments[0].Method != "cash" || got.Payments[1].Total != 858000 {
		t.Errorf("payments = %+v", got.Payments)
	}
}

func TestApp_GetSaleDetails_EmptyPaymentsStaysNonNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sales": {"closed_check_count": 0, "gross": 0, "item_count": 0, "average_check": 0}, "payments": []}`))
	}))
	defer srv.Close()

	a := newSaleDetailsTestApp(t, srv.URL)
	got, err := a.GetSaleDetails("b-1", "2026-10-01T06:00:00Z", "2026-10-01T20:00:00Z")
	if err != nil {
		t.Fatalf("GetSaleDetails: %v", err)
	}
	// nil would marshal to JSON null and crash a frontend .filter/.map — see
	// SaleDetailsDTO's doc comment.
	if got.Payments == nil {
		t.Fatal("Payments must be a non-nil slice")
	}
}

func TestApp_GetSaleDetails_ValidatesArgsBeforeCallingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("must not reach the server with invalid args")
	}))
	defer srv.Close()

	a := newSaleDetailsTestApp(t, srv.URL)
	if _, err := a.GetSaleDetails("", "2026-10-01T06:00:00Z", "2026-10-01T20:00:00Z"); err == nil {
		t.Error("expected an error for an empty branch_id")
	}
	if _, err := a.GetSaleDetails("b-1", "dün sabah", "2026-10-01T20:00:00Z"); err == nil {
		t.Error("expected an error for a malformed from")
	}
	if _, err := a.GetSaleDetails("b-1", "2026-10-01T06:00:00Z", ""); err == nil {
		t.Error("expected an error for an empty to")
	}
}
