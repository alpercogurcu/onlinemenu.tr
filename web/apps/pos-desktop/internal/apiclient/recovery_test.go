package apiclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDoWithHeaders_401TriggersRecoveryAndRetriesOnce guards the CTX-401
// recovery path (main.App.recoverKeycloakContext in production): a 401
// with a recovery hook installed must call the hook exactly once, then
// retry the original request exactly once with the recovered token.
func TestDoWithHeaders_401TriggersRecoveryAndRetriesOnce(t *testing.T) {
	var requestCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)
		if n == 1 {
			if got := r.Header.Get("Authorization"); got != "Bearer expired-ctx-token" {
				t.Fatalf("first attempt Authorization = %q, want Bearer expired-ctx-token", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer recovered-ctx-token" {
			t.Fatalf("retry Authorization = %q, want Bearer recovered-ctx-token", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})
	c.setToken("expired-ctx-token")

	var recoveryCalls atomic.Int64
	c.SetUnauthorizedRecovery(func(ctx context.Context) (string, error) {
		recoveryCalls.Add(1)
		return "recovered-ctx-token", nil
	})

	if err := c.do(t.Context(), http.MethodGet, "/v1/identity/me", nil, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
	if requestCount.Load() != 2 {
		t.Fatalf("requestCount = %d, want 2 (original + one retry)", requestCount.Load())
	}
	if recoveryCalls.Load() != 1 {
		t.Fatalf("recoveryCalls = %d, want exactly 1", recoveryCalls.Load())
	}
	if c.token() != "recovered-ctx-token" {
		t.Fatalf("client token = %q, want recovered-ctx-token installed after recovery", c.token())
	}
}

// TestDoWithHeaders_RecoveryFailure_Returns401Once guards against an
// infinite loop / repeated recovery attempts when the recovery hook itself
// fails (e.g. the Keycloak refresh token is also expired).
func TestDoWithHeaders_RecoveryFailure_Returns401Once(t *testing.T) {
	var requestCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})
	c.setToken("expired-ctx-token")

	var recoveryCalls atomic.Int64
	c.SetUnauthorizedRecovery(func(ctx context.Context) (string, error) {
		recoveryCalls.Add(1)
		return "", errors.New("keycloak refresh token also expired")
	})

	err := c.do(t.Context(), http.MethodGet, "/v1/identity/me", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("do = %v, want *APIError{401}", err)
	}
	if requestCount.Load() != 1 {
		t.Fatalf("requestCount = %d, want exactly 1 (no retry after failed recovery)", requestCount.Load())
	}
	if recoveryCalls.Load() != 1 {
		t.Fatalf("recoveryCalls = %d, want exactly 1", recoveryCalls.Load())
	}
}

// TestDoWithHeaders_NoRecoveryHook_401PropagatesUnchanged guards the
// dev-login regression case: with no recovery hook installed (dev-login
// never wires one), a 401 must propagate exactly as it did before this
// feature existed.
func TestDoWithHeaders_NoRecoveryHook_401PropagatesUnchanged(t *testing.T) {
	var requestCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})
	c.setToken("some-token")

	err := c.do(t.Context(), http.MethodGet, "/v1/identity/me", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("do = %v, want *APIError{401}", err)
	}
	if requestCount.Load() != 1 {
		t.Fatalf("requestCount = %d, want exactly 1", requestCount.Load())
	}
}

// TestDoWithHeaders_ConcurrentRecoveries_AreSingleFlighted guards the
// scenario the Client.recoverToken doc comment describes: Wails invokes
// each bound method call on its own goroutine, so N concurrent CTX-401s
// (e.g. several ListChecks/GetCheck calls in flight when the CTX token
// expires) must trigger exactly one recovery attempt, and all callers must
// observe its result. Run with -race (task pos:test).
func TestDoWithHeaders_ConcurrentRecoveries_AreSingleFlighted(t *testing.T) {
	const workers = 10

	// The server holds every initial (401-bound) request until all `workers`
	// requests have arrived, so every worker's do() call reaches the 401
	// branch — and races into recoverToken — in a tight batch, instead of
	// completing one at a time with no actual overlap.
	var arrived atomic.Int64
	allArrived := make(chan struct{})
	var closeOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer recovered-ctx-token" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if arrived.Add(1) == workers {
			closeOnce.Do(func() { close(allArrived) })
		}
		<-allArrived
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})
	c.setToken("expired-ctx-token")

	var recoveryCalls atomic.Int64
	c.SetUnauthorizedRecovery(func(ctx context.Context) (string, error) {
		recoveryCalls.Add(1)
		// Hold the single-flight window open long enough for the other
		// (already-401'd, already racing into recoverToken) workers to
		// observe recoveryInFlight != nil instead of starting a second
		// recovery.
		time.Sleep(100 * time.Millisecond)
		return "recovered-ctx-token", nil
	})

	var wg sync.WaitGroup
	wg.Add(workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			errs[i] = c.do(t.Context(), http.MethodGet, "/v1/identity/me", nil, nil)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: do: %v", i, err)
		}
	}
	if recoveryCalls.Load() != 1 {
		t.Fatalf("recoveryCalls = %d, want exactly 1 across %d concurrent 401s", recoveryCalls.Load(), workers)
	}
}

// TestDoWithHeaders_IdempotencyKeyHeaderSurvivesRecoveryRetry guards
// ADR-SEC-003: a 401-triggered retry inside doWithHeaders must resend the
// exact same Idempotency-Key, not mint a new one (that responsibility is
// doIdempotent's for its own 5xx/transport retries — this test is about
// the internal 401 retry reusing whatever headers it was given).
func TestDoWithHeaders_IdempotencyKeyHeaderSurvivesRecoveryRetry(t *testing.T) {
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get(idempotencyHeader))
		if len(keys) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})
	c.setToken("expired-ctx-token")
	c.SetUnauthorizedRecovery(func(ctx context.Context) (string, error) {
		return "recovered-ctx-token", nil
	})

	err := c.doWithHeaders(t.Context(), http.MethodPost, "/v1/pos/checks", nil, nil, map[string]string{idempotencyHeader: "key-123"})
	if err != nil {
		t.Fatalf("doWithHeaders: %v", err)
	}
	if len(keys) != 2 || keys[0] != "key-123" || keys[1] != "key-123" {
		t.Fatalf("Idempotency-Key headers = %v, want [key-123 key-123]", keys)
	}
}

// TestDoWithBearer_Never401Retries guards the recursion guard documented on
// doWithBearer: it must never itself trigger recovery, even if a recovery
// hook is installed and the pre-context call gets a 401.
func TestDoWithBearer_Never401Retries(t *testing.T) {
	var requestCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})
	var recoveryCalls atomic.Int64
	c.SetUnauthorizedRecovery(func(ctx context.Context) (string, error) {
		recoveryCalls.Add(1)
		return "recovered-ctx-token", nil
	})

	_, err := c.FetchKeycloakContexts(t.Context(), "expired-keycloak-access-token")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("FetchKeycloakContexts = %v, want *APIError{401}", err)
	}
	if requestCount.Load() != 1 {
		t.Fatalf("requestCount = %d, want exactly 1 (doWithBearer must not retry)", requestCount.Load())
	}
	if recoveryCalls.Load() != 0 {
		t.Fatalf("recoveryCalls = %d, want 0 — doWithBearer must never invoke the recovery hook", recoveryCalls.Load())
	}
}

func TestClearUnauthorizedRecovery_DisablesHook(t *testing.T) {
	var requestCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL, &memStore{})
	c.setToken("some-token")
	c.SetUnauthorizedRecovery(func(ctx context.Context) (string, error) {
		return "recovered", nil
	})
	c.ClearUnauthorizedRecovery()

	err := c.do(t.Context(), http.MethodGet, "/v1/identity/me", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("do = %v, want *APIError{401}", err)
	}
	if requestCount.Load() != 1 {
		t.Fatalf("requestCount = %d, want exactly 1 after ClearUnauthorizedRecovery", requestCount.Load())
	}
}
