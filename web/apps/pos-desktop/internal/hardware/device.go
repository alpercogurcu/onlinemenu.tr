// Package hardware provides the device abstraction POS peripherals (printer,
// scale, fiscal device, card reader) implement.
//
// lessons-from-b2b Bölüm 5 kaydı: b2b'de terazi bağlantısı koptuğunda UI
// sonsuza dek "bağlı" göstermeye devam ediyordu, çünkü poll döngüsü hatayı
// yutup son bilinen durumu tekrarlıyordu. The pattern enforced here is the
// opposite: every state transition — including the transition into an
// error or disconnected state — is an explicit Event pushed onto the
// device's event channel. There is no code path that observes a failure
// and simply keeps reporting the previous status.
package hardware

import (
	"context"
	"time"
)

// Status is the connectivity state of a hardware device. There is
// deliberately no "unknown" zero-value state that could be mistaken for
// "connected" — StatusDisconnected is the zero value, so an unstarted or
// misconfigured device reads as disconnected, not connected.
type Status int

const (
	StatusDisconnected Status = iota
	StatusConnected
	StatusError
)

func (s Status) String() string {
	switch s {
	case StatusConnected:
		return "connected"
	case StatusError:
		return "error"
	default:
		return "disconnected"
	}
}

// Event is a single, explicit status transition emitted by a Device. Err is
// non-nil only when Status is StatusError, and always describes why the
// device left the connected state — it is never discarded by the emitting
// goroutine.
type Event struct {
	Status    Status
	Err       error
	Timestamp time.Time
}

// Device is the abstraction every POS peripheral implements. Kind
// identifies the device family (e.g. "printer", "scale", "fiscal") for
// routing frontend notifications; a station may hold several Devices of
// the same Kind (multiple printers).
//
// Events returns a channel that receives every status transition for the
// lifetime of the Device, starting from the moment Start is called until
// the context passed to Start is cancelled. Implementations must close the
// channel once their event-emitting goroutine has fully exited, so
// consumers can range over it safely.
type Device interface {
	Kind() string
	Status() Status
	Events() <-chan Event
}

// Printer is the Device every receipt-printing adapter implements —
// MockPrinter (dev, no hardware required) and NetworkPrinter (ESC/POS over
// TCP 9100) both satisfy it, so app.go can hold a single Printer field and
// swap the concrete implementation based on config.Config.PrinterAddr
// without any other code path knowing which one it's talking to.
//
// Print must never silently succeed on failure (lessons-from-b2b §5: "b2b'de
// başarısız fatura baskısı //nolint:errcheck yüzünden başarılı görünüyordu").
// A failing implementation must both return a non-nil error AND — if the
// failure indicates the device itself is no longer reachable, as opposed to
// e.g. a caller passing a nil job — push a StatusError Event onto Events(),
// the same double-reporting NetworkPrinter uses. Wait blocks until the
// Device's background goroutine (started by Start) has fully exited, for
// graceful shutdown and goroutine-leak tests.
type Printer interface {
	Device
	Start(ctx context.Context)
	Print(job []byte) error
	Wait()
}
