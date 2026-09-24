package fontkit

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode"

	"github.com/brf-tech/filex-sign/internal/fontkit/notofixture"
)

// ── the pinned table ───────────────────────────────────────────────────

// Every script the table maps resolves to a face that is pinned: an https
// URL on the one host the manifest grants, a sha256, a size the host will
// accept. And every Unicode script is accounted for — drawn, or declared
// undrawable — so no script falls between the two.
func TestTable_EveryScriptResolvesToAPinnedFace(t *testing.T) {
	for script, fams := range notoScripts {
		if len(fams) == 0 {
			t.Errorf("%s maps to no face", script)
		}
		for _, fam := range fams {
			f, ok := notoFonts[fam]
			if !ok {
				t.Errorf("%s: %q is not pinned", script, fam)
				continue
			}
			if !strings.HasPrefix(f.URL, "https://fonts.gstatic.com/") || !strings.HasSuffix(f.URL, ".ttf") {
				t.Errorf("%s: %q is not a gstatic TrueType URL: %s", script, fam, f.URL)
			}
			if len(f.SHA256) != 64 || strings.Trim(f.SHA256, "0123456789abcdef") != "" {
				t.Errorf("%s: %q has no pinned sha256", script, fam)
			}
			if f.Size <= 0 || f.Size > 32<<20 {
				t.Errorf("%s: %q is %d bytes — outside what asset_fetch takes", script, fam, f.Size)
			}
		}
	}
	for name := range unicode.Scripts {
		_, drawn := notoScripts[name]
		if !drawn && !Unsupported(name) {
			t.Errorf("script %s is neither drawn nor declared undrawable", name)
		}
	}
	for _, key := range []string{"Han", "Han/ja", "Han/ko", "Han/zh-Hant", "Arabic", "Hebrew", "Devanagari", "Thai", "Common"} {
		if len(notoScripts[key]) == 0 {
			t.Errorf("the table has nothing for %s", key)
		}
	}
}

// ⚠ Opt-in — FILEX_SIGN_VERIFY_ALL_FONTS=1, about 62 MiB the first time:
// every pinned face is downloaded and checked against its hash (the file
// gstatic serves today is still the one pinned), and every script the table
// maps has letters its own faces draw — through the same path a stamp
// takes. Run before a release and whenever scripts/notogen regenerates the
// table.
func TestTable_EveryPinHoldsAndEveryScriptDraws(t *testing.T) {
	if os.Getenv("FILEX_SIGN_VERIFY_ALL_FONTS") == "" {
		t.Skip("set FILEX_SIGN_VERIFY_ALL_FONTS=1 to download and check every pinned face")
	}
	forgetFaces(t)
	SetFetcher(testFetcher(t))
	t.Cleanup(func() { SetFetcher(nil) })
	verified := 0
	for fam := range notoFonts {
		if _, err := notoFace(fam); err != nil {
			t.Errorf("%s: %v", fam, err)
			continue
		}
		verified++
	}
	checked := 0
	for key, fams := range notoScripts {
		base, _, regional := strings.Cut(key, "/")
		table := unicode.Scripts[base]
		if key == "Common" || table == nil {
			continue
		}
		var sample rune = -1
		var by string
	search:
		for _, rg := range append(r16(table), r32(table)...) {
			for r := rg[0]; r <= rg[1]; r += rg[2] {
				for _, fam := range fams {
					if face, err := notoFace(fam); err == nil {
						if _, ok := face.glyph(r); ok {
							sample, by = r, fam
							break search
						}
					}
				}
			}
		}
		if sample < 0 {
			t.Errorf("%s: none of %v draws a letter of it", key, fams)
			continue
		}
		checked++
		if regional {
			continue // Han/ja and the like: LayoutLine picks them by the kana or Hangul around
		}
		line, err := LayoutLine(Default(), string(sample))
		if err != nil || len(line.Missing) != 0 || len(line.Runs) != 1 {
			t.Errorf("%s: %U through LayoutLine: %v %+v", key, sample, err, line.Missing)
			continue
		}
		// A bundled face may draw it first (Inter has Latin, Greek and
		// Cyrillic); a FETCHED face must be one of the script's own.
		if f := line.Runs[0].Face; f.Fetched && f.Family != by && !contains(fams, f.Family) {
			t.Errorf("%s: %U drawn by %s, not one of its own faces %v", key, sample, f.Family, fams)
		}
	}
	t.Logf("%d of %d faces verified against their pins, %d scripts drawn", verified, len(notoFonts), checked)
}

func r16(t *unicode.RangeTable) [][3]rune {
	var out [][3]rune
	for _, r := range t.R16 {
		out = append(out, [3]rune{rune(r.Lo), rune(r.Hi), rune(r.Stride)})
	}
	return out
}

func r32(t *unicode.RangeTable) [][3]rune {
	var out [][3]rune
	for _, r := range t.R32 {
		out = append(out, [3]rune{rune(r.Lo), rune(r.Hi), rune(r.Stride)})
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ── fonts for the tests: the pinned files themselves ────────────────────

// testFetcher reads a pinned face through notofixture (a cache in the
// temporary directory, downloaded once through the same pin).
func testFetcher(t *testing.T) Fetcher {
	t.Helper()
	return func(n NotoFont) ([]byte, error) { return notofixture.Fetch(n.URL, n.SHA256) }
}

// withNoto wires the test fetcher, and skips when a face the test needs
// cannot be had (no network) — unless FILEX_SIGN_REQUIRE_FONTS says it must.
func withNoto(t *testing.T, families ...string) {
	t.Helper()
	SetFetcher(testFetcher(t))
	t.Cleanup(func() { SetFetcher(nil) })
	for _, fam := range families {
		if _, err := notoFace(fam); err != nil {
			if notofixture.Required() {
				t.Fatalf("%s: %v", fam, err)
			}
			t.Skipf("%s is not reachable here (%v)", fam, err)
		}
	}
}

func gids(l Line) []uint16 {
	var out []uint16
	for _, r := range l.Runs {
		for _, g := range r.Glyphs {
			out = append(out, g.GID)
		}
	}
	return out
}

// ── layout ─────────────────────────────────────────────────────────────

// ⚠ Arabic letters take their JOINED forms and the word runs right to left:
// the isolated forms a character map gives, laid left to right, are the
// disconnected letters the owner said must not be printed.
func TestLayout_ArabicJoinsAndRunsRightToLeft(t *testing.T) {
	withNoto(t, "Noto Sans Arabic")
	line, err := LayoutLine(Default(), "سلام")
	if err != nil {
		t.Fatal(err)
	}
	if !line.RTL || len(line.Missing) != 0 || len(line.Runs) != 1 || !line.Runs[0].Shaped {
		t.Fatalf("one shaped right-to-left run expected: %+v", line)
	}
	face, _ := notoFace("Noto Sans Arabic")
	var isolated []uint16
	for _, r := range "سلام" {
		g, _ := face.glyph(r)
		isolated = append(isolated, g.GID)
	}
	got := gids(line)
	if len(got) != len(isolated) {
		t.Fatalf("glyphs %v, isolated %v", got, isolated)
	}
	same := 0
	for i := range got {
		// Visual order is the reverse of the logical one.
		if got[i] == isolated[len(isolated)-1-i] {
			same++
		}
	}
	if same == len(got) {
		t.Errorf("every letter kept its isolated form: nothing was joined (%v)", got)
	}
	// The glyph drawn first (leftmost) is the LAST letter of the word.
	if line.Runs[0].Glyphs[0].Rune != 'م' {
		t.Errorf("the leftmost glyph should be the word's last letter, got %q", line.Runs[0].Glyphs[0].Rune)
	}
}

// A line mixing directions is drawn in visual order: Latin left to right,
// the Hebrew word reversed in place, its number left to right again.
func TestLayout_MixedDirectionsInVisualOrder(t *testing.T) {
	withNoto(t, "Noto Sans Hebrew")
	line, err := LayoutLine(Default(), "Ref שלום 42")
	if err != nil {
		t.Fatal(err)
	}
	if line.RTL {
		t.Error("the paragraph starts with a Latin letter: it reads left to right")
	}
	var order []string
	for _, r := range line.Runs {
		var b strings.Builder
		for _, g := range r.Glyphs {
			b.WriteRune(g.Rune)
		}
		order = append(order, b.String())
	}
	joined := strings.Join(order, "|")
	if !strings.HasPrefix(joined, "Ref") || !strings.Contains(joined, "םולש") {
		t.Errorf("visual order %q: expected Latin first and the Hebrew word reversed", joined)
	}
}

// Indic conjuncts form: fewer glyphs than characters, and none missing.
func TestLayout_DevanagariFormsConjuncts(t *testing.T) {
	withNoto(t, "Noto Sans Devanagari")
	s := "क्षत्रिय"
	line, err := LayoutLine(Default(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(line.Missing) != 0 {
		t.Fatalf("missing: %+v", line.Missing)
	}
	if n := len(gids(line)); n >= len([]rune(s)) {
		t.Errorf("%d glyphs for %d characters: the conjuncts did not form", n, len([]rune(s)))
	}
}

// Japanese: kana and kanji in the Japanese face, Latin in the chosen one.
func TestLayout_JapaneseInTheJapaneseFace(t *testing.T) {
	withNoto(t, "Noto Sans JP")
	line, err := LayoutLine(Default(), "署名 OK です")
	if err != nil {
		t.Fatal(err)
	}
	if len(line.Missing) != 0 {
		t.Fatalf("missing: %+v", line.Missing)
	}
	for _, r := range line.Runs {
		if r.Face.Family == "Noto Sans SC" {
			t.Error("kanji beside kana are Japanese, not Simplified Chinese")
		}
	}
}

// ⚠⚠ Offline: the Latin part still prints; the rest is REPORTED, with the
// reason, never dropped in silence.
func TestLayout_OfflineReportsWhatCannotBePrinted(t *testing.T) {
	forgetFaces(t)
	SetFetcher(func(NotoFont) ([]byte, error) { return nil, errors.New("unavailable: no network") })
	t.Cleanup(func() { SetFetcher(nil) })
	line, err := LayoutLine(Default(), "Ali سلام")
	if err != nil {
		t.Fatal(err)
	}
	if len(line.Runs) == 0 || line.Runs[0].Glyphs[0].Rune != 'A' {
		t.Fatalf("the Latin part must still print: %+v", line.Runs)
	}
	if len(line.Missing) != 4 {
		t.Fatalf("four letters cannot print, got %+v", line.Missing)
	}
	for _, m := range line.Missing {
		if m.Reason != Unavailable || m.Script != "Arabic" {
			t.Errorf("missing %+v: want Arabic, unavailable", m)
		}
	}
	if Covers(Default(), "سلام") {
		t.Error("Covers must say no while the face cannot be had")
	}
}

// Printed is the line as the paper will carry it: what cannot be drawn is
// taken out at its own place, and only there — the screens show this, so
// the preview a person approves is what gets stamped.
func TestPrinted_IsTheLineWithoutWhatCannotPrint(t *testing.T) {
	forgetFaces(t)
	SetFetcher(func(NotoFont) ([]byte, error) { return nil, errors.New("unavailable: no network") })
	t.Cleanup(func() { SetFetcher(nil) })
	got, missing := Printed(Default(), "Ali سلام 42")
	if got != "Ali  42" {
		t.Errorf("printed %q, want %q", got, "Ali  42")
	}
	if len(missing) != 4 || missing[0].At != 4 || missing[3].At != 7 {
		t.Errorf("missing %+v", missing)
	}
	if got, missing := Printed(Default(), "Şükrü"); got != "Şükrü" || missing != nil {
		t.Errorf("Latin is printed as typed: %q %+v", got, missing)
	}
}

// ⚠ A face the host is still downloading is "wait a moment", not "check
// the network": a person typing Japanese on a slow line would otherwise be
// sent looking for a fault that is not there.
func TestLayout_AFaceStillOnItsWay(t *testing.T) {
	forgetFaces(t)
	SetFetcher(func(NotoFont) ([]byte, error) {
		return nil, fmt.Errorf("timeout: still downloading: %w", ErrStillDownloading)
	})
	t.Cleanup(func() { SetFetcher(nil) })
	for _, m := range MissingIn(Default(), "署名") {
		if m.Reason != Downloading {
			t.Errorf("%+v: want downloading", m)
		}
	}
}

// A script no Noto face draws is reported as such — not as a network error.
func TestLayout_AScriptWithNoFont(t *testing.T) {
	forgetFaces(t)
	SetFetcher(func(NotoFont) ([]byte, error) { return nil, errors.New("must not be asked") })
	t.Cleanup(func() { SetFetcher(nil) })
	if len(notoNone) == 0 {
		t.Skip("every script has a face")
	}
	table := unicode.Scripts[notoNone[0]]
	var r rune
	if len(table.R16) > 0 {
		r = rune(table.R16[0].Lo)
	} else {
		r = rune(table.R32[0].Lo)
	}
	m := MissingIn(Default(), string(r))
	if len(m) != 1 || m[0].Reason != NoFont {
		t.Errorf("%U (%s): %+v", r, notoNone[0], m)
	}
}

// The common case never touches the network.
func TestLayout_LatinAndTurkishNeedNoFetch(t *testing.T) {
	SetFetcher(func(NotoFont) ([]byte, error) { t.Fatal("fetched for Latin text"); return nil, nil })
	t.Cleanup(func() { SetFetcher(nil) })
	line, err := LayoutLine(Get(HomemadeApple), "Şükrü Öztürk, İğdır — 12.09.2026")
	if err != nil || len(line.Missing) != 0 {
		t.Fatalf("%v %+v", err, line.Missing)
	}
}

// forgetFaces drops the faces loaded by earlier tests, so a test can ask
// what happens when a face has never been fetched.
func forgetFaces(t *testing.T) {
	t.Helper()
	notoMu.Lock()
	notoFaces = map[string]*Face{}
	notoMu.Unlock()
}
