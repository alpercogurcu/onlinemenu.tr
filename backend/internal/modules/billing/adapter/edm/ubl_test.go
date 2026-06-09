package edm

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/billing/domain"
)

func sampleInvoice() domain.Invoice {
	prod := uuid.MustParse("dddddddd-0000-0000-0000-000000000001")
	return domain.Invoice{
		ID:             uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001"),
		TenantID:       uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001"),
		InvoiceType:    domain.InvoiceTypeEArsiv,
		InvoiceNumber:  "ONM2026000000001",
		GibUUID:        uuid.MustParse("cccccccc-0000-0000-0000-000000000001"),
		SupplierVKN:    "1234567890",
		SupplierName:   "TEST TEDARİKÇİ A.Ş.",
		SupplierAlias:  "urn:mail:test@edm.com.tr",
		CustomerVKN:    "9876543210",
		CustomerName:   "ALICI FİRMA LTD.",
		IssueDate:      time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC),
		Currency:       "TRY",
		AmountExcludingTax: 30000,
		TaxAmount:          2400,
		AmountTotal:        32400,
		Items: []domain.InvoiceItem{
			{
				ProductID:       &prod,
				ProductName:     "Adana Kebap",
				Quantity:        2,
				UnitPriceAmount: 15000,
				TaxRateBPS:      800,
				LineTotal:       30000,
				TaxAmount:       2400,
			},
		},
	}
}

func TestBuildInvoiceXML_Structure(t *testing.T) {
	inv := sampleInvoice()
	xmlBytes, err := BuildInvoiceXML(inv)
	require.NoError(t, err)

	xml := string(xmlBytes)

	// Root element
	assert.Contains(t, xml, `<Invoice`)
	assert.Contains(t, xml, `xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"`)

	// Profile
	assert.Contains(t, xml, `<cbc:ProfileID>EARSIVFATURA</cbc:ProfileID>`)

	// Invoice number and UUID
	assert.Contains(t, xml, `<cbc:ID>ONM2026000000001</cbc:ID>`)
	assert.Contains(t, xml, `<cbc:UUID>cccccccc-0000-0000-0000-000000000001</cbc:UUID>`)

	// Issue date
	assert.Contains(t, xml, `<cbc:IssueDate>2026-06-09</cbc:IssueDate>`)

	// Currency
	assert.Contains(t, xml, `<cbc:DocumentCurrencyCode>TRY</cbc:DocumentCurrencyCode>`)

	// Supplier VKN
	assert.Contains(t, xml, `<cbc:ID schemeID="VKN">1234567890</cbc:ID>`)

	// Customer VKN
	assert.Contains(t, xml, `<cbc:ID schemeID="VKN">9876543210</cbc:ID>`)
}

func TestBuildInvoiceXML_EFaturaProfileID(t *testing.T) {
	inv := sampleInvoice()
	inv.InvoiceType = domain.InvoiceTypeEFatura

	xmlBytes, err := BuildInvoiceXML(inv)
	require.NoError(t, err)
	assert.Contains(t, string(xmlBytes), `<cbc:ProfileID>TICARIFATURA</cbc:ProfileID>`)
}

func TestBuildInvoiceXML_Amounts(t *testing.T) {
	inv := sampleInvoice()
	xmlBytes, err := BuildInvoiceXML(inv)
	require.NoError(t, err)
	xml := string(xmlBytes)

	// 30000 kuruş = 300.00 TRY
	assert.Contains(t, xml, `300.00`)
	// 2400 kuruş = 24.00 TRY
	assert.Contains(t, xml, `24.00`)
	// 32400 kuruş = 324.00 TRY
	assert.Contains(t, xml, `324.00`)
	// Unit price: 15000 kuruş = 150.00 TRY
	assert.Contains(t, xml, `150.00`)
}

func TestBuildInvoiceXML_LineItems(t *testing.T) {
	inv := sampleInvoice()
	xmlBytes, err := BuildInvoiceXML(inv)
	require.NoError(t, err)
	xml := string(xmlBytes)

	assert.Contains(t, xml, `<cac:InvoiceLine>`)
	assert.Contains(t, xml, `<cbc:Name>Adana Kebap</cbc:Name>`)
	assert.Contains(t, xml, `<cbc:InvoicedQuantity unitCode="C62">2</cbc:InvoicedQuantity>`)
}

func TestFormatKurus(t *testing.T) {
	cases := []struct {
		input    int64
		expected string
	}{
		{0, "0.00"},
		{100, "1.00"},
		{150, "1.50"},
		{1234, "12.34"},
		{10000, "100.00"},
		{99, "0.99"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.expected, formatKurus(tc.input), "input: %d", tc.input)
	}
}

func TestNextInvoiceNumber(t *testing.T) {
	result := nextInvoiceNumber("ONM", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 1)
	assert.Equal(t, "ONM2026000000001", result)

	result2 := nextInvoiceNumber("ONM", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 100)
	assert.Equal(t, "ONM2026000000100", result2)
}

func TestBuildInvoiceXML_XMLEscape(t *testing.T) {
	inv := sampleInvoice()
	inv.SupplierName = `TEST & "COMPANY" <LTD>`

	xmlBytes, err := BuildInvoiceXML(inv)
	require.NoError(t, err)
	xml := string(xmlBytes)

	// Special characters must be escaped.
	assert.False(t, strings.Contains(xml, `TEST & "COMPANY"`),
		"unescaped ampersand/quotes should not appear in XML")
}
