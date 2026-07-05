package keycloakauth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestLoopbackServer_RedirectURIMatchesLoopbackWildcard(t *testing.T) {
	s, err := NewLoopbackServer()
	if err != nil {
		t.Fatalf("NewLoopbackServer: %v", err)
	}
	defer s.Close()

	uri := s.RedirectURI()
	if !strings.HasPrefix(uri, "http://127.0.0.1:") || !strings.HasSuffix(uri, "/callback") {
		t.Fatalf("RedirectURI = %q, want http://127.0.0.1:<port>/callback", uri)
	}
	if s.Port() == 0 {
		t.Fatal("Port() = 0, want a real ephemeral port")
	}
}

// TestLoopbackServer_ReceivesCallback_NoGoroutineLeak mirrors
// internal/hardware's goleak convention (see mock_printer_test.go): the
// server's Serve goroutine must be guaranteed to have exited by the time
// Wait returns.
func TestLoopbackServer_ReceivesCallback_NoGoroutineLeak(t *testing.T) {
	defer goleak.VerifyNone(t)

	s, err := NewLoopbackServer()
	if err != nil {
		t.Fatalf("NewLoopbackServer: %v", err)
	}

	go func() {
		resp, err := http.Get(s.RedirectURI() + "?code=abc123&state=state-xyz")
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()

	res, err := s.Wait(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if res.Code != "abc123" || res.State != "state-xyz" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestLoopbackServer_TimesOut_NoGoroutineLeak(t *testing.T) {
	defer goleak.VerifyNone(t)

	s, err := NewLoopbackServer()
	if err != nil {
		t.Fatalf("NewLoopbackServer: %v", err)
	}

	_, err = s.Wait(context.Background(), 50*time.Millisecond)
	if !errors.Is(err, ErrCallbackTimeout) {
		t.Fatalf("Wait = %v, want ErrCallbackTimeout", err)
	}
}

func TestLoopbackServer_ContextCancelled_NoGoroutineLeak(t *testing.T) {
	defer goleak.VerifyNone(t)

	s, err := NewLoopbackServer()
	if err != nil {
		t.Fatalf("NewLoopbackServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = s.Wait(ctx, 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait = %v, want context.Canceled", err)
	}
}

func TestLoopbackServer_AuthorizationErrorFromIdP_NoGoroutineLeak(t *testing.T) {
	defer goleak.VerifyNone(t)

	s, err := NewLoopbackServer()
	if err != nil {
		t.Fatalf("NewLoopbackServer: %v", err)
	}

	go func() {
		resp, err := http.Get(s.RedirectURI() + "?error=access_denied&error_description=user+cancelled")
		if err != nil {
			return
		}
		_ = resp.Body.Close()
	}()

	_, err = s.Wait(context.Background(), 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "user cancelled") {
		t.Fatalf("Wait = %v, want an error containing the IdP's error_description", err)
	}
}

func TestLoopbackServer_MissingCodeOrState_Returns400(t *testing.T) {
	s, err := NewLoopbackServer()
	if err != nil {
		t.Fatalf("NewLoopbackServer: %v", err)
	}
	defer s.Close()

	resp, err := http.Get(s.RedirectURI())
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
