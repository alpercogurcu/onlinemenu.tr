package apiclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

func TestClient_GetActiveCashSession_DecodesEnvelope(t *testing.T) {
	const body = `{
	  "id": "44444444-4444-4444-4444-444444444444",
	  "branch_id": "22222222-2222-2222-2222-222222222222",
	  "status": "opened",
	  "opening_counted_amount": 50000,
	  "opening_notes": "",
	  "opened_by": "11111111-1111-1111-1111-111111111111",
	  "opened_at": "2026-08-01T08:00:00Z",
	  "closing_counted_amount": null,
	  "closing_denominations": null,
	  "closing_notes": "",
	  "closing_submitted_at": null,
	  "closed_by": null,
	  "closed_at": null,
	  "movements_net": 1000,
	  "cash_payments_taken": 25000,
	  "expected_close": 76000,
	  "difference": null
	}`

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.URL.Path != "/api/v1/payments/cash-sessions/active" {
			t.Errorf("path = %q, want /api/v1/payments/cash-sessions/active", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	got, err := c.GetActiveCashSession(context.Background(), "22222222-2222-2222-2222-222222222222")
	if err != nil {
		t.Fatalf("GetActiveCashSession: %v", err)
	}
	if gotQuery != "branch_id=22222222-2222-2222-2222-222222222222" {
		t.Errorf("query = %q, want branch_id=...", gotQuery)
	}
	if got.Status != "opened" || got.ExpectedClose != 76000 || got.MovementsNet != 1000 {
		t.Errorf("unexpected session: %+v", got)
	}
	if got.Difference != nil {
		t.Errorf("difference = %v, want nil (no closing count submitted yet)", got.Difference)
	}
}

// The steady state of "no shift started yet" must be a distinguishable typed
// error, not a generic *APIError a caller could mistake for a transient
// fault — see ErrNoActiveCashSession's doc comment.
func TestClient_GetActiveCashSession_NotFoundSurfacesTypedSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no open cash session for this branch", http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	_, err := c.GetActiveCashSession(context.Background(), "22222222-2222-2222-2222-222222222222")
	if !errors.Is(err, ErrNoActiveCashSession) {
		t.Fatalf("err = %v, want ErrNoActiveCashSession", err)
	}
}

func TestClient_GetActiveCashSession_RejectsMissingBranchIDBeforeCallingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("must not reach the server without a branch_id")
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	if _, err := c.GetActiveCashSession(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an empty branch_id")
	}
}

// The whole point of CashSessionCannotCloseError: the backend's cannotClose
// guard's reasons must reach the caller intact (as a slice, not squashed
// into an opaque string) so the cashier sees every blocking reason verbatim.
func TestClient_CloseCashSession_CannotCloseParsesReasonsIntoTypedError(t *testing.T) {
	const body = `{"code":"cannot_close","reasons":["branch has 2 pending fiscal submission(s); money state is unknown until the ÖKC resolves them"]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/payments/cash-sessions/session-1/close" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	_, err := c.CloseCashSession(context.Background(), "session-1")

	var cannotClose *CashSessionCannotCloseError
	if !errors.As(err, &cannotClose) {
		t.Fatalf("error is not a *CashSessionCannotCloseError: %v", err)
	}
	if len(cannotClose.Reasons) != 1 {
		t.Fatalf("reasons len = %d, want 1: %+v", len(cannotClose.Reasons), cannotClose.Reasons)
	}
	if cannotClose.Reasons[0] != "branch has 2 pending fiscal submission(s); money state is unknown until the ÖKC resolves them" {
		t.Errorf("unexpected reason: %q", cannotClose.Reasons[0])
	}
}

// A 409 whose body is PLAIN TEXT (domain.ErrInvalidCashSessionTransition,
// emitted via http.Error rather than respondJSON — e.g. calling close before
// a closing count was ever submitted) must fall through to the ordinary
// *APIError path, not be misparsed as a cannot-close response.
func TestClient_CloseCashSession_PlainTextConflictSurfacesAsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "payment/service: cash session is \"opened\", submit a closing count first: invalid transition", http.StatusConflict)
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	_, err := c.CloseCashSession(context.Background(), "session-1")

	var cannotClose *CashSessionCannotCloseError
	if errors.As(err, &cannotClose) {
		t.Fatalf("plain-text 409 must not parse as CashSessionCannotCloseError, got reasons %+v", cannotClose.Reasons)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an *APIError: %v", err)
	}
	if apiErr.StatusCode != http.StatusConflict {
		t.Errorf("StatusCode = %d, want 409", apiErr.StatusCode)
	}
}

func TestClient_CloseCashSession_SuccessDecodesSession(t *testing.T) {
	const body = `{
	  "id": "44444444-4444-4444-4444-444444444444",
	  "branch_id": "22222222-2222-2222-2222-222222222222",
	  "status": "closed",
	  "opening_counted_amount": 50000,
	  "opening_notes": "",
	  "opened_by": "11111111-1111-1111-1111-111111111111",
	  "opened_at": "2026-08-01T08:00:00Z",
	  "closing_counted_amount": 75500,
	  "closing_denominations": [{"denomination_minor":20000,"count":3}],
	  "closing_notes": "",
	  "closing_submitted_at": "2026-08-01T20:00:00Z",
	  "closed_by": "11111111-1111-1111-1111-111111111111",
	  "closed_at": "2026-08-01T20:05:00Z",
	  "movements_net": 500,
	  "cash_payments_taken": 25000,
	  "expected_close": 75500,
	  "difference": 0
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	got, err := c.CloseCashSession(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("CloseCashSession: %v", err)
	}
	if got.Status != "closed" || got.Difference == nil || *got.Difference != 0 {
		t.Errorf("unexpected session: %+v", got)
	}
}

// RecordCashMovement must carry an Idempotency-Key (ADR-SEC-003 addendum for
// this route — see handler.go's route comment): a retried in/out would
// double-record real cash.
func TestClient_RecordCashMovement_SendsIdempotencyKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"mv-1","session_id":"session-1","direction":"in","amount_minor":5000,"reason":"bozuk para","created_by":"11111111-1111-1111-1111-111111111111","created_at":"2026-08-01T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	got, err := c.RecordCashMovement(context.Background(), "session-1", "in", 5000, "bozuk para")
	if err != nil {
		t.Fatalf("RecordCashMovement: %v", err)
	}
	if gotKey == "" {
		t.Error("Idempotency-Key header was not sent")
	}
	if got.AmountMinor != 5000 || got.Direction != "in" {
		t.Errorf("unexpected movement: %+v", got)
	}
}

func TestClient_RecordCashMovement_RejectsNonPositiveAmountBeforeCallingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("must not reach the server with a non-positive amount")
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	if _, err := c.RecordCashMovement(context.Background(), "session-1", "out", 0, "kasa kontrolü"); err == nil {
		t.Fatal("expected an error for a zero amount")
	}
}

func TestClient_RecordCashMovement_RejectsEmptyReasonBeforeCallingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("must not reach the server with an empty reason")
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	if _, err := c.RecordCashMovement(context.Background(), "session-1", "out", 500, "  "); err == nil {
		t.Fatal("expected an error for a blank reason")
	}
}

func TestClient_SubmitClosingCount_DecodesFreshDifference(t *testing.T) {
	const body = `{
	  "id": "44444444-4444-4444-4444-444444444444",
	  "branch_id": "22222222-2222-2222-2222-222222222222",
	  "status": "closing_control",
	  "opening_counted_amount": 50000,
	  "opening_notes": "",
	  "opened_by": "11111111-1111-1111-1111-111111111111",
	  "opened_at": "2026-08-01T08:00:00Z",
	  "closing_counted_amount": 76500,
	  "closing_denominations": [{"denomination_minor":20000,"count":3},{"denomination_minor":10000,"count":1}],
	  "closing_notes": "",
	  "closing_submitted_at": "2026-08-01T20:00:00Z",
	  "closed_by": null,
	  "closed_at": null,
	  "movements_net": 1000,
	  "cash_payments_taken": 25000,
	  "expected_close": 76000,
	  "difference": 500
	}`

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	got, err := c.SubmitClosingCount(context.Background(), "session-1", 76500,
		[]DenominationCount{{DenominationMinor: 20000, Count: 3}, {DenominationMinor: 10000, Count: 1}}, "")
	if err != nil {
		t.Fatalf("SubmitClosingCount: %v", err)
	}
	if gotBody == "" {
		t.Fatal("request body was not sent")
	}
	if got.Status != "closing_control" || got.Difference == nil || *got.Difference != 500 {
		t.Errorf("unexpected session: %+v", got)
	}
	if got.ClosingSubmittedAt == nil {
		t.Error("closing_submitted_at did not decode")
	}
}
