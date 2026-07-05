package receipt

import (
	"bytes"
	"testing"
	"time"

	"onlinemenu.tr/pos-desktop/internal/hardware/escpos"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse("2006-01-02T15:04:05Z", s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return tm.Local()
}

func TestBuild_GoldenByteSequence_Width48(t *testing.T) {
	cfg := Config{BusinessName: "Test Lokanta", BranchName: "Merkez Sube", Width: escpos.Width48}
	opened := mustTime(t, "2026-07-05T12:30:00Z")
	items := []Item{
		{ProductName: "Cay", Quantity: 2, UnitPriceAmount: 500},
		{ProductName: "Kahve", Quantity: 1, UnitPriceAmount: 4500},
	}

	got := Build(cfg, "Masa 3", opened, items, 6000)

	want := escpos.NewBuilder(escpos.Width48).Init().
		Align(escpos.AlignCenter).SetMode(true, true).Line("Test Lokanta").
		SetMode(false, false).
		Line("Merkez Sube").
		Align(escpos.AlignLeft).
		Line("Masa 3").
		Line(opened.Format("02.01.2006 15:04")).
		Divider().
		Line(escpos.Columns(48, "2x Cay", "10,00 TL")).
		Line(escpos.Columns(48, "1x Kahve", "45,00 TL")).
		Divider().
		SetMode(true, false).
		Line(escpos.Columns(48, "TOPLAM", "55,00 TL")).
		SetMode(false, false).
		Line(escpos.Columns(48, "ALINAN", "60,00 TL")).
		Line(escpos.Columns(48, "PARA ÜSTÜ", "5,00 TL")).
		Feed(1).
		Align(escpos.AlignCenter).
		Line(footerLine1).
		Line(footerLine2).
		Feed(1).
		Cut(escpos.CutFull, 3).
		Bytes()

	if !bytes.Equal(got, want) {
		t.Fatalf("Build() =\n% x\nwant\n% x", got, want)
	}
}

func TestBuild_NoBranchNameOmitsLine(t *testing.T) {
	cfg := Config{BusinessName: "Test Lokanta", Width: escpos.Width48}
	opened := mustTime(t, "2026-07-05T12:30:00Z")
	got := Build(cfg, "Masa 1", opened, nil, 0)

	want := escpos.NewBuilder(escpos.Width48).Init().
		Align(escpos.AlignCenter).SetMode(true, true).Line("Test Lokanta").
		SetMode(false, false).
		Align(escpos.AlignLeft).
		Line("Masa 1").
		Line(opened.Format("02.01.2006 15:04")).
		Divider().
		Divider().
		SetMode(true, false).
		Line(escpos.Columns(48, "TOPLAM", "0,00 TL")).
		SetMode(false, false).
		Feed(1).
		Align(escpos.AlignCenter).
		Line(footerLine1).
		Line(footerLine2).
		Feed(1).
		Cut(escpos.CutFull, 3).
		Bytes()

	if !bytes.Equal(got, want) {
		t.Fatalf("Build() =\n% x\nwant\n% x", got, want)
	}
}

func TestBuild_ZeroReceivedAmountOmitsPaymentLines(t *testing.T) {
	cfg := Config{Width: escpos.Width32}
	opened := mustTime(t, "2026-01-01T00:00:00Z")
	items := []Item{{ProductName: "Su", Quantity: 1, UnitPriceAmount: 1000}}

	got := Build(cfg, "Paket servis", opened, items, 0)

	if bytes.Contains(got, []byte("ALINAN")) {
		t.Fatal("Build() with receivedAmount=0 must omit the ALINAN line (reprint with unknown received amount)")
	}
	if bytes.Contains(got, escpos.EncodeCP857("ÜSTÜ")) {
		t.Fatal("Build() with receivedAmount=0 must omit the PARA ÜSTÜ line")
	}
	if !bytes.Contains(got, []byte("TOPLAM")) {
		t.Fatal("Build() must always print the TOPLAM line")
	}
}

func TestBuild_EmptyBusinessNameFallsBackToDefault(t *testing.T) {
	cfg := Config{Width: escpos.Width32}
	got := Build(cfg, "", time.Now(), nil, 0)
	if !bytes.Contains(got, []byte(defaultBusinessName)) {
		t.Fatalf("Build() with empty BusinessName must fall back to %q", defaultBusinessName)
	}
}

// TestBuild_LongBusinessNameTruncatedForDoubleWidthLine guards the header
// line specifically: it prints at SetMode(bold, double), which doubles
// BOTH width and height per rune (ESC/POS has no independent single-axis
// double-size bit — see SetMode's doc comment), so a business name must be
// truncated to half the paper's column count there, not the full count, or
// it overflows the physical paper width.
func TestBuild_LongBusinessNameTruncatedForDoubleWidthLine(t *testing.T) {
	longName := "Bu Isim Otuz Alti Kolonu Kesinlikle Asar"
	const width = escpos.Width32
	cfg := Config{BusinessName: longName, Width: width}
	got := Build(cfg, "Masa 1", time.Now(), nil, 0)

	if bytes.Contains(got, escpos.EncodeCP857(longName)) {
		t.Fatalf("Build() printed the full %d-rune business name untruncated on a double-width line (paper width %d, so max is %d runes there)", len([]rune(longName)), width, int(width)/2)
	}
	wantTruncated := escpos.EncodeCP857(escpos.Truncate(longName, int(width)/2))
	if !bytes.Contains(got, wantTruncated) {
		t.Fatalf("Build() header line does not contain the expected truncated (to %d runes) business name", int(width)/2)
	}
}

func TestBuild_EmptyTableLabelFallsBackToAdisyon(t *testing.T) {
	cfg := Config{Width: escpos.Width32}
	got := Build(cfg, "", time.Now(), nil, 0)
	if !bytes.Contains(got, []byte("Adisyon")) {
		t.Fatal(`Build() with empty tableLabel must fall back to "Adisyon"`)
	}
}

// TestBuild_MandatoryDisclaimerAlwaysPresent pins the ADR-FISCAL-001-driven
// requirement (task brief: "ZORUNLU") that every receipt, regardless of
// width/content, states it carries no fiscal value — asserting against the
// REQUIRED WORDING (independently re-typed here, CP857-encoded the same way
// Build's own output is), not against footerLine1/footerLine2 themselves:
// comparing against those constants directly would let a future edit that
// breaks the disclaimer's Turkish spelling (e.g. re-introducing the
// de-accented "fisidir"/"degeri" typo this package's history already hit
// once) pass silently, since the test and the code would agree with each
// other while both being wrong.
func TestBuild_MandatoryDisclaimerAlwaysPresent(t *testing.T) {
	wantPart1 := escpos.EncodeCP857("bilgi fişidir")
	wantPart2 := escpos.EncodeCP857("mali değeri yoktur")
	for _, width := range []escpos.Width{escpos.Width32, escpos.Width48} {
		got := Build(Config{Width: width}, "Masa 1", time.Now(), nil, 0)
		if !bytes.Contains(got, wantPart1) {
			t.Fatalf(`width %d: Build() missing mandatory disclaimer wording "bilgi fişidir" (properly accented)`, width)
		}
		if !bytes.Contains(got, wantPart2) {
			t.Fatalf(`width %d: Build() missing mandatory disclaimer wording "mali değeri yoktur" (properly accented)`, width)
		}
	}
}

// TestBuild_TurkishProductNameEncodedCorrectly is the end-to-end Turkish
// character test the task brief calls for: a product name using every
// Turkish letter must appear in the job's CP857 bytes exactly as
// escpos.EncodeCP857 would encode it standalone — proving Build doesn't
// route item names through some other, incorrect encoding path.
func TestBuild_TurkishProductNameEncodedCorrectly(t *testing.T) {
	name := "Çığ Köfte Şalgam"
	items := []Item{{ProductName: name, Quantity: 3, UnitPriceAmount: 1250}}
	got := Build(Config{Width: escpos.Width48}, "Masa 5", time.Now(), items, 0)

	wantLine := escpos.Columns(48, "3x "+name, "37,50 TL")
	wantBytes := escpos.EncodeCP857(wantLine)
	if !bytes.Contains(got, wantBytes) {
		t.Fatalf("Build() output does not contain the expected CP857-encoded Turkish item line for %q", name)
	}
}

// TestBuild_LongProductNameTruncatedNotWrapped pins the "uzun ürün adı
// kesme" (long product name truncation) requirement — Columns must
// truncate rather than let a too-long name push the price off the printer's
// physical width.
func TestBuild_LongProductNameTruncatedNotWrapped(t *testing.T) {
	longName := "Bu Cok Uzun Bir Urun Adi Ki Kesinlikle Tek Satira Sigmaz Ve Fiyati Da Var"
	items := []Item{{ProductName: longName, Quantity: 1, UnitPriceAmount: 100}}

	for _, width := range []escpos.Width{escpos.Width32, escpos.Width48} {
		got := Build(Config{Width: width}, "Masa 1", time.Now(), items, 0)
		lines := bytes.Split(got, []byte("\n"))
		for _, line := range lines {
			if len(line) > int(width)+8 { // + generous slack for any control bytes sharing the segment
				t.Fatalf("width %d: a line is %d bytes, want <= ~%d — long product name was not truncated to the column width", width, len(line), width)
			}
		}
	}
}

func TestGroupThousands(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{5, "5"},
		{999, "999"},
		{1000, "1.000"},
		{12345, "12.345"},
		{1234567, "1.234.567"},
	}
	for _, tt := range tests {
		if got := groupThousands(tt.in); got != tt.want {
			t.Errorf("groupThousands(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatMoneyTL(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0,00 TL"},
		{5, "0,05 TL"},
		{100, "1,00 TL"},
		{123456, "1.234,56 TL"},
	}
	for _, tt := range tests {
		if got := formatMoneyTL(tt.in); got != tt.want {
			t.Errorf("formatMoneyTL(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestItem_Total(t *testing.T) {
	it := Item{ProductName: "x", Quantity: 3, UnitPriceAmount: 250}
	if got := it.Total(); got != 750 {
		t.Fatalf("Total() = %d, want 750", got)
	}
}
