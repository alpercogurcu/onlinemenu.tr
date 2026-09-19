package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
	"onlinemenu.tr/pos-desktop/internal/hardware"
	"onlinemenu.tr/pos-desktop/internal/hardware/escpos"
	"onlinemenu.tr/pos-desktop/internal/printedids"
	"onlinemenu.tr/pos-desktop/internal/receipt"
	"onlinemenu.tr/pos-desktop/internal/tokenstore"
)

// --- fakes ------------------------------------------------------------------

type fakeFrame struct {
	events []apiclient.KitchenEvent
	err    error
}

// fakeSource is a kitchenEventSource driven frame by frame from the test.
type fakeSource struct {
	frames chan fakeFrame
	closed chan struct{}
	once   sync.Once
}

func newFakeSource() *fakeSource {
	return &fakeSource{frames: make(chan fakeFrame, 16), closed: make(chan struct{})}
}

func (s *fakeSource) Next() ([]apiclient.KitchenEvent, error) {
	select {
	case f := <-s.frames:
		return f.events, f.err
	case <-s.closed:
		return nil, errors.New("closed")
	}
}

func (s *fakeSource) Close() { s.once.Do(func() { close(s.closed) }) }

func (s *fakeSource) send(events ...apiclient.KitchenEvent) { s.frames <- fakeFrame{events: events} }
func (s *fakeSource) drop(err error)                        { s.frames <- fakeFrame{err: err} }

func qrOrder(id, status string) apiclient.KitchenEvent {
	return apiclient.KitchenEvent{Type: "order.placed", OrderID: id, Source: "online_qr", Status: status, TableLabel: "Masa 5"}
}

type dispatcherHarness struct {
	d *kitchenDispatcher

	mu       sync.Mutex
	printed  []string
	results  []KitchenPrintResultDTO
	warnings []string
	failIDs  map[string]error

	set     *printedids.Set
	sources chan *fakeSource
	opens   atomic.Int32
	openErr func(attempt int) error
}

// newHarness wires a dispatcher whose print function mimics App: it records the
// call and, on success, marks the id printed in the shared set.
func newHarness(t *testing.T) *dispatcherHarness {
	t.Helper()
	h := &dispatcherHarness{
		set:     printedids.Open(t.TempDir(), 500, nil),
		sources: make(chan *fakeSource, 8),
		failIDs: map[string]error{},
	}
	h.d = &kitchenDispatcher{
		branchID: "branch-1",
		open: func(ctx context.Context, branchID string) (kitchenEventSource, error) {
			n := int(h.opens.Add(1))
			if h.openErr != nil {
				if err := h.openErr(n); err != nil {
					return nil, err
				}
			}
			select {
			case s := <-h.sources:
				return s, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		print: func(_ context.Context, orderID string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.printed = append(h.printed, orderID)
			if err := h.failIDs[orderID]; err != nil {
				return err
			}
			return h.set.Add(orderID)
		},
		printed: h.set,
		emit: func(dto KitchenPrintResultDTO) {
			h.mu.Lock()
			h.results = append(h.results, dto)
			h.mu.Unlock()
		},
		logWarn: func(msg string) {
			h.mu.Lock()
			h.warnings = append(h.warnings, msg)
			h.mu.Unlock()
		},
		backoffMin: time.Millisecond,
		backoffMax: 4 * time.Millisecond,
	}
	return h
}

func (h *dispatcherHarness) start(t *testing.T) {
	t.Helper()
	h.d.Start(context.Background())
	t.Cleanup(h.d.Stop)
}

func (h *dispatcherHarness) printedIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.printed...)
}

func (h *dispatcherHarness) resultList() []KitchenPrintResultDTO {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]KitchenPrintResultDTO(nil), h.results...)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// settle gives the dispatcher a moment to (not) act — used to prove a negative.
func settle() { time.Sleep(40 * time.Millisecond) }

// --- shouldPrint ------------------------------------------------------------

func TestKitchenDispatcher_ShouldPrint(t *testing.T) {
	printedSet := printedids.Open(t.TempDir(), 10, nil)
	_ = printedSet.Add("done")
	d := &kitchenDispatcher{printed: printedSet}

	tests := []struct {
		name string
		evt  apiclient.KitchenEvent
		want bool
	}{
		{"qr pending", qrOrder("o1", "pending"), true},
		{"qr accepted, never printed (accepted while POS was closed)", qrOrder("o1", "accepted"), true},
		{"qr status_changed event for an unseen order", apiclient.KitchenEvent{Type: "order.status_changed", OrderID: "o1", Source: "online_qr", Status: "accepted"}, true},
		{"pos-origin order is printed by the POS that placed it", apiclient.KitchenEvent{OrderID: "o1", Source: "pos", Status: "pending"}, false},
		{"unknown source is not guessed at", apiclient.KitchenEvent{OrderID: "o1", Source: "", Status: "pending"}, false},
		{"already preparing: the kitchen has it", qrOrder("o1", "preparing"), false},
		{"ready", qrOrder("o1", "ready"), false},
		{"rejected", qrOrder("o1", "rejected"), false},
		{"cancelled", qrOrder("o1", "cancelled"), false},
		{"already printed", qrOrder("done", "pending"), false},
		{"no order id", apiclient.KitchenEvent{Source: "online_qr", Status: "pending"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.shouldPrint(tt.evt); got != tt.want {
				t.Fatalf("shouldPrint = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- run loop ---------------------------------------------------------------

func TestKitchenDispatcher_SnapshotPrintsOnlyUnprintedQROrders(t *testing.T) {
	h := newHarness(t)
	_ = h.set.Add("already")
	src := newFakeSource()
	h.sources <- src
	h.start(t)

	src.send(
		qrOrder("new-1", "pending"),
		apiclient.KitchenEvent{Type: "order.placed", OrderID: "pos-1", Source: "pos", Status: "pending"},
		qrOrder("already", "pending"),
		qrOrder("cooking", "preparing"),
		qrOrder("new-2", "accepted"),
	)

	eventually(t, "two tickets", func() bool { return len(h.printedIDs()) == 2 })
	settle()
	if got := strings.Join(h.printedIDs(), ","); got != "new-1,new-2" {
		t.Fatalf("printed %q, want new-1,new-2 (in arrival order)", got)
	}
}

func TestKitchenDispatcher_LiveEventPrintsOnce(t *testing.T) {
	h := newHarness(t)
	src := newFakeSource()
	h.sources <- src
	h.start(t)

	src.send() // empty snapshot
	src.send(qrOrder("o1", "pending"))
	eventually(t, "live ticket", func() bool { return len(h.printedIDs()) == 1 })

	// The follow-up status change of the same order must not print again.
	src.send(apiclient.KitchenEvent{Type: "order.status_changed", OrderID: "o1", Source: "online_qr", Status: "accepted"})
	src.send(qrOrder("o1", "pending"))
	settle()
	if n := len(h.printedIDs()); n != 1 {
		t.Fatalf("printed %d times, want exactly once", n)
	}
}

func TestKitchenDispatcher_DuplicateInOneFrameIsPrintedOnce(t *testing.T) {
	h := newHarness(t)
	src := newFakeSource()
	h.sources <- src
	h.start(t)

	src.send(qrOrder("o1", "pending"), qrOrder("o1", "pending"))
	eventually(t, "ticket", func() bool { return len(h.printedIDs()) >= 1 })
	settle()
	if n := len(h.printedIDs()); n != 1 {
		t.Fatalf("printed %d times, want 1", n)
	}
}

func TestKitchenDispatcher_PrintFailureEmitsResultAndKeepsRunning(t *testing.T) {
	h := newHarness(t)
	h.failIDs["bad"] = errors.New("print kitchen ticket: network printer: not connected")
	src := newFakeSource()
	h.sources <- src
	h.start(t)

	src.send(qrOrder("bad", "pending"), qrOrder("good", "pending"))
	eventually(t, "both attempts", func() bool { return len(h.resultList()) == 2 })

	res := h.resultList()
	if res[0].OrderID != "bad" || res[0].OK || !strings.Contains(res[0].Error, "not connected") || res[0].TableLabel != "Masa 5" {
		t.Fatalf("failure result = %+v", res[0])
	}
	if res[1].OrderID != "good" || !res[1].OK || res[1].Error != "" {
		t.Fatalf("success result = %+v", res[1])
	}
	if h.set.Has("bad") || !h.set.Has("good") {
		t.Fatal("a failed print must stay unprinted so it can be retried; a good one must be recorded")
	}
}

func TestKitchenDispatcher_FailedOrderIsRetriedOnItsNextEvent(t *testing.T) {
	h := newHarness(t)
	h.failIDs["o1"] = errors.New("printer offline")
	src := newFakeSource()
	h.sources <- src
	h.start(t)

	src.send(qrOrder("o1", "pending"))
	eventually(t, "failure", func() bool { return len(h.resultList()) == 1 })

	h.mu.Lock()
	delete(h.failIDs, "o1") // printer came back
	h.mu.Unlock()
	src.send(apiclient.KitchenEvent{Type: "order.status_changed", OrderID: "o1", Source: "online_qr", Status: "accepted", TableLabel: "Masa 5"})
	eventually(t, "retry success", func() bool { return len(h.resultList()) == 2 })

	if last := h.resultList()[1]; !last.OK || last.OrderID != "o1" {
		t.Fatalf("retry result = %+v", last)
	}
}

func TestKitchenDispatcher_ReconnectSkipsPrintedAndPrintsNew(t *testing.T) {
	h := newHarness(t)
	first, second := newFakeSource(), newFakeSource()
	h.sources <- first
	h.sources <- second
	h.start(t)

	first.send(qrOrder("o1", "pending"))
	eventually(t, "first ticket", func() bool { return len(h.printedIDs()) == 1 })
	first.drop(errors.New("connection reset"))

	// The replayed snapshot lists o1 again plus an order placed during the outage.
	second.send(qrOrder("o1", "pending"), qrOrder("o2", "pending"))
	eventually(t, "post-reconnect ticket", func() bool { return len(h.printedIDs()) == 2 })
	settle()

	if got := strings.Join(h.printedIDs(), ","); got != "o1,o2" {
		t.Fatalf("printed %q, want o1,o2 — o1 must not be printed again after reconnect", got)
	}
	if h.opens.Load() != 2 {
		t.Fatalf("opens = %d, want 2", h.opens.Load())
	}
}

func TestKitchenDispatcher_RetriesConnectWithBackoff(t *testing.T) {
	h := newHarness(t)
	h.openErr = func(attempt int) error {
		if attempt < 4 {
			return errors.New("connection refused")
		}
		return nil
	}
	src := newFakeSource()
	h.sources <- src
	h.start(t)

	src.send(qrOrder("o1", "pending"))
	eventually(t, "ticket after retries", func() bool { return len(h.printedIDs()) == 1 })
	if h.opens.Load() != 4 {
		t.Fatalf("opens = %d, want 4", h.opens.Load())
	}
	h.mu.Lock()
	warned := len(h.warnings)
	h.mu.Unlock()
	if warned == 0 {
		t.Fatal("failed connects must be logged, not swallowed")
	}
}

func TestKitchenDispatcher_ForbiddenStopsForGood(t *testing.T) {
	h := newHarness(t)
	h.openErr = func(int) error {
		return fmt.Errorf("open: %w", &apiclient.APIError{StatusCode: http.StatusForbidden, Body: "forbidden"})
	}
	h.d.Start(context.Background())

	select {
	case <-h.d.done:
	case <-time.After(time.Second):
		t.Fatal("a 403 is a role problem, not a transient fault: the dispatcher must stop retrying")
	}
	if h.opens.Load() != 1 {
		t.Fatalf("opens = %d, want 1", h.opens.Load())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one terminal notice", h.warnings)
	}
	h.d.Stop() // must not hang after a self-exit
}

func TestKitchenDispatcher_StopUnblocksReadAndClosesSource(t *testing.T) {
	h := newHarness(t)
	src := newFakeSource()
	h.sources <- src
	h.d.Start(context.Background())
	settle() // now blocked in Next

	stopped := make(chan struct{})
	go func() { h.d.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop hung on a blocked read")
	}
	select {
	case <-src.closed:
	default:
		t.Fatal("Stop must close the open stream")
	}
}

func TestKitchenDispatcher_StopOnNeverStartedIsSafe(t *testing.T) {
	(&kitchenDispatcher{}).Stop()
}

// --- end to end: real WebSocket -> App.PrintKitchenTicket -> MockPrinter -----

func TestKitchenDispatcher_EndToEndOverRealWebSocket(t *testing.T) {
	const orderID = "a1b2c3d4-aaaa-bbbb-cccc-000000000001"
	checkID := "check-9"

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/pos/ws/kitchen", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Query().Get("branch_id") != "branch-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		snapshot, _ := json.Marshal(map[string]any{"type": "snapshot", "orders": []map[string]any{
			{"type": "order.placed", "order_id": orderID, "check_id": checkID, "table_label": "Masa 9", "source": "online_qr", "status": "pending"},
		}})
		_ = conn.WriteMessage(websocket.TextMessage, snapshot)
		time.Sleep(300 * time.Millisecond)
	})
	mux.HandleFunc("/api/v1/pos/orders/"+orderID, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(apiclient.Order{
			ID: orderID, CheckID: &checkID, CreatedAt: time.Date(2026, 9, 19, 14, 32, 0, 0, time.UTC),
			Items: []apiclient.OrderItem{{ID: "i1", ProductName: "Çiğ Köfte", Quantity: 2, Note: "acısız"}},
		})
	})
	mux.HandleFunc("/api/v1/pos/checks/"+checkID, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(apiclient.Check{ID: checkID, TableLabel: "Masa 9"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	kitchen := hardware.NewMockPrinter()
	a := &App{
		ctx:            context.Background(),
		api:            apiclient.New(srv.URL, tokenstore.New(t.TempDir(), nil)),
		printer:        hardware.NewMockPrinter(),
		kitchenPrinter: kitchen,
		receiptConfig:  receipt.Config{Width: escpos.Width48},
		printedOrders:  printedids.Open(dir, 500, nil),
	}
	a.api.SetSessionToken("tok")

	var results atomic.Int32
	d := &kitchenDispatcher{
		branchID: "branch-1",
		open: func(ctx context.Context, branchID string) (kitchenEventSource, error) {
			return a.api.OpenKitchenStream(ctx, branchID)
		},
		print:      a.printKitchenTicket,
		printed:    a.printedOrders,
		emit:       func(KitchenPrintResultDTO) { results.Add(1) },
		logWarn:    func(string) {},
		backoffMin: time.Millisecond,
		backoffMax: 5 * time.Millisecond,
	}
	d.Start(context.Background())
	defer d.Stop()

	eventually(t, "kitchen job", func() bool { return kitchen.LastJob() != nil })
	job := kitchen.LastJob()
	if !bytes.Contains(job, escpos.EncodeCP857("2x  Çiğ Köfte")) || !bytes.Contains(job, escpos.EncodeCP857("> acısız")) {
		t.Fatalf("ticket lacks the order lines: %q", job)
	}
	eventually(t, "result event", func() bool { return results.Load() >= 1 })

	if !a.printedOrders.Has(orderID) {
		t.Fatal("printing must record the order id")
	}
	data, err := os.ReadFile(filepath.Join(dir, "printed-kitchen-orders.json"))
	if err != nil || !bytes.Contains(data, []byte(orderID)) {
		t.Fatalf("id not persisted: %v %s", err, data)
	}
}

// --- App lifecycle wiring ---------------------------------------------------

// newKitchenLifecycleApp builds an App whose kitchen socket points at a server
// that records the branch of every connection and holds it open.
func newKitchenLifecycleApp(t *testing.T, branchID string) (*App, func() map[string]int, *httptest.Server) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Reject") != "" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		mu.Lock()
		seen[r.URL.Query().Get("branch_id")]++
		mu.Unlock()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"snapshot","orders":[]}`))
		_, _, _ = conn.ReadMessage() // hold open until the client goes away
	}))
	t.Cleanup(srv.Close)

	app := &App{
		ctx:                    context.Background(),
		api:                    apiclient.New(srv.URL, tokenstore.New(t.TempDir(), nil)),
		kitchenPrinter:         hardware.NewMockPrinter(),
		printedOrders:          printedids.Open(t.TempDir(), 10, nil),
		kitchenDispatchEnabled: true,
		emitEvent:              func(string, any) {},
	}
	app.api.SetSessionToken(fakeCtxToken(t, "11111111-1111-1111-1111-111111111111", branchID))
	snapshot := func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int{}
		for k, v := range seen {
			out[k] = v
		}
		return out
	}
	return app, snapshot, srv
}

func TestSyncKitchenDispatcher_StartsOnBranchAndIsIdempotent(t *testing.T) {
	branch := "22222222-2222-2222-2222-222222222222"
	app, seen, _ := newKitchenLifecycleApp(t, branch)
	defer app.stopKitchenDispatcher()

	app.syncKitchenDispatcher()
	first := app.kitchenDispatcher
	eventually(t, "socket connection", func() bool { return seen()[branch] == 1 })

	app.syncKitchenDispatcher()
	if first == nil || app.kitchenDispatcher != first {
		t.Fatal("re-syncing the same branch must not restart the dispatcher")
	}
	settle()
	if n := seen()[branch]; n != 1 {
		t.Fatalf("connections = %d, want 1", n)
	}
}

func TestSyncKitchenDispatcher_BranchSwitchReplacesDispatcher(t *testing.T) {
	branchA := "22222222-2222-2222-2222-222222222222"
	branchB := "33333333-3333-3333-3333-333333333333"
	app, seen, _ := newKitchenLifecycleApp(t, branchA)
	defer app.stopKitchenDispatcher()

	app.syncKitchenDispatcher()
	first := app.kitchenDispatcher
	eventually(t, "branch A", func() bool { return seen()[branchA] == 1 })

	app.api.SetSessionToken(fakeCtxToken(t, "11111111-1111-1111-1111-111111111111", branchB))
	app.syncKitchenDispatcher()
	if app.kitchenDispatcher == first {
		t.Fatal("a branch switch must replace the dispatcher")
	}
	eventually(t, "branch B", func() bool { return seen()[branchB] == 1 })
}

func TestSyncKitchenDispatcher_DoesNotStartWhenNotApplicable(t *testing.T) {
	branch := "22222222-2222-2222-2222-222222222222"
	tests := []struct {
		name   string
		mutate func(a *App)
	}{
		{"chain-wide session without a branch", func(a *App) {
			a.api.SetSessionToken(fakeCtxToken(t, "11111111-1111-1111-1111-111111111111", ""))
		}},
		{"opted out via config", func(a *App) { a.kitchenDispatchEnabled = false }},
		{"no frontend attached", func(a *App) { a.emitEvent = nil }},
		{"no printed-order memory", func(a *App) { a.printedOrders = nil }},
		{"no kitchen printer", func(a *App) { a.kitchenPrinter = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, seen, _ := newKitchenLifecycleApp(t, branch)
			tt.mutate(app)
			app.syncKitchenDispatcher()
			defer app.stopKitchenDispatcher()

			if app.kitchenDispatcher != nil {
				t.Fatal("dispatcher started although it must not")
			}
			settle()
			if len(seen()) != 0 {
				t.Fatalf("unexpected connections: %v", seen())
			}
		})
	}
}

func TestSyncKitchenDispatcher_RestartsAfterSelfStop(t *testing.T) {
	branch := "22222222-2222-2222-2222-222222222222"
	app, _, srv := newKitchenLifecycleApp(t, branch)
	defer app.stopKitchenDispatcher()

	// First attempt: the server refuses (403) and the dispatcher gives up.
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer rejecting.Close()
	deadApp := &App{
		ctx:                    context.Background(),
		api:                    apiclient.New(rejecting.URL, tokenstore.New(t.TempDir(), nil)),
		kitchenPrinter:         hardware.NewMockPrinter(),
		printedOrders:          printedids.Open(t.TempDir(), 10, nil),
		kitchenDispatchEnabled: true,
		emitEvent:              func(string, any) {},
	}
	deadApp.api.SetSessionToken(fakeCtxToken(t, "11111111-1111-1111-1111-111111111111", branch))
	defer deadApp.stopKitchenDispatcher()

	deadApp.syncKitchenDispatcher()
	first := deadApp.kitchenDispatcher
	eventually(t, "self-stop on 403", func() bool { return first.finished() })

	// A later login (better role) syncs again on the SAME branch: it must get a
	// fresh dispatcher, not keep the dead one.
	deadApp.api = apiclient.New(srv.URL, tokenstore.New(t.TempDir(), nil))
	deadApp.api.SetSessionToken(fakeCtxToken(t, "11111111-1111-1111-1111-111111111111", branch))
	deadApp.syncKitchenDispatcher()
	if deadApp.kitchenDispatcher == first || deadApp.kitchenDispatcher.finished() {
		t.Fatal("a self-stopped dispatcher must be replaced on the next sync")
	}
}

func TestLogout_StopsKitchenDispatcher(t *testing.T) {
	branch := "22222222-2222-2222-2222-222222222222"
	app, seen, _ := newKitchenLifecycleApp(t, branch)
	app.kcStore = tokenstore.NewKeycloak(t.TempDir(), nil)
	app.openURL = func(string) {}

	app.syncKitchenDispatcher()
	eventually(t, "connection", func() bool { return seen()[branch] == 1 })

	if err := app.Logout(); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if app.kitchenDispatcher != nil {
		t.Fatal("Logout must stop the kitchen dispatcher")
	}
}
