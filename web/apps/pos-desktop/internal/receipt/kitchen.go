package receipt

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"onlinemenu.tr/pos-desktop/internal/hardware/escpos"
)

// KitchenItem is one order line on a kitchen ticket. Unlike Item it carries no
// price on purpose: the kitchen must never see (or be able to leak) what a
// customer paid.
type KitchenItem struct {
	ProductName string
	Quantity    int
	// Note is the free-text line note ("acısız"); blank means none.
	Note string
}

const (
	kitchenTitle = "MUTFAK"

	// shortIDLen is how much of an order UUID the kitchen sees — enough to tell
	// two tickets apart at the pass, short enough to read across a room.
	shortIDLen = 8

	// noteMarker prefixes a note so it reads as belonging to the item above it.
	noteMarker = "    > "
	itemGap    = "  "
)

// ShortOrderID returns the first 8 runes of an order id, the number printed on
// the kitchen ticket.
func ShortOrderID(id string) string {
	return escpos.Truncate(id, shortIDLen)
}

// BuildKitchenTicket assembles the ESC/POS byte stream for the paper that
// goes to the kitchen when an order is placed: a big "MUTFAK" banner, the
// table in double size (read from across the pass), time and short order
// number, then one bold `2x  Adana Kebap` line per item with any note beneath.
// No prices, no totals, no disclaimer — this is a production slip, not a
// receipt.
//
// Long product names and notes are wrapped onto indented continuation lines
// rather than truncated: unlike a receipt line, losing the tail of a note
// ("...fıstık alerjisi var") would be a food-safety problem.
//
// Every piece of free text passes through sanitize first. Notes can originate
// from a customer's phone (QR self-order), and CP857 encoding passes ASCII
// control bytes through untouched, so without stripping them a note could
// inject printer commands (cut, drawer pulse) into the job.
func BuildKitchenTicket(cfg Config, tableLabel string, orderShortID string, placedAt time.Time, items []KitchenItem) []byte {
	width := normalizeWidth(cfg.Width)
	cols := int(width)

	b := escpos.NewBuilder(width).Init()

	b.Align(escpos.AlignCenter).SetMode(true, true).Line(kitchenTitle)

	label := sanitize(tableLabel)
	if label == "" {
		label = "Adisyon"
	}
	b.Align(escpos.AlignLeft)
	// Double size occupies two printer columns per glyph, so the table label
	// gets half the paper width.
	for _, line := range wrap("", label, cols/2) {
		b.Line(line)
	}
	b.SetMode(false, false)

	b.Line(escpos.Columns(cols, placedAt.Local().Format("15:04"), orderRef(orderShortID)))
	b.Divider()

	for _, it := range items {
		b.SetMode(true, false)
		for _, line := range wrap(itemPrefix(it.Quantity), sanitize(it.ProductName), cols) {
			b.Line(line)
		}
		b.SetMode(false, false)

		if note := sanitize(it.Note); note != "" {
			for _, line := range wrap(noteMarker, note, cols) {
				b.Line(line)
			}
		}
	}
	b.Divider()

	b.Feed(1)
	b.Cut(escpos.CutFull, 3)

	return b.Bytes()
}

func normalizeWidth(w escpos.Width) escpos.Width {
	if w != escpos.Width32 && w != escpos.Width48 {
		return escpos.Width48
	}
	return w
}

func itemPrefix(quantity int) string {
	return strconv.Itoa(quantity) + "x" + itemGap
}

func orderRef(shortID string) string {
	id := escpos.Truncate(sanitize(shortID), shortIDLen*2)
	if id == "" {
		return ""
	}
	return "#" + id
}

// sanitize turns free text into a single printable line: whitespace controls
// (tab, CR, LF) become spaces, every other control character is dropped, and
// runs of spaces collapse.
func sanitize(s string) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\r' || r == '\n':
			return ' '
		case unicode.IsControl(r):
			return -1
		default:
			return r
		}
	}, s)
	return strings.Join(strings.Fields(mapped), " ")
}

// wrap lays text out on lines of at most width runes. The first line starts
// with prefix; continuation lines are indented to the same depth so a wrapped
// item stays visually one block. A word longer than a whole line is split
// hard rather than dropped or allowed to overflow.
func wrap(prefix, text string, width int) []string {
	indent := strings.Repeat(" ", len([]rune(prefix)))
	avail := width - len(indent)
	if avail < 1 {
		avail = 1
	}

	var bodies []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			bodies = append(bodies, string(cur))
			cur = nil
		}
	}
	for _, word := range strings.Fields(text) {
		w := []rune(word)
		for len(w) > avail {
			flush()
			bodies = append(bodies, string(w[:avail]))
			w = w[avail:]
		}
		switch {
		case len(cur) == 0:
			cur = w
		case len(cur)+1+len(w) <= avail:
			cur = append(append(cur, ' '), w...)
		default:
			flush()
			cur = w
		}
	}
	flush()

	if len(bodies) == 0 {
		return []string{strings.TrimRight(prefix, " ")}
	}
	lines := make([]string, len(bodies))
	for i, body := range bodies {
		if i == 0 {
			lines[i] = prefix + body
		} else {
			lines[i] = indent + body
		}
	}
	return lines
}
