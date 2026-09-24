package pdfdoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"

	"github.com/digitorus/pdf"

	"github.com/brf-tech/filex-sign/internal/geometry"
	"github.com/brf-tech/filex-sign/internal/testpdf"
)

func base(t *testing.T) []byte {
	t.Helper()
	return testpdf.Build(testpdf.Options{Pages: []testpdf.Page{
		testpdf.Letter(), {Width: 612, Height: 792, Rotate: 90, Text: "p2"}}})
}

func specs() []FieldSpec {
	return []FieldSpec{
		{Name: "text-1", Page: 1, Rect: geometry.Rect{X: 60, Y: 600, W: 200, H: 20}, Kind: KindText, Label: "Görev unvanı"},
		{Name: "chk-1", Page: 1, Rect: geometry.Rect{X: 60, Y: 560, W: 16, H: 16}, Kind: KindCheck, Label: "Kabul"},
		{Name: "text-2", Page: 2, Rect: geometry.Rect{X: 60, Y: 400, W: 200, H: 20}, Rotate: 90, Kind: KindText, Label: "Not"},
	}
}

// ⚠ The whole point of the form path: filling a field must not touch
// what a page DRAWS. If it did, a second signature would make the first
// one read as "the document changed after it was signed".
func TestFillingAFieldLeavesEveryPageDrawingTheSameThing(t *testing.T) {
	in := base(t)
	before := pageDigests(t, in)

	prepared, err := PrepareForm(in, specs())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(prepared, in) {
		t.Fatal("PrepareForm must append, never rewrite what was there")
	}
	if got := pageDigests(t, prepared); !same(before, got) {
		t.Error("creating the fields changed what a page draws")
	}

	filled, err := FillForm(prepared, []FieldValue{
		{Name: "text-1", Kind: KindText, Text: "Müdür", Font: "inter"},
		{Name: "chk-1", Kind: KindCheck},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(filled, prepared) {
		t.Fatal("FillForm must append, never rewrite what was there")
	}
	if got := pageDigests(t, filled); !same(before, got) {
		t.Error("filling a field changed what a page draws")
	}

	// A second signer fills the remaining field: still nothing redrawn.
	again, err := FillForm(filled, []FieldValue{{Name: "text-2", Kind: KindText, Text: "Şahin", Rotate: 90}})
	if err != nil {
		t.Fatal(err)
	}
	if got := pageDigests(t, again); !same(before, got) {
		t.Error("the second signer's values changed what a page draws")
	}
	if !HasFields(again, []string{"text-1", "chk-1", "text-2"}) {
		t.Error("the fields are not in the document")
	}
}

func TestPrepareFormIsIdempotentAndValuesAreReadable(t *testing.T) {
	in := base(t)
	once, err := PrepareForm(in, specs())
	if err != nil {
		t.Fatal(err)
	}
	twice, err := PrepareForm(once, specs())
	if err != nil {
		t.Fatal(err)
	}
	if len(twice) != len(once) {
		t.Errorf("preparing twice added %d bytes — the fields were created again", len(twice)-len(once))
	}
	filled, err := FillForm(once, []FieldValue{
		{Name: "text-1", Kind: KindText, Text: "Müdür", Font: "inter"},
		{Name: "chk-1", Kind: KindCheck},
	})
	if err != nil {
		t.Fatal(err)
	}
	fs := readFields(t, filled)
	if got := fs["text-1"].Key("V").Text(); got != "Müdür" {
		t.Errorf("the value a reader sees is %q", got)
	}
	if got := fs["chk-1"].Key("V").Name(); got != "Yes" {
		t.Errorf("the tick is %q", got)
	}
	if got := fs["chk-1"].Key("AS").Name(); got != "Yes" {
		t.Errorf("the checkbox shows %q", got)
	}
	for _, name := range []string{"text-1", "chk-1"} {
		if fs[name].Key("AP").Key("N").IsNull() {
			t.Errorf("%s has no appearance of its own — a reader would draw it however it likes", name)
		}
		if fs[name].Key("Ff").Int64()&readOnlyFlag == 0 {
			t.Errorf("%s was left editable after it was signed over", name)
		}
	}
	// The one nobody filled stays open and empty.
	if fs["text-2"].Key("Ff").Int64()&readOnlyFlag != 0 {
		t.Error("an unfilled field must stay fillable")
	}
	// ⚠ /NeedAppearances would hand the drawing back to the reader, and a
	// Turkish name would render in whatever it guesses.
	rdr := reader(t, filled)
	if !rdr.Trailer().Key("Root").Key("AcroForm").Key("NeedAppearances").IsNull() {
		t.Error("/NeedAppearances must not be set")
	}
}

func TestFillFormRefusesAFieldThatIsNotThere(t *testing.T) {
	in := base(t)
	if _, err := FillForm(in, []FieldValue{{Name: "nope", Kind: KindText, Text: "x"}}); err == nil {
		t.Fatal("filling a field that was never created must be an error, not a silent no-op")
	}
}

func TestPrepareFormKeepsAnExistingForm(t *testing.T) {
	in := base(t)
	first, err := PrepareForm(in, specs()[:1])
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareForm(first, specs())
	if err != nil {
		t.Fatal(err)
	}
	if !HasFields(second, []string{"text-1", "chk-1", "text-2"}) {
		t.Error("adding to a form dropped what was already in it")
	}
}

// ── helpers ────────────────────────────────────────────────────────────

func reader(t *testing.T, b []byte) *pdf.Reader {
	t.Helper()
	r, err := pdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// pageDigests hashes what each page draws, which is the question a
// verifier is really asking.
func pageDigests(t *testing.T, b []byte) []string {
	t.Helper()
	rdr := reader(t, b)
	var out []string
	for i := 1; i <= rdr.NumPage(); i++ {
		h := sha256.New()
		switch c := rdr.Page(i).V.Key("Contents"); c.Kind() {
		case pdf.Array:
			for j := 0; j < c.Len(); j++ {
				copyStream(h, c.Index(j))
			}
		default:
			copyStream(h, c)
		}
		out = append(out, hex.EncodeToString(h.Sum(nil)))
	}
	return out
}

func copyStream(h io.Writer, v pdf.Value) {
	if v.Kind() != pdf.Stream {
		return
	}
	r := v.Reader()
	if r == nil {
		return
	}
	defer r.Close()
	_, _ = io.Copy(h, r)
}

func readFields(t *testing.T, b []byte) map[string]pdf.Value {
	t.Helper()
	out := map[string]pdf.Value{}
	collectFields(reader(t, b).Trailer().Key("Root").Key("AcroForm").Key("Fields"), out, 0)
	return out
}

func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
