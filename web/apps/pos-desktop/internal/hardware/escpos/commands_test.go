package escpos

import (
	"bytes"
	"testing"
)

func TestBuilder_Init(t *testing.T) {
	got := NewBuilder(Width48).Init().Bytes()
	want := []byte{0x1b, '@', 0x1b, 't', codePagePC857}
	if !bytes.Equal(got, want) {
		t.Fatalf("Init() = % x, want % x", got, want)
	}
}

func TestBuilder_Align(t *testing.T) {
	tests := []struct {
		name string
		a    Align
		want byte
	}{
		{"left", AlignLeft, 0},
		{"center", AlignCenter, 1},
		{"right", AlignRight, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewBuilder(Width48).Align(tt.a).Bytes()
			want := []byte{0x1b, 'a', tt.want}
			if !bytes.Equal(got, want) {
				t.Fatalf("Align(%v) = % x, want % x", tt.a, got, want)
			}
		})
	}
}

func TestBuilder_SetMode(t *testing.T) {
	tests := []struct {
		name         string
		bold, double bool
		want         byte
	}{
		{"plain", false, false, 0},
		{"bold", true, false, modeBold},
		{"double", false, true, modeDoubleHeight | modeDoubleWidth},
		{"bold+double", true, true, modeBold | modeDoubleHeight | modeDoubleWidth},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewBuilder(Width48).SetMode(tt.bold, tt.double).Bytes()
			want := []byte{0x1b, '!', tt.want}
			if !bytes.Equal(got, want) {
				t.Fatalf("SetMode(%v,%v) = % x, want % x", tt.bold, tt.double, got, want)
			}
		})
	}
}

func TestBuilder_LineAppendsLF(t *testing.T) {
	got := NewBuilder(Width48).Line("Masa 3").Bytes()
	want := append([]byte("Masa 3"), '\n')
	if !bytes.Equal(got, want) {
		t.Fatalf("Line() = % x, want % x", got, want)
	}
}

func TestBuilder_LineEncodesTurkish(t *testing.T) {
	got := NewBuilder(Width48).Line("Çay").Bytes()
	want := []byte{0x80, 'a', 'y', '\n'} // Ç -> 0x80 (see codepage857_test.go)
	if !bytes.Equal(got, want) {
		t.Fatalf("Line(%q) = % x, want % x", "Çay", got, want)
	}
}

func TestBuilder_Feed(t *testing.T) {
	got := NewBuilder(Width48).Feed(3).Bytes()
	want := []byte{0x1b, 'd', 3}
	if !bytes.Equal(got, want) {
		t.Fatalf("Feed(3) = % x, want % x", got, want)
	}

	// Feed(0) / Feed(negative) is a no-op — must not emit a malformed
	// ESC d command with n=0 (some firmwares treat that as "feed 256/undefined").
	if got := NewBuilder(Width48).Feed(0).Bytes(); len(got) != 0 {
		t.Fatalf("Feed(0) = % x, want no bytes", got)
	}
}

func TestBuilder_Cut(t *testing.T) {
	got := NewBuilder(Width48).Cut(CutFull, 3).Bytes()
	want := []byte{0x1d, 'V', 'A', 3}
	if !bytes.Equal(got, want) {
		t.Fatalf("Cut(Full,3) = % x, want % x", got, want)
	}

	got = NewBuilder(Width48).Cut(CutPartial, 4).Bytes()
	want = []byte{0x1d, 'V', 'B', 4}
	if !bytes.Equal(got, want) {
		t.Fatalf("Cut(Partial,4) = % x, want % x", got, want)
	}
}

func TestBuilder_Divider(t *testing.T) {
	tests := []struct {
		width Width
		want  int
	}{
		{Width32, 32},
		{Width48, 48},
	}
	for _, tt := range tests {
		got := NewBuilder(tt.width).Divider().Bytes()
		// Divider() = Line(rule) => width dashes + one '\n'.
		if len(got) != int(tt.width)+1 {
			t.Fatalf("Divider() for width %d produced %d bytes, want %d", tt.width, len(got), tt.width+1)
		}
		for i := 0; i < int(tt.width); i++ {
			if got[i] != '-' {
				t.Fatalf("Divider()[%d] = %q, want '-'", i, got[i])
			}
		}
		if got[tt.width] != '\n' {
			t.Fatalf("Divider() missing trailing LF")
		}
	}
}

func TestBuilder_FullJobByteSequence(t *testing.T) {
	// One assembled job end-to-end: init -> centered bold header -> plain
	// item line with a Turkish product name -> divider -> cut. This is the
	// shape internal/receipt.Build produces; pinning the whole sequence
	// here (not just each command in isolation) catches ordering mistakes
	// a per-command test wouldn't.
	b := NewBuilder(Width32).
		Init().
		Align(AlignCenter).
		SetMode(true, false).
		Line("ONLINEMENU").
		SetMode(false, false).
		Align(AlignLeft).
		Line("1x Çay").
		Divider().
		Cut(CutFull, 3)

	got := b.Bytes()

	var want bytes.Buffer
	want.Write([]byte{0x1b, '@', 0x1b, 't', codePagePC857})
	want.Write([]byte{0x1b, 'a', 1})
	want.Write([]byte{0x1b, '!', modeBold})
	want.WriteString("ONLINEMENU")
	want.WriteByte('\n')
	want.Write([]byte{0x1b, '!', 0})
	want.Write([]byte{0x1b, 'a', 0})
	want.Write([]byte{'1', 'x', ' ', 0x80, 'a', 'y'})
	want.WriteByte('\n')
	for i := 0; i < 32; i++ {
		want.WriteByte('-')
	}
	want.WriteByte('\n')
	want.Write([]byte{0x1d, 'V', 'A', 3})

	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("full job = % x\nwant       % x", got, want.Bytes())
	}
}

func TestColumns(t *testing.T) {
	tests := []struct {
		name        string
		width       int
		left, right string
		want        string
	}{
		{"fits with padding", 20, "Çay", "12,50", padExpected(20, "Çay", "12,50")},
		{"exact fit no padding", 10, "12345", "6789", padExpected(10, "12345", "6789")},
		{"long left truncated", 10, "Uzun Ürün Adı Burada", "5,00", padExpected(10, "Uzun Ürün Adı Burada", "5,00")},
		{"turkish runes count as one column each", 12, "çşğ", "1,00", padExpected(12, "çşğ", "1,00")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Columns(tt.width, tt.left, tt.right)
			if len([]rune(got)) != tt.width {
				t.Fatalf("Columns(%d,%q,%q) = %q (len %d runes), want exactly %d runes", tt.width, tt.left, tt.right, got, len([]rune(got)), tt.width)
			}
			if got != tt.want {
				t.Fatalf("Columns(%d,%q,%q) = %q, want %q", tt.width, tt.left, tt.right, got, tt.want)
			}
		})
	}
}

// padExpected is a small reference implementation used only to compute this
// test's expected values, deliberately written differently (rune-slice
// arithmetic) from Columns' own implementation so the test isn't just
// re-asserting the same code against itself.
func padExpected(width int, left, right string) string {
	l := []rune(left)
	r := []rune(right)
	maxLeft := width - len(r) - 1
	if maxLeft < 0 {
		maxLeft = 0
	}
	if len(l) > maxLeft {
		l = l[:maxLeft]
	}
	pad := width - len(l) - len(r)
	if pad < 0 {
		pad = 0
	}
	out := make([]rune, 0, width)
	out = append(out, l...)
	for i := 0; i < pad; i++ {
		out = append(out, ' ')
	}
	out = append(out, r...)
	return string(out)
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"shorter than n", "Çay", 10, "Çay"},
		{"exact n", "Çay", 3, "Çay"},
		{"longer, ascii", "Uzun Ürün Adı", 4, "Uzun"},
		{"longer, counts runes not bytes", "Çorba İskender", 6, "Çorba "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.in, tt.n)
			if got != tt.want {
				t.Fatalf("Truncate(%q,%d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
			if len([]rune(got)) > tt.n {
				t.Fatalf("Truncate(%q,%d) result has %d runes, want <= %d", tt.in, tt.n, len([]rune(got)), tt.n)
			}
		})
	}
}
