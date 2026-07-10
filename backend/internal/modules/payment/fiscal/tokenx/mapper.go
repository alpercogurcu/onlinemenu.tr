package tokenx

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// SectionResolver maps a catalog category to a device section (kısım).
// Implementations read the tenant's synchronized fiscal_section_mappings; the
// adapter never guesses a sectionNo, because a wrong section means a wrong tax
// rate on a legal receipt (ADR-FISCAL-002 §2).
type SectionResolver interface {
	Resolve(ctx context.Context, tenantID, branchID, categoryID uuid.UUID) (sectionNo int, taxPermyriad int, err error)
}

// TerminalRef identifies where a basket must be delivered. VendorBranchRef is
// Token's branch id used as the branch-id header in list mode; it varies per
// basket and therefore comes from the resolver, never from static config.
type TerminalRef struct {
	Serial          string
	VendorBranchRef string
	Mode            BasketMode
}

// TerminalResolver picks the terminal that serves a branch.
type TerminalResolver interface {
	Resolve(ctx context.Context, tenantID, branchID uuid.UUID) (TerminalRef, error)
}

// currencyTRY is the only currency a Turkish fiscal device registers.
const currencyTRY = "TRY"

// Token payment-type codes. Types 4, 5 and 6 are undocumented (open question
// to Token) and are deliberately absent.
const (
	paymentTypeCash        = 1
	paymentTypeTerminal    = 3
	paymentTypeMealCard    = 7
	paymentTypeNoCharge    = 8
	paymentTypeComp        = 9
	paymentTypeOpenAccount = 17
)

var paymentTypeByMethod = map[domain.PaymentMethod]int{
	domain.PaymentMethodCash:        paymentTypeCash,
	domain.PaymentMethodTerminal:    paymentTypeTerminal,
	domain.PaymentMethodMealCard:    paymentTypeMealCard,
	domain.PaymentMethodNoCharge:    paymentTypeNoCharge,
	domain.PaymentMethodComp:        paymentTypeComp,
	domain.PaymentMethodOpenAccount: paymentTypeOpenAccount,
}

var methodByPaymentType = func() map[int]domain.PaymentMethod {
	m := make(map[int]domain.PaymentMethod, len(paymentTypeByMethod))
	for method, code := range paymentTypeByMethod {
		m[code] = method
	}
	return m
}()

// paymentTypeOf maps a domain payment method to a Token code.
func paymentTypeOf(m domain.PaymentMethod) (int, error) {
	code, ok := paymentTypeByMethod[m]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownPaymentMethod, m)
	}
	return code, nil
}

// methodOf reverses paymentTypeOf. An unmapped vendor code yields an empty
// method: the raw code is still preserved on FiscalConfirmedPayment.VendorType
// so nothing is lost for audit, and the caller decides how to treat it.
func methodOf(code int) domain.PaymentMethod {
	return methodByPaymentType[code]
}

// buildBasket converts a vendor-neutral sale into a Token basket. isVoid marks
// the basket as a cancellation (iptal fişi) of an earlier registration.
func buildBasket(ctx context.Context, sale domain.FiscalSale, sections SectionResolver, isVoid bool) (Basket, error) {
	if len(sale.Lines) == 0 {
		return Basket{}, ErrNoLines
	}
	// The basket JSON has no currency field: every amount is assumed to be TRY.
	// Refuse a foreign-currency sale instead of registering it at face value.
	if sale.Currency != "" && !strings.EqualFold(sale.Currency, currencyTRY) {
		return Basket{}, fmt.Errorf("%w: got %q", ErrUnsupportedCurrency, sale.Currency)
	}

	items := make([]BasketItem, 0, len(sale.Lines))
	for i, line := range sale.Lines {
		sectionNo, sectionTax, err := sections.Resolve(ctx, sale.TenantID, sale.BranchID, line.CategoryID)
		if err != nil {
			return Basket{}, fmt.Errorf("resolve section for line %d (category %s): %w", i, line.CategoryID, err)
		}
		// The catalog decides the price, the device decides the tax. They must
		// agree, otherwise the receipt would contradict the fiscal memory.
		if sectionTax != line.TaxRatePermyriad {
			return Basket{}, fmt.Errorf("%w: line %d (%q) has %d, section %d has %d",
				ErrTaxMismatch, i, line.Name, line.TaxRatePermyriad, sectionNo, sectionTax)
		}

		unit := line.Unit
		if unit == "" {
			unit = defaultUnitCode
		}
		items = append(items, BasketItem{
			Name:       line.Name,
			Price:      line.UnitPriceMinor,
			SectionNo:  sectionNo,
			TaxPercent: sectionTax,
			Quantity:   line.QuantityMilli,
			Unit:       unit,
		})
	}

	paymentItems := make([]PaymentItem, 0, len(sale.Payments))
	for i, p := range sale.Payments {
		code, err := paymentTypeOf(p.Method)
		if err != nil {
			return Basket{}, fmt.Errorf("payment %d: %w", i, err)
		}
		paymentItems = append(paymentItems, PaymentItem{Amount: p.AmountMinor, Type: code})
	}

	return Basket{
		BasketID: sale.SubmissionID.String(),
		// createInvoice stays false: invoice-linked document flows (receiptLimit
		// overflow, e-fatura) are owned by the billing module, not this adapter.
		CreateInvoice: false,
		DocumentType:  documentTypeReceipt,
		IsVoid:        isVoid,
		Title:         sale.Meta.TableLabel,
		Filter:        sale.Meta.WaiterName,
		CheckNumber:   sale.Meta.CheckNumber,
		Items:         items,
		PaymentItems:  paymentItems,
		CustomerInfo:  buildCustomerInfo(sale.Customer),
		Adjust:        buildAdjust(sale.Discount),
	}, nil
}

// buildVoidBasket is the cancellation payload for a previously completed sale.
func buildVoidBasket(ref domain.FiscalSubmissionRef) Basket {
	return Basket{
		BasketID:     ref.SubmissionID.String(),
		DocumentType: documentTypeReceipt,
		IsVoid:       true,
		Items:        []BasketItem{},
	}
}

func buildCustomerInfo(c *domain.FiscalCustomer) *CustomerInfo {
	if c == nil {
		return nil
	}
	return &CustomerInfo{
		Name:      c.Name,
		TaxID:     c.TaxID,
		Email:     c.Email,
		Telephone: c.Telephone,
		TaxScheme: c.TaxOffice,
		Street:    c.Address,
	}
}

func buildAdjust(a *domain.FiscalAdjust) *Adjust {
	if a == nil {
		return nil
	}
	kind := 0
	if a.Kind == domain.FiscalAdjustSurcharge {
		kind = 1
	}
	mode := 0
	if a.Mode == domain.FiscalAdjustPercent {
		mode = 1
	}
	return &Adjust{
		Description:         a.Description,
		DiscountOrSurcharge: kind,
		Type:                mode,
		Value:               a.Value,
	}
}
