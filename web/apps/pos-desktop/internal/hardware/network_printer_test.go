package hardware

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeServer is a minimal ESC/POS-speaking TCP listener used to drive
// NetworkPrinter against real socket I/O (net.Listen), matching the task
// brief's "sahte TCP sunucusuyla byte-stream doğrulaması" requirement — no
// mocked net.Conn, an actual loopback socket.
type fakeServer struct {
	ln net.Listener

	mu       sync.Mutex
	received [][]byte
	conns    []net.Conn

	// respondToHealthCheck controls whether Read on an accepted connection
	// answers the printer's DLE EOT 1 probe (see NetworkPrinter.healthCheck).
	// false simulates a printer that has gone silent (powered off / cable
	// pulled) without the TCP connection itself immediately erroring —
	// exactly the case a health-check-less implementation would misreport
	// as still "connected".
	respondToHealthCheck bool

	acceptedCh chan net.Conn
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeServer{ln: ln, respondToHealthCheck: true, acceptedCh: make(chan net.Conn, 8)}
	go s.acceptLoop()
	t.Cleanup(func() { _ = s.ln.Close() })
	return s
}

func (s *fakeServer) addr() string { return s.ln.Addr().String() }

func (s *fakeServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, conn)
		respond := s.respondToHealthCheck
		s.mu.Unlock()
		s.acceptedCh <- conn
		go s.serve(conn, respond)
	}
}

func (s *fakeServer) serve(conn net.Conn, respondToHealthCheck bool) {
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			job := append([]byte(nil), buf[:n]...)
			s.mu.Lock()
			s.received = append(s.received, job)
			s.mu.Unlock()
			// Answer the ESC/POS real-time status probe (DLE EOT 1) with a
			// single dummy status byte, unless this server is simulating a
			// silent/dead printer.
			if respondToHealthCheck && n == 3 && job[0] == 0x10 && job[1] == 0x04 {
				_, _ = conn.Write([]byte{0x12})
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *fakeServer) receivedJobs() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.received))
	copy(out, s.received)
	return out
}

func (s *fakeServer) closeAllConns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_ = c.Close()
	}
}

func (s *fakeServer) waitForAccept(t *testing.T, timeout time.Duration) net.Conn {
	t.Helper()
	select {
	case c := <-s.acceptedCh:
		return c
	case <-time.After(timeout):
		t.Fatal("timed out waiting for fake server to accept a connection")
		return nil
	}
}

func waitForEvent(t *testing.T, events <-chan Event, want Status, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				t.Fatalf("Events() closed before observing status %v", want)
			}
			if evt.Status == want {
				return evt
			}
		case <-deadline:
			t.Fatalf("timed out waiting for status %v", want)
		}
	}
}

func TestNetworkPrinter_ConnectsAndReportsConnected(t *testing.T) {
	srv := newFakeServer(t)
	p := NewNetworkPrinter(srv.addr(), WithDialTimeout(2*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	defer p.Wait()
	defer cancel()

	p.Start(ctx)
	waitForEvent(t, p.Events(), StatusConnected, 2*time.Second)

	if got := p.Status(); got != StatusConnected {
		t.Fatalf("Status() = %v, want StatusConnected", got)
	}
}

func TestNetworkPrinter_ZeroValueIsDisconnected(t *testing.T) {
	p := NewNetworkPrinter("127.0.0.1:0")
	if got := p.Status(); got != StatusDisconnected {
		t.Fatalf("unstarted NetworkPrinter.Status() = %v, want StatusDisconnected (zero value)", got)
	}
}

func TestNetworkPrinter_PrintWritesExactJobBytes(t *testing.T) {
	srv := newFakeServer(t)
	p := NewNetworkPrinter(srv.addr(), WithDialTimeout(2*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	defer p.Wait()
	defer cancel()

	p.Start(ctx)
	waitForEvent(t, p.Events(), StatusConnected, 2*time.Second)

	job := []byte{0x1b, '@', 0x1b, 't', 13, 'C', 0x80, 'a', 'y', '\n'} // arbitrary ESC/POS-shaped bytes incl. a CP857 byte (0x80 = 'Ç')
	if err := p.Print(job); err != nil {
		t.Fatalf("Print() error = %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		jobs := srv.receivedJobs()
		if len(jobs) > 0 {
			if !bytesEqual(jobs[0], job) {
				t.Fatalf("server received % x, want % x", jobs[0], job)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for fake server to receive the print job")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestNetworkPrinter_PrintBeforeConnectedReturnsError guards the
// never-connected case: Print must fail with ErrPrinterNotConnected rather
// than panicking or blocking. Note this does NOT exercise Print's own
// StatusError-emitting path (see reportFault) — the device is already
// reporting StatusError from the failing dial by the time Print is called,
// and Print's fault here is silently absorbed as a (harmless) duplicate
// rather than producing a second, distinct event; see
// TestNetworkPrinter_PrintWriteFailureOnLiveConnectionReportsErrorEvent for
// the test that actually observes Print emitting its own StatusError event
// on an established connection.
func TestNetworkPrinter_PrintBeforeConnectedReturnsError(t *testing.T) {
	// Point at a TCP port nothing listens on, so the dial itself keeps
	// failing (short backoff) and the device never reaches StatusConnected.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // close immediately: addr is now a "connection refused" target

	p := NewNetworkPrinter(addr, WithDialTimeout(300*time.Millisecond), WithBackoff(50*time.Millisecond, 50*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer p.Wait()
	defer cancel()

	p.Start(ctx)
	waitForEvent(t, p.Events(), StatusError, 2*time.Second)

	err = p.Print([]byte{0x1b, '@'})
	if !errors.Is(err, ErrPrinterNotConnected) {
		t.Fatalf("Print() before any successful connect: err = %v, want ErrPrinterNotConnected", err)
	}
}

// TestNetworkPrinter_PrintWriteFailureOnLiveConnectionReportsErrorEvent
// exercises the OTHER half of Print's double-reporting contract (see
// hardware.Printer's doc comment): a write failure on an ALREADY-CONNECTED
// device — not merely "never connected yet" — must still both return an
// error from Print AND push a StatusError Event. It uses the injectable
// dial seam with a net.Pipe() whose peer is closed up front, so the very
// first Write deterministically fails (see io.ErrClosedPipe), without
// needing a real socket or any timing-dependent health-check tick.
func TestNetworkPrinter_PrintWriteFailureOnLiveConnectionReportsErrorEvent(t *testing.T) {
	local, remote := net.Pipe()
	_ = remote.Close()

	p := NewNetworkPrinter("unused:0", WithHealthInterval(time.Hour)) // long enough that only Print's own fault can trigger StatusError here
	p.dial = func(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
		return local, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer p.Wait()
	defer cancel()

	p.Start(ctx)
	waitForEvent(t, p.Events(), StatusConnected, 2*time.Second)

	err := p.Print([]byte{0x1b, '@'})
	if err == nil {
		t.Fatal("Print() on a connection whose peer already closed: want a write error, got nil")
	}

	waitForEvent(t, p.Events(), StatusError, 2*time.Second)
}

// TestNetworkPrinter_PrintWhileDisconnectedDoesNotFlapNextReconnect is a
// regression test for a real defect this package's own review caught: a
// stale fault queued by Print while conn==nil must not be misapplied to the
// NEXT, unrelated connection once the printer reconnects — that would
// misreport a freshly-succeeded reconnect as immediately broken again (a
// spurious Connected -> Error -> Connected flicker), which is exactly the
// class of "misreport connectivity" bug this whole device exists to avoid.
func TestNetworkPrinter_PrintWhileDisconnectedDoesNotFlapNextReconnect(t *testing.T) {
	srv := newFakeServer(t)

	// Make the printer's addr initially refuse connections so it starts up
	// disconnected/erroring (see the other dial-failure tests), then let it
	// succeed once we point it at srv below.
	deadLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := deadLn.Addr().String()
	_ = deadLn.Close()

	var target atomicAddr
	target.set(deadAddr)

	p := NewNetworkPrinter(
		deadAddr,
		WithDialTimeout(300*time.Millisecond),
		WithHealthInterval(200*time.Millisecond),
		WithBackoff(50*time.Millisecond, 50*time.Millisecond),
	)
	p.dial = func(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
		d := net.Dialer{Timeout: timeout}
		return d.DialContext(ctx, "tcp", target.get())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer p.Wait()
	defer cancel()

	p.Start(ctx)
	waitForEvent(t, p.Events(), StatusError, 2*time.Second) // first dial attempt fails (deadAddr)

	// Print while there is no connection at all — queues a fault via
	// reportFault (see Print's doc comment) that, before this package's
	// fix, would sit in faultCh until wrongly consumed by the NEXT
	// connection's monitor loop.
	if err := p.Print([]byte{0x1b, '@'}); err == nil {
		t.Fatal("Print() while disconnected: want an error, got nil")
	}

	// Now let the printer succeed: point dial at the real fake server.
	target.set(srv.addr())

	connectedAt := waitForEvent(t, p.Events(), StatusConnected, 2*time.Second)

	// The bug this test guards against would surface as a StatusError event
	// arriving shortly AFTER connectedAt (the stale fault being drained by
	// the fresh monitor loop). Give it a real chance to appear.
	select {
	case evt, ok := <-p.Events():
		if !ok {
			return
		}
		if evt.Status == StatusError && evt.Timestamp.After(connectedAt.Timestamp) {
			t.Fatalf("spurious StatusError immediately after reconnecting — stale pre-connect fault was misapplied to the new connection: %+v", evt)
		}
	case <-time.After(500 * time.Millisecond):
		// No further event within a generous window past reconnecting —
		// exactly what a healthy, non-flapping reconnect looks like.
	}
}

// atomicAddr is a tiny concurrency-safe string box — used only to let
// TestNetworkPrinter_PrintWhileDisconnectedDoesNotFlapNextReconnect's
// injected dial function target a different address mid-test without a
// data race (the run loop's goroutine reads it concurrently with the test
// goroutine's Set call).
type atomicAddr struct {
	mu   sync.Mutex
	addr string
}

func (a *atomicAddr) set(addr string) {
	a.mu.Lock()
	a.addr = addr
	a.mu.Unlock()
}

func (a *atomicAddr) get() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.addr
}

func TestNetworkPrinter_ConnectionDropTriggersDisconnectAndReconnect(t *testing.T) {
	srv := newFakeServer(t)
	p := NewNetworkPrinter(
		srv.addr(),
		WithDialTimeout(1*time.Second),
		WithHealthInterval(50*time.Millisecond),
		// A short write/health-check-read timeout matters here specifically
		// (see healthCheck's doc comment — it reuses this same timeout as
		// its read deadline): the test's waitForEvent budget below must
		// comfortably exceed one health-check interval PLUS one full
		// deadline-expiry wait, or the assertion races the printer's own
		// probe timing instead of testing its behavior.
		WithWriteTimeout(300*time.Millisecond),
		WithBackoff(50*time.Millisecond, 100*time.Millisecond),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer p.Wait()
	defer cancel()

	p.Start(ctx)
	waitForEvent(t, p.Events(), StatusConnected, 2*time.Second)

	// Simulate the printer going silent (server stops answering health
	// checks on new connections, and drops the current one) — this must
	// surface as an explicit StatusError, never as a NetworkPrinter that
	// keeps reporting StatusConnected (the exact b2b terazi regression this
	// task's brief calls out).
	srv.mu.Lock()
	srv.respondToHealthCheck = false
	srv.mu.Unlock()
	srv.closeAllConns()

	waitForEvent(t, p.Events(), StatusError, 3*time.Second)

	// The device must not get stuck erroring forever either — once the
	// (now silent-but-still-accepting) server takes a fresh connection, the
	// backoff+redial loop reconnects. We flip health-check answering back
	// on right as the printer reconnects, mirroring "printer came back".
	srv.mu.Lock()
	srv.respondToHealthCheck = true
	srv.mu.Unlock()

	waitForEvent(t, p.Events(), StatusConnected, 3*time.Second)
}

func TestNetworkPrinter_ContextCancelDisconnectsClosesEventsNoLeak(t *testing.T) {
	defer verifyNoLeak(t)

	srv := newFakeServer(t)
	p := NewNetworkPrinter(srv.addr(), WithDialTimeout(1*time.Second))
	ctx, cancel := context.WithCancel(context.Background())

	p.Start(ctx)
	waitForEvent(t, p.Events(), StatusConnected, 2*time.Second)

	done := make(chan struct{})
	var lastEvent Event
	go func() {
		defer close(done)
		for evt := range p.Events() {
			lastEvent = evt
		}
	}()

	cancel()
	p.Wait()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Events() channel was not closed after context cancellation")
	}

	if lastEvent.Status != StatusDisconnected {
		t.Fatalf("last event status = %v, want StatusDisconnected", lastEvent.Status)
	}
	if got := p.Status(); got != StatusDisconnected {
		t.Fatalf("Status() after cancel = %v, want StatusDisconnected", got)
	}

	// Shut the fake server down explicitly (rather than relying on its
	// t.Cleanup, which only runs after this test function — and its
	// deferred verifyNoLeak — has already returned): its acceptLoop
	// goroutine is otherwise still blocked in Accept when goleak.Find runs,
	// which would be a false-positive leak report, not a real one.
	_ = srv.ln.Close()
}

// TestNetworkPrinter_CancelDuringBackoffReturnsPromptly guards specifically
// against a bare time.Sleep-style backoff: with a long backoff window and
// no listener to connect to, ctx cancellation must still make Wait return
// quickly (well under the backoff duration), proving the backoff wait is
// itself ctx-aware (see NetworkPrinter.sleep).
func TestNetworkPrinter_CancelDuringBackoffReturnsPromptly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	p := NewNetworkPrinter(addr, WithDialTimeout(200*time.Millisecond), WithBackoff(10*time.Second, 10*time.Second))
	ctx, cancel := context.WithCancel(context.Background())

	p.Start(ctx)
	waitForEvent(t, p.Events(), StatusError, 2*time.Second) // first failed dial, now sleeping in a 10s backoff

	start := time.Now()
	cancel()
	p.Wait()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Wait() took %v after cancel during a 10s backoff — backoff sleep is not ctx-aware", elapsed)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// verifyNoLeak is defined in mock_printer_test.go (same package) and reused
// here.
