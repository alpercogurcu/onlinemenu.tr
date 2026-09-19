package receipt

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"onlinemenu.tr/pos-desktop/internal/hardware/escpos"
)

// printedLine is one physical text line of a decoded job together with the
// print mode that was active while it was written — the mode decides how many
// printer columns each byte occupies (double = 2), which is what the
// fit-to-paper assertions below need.
type printedLine struct {
	text   []byte // CP857 bytes: one byte == one printer column at normal size
	bold   bool
	double bool
}

func (l printedLine) columns() int {
	if l.double {
		return len(l.text) * 2
	}
	return len(l.text)
}

// decodeJob walks an ESC/POS byte stream the same way a printer would and
// returns its text lines plus the number of GS V (cut) commands seen. Only the
// command subset escpos.Builder emits is understood.
func decodeJob(t *testing.T, job []byte) (lines []printedLine, cuts int) {
	t.Helper()
	var mode byte
	var cur []byte
	flush := func() {
		lines = append(lines, printedLine{text: cur, bold: mode&(1<<3) != 0, double: mode&(1<<4) != 0})
		cur = nil
	}
	for i := 0; i < len(job); {
		switch job[i] {
		case 0x1b:
			if i+1 >= len(job) {
				t.Fatalf("truncated ESC command at %d", i)
			}
			switch job[i+1] {
			case '@':
				i += 2
			case 't', 'a', 'd':
				i += 3
			case '!':
				mode = job[i+2]
				i += 3
			default:
				t.Fatalf("unexpected ESC command %q at %d", job[i+1], i)
			}
		case 0x1d:
			if i+3 >= len(job) || job[i+1] != 'V' {
				t.Fatalf("unexpected GS command at %d", i)
			}
			cuts++
			i += 4
		case '\n':
			flush()
			i++
		default:
			cur = append(cur, job[i])
			i++
		}
	}
	if len(cur) > 0 {
		flush()
	}
	return lines, cuts
}

func lineTexts(lines []printedLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l.text)
	}
	return out
}

func TestBuildKitchenTicket_GoldenByteSequence(t *testing.T) {
	placedAt := mustTime(t, "2026-09-19T14:32:00Z")
	items := []KitchenItem{
		{ProductName: "Adana Kebap", Quantity: 2, Note: "acısız"},
		{ProductName: "Ayran", Quantity: 1},
	}

	got := BuildKitchenTicket(Config{Width: escpos.Width48}, "Masa 7", "a1b2c3d4", placedAt, items)

	want := escpos.NewBuilder(escpos.Width48).Init().
		Align(escpos.AlignCenter).SetMode(true, true).Line("MUTFAK").
		Align(escpos.AlignLeft).
		Line("Masa 7").
		SetMode(false, false).
		Line(escpos.Columns(48, placedAt.Local().Format("15:04"), "#a1b2c3d4")).
		Divider().
		SetMode(true, false).
		Line("2x  Adana Kebap").
		SetMode(false, false).
		Line("    > acısız").
		SetMode(true, false).
		Line("1x  Ayran").
		SetMode(false, false).
		Divider().
		Feed(1).
		Cut(escpos.CutFull, 3).
		Bytes()

	if !bytes.Equal(got, want) {
		t.Fatalf("BuildKitchenTicket() =\n% x\nwant\n% x", got, want)
	}
}

func TestBuildKitchenTicket_Content(t *testing.T) {
	placedAt := mustTime(t, "2026-09-19T09:05:00Z")

	tests := []struct {
		name      string
		tableName string
		items     []KitchenItem
		wantLines []string // must appear, in this order, among the normal-size lines
	}{
		{
			name:      "quantity and name separated by two spaces",
			tableName: "Masa 3",
			items:     []KitchenItem{{ProductName: "Adana Kebap", Quantity: 2}},
			wantLines: []string{"2x  Adana Kebap"},
		},
		{
			name:      "note sits under its item, indented",
			tableName: "Masa 3",
			items: []KitchenItem{
				{ProductName: "Lahmacun", Quantity: 3, Note: "acısız"},
				{ProductName: "Ayran", Quantity: 1},
			},
			wantLines: []string{"3x  Lahmacun", "    > acısız", "1x  Ayran"},
		},
		{
			name:      "multi-digit quantity keeps the same layout",
			tableName: "Masa 3",
			items:     []KitchenItem{{ProductName: "Çay", Quantity: 12}},
			wantLines: []string{"12x  Çay"},
		},
		{
			name:      "blank note is not printed",
			tableName: "Masa 3",
			items:     []KitchenItem{{ProductName: "Su", Quantity: 1, Note: "   "}},
			wantLines: []string{"1x  Su"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, width := range []escpos.Width{escpos.Width32, escpos.Width48} {
				job := BuildKitchenTicket(Config{Width: width}, tt.tableName, "deadbeef", placedAt, tt.items)

				// Turkish letters must go through the CP857 path, so compare
				// against the encoded form of each expected line.
				for _, want := range tt.wantLines {
					if !bytes.Contains(job, escpos.EncodeCP857(want)) {
						t.Fatalf("width %d: job missing line %q", width, want)
					}
				}

				lines, _ := decodeJob(t, job)
				texts := lineTexts(lines)
				idx := 0
				for _, want := range tt.wantLines {
					enc := string(escpos.EncodeCP857(want))
					found := false
					for idx < len(texts) {
						idx++
						if texts[idx-1] == enc {
							found = true
							break
						}
					}
					if !found {
						t.Fatalf("width %d: line %q missing or out of order in %q", width, want, texts)
					}
				}
			}
		})
	}
}

func TestBuildKitchenTicket_HeaderTableTimeAndShortID(t *testing.T) {
	placedAt := mustTime(t, "2026-09-19T14:32:00Z")
	job := BuildKitchenTicket(Config{Width: escpos.Width48}, "Bahçe 12", "0f1e2d3c", placedAt, []KitchenItem{{ProductName: "Su", Quantity: 1}})

	lines, cuts := decodeJob(t, job)
	if cuts != 1 {
		t.Fatalf("cuts = %d, want exactly 1", cuts)
	}

	var title, table, meta *printedLine
	for i := range lines {
		switch string(lines[i].text) {
		case "MUTFAK":
			title = &lines[i]
		case string(escpos.EncodeCP857("Bahçe 12")):
			table = &lines[i]
		}
		if strings.Contains(string(lines[i].text), "#0f1e2d3c") {
			meta = &lines[i]
		}
	}
	if title == nil || !title.double || !title.bold {
		t.Fatalf("MUTFAK title must be printed bold+double, got %+v", title)
	}
	if table == nil || !table.double {
		t.Fatalf("table label must be printed in double size, got %+v", table)
	}
	if meta == nil || meta.double {
		t.Fatalf("time/short-id line missing or double-size: %+v", meta)
	}
	if want := placedAt.Local().Format("15:04"); !strings.Contains(string(meta.text), want) {
		t.Fatalf("meta line %q missing time %q", meta.text, want)
	}
}

func TestBuildKitchenTicket_NeverPrintsPrices(t *testing.T) {
	items := []KitchenItem{{ProductName: "Adana Kebap", Quantity: 2, Note: "acılı"}}
	job := BuildKitchenTicket(Config{Width: escpos.Width48}, "Masa 1", "abcdef12", time.Now(), items)

	for _, forbidden := range []string{"TL", "TOPLAM", ",00", "bilgi fi"} {
		if bytes.Contains(job, []byte(forbidden)) {
			t.Fatalf("kitchen ticket must carry no price/receipt text, found %q", forbidden)
		}
	}
}

func TestBuildKitchenTicket_ItemLinesAreBoldNotesAreNot(t *testing.T) {
	job := BuildKitchenTicket(Config{Width: escpos.Width48}, "Masa 1", "abcdef12", time.Now(),
		[]KitchenItem{{ProductName: "Kebap", Quantity: 2, Note: "acısız"}})

	lines, _ := decodeJob(t, job)
	for _, l := range lines {
		switch string(l.text) {
		case "2x  Kebap":
			if !l.bold || l.double {
				t.Fatalf("item line must be bold, normal size: %+v", l)
			}
		case string(escpos.EncodeCP857("    > acısız")):
			if l.bold || l.double {
				t.Fatalf("note line must be plain: %+v", l)
			}
		}
	}
}

// TestBuildKitchenTicket_FitsPaperWidth is the column-fit guarantee: whatever
// the input length, no physical line may exceed the paper width, counting
// double-size lines at two columns per glyph. Overlong text is wrapped, not
// truncated, so the kitchen never loses part of an order.
func TestBuildKitchenTicket_FitsPaperWidth(t *testing.T) {
	longName := "Karışık Izgara Büyük Boy Özel Soslu Acılı Porsiyon"
	longNote := "acısız olsun, soğansız, fıstık alerjisi var lütfen dikkat edin"
	longTable := "Teras Bahçe Salon Masa 12"
	unbroken := strings.Repeat("W", 70)

	tests := []struct {
		name  string
		table string
		items []KitchenItem
	}{
		{"long product and note", "Masa 7", []KitchenItem{{ProductName: longName, Quantity: 2, Note: longNote}}},
		{"long table label", longTable, []KitchenItem{{ProductName: "Su", Quantity: 1}}},
		{"unbreakable word", "Masa 1", []KitchenItem{{ProductName: unbroken, Quantity: 1, Note: unbroken}}},
		{"large quantity", "Masa 1", []KitchenItem{{ProductName: longName, Quantity: 1234}}},
		{"many items", "Masa 1", func() []KitchenItem {
			out := make([]KitchenItem, 30)
			for i := range out {
				out[i] = KitchenItem{ProductName: longName, Quantity: i + 1, Note: longNote}
			}
			return out
		}()},
	}

	for _, tt := range tests {
		for _, width := range []escpos.Width{escpos.Width32, escpos.Width48} {
			t.Run(fmt.Sprintf("%s/%d", tt.name, width), func(t *testing.T) {
				job := BuildKitchenTicket(Config{Width: width}, tt.table, "abcdef12", time.Now(), tt.items)
				lines, cuts := decodeJob(t, job)
				if cuts != 1 {
					t.Fatalf("cuts = %d, want 1", cuts)
				}
				for _, l := range lines {
					if l.columns() > int(width) {
						t.Fatalf("width %d: line %q spans %d columns", width, l.text, l.columns())
					}
				}
			})
		}
	}
}

// TestBuildKitchenTicket_WrapPreservesText proves wrapping is lossless: the
// words of a long item reappear, in order, across its continuation lines.
func TestBuildKitchenTicket_WrapPreservesText(t *testing.T) {
	name := "Karışık Izgara Büyük Boy Özel Soslu Acılı Porsiyon"
	note := "acısız olsun soğansız fıstık alerjisi var"

	for _, width := range []escpos.Width{escpos.Width32, escpos.Width48} {
		job := BuildKitchenTicket(Config{Width: width}, "Masa 1", "abcdef12", time.Now(),
			[]KitchenItem{{ProductName: name, Quantity: 2, Note: note}})
		lines, _ := decodeJob(t, job)

		var body []string
		for _, l := range lines {
			if l.double {
				continue
			}
			s := string(l.text)
			if strings.HasPrefix(s, "-") || strings.Contains(s, "#abcdef12") {
				continue
			}
			body = append(body, strings.TrimSpace(s))
		}
		joined := strings.Join(body, " ")
		want := "2x " + name + " > " + note
		// joined holds raw CP857 bytes already, so only the expectation is encoded.
		if strings.Join(strings.Fields(joined), " ") != string(escpos.EncodeCP857(strings.Join(strings.Fields(want), " "))) {
			t.Fatalf("width %d: wrapped text differs\n got: %q\nwant: %q", width, joined, want)
		}
	}
}

func TestBuildKitchenTicket_WrapContinuationIsIndented(t *testing.T) {
	job := BuildKitchenTicket(Config{Width: escpos.Width32}, "Masa 1", "abcdef12", time.Now(),
		[]KitchenItem{{ProductName: "Karışık Izgara Büyük Boy Özel Soslu Acılı Porsiyon", Quantity: 2}})
	lines, _ := decodeJob(t, job)

	var item []printedLine
	for _, l := range lines {
		if l.bold && !l.double {
			item = append(item, l)
		}
	}
	if len(item) < 2 {
		t.Fatalf("expected the long item to wrap over multiple lines, got %d", len(item))
	}
	for _, l := range item[1:] {
		if !bytes.HasPrefix(l.text, []byte("    ")) {
			t.Fatalf("continuation line %q must be indented under the product name", l.text)
		}
	}
}

// TestBuildKitchenTicket_StripsControlCharacters guards the printer command
// channel: a note typed by a customer (QR self-order) must not be able to
// smuggle ESC/POS commands such as a cut or a cash-drawer pulse into the job.
func TestBuildKitchenTicket_StripsControlCharacters(t *testing.T) {
	evil := "acısız\x1bp\x00\x19\x1dV\x00 tamam\r\nyeni satır"
	job := BuildKitchenTicket(Config{Width: escpos.Width48}, "Masa\x1b@ 1", "ab\x1dcd", time.Now(),
		[]KitchenItem{{ProductName: "Ke\x07bap", Quantity: 1, Note: evil}})

	_, cuts := decodeJob(t, job)
	if cuts != 1 {
		t.Fatalf("cuts = %d, want 1 — a control sequence in free text reached the printer", cuts)
	}
	if bytes.Contains(job, []byte{0x1b, 'p'}) {
		t.Fatal("ESC p (drawer pulse) leaked through the note")
	}
	if !bytes.Contains(job, escpos.EncodeCP857("tamam yeni satır")) {
		t.Fatal("printable text around the stripped controls must survive")
	}
}

func TestBuildKitchenTicket_InvalidWidthFallsBackTo48(t *testing.T) {
	items := []KitchenItem{{ProductName: "Su", Quantity: 1}}
	placedAt := mustTime(t, "2026-09-19T14:32:00Z")
	got := BuildKitchenTicket(Config{Width: 0}, "Masa 1", "abcdef12", placedAt, items)
	want := BuildKitchenTicket(Config{Width: escpos.Width48}, "Masa 1", "abcdef12", placedAt, items)
	if !bytes.Equal(got, want) {
		t.Fatal("unsupported width must fall back to 48 columns like Build does")
	}
}

func TestBuildKitchenTicket_EmptyLabelAndNoItems(t *testing.T) {
	job := BuildKitchenTicket(Config{Width: escpos.Width32}, "", "abcdef12", time.Now(), nil)
	if !bytes.Contains(job, []byte("Adisyon")) {
		t.Fatal(`empty table label must fall back to "Adisyon", as Build does`)
	}
	if _, cuts := decodeJob(t, job); cuts != 1 {
		t.Fatalf("cuts = %d, want 1", cuts)
	}
}

func TestShortOrderID(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"a1b2c3d4-1111-2222-3333-444455556666", "a1b2c3d4"},
		{"abc", "abc"},
		{"", ""},
		{"çşğüöıİç9", "çşğüöıİç"},
	}
	for _, tt := range tests {
		if got := ShortOrderID(tt.in); got != tt.want {
			t.Errorf("ShortOrderID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
