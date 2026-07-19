package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// These tests drive branchFiscalPoller directly with injected fetch/emit
// funcs — no Wails runtime context anywhere (runtime.EventsEmit panics
// outside one, see app.go's emitEvent doc comment), matching how app_test.go
// avoids the runtime via the openURL seam.

// pollerHarness collects emitted snapshots and counts fetches, thread-safely
// (the poll loop runs on its own goroutine).
type pollerHarness struct {
	mu       sync.Mutex
	emitted  []BranchPendingFiscalDTO
	warnings []string
	fetches  int
}

func (h *pollerHarness) emit(dto BranchPendingFiscalDTO) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.emitted = append(h.emitted, dto)
}

func (h *pollerHarness) warn(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.warnings = append(h.warnings, msg)
}

func (h *pollerHarness) countFetch() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fetches++
	return h.fetches
}

func (h *pollerHarness) snapshot() (emitted []BranchPendingFiscalDTO, warnings []string, fetches int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]BranchPendingFiscalDTO(nil), h.emitted...),
		append([]string(nil), h.warnings...),
		h.fetches
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

func TestBranchFiscalPoller_EmitsSnapshotAndStopsOnCancel(t *testing.T) {
	h := &pollerHarness{}
	registeredAt := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)

	p := &branchFiscalPoller{
		branchID: "branch-1",
		fetch: func(_ context.Context, branchID string) (apiclient.BranchPendingFiscal, error) {
			h.countFetch()
			if branchID != "branch-1" {
				t.Errorf("fetch got branch %q, want branch-1", branchID)
			}
			return apiclient.BranchPendingFiscal{
				BranchID: branchID,
				AsOf:     registeredAt,
				Pending: []apiclient.PendingFiscalItem{{
					PaymentID:    "pay-1",
					CheckID:      "check-1",
					AmountTotal:  12_500,
					RegisteredAt: registeredAt,
					AgeSeconds:   4,
				}},
			}, nil
		},
		emit:    h.emit,
		logWarn: h.warn,
	}

	p.Start(context.Background())
	waitFor(t, func() bool { e, _, _ := h.snapshot(); return len(e) > 0 }, "first snapshot")
	p.Stop()

	emitted, _, _ := h.snapshot()
	if len(emitted[0].Pending) != 1 {
		t.Fatalf("emitted %d pending items, want 1", len(emitted[0].Pending))
	}
	item := emitted[0].Pending[0]
	if item.PaymentID != "pay-1" || item.CheckID != "check-1" || item.AmountTotal != 12_500 {
		t.Errorf("unexpected pending item: %+v", item)
	}
	if item.AgeSeconds != 4 {
		t.Errorf("age_seconds = %d, want 4", item.AgeSeconds)
	}

	// Stop must have fully drained the goroutine: no further fetch may land.
	_, _, before := h.snapshot()
	time.Sleep(20 * time.Millisecond)
	_, _, after := h.snapshot()
	if after != before {
		t.Errorf("poller kept fetching after Stop (%d -> %d)", before, after)
	}
}

func TestBranchFiscalPoller_EmitsEmptySnapshotsSoDotsCanClear(t *testing.T) {
	h := &pollerHarness{}
	p := &branchFiscalPoller{
		branchID: "branch-1",
		fetch: func(context.Context, string) (apiclient.BranchPendingFiscal, error) {
			h.countFetch()
			return apiclient.BranchPendingFiscal{BranchID: "branch-1"}, nil
		},
		emit:    h.emit,
		logWarn: h.warn,
	}

	p.Start(context.Background())
	waitFor(t, func() bool { e, _, _ := h.snapshot(); return len(e) > 0 }, "empty snapshot")
	p.Stop()

	emitted, _, _ := h.snapshot()
	if len(emitted) == 0 {
		t.Fatal("an empty snapshot must still be emitted — it is what clears a stale amber dot")
	}
	if len(emitted[0].Pending) != 0 {
		t.Errorf("expected empty pending, got %+v", emitted[0].Pending)
	}
}

func TestBranchFiscalPoller_ForbiddenStopsLoopAfterOneLog(t *testing.T) {
	h := &pollerHarness{}
	p := &branchFiscalPoller{
		branchID: "branch-1",
		fetch: func(context.Context, string) (apiclient.BranchPendingFiscal, error) {
			h.countFetch()
			return apiclient.BranchPendingFiscal{}, &apiclient.APIError{StatusCode: 403, Body: "forbidden"}
		},
		emit:    h.emit,
		logWarn: h.warn,
	}

	p.Start(context.Background())
	waitFor(t, func() bool { _, w, _ := h.snapshot(); return len(w) > 0 }, "forbidden warning")

	// Well past the 3s active interval would be needed for a second attempt;
	// a short sleep is enough to prove the loop exited rather than slept.
	time.Sleep(30 * time.Millisecond)
	emitted, warnings, fetches := h.snapshot()

	if fetches != 1 {
		t.Errorf("403 must stop the poller after ONE attempt, got %d fetches", fetches)
	}
	if len(warnings) != 1 {
		t.Errorf("403 must be logged exactly once, got %d warnings: %v", len(warnings), warnings)
	}
	if len(emitted) != 0 {
		t.Errorf("403 must emit nothing, got %+v", emitted)
	}

	// The goroutine already self-exited; Stop must not hang on its done chan.
	p.Stop()
}

func TestBranchFiscalPoller_TransientErrorKeepsPolling(t *testing.T) {
	h := &pollerHarness{}
	p := &branchFiscalPoller{
		branchID: "branch-1",
		fetch: func(context.Context, string) (apiclient.BranchPendingFiscal, error) {
			h.countFetch()
			return apiclient.BranchPendingFiscal{}, &apiclient.APIError{StatusCode: 500, Body: "boom"}
		},
		emit:    h.emit,
		logWarn: h.warn,
	}

	p.Start(context.Background())
	waitFor(t, func() bool { _, w, _ := h.snapshot(); return len(w) > 0 }, "transient warning")
	p.Stop()

	emitted, _, _ := h.snapshot()
	if len(emitted) != 0 {
		t.Errorf("a failed fetch must not emit a snapshot, got %+v", emitted)
	}
}

func TestBranchFiscalPoller_StopCancelsInFlightFetch(t *testing.T) {
	h := &pollerHarness{}
	entered := make(chan struct{})
	var once sync.Once

	p := &branchFiscalPoller{
		branchID: "branch-1",
		fetch: func(ctx context.Context, _ string) (apiclient.BranchPendingFiscal, error) {
			once.Do(func() { close(entered) })
			// Block until the poller's context is cancelled — the real HTTP
			// call behaves the same way (NewRequestWithContext).
			<-ctx.Done()
			return apiclient.BranchPendingFiscal{}, ctx.Err()
		},
		emit:    h.emit,
		logWarn: h.warn,
	}

	p.Start(context.Background())
	<-entered

	done := make(chan struct{})
	go func() {
		p.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not abort an in-flight fetch — shutdown would block on the HTTP timeout")
	}

	_, warnings, _ := h.snapshot()
	if len(warnings) != 0 {
		t.Errorf("a cancelled fetch is an expected shutdown path, not a fault: %v", warnings)
	}
}

// --- App lifecycle wiring ------------------------------------------------

// newFiscalTestApp builds an App literal (no Wails runtime — see
// app_test.go's newTestApp) whose api points at srv and whose session token
// carries branchID.
func newFiscalTestApp(t *testing.T, srvURL, branchID string) (*App, *pollerHarness) {
	t.Helper()
	h := &pollerHarness{}
	app := &App{
		ctx: context.Background(),
		api: apiclient.New(srvURL, tokenstore.New(t.TempDir(), nil)),
		emitEvent: func(_ string, data any) {
			if dto, ok := data.(BranchPendingFiscalDTO); ok {
				h.emit(dto)
			}
		},
	}
	app.api.SetSessionToken(fakeCtxToken(t, "11111111-1111-1111-1111-111111111111", branchID))
	return app, h
}

func TestSyncBranchFiscalPoller_NoBranchContextNeverStarts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("poller must not issue a request without a branch context")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// A chain-wide staff session: valid token, empty bid claim.
	app, _ := newFiscalTestApp(t, srv.URL, "")
	app.syncBranchFiscalPoller()
	defer app.stopBranchFiscalPoller()

	if app.fiscalPoller != nil {
		t.Fatal("poller started without a branch context")
	}
	time.Sleep(20 * time.Millisecond)
}

func TestSyncBranchFiscalPoller_SameBranchIsIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeFiscalPending(t, w, "22222222-2222-2222-2222-222222222222")
	}))
	defer srv.Close()

	branchID := "22222222-2222-2222-2222-222222222222"
	app, _ := newFiscalTestApp(t, srv.URL, branchID)

	app.syncBranchFiscalPoller()
	first := app.fiscalPoller
	app.syncBranchFiscalPoller()
	second := app.fiscalPoller
	defer app.stopBranchFiscalPoller()

	if first == nil || first != second {
		t.Fatal("re-syncing the same branch must not restart the poller")
	}
}

func TestSyncBranchFiscalPoller_BranchSwitchReplacesPoller(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Query().Get("branch_id")]++
		mu.Unlock()
		writeFiscalPending(t, w, r.URL.Query().Get("branch_id"))
	}))
	defer srv.Close()

	branchA := "22222222-2222-2222-2222-222222222222"
	branchB := "33333333-3333-3333-3333-333333333333"

	app, _ := newFiscalTestApp(t, srv.URL, branchA)
	app.syncBranchFiscalPoller()
	pollerA := app.fiscalPoller
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen[branchA] > 0
	}, "branch A poll")

	app.api.SetSessionToken(fakeCtxToken(t, "11111111-1111-1111-1111-111111111111", branchB))
	app.syncBranchFiscalPoller()
	defer app.stopBranchFiscalPoller()

	if app.fiscalPoller == pollerA {
		t.Fatal("a branch switch must replace the poller, not keep the old branch's one")
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return seen[branchB] > 0
	}, "branch B poll")
}

func TestLogout_StopsBranchFiscalPoller(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFiscalPending(t, w, r.URL.Query().Get("branch_id"))
	}))
	defer srv.Close()

	branchID := "22222222-2222-2222-2222-222222222222"
	app, h := newFiscalTestApp(t, srv.URL, branchID)
	app.kcStore = tokenstore.NewKeycloak(t.TempDir(), nil)
	app.openURL = func(string) {}

	app.syncBranchFiscalPoller()
	waitFor(t, func() bool { e, _, _ := h.snapshot(); return len(e) > 0 }, "first snapshot")

	if err := app.Logout(); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if app.fiscalPoller != nil {
		t.Fatal("Logout must stop the branch fiscal poller")
	}
}

func TestIsForbidden(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"typed 403", &apiclient.APIError{StatusCode: 403}, true},
		{"wrapped 403", errors.New("x"), false},
		{"typed 500", &apiclient.APIError{StatusCode: 500}, false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isForbidden(tt.err); got != tt.want {
				t.Errorf("isForbidden = %v, want %v", got, tt.want)
			}
		})
	}
}

// fakeCtxToken builds an unsigned JWT-shaped context token carrying tid/bid
// claims — the only thing apiclient.Client.claims decodes (it deliberately
// does not verify the signature; see that method's doc comment). Mirrors
// internal/apiclient/pos_test.go's fakeContextToken, duplicated here because
// that one is unexported in another package.
func fakeCtxToken(t *testing.T, tenantID, branchID string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, err := json.Marshal(map[string]string{"tid": tenantID, "bid": branchID})
	if err != nil {
		t.Fatalf("marshal ctx token payload: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func writeFiscalPending(t *testing.T, w http.ResponseWriter, branchID string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"branch_id":        branchID,
		"as_of":            time.Now().UTC().Format(time.RFC3339),
		"pending":          []any{},
		"recently_settled": []any{},
	})
}
