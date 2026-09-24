package fields

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// ── A box's identity ────────────────────────────────────────────────────
//
// A box has a NAME and an IDENTITY, and they are different things (the
// owner, 2026-09-21: "ASCII kimlik üretsin ama göstermesin hiç — adam
// isterse Japonca yazsın alanı; isim ayrı, kimlik ayrı olsun").
//
//	name      what people read — the caption, the audit trail, the PDF
//	          tooltip (/TU). Any script, exactly as typed.
//	identity  the PDF form field's name (/T), so an exported form reads
//	          `musteri-adi` rather than `f_x7k2`. ASCII, derived, never
//	          shown to anybody.
//
// Slug is the derivation; envelope.AssignKeys makes the result unique
// in the document and decides when it stops following the name.

// MaxSlug bounds an identity: a PDF field name is not a sentence.
const MaxSlug = 40

// fold is what NFD cannot take apart: letters that are not a base letter
// plus a mark, so stripping marks would lose them instead of folding them.
// ⚠ ı (dotless i) is the one that matters most here — it is not "i with a
// mark removed", it is its own letter, and without this line "Kayıt"
// became "kayt".
var fold = map[rune]string{
	'ı': "i", 'İ': "I", 'ł': "l", 'Ł': "L", 'ø': "o", 'Ø': "O", 'đ': "d", 'Đ': "D",
	'ß': "ss", 'æ': "ae", 'Æ': "AE", 'œ': "oe", 'Œ': "OE", 'þ': "th", 'Þ': "Th",
	'ð': "d", 'Ð': "D", 'ħ': "h", 'Ħ': "H", 'ŀ': "l", 'Ŀ': "L", 'ŋ': "n", 'Ŋ': "N",
}

// Slug turns a box's name into its identity: nearest ASCII (ş→s, ğ→g,
// ı→i, İ→i, ö→o, ü→u, ç→c, é→e …), lower case, every run of anything
// else — spaces included — one '-', no '-' at either end. A name with
// nothing that folds to ASCII (Japanese, emoji) answers "": the caller
// falls back to something stable (AssignKeys uses the box's type).
func Slug(name string) string {
	var b strings.Builder
	dash := false
	emit := func(s string) {
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				b.WriteRune(r)
				dash = false
			case r >= 'A' && r <= 'Z':
				b.WriteRune(r + ('a' - 'A'))
				dash = false
			default:
				if !dash && b.Len() > 0 {
					b.WriteByte('-')
					dash = true
				}
			}
		}
	}
	for _, r := range name {
		if f, ok := fold[r]; ok {
			emit(f)
			continue
		}
		if r < 0x80 {
			emit(string(r))
			continue
		}
		// Take the letter apart and keep its base: é → e + ́ → e.
		var base strings.Builder
		for _, d := range norm.NFD.String(string(r)) {
			if unicode.Is(unicode.Mn, d) {
				continue
			}
			base.WriteRune(d)
		}
		emit(base.String())
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > MaxSlug {
		out = strings.Trim(out[:MaxSlug], "-")
	}
	return out
}
