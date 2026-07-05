// Package escpos is a minimal, dependency-free ESC/POS command encoder for
// network thermal receipt printers (see hardware.NetworkPrinter). It only
// implements the narrow command subset an "informational receipt" (see
// internal/receipt) needs: init, Turkish (CP857) text, alignment,
// bold/double-size emphasis, line feeds and paper cut. It is not a general
// ESC/POS library — no images, no barcodes, no QR codes.
//
// Every command byte sequence below is a standard ESC/POS command shared
// across virtually all Epson-compatible thermal printers (verified against
// mike42/escpos-php's Printer.php constants: JUSTIFY_LEFT/CENTER/RIGHT = 0/1/2,
// MODE_EMPHASIZED/DOUBLE_HEIGHT/DOUBLE_WIDTH = 8/16/32, CUT_FULL/PARTIAL =
// 65/66 as the "m" byte of GS V m n) — these do not vary by printer model.
// The one command that IS printer/firmware-dependent is the code-page
// selector's data byte (see codePagePC857's doc comment) — flagged there,
// not guessed silently.
package escpos

import "bytes"

// Width is the printer's paper width in character columns at the default
// (non-double-size) font, the two sizes this task supports.
type Width int

const (
	Width32 Width = 32
	Width48 Width = 48
)

// Align selects horizontal text justification (ESC a n).
type Align byte

const (
	AlignLeft   Align = 0
	AlignCenter Align = 1
	AlignRight  Align = 2
)

// CutMode selects GS V's cut style. Full cut severs the paper completely;
// partial cut leaves a small connecting point so the strip can be torn by
// hand — the gentler default for auto-cutters (see Builder.Cut).
type CutMode byte

const (
	CutFull    CutMode = 'A' // 0x41 — mike42/escpos-php's CUT_FULL
	CutPartial CutMode = 'B' // 0x42 — mike42/escpos-php's CUT_PARTIAL
)

const (
	esc byte = 0x1b
	gs  byte = 0x1d

	// codePagePC857 is the "n" data byte of ESC t n (select character code
	// table) for CP857 (DOS Turkish). This value is NOT standardized across
	// ESC/POS printer firmwares — it varies per vendor/model (surveyed
	// values from mike42/escpos-php's capabilities.json: 8, 12, 13, 29, 61
	// depending on printer profile). 13 is used here because it is both
	// that project's "default" profile value AND its value for the Epson
	// TM-T88V — the de facto reference implementation most generic
	// ESC/POS-over-TCP-9100 thermal printers (the class this task targets)
	// emulate. If a deployed printer model prints garbled Turkish
	// characters despite EncodeCP857 being correct, this hardcoded value —
	// not the encoder — is the first thing to check against that printer's
	// own command reference. There is currently no runtime override for it
	// (this package has no per-station config plumbed in yet); if a real
	// deployment needs a different code-page selector, add one here rather
	// than guessing a second hardcoded value.
	codePagePC857 byte = 13

	modeBold         byte = 1 << 3 // ESC ! bit 3 — MODE_EMPHASIZED
	modeDoubleHeight byte = 1 << 4 // ESC ! bit 4 — MODE_DOUBLE_HEIGHT
	modeDoubleWidth  byte = 1 << 5 // ESC ! bit 5 — MODE_DOUBLE_WIDTH
)

// Builder accumulates an ESC/POS byte stream for one receipt job. It is not
// safe for concurrent use — build one job per Print call.
type Builder struct {
	width Width
	buf   bytes.Buffer
}

// NewBuilder starts a new job for the given paper width. Callers should
// call Init first (see Init's doc comment) before any text/formatting
// command.
func NewBuilder(width Width) *Builder {
	return &Builder{width: width}
}

// Width reports the paper width (in columns) this builder was constructed
// with — internal/receipt uses this to lay out columns.
func (b *Builder) Width() int { return int(b.width) }

// Init resets the printer to its power-on defaults (ESC @) and selects the
// CP857 (Turkish) code page (ESC t n) — every job must start with this so a
// previous job's alignment/emphasis/code-page state never bleeds into the
// next one.
func (b *Builder) Init() *Builder {
	b.buf.WriteByte(esc)
	b.buf.WriteByte('@')
	b.buf.WriteByte(esc)
	b.buf.WriteByte('t')
	b.buf.WriteByte(codePagePC857)
	return b
}

// Align sets horizontal justification for subsequently written lines
// (ESC a n).
func (b *Builder) Align(a Align) *Builder {
	b.buf.WriteByte(esc)
	b.buf.WriteByte('a')
	b.buf.WriteByte(byte(a))
	return b
}

// SetMode selects bold and/or double-size (both width and height scale
// together on real ESC/POS hardware; there is no independent single-axis
// double-size bit in the standard command set) via ESC ! n — a single
// print-mode byte rather than separate bold/double toggles, matching how
// real firmware implements it.
func (b *Builder) SetMode(bold, double bool) *Builder {
	var mode byte
	if bold {
		mode |= modeBold
	}
	if double {
		mode |= modeDoubleHeight | modeDoubleWidth
	}
	b.buf.WriteByte(esc)
	b.buf.WriteByte('!')
	b.buf.WriteByte(mode)
	return b
}

// Text encodes s as CP857 and writes it with no trailing line feed.
func (b *Builder) Text(s string) *Builder {
	b.buf.Write(EncodeCP857(s))
	return b
}

// Line encodes s as CP857 and writes it followed by a line feed.
func (b *Builder) Line(s string) *Builder {
	b.Text(s)
	b.buf.WriteByte('\n')
	return b
}

// Feed advances the paper n lines (ESC d n) without printing anything.
func (b *Builder) Feed(n int) *Builder {
	if n <= 0 {
		return b
	}
	b.buf.WriteByte(esc)
	b.buf.WriteByte('d')
	b.buf.WriteByte(byte(n))
	return b
}

// Divider prints a full-width dashed rule — the visual line between the
// item list and the totals block on a real thermal receipt.
func (b *Builder) Divider() *Builder {
	return b.Line(dividerRule(int(b.width)))
}

// Cut feeds feedLines lines and then cuts the paper (GS V m n). See CutMode
// for the two cut styles.
func (b *Builder) Cut(mode CutMode, feedLines int) *Builder {
	b.buf.WriteByte(gs)
	b.buf.WriteByte('V')
	b.buf.WriteByte(byte(mode))
	b.buf.WriteByte(byte(feedLines))
	return b
}

// Bytes returns the accumulated job. Call this once, after the job's final
// Cut.
func (b *Builder) Bytes() []byte {
	return b.buf.Bytes()
}

func dividerRule(width int) string {
	r := make([]byte, width)
	for i := range r {
		r[i] = '-'
	}
	return string(r)
}

// Columns lays out left and right within width printer columns: left is
// left-aligned, right is right-aligned, with at least one space between
// them. Both sides are measured and truncated/padded in RUNES, not UTF-8
// bytes — a Turkish letter like 'ç' or 'ş' is 2 bytes but must occupy
// exactly 1 printer column, so byte-length padding would misalign every
// line containing one (see this package's tests). If left alone would leave
// no room for right plus the separating space, left is truncated to fit.
func Columns(width int, left, right string) string {
	leftRunes := []rune(left)
	rightRunes := []rune(right)

	maxLeft := width - len(rightRunes) - 1
	if maxLeft < 0 {
		maxLeft = 0
	}
	if len(leftRunes) > maxLeft {
		leftRunes = leftRunes[:maxLeft]
	}

	padding := width - len(leftRunes) - len(rightRunes)
	if padding < 0 {
		padding = 0
	}

	out := make([]rune, 0, width)
	out = append(out, leftRunes...)
	for i := 0; i < padding; i++ {
		out = append(out, ' ')
	}
	out = append(out, rightRunes...)
	return string(out)
}

// Truncate shortens s to at most n runes (not bytes) — used to keep a long
// product name from wrapping past the printer's column width.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
