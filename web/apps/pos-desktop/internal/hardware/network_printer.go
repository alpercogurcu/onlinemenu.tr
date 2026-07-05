package hardware

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Default tuning for NetworkPrinter — overridable via the With* options
// below. These are conservative defaults for a LAN-attached thermal
// printer, not values a station operator is expected to ever need to
// change; the options exist mainly for tests (which need much shorter
// intervals than a real deployment).
const (
	defaultDialTimeout    = 5 * time.Second
	defaultWriteTimeout   = 5 * time.Second
	defaultHealthInterval = 15 * time.Second
	defaultBackoffMin     = 1 * time.Second
	defaultBackoffMax     = 30 * time.Second
)

// ErrPrinterNotConnected is returned by Print when there is no live TCP
// connection to write to (device disconnected/erroring). Print still emits
// a StatusError Event alongside this — see reportFault — so a caller that
// only checks the Events() stream (like app.go's forwarding goroutine) sees
// the same fault a caller that only checks Print's return value sees.
var ErrPrinterNotConnected = errors.New("network printer: not connected")

// NetworkPrinter is a hardware.Printer that talks ESC/POS to a receipt
// printer over a raw TCP socket (port 9100 — "direct/raw" printing, the
// universal fallback protocol nearly every network thermal printer
// supports regardless of brand). It follows the exact Device lifecycle
// contract MockPrinter documents (see mock_printer.go and this package's
// doc comment): StatusDisconnected is the zero value, every transition —
// including a dropped connection — is an explicit Event, Start's goroutine
// is the sole owner of status/conn state, and Wait blocks until that
// goroutine has fully exited after ctx cancellation.
//
// Connection loss detection: TCP alone does not reliably notice a
// half-open or silently-dead network path (a cut Ethernet cable rarely
// produces an immediate error on a socket nothing is being written to), so
// NetworkPrinter periodically probes liveness with the ESC/POS real-time
// status transmission command (DLE EOT n — see healthCheck) rather than
// only reacting to Print's own write errors. This is the piece that
// prevents the exact b2b regression this task's brief calls out:
// "kopan yazıcı 'bağlı' görünmeyecek".
type NetworkPrinter struct {
	addr string

	dialTimeout    time.Duration
	writeTimeout   time.Duration
	healthInterval time.Duration
	backoffMin     time.Duration
	backoffMax     time.Duration

	// mu guards status and conn together — every status transition and
	// every conn swap happens with mu held, but I/O itself (Write/Read on
	// conn) never happens while mu is held (see getConn/withConn) so a
	// slow write can never block Status()/Events() consumers or a
	// concurrent reportFault. This mirrors MockPrinter.setStatus's own
	// "release the lock before sending on the channel" discipline.
	mu     sync.Mutex
	status Status
	conn   net.Conn

	events  chan Event
	faultCh chan error
	wg      sync.WaitGroup

	dial func(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error)
}

// NetworkPrinterOption configures NetworkPrinter construction — see
// WithDialTimeout, WithWriteTimeout, WithHealthInterval, WithBackoff.
type NetworkPrinterOption func(*NetworkPrinter)

// WithDialTimeout overrides the default 5s TCP connect timeout.
func WithDialTimeout(d time.Duration) NetworkPrinterOption {
	return func(p *NetworkPrinter) { p.dialTimeout = d }
}

// WithWriteTimeout overrides the default 5s write/health-check-read
// deadline.
func WithWriteTimeout(d time.Duration) NetworkPrinterOption {
	return func(p *NetworkPrinter) { p.writeTimeout = d }
}

// WithHealthInterval overrides the default 15s liveness-probe interval.
// Tests use a much shorter interval to exercise reconnect without waiting
// on the production default.
func WithHealthInterval(d time.Duration) NetworkPrinterOption {
	return func(p *NetworkPrinter) { p.healthInterval = d }
}

// WithBackoff overrides the default 1s..30s exponential reconnect backoff.
func WithBackoff(min, max time.Duration) NetworkPrinterOption {
	return func(p *NetworkPrinter) { p.backoffMin, p.backoffMax = min, max }
}

// NewNetworkPrinter constructs a NetworkPrinter targeting addr (host:port,
// typically "<printer-ip>:9100"). Call Start to begin connecting.
func NewNetworkPrinter(addr string, opts ...NetworkPrinterOption) *NetworkPrinter {
	p := &NetworkPrinter{
		addr:           addr,
		dialTimeout:    defaultDialTimeout,
		writeTimeout:   defaultWriteTimeout,
		healthInterval: defaultHealthInterval,
		backoffMin:     defaultBackoffMin,
		backoffMax:     defaultBackoffMax,
		events:         make(chan Event, 8),
		faultCh:        make(chan error, 1),
		dial: func(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
			d := net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, "tcp", addr)
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *NetworkPrinter) Kind() string { return "printer" }

func (p *NetworkPrinter) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *NetworkPrinter) Events() <-chan Event { return p.events }

// Start begins connecting to addr in a tracked background goroutine and
// returns immediately — the initial connection attempt (and every
// subsequent reconnect) happens asynchronously, reported via Events()
// exactly like every later transition.
func (p *NetworkPrinter) Start(ctx context.Context) {
	p.wg.Add(1)
	go p.run(ctx)
}

// Wait blocks until the connection-management goroutine has fully exited
// (see Start). Intended for graceful shutdown (app.go's shutdown) and for
// goleak-verified tests.
func (p *NetworkPrinter) Wait() {
	p.wg.Wait()
}

// Print writes job to the current connection if there is one, applying
// writeTimeout as a hard deadline. A failure — no connection, or a write
// error — is reported on BOTH channels a caller might be watching: it is
// returned here, and (via reportFault) pushed as a StatusError Event, so
// neither a caller that only checks the error return nor one that only
// watches Events() misses it (see hardware.Printer's doc comment — this is
// the concrete fix for the b2b "//nolint:errcheck" silent-success
// regression this task's brief cites).
func (p *NetworkPrinter) Print(job []byte) error {
	conn := p.getConn()
	if conn == nil {
		p.reportFault(ErrPrinterNotConnected)
		return ErrPrinterNotConnected
	}

	if err := conn.SetWriteDeadline(time.Now().Add(p.writeTimeout)); err != nil {
		p.reportFault(err)
		return fmt.Errorf("network printer: set write deadline: %w", err)
	}
	_, err := conn.Write(job)
	_ = conn.SetWriteDeadline(time.Time{})
	if err != nil {
		p.reportFault(err)
		return fmt.Errorf("network printer: print: %w", err)
	}
	return nil
}

func (p *NetworkPrinter) getConn() net.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn
}

func (p *NetworkPrinter) setConn(c net.Conn) {
	p.mu.Lock()
	p.conn = c
	p.mu.Unlock()
}

// reportFault enqueues err for the run loop to act on — the same
// non-blocking, drop-if-already-queued discipline as
// MockPrinter.SimulateFault (a fault already queued and not yet processed
// makes a duplicate redundant, not hidden: the loop always re-evaluates and
// transitions to StatusError either way). Centralizing every status
// transition in the run loop (rather than mutating status directly here)
// is what keeps Print (called from an arbitrary Wails-invocation
// goroutine) from racing the health-check monitor's own fault detection.
func (p *NetworkPrinter) reportFault(err error) {
	select {
	case p.faultCh <- err:
	default:
	}
}

func (p *NetworkPrinter) setStatus(s Status, err error) {
	p.mu.Lock()
	p.status = s
	p.mu.Unlock()

	p.events <- Event{Status: s, Err: err, Timestamp: time.Now()}
}

// sleep waits for d or ctx cancellation, whichever comes first, returning
// false if ctx was cancelled first. Backoff between reconnect attempts MUST
// go through this (not a bare time.Sleep) — otherwise cancelling ctx during
// a long backoff would leave Wait blocked for up to backoffMax, and a
// goleak-based shutdown test would see the goroutine as still running.
func (p *NetworkPrinter) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

// run is the sole owner of p.status and p.conn for the lifetime of this
// Device — every read of those fields elsewhere goes through the mutex,
// but every WRITE to them happens here, so there is exactly one place that
// decides "what state are we in now", matching MockPrinter.run's contract.
func (p *NetworkPrinter) run(ctx context.Context) {
	defer p.wg.Done()
	defer close(p.events)

	backoff := p.backoffMin
	for {
		if ctx.Err() != nil {
			p.setStatus(StatusDisconnected, nil)
			return
		}

		conn, err := p.dial(ctx, p.addr, p.dialTimeout)
		if err != nil {
			p.setStatus(StatusError, fmt.Errorf("connect %s: %w", p.addr, err))
			if !p.sleep(ctx, backoff) {
				p.setStatus(StatusDisconnected, nil)
				return
			}
			backoff = nextBackoff(backoff, p.backoffMax)
			continue
		}

		p.setConn(conn)
		p.setStatus(StatusConnected, nil)
		backoff = p.backoffMin

		// Drain any stale fault left over from BEFORE this connection
		// existed — e.g. a Print call that ran while conn was nil (see
		// Print's doc comment) queued one on faultCh, but monitor only
		// drains faultCh while it's actually running (i.e. while connected),
		// so that fault would otherwise still be sitting there and be the
		// very first thing the fresh monitor below reads, misreporting a
		// connection that just successfully came up as immediately broken
		// (a spurious Connected -> Error -> Connected flicker — the exact
		// "misreport connectivity" failure mode this device exists to
		// avoid). This is a best-effort drain, not a lock-guarded handoff:
		// a genuine fault for THIS connection arriving in the narrow window
		// between setConn/setStatus above and this drain could in theory
		// also be discarded here, but the next healthCheck tick (run at
		// most healthInterval later) still catches a persistently broken
		// connection, so nothing is silently lost long-term.
		select {
		case <-p.faultCh:
		default:
		}

		monitorErr := p.monitor(ctx, conn)
		p.setConn(nil)
		_ = conn.Close()

		if ctx.Err() != nil {
			p.setStatus(StatusDisconnected, nil)
			return
		}

		p.setStatus(StatusError, monitorErr)
		if !p.sleep(ctx, backoff) {
			p.setStatus(StatusDisconnected, nil)
			return
		}
		backoff = nextBackoff(backoff, p.backoffMax)
	}
}

// monitor blocks while conn is presumed healthy: it returns nil the moment
// ctx is cancelled (a clean shutdown, not a fault), or a non-nil error the
// moment either Print reports a write fault (via faultCh) or a periodic
// healthCheck fails (a fault the run loop's caller — not Print — is the
// first to notice, e.g. a printer powered off between two Print calls).
func (p *NetworkPrinter) monitor(ctx context.Context, conn net.Conn) error {
	ticker := time.NewTicker(p.healthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-p.faultCh:
			return err
		case <-ticker.C:
			if err := p.healthCheck(conn); err != nil {
				return fmt.Errorf("health check: %w", err)
			}
		}
	}
}

// healthCheck sends the ESC/POS real-time status transmission command
// (DLE EOT n, n=1 selects "printer status") and reads its 1-byte reply.
// This is a standard, universally-supported ESC/POS command (distinct from
// the vendor-specific code-page selector in escpos.commands.go) used here
// purely as a liveness probe — the reply's actual status bits are not
// interpreted, only whether a reply arrives at all within the deadline.
//
// The deadline reuses p.writeTimeout (the same budget Print applies to its
// own write) rather than a separate hardcoded constant — a printer that is
// too slow to be worth waiting on for a print job is equally not worth
// waiting on for a health probe, and keeping a single configurable timeout
// is also what lets tests drive a fast, deterministic reconnect scenario
// (see WithWriteTimeout) instead of racing an unrelated fixed constant.
func (p *NetworkPrinter) healthCheck(conn net.Conn) error {
	if err := conn.SetDeadline(time.Now().Add(p.writeTimeout)); err != nil {
		return err
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	if _, err := conn.Write([]byte{0x10, 0x04, 0x01}); err != nil {
		return fmt.Errorf("write status request: %w", err)
	}
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err != nil {
		return fmt.Errorf("read status reply: %w", err)
	}
	return nil
}
