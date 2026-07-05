package hardware

import (
	"context"
	"sync"
	"time"
)

// MockPrinter is a Device implementation that simulates a receipt printer
// without requiring physical hardware. It exists so the event-driven
// device pattern (connect → fault → explicit error event) can be developed
// and demoed before real printer/scale/fiscal adapters land in the UI wave.
type MockPrinter struct {
	mu     sync.Mutex
	status Status

	events  chan Event
	faultCh chan error
	wg      sync.WaitGroup
}

// NewMockPrinter constructs a MockPrinter in the disconnected state. Call
// Start to begin its lifecycle.
func NewMockPrinter() *MockPrinter {
	return &MockPrinter{
		events:  make(chan Event, 8),
		faultCh: make(chan error, 1),
	}
}

func (p *MockPrinter) Kind() string { return "printer" }

func (p *MockPrinter) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *MockPrinter) Events() <-chan Event { return p.events }

// Start connects the mock printer and runs its event loop in a tracked
// background goroutine until ctx is cancelled. On cancellation the
// goroutine transitions the device to StatusDisconnected, closes the
// Events channel, and exits — callers can rely on Wait returning promptly
// after ctx is cancelled, with no leaked goroutine left behind.
func (p *MockPrinter) Start(ctx context.Context) {
	p.setStatus(StatusConnected, nil)

	p.wg.Add(1)
	go p.run(ctx)
}

// Wait blocks until the event loop goroutine has fully exited. Intended
// for graceful shutdown and for tests asserting no goroutine leak.
func (p *MockPrinter) Wait() {
	p.wg.Wait()
}

func (p *MockPrinter) run(ctx context.Context) {
	defer p.wg.Done()
	defer close(p.events)

	for {
		select {
		case <-ctx.Done():
			p.setStatus(StatusDisconnected, nil)
			return
		case err := <-p.faultCh:
			// A fault must never leave the device silently reporting its
			// last-known-good status (the b2b terazi regression). The
			// transition to StatusError is always paired with the error
			// that caused it, and is pushed onto Events() immediately.
			p.setStatus(StatusError, err)
		}
	}
}

// SimulateFault injects a hardware fault (e.g. "out of paper", "USB
// disconnected") for local development and tests. Real Device
// implementations trigger the same StatusError transition from their
// actual I/O error paths — this method only exists on the mock.
func (p *MockPrinter) SimulateFault(err error) {
	select {
	case p.faultCh <- err:
	default:
		// A fault is already queued and not yet processed; dropping a
		// duplicate is acceptable since the loop always re-evaluates
		// state. Silence here is safe: it does not hide an error from
		// the consumer, it only avoids double-queuing one.
	}
}

func (p *MockPrinter) setStatus(s Status, err error) {
	p.mu.Lock()
	p.status = s
	p.mu.Unlock()

	p.events <- Event{Status: s, Err: err, Timestamp: time.Now()}
}
