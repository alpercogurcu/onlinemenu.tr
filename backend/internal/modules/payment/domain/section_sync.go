package domain

import "context"

// DeviceSection is one tax department (kısım) as reported by a fiscal device.
// TaxPermyriad uses the same resolution as FiscalLine.TaxRatePermyriad
// (1000 = 10.00%), so a synced section can be compared to a catalog tax rate
// without a lossy conversion.
type DeviceSection struct {
	SectionNo    int
	Name         string
	TaxPermyriad int
}

// SectionSyncer is an OPTIONAL capability of a FiscalDeviceAdapter: it reads
// the device's own section table so an operator never types a sectionNo or a
// tax rate by hand (ADR-FISCAL-002 §2 — hardcoding either corrupts a legal
// receipt).
//
// It is deliberately kept out of FiscalDeviceAdapter: a wire/DLL driver may
// have no way to enumerate sections, and forcing a stub on every vendor would
// make "unsupported" indistinguishable from "device reports no sections".
// Callers type-assert and degrade gracefully (HTTP 501) when the assertion
// fails.
type SectionSyncer interface {
	FetchSections(ctx context.Context, terminalSerial string) ([]DeviceSection, error)
}
