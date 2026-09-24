package pdfdoc

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/fontkit/notofixture"
	"github.com/brf-tech/filex-sign/internal/geometry"
)

// withNoto wires the pinned Noto faces (notofixture) and skips when the one
// sample needs cannot be had here — unless FILEX_SIGN_REQUIRE_FONTS says it
// must.
func withNoto(t *testing.T, sample string) {
	t.Helper()
	fontkit.SetFetcher(func(n fontkit.NotoFont) ([]byte, error) { return notofixture.Fetch(n.URL, n.SHA256) })
	t.Cleanup(func() { fontkit.SetFetcher(nil) })
	fontkit.BeginCall()
	if m := fontkit.MissingIn(fontkit.Default(), sample); len(m) > 0 {
		if notofixture.Required() {
			t.Fatalf("%q cannot be drawn here: %+v", sample, m)
		}
		t.Skipf("the face for %q is not reachable here", sample)
	}
}

// ⚠ A Devanagari syllable whose vowel sign is drawn BEFORE its consonants
// ([ि, त्र] for "त्रि") has no per-glyph reading: the ToUnicode map could only
// say "त्रि" for the first glyph and nothing for the second, and pdftotext
// gave "क्षत्रि य हस्ता क्षर" back (2026-09-21). The cluster carries its text
// as ActualText; single-glyph clusters — all of a Latin line — do not.
func TestStampComplexClustersCarryTheirText(t *testing.T) {
	const word = "क्षत्रिय"
	withNoto(t, word)
	out, err := Stamp(twoPages(), []Item{{Page: 1, Kind: KindText, Text: word, Font: fontkit.Inter,
		Rect: geometry.Rect{X: 72, Y: 120, W: 300, H: 40}}})
	if err != nil {
		t.Fatal(err)
	}
	c := contentOf(t, out, 1)
	if n := strings.Count(c, "/ActualText <FEFF"); n == 0 {
		t.Fatalf("no cluster carries its text:\n%s", c)
	}
	if strings.Count(c, " BDC\n") != strings.Count(c, "EMC\n") {
		t.Errorf("unbalanced marked content:\n%s", c)
	}
	if !strings.Contains(c, "/ActualText <FEFF"+utf16BEText("त्रि")+">") {
		t.Errorf("the syllable त्रि is not given back whole:\n%s", c)
	}

	latin, err := Stamp(twoPages(), []Item{box(1)})
	if err != nil {
		t.Fatal(err)
	}
	if c := contentOf(t, latin, 1); strings.Contains(c, "ActualText") {
		t.Errorf("a Latin line needs no ActualText:\n%s", c)
	}
}
