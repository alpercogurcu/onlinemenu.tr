package edm

import (
	"fmt"
	"strings"
	"time"

	"onlinemenu.tr/internal/modules/billing/domain"
)

// BuildInvoiceXML creates a UBL 2.1 Invoice XML conforming to GİB TR profile.
// All monetary amounts are passed in kuruş (int64); this function converts to
// decimal strings for XML output (100 kuruş → "1.00").
func BuildInvoiceXML(inv domain.Invoice) ([]byte, error) {
	if inv.Currency == "" {
		inv.Currency = "TRY"
	}

	lines := buildLines(inv.Items)
	taxSections := buildTaxSections(inv.Items)
	totals := buildTotals(inv)

	profileID := "EARSIVFATURA"
	if inv.InvoiceType == domain.InvoiceTypeEFatura {
		profileID = "TICARIFATURA"
	}

	issueDate := inv.IssueDate.Format("2006-01-02")
	issueTime := inv.IssueDate.Format("15:04:05")

	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Invoice xmlns="urn:oasis:names:specification:ubl:schema:xsd:Invoice-2"
         xmlns:cac="urn:oasis:names:specification:ubl:schema:xsd:CommonAggregateComponents-2"
         xmlns:cbc="urn:oasis:names:specification:ubl:schema:xsd:CommonBasicComponents-2"
         xmlns:ext="urn:oasis:names:specification:ubl:schema:xsd:CommonExtensionComponents-2">
  <cbc:UBLVersionID>2.1</cbc:UBLVersionID>
  <cbc:CustomizationID>TR1.2</cbc:CustomizationID>
  <cbc:ProfileID>%s</cbc:ProfileID>
  <cbc:ID>%s</cbc:ID>
  <cbc:CopyIndicator>false</cbc:CopyIndicator>
  <cbc:UUID>%s</cbc:UUID>
  <cbc:IssueDate>%s</cbc:IssueDate>
  <cbc:IssueTime>%s</cbc:IssueTime>
  <cbc:InvoiceTypeCode>SATIS</cbc:InvoiceTypeCode>
  <cbc:DocumentCurrencyCode>%s</cbc:DocumentCurrencyCode>
  <cbc:LineCountNumeric>%d</cbc:LineCountNumeric>
%s
%s
%s
%s
</Invoice>`,
		escapeXML(profileID),
		escapeXML(inv.InvoiceNumber),
		escapeXML(inv.GibUUID.String()),
		issueDate,
		issueTime,
		escapeXML(inv.Currency),
		len(inv.Items),
		buildSupplierParty(inv),
		buildCustomerParty(inv),
		taxSections+totals,
		lines,
	)

	return []byte(xml), nil
}

func buildSupplierParty(inv domain.Invoice) string {
	return fmt.Sprintf(`  <cac:AccountingSupplierParty>
    <cac:Party>
      <cbc:WebsiteURI/>
      <cac:PartyIdentification><cbc:ID schemeID="VKN">%s</cbc:ID></cac:PartyIdentification>
      <cac:PartyName><cbc:Name>%s</cbc:Name></cac:PartyName>
      <cac:PostalAddress>
        <cbc:StreetName/>
        <cbc:CityName/>
        <cac:Country><cbc:Name>Türkiye</cbc:Name></cac:Country>
      </cac:PostalAddress>
      <cac:PartyTaxScheme>
        <cbc:TaxLevelCode>N</cbc:TaxLevelCode>
        <cac:TaxScheme><cbc:Name/></cac:TaxScheme>
      </cac:PartyTaxScheme>
      <cac:Contact><cbc:ElectronicMail/></cac:Contact>
    </cac:Party>
  </cac:AccountingSupplierParty>`,
		escapeXML(inv.SupplierVKN),
		escapeXML(inv.SupplierName),
	)
}

func buildCustomerParty(inv domain.Invoice) string {
	schemeID := "VKN"
	if len(inv.CustomerVKN) == 11 {
		schemeID = "TCKN"
	}
	return fmt.Sprintf(`  <cac:AccountingCustomerParty>
    <cac:Party>
      <cac:PartyIdentification><cbc:ID schemeID="%s">%s</cbc:ID></cac:PartyIdentification>
      <cac:PartyName><cbc:Name>%s</cbc:Name></cac:PartyName>
      <cac:PostalAddress>
        <cbc:StreetName/>
        <cbc:CityName/>
        <cac:Country><cbc:Name>Türkiye</cbc:Name></cac:Country>
      </cac:PostalAddress>
      <cac:PartyTaxScheme>
        <cbc:TaxLevelCode>N</cbc:TaxLevelCode>
        <cac:TaxScheme><cbc:Name/></cac:TaxScheme>
      </cac:PartyTaxScheme>
    </cac:Party>
  </cac:AccountingCustomerParty>`,
		schemeID,
		escapeXML(inv.CustomerVKN),
		escapeXML(inv.CustomerName),
	)
}

// buildTaxSections generates TaxTotal elements grouped by tax rate.
func buildTaxSections(items []domain.InvoiceItem) string {
	type taxGroup struct {
		base int64
		tax  int64
	}
	groups := make(map[int32]*taxGroup)
	for _, item := range items {
		g, ok := groups[item.TaxRateBPS]
		if !ok {
			g = &taxGroup{}
			groups[item.TaxRateBPS] = g
		}
		g.base += item.LineTotal
		g.tax += item.TaxAmount
	}

	var totalTax int64
	var sb strings.Builder
	sb.WriteString("  <cac:TaxTotal>\n")
	for rate, g := range groups {
		pct := rate / 100
		totalTax += g.tax
		sb.WriteString(fmt.Sprintf(`    <cac:TaxSubtotal>
      <cbc:TaxableAmount currencyID="TRY">%s</cbc:TaxableAmount>
      <cbc:TaxAmount currencyID="TRY">%s</cbc:TaxAmount>
      <cbc:Percent>%d</cbc:Percent>
      <cac:TaxCategory>
        <cac:TaxScheme><cbc:Name>KDV</cbc:Name><cbc:TaxTypeCode>0015</cbc:TaxTypeCode></cac:TaxScheme>
      </cac:TaxCategory>
    </cac:TaxSubtotal>`,
			formatKurus(g.base), formatKurus(g.tax), pct))
		sb.WriteString("\n")
	}
	sb.WriteString(fmt.Sprintf("    <cbc:TaxAmount currencyID=\"TRY\">%s</cbc:TaxAmount>\n", formatKurus(totalTax)))
	sb.WriteString("  </cac:TaxTotal>\n")
	return sb.String()
}

func buildTotals(inv domain.Invoice) string {
	return fmt.Sprintf(`  <cac:LegalMonetaryTotal>
    <cbc:LineExtensionAmount currencyID="%s">%s</cbc:LineExtensionAmount>
    <cbc:TaxExclusiveAmount currencyID="%s">%s</cbc:TaxExclusiveAmount>
    <cbc:TaxInclusiveAmount currencyID="%s">%s</cbc:TaxInclusiveAmount>
    <cbc:PayableAmount currencyID="%s">%s</cbc:PayableAmount>
  </cac:LegalMonetaryTotal>`,
		inv.Currency, formatKurus(inv.AmountExcludingTax),
		inv.Currency, formatKurus(inv.AmountExcludingTax),
		inv.Currency, formatKurus(inv.AmountTotal),
		inv.Currency, formatKurus(inv.AmountTotal),
	)
}

func buildLines(items []domain.InvoiceItem) string {
	var sb strings.Builder
	for i, item := range items {
		pct := item.TaxRateBPS / 100
		sb.WriteString(fmt.Sprintf(`  <cac:InvoiceLine>
    <cbc:ID>%d</cbc:ID>
    <cbc:InvoicedQuantity unitCode="C62">%d</cbc:InvoicedQuantity>
    <cbc:LineExtensionAmount currencyID="TRY">%s</cbc:LineExtensionAmount>
    <cac:TaxTotal>
      <cbc:TaxAmount currencyID="TRY">%s</cbc:TaxAmount>
      <cac:TaxSubtotal>
        <cbc:TaxableAmount currencyID="TRY">%s</cbc:TaxableAmount>
        <cbc:TaxAmount currencyID="TRY">%s</cbc:TaxAmount>
        <cbc:Percent>%d</cbc:Percent>
        <cac:TaxCategory>
          <cac:TaxScheme><cbc:Name>KDV</cbc:Name><cbc:TaxTypeCode>0015</cbc:TaxTypeCode></cac:TaxScheme>
        </cac:TaxCategory>
      </cac:TaxSubtotal>
    </cac:TaxTotal>
    <cac:Item><cbc:Name>%s</cbc:Name></cac:Item>
    <cac:Price><cbc:PriceAmount currencyID="TRY">%s</cbc:PriceAmount></cac:Price>
  </cac:InvoiceLine>`,
			i+1,
			item.Quantity,
			formatKurus(item.LineTotal),
			formatKurus(item.TaxAmount),
			formatKurus(item.LineTotal),
			formatKurus(item.TaxAmount),
			pct,
			escapeXML(item.ProductName),
			formatKurus(item.UnitPriceAmount),
		))
		sb.WriteString("\n")
	}
	return sb.String()
}

// formatKurus converts int64 kuruş to a "x.xx" decimal string for XML output.
func formatKurus(v int64) string {
	if v < 0 {
		return fmt.Sprintf("-%d.%02d", (-v)/100, (-v)%100)
	}
	return fmt.Sprintf("%d.%02d", v/100, v%100)
}

// invoiceNumber returns a formatted GİB invoice number (e.g. "ONM2026000000001").
// The prefix should be configured per-tenant; this helper is used when the service
// has not yet assigned a real number (e.g. before submission).
func makeInvoiceNumber(prefix string, year int, seq int) string {
	if prefix == "" {
		prefix = "ONM"
	}
	return fmt.Sprintf("%s%d%09d", prefix, year, seq)
}

// nextInvoiceNumber generates a sequential invoice number using the issue year and
// a monotonically increasing sequence.  Called before UBL XML generation.
func nextInvoiceNumber(prefix string, issueDate time.Time, seq int) string {
	return makeInvoiceNumber(prefix, issueDate.Year(), seq)
}
