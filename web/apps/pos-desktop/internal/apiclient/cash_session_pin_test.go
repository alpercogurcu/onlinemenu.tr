package apiclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

func TestClient_ListCashSessionParticipants_DecodesEnvelope(t *testing.T) {
	const body = `{"participants":[
		{"person_id":"11111111-1111-1111-1111-111111111111","full_name":"Ayşe Yılmaz","has_pin":true,"locked":false},
		{"person_id":"22222222-2222-2222-2222-222222222222","full_name":"Mehmet Demir","has_pin":false,"locked":false}
	]}`

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	got, err := c.ListCashSessionParticipants(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("ListCashSessionParticipants: %v", err)
	}
	if gotPath != "/api/v1/payments/cash-sessions/session-1/participants" {
		t.Errorf("path = %q", gotPath)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].FullName != "Ayşe Yılmaz" || !got[0].HasPin || got[0].Locked {
		t.Errorf("unexpected first participant: %+v", got[0])
	}
	if got[1].HasPin {
		t.Errorf("second participant must have has_pin=false: %+v", got[1])
	}
}

func TestClient_ListCashSessionParticipants_RejectsEmptySessionIDBeforeCallingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("must not reach the server without a session id")
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	if _, err := c.ListCashSessionParticipants(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an empty session id")
	}
}

func TestClient_JoinCashSession_SendsPinInBody(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		buf := make([]byte, 256)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	if err := c.JoinCashSession(context.Background(), "session-1", "1234"); err != nil {
		t.Fatalf("JoinCashSession: %v", err)
	}
	if gotPath != "/api/v1/payments/cash-sessions/session-1/participants" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody != `{"pin":"1234"}` {
		t.Errorf("body = %q", gotBody)
	}
}

func TestClient_JoinCashSession_RejectsEmptySessionIDBeforeCallingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("must not reach the server without a session id")
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	if err := c.JoinCashSession(context.Background(), "", "1234"); err == nil {
		t.Fatal("expected an error for an empty session id")
	}
}

func TestClient_SwitchCashier_SendsIdempotencyKeyAndDecodesToken(t *testing.T) {
	var gotKey, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Idempotency-Key")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"ctx-token-for-switched-cashier"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	got, err := c.SwitchCashier(context.Background(), "session-1", "person-1", "1234")
	if err != nil {
		t.Fatalf("SwitchCashier: %v", err)
	}
	if gotKey == "" {
		t.Error("Idempotency-Key header was not sent")
	}
	if gotPath != "/api/v1/payments/cash-sessions/session-1/switch" {
		t.Errorf("path = %q", gotPath)
	}
	if got != "ctx-token-for-switched-cashier" {
		t.Errorf("token = %q", got)
	}
	// SwitchCashier must never install the token itself — that is main.App's
	// job (see the method's doc comment) — so the client's own current token
	// must be unaffected by a successful call.
	if c.token() == got {
		t.Error("SwitchCashier must not install the returned token on the client itself")
	}
}

// TestClient_SwitchCashier_401SurfacesGenericSentinel guards the
// enumeration-safety contract: a wrong pin, a locked participant, and a
// non-participant all reach the backend as a bare 401 with no
// distinguishing body — this must surface as ErrPinVerificationFailed, not
// a generic *APIError a caller might be tempted to inspect for specifics.
func TestClient_SwitchCashier_401SurfacesGenericSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "verification failed", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	_, err := c.SwitchCashier(context.Background(), "session-1", "person-1", "0000")
	if !errors.Is(err, ErrPinVerificationFailed) {
		t.Fatalf("err = %v, want ErrPinVerificationFailed", err)
	}
}

// TestClient_SwitchCashier_401DoesNotTriggerRecovery is the regression test
// for doWithHeadersNoRecovery's whole reason for existing: with a recovery
// hook installed (as main.App always wires one after a Keycloak-derived
// login), a wrong-PIN 401 from Switch must NOT invoke it, must NOT retry,
// and must NOT silently swap the client's installed token.
func TestClient_SwitchCashier_401DoesNotTriggerRecovery(t *testing.T) {
	var requestCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		http.Error(w, "verification failed", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	c.setToken("original-ctx-token")

	var recoveryCalls atomic.Int64
	c.SetUnauthorizedRecovery(func(ctx context.Context) (string, error) {
		recoveryCalls.Add(1)
		return "recovered-ctx-token", nil
	})

	_, err := c.SwitchCashier(context.Background(), "session-1", "person-1", "0000")
	if !errors.Is(err, ErrPinVerificationFailed) {
		t.Fatalf("err = %v, want ErrPinVerificationFailed", err)
	}
	if requestCount.Load() != 1 {
		t.Fatalf("requestCount = %d, want exactly 1 (no recovery retry)", requestCount.Load())
	}
	if recoveryCalls.Load() != 0 {
		t.Fatalf("recoveryCalls = %d, want 0 — Switch's 401 must never trigger recovery", recoveryCalls.Load())
	}
	if c.token() != "original-ctx-token" {
		t.Fatalf("client token = %q, want unchanged original-ctx-token", c.token())
	}
}

func TestClient_SwitchCashier_RejectsMissingArgsBeforeCallingServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("must not reach the server with a missing argument")
	}))
	defer srv.Close()

	c := New(srv.URL, tokenstore.New(t.TempDir(), nil))
	if _, err := c.SwitchCashier(context.Background(), "", "person-1", "1234"); err == nil {
		t.Fatal("expected an error for an empty session id")
	}
	if _, err := c.SwitchCashier(context.Background(), "session-1", "", "1234"); err == nil {
		t.Fatal("expected an error for an empty person id")
	}
}
