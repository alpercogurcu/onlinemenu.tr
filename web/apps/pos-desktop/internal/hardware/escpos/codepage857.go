package escpos

// cp857Encode maps a Unicode code point (rune) to its single-byte
// representation in IBM/DOS Code Page 857 ("DOS Turkish"), the code page
// this package selects on the printer via SetCodePage (see commands.go's
// codePagePC857 constant).
//
// Source of truth: this table was transcribed directly from the Unicode
// Consortium's authoritative vendor mapping file
//
//	https://www.unicode.org/Public/MAPPINGS/VENDORS/MICSFT/PC/CP857.TXT
//	(cp857_DOSTurkish to Unicode table, table version 2.00, 1996-04-24,
//	 Microsoft/Unicode contact Shawn.Steele@microsoft.com)
//
// fetched and parsed mechanically (byte -> Unicode column swapped to build
// this reverse rune -> byte map) rather than typed from memory, specifically
// so this table and any test asserting against it are not both derived from
// the same possibly-wrong recollection (a hand-typed table checked against
// itself proves nothing). Only the codes >= 0x80 are listed here; 0x00-0x7F
// is a plain ASCII passthrough in CP857 (verified against the same source:
// every entry below 0x80 maps to the identical code point), handled directly
// by EncodeCP857 below without a table lookup.
//
// The three codes the source table marks "#UNDEFINED" for this page
// (0xD5, 0xE7, 0xF2) are intentionally absent — encoding never produces
// them.
var cp857Encode = map[rune]byte{
	0x00c7: 0x80, // 'Ç' LATIN CAPITAL LETTER C WITH CEDILLA
	0x00fc: 0x81, // 'ü' LATIN SMALL LETTER U WITH DIAERESIS
	0x00e9: 0x82, // 'é' LATIN SMALL LETTER E WITH ACUTE
	0x00e2: 0x83, // 'â' LATIN SMALL LETTER A WITH CIRCUMFLEX
	0x00e4: 0x84, // 'ä' LATIN SMALL LETTER A WITH DIAERESIS
	0x00e0: 0x85, // 'à' LATIN SMALL LETTER A WITH GRAVE
	0x00e5: 0x86, // 'å' LATIN SMALL LETTER A WITH RING ABOVE
	0x00e7: 0x87, // 'ç' LATIN SMALL LETTER C WITH CEDILLA
	0x00ea: 0x88, // 'ê' LATIN SMALL LETTER E WITH CIRCUMFLEX
	0x00eb: 0x89, // 'ë' LATIN SMALL LETTER E WITH DIAERESIS
	0x00e8: 0x8a, // 'è' LATIN SMALL LETTER E WITH GRAVE
	0x00ef: 0x8b, // 'ï' LATIN SMALL LETTER I WITH DIAERESIS
	0x00ee: 0x8c, // 'î' LATIN SMALL LETTER I WITH CIRCUMFLEX
	0x0131: 0x8d, // 'ı' LATIN SMALL LETTER DOTLESS I (Turkish)
	0x00c4: 0x8e, // 'Ä' LATIN CAPITAL LETTER A WITH DIAERESIS
	0x00c5: 0x8f, // 'Å' LATIN CAPITAL LETTER A WITH RING ABOVE
	0x00c9: 0x90, // 'É' LATIN CAPITAL LETTER E WITH ACUTE
	0x00e6: 0x91, // 'æ' LATIN SMALL LIGATURE AE
	0x00c6: 0x92, // 'Æ' LATIN CAPITAL LIGATURE AE
	0x00f4: 0x93, // 'ô' LATIN SMALL LETTER O WITH CIRCUMFLEX
	0x00f6: 0x94, // 'ö' LATIN SMALL LETTER O WITH DIAERESIS (Turkish)
	0x00f2: 0x95, // 'ò' LATIN SMALL LETTER O WITH GRAVE
	0x00fb: 0x96, // 'û' LATIN SMALL LETTER U WITH CIRCUMFLEX
	0x00f9: 0x97, // 'ù' LATIN SMALL LETTER U WITH GRAVE
	0x0130: 0x98, // 'İ' LATIN CAPITAL LETTER I WITH DOT ABOVE (Turkish)
	0x00d6: 0x99, // 'Ö' LATIN CAPITAL LETTER O WITH DIAERESIS (Turkish)
	0x00dc: 0x9a, // 'Ü' LATIN CAPITAL LETTER U WITH DIAERESIS (Turkish)
	0x00f8: 0x9b, // 'ø' LATIN SMALL LETTER O WITH STROKE
	0x00a3: 0x9c, // '£' POUND SIGN
	0x00d8: 0x9d, // 'Ø' LATIN CAPITAL LETTER O WITH STROKE
	0x015e: 0x9e, // 'Ş' LATIN CAPITAL LETTER S WITH CEDILLA (Turkish)
	0x015f: 0x9f, // 'ş' LATIN SMALL LETTER S WITH CEDILLA (Turkish)
	0x00e1: 0xa0, // 'á' LATIN SMALL LETTER A WITH ACUTE
	0x00ed: 0xa1, // 'í' LATIN SMALL LETTER I WITH ACUTE
	0x00f3: 0xa2, // 'ó' LATIN SMALL LETTER O WITH ACUTE
	0x00fa: 0xa3, // 'ú' LATIN SMALL LETTER U WITH ACUTE
	0x00f1: 0xa4, // 'ñ' LATIN SMALL LETTER N WITH TILDE
	0x00d1: 0xa5, // 'Ñ' LATIN CAPITAL LETTER N WITH TILDE
	0x011e: 0xa6, // 'Ğ' LATIN CAPITAL LETTER G WITH BREVE (Turkish)
	0x011f: 0xa7, // 'ğ' LATIN SMALL LETTER G WITH BREVE (Turkish)
	0x00bf: 0xa8, // '¿' INVERTED QUESTION MARK
	0x00ae: 0xa9, // '®' REGISTERED SIGN
	0x00ac: 0xaa, // '¬' NOT SIGN
	0x00bd: 0xab, // '½' VULGAR FRACTION ONE HALF
	0x00bc: 0xac, // '¼' VULGAR FRACTION ONE QUARTER
	0x00a1: 0xad, // '¡' INVERTED EXCLAMATION MARK
	0x00ab: 0xae, // '«' LEFT-POINTING DOUBLE ANGLE QUOTATION MARK
	0x00bb: 0xaf, // '»' RIGHT-POINTING DOUBLE ANGLE QUOTATION MARK
	0x2591: 0xb0, // '░' LIGHT SHADE
	0x2592: 0xb1, // '▒' MEDIUM SHADE
	0x2593: 0xb2, // '▓' DARK SHADE
	0x2502: 0xb3, // '│' BOX DRAWINGS LIGHT VERTICAL
	0x2524: 0xb4, // '┤' BOX DRAWINGS LIGHT VERTICAL AND LEFT
	0x00c1: 0xb5, // 'Á' LATIN CAPITAL LETTER A WITH ACUTE
	0x00c2: 0xb6, // 'Â' LATIN CAPITAL LETTER A WITH CIRCUMFLEX
	0x00c0: 0xb7, // 'À' LATIN CAPITAL LETTER A WITH GRAVE
	0x00a9: 0xb8, // '©' COPYRIGHT SIGN
	0x2563: 0xb9, // '╣' BOX DRAWINGS DOUBLE VERTICAL AND LEFT
	0x2551: 0xba, // '║' BOX DRAWINGS DOUBLE VERTICAL
	0x2557: 0xbb, // '╗' BOX DRAWINGS DOUBLE DOWN AND LEFT
	0x255d: 0xbc, // '╝' BOX DRAWINGS DOUBLE UP AND LEFT
	0x00a2: 0xbd, // '¢' CENT SIGN
	0x00a5: 0xbe, // '¥' YEN SIGN
	0x2510: 0xbf, // '┐' BOX DRAWINGS LIGHT DOWN AND LEFT
	0x2514: 0xc0, // '└' BOX DRAWINGS LIGHT UP AND RIGHT
	0x2534: 0xc1, // '┴' BOX DRAWINGS LIGHT UP AND HORIZONTAL
	0x252c: 0xc2, // '┬' BOX DRAWINGS LIGHT DOWN AND HORIZONTAL
	0x251c: 0xc3, // '├' BOX DRAWINGS LIGHT VERTICAL AND RIGHT
	0x2500: 0xc4, // '─' BOX DRAWINGS LIGHT HORIZONTAL
	0x253c: 0xc5, // '┼' BOX DRAWINGS LIGHT VERTICAL AND HORIZONTAL
	0x00e3: 0xc6, // 'ã' LATIN SMALL LETTER A WITH TILDE
	0x00c3: 0xc7, // 'Ã' LATIN CAPITAL LETTER A WITH TILDE
	0x255a: 0xc8, // '╚' BOX DRAWINGS DOUBLE UP AND RIGHT
	0x2554: 0xc9, // '╔' BOX DRAWINGS DOUBLE DOWN AND RIGHT
	0x2569: 0xca, // '╩' BOX DRAWINGS DOUBLE UP AND HORIZONTAL
	0x2566: 0xcb, // '╦' BOX DRAWINGS DOUBLE DOWN AND HORIZONTAL
	0x2560: 0xcc, // '╠' BOX DRAWINGS DOUBLE VERTICAL AND RIGHT
	0x2550: 0xcd, // '═' BOX DRAWINGS DOUBLE HORIZONTAL
	0x256c: 0xce, // '╬' BOX DRAWINGS DOUBLE VERTICAL AND HORIZONTAL
	0x00a4: 0xcf, // '¤' CURRENCY SIGN
	0x00ba: 0xd0, // 'º' MASCULINE ORDINAL INDICATOR
	0x00aa: 0xd1, // 'ª' FEMININE ORDINAL INDICATOR
	0x00ca: 0xd2, // 'Ê' LATIN CAPITAL LETTER E WITH CIRCUMFLEX
	0x00cb: 0xd3, // 'Ë' LATIN CAPITAL LETTER E WITH DIAERESIS
	0x00c8: 0xd4, // 'È' LATIN CAPITAL LETTER E WITH GRAVE
	0x00cd: 0xd6, // 'Í' LATIN CAPITAL LETTER I WITH ACUTE
	0x00ce: 0xd7, // 'Î' LATIN CAPITAL LETTER I WITH CIRCUMFLEX
	0x00cf: 0xd8, // 'Ï' LATIN CAPITAL LETTER I WITH DIAERESIS
	0x2518: 0xd9, // '┘' BOX DRAWINGS LIGHT UP AND LEFT
	0x250c: 0xda, // '┌' BOX DRAWINGS LIGHT DOWN AND RIGHT
	0x2588: 0xdb, // '█' FULL BLOCK
	0x2584: 0xdc, // '▄' LOWER HALF BLOCK
	0x00a6: 0xdd, // '¦' BROKEN BAR
	0x00cc: 0xde, // 'Ì' LATIN CAPITAL LETTER I WITH GRAVE
	0x2580: 0xdf, // '▀' UPPER HALF BLOCK
	0x00d3: 0xe0, // 'Ó' LATIN CAPITAL LETTER O WITH ACUTE
	0x00df: 0xe1, // 'ß' LATIN SMALL LETTER SHARP S
	0x00d4: 0xe2, // 'Ô' LATIN CAPITAL LETTER O WITH CIRCUMFLEX
	0x00d2: 0xe3, // 'Ò' LATIN CAPITAL LETTER O WITH GRAVE
	0x00f5: 0xe4, // 'õ' LATIN SMALL LETTER O WITH TILDE
	0x00d5: 0xe5, // 'Õ' LATIN CAPITAL LETTER O WITH TILDE
	0x00b5: 0xe6, // 'µ' MICRO SIGN
	0x00d7: 0xe8, // '×' MULTIPLICATION SIGN
	0x00da: 0xe9, // 'Ú' LATIN CAPITAL LETTER U WITH ACUTE
	0x00db: 0xea, // 'Û' LATIN CAPITAL LETTER U WITH CIRCUMFLEX
	0x00d9: 0xeb, // 'Ù' LATIN CAPITAL LETTER U WITH GRAVE
	0x00ec: 0xec, // 'ì' LATIN SMALL LETTER I WITH GRAVE
	0x00ff: 0xed, // 'ÿ' LATIN SMALL LETTER Y WITH DIAERESIS
	0x00af: 0xee, // '¯' MACRON
	0x00b4: 0xef, // '´' ACUTE ACCENT
	0x00ad: 0xf0, // SOFT HYPHEN
	0x00b1: 0xf1, // '±' PLUS-MINUS SIGN
	0x00be: 0xf3, // '¾' VULGAR FRACTION THREE QUARTERS
	0x00b6: 0xf4, // '¶' PILCROW SIGN
	0x00a7: 0xf5, // '§' SECTION SIGN
	0x00f7: 0xf6, // '÷' DIVISION SIGN
	0x00b8: 0xf7, // '¸' CEDILLA
	0x00b0: 0xf8, // '°' DEGREE SIGN
	0x00a8: 0xf9, // '¨' DIAERESIS
	0x00b7: 0xfa, // '·' MIDDLE DOT
	0x00b9: 0xfb, // '¹' SUPERSCRIPT ONE
	0x00b3: 0xfc, // '³' SUPERSCRIPT THREE
	0x00b2: 0xfd, // '²' SUPERSCRIPT TWO
	0x25a0: 0xfe, // '■' BLACK SQUARE
	0x00a0: 0xff, // NO-BREAK SPACE
}

// cp857Fallback is written for any rune with no CP857 representation
// (notably '₺' U+20BA, the Turkish lira sign — CP857 predates it, this
// table's source file has no entry for it) instead of silently dropping the
// character or panicking (lessons-from-b2b §5: a fault must never be
// swallowed). Receipt money formatting (internal/receipt) sidesteps this by
// spelling the currency as the literal ASCII "TL" rather than the ₺ glyph,
// so this fallback path is expected to be rare in practice (only reachable
// from free-text fields like a product name or check note containing a
// character truly outside CP857's repertoire).
const cp857Fallback = '?'

// EncodeCP857 converts a UTF-8 Go string to CP857 bytes for the printer.
// ASCII (runes < 0x80) passes through unchanged — CP857, like every OEM DOS
// code page, is an ASCII superset for its low 128 codes (verified against
// the same source file this package's table was built from). Every other
// rune is looked up in cp857Encode; a rune with no CP857 representation
// becomes cp857Fallback rather than being dropped, matching this package's
// no-silent-failure requirement.
func EncodeCP857(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		default:
			if b, ok := cp857Encode[r]; ok {
				out = append(out, b)
			} else {
				out = append(out, cp857Fallback)
			}
		}
	}
	return out
}
