package main

import (
	"context"
	"sync"
	"time"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
)

// kitchenPrintResultEvent is the Wails topic the frontend listens on for the
// outcome of every dispatcher-driven kitchen print. Success is emitted too:
// it is what lets the frontend clear a failure banner when a later automatic
// retry of the same order finally succeeds.
const kitchenPrintResultEvent = "hardware:kitchen-print"

const (
	kitchenBackoffMin = time.Second
	kitchenBackoffMax = 15 * time.Second

	// kitchenQueueSize bounds orders waiting for the printer. A full queue
	// blocks the socket reader (backpressure), it never drops an order: a
	// dropped one would simply never reach the kitchen.
	kitchenQueueSize = 256

	// sourceOnlineQR is pos/domain's Source for guest QR self-orders — the
	// only orders nobody at the counter rings in, hence the only ones this
	// dispatcher prints. See shouldPrint.
	sourceOnlineQR = "online_qr"
)

// KitchenPrintResultDTO is the JSON shape of kitchenPrintResultEvent. Error is
// empty on success.
type KitchenPrintResultDTO struct {
	OrderID    string `json:"order_id"`
	TableLabel string `json:"table_label"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// kitchenEventSource is the open kitchen WebSocket, as the dispatcher sees it
// (*apiclient.KitchenStream in production).
type kitchenEventSource interface {
	Next() ([]apiclient.KitchenEvent, error)
	Close()
}

// printedChecker is the read side of the printed-order memory
// (*printedids.Set). The dispatcher only asks; the print function is what
// records an order as printed, so a POS-placed order, a reprint and a
// dispatcher print all feed the same memory through one code path.
type printedChecker interface {
	Has(id string) bool
}

// kitchenDispatcher prints a kitchen ticket for every order that reaches the
// branch WITHOUT passing through this station's PlaceOrder — today, QR
// self-orders — by following the backend's kitchen WebSocket. It is the
// restaurant-with-a-printer-instead-of-a-KDS counterpart of the admin KDS page.
//
// Shaped like branchFiscalPoller (Start / Stop, injected collaborators, one
// instance per branch) so it can be tested with no Wails runtime in sight.
//
// Two goroutines, on purpose: the reader keeps consuming the socket (which is
// what answers the server's heartbeat pings) while the printer worker may
// spend seconds in an HTTP call or a slow printer write. Doing both in one
// loop would let a slow print get the connection dropped for missing a pong.
type kitchenDispatcher struct {
	branchID string

	// open connects one stream; called again after every disconnect.
	open func(ctx context.Context, branchID string) (kitchenEventSource, error)

	// print produces the ticket and, on success, records the order as printed.
	print func(ctx context.Context, orderID string) error

	printed printedChecker

	// emit reports the outcome of each print attempt to the frontend.
	emit func(KitchenPrintResultDTO)

	// logWarn records connection trouble and the single terminal 403.
	logWarn func(msg string)

	backoffMin, backoffMax time.Duration

	mu       sync.Mutex
	inflight map[string]struct{}
	queue    chan kitchenJob

	cancel context.CancelFunc
	done   chan struct{}
}

type kitchenJob struct {
	orderID    string
	tableLabel string
}

// Start launches the dispatcher against a child of ctx. Call once per
// instance; App.syncKitchenDispatcher builds a fresh one per branch.
func (d *kitchenDispatcher) Start(ctx context.Context) {
	runCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.done = make(chan struct{})
	d.inflight = make(map[string]struct{})
	d.queue = make(chan kitchenJob, kitchenQueueSize)
	if d.backoffMin <= 0 {
		d.backoffMin = kitchenBackoffMin
	}
	if d.backoffMax < d.backoffMin {
		d.backoffMax = kitchenBackoffMax
	}
	go d.run(runCtx)
}

// Stop cancels the dispatcher and waits for both goroutines to exit. Safe on a
// never-started or already-stopped (including self-stopped) dispatcher.
func (d *kitchenDispatcher) Stop() {
	if d.cancel == nil {
		return
	}
	d.cancel()
	<-d.done
}

func (d *kitchenDispatcher) run(ctx context.Context) {
	defer close(d.done)

	loopCtx, stopLoop := context.WithCancel(ctx)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		d.printLoop(loopCtx)
	}()

	d.connectLoop(loopCtx)

	stopLoop()
	workers.Wait()
}

// connectLoop keeps a stream open until ctx ends or the session is refused for
// good.
func (d *kitchenDispatcher) connectLoop(ctx context.Context) {
	backoff := d.backoffMin
	for ctx.Err() == nil {
		src, err := d.open(ctx, d.branchID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if isForbidden(err) {
				// A role property (no pos.order.read), not a transient fault:
				// retrying every few seconds all shift would be a
				// guaranteed-useless loop. A later login builds a fresh
				// dispatcher and may have a different role.
				d.logWarn("mutfak fişi otomatik baskısı kapalı: bu oturumun mutfak akışına erişim yetkisi yok (pos.order.read): " + err.Error())
				return
			}
			d.logWarn("mutfak akışına bağlanılamadı, yeniden denenecek: " + err.Error())
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, d.backoffMax)
			continue
		}

		gotFrame := d.readLoop(ctx, src)
		src.Close()
		if gotFrame {
			// A connection that actually delivered its snapshot was healthy;
			// only consecutive failures should grow the delay.
			backoff = d.backoffMin
		}
		if ctx.Err() != nil {
			return
		}
		d.logWarn("mutfak akışı koptu, yeniden bağlanılıyor")
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, d.backoffMax)
	}
}

// readLoop consumes one stream until it fails, queuing every order that needs
// a ticket. Reports whether at least one frame arrived.
func (d *kitchenDispatcher) readLoop(ctx context.Context, src kitchenEventSource) bool {
	// Next only returns once the connection errors, so cancellation must close
	// the source to unblock it.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			src.Close()
		case <-stop:
		}
	}()

	gotFrame := false
	for {
		events, err := src.Next()
		if err != nil {
			return gotFrame
		}
		gotFrame = true
		for _, evt := range events {
			if !d.enqueue(ctx, evt) {
				return gotFrame
			}
		}
	}
}

// shouldPrint decides whether evt is an order this station still owes the
// kitchen a ticket for.
//
//   - Only QR self-orders. A POS-placed order is printed by the station that
//     placed it (App.PrintKitchenTicket right after PlaceOrder); printing it
//     here as well would double every ticket and, with several tills in one
//     branch, print each order once per till. An empty/unknown source is
//     skipped rather than guessed at, for the same reason.
//   - Only pending/accepted. Those are the two statuses before cooking starts;
//     preparing/ready mean the kitchen already has the order from somewhere
//     else, so a ticket now would just cause a second dish.
//   - Any event kind, not just order.placed. The backend can miss the
//     placed event of an order committed in the instant between a snapshot and
//     the room registration (its own doc calls that self-correcting via the
//     next status change), and an order accepted while this POS was closed
//     reaches us only as an accepted row in the snapshot. The printed-order
//     memory is what makes "print on any sighting" safe.
func (d *kitchenDispatcher) shouldPrint(evt apiclient.KitchenEvent) bool {
	if evt.OrderID == "" || evt.Source != sourceOnlineQR {
		return false
	}
	if evt.Status != "pending" && evt.Status != "accepted" {
		return false
	}
	return !d.printed.Has(evt.OrderID)
}

// enqueue queues evt's order for printing unless it is not owed or is already
// waiting. Returns false only if ctx ended while queuing.
func (d *kitchenDispatcher) enqueue(ctx context.Context, evt apiclient.KitchenEvent) bool {
	if !d.shouldPrint(evt) {
		return true
	}

	d.mu.Lock()
	if _, waiting := d.inflight[evt.OrderID]; waiting {
		d.mu.Unlock()
		return true
	}
	d.inflight[evt.OrderID] = struct{}{}
	d.mu.Unlock()

	select {
	case d.queue <- kitchenJob{orderID: evt.OrderID, tableLabel: evt.TableLabel}:
		return true
	case <-ctx.Done():
		d.release(evt.OrderID)
		return false
	}
}

func (d *kitchenDispatcher) release(orderID string) {
	d.mu.Lock()
	delete(d.inflight, orderID)
	d.mu.Unlock()
}

// printLoop prints queued orders one at a time. A print failure is reported and
// the loop carries on: one dead printer moment must not stop later orders, and
// the failed order stays unrecorded so its next sighting (any later event, or
// the snapshot after a reconnect) retries it.
func (d *kitchenDispatcher) printLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-d.queue:
			d.process(ctx, job)
			d.release(job.orderID)
		}
	}
}

func (d *kitchenDispatcher) process(ctx context.Context, job kitchenJob) {
	// Re-checked here: the order may have been printed (by a manual reprint)
	// while it waited in the queue.
	if d.printed.Has(job.orderID) {
		return
	}

	err := d.print(ctx, job.orderID)
	if ctx.Err() != nil && err != nil {
		// Shutdown interrupted the print — not a printer fault to alarm the
		// cashier about; the order is still unrecorded and prints next start.
		return
	}
	res := KitchenPrintResultDTO{OrderID: job.orderID, TableLabel: job.tableLabel, OK: err == nil}
	if err != nil {
		res.Error = err.Error()
		d.logWarn("mutfak fişi basılamadı (sipariş " + job.orderID + "): " + err.Error())
	}
	d.emit(res)
}

// sleepCtx waits d or until ctx ends; reports whether the full wait elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// warn logs through the Wails runtime when one is attached.
func (a *App) warn(msg string) {
	if a.logWarn != nil {
		a.logWarn(msg)
	}
}

// syncBranchWorkers brings every branch-scoped background worker (the
// fiscal poller and the kitchen dispatcher) in line with the session's
// current branch. Called after each authentication transition.
func (a *App) syncBranchWorkers() {
	a.syncBranchFiscalPoller()
	a.syncKitchenDispatcher()
}

// stopBranchWorkers stops them all, synchronously — see Logout and shutdown.
func (a *App) stopBranchWorkers() {
	a.stopBranchFiscalPoller()
	a.stopKitchenDispatcher()
}

// syncKitchenDispatcher is syncBranchFiscalPoller's twin for the kitchen
// dispatcher: idempotent, restarts on a branch change, stops without a branch.
// A dispatcher that gave up on its own (403) counts as absent, so a later
// login with a better role gets a fresh one even on the same branch.
func (a *App) syncKitchenDispatcher() {
	branchID := a.api.CurrentBranchID()

	a.kitchenMu.Lock()
	defer a.kitchenMu.Unlock()

	if a.kitchenDispatcher != nil && a.kitchenBranchID == branchID && !a.kitchenDispatcher.finished() {
		return
	}
	if a.kitchenDispatcher != nil {
		a.kitchenDispatcher.Stop()
		a.kitchenDispatcher = nil
		a.kitchenBranchID = ""
	}
	// No frontend attached (a test App literal), nothing to print with, or the
	// station opted out — see config.Config.KitchenDispatcherEnabled.
	if branchID == "" || !a.kitchenDispatchEnabled || a.emitEvent == nil || a.printedOrders == nil || a.kitchenPrinter == nil {
		return
	}

	d := &kitchenDispatcher{
		branchID: branchID,
		open: func(ctx context.Context, branch string) (kitchenEventSource, error) {
			stream, err := a.api.OpenKitchenStream(ctx, branch)
			if err != nil {
				return nil, err
			}
			return stream, nil
		},
		print:   a.printKitchenTicket,
		printed: a.printedOrders,
		emit:    func(dto KitchenPrintResultDTO) { a.emitEvent(kitchenPrintResultEvent, dto) },
		logWarn: a.warn,
	}
	d.Start(a.ctx)
	a.kitchenDispatcher = d
	a.kitchenBranchID = branchID
}

func (a *App) stopKitchenDispatcher() {
	a.kitchenMu.Lock()
	defer a.kitchenMu.Unlock()

	if a.kitchenDispatcher == nil {
		return
	}
	a.kitchenDispatcher.Stop()
	a.kitchenDispatcher = nil
	a.kitchenBranchID = ""
}

// finished reports whether the dispatcher's goroutines have exited — by Stop
// or on their own.
func (d *kitchenDispatcher) finished() bool {
	select {
	case <-d.done:
		return true
	default:
		return false
	}
}
