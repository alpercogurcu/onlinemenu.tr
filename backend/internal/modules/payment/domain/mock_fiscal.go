package domain

import (
	"context"
	"fmt"
	"time"
)

// MockFiscalAdapter is a no-op FiscalDeviceAdapter used in development and tests.
// ADR-FISCAL-001: fiscal_device_type='none' is forbidden in production — this adapter
// is wired only when FISCAL_DEVICE_TYPE=mock (the default in dev/CI).
// It completes synchronously: SubmitSale returns a non-nil FiscalResult so the
// submission worker finalizes the payment without any async round-trip.
type MockFiscalAdapter struct{}

func (MockFiscalAdapter) SubmitSale(_ context.Context, sale FiscalSale) (*FiscalResult, error) {
	return &FiscalResult{
		SubmissionID: sale.SubmissionID,
		TenantID:     sale.TenantID,
		BranchID:     sale.BranchID,
		PaymentID:    sale.PaymentID,
		Status:       FiscalSubmissionCompleted,
		DeviceType:   "mock",
		ReceiptNo:    fmt.Sprintf("MOCK-%s", sale.PaymentID),
		CompletedAt:  time.Now().UTC(),
	}, nil
}

func (MockFiscalAdapter) VoidSale(_ context.Context, ref FiscalSubmissionRef) (*FiscalResult, error) {
	return &FiscalResult{
		SubmissionID: ref.SubmissionID,
		TenantID:     ref.TenantID,
		BranchID:     ref.BranchID,
		Status:       FiscalSubmissionVoided,
		DeviceType:   "mock",
		CompletedAt:  time.Now().UTC(),
	}, nil
}

func (MockFiscalAdapter) Capabilities() FiscalCapabilities {
	return FiscalCapabilities{VoidSale: true}
}

var _ SectionSyncer = MockFiscalAdapter{}

// FetchSections returns the three VAT sections a Turkish ÖKC ships with by
// default (%1, %10, %20), so the admin section-mapping flow is exercisable in
// dev and CI without a paired device.
func (MockFiscalAdapter) FetchSections(_ context.Context, _ string) ([]DeviceSection, error) {
	return []DeviceSection{
		{SectionNo: 1, Name: "KDV %1", TaxPermyriad: 100},
		{SectionNo: 2, Name: "KDV %10", TaxPermyriad: 1000},
		{SectionNo: 3, Name: "KDV %20", TaxPermyriad: 2000},
	}, nil
}
