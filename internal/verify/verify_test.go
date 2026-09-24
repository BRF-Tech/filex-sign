package verify

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex-sign/internal/geometry"
	"github.com/brf-tech/filex-sign/internal/pdfdoc"
	"github.com/brf-tech/filex-sign/internal/testca"
	"github.com/brf-tech/filex-sign/internal/testpdf"
)

func base(t *testing.T) []byte {
	t.Helper()
	return testpdf.Build(testpdf.Options{Pages: []testpdf.Page{testpdf.Letter()}})
}

// ⚠ The distinction the whole form path exists for, checked on its own:
// filling a field is "nothing was redrawn", stamping the page is not.
func TestLaterChangesTellsAFilledFieldFromARedrawnPage(t *testing.T) {
	in := base(t)
	rect := geometry.Rect{X: 60, Y: 600, W: 200, H: 20}

	prepared, err := pdfdoc.PrepareForm(in, []pdfdoc.FieldSpec{
		{Name: "text-1", Page: 1, Rect: rect, Kind: pdfdoc.KindText, Label: "Ad"}})
	if err != nil {
		t.Fatal(err)
	}
	filled, err := pdfdoc.FillForm(prepared, []pdfdoc.FieldValue{
		{Name: "text-1", Kind: pdfdoc.KindText, Text: "Gökçe", Font: "inter"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := laterChanges(filled, int64(len(in))); got != ChangedFields {
		t.Errorf("filling a form field reads as %q, expected %q", got, ChangedFields)
	}

	stamped, err := pdfdoc.Stamp(in, []pdfdoc.Item{
		{Page: 1, Rect: rect, Kind: pdfdoc.KindText, Text: "Gökçe", Font: "inter"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := laterChanges(stamped, int64(len(in))); got != ChangedContent {
		t.Errorf("redrawing the page reads as %q, expected %q", got, ChangedContent)
	}
}

func TestInspectSaysNothingWhenThereIsNothing(t *testing.T) {
	ca, err := testca.New("sign")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Inspect(base(t), testca.PEM(ca.Cert))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Signed() {
		t.Error("an unsigned document carries no signatures")
	}
	if rep.Pages != 1 {
		t.Errorf("pages: %d", rep.Pages)
	}
	if len(rep.Authorities) != 1 || rep.Authorities[0].FP == "" {
		t.Errorf("the authorities it would trust should be listed: %+v", rep.Authorities)
	}
	if !rep.Authorities[0].IsCA {
		t.Error("a signing authority is a CA")
	}
}

func TestInspectRefusesSomethingThatIsNotAPDF(t *testing.T) {
	if _, err := Inspect([]byte("hello"), ""); err == nil {
		t.Fatal("a file that is not a PDF must be refused, not reported as unsigned")
	}
}

func TestParsePEMSkipsWhatIsNotACertificate(t *testing.T) {
	ca, err := testca.New("sign")
	if err != nil {
		t.Fatal(err)
	}
	bundle := "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n" + testca.PEM(ca.Cert)
	got := ParsePEM(bundle)
	if len(got) != 1 || !got[0].Equal(ca.Cert) {
		t.Fatalf("expected the one certificate, got %d", len(got))
	}
	if ParsePEM("") != nil {
		t.Error("an empty bundle is no certificates")
	}
}

func TestFingerprintIsOneReadableShape(t *testing.T) {
	ca, err := testca.New("sign")
	if err != nil {
		t.Fatal(err)
	}
	fp := Fingerprint(ca.Cert)
	if len(strings.Fields(fp)) != 16 || fp != strings.ToUpper(fp) {
		t.Errorf("a SHA-256 in upper-case groups of four: %q", fp)
	}
	if Fingerprint(nil) != "" {
		t.Error("no certificate, no fingerprint")
	}
}
