// Package receipt builds the ESC/POS byte stream for a POS station's
// "informational receipt" (bilgi fişi) — the paper handed to a customer
// after a cash sale. This is explicitly NOT a fiscal/ÖKC receipt: no fiscal
// device signs or registers this printout (see ADR-FISCAL-001, which this
// package's footer line exists specifically to keep true — a customer must
// never mistake this for a fiscal document).
//
// This package has no dependency on apiclient, the Wails-bound App struct,
// or hardware.Printer — it only turns plain Go values (Item, a table label,
// a timestamp, amounts in kuruş) into bytes via escpos.Builder, so it can be
// tested in complete isolation from the check/order/payment domain types
// and from any real or fake printer connection.
package receipt

import (
	"fmt"
	"strings"
	"time"

	"onlinemenu.tr/pos-desktop/internal/hardware/escpos"
)

// Config carries the station-level details every receipt prints in its
// header — set once from internal/config.Config at startup (see
// app.go's startHardware), not per print call.
type Config struct {
	// BusinessName is the tenant's trade name (config.json's
	// business_name / POS_BUSINESS_NAME). Falls back to a generic label
	// if empty rather than printing a blank header line.
	BusinessName string
	// BranchName is the branch/şube name (config.json's branch_name /
	// POS_BRANCH_NAME). Omitted from the header entirely if empty — not
	// every tenant is multi-branch.
	BranchName string
	// Width is the printer's paper width (escpos.Width32 or Width48),
	// mirroring config.Config.PrinterWidth.
	Width escpos.Width
}

// defaultBusinessName is printed when Config.BusinessName is unset, so a
// station with a missing config.json field still produces a valid receipt
// instead of a blank or malformed header line.
const defaultBusinessName = "Online Menu POS"

// Item is one order line to print — a flattened, receipt-local view of
// apiclient.OrderItem (main.OrderItemDTO in pos.go), deliberately not
// reusing that type so this package stays free of any apiclient import.
type Item struct {
	ProductName     string
	Quantity        int
	UnitPriceAmount int64 // kuruş, per unit
}

// Total returns this item's line total in kuruş.
func (it Item) Total() int64 {
	return int64(it.Quantity) * it.UnitPriceAmount
}

// footerLine1/footerLine2 together spell the mandatory "bu bir bilgi
// fişidir, mali değeri yoktur" disclaimer (task brief: "ZORUNLU"), split
// across two centered lines (20 and 18 runes — both fit the narrower
// 32-column paper width) so it never wraps mid-word. Every Turkish letter
// in these two lines (ş, ğ, İ...) IS kept — only the em dash between the
// two clauses was dropped in favor of a line break, since em dash (U+2014)
// has no CP857 code point (see escpos.EncodeCP857's fallback behavior) and
// this is exactly the line that must never hit that fallback. Encoded via
// escpos.EncodeCP857 like every other line Build writes — there is no
// separate "plain ASCII" text path for the footer.
const (
	footerLine1 = "Bu bir bilgi fişidir,"
	footerLine2 = "mali değeri yoktur."
)

// Build assembles the full ESC/POS byte stream for one check's receipt.
//
// receivedAmount is the cash amount the customer handed over, in kuruş, as
// already computed by the cashier UI at the moment of payment (see
// pos-desktop/README.md and app.go's PrintReceipt doc comment for why this
// is passed in rather than re-fetched from the backend: ListCheckPayments
// requires payment.payment.read, which the cashier role does not have).
// Pass 0 to omit the "ALINAN" / "PARA ÜSTÜ" lines entirely (e.g. a reprint
// where the original received amount is no longer known) — the totals line
// alone is still a complete, correct receipt.
func Build(cfg Config, tableLabel string, openedAt time.Time, items []Item, receivedAmount int64) []byte {
	width := cfg.Width
	if width != escpos.Width32 && width != escpos.Width48 {
		width = escpos.Width48
	}
	cols := int(width)

	b := escpos.NewBuilder(width).Init()

	businessName := cfg.BusinessName
	if businessName == "" {
		businessName = defaultBusinessName
	}
	// SetMode(bold, double) doubles BOTH width and height (see its doc
	// comment — ESC/POS has no independent single-axis double-size bit), so
	// at this print mode each rune occupies 2 physical printer columns:
	// truncate to cols/2 runes here, not cols, or a business name close to
	// the full paper width would overflow/wrap past its right edge.
	b.Align(escpos.AlignCenter).SetMode(true, true).Line(escpos.Truncate(businessName, cols/2))
	b.SetMode(false, false)
	if cfg.BranchName != "" {
		b.Line(escpos.Truncate(cfg.BranchName, cols))
	}

	b.Align(escpos.AlignLeft)
	label := tableLabel
	if label == "" {
		label = "Adisyon"
	}
	b.Line(escpos.Truncate(label, cols))
	b.Line(openedAt.Local().Format("02.01.2006 15:04"))
	b.Divider()

	var subtotal int64
	for _, it := range items {
		lineTotal := it.Total()
		subtotal += lineTotal
		left := fmt.Sprintf("%dx %s", it.Quantity, it.ProductName)
		b.Line(escpos.Columns(cols, left, formatMoneyTL(lineTotal)))
	}
	b.Divider()

	b.SetMode(true, false)
	b.Line(escpos.Columns(cols, "TOPLAM", formatMoneyTL(subtotal)))
	b.SetMode(false, false)

	if receivedAmount > 0 {
		change := receivedAmount - subtotal
		if change < 0 {
			change = 0
		}
		b.Line(escpos.Columns(cols, "ALINAN", formatMoneyTL(receivedAmount)))
		b.Line(escpos.Columns(cols, "PARA ÜSTÜ", formatMoneyTL(change)))
	}

	b.Feed(1)
	b.Align(escpos.AlignCenter)
	b.Line(footerLine1)
	b.Line(footerLine2)
	b.Feed(1)
	b.Cut(escpos.CutFull, 3)

	return b.Bytes()
}

// formatMoneyTL renders a kuruş amount as a Turkish-grouped decimal string
// with a plain ASCII "TL" suffix — deliberately not the '₺' glyph, which
// postdates CP857 and has no code point on it (see
// escpos.EncodeCP857's doc comment); using "TL" here means the mandatory
// money lines never hit that encoder fallback path.
func formatMoneyTL(kurus int64) string {
	neg := kurus < 0
	if neg {
		kurus = -kurus
	}
	whole := kurus / 100
	frac := kurus % 100
	s := fmt.Sprintf("%s,%02d TL", groupThousands(whole), frac)
	if neg {
		s = "-" + s
	}
	return s
}

// groupThousands inserts '.' (the Turkish thousands separator) every three
// digits from the right, e.g. 1234567 -> "1.234.567".
func groupThousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return strings.Join(parts, ".")
}
