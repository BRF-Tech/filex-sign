package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
	"github.com/digitorus/pdf"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/pdfdoc"
	"github.com/brf-tech/filex-sign/internal/views"
)

// noisyPNG is a drawing a compressor cannot shrink much — the worst case
// the 64 KiB a job may carry has to hold.
func noisyPNG(t *testing.T, seed int64) string {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	img := image.NewNRGBA(image.Rect(0, 0, 600, 200))
	for i := 0; i < 9000; i++ {
		img.Set(r.Intn(600), r.Intn(200), color.NRGBA{R: uint8(r.Intn(255)), G: uint8(r.Intn(255)), B: 90, A: uint8(80 + r.Intn(175))})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// ⚠⚠ The owner, 2026-09-21, all at once: the lines under a signature are
// chosen per box and the signer SEES them before signing (an IP address is
// personal data); a box's name is free text and its identity — the PDF
// field's own name — is ASCII and never shown; and a signer with two
// signature boxes gets their drawing in both, each with its own lines.
func TestTwoSignatureBoxes_EachCarriesItsOwnDrawingAndLines(t *testing.T) {
	a, f := newApp(t)
	lines := func(ids ...string) []string { return ids }
	boxes := []map[string]any{
		merge(sigBox("sig-1", "s1", .1), map[string]any{"label": "Yetkili imzası", "lines": lines("name", "ip", "cert")}),
		merge(sigBox("sig-2", "s1", .5), map[string]any{"label": "顧客", "lines": []string{}}),
		merge(textBox("text-1", "s1", .1), map[string]any{"label": "Müşteri adı"}),
	}
	s := walkRequest(t, a, "", boxes, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	env := loadEnv(t, a)
	for id, want := range map[string]string{"sig-1": "yetkili-imzasi", "sig-2": "signature", "text-1": "musteri-adi"} {
		if got := env.Field(id).Key; got != want {
			t.Errorf("box %s: identity %q, want %q", id, got, want)
		}
	}
	if got := env.Field("sig-2").Label; got != "顧客" {
		t.Errorf("a name is kept exactly as typed: %q", got)
	}

	// Gökçe signs from her own screen, whose address filex hands the app.
	me := gokce
	me.IP = "203.0.113.9"
	scr, err := a.viewFill(viewInputAs(me, ViewFill, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	scr, _ = a.viewFill(viewInputAs(me, ViewFill, "submit", "", scr.State, nil))
	vals := merge(map[string]any{
		views.PadFor("sig-1"): map[string]any{"png_b64": noisyPNG(t, 1), "mode": "draw"},
		views.PadFor("sig-2"): map[string]any{"png_b64": noisyPNG(t, 2), "mode": "draw"},
	}, map[string]any{"text-1": "Acme"})
	scr, err = a.viewFill(viewInputAs(me, ViewFill, "submit", "", scr.State, vals))
	if err != nil {
		t.Fatal(err)
	}
	if len(scr.Errors) > 0 {
		t.Fatalf("refused: %+v", scr.Errors)
	}
	// The approve step says, in words, exactly what goes under each box —
	// the address included — and says when the certificate will exist.
	words := surfaceWords(scr)
	for _, want := range []string{"“Yetkili imzası” altına yazılacaklar: Gökçe · IP adresi: 203.0.113.9 · Sertifika SHA-256: imzaladığınızda verilir",
		"“顧客” altına bir şey yazılmıyor"} {
		if !strings.Contains(words, want) {
			t.Errorf("the approve step does not say %q:\n%s", want, words)
		}
	}
	// ...and the document on it shows the picture the job will print.
	pdfNode := findNode(scr, "pdf-fields")
	if pdfNode == nil {
		t.Fatal("no document on the approve step")
	}
	shown := map[string]string{}
	for _, fp := range pdfNode.Props["fields"].([]map[string]any) {
		v, _ := fp["value"].(string)
		shown[fp["id"].(string)] = v
	}
	if shown["sig-1"] == "" || shown["sig-2"] == "" || shown["sig-1"] == shown["sig-2"] {
		t.Error("each signature box must preview its own picture")
	}

	scr, err = a.viewFill(viewInputAs(me, ViewFill, "submit", "", scr.State, nil))
	if err != nil {
		t.Fatal(err)
	}
	if scr.Job == nil {
		t.Fatalf("no job queued: %+v", scr.Errors)
	}
	// ⚠ Both drawings ride in ONE job, which filex caps at 64 KiB: two
	// boxes at 40 KiB apiece were refused at the door (413).
	raw, _ := json.Marshal(scr.Job.Params)
	if len(raw) > 64<<10 {
		t.Fatalf("the job's parameters are %d bytes; filex refuses anything over 64 KiB", len(raw))
	}
	if ip, _ := scr.Job.Params["visitor_ip"].(string); ip != "203.0.113.9" {
		t.Errorf("the job must carry the address the signer was shown: %q", ip)
	}
	out, err := a.actionApply(jobFrom(t, scr, me, docName))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("the signature failed: %v", out.Message)
	}
	final := commit(t, f, out)

	names := pdfdoc.FieldNames(final)
	for _, want := range []string{"musteri-adi", "signature"} {
		if !names[want] {
			t.Errorf("the PDF has no form field named %q (has %v)", want, names)
		}
	}
	if names["text-1"] || names["sig-2"] {
		t.Errorf("a field is still named by its internal id: %v", names)
	}
	// The second box is a PICTURE — its appearance draws an image — not a
	// name set in a handwriting face (whose appearance would draw text in a
	// font). ⚠ Asked of that one field: the signature widget's own
	// appearance is an image too, so "the file contains an image" proves
	// nothing about the second box.
	if !fieldDrawsImage(t, final, "signature") {
		t.Error("the second signature box carries no drawing")
	}
	if got := loadEnv(t, a).Signer("s1").SignedIP; got != "203.0.113.9" {
		t.Errorf("the record's address is %q", got)
	}
}

// fieldDrawsImage reports whether the form field named `name` has a normal
// appearance whose resources hold an image XObject.
func fieldDrawsImage(t *testing.T, doc []byte, name string) bool {
	t.Helper()
	rdr, err := pdf.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		t.Fatal(err)
	}
	fs := rdr.Trailer().Key("Root").Key("AcroForm").Key("Fields")
	for i := 0; i < fs.Len(); i++ {
		f := fs.Index(i)
		if f.Key("T").Text() != name {
			continue
		}
		xo := f.Key("AP").Key("N").Key("Resources").Key("XObject")
		for _, k := range xo.Keys() {
			if xo.Key(k).Key("Subtype").Name() == "Image" {
				return true
			}
		}
		return false
	}
	t.Fatalf("no form field named %q", name)
	return false
}

func findNode(s *wire.Surface, typ string) *wire.Node {
	var found *wire.Node
	var walk func([]wire.Node)
	walk = func(ns []wire.Node) {
		for i := range ns {
			if ns[i].Type == typ && found == nil {
				found = &ns[i]
			}
			walk(ns[i].Children)
		}
	}
	walk(s.Nodes)
	return found
}

// The review counts every box in exactly one row: a box that belongs to
// ANYONE is a row of its own (the owner, 2026-09-21: "kimlik kısmında
// 'Herkes' diye bir kimlik daha göster"), so the rows add up to the boxes.
func TestReview_AnyoneIsARowAndTheRowsAddUp(t *testing.T) {
	a, _ := newApp(t)
	s, err := a.viewRequest(viewInput(ViewRequest, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	boxes := []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5),
		textBox("text-1", "", .1), dateBox("date-1", "", .5), textBox("text-2", "s1", .3),
	}
	answers := map[int]map[string]any{
		views.StepSigners: {views.IDSigners: []map[string]any{{"user_id": 7, "email": gokce.Email, "name": gokce.Name}},
			"identities": "Dış Kişi <dis@example.test>"},
		views.StepBoxes: {views.IDFields: boxes}, views.StepPlace: {views.IDFields: boxes},
	}
	for i := 0; i < 8; i++ {
		step, _ := s.State["step"].(int)
		if step == views.StepReview {
			break
		}
		s, err = a.viewRequest(viewInput(ViewRequest, "submit", "", s.State, answers[step]))
		if err != nil {
			t.Fatal(err)
		}
	}
	list := findNode(s, "list")
	if list == nil {
		t.Fatal("no review table")
	}
	cells := map[string]wire.Text{}
	for _, r := range list.Props["rows"].([]map[string]any) {
		cells[r["id"].(string)] = r["cells"].(map[string]wire.Text)["boxes"]
	}
	// ⚠ The row says what the person is ASKED FOR, not arithmetic about
	// them: a participant may be asked for no signature at all now, and
	// "0 imza · 3 doldurulacak" was the commonest row and the least
	// readable (the owner, 2026-09-23).
	want := map[string]string{"s1": "imzalar · 1 doldurulacak", "s2": "imzalar", "anyone": "2 kutu doldurur"}
	for id, w := range want {
		if cells[id]["tr"] != w {
			t.Errorf("row %s: %q, want %q", id, cells[id]["tr"], w)
		}
	}
	if !strings.Contains(surfaceWords(s), "Herkes") {
		t.Error("the Anyone row must be named as the define step names it")
	}
	if strings.Contains(surfaceWords(s), "Atanmamış kutu") {
		t.Error("the old one-liner that counted for nobody is still there")
	}
	_ = envelope.MaxFields
}
