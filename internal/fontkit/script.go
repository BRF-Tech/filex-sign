package fontkit

import (
	"sort"
	"unicode"
)

// ── Which script a character belongs to ─────────────────────────────────
//
// Go's own Unicode tables (unicode.Scripts) — the same names the generated
// table is keyed by, so a script can never be spelled two ways.

type scriptRange struct {
	lo, hi rune
	name   string
}

var scriptRanges = func() []scriptRange {
	var out []scriptRange
	for name, t := range unicode.Scripts {
		for _, r := range t.R16 {
			out = appendRange(out, rune(r.Lo), rune(r.Hi), rune(r.Stride), name)
		}
		for _, r := range t.R32 {
			out = appendRange(out, rune(r.Lo), rune(r.Hi), rune(r.Stride), name)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].lo < out[j].lo })
	return out
}()

func appendRange(out []scriptRange, lo, hi, stride rune, name string) []scriptRange {
	if stride == 1 {
		return append(out, scriptRange{lo, hi, name})
	}
	for r := lo; r <= hi; r += stride {
		out = append(out, scriptRange{r, r, name})
	}
	return out
}

// ScriptOf is the Unicode script of r ("Latin", "Arabic", "Han", "Common"…);
// "Unknown" for an unassigned code point.
func ScriptOf(r rune) string {
	i := sort.Search(len(scriptRanges), func(i int) bool { return scriptRanges[i].hi >= r })
	if i < len(scriptRanges) && scriptRanges[i].lo <= r {
		return scriptRanges[i].name
	}
	return "Unknown"
}

// weak scripts take the script of the text around them.
func weak(script string) bool {
	return script == "Common" || script == "Inherited" || script == "Unknown"
}

// hanKey is the table key a string's Han characters are drawn with: the
// regional face its OTHER characters ask for. Kana make it Japanese, hangul
// Korean, bopomofo Traditional Chinese; otherwise Simplified Chinese.
func hanKey(rs []rune) string {
	for _, r := range rs {
		switch ScriptOf(r) {
		case "Hiragana", "Katakana":
			return "Han/ja"
		case "Hangul":
			return "Han/ko"
		case "Bopomofo":
			return "Han/zh-Hant"
		}
	}
	return "Han"
}
