package hardware

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMockPrinter_StartTransitionsToConnected(t *testing.T) {
	p := NewMockPrinter()
	ctx, cancel := context.WithCancel(context.Background())
	// NOTE: cancel must run before Wait — Wait blocks until the event loop
	// observes ctx.Done(), so deferring Wait after cancel (LIFO) would
	// deadlock. defer p.Wait() first, defer cancel() second, so cancel()
	// (registered last) executes first.
	defer p.Wait()
	defer cancel()

	p.Start(ctx)

	if got := p.Status(); got != StatusConnected {
		t.Fatalf("Status() = %v, want %v", got, StatusConnected)
	}

	evt := <-p.Events()
	if evt.Status != StatusConnected || evt.Err != nil {
		t.Fatalf("unexpected initial event: %+v", evt)
	}
}

func TestMockPrinter_FaultEmitsExplicitErrorEvent(t *testing.T) {
	p := NewMockPrinter()
	ctx, cancel := context.WithCancel(context.Background())
	// See NOTE in TestMockPrinter_StartTransitionsToConnected re: defer order.
	defer p.Wait()
	defer cancel()

	p.Start(ctx)

	<-p.Events() // initial connected event

	faultErr := errors.New("out of paper")
	p.SimulateFault(faultErr)

	select {
	case evt := <-p.Events():
		if evt.Status != StatusError {
			t.Fatalf("Status = %v, want StatusError", evt.Status)
		}
		if !errors.Is(evt.Err, faultErr) {
			t.Fatalf("Err = %v, want %v", evt.Err, faultErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fault event — a fault must never be swallowed")
	}

	if got := p.Status(); got != StatusError {
		t.Fatalf("Status() after fault = %v, want StatusError (device must not keep reporting stale connected status)", got)
	}
}

func TestMockPrinter_ContextCancelDisconnectsAndClosesEventsWithoutLeak(t *testing.T) {
	defer verifyNoLeak(t)

	p := NewMockPrinter()
	ctx, cancel := context.WithCancel(context.Background())

	p.Start(ctx)
	<-p.Events() // initial connected event

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
}

// verifyNoLeak asserts (with retry, matching the backend's goleak usage
// convention) that the device's event-loop goroutine has fully exited.
func verifyNoLeak(t *testing.T) {
	t.Helper()
	if err := goleak.Find(); err != nil {
		fmt.Fprintf(os.Stderr, "goleak: %v\n", err)
		t.Fatalf("goroutine leak detected: %v", err)
	}
}
