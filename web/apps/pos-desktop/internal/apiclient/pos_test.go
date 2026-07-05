package apiclient

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeContextToken builds a syntactically valid (3-segment) CTX-shaped
// token carrying the given tid/bid claims, without any real signature —
// enough to exercise Client.claims(), which deliberately does not verify
// the signature (see its doc comment).
func fakeContextToken(t *testing.T, tenantID, branchID string) string {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"CTX"}`))
	payload, err := json.Marshal(sessionClaims{TenantID: tenantID, BranchID: branchID})
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	pay := base64.RawURLEncoding.EncodeToString(payload)
	sig := base64.RawURLEncoding.EncodeToString([]byte("unverified-in-tests"))
	return hdr + "." + pay + "." + sig
}

func TestClient_Login_DecodesBranchFromTokenClaims(t *testing.T) {
	const tenantID = "11111111-1111-1111-1111-111111111111"
	const branchID = "22222222-2222-2222-2222-222222222222"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(loginResponse{
			Token:    fakeContextToken(t, tenantID, branchID),
			TenantID: tenantID,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})
	session, err := c.Login(t.Context(), "cashier@example.com")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if session.BranchID != branchID {
		t.Fatalf("BranchID = %q, want %q", session.BranchID, branchID)
	}
}

func TestClient_WhoAmI_DecodesTenantAndBranchFromTokenClaims(t *testing.T) {
	const tenantID = "11111111-1111-1111-1111-111111111111"
	const branchID = "22222222-2222-2222-2222-222222222222"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whoAmIResponse{})
	}))
	defer srv.Close()

	store := &memStore{token: fakeContextToken(t, tenantID, branchID), saved: true}
	c := New(srv.URL, store)

	session, err := c.WhoAmI(t.Context())
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if session.TenantID != tenantID || session.BranchID != branchID {
		t.Fatalf("unexpected session: %+v", session)
	}
}

func TestClient_WhoAmI_ChainWideStaff_LeavesBranchIDEmpty(t *testing.T) {
	const tenantID = "11111111-1111-1111-1111-111111111111"
	const nilBranch = "00000000-0000-0000-0000-000000000000"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whoAmIResponse{})
	}))
	defer srv.Close()

	store := &memStore{token: fakeContextToken(t, tenantID, nilBranch), saved: true}
	c := New(srv.URL, store)

	session, err := c.WhoAmI(t.Context())
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if session.BranchID != "" {
		t.Fatalf("BranchID = %q, want empty (chain-wide)", session.BranchID)
	}
}

func TestClient_ListOpenChecks_FiltersToOpenStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pos/checks" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]Check{
			{ID: "1", Status: "open", BranchID: "branch-a"},
			{ID: "2", Status: "closed", BranchID: "branch-a"},
			{ID: "3", Status: "open", BranchID: "branch-a"},
			{ID: "4", Status: "cancelled", BranchID: "branch-a"},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	checks, err := c.ListOpenChecks(t.Context(), "")
	if err != nil {
		t.Fatalf("ListOpenChecks: %v", err)
	}
	if len(checks) != 2 || checks[0].ID != "1" || checks[1].ID != "3" {
		t.Fatalf("unexpected checks: %+v", checks)
	}
}

// TestClient_ListOpenChecks_FiltersToBranch guards the multi-branch data
// isolation gap this filter exists for: the backend's listChecks endpoint
// returns every branch's checks for the tenant (no WHERE beyond RLS's
// tenant scoping), so without this filter a station could select — and
// then place orders/payments against — another branch's open check.
func TestClient_ListOpenChecks_FiltersToBranch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]Check{
			{ID: "1", Status: "open", BranchID: "branch-a"},
			{ID: "2", Status: "open", BranchID: "branch-b"},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	checks, err := c.ListOpenChecks(t.Context(), "branch-a")
	if err != nil {
		t.Fatalf("ListOpenChecks: %v", err)
	}
	if len(checks) != 1 || checks[0].ID != "1" {
		t.Fatalf("unexpected checks: %+v", checks)
	}
}

func TestClient_ListProducts_UsesCategoryScopedRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/catalog/categories/cat-1/products" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]Product{{ID: "p1", Name: "Ayran"}})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	products, err := c.ListProducts(t.Context(), "cat-1")
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if len(products) != 1 || products[0].Name != "Ayran" {
		t.Fatalf("unexpected products: %+v", products)
	}
}

// TestClient_PlaceOrder_RetriesSameIdempotencyKeyOn5xx guards the core
// ADR-SEC-003 retry contract: a retried attempt must reuse the exact same
// Idempotency-Key (not mint a fresh one), because the key identifies one
// logical write, not a request attempt.
func TestClient_PlaceOrder_RetriesSameIdempotencyKeyOn5xx(t *testing.T) {
	var attempts atomic.Int64
	var seenKeys []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		seenKeys = append(seenKeys, r.Header.Get("Idempotency-Key"))
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Order{ID: "order-1", Status: "pending"})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	order, err := c.PlaceOrder(t.Context(), "branch-1", "check-1", []OrderItemInput{{ProductID: "p1", Quantity: 1}})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if order.ID != "order-1" {
		t.Fatalf("unexpected order: %+v", order)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
	for i, k := range seenKeys {
		if k == "" {
			t.Fatalf("attempt %d: missing Idempotency-Key header", i)
		}
		if k != seenKeys[0] {
			t.Fatalf("attempt %d used a different Idempotency-Key (%q) than attempt 0 (%q) — retries must reuse the same key", i, k, seenKeys[0])
		}
	}
}

// TestClient_CloseCheck_DoesNotRetry4xx guards the other half of the
// contract: a 4xx means the server already made a decision (e.g. check not
// found, or already closed) and retrying cannot change that — retrying
// anyway would just be wasted latency at best.
func TestClient_CloseCheck_DoesNotRetry4xx(t *testing.T) {
	var attempts atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	_, err := c.CloseCheck(t.Context(), "missing-check")
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1 (4xx must not be retried)", attempts.Load())
	}
}

// TestClient_RegisterCashPayment_DecodesSnakeCasePaymentResponse simulates
// the scenario ADR-SEC-003 exists for: the first attempt succeeds
// server-side but the response never reaches the client (here modelled
// directly as a second call with the same semantics reusing the same key),
// and the server replays the original response instead of creating a
// second payment. This also exercises the request/response shape against
// the backend's actual paymentResponse DTO — snake_case on every payment
// endpoint (registerSale, getPayment, listPayments) after the DTO-casing
// fix; there is no longer a PascalCase asymmetry to model here.
func TestClient_RegisterCashPayment_DecodesSnakeCasePaymentResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/payments" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got == "" {
			t.Fatal("missing Idempotency-Key header")
		}
		var req registerSaleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Method != "cash" {
			t.Fatalf("Method = %q, want cash", req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"pay-1","branch_id":"branch-1","check_id":"check-1","method":"cash","status":"completed","amount_total":1500,"currency":"TRY"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	payment, err := c.RegisterCashPayment(t.Context(), "branch-1", "check-1", 1500)
	if err != nil {
		t.Fatalf("RegisterCashPayment: %v", err)
	}
	if payment.ID != "pay-1" || payment.Status != "completed" || payment.AmountTotal != 1500 {
		t.Fatalf("unexpected payment: %+v", payment)
	}
}

func TestClient_RegisterCashPayment_RejectsMissingCheckIDBeforeCallingServer(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	_, err := c.RegisterCashPayment(t.Context(), "branch-1", "", 1500)
	if err == nil {
		t.Fatal("expected error for empty check_id")
	}
	if called.Load() {
		t.Fatal("server should not have been called")
	}
	if !strings.Contains(err.Error(), "check_id") {
		t.Fatalf("error = %q, want to mention check_id", err.Error())
	}
}

// TestClient_ListCheckPayments_DecodesEnvelope verifies the query string
// shape and the {"payments": [...]} envelope payment/http listPayments
// actually returns.
func TestClient_ListCheckPayments_DecodesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/payments" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("check_id"); got != "check-1" {
			t.Fatalf("check_id query param = %q, want check-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"payments":[{"id":"pay-1","branch_id":"branch-1","check_id":"check-1","method":"cash","status":"completed","amount_total":1500,"currency":"TRY"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	payments, err := c.ListCheckPayments(t.Context(), "check-1")
	if err != nil {
		t.Fatalf("ListCheckPayments: %v", err)
	}
	if len(payments) != 1 || payments[0].ID != "pay-1" || payments[0].AmountTotal != 1500 {
		t.Fatalf("unexpected payments: %+v", payments)
	}
}

func TestClient_ListCheckPayments_RejectsMissingCheckIDBeforeCallingServer(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	_, err := c.ListCheckPayments(t.Context(), "")
	if err == nil {
		t.Fatal("expected error for empty check_id")
	}
	if called.Load() {
		t.Fatal("server should not have been called")
	}
}
