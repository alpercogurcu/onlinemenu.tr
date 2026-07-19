package apiclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

func TestClient_ListBranchPendingFiscal_DecodesEnvelope(t *testing.T) {
	const body = `{
	  "branch_id": "22222222-2222-2222-2222-222222222222",
	  "as_of": "2026-07-19T10:00:00Z",
	  "pending": [
	    {"payment_id":"pay-1","check_id":"chk-1","amount_total":12500,
	     "registered_at":"2026-07-19T09:59:56Z","age_seconds":4}
	  ],
	  "recently_settled": [
	    {"payment_id":"pay-0","check_id":"chk-0","status":"failed","amount_total":4200,
	     "failure_reason":"ECR timeout after 30s","settled_at":"2026-07-19T09:58:00Z"}
	  ]
	}`

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.URL.Path != "/api/v1/payments/fiscal-pending" {
			t.Errorf("path = %q, want /api/v1/payments/fiscal-pending", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	got, err := c.ListBranchPendingFiscal(context.Background(), "22222222-2222-2222-2222-222222222222")
	if err != nil {
		t.Fatalf("ListBranchPendingFiscal: %v", err)
	}

	if gotQuery != "branch_id=22222222-2222-2222-2222-222222222222" {
		t.Errorf("query = %q, want branch_id=...", gotQuery)
	}
	if len(got.Pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(got.Pending))
	}
	if got.Pending[0].PaymentID != "pay-1" || got.Pending[0].AmountTotal != 12500 || got.Pending[0].AgeSeconds != 4 {
		t.Errorf("unexpected pending item: %+v", got.Pending[0])
	}
	if len(got.RecentlySettled) != 1 {
		t.Fatalf("recently_settled len = %d, want 1", len(got.RecentlySettled))
	}
	settled := got.RecentlySettled[0]
	if settled.Status != "failed" || settled.FailureReason != "ECR timeout after 30s" {
		t.Errorf("unexpected settled item: %+v", settled)
	}
	// amount_total on a settled item is what lets the frontend credit a payment
	// completed at ANOTHER station instead of offering its money for collection
	// a second time — a silent 0 here would reopen that exact hole.
	if settled.AmountTotal != 4200 {
		t.Errorf("settled amount_total = %d, want 4200", settled.AmountTotal)
	}
	if got.AsOf.IsZero() {
		t.Error("as_of did not decode")
	}
}

func TestClient_ListBranchPendingFiscal_RejectsMissingBranchIDBeforeCallingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("must not reach the server without a branch_id")
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	if _, err := c.ListBranchPendingFiscal(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an empty branch_id")
	}
}

// A 403 must surface as a typed *APIError so the poller can recognize it as
// the permanent, stop-the-loop condition it is (see main.isForbidden) rather
// than string-matching the message.
func TestClient_ListBranchPendingFiscal_ForbiddenSurfacesTypedAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	_, err := c.ListBranchPendingFiscal(context.Background(), "22222222-2222-2222-2222-222222222222")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an *APIError: %v", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
}

func TestClient_GetCheckSettlement_DecodesEnvelope(t *testing.T) {
	const body = `{
	  "check_id": "33333333-3333-3333-3333-333333333333",
	  "as_of": "2026-07-19T10:00:00Z",
	  "completed": [
	    {"payment_id":"pay-1","amount_total":12500},
	    {"payment_id":"pay-2","amount_total":4200}
	  ],
	  "pending_total": 900
	}`

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	got, err := c.GetCheckSettlement(context.Background(), "33333333-3333-3333-3333-333333333333")
	if err != nil {
		t.Fatalf("GetCheckSettlement: %v", err)
	}

	if want := "/api/v1/payments/checks/33333333-3333-3333-3333-333333333333/settlement"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if len(got.Completed) != 2 {
		t.Fatalf("completed len = %d, want 2", len(got.Completed))
	}
	if got.Completed[0].PaymentID != "pay-1" || got.Completed[0].AmountTotal != 12500 {
		t.Errorf("unexpected completed[0]: %+v", got.Completed[0])
	}
	// The amounts are what a cashier session credits against the check's
	// balance; a silently-zeroed value here would offer collected money for
	// collection a second time.
	if got.Completed[1].AmountTotal != 4200 {
		t.Errorf("completed[1] amount_total = %d, want 4200", got.Completed[1].AmountTotal)
	}
	if got.PendingTotal != 900 {
		t.Errorf("pending_total = %d, want 900", got.PendingTotal)
	}
	if got.AsOf.IsZero() {
		t.Error("as_of did not decode")
	}
}

// A check in ANOTHER branch answers 200 with an empty settlement rather than
// 403 (the backend refuses to confirm the id exists — see the handler's doc
// comment). It must decode as a clean empty result, not an error: the caller
// distinguishes "nothing collected" from "unreadable" by exactly this.
func TestClient_GetCheckSettlement_EmptyCompletedDecodesAsNoPayments(t *testing.T) {
	const body = `{"check_id":"33333333-3333-3333-3333-333333333333",
	  "as_of":"2026-07-19T10:00:00Z","completed":[],"pending_total":0}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	got, err := c.GetCheckSettlement(context.Background(), "33333333-3333-3333-3333-333333333333")
	if err != nil {
		t.Fatalf("GetCheckSettlement: %v", err)
	}
	if len(got.Completed) != 0 {
		t.Errorf("completed len = %d, want 0", len(got.Completed))
	}
	if got.PendingTotal != 0 {
		t.Errorf("pending_total = %d, want 0", got.PendingTotal)
	}
}

func TestClient_GetCheckSettlement_RejectsMissingCheckIDBeforeCallingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("must not reach the server without a check_id")
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	if _, err := c.GetCheckSettlement(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an empty check_id")
	}
}

// A session lacking payment.fiscal_status.read gets 403 here. It must surface
// as a typed *APIError like every other call, and the CALLER fails open — this
// read must never block a cashier from collecting.
func TestClient_GetCheckSettlement_ForbiddenSurfacesTypedAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	_, err := c.GetCheckSettlement(context.Background(), "33333333-3333-3333-3333-333333333333")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an *APIError: %v", err)
	}
	if apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", apiErr.StatusCode)
	}
}
