package domain

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MockFiscalAdapter is a no-op FiscalDeviceAdapter used in development and tests.
// ADR-FISCAL-001: fiscal_device_type='none' is forbidden in production — this adapter
// is wired only when FISCAL_DEVICE_TYPE=mock (the default in dev/CI).
type MockFiscalAdapter struct{}

func (MockFiscalAdapter) RegisterSale(_ context.Context, sale FiscalSale) (FiscalReceipt, error) {
	return FiscalReceipt{
		ID:            uuid.New(),
		TenantID:      sale.TenantID,
		PaymentID:     sale.PaymentID,
		DeviceType:    "mock",
		ReceiptNumber: fmt.Sprintf("MOCK-%s", sale.PaymentID),
		ReceiptData:   map[string]any{"amount": sale.AmountTotal, "currency": sale.Currency},
		IssuedAt:      time.Now().UTC(),
	}, nil
}
