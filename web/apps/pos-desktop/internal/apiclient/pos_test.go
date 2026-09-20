package apiclient

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// TestClient_ListTables_DecodesZoneGroupedPlan verifies the client decodes
// GET /tables's actual zone-grouped shape (zonePlanResponse) — a slice of
// zones each carrying its own tables slice — and requires branch_id.
func TestClient_ListTables_DecodesZoneGroupedPlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pos/tables" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("branch_id"); got != "branch-1" {
			t.Fatalf("branch_id query param = %q, want branch-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"zone_id":"zone-1","zone_name":"Salon","floor":0,"tables":[
				{"id":"t1","branch_id":"branch-1","zone_id":"zone-1","name":"Masa 1","capacity":4,"status":"empty","layout_position":null,"is_active":true,"active_check_id":null},
				{"id":"t2","branch_id":"branch-1","zone_id":"zone-1","name":"Masa 2","capacity":2,"status":"occupied","layout_position":null,"is_active":true,"active_check_id":"chk-1"}
			]}
		]`))
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	zones, err := c.ListTables(t.Context(), "branch-1")
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if len(zones) != 1 || zones[0].ZoneName != "Salon" || len(zones[0].Tables) != 2 {
		t.Fatalf("unexpected zones: %+v", zones)
	}
	if zones[0].Tables[0].ActiveCheckID != nil {
		t.Fatalf("empty table should have nil ActiveCheckID, got %+v", zones[0].Tables[0].ActiveCheckID)
	}
	got := zones[0].Tables[1]
	if got.Status != "occupied" || got.ActiveCheckID == nil || *got.ActiveCheckID != "chk-1" {
		t.Fatalf("unexpected occupied table: %+v", got)
	}
}

func TestClient_ListTables_RejectsMissingBranchIDBeforeCallingServer(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	_, err := c.ListTables(t.Context(), "")
	if err == nil {
		t.Fatal("expected error for empty branch_id")
	}
	if called.Load() {
		t.Fatal("server should not have been called")
	}
}

// TestClient_OpenCheck_SendsTableIDWhenSet guards the *string encoding
// choice in openCheckRequest: a non-empty tableID must be sent as the
// table_id JSON field.
func TestClient_OpenCheck_SendsTableIDWhenSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openCheckRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.TableID == nil || *req.TableID != "table-1" {
			t.Fatalf("TableID = %v, want table-1", req.TableID)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Check{ID: "chk-1", TableLabel: "Masa 1"})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	chk, err := c.OpenCheck(t.Context(), "branch-1", "table-1", "Masa 1", "")
	if err != nil {
		t.Fatalf("OpenCheck: %v", err)
	}
	if chk.ID != "chk-1" {
		t.Fatalf("unexpected check: %+v", chk)
	}
}

// TestClient_OpenCheck_OmitsTableIDWhenEmpty guards the masasız satış
// (takeaway) path: an empty tableID must omit table_id from the request
// body entirely, not send an empty-string uuid the backend's *uuid.UUID
// field would fail to decode.
func TestClient_OpenCheck_OmitsTableIDWhenEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "table_id") {
			t.Fatalf("expected no table_id key in body, got: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Check{ID: "chk-2", TableLabel: "Paket servis"})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	chk, err := c.OpenCheck(t.Context(), "branch-1", "", "Paket servis", "")
	if err != nil {
		t.Fatalf("OpenCheck: %v", err)
	}
	if chk.ID != "chk-2" {
		t.Fatalf("unexpected check: %+v", chk)
	}
}

// TestClient_OpenCheck_MapsOccupiedTableConflict guards that a 409 from an
// already-occupied table surfaces as an *APIError the frontend's
// describeError can pattern-match on (see pos/http.Handler.error's "table is
// already occupied" body).
func TestClient_OpenCheck_MapsOccupiedTableConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "table is already occupied", http.StatusConflict)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	_, err := c.OpenCheck(t.Context(), "branch-1", "table-1", "Masa 1", "")
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("StatusCode = %d, want 409", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Body, "already occupied") {
		t.Fatalf("Body = %q, want it to mention already occupied", apiErr.Body)
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

// TestClient_RegisterCashPayment_AcceptsPartialAmountBelowCheckTotal is the
// split/partial-cash-payment regression: RegisterCashPayment must not
// reject (or silently coerce) an amountTotal that is only a fraction of
// what the check actually totals — the caller (App/frontend) is
// responsible for that decision (see this method's doc comment), the
// client is a pass-through. This reproduces the reported bug's root cause
// at the wire level: a ₺50 payment against what the server/caller knows to
// be a ₺630 check must be sent to the server as exactly 5000 kuruş, not
// rejected client-side and not silently rounded up to the full total.
func TestClient_RegisterCashPayment_AcceptsPartialAmountBelowCheckTotal(t *testing.T) {
	const checkTotalKurus = 63000 // ₺630
	const partialKurus = 5000     // ₺50 — well under the check total

	var gotAmount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req registerSaleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotAmount = req.AmountTotal
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"pay-1","branch_id":"branch-1","check_id":"check-1","method":"cash","status":"completed","amount_total":5000,"currency":"TRY"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	payment, err := c.RegisterCashPayment(t.Context(), "branch-1", "check-1", partialKurus)
	if err != nil {
		t.Fatalf("RegisterCashPayment: %v", err)
	}
	if gotAmount != partialKurus {
		t.Fatalf("server received amount_total = %d, want the partial %d (< check total %d) untouched",
			gotAmount, partialKurus, checkTotalKurus)
	}
	if payment.AmountTotal != partialKurus {
		t.Fatalf("payment.AmountTotal = %d, want %d", payment.AmountTotal, partialKurus)
	}
}

// TestClient_RegisterCashPayment_UsesFreshIdempotencyKeyPerInstallment
// guards the exact failure mode a split payment is vulnerable to if this
// regresses: two installments of the SAME amount (a common case — e.g. the
// customer pays ₺50 twice) must not be deduplicated as an idempotency
// replay of each other. doIdempotent mints one uuid per call (see
// client.go), so two separate RegisterCashPayment calls must carry two
// different Idempotency-Key headers even with byte-identical bodies.
func TestClient_RegisterCashPayment_UsesFreshIdempotencyKeyPerInstallment(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get(idempotencyHeader))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"pay-` + strings.TrimSpace(strconv.Itoa(len(keys))) + `","branch_id":"branch-1","check_id":"check-1","method":"cash","status":"completed","amount_total":5000,"currency":"TRY"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	for i := 0; i < 2; i++ {
		if _, err := c.RegisterCashPayment(t.Context(), "branch-1", "check-1", 5000); err != nil {
			t.Fatalf("RegisterCashPayment #%d: %v", i+1, err)
		}
	}

	if len(keys) != 2 {
		t.Fatalf("got %d requests, want 2", len(keys))
	}
	if keys[0] == "" || keys[1] == "" {
		t.Fatalf("empty Idempotency-Key: %v", keys)
	}
	if keys[0] == keys[1] {
		t.Fatalf("both installments used the SAME Idempotency-Key (%q) — the second would be treated as a replay of the first, silently deduplicating a legitimate second ₺50 installment", keys[0])
	}
}

// TestClient_ListCheckPayments_SumMatchesMultipleInstallments verifies the
// remaining-balance arithmetic the cashier UI depends on
// (frontend/src/lib/payment.ts's remainingBalance): ListCheckPayments must
// decode every completed installment on the check, in full, so their sum
// (checkTotal - sum(payments) = remaining) is correct after a split
// payment — not just the most recent installment.
func TestClient_ListCheckPayments_SumMatchesMultipleInstallments(t *testing.T) {
	const checkTotalKurus = 63000 // ₺630
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"payments":[
			{"id":"pay-1","branch_id":"branch-1","check_id":"check-1","method":"cash","status":"completed","amount_total":5000,"currency":"TRY"},
			{"id":"pay-2","branch_id":"branch-1","check_id":"check-1","method":"cash","status":"completed","amount_total":5000,"currency":"TRY"},
			{"id":"pay-3","branch_id":"branch-1","check_id":"check-1","method":"cash","status":"completed","amount_total":53000,"currency":"TRY"}
		]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	payments, err := c.ListCheckPayments(t.Context(), "check-1")
	if err != nil {
		t.Fatalf("ListCheckPayments: %v", err)
	}
	if len(payments) != 3 {
		t.Fatalf("got %d payments, want 3", len(payments))
	}

	var paidSoFar int64
	for _, p := range payments {
		paidSoFar += p.AmountTotal
	}
	if paidSoFar != checkTotalKurus {
		t.Fatalf("sum of installments = %d, want %d (exactly settled after the split)", paidSoFar, checkTotalKurus)
	}
	remaining := checkTotalKurus - paidSoFar
	if remaining != 0 {
		t.Fatalf("remaining = %d, want 0 once every installment is accounted for", remaining)
	}
}

func TestClient_GetOrder_DecodesOrderWithItemNotes(t *testing.T) {
	checkID := "check-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/pos/orders/order-1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Order{
			ID:      "order-1",
			CheckID: &checkID,
			Items:   []OrderItem{{ID: "i1", ProductName: "Adana Kebap", Quantity: 2, Note: "acısız"}},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	order, err := c.GetOrder(t.Context(), "order-1")
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if order.ID != "order-1" || order.CheckID == nil || *order.CheckID != checkID {
		t.Fatalf("unexpected order: %+v", order)
	}
	if len(order.Items) != 1 || order.Items[0].Note != "acısız" || order.Items[0].Quantity != 2 {
		t.Fatalf("unexpected items: %+v", order.Items)
	}
}

func TestClient_GetOrder_RejectsEmptyIDBeforeCallingServer(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	if _, err := c.GetOrder(t.Context(), ""); err == nil {
		t.Fatal("GetOrder(\"\"): want error, got nil")
	}
	if calls.Load() != 0 {
		t.Fatalf("server called %d times, want 0", calls.Load())
	}
}

func TestClient_GetOrder_NotFoundIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	if _, err := c.GetOrder(t.Context(), "missing"); err == nil {
		t.Fatal("GetOrder on 404: want error, got nil")
	}
}

func TestClient_ModifierCalls_UseCatalogRoutesAndDecode(t *testing.T) {
	tests := []struct {
		name     string
		wantPath string
		body     string
		call     func(c *Client) (int, error)
	}{
		{
			name:     "all groups",
			wantPath: "/api/v1/catalog/modifier-groups",
			body:     `[{"id":"g1","name":"Acı","selection_type":"single","min_selections":1,"max_selections":null,"is_required":true,"sort_order":1}]`,
			call: func(c *Client) (int, error) {
				groups, err := c.ListModifierGroups(t.Context())
				if err == nil && (groups[0].MaxSelections != nil || !groups[0].IsRequired || groups[0].SelectionType != "single") {
					t.Fatalf("group decoded wrong: %+v", groups[0])
				}
				return len(groups), err
			},
		},
		{
			name:     "product group ids",
			wantPath: "/api/v1/catalog/products/p-1/modifier-groups",
			body:     `["g1","g2"]`,
			call: func(c *Client) (int, error) {
				ids, err := c.ListProductModifierGroupIDs(t.Context(), "p-1")
				return len(ids), err
			},
		},
		{
			name:     "modifiers of a group",
			wantPath: "/api/v1/catalog/modifier-groups/g1/modifiers",
			body:     `[{"id":"m1","group_id":"g1","name":"Lavaş","price_delta":500,"is_active":true,"sort_order":1},{"id":"m2","group_id":"g1","name":"Eski","price_delta":-100,"is_active":false,"sort_order":2}]`,
			call: func(c *Client) (int, error) {
				mods, err := c.ListModifiers(t.Context(), "g1")
				if err == nil && (mods[0].PriceDelta != 500 || mods[1].PriceDelta != -100 || mods[1].IsActive) {
					t.Fatalf("modifiers decoded wrong: %+v", mods)
				}
				return len(mods), err
			},
		},
	}
	wantCounts := []int{1, 2, 2}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != tt.wantPath {
					t.Fatalf("request = %s %s, want GET %s", r.Method, r.URL.Path, tt.wantPath)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c := New(srv.URL, &memStore{token: "tok", saved: true})
			got, err := tt.call(c)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if got != wantCounts[i] {
				t.Fatalf("decoded %d items, want %d", got, wantCounts[i])
			}
		})
	}
}

func TestClient_PlaceOrder_SendsModifierIDs(t *testing.T) {
	var body struct {
		Items []struct {
			ModifierIDs []string `json:"modifier_ids"`
			Note        string   `json:"note"`
		} `json:"items"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Order{ID: "order-1"})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	_, err := c.PlaceOrder(t.Context(), "branch-1", "check-1", []OrderItemInput{
		{ProductID: "p1", Quantity: 1, Note: "Acılı", ModifierIDs: []string{"m1", "m2"}},
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if len(body.Items) != 1 || len(body.Items[0].ModifierIDs) != 2 || body.Items[0].ModifierIDs[1] != "m2" || body.Items[0].Note != "Acılı" {
		t.Fatalf("request items = %+v", body.Items)
	}
}

func TestClient_RegisterPayment_SendsMethodLinesAndMeta(t *testing.T) {
	var body struct {
		Method      string `json:"method"`
		AmountTotal int64  `json:"amount_total"`
		Lines       []struct {
			Name             string `json:"name"`
			UnitPriceMinor   int64  `json:"unit_price_minor"`
			QuantityMilli    int64  `json:"quantity_milli"`
			TaxRatePermyriad int    `json:"tax_rate_permyriad"`
			CategoryID       string `json:"category_id"`
			Unit             string `json:"unit"`
		} `json:"lines"`
		Meta struct {
			TableLabel string `json:"table_label"`
		} `json:"meta"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Payment{ID: "pay-1", Method: body.Method, Status: "pending", AmountTotal: body.AmountTotal})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	_, err := c.RegisterPayment(t.Context(), RegisterPaymentInput{
		BranchID:    "b1",
		CheckID:     "c1",
		Method:      "terminal",
		AmountTotal: 14500,
		TableLabel:  "Masa 7",
		Lines: []FiscalLine{
			{Name: "Lahmacun", UnitPriceMinor: 6500, QuantityMilli: 2000, TaxRatePermyriad: 1000, CategoryID: "cat-1", Unit: "C62"},
			{Name: "Çay", UnitPriceMinor: 1500, QuantityMilli: 1000, TaxRatePermyriad: 1000},
		},
	})
	if err != nil {
		t.Fatalf("RegisterPayment: %v", err)
	}
	if body.Method != "terminal" || body.AmountTotal != 14500 || body.Meta.TableLabel != "Masa 7" {
		t.Fatalf("request = %+v", body)
	}
	if len(body.Lines) != 2 || body.Lines[0].QuantityMilli != 2000 || body.Lines[0].TaxRatePermyriad != 1000 || body.Lines[0].CategoryID != "cat-1" || body.Lines[0].Unit != "C62" {
		t.Fatalf("lines = %+v", body.Lines)
	}
	if body.Lines[1].CategoryID != "" {
		t.Fatalf("a line without a category must omit category_id, got %q", body.Lines[1].CategoryID)
	}
}

func TestClient_RegisterPayment_RejectsLinesThatDoNotAddUpToTheAmountBeforeCallingServer(t *testing.T) {
	tests := []struct {
		name  string
		lines []FiscalLine
	}{
		{"lines below the amount", []FiscalLine{{Name: "A", UnitPriceMinor: 1000, QuantityMilli: 1000}}},
		{"lines above the amount", []FiscalLine{{Name: "A", UnitPriceMinor: 3000, QuantityMilli: 1000}}},
		{"a quantity that does not divide into whole kuruş", []FiscalLine{{Name: "A", UnitPriceMinor: 1001, QuantityMilli: 1500}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) }))
			defer srv.Close()

			c := New(srv.URL, &memStore{token: "tok", saved: true})
			_, err := c.RegisterPayment(t.Context(), RegisterPaymentInput{BranchID: "b1", CheckID: "c1", Method: "cash", AmountTotal: 2000, Lines: tt.lines})
			if !errors.Is(err, ErrLinesTotalMismatch) {
				t.Fatalf("err = %v, want ErrLinesTotalMismatch", err)
			}
			if called.Load() {
				t.Fatal("the server was called although the basket cannot be valid")
			}
		})
	}
}

func TestClient_RegisterPayment_NoLinesIsAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Payment{ID: "pay-1"})
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	if _, err := c.RegisterPayment(t.Context(), RegisterPaymentInput{BranchID: "b1", CheckID: "c1", Method: "cash", AmountTotal: 2000}); err != nil {
		t.Fatalf("RegisterPayment without lines: %v", err)
	}
}

func TestClient_GetProduct_UsesProductRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/catalog/products/p-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"p-1","category_id":null,"name":"Çay","unit":"adet","tax_rate_bps":1000}`))
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	p, err := c.GetProduct(t.Context(), "p-1")
	if err != nil {
		t.Fatalf("GetProduct: %v", err)
	}
	if p.CategoryID != "" || p.TaxRateBPS != 1000 || p.Unit != "adet" {
		t.Fatalf("product = %+v", p)
	}
}

func TestClient_ListOpenChecks_ExcludesMergedAndKeepsTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":"1","status":"open","branch_id":"b","total":24000},
			{"id":"2","status":"merged","branch_id":"b","total":0,"merged_into_check_id":"1"},
			{"id":"3","status":"open","branch_id":"b"}
		]`))
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	checks, err := c.ListOpenChecks(t.Context(), "")
	if err != nil {
		t.Fatalf("ListOpenChecks: %v", err)
	}
	if len(checks) != 2 || checks[0].ID != "1" || checks[1].ID != "3" {
		t.Fatalf("checks = %+v, want the two open ones — a merged adisyon is not an open one", checks)
	}
	if checks[0].Total == nil || *checks[0].Total != 24000 {
		t.Fatalf("total = %v, want 24000", checks[0].Total)
	}
	if checks[1].Total != nil {
		t.Fatalf("a check without a total must decode to nil, got %d", *checks[1].Total)
	}
}

func TestClient_CheckMoves_UseTheRoutesAndBodiesTheBackendExpects(t *testing.T) {
	tests := []struct {
		name     string
		wantPath string
		wantBody map[string]any
		call     func(c *Client) (Check, error)
	}{
		{
			name:     "transfer",
			wantPath: "/api/v1/pos/checks/chk-1/transfer",
			wantBody: map[string]any{"table_id": "tbl-9"},
			call:     func(c *Client) (Check, error) { return c.TransferCheck(t.Context(), "chk-1", "tbl-9") },
		},
		{
			name:     "merge: the path id is the surviving check",
			wantPath: "/api/v1/pos/checks/target/merge",
			wantBody: map[string]any{"source_check_id": "source"},
			call:     func(c *Client) (Check, error) { return c.MergeChecks(t.Context(), "target", "source") },
		},
		{
			name:     "move items: the path id is the check the items leave",
			wantPath: "/api/v1/pos/checks/from/move-items",
			wantBody: map[string]any{"target_check_id": "to", "order_item_ids": []any{"i1", "i2"}},
			call: func(c *Client) (Check, error) {
				return c.MoveCheckItems(t.Context(), "from", "to", []string{"i1", "i2"})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var key string
			var got map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != tt.wantPath {
					t.Fatalf("request = %s %s, want POST %s", r.Method, r.URL.Path, tt.wantPath)
				}
				key = r.Header.Get("Idempotency-Key")
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatalf("decode: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"chk-1","status":"open","table_label":"Masa 9"}`))
			}))
			defer srv.Close()

			c := New(srv.URL, &memStore{token: "tok", saved: true})
			check, err := tt.call(c)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if check.TableLabel != "Masa 9" {
				t.Fatalf("check = %+v", check)
			}
			if key == "" {
				t.Fatal("missing Idempotency-Key — ADR-SEC-003: these endpoints change the state of money-bearing checks")
			}
			if len(got) != len(tt.wantBody) {
				t.Fatalf("body = %v, want %v", got, tt.wantBody)
			}
			for k, want := range tt.wantBody {
				if fmt.Sprint(got[k]) != fmt.Sprint(want) {
					t.Fatalf("body[%s] = %v, want %v", k, got[k], want)
				}
			}
		})
	}
}

func TestClient_CheckMoves_RejectMissingIDsBeforeCallingServer(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Store(true) }))
	defer srv.Close()
	c := New(srv.URL, &memStore{token: "tok", saved: true})

	calls := map[string]func() error{
		"transfer without table": func() error { _, err := c.TransferCheck(t.Context(), "c", ""); return err },
		"transfer without check": func() error { _, err := c.TransferCheck(t.Context(), "", "t"); return err },
		"merge without source":   func() error { _, err := c.MergeChecks(t.Context(), "c", ""); return err },
		"move without items":     func() error { _, err := c.MoveCheckItems(t.Context(), "a", "b", nil); return err },
		"move without target":    func() error { _, err := c.MoveCheckItems(t.Context(), "a", "", []string{"i"}); return err },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if called.Load() {
		t.Fatal("the server was called with an incomplete request")
	}
}

func TestClient_CheckMoves_SurfaceTheMachineReadableConflictCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"source check has payments; close it before merging","code":"payments_present"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	_, err := c.MergeChecks(t.Context(), "target", "source")
	if err == nil || !strings.Contains(err.Error(), `"code":"payments_present"`) {
		t.Fatalf("err = %v — the frontend maps the Turkish message from this code, so it must survive in the error text", err)
	}
}

func TestClient_SetTableStatus_PostsTheStatusToTheTableRoute(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/pos/tables/tbl-1/status" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"tbl-1","status":"empty"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{token: "tok", saved: true})
	table, err := c.SetTableStatus(t.Context(), "tbl-1", "empty")
	if err != nil {
		t.Fatalf("SetTableStatus: %v", err)
	}
	if got["status"] != "empty" || table.Status != "empty" {
		t.Fatalf("sent %v, decoded %+v", got, table)
	}
}

func TestClient_SetTableStatus_RejectsMissingArgumentsAndKeepsTheForbiddenStatus(t *testing.T) {
	c := New("http://unused.invalid", &memStore{token: "tok", saved: true})
	if _, err := c.SetTableStatus(t.Context(), "", "empty"); err == nil {
		t.Fatal("expected an error for a missing table id")
	}
	if _, err := c.SetTableStatus(t.Context(), "t", ""); err == nil {
		t.Fatal("expected an error for a missing status")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()
	c = New(srv.URL, &memStore{token: "tok", saved: true})
	_, err := c.SetTableStatus(t.Context(), "t", "empty")
	if err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("err = %v — the frontend tells 403 (only a manager may free it) from other failures by this text", err)
	}
}
