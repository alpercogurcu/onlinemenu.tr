// Package tokenx implements the Token X Connect Cloud fiscal device adapter
// (Beko X30TR). See ADR-FISCAL-002.
//
// The adapter is asynchronous: SubmitSale/VoidSale hand the basket to Token's
// cloud and return (nil, nil). The registration result arrives later as a
// BASKET_COMPLETED webhook, which the transport layer normalizes through
// ParseWebhook and feeds into domain.FiscalResultSink.
package tokenx

import (
	"encoding/json"

	"onlinemenu.tr/internal/modules/payment/domain"
)

// DeviceType is the value stored in branch_settings.fiscal_device_type and
// echoed on every FiscalResult this adapter produces.
const DeviceType = "beko_x30tr_cloud"

// Vendor is the fiscal_terminals.vendor value for every Token X device,
// independent of the transport (Cloud today, Wire later). DeviceType names the
// driver; Vendor names the manufacturer's platform.
const Vendor = "tokenx"

// The fiscal ports and shared value types live in payment/domain; the adapter
// aliases them so vendor code reads naturally while the repo layer implements
// the ports without importing this package (see domain/fiscal_ports.go).
type (
	BasketMode       = domain.BasketMode
	TerminalRef      = domain.TerminalRef
	SectionResolver  = domain.SectionResolver
	TerminalResolver = domain.TerminalResolver
)

const (
	BasketModeInstant = domain.BasketModeInstant
	BasketModeList    = domain.BasketModeList
)

// Document types accepted by Token. Only the plain fiscal receipt is used in
// phase 1; invoice-linked types (9005/9006/9007) are a billing-module concern.
const documentTypeReceipt = 0

// Unit code sent when a line carries no UN/ECE unit. C62 is "piece".
const defaultUnitCode = "C62"

// Basket is the Token sale basket payload, shared by the Cloud and Wire
// transports (ADR-FISCAL-002: only transport differs, the JSON model is one).
type Basket struct {
	BasketID      string        `json:"basketID"`
	CreateInvoice bool          `json:"createInvoice"`
	DocumentType  int           `json:"documentType"`
	IsVoid        bool          `json:"isVoid"`
	Title         string        `json:"title,omitempty"`
	Filter        string        `json:"filter,omitempty"`
	CheckNumber   int           `json:"checkNumber,omitempty"`
	Note          string        `json:"note,omitempty"`
	Items         []BasketItem  `json:"items"`
	PaymentItems  []PaymentItem `json:"paymentItems,omitempty"`
	CustomerInfo  *CustomerInfo `json:"customerInfo,omitempty"`
	Adjust        *Adjust       `json:"adjust,omitempty"`
}

// BasketItem is one sale line. Price is in kuruş, Quantity in thousandths
// (1000 = 1 unit) and TaxPercent in permyriad (1000 = 10.00%) — the same
// resolutions the domain model uses, so no rounding happens here.
type BasketItem struct {
	Name       string `json:"name"`
	Price      int64  `json:"price"`
	SectionNo  int    `json:"sectionNo"`
	TaxPercent int    `json:"taxPercent"`
	Quantity   int64  `json:"quantity"`
	Unit       string `json:"unit"`
	Barcode    string `json:"barcode,omitempty"`
	PluNo      string `json:"pluNo,omitempty"`
}

// PaymentItem is one entry of the payment plan. Amount is in kuruş and Type is
// a Token payment-type code (see paymentTypeOf).
type PaymentItem struct {
	Amount      int64  `json:"amount"`
	Type        int    `json:"type"`
	Description string `json:"description,omitempty"`
	OperatorID  int    `json:"operatorId,omitempty"`
}

// Adjust encodes a basket-level discount or surcharge.
type Adjust struct {
	Description string `json:"description,omitempty"`
	// DiscountOrSurcharge: 0 = discount, 1 = surcharge.
	DiscountOrSurcharge int `json:"discountOrSurcharge"`
	// Type: 0 = absolute amount (kuruş), 1 = percentage.
	Type int `json:"type"`
	// Value is kuruş when Type is 0 and permyriad (1000 = 10.00%) when Type is
	// 1, matching the domain encoding of FiscalAdjust.Value.
	Value int64 `json:"value"`
}

// CustomerInfo carries buyer identity for invoice-linked document flows.
type CustomerInfo struct {
	Name      string `json:"name,omitempty"`
	TaxID     string `json:"taxID,omitempty"`
	Email     string `json:"email,omitempty"`
	Telephone string `json:"telephone,omitempty"`
	TaxScheme string `json:"taxScheme,omitempty"`
	Street    string `json:"street,omitempty"`
}

// Section is one device department (kısım) returned by Get Fiscal Parameters.
// TaxPercent is permyriad, matching BasketItem.TaxPercent.
type Section struct {
	SectionNo  int    `json:"sectionNo"`
	Name       string `json:"name"`
	TaxPercent int    `json:"taxPercent"`
	Type       int    `json:"type"`
	Limit      int64  `json:"limit"`
	Price      int64  `json:"price"`
}

// FiscalInfo is the response of GET /v1/fiscal-info. Terminal is kept raw: its
// schema is not documented and inventing fields would silently drop data.
type FiscalInfo struct {
	Sections []Section       `json:"sections"`
	Terminal json.RawMessage `json:"terminal"`
}

// OpenBasket is one not-yet-completed basket on a terminal, used by the
// reconciliation job when a webhook is lost (ADR-FISCAL-002 §1).
type OpenBasket struct {
	BasketID    string `json:"basketID"`
	Title       string `json:"title"`
	CheckNumber int    `json:"checkNumber"`
}
