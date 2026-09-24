package pdfdoc

import (
	"bytes"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/digitorus/pdf"

	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/geometry"
	"github.com/brf-tech/filex-sign/internal/testpdf"
)

// The Turkish repertoire the subsets exist for: a stamp that loses these
// is worse than no stamp at all.
const turkish = "Şükrü Çağlayan İğneada 12.09.2026 — ödül"

func twoPages() []byte {
	return testpdf.Build(testpdf.Options{Pages: []testpdf.Page{
		testpdf.Letter(),
		{Width: 612, Height: 792, Rotate: 90, Text: "two"},
	}})
}

func box(page int) Item {
	return Item{Page: page, Kind: KindText, Text: turkish, Font: fontkit.Inter,
		Rect: geometry.Rect{X: 72, Y: 120, W: 300, H: 40}}
}

// contentOf concatenates every content stream of a 1-based page.
func contentOf(t *testing.T, b []byte, page int) string {
	t.Helper()
	rdr, err := pdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("re-reading the stamped document: %v", err)
	}
	p := rdr.Page(page)
	if p.V.IsNull() {
		t.Fatalf("page %d vanished", page)
	}
	var out strings.Builder
	c := p.V.Key("Contents")
	read := func(v pdf.Value) {
		r := v.Reader()
		if r == nil {
			return
		}
		defer r.Close()
		data, _ := io.ReadAll(r)
		out.Write(data)
		out.WriteString("\n")
	}
	if c.Kind() == pdf.Array {
		for i := 0; i < c.Len(); i++ {
			read(c.Index(i))
		}
	} else {
		read(c)
	}
	return out.String()
}

func TestStampKeepsTheOriginalBytes(t *testing.T) {
	in := twoPages()
	out, err := Stamp(in, []Item{box(1)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, in) {
		t.Fatal("the stamp rewrote the original bytes instead of appending an update")
	}
	if len(out) <= len(in) {
		t.Fatal("nothing was appended")
	}
	rdr, err := pdf.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("the stamped document does not parse: %v", err)
	}
	if rdr.NumPage() != 2 {
		t.Fatalf("pages: got %d, want 2", rdr.NumPage())
	}
}

func TestStampDrawsTextAndKeepsTheOldContent(t *testing.T) {
	in := twoPages()
	out, err := Stamp(in, []Item{box(1), {Page: 2, Kind: KindCheck, Rect: geometry.Rect{X: 100, Y: 100, W: 20, H: 20}, Rotate: 90}})
	if err != nil {
		t.Fatal(err)
	}
	page1 := contentOf(t, out, 1)
	if !strings.Contains(page1, "(Hello) Tj") {
		t.Error("the page's own content was dropped")
	}
	if !strings.Contains(page1, "/"+ResourcePrefix+"0") || !strings.Contains(page1, "Tj") {
		t.Errorf("no stamped text on page 1:\n%s", page1)
	}
	if !strings.HasPrefix(strings.TrimSpace(page1), "q") {
		t.Error("the document's own content is not wrapped in q … Q")
	}
	page2 := contentOf(t, out, 2)
	if !strings.Contains(page2, " S\n") {
		t.Errorf("no tick stroked on page 2:\n%s", page2)
	}
	// The rotated page's frame turns the stamp upright: 90° means the
	// matrix is (0 1 -1 0), not the identity.
	if !strings.Contains(page2, "0.0000 1.0000 -1.0000 0.0000") {
		t.Errorf("page 2 was not rotated with the page:\n%s", page2)
	}
}

func TestStampEmbedsTheFontWithTurkishGlyphs(t *testing.T) {
	out, err := Stamp(twoPages(), []Item{box(1)})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"/Subtype /Type0", "/Encoding /Identity-H", "/Subtype /CIDFontType2", "/FontFile2", "/ToUnicode"} {
		if !strings.Contains(s, want) {
			t.Errorf("the embedded font is missing %s", want)
		}
	}
	// Every Turkish letter has to have found a glyph, or the stamp would
	// silently drop it.
	face := fontkit.Get(fontkit.Inter)
	glyphs, err := face.Shape(turkish)
	if err != nil {
		t.Fatal(err)
	}
	if len(glyphs) != len([]rune(turkish)) {
		t.Fatalf("glyphs: got %d for %d runes — a character has no glyph in the subset", len(glyphs), len([]rune(turkish)))
	}
	for _, r := range []rune("ışğüöçİŞĞÜÖÇ") {
		gs, _ := face.Shape(string(r))
		if len(gs) != 1 {
			t.Errorf("no glyph for %q", r)
		}
	}
}

func TestStampAllFiveFaces(t *testing.T) {
	for _, f := range fontkit.All() {
		runs, err := fontkit.ShapeRuns(f, turkish)
		if err != nil {
			t.Fatalf("%s: %v", f.ID, err)
		}
		n := 0
		for _, r := range runs {
			n += len(r.Glyphs)
		}
		if n != len([]rune(turkish)) {
			t.Errorf("%s: %d glyphs for %d runes — a Turkish letter was dropped", f.ID, n, len([]rune(turkish)))
		}
		out, err := Stamp(twoPages(), []Item{{Page: 1, Kind: KindText, Text: turkish, Font: f.ID,
			Rect: geometry.Rect{X: 72, Y: 200, W: 300, H: 40}}})
		if err != nil {
			t.Fatalf("%s: %v", f.ID, err)
		}
		if _, err := pdf.NewReader(bytes.NewReader(out), int64(len(out))); err != nil {
			t.Fatalf("%s: the stamped document does not parse: %v", f.ID, err)
		}
	}
}

// Homemade Apple has no Ş, ğ or İ. A Turkish name in it has to come out
// whole anyway, with the missing letters borrowed from another face.
func TestStampBorrowsGlyphsAFaceDoesNotHave(t *testing.T) {
	face := fontkit.Get(fontkit.HomemadeApple)
	if g, _ := face.Shape("Ş"); len(g) != 0 {
		t.Skip("this build of Homemade Apple has Ş; the fallback is exercised elsewhere")
	}
	runs, err := fontkit.ShapeRuns(face, "Şahin")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) < 2 {
		t.Fatalf("expected the line to be split across faces, got %d run(s)", len(runs))
	}
	if runs[0].Face.ID == face.ID {
		t.Errorf("the missing Ş was not borrowed, it came from %s", runs[0].Face.ID)
	}
	out, err := Stamp(twoPages(), []Item{{Page: 1, Kind: KindText, Text: "Şahin", Font: fontkit.HomemadeApple,
		Rect: geometry.Rect{X: 72, Y: 120, W: 200, H: 30}}})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out), "/Subtype /Type0"); n != 2 {
		t.Errorf("both faces should be embedded, found %d Type0 fonts", n)
	}
	// Two runs: each selects its face and is ONE string of glyphs (a run
	// drawn by the character map is not placed glyph by glyph).
	if c := contentOf(t, out, 1); strings.Count(c, " Tf\n") != 2 || strings.Count(c, " Tj\n") != 2 {
		t.Errorf("the line should be drawn in two runs:\n%s", c)
	}
}

func TestStampOnAnXRefStreamDocument(t *testing.T) {
	in := testpdf.Build(testpdf.Options{XRefStream: true, Pages: []testpdf.Page{testpdf.Letter(), {Width: 400, Height: 400, Text: "two"}}})
	out, err := Stamp(in, []Item{box(2)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out[len(in):]), "/Type /XRef") {
		t.Error("an xref-stream document was updated with a classic table")
	}
	rdr, err := pdf.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("the stamped document does not parse: %v", err)
	}
	if rdr.NumPage() != 2 {
		t.Fatalf("pages: got %d, want 2", rdr.NumPage())
	}
	if !strings.Contains(contentOf(t, out, 2), "Tj") {
		t.Error("nothing was stamped on page 2")
	}
}

func TestStampMergesInheritedResources(t *testing.T) {
	in := testpdf.Build(testpdf.Options{SharedResources: true, Pages: []testpdf.Page{testpdf.Letter()}})
	out, err := Stamp(in, []Item{box(1)})
	if err != nil {
		t.Fatal(err)
	}
	rdr, err := pdf.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	fonts := rdr.Page(1).V.Key("Resources").Key("Font")
	if fonts.IsNull() {
		t.Fatal("the page lost its resources")
	}
	if fonts.Key("F1").IsNull() {
		t.Error("the inherited font was dropped instead of merged")
	}
	if fonts.Key(ResourcePrefix + "0").IsNull() {
		t.Error("the stamp's own font is not in the page resources")
	}
	// The page's own content still draws with F1, so the merge has to keep
	// the name pointing at the same object.
	if !strings.Contains(contentOf(t, out, 1), "/F1 24 Tf") {
		t.Error("the page's own content was lost")
	}
}

func TestStampTwiceStacks(t *testing.T) {
	once, err := Stamp(twoPages(), []Item{box(1)})
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Stamp(once, []Item{{Page: 1, Kind: KindText, Text: "second", Rect: geometry.Rect{X: 72, Y: 200, W: 200, H: 30}}})
	if err != nil {
		t.Fatal(err)
	}
	c := contentOf(t, twice, 1)
	if !strings.Contains(c, "(Hello) Tj") {
		t.Error("the original content was lost on the second pass")
	}
	if strings.Count(c, "re W n") != 2 {
		t.Errorf("both stamps should be on the page, got:\n%s", c)
	}
}

func TestStampWithNoItemsIsAnIdentity(t *testing.T) {
	in := twoPages()
	out, err := Stamp(in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, out) {
		t.Error("a stamp with nothing to draw still changed the document")
	}
}

func TestStampSkipsAPageThatIsNotThere(t *testing.T) {
	if _, err := Stamp(twoPages(), []Item{box(9)}); err == nil {
		t.Error("a stamp on a page that does not exist should be refused")
	}
}

// ── the frame ──────────────────────────────────────────────────────────

func TestFrameMapsTheVisualBoxOntoTheRect(t *testing.T) {
	r := geometry.Rect{X: 100, Y: 200, W: 120, H: 40}
	for _, rot := range []int{0, 90, 180, 270} {
		f := newFrame(r, rot)
		// The four corners of the visual box have to land on the rect.
		minX, minY := math.Inf(1), math.Inf(1)
		maxX, maxY := math.Inf(-1), math.Inf(-1)
		for _, c := range [][2]float64{{0, 0}, {f.w, 0}, {0, f.h}, {f.w, f.h}} {
			x := f.ox + c[0]*f.ax + c[1]*f.bx
			y := f.oy + c[0]*f.ay + c[1]*f.by
			minX, maxX = math.Min(minX, x), math.Max(maxX, x)
			minY, maxY = math.Min(minY, y), math.Max(maxY, y)
		}
		if math.Abs(minX-r.X) > 1e-6 || math.Abs(minY-r.Y) > 1e-6 ||
			math.Abs(maxX-(r.X+r.W)) > 1e-6 || math.Abs(maxY-(r.Y+r.H)) > 1e-6 {
			t.Errorf("rotate %d: the frame covers [%g %g %g %g], want [%g %g %g %g]",
				rot, minX, minY, maxX, maxY, r.X, r.Y, r.X+r.W, r.Y+r.H)
		}
		// The baseline direction has to point along the page's own reading
		// direction after /Rotate is applied.
		wantAx := []float64{1, 0, -1, 0}[rot/90]
		wantAy := []float64{0, 1, 0, -1}[rot/90]
		if math.Abs(f.ax-wantAx) > 1e-9 || math.Abs(f.ay-wantAy) > 1e-9 {
			t.Errorf("rotate %d: text runs along (%g %g), want (%g %g)", rot, f.ax, f.ay, wantAx, wantAy)
		}
	}
}

func TestClipToWidthShortensWithAnEllipsis(t *testing.T) {
	face := fontkit.Get(fontkit.Inter)
	long := strings.Repeat("Şahin ", 40)
	got := clipToWidth(face, long, 100, 10)
	if got == long {
		t.Fatal("a line far too wide for its box was not shortened")
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a shortened line should end in an ellipsis, got %q", got)
	}
	if face.Width(got, 10) > 100 {
		t.Error("the shortened line still does not fit")
	}
}

// ── the audit trail ────────────────────────────────────────────────────

func TestAuditIsAReadablePDF(t *testing.T) {
	var lines []AuditLine
	lines = append(lines, AuditLine{Text: "Signers", Head: true})
	for i := 0; i < 60; i++ {
		lines = append(lines, AuditLine{Text: turkish + " " + strings.Repeat("uzun ", 20), Muted: i%2 == 0})
	}
	b, err := Audit("Signature audit trail — sözleşme.pdf", lines)
	if err != nil {
		t.Fatal(err)
	}
	rdr, err := pdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("the audit trail does not parse: %v", err)
	}
	if rdr.NumPage() < 2 {
		t.Fatalf("60 long lines should not fit one page, got %d", rdr.NumPage())
	}
	if !strings.Contains(string(b), "/Encoding /Identity-H") {
		t.Error("the audit trail does not embed its font")
	}
	if !strings.Contains(contentOf(t, b, 1), "Tj") {
		t.Error("the first page of the audit trail is empty")
	}
}
