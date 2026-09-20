package main

import (
	"context"
	"fmt"
	"strings"

	"onlinemenu.tr/pos-desktop/internal/apiclient"
)

// PaymentLineDTO is one line of the basket a payment covers, as the payment
// screen knows it: which product, what it is called on the receipt, and the
// quantity/price this payment accounts for. Tax rate, category and unit are
// added here from the catalog — an order item carries none of them.
type PaymentLineDTO struct {
	ProductID      string `json:"product_id"`
	Name           string `json:"name"`
	UnitPriceMinor int64  `json:"unit_price_minor"`
	// QuantityMilli is in thousandths: 1000 = one unit.
	QuantityMilli int64 `json:"quantity_milli"`
}

// RegisterPaymentInputDTO is one payment the cashier takes. Method is "cash" or
// "card". AmountTotal is this payment only; Lines, when present, must add up to
// it (apiclient.RegisterPayment enforces that).
type RegisterPaymentInputDTO struct {
	BranchID    string           `json:"branch_id"`
	CheckID     string           `json:"check_id"`
	Method      string           `json:"method"`
	AmountTotal int64            `json:"amount_total"`
	Lines       []PaymentLineDTO `json:"lines"`
	TableLabel  string           `json:"table_label"`
}

type productMetaSource interface {
	productMeta(ctx context.Context, productID string) (apiclient.Product, error)
}

// buildFiscalLines completes the payment screen's lines into fiscal lines. It
// refuses to guess: a line whose product the catalog cannot describe fails the
// whole payment BEFORE it is registered, because a made-up tax rate or section
// is rejected by a real device only after the money is already recorded.
//
// KNOWN GAP: a product with no category has no device section. It is sent
// without a category_id rather than failing, because the mock adapter accepts
// it and blocking the sale would hurt installations that have no real device;
// a real-device rollout must first give every sellable product a category.
func buildFiscalLines(ctx context.Context, src productMetaSource, lines []PaymentLineDTO) ([]apiclient.FiscalLine, error) {
	if len(lines) == 0 {
		return nil, nil
	}
	out := make([]apiclient.FiscalLine, len(lines))
	for i, l := range lines {
		meta, err := src.productMeta(ctx, l.ProductID)
		if err != nil {
			return nil, fmt.Errorf("%q için ürün bilgisi alınamadı, ödeme kaydedilmedi: %w", l.Name, err)
		}
		out[i] = apiclient.FiscalLine{
			Name:             l.Name,
			UnitPriceMinor:   l.UnitPriceMinor,
			QuantityMilli:    l.QuantityMilli,
			TaxRatePermyriad: meta.TaxRateBPS,
			CategoryID:       meta.CategoryID,
			Unit:             fiscalUnitCode(meta.Unit),
		}
	}
	return out, nil
}

// fiscalUnitCode maps a catalog unit ("adet", "kg", "lt", "porsiyon") to the
// UN/ECE code a fiscal device expects. Unknown units fall back to C62 (piece),
// the code every device accepts; an empty unit stays empty so the backend
// applies its own default.
func fiscalUnitCode(catalogUnit string) string {
	switch strings.ToLower(strings.TrimSpace(catalogUnit)) {
	case "":
		return ""
	case "kg":
		return "KGM"
	case "lt", "l":
		return "LTR"
	default:
		return "C62"
	}
}

// paymentMethodFor maps the payment screen's method to the backend's payment
// method. A card payment is "terminal" (EFT-POS / ÖKC card); in Faz 1 it only
// records a charge taken on an external device.
func paymentMethodFor(method string) (string, error) {
	switch method {
	case "cash":
		return "cash", nil
	case "card":
		return "terminal", nil
	default:
		return "", fmt.Errorf("bilinmeyen ödeme yöntemi %q", method)
	}
}

// RegisterPayment registers one payment (cash or card) against a check, with the
// fiscal basket it covers. The lines are what make a real ÖKC accept the
// receipt: without them the backend sends a single synthetic "Satis" line
// (payment_service.go buildFiscalSale), which such a device rejects.
func (a *App) RegisterPayment(in RegisterPaymentInputDTO) (PaymentDTO, error) {
	if in.BranchID == "" {
		return PaymentDTO{}, fmt.Errorf("şube bilgisi eksik — oturum yeniden açılmalı")
	}
	method, err := paymentMethodFor(in.Method)
	if err != nil {
		return PaymentDTO{}, err
	}
	lines, err := buildFiscalLines(a.ctx, a.optionsResolver(), in.Lines)
	if err != nil {
		return PaymentDTO{}, err
	}
	p, err := a.api.RegisterPayment(a.ctx, apiclient.RegisterPaymentInput{
		BranchID:    in.BranchID,
		CheckID:     in.CheckID,
		Method:      method,
		AmountTotal: in.AmountTotal,
		Lines:       lines,
		TableLabel:  in.TableLabel,
	})
	if err != nil {
		return PaymentDTO{}, err
	}
	dto := PaymentDTO{
		ID:          p.ID,
		Method:      p.Method,
		Status:      p.Status,
		AmountTotal: p.AmountTotal,
		Currency:    p.Currency,
	}
	if p.CheckID != nil {
		dto.CheckID = *p.CheckID
	}
	return dto, nil
}
