package escpos

import (
	"bytes"
	"testing"
)

// TestEncodeCP857_TurkishLetters pins every Turkish letter's byte value
// against the Unicode Consortium's own CP857 mapping file (see
// codepage857.go's doc comment for the exact URL/version) — not against
// this package's own table, so a wrong table and a wrong test can't agree
// with each other by construction. Each expected byte below was read
// directly off that file's "0xNN <TAB> 0xUUUU" lines for the given code
// point.
func TestEncodeCP857_TurkishLetters(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want byte
	}{
		{"ç LATIN SMALL LETTER C WITH CEDILLA", "ç", 0x87},
		{"Ç LATIN CAPITAL LETTER C WITH CEDILLA", "Ç", 0x80},
		{"ğ LATIN SMALL LETTER G WITH BREVE", "ğ", 0xa7},
		{"Ğ LATIN CAPITAL LETTER G WITH BREVE", "Ğ", 0xa6},
		{"ı LATIN SMALL LETTER DOTLESS I", "ı", 0x8d},
		{"İ LATIN CAPITAL LETTER I WITH DOT ABOVE", "İ", 0x98},
		{"ö LATIN SMALL LETTER O WITH DIAERESIS", "ö", 0x94},
		{"Ö LATIN CAPITAL LETTER O WITH DIAERESIS", "Ö", 0x99},
		{"ş LATIN SMALL LETTER S WITH CEDILLA", "ş", 0x9f},
		{"Ş LATIN CAPITAL LETTER S WITH CEDILLA", "Ş", 0x9e},
		{"ü LATIN SMALL LETTER U WITH DIAERESIS", "ü", 0x81},
		{"Ü LATIN CAPITAL LETTER U WITH DIAERESIS", "Ü", 0x9a},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EncodeCP857(tt.in)
			if len(got) != 1 || got[0] != tt.want {
				t.Fatalf("EncodeCP857(%q) = % x, want [% x]", tt.in, got, tt.want)
			}
		})
	}
}

// TestEncodeCP857_DotlessVsDottedI guards specifically against folding
// Turkish ı/İ onto ASCII i/I (or onto each other) — the single most common
// mistake in hand-rolled Turkish encoders (advisor-flagged risk).
func TestEncodeCP857_DotlessVsDottedI(t *testing.T) {
	cases := map[string]byte{
		"i": 'i',  // ASCII, unaffected by the Turkish table
		"I": 'I',  // ASCII, unaffected by the Turkish table
		"ı": 0x8d, // dotless small i — distinct code point, distinct byte
		"İ": 0x98, // dotted capital I — distinct code point, distinct byte
	}
	seen := map[byte]string{}
	for in, want := range cases {
		got := EncodeCP857(in)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("EncodeCP857(%q) = % x, want [%02x]", in, got, want)
		}
		if other, ok := seen[got[0]]; ok {
			t.Fatalf("%q and %q both encode to byte %02x — must be distinct", in, other, got[0])
		}
		seen[got[0]] = in
	}
}

func TestEncodeCP857_ASCIIPassthrough(t *testing.T) {
	in := "Adisyon #12 - Masa 3.50 TL"
	got := EncodeCP857(in)
	if !bytes.Equal(got, []byte(in)) {
		t.Fatalf("EncodeCP857(%q) = % x, want plain ASCII bytes", in, got)
	}
}

// TestEncodeCP857_LiraSignHasNoFallbackPanic documents that ₺ (U+20BA,
// Turkish lira sign) postdates CP857 (1996) and has no code point on this
// page (confirmed absent from the source mapping file) — encoding it must
// degrade to the visible fallback, never panic or silently drop the rune.
// internal/receipt sidesteps this in practice by spelling amounts with the
// literal "TL" instead of the ₺ glyph; this test exists so the encoder's
// own contract (no silent failure on ANY unmappable rune) is pinned
// independent of that receipt-layer decision.
func TestEncodeCP857_LiraSignHasNoFallbackPanic(t *testing.T) {
	got := EncodeCP857("₺")
	if len(got) != 1 || got[0] != cp857Fallback {
		t.Fatalf("EncodeCP857(%q) = % x, want fallback byte %02x", "₺", got, cp857Fallback)
	}
}

func TestEncodeCP857_MixedTurkishSentence(t *testing.T) {
	// "Çığ öğürtşü" is not a real word — it's a deliberately dense pangram-ish
	// probe hitting every Turkish letter at least once in one string, so a
	// single test asserts the whole table works together, not just isolated
	// runes.
	in := "Çığ öğürtşü İİ"
	got := EncodeCP857(in)
	runes := []rune(in)
	if len(got) != len(runes) {
		t.Fatalf("EncodeCP857(%q) produced %d bytes, want %d (one byte per rune, no multi-byte CP857 codes)", in, len(got), len(runes))
	}
	for i, r := range runes {
		want := cp857Encode[r]
		if r < 0x80 {
			want = byte(r)
		}
		if got[i] != want {
			t.Fatalf("EncodeCP857(%q)[%d] (rune %q) = %02x, want %02x", in, i, r, got[i], want)
		}
	}
}
