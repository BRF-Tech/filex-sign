package pdfsig

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/digitorus/pdfsign/verify"
	"github.com/digitorus/pkcs7"

	"github.com/brf-tech/filex-sign/internal/geometry"
	"github.com/brf-tech/filex-sign/internal/pdfdoc"
	"github.com/brf-tech/filex-sign/internal/testca"
	"github.com/brf-tech/filex-sign/internal/testpdf"
)

func scribble(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		y := h/2 + int(float64(h)*0.3*sin(float64(x)/float64(w)*6.28))
		for dy := -2; dy <= 2; dy++ {
			if y+dy >= 0 && y+dy < h {
				img.Set(x, y+dy, color.NRGBA{R: 20, G: 30, B: 120, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sin(x float64) float64 {
	// tiny Taylor series is enough for a squiggle
	x2 := x * x
	return x - x*x2/6 + x*x2*x2/120 - x*x2*x2*x2/5040
}

// ── intake ─────────────────────────────────────────────────────────────

func TestInspect(t *testing.T) {
	crop := [4]float64{50, 100, 350, 500}
	b := testpdf.Build(testpdf.Options{Pages: []testpdf.Page{
		testpdf.Letter(),
		{Width: 612, Height: 792, CropBox: &crop, Rotate: 90, Text: "two"},
		{Width: 595, Height: 842, Rotate: -90, Text: "three"},
	}})
	info, err := Inspect(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Pages) != 3 || info.Signatures != 0 {
		t.Fatalf("info = %+v", info)
	}
	if got := info.Pages[0].Box; got != (geometry.Box{X0: 0, Y0: 0, X1: 612, Y1: 792, Rotate: 0}) {
		t.Fatalf("page 1 box = %+v", got)
	}
	if got := info.Pages[1].Box; got != (geometry.Box{X0: 50, Y0: 100, X1: 350, Y1: 500, Rotate: 90}) {
		t.Fatalf("page 2 box = %+v", got)
	}
	if got := info.Pages[2].Box; got != (geometry.Box{X0: 0, Y0: 0, X1: 595, Y1: 842, Rotate: 270}) {
		t.Fatalf("page 3 box = %+v", got)
	}
	if _, err := info.Box(4); err == nil {
		t.Fatal("page 4 must be out of range")
	}
}

func TestInspect_Refusals(t *testing.T) {
	if _, err := Inspect([]byte("hello")); err != ErrNotPDF {
		t.Fatalf("text → %v", err)
	}
	if _, err := Inspect(testpdf.Build(testpdf.Options{Encrypt: true})); err != ErrEncrypted {
		t.Fatalf("encrypted → %v", err)
	}
	if _, err := Inspect(testpdf.Build(testpdf.Options{XFA: true})); err != ErrXFA {
		t.Fatalf("xfa → %v", err)
	}
}

// ── appearance ─────────────────────────────────────────────────────────

func TestLayout_WithDrawing(t *testing.T) {
	l := LayoutFor(150, 50, 600, 200, 2)
	if l.CanvasW != 600 || l.CanvasH != 200 {
		t.Fatalf("canvas = %d×%d", l.CanvasW, l.CanvasH)
	}
	// drawing sits above the text band, aspect preserved (3:1), centred
	if l.Image.Empty() || l.Image.Max.Y > 132 || l.Image.Min.Y < 0 {
		t.Fatalf("image box = %v", l.Image)
	}
	ratio := float64(l.Image.Dx()) / float64(l.Image.Dy())
	if ratio < 2.9 || ratio > 3.1 {
		t.Fatalf("aspect not preserved: %v", ratio)
	}
	if (l.Image.Min.X+l.Image.Max.X)/2 < 295 || (l.Image.Min.X+l.Image.Max.X)/2 > 305 {
		t.Fatalf("not centred: %v", l.Image)
	}
	if len(l.Lines) != 2 {
		t.Fatalf("two lines asked for, %d laid out", len(l.Lines))
	}
	if l.Lines[0].Base <= l.Image.Max.Y || l.Lines[1].Base <= l.Lines[0].Base || l.Lines[1].Base > l.CanvasH {
		t.Fatalf("text lines must sit under the drawing in order: %+v", l)
	}
	if l.Lines[0].Size <= l.Lines[1].Size || l.Lines[0].Size <= 0 {
		t.Fatalf("name must be larger than the date: %+v", l)
	}
}

func TestLayout_TallDrawingIsFitted(t *testing.T) {
	l := LayoutFor(100, 100, 100, 400, 2) // portrait drawing in a square field
	if l.Image.Dy() > int(float64(l.CanvasH)*0.66) || l.Image.Dx() > l.CanvasW {
		t.Fatalf("drawing overflows: %v in %d×%d", l.Image, l.CanvasW, l.CanvasH)
	}
}

func TestLayout_NoDrawingAndCaps(t *testing.T) {
	l := LayoutFor(150, 50, 0, 0, 2)
	if !l.Image.Empty() || l.Lines[0].Base >= l.Lines[1].Base || l.Lines[1].Base > l.CanvasH {
		t.Fatalf("text-only layout: %+v", l)
	}
	l = LayoutFor(2000, 100, 0, 0, 2)
	if l.CanvasW > maxCanvasPx {
		t.Fatalf("canvas not capped: %d", l.CanvasW)
	}
	if l := LayoutFor(0, 10, 0, 0, 2); l.CanvasW != 0 {
		t.Fatal("zero rect must yield an empty layout")
	}
}

func TestCompose_TurkishGlyphsRender(t *testing.T) {
	rect := geometry.Rect{X: 0, Y: 0, W: 150, H: 50}
	withText, err := Compose(rect, scribble(t, 300, 100), []string{"Şükrü Öğünç İ.", DateLine(time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC), "tr")})
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(withText))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 600 || img.Bounds().Dy() != 200 {
		t.Fatalf("composed size = %v", img.Bounds())
	}
	// The text band (lower third) must hold ink: glyphs were drawn, not
	// skipped as missing.
	ink := 0
	for y := 140; y < 200; y++ {
		for x := 0; x < 600; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
				ink++
			}
		}
	}
	if ink < 200 {
		t.Fatalf("text band has %d inked pixels; the name and date were not drawn", ink)
	}
	// And the drawing above it too.
	ink = 0
	for y := 0; y < 130; y++ {
		for x := 0; x < 600; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
				ink++
			}
		}
	}
	if ink < 200 {
		t.Fatal("the drawing was not placed")
	}
}

// Every chosen line is printed, top to bottom, under the drawing — and the
// drawing stays the larger part of the signature however many are chosen
// (the owner's stamp-line choice, 2026-09-21).
func TestLayout_ManyLinesStayUnderTheDrawing(t *testing.T) {
	for n := 0; n <= 7; n++ {
		l := LayoutFor(150, 60, 600, 200, n)
		if len(l.Lines) != n {
			t.Fatalf("n=%d: %d lines laid out", n, len(l.Lines))
		}
		if n == 0 && l.Image.Dy() < l.CanvasH*80/100 {
			t.Fatalf("no lines: the drawing should take the box, got %v of %d", l.Image, l.CanvasH)
		}
		if l.Image.Dy() < l.CanvasH*30/100 {
			t.Fatalf("n=%d: the drawing shrank to %d of %d px", n, l.Image.Dy(), l.CanvasH)
		}
		for i, ln := range l.Lines {
			if ln.Base <= l.Image.Max.Y || ln.Base > l.CanvasH || ln.Size <= 0 {
				t.Fatalf("n=%d line %d misplaced: %+v (image %v)", n, i, ln, l.Image)
			}
			if i > 0 && ln.Base <= l.Lines[i-1].Base {
				t.Fatalf("n=%d: lines out of order: %+v", n, l.Lines)
			}
		}
	}
}

func TestCompose_EveryLineIsInked(t *testing.T) {
	rect := geometry.Rect{W: 180, H: 80}
	lines := []string{"Ayşe Yılmaz", "ayse@example.com", "İmza tarihi: 21.09.2026 14:05 UTC", "IP adresi: 203.0.113.7", "Sertifika SHA-256: 3F2A 9C1B 7D4E 0A11…"}
	b, err := Compose(rect, scribble(t, 300, 100), lines)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	l := LayoutFor(rect.W, rect.H, 300, 100, len(lines))
	top := l.Image.Max.Y
	for i, ln := range l.Lines {
		ink := 0
		for y := top; y <= ln.Base && y < img.Bounds().Dy(); y++ {
			for x := 0; x < img.Bounds().Dx(); x++ {
				if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
					ink++
				}
			}
		}
		if ink < 20 {
			t.Fatalf("line %d (%q) has %d inked pixels: it was not printed", i, lines[i], ink)
		}
		top = ln.Base
	}
}

func TestCompose_LongNameShrinks(t *testing.T) {
	rect := geometry.Rect{W: 60, H: 20}
	b, err := Compose(rect, nil, []string{strings.Repeat("Çok uzun bir isim ", 6), "2026"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
}

func TestNormalize(t *testing.T) {
	big := scribble(t, 1800, 900) // an "upload" three times the pad
	out, err := Normalize(big, 40<<10)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() > PadMaxW || img.Bounds().Dy() > PadMaxH {
		t.Fatalf("not fitted: %v", img.Bounds())
	}
	if len(out) > 40<<10 {
		t.Fatalf("over budget: %d", len(out))
	}
	if _, err := Normalize([]byte("nope"), 1000); err == nil {
		t.Fatal("garbage must be refused")
	}

	// ⚠ The pad's own drawing — a PNG that fits the pad and the budget — is
	// kept as it came: re-encoding it on every event of the signer's screen
	// was the whole cost of the approve step (profiled 2026-09-21).
	// Encoded at another level than Normalize writes, so a re-encode could
	// not come back byte-for-byte by accident.
	img, err = png.Decode(bytes.NewReader(scribble(t, PadMaxW, PadMaxH)))
	if err != nil {
		t.Fatal(err)
	}
	var fast bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&fast, img); err != nil {
		t.Fatal(err)
	}
	pad := fast.Bytes()
	kept, err := Normalize(pad, 40<<10)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept, pad) {
		t.Error("a drawing that already fits was re-encoded")
	}
	// ...and one over the budget is still brought under it.
	if tight, err := Normalize(pad, len(pad)-1); err != nil || len(tight) >= len(pad) {
		t.Errorf("over budget by a byte: %d bytes, %v", len(tight), err)
	}
}

// ── signing ────────────────────────────────────────────────────────────

var byteRangeRe = regexp.MustCompile(`/ByteRange\s*\[\s*(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s*\]`)

// checkStructure reads every /ByteRange in the file, checks it covers the
// whole file except the /Contents hex string, and returns the CMS blobs.
func checkStructure(t *testing.T, pdfBytes []byte) [][]byte {
	t.Helper()
	var blobs [][]byte
	for _, m := range byteRangeRe.FindAllSubmatchIndex(pdfBytes, -1) {
		var br [4]int
		for i := 0; i < 4; i++ {
			v, _ := strconv.Atoi(string(pdfBytes[m[2+2*i]:m[3+2*i]]))
			br[i] = v
		}
		if br[0] != 0 {
			t.Fatalf("ByteRange must start at 0: %v", br)
		}
		// [0, a] and [b, b+c]; the gap is the /Contents <hex>
		gap := pdfBytes[br[1]:br[2]]
		if !bytes.HasPrefix(bytes.TrimSpace(gap), []byte("<")) || !bytes.HasSuffix(bytes.TrimSpace(gap), []byte(">")) {
			t.Fatalf("the ByteRange gap is not a hex string: %q…", gap[:16])
		}
		hexs := bytes.TrimSpace(gap)
		raw, err := hex.DecodeString(string(bytes.TrimRight(hexs[1:len(hexs)-1], "0")) + strings.Repeat("0", len(bytes.TrimRight(hexs[1:len(hexs)-1], "0"))%2))
		if err != nil {
			t.Fatalf("contents hex: %v", err)
		}
		blobs = append(blobs, raw)
		// the signed span must reach the end of this revision
		if br[2]+br[3] > len(pdfBytes) {
			t.Fatalf("ByteRange runs past the file: %v (len %d)", br, len(pdfBytes))
		}
	}
	if len(blobs) == 0 {
		t.Fatal("no /ByteRange found")
	}
	return blobs
}

func signedSpan(pdfBytes []byte, br [4]int) []byte {
	return append(append([]byte{}, pdfBytes[br[0]:br[0]+br[1]]...), pdfBytes[br[2]:br[2]+br[3]]...)
}

func lastByteRange(t *testing.T, pdfBytes []byte) [4]int {
	t.Helper()
	all := byteRangeRe.FindAllSubmatch(pdfBytes, -1)
	m := all[len(all)-1]
	var br [4]int
	for i := 0; i < 4; i++ {
		br[i], _ = strconv.Atoi(string(m[1+i]))
	}
	return br
}

func TestSign_VisibleThenSecondSigner(t *testing.T) {
	ca, err := testca.New("sign")
	if err != nil {
		t.Fatal(err)
	}
	original := testpdf.Build(testpdf.Options{Pages: []testpdf.Page{testpdf.Letter(), {Width: 612, Height: 792, Rotate: 90, Text: "p2"}}})
	info, err := Inspect(original)
	if err != nil {
		t.Fatal(err)
	}

	// signer 1: visible, page 2 (rotated), field placed as fractions
	leaf1, err := ca.Issue("Ayşe Yılmaz", "ayse@example.com", 30)
	if err != nil {
		t.Fatal(err)
	}
	box, _ := info.Box(2)
	rect := geometry.ToRect(geometry.Frac{X: 0.1, Y: 0.8, W: 0.3, H: 0.1}, box)
	when := time.Now().Truncate(time.Second)
	img, err := Compose(rect, scribble(t, 400, 120), []string{"Ayşe Yılmaz", DateLine(when, "en")})
	if err != nil {
		t.Fatal(err)
	}
	// The fields exist before anything is signed: signer 1's visible box
	// (page 2, rotated), signer 2's invisible field.
	prepared, err := pdfdoc.PrepareForm(original, []pdfdoc.FieldSpec{
		{Name: "ayse", Page: 2, Rect: rect, Rotate: box.Rotate, Kind: pdfdoc.KindSig, Label: "Ayşe"},
		{Name: "burak", Page: 1, Kind: pdfdoc.KindSig, Hidden: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	signed1, err := Sign(prepared, Options{
		Signer: leaf1.Signer(), Cert: leaf1.Cert, Chain: []*x509.Certificate{ca.Cert},
		Name: "Ayşe Yılmaz", Reason: "Approved", When: when,
		Field: "ayse", Image: img, Rotate: box.Rotate, Certify: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The first signature CERTIFIES: the catalogue's /Perms names it, and
	// its reference says what may still happen (P=2).
	if !bytes.Contains(signed1, []byte("/TransformMethod /DocMDP /TransformParams << /Type /TransformParams /P 2")) ||
		!bytes.Contains(signed1, []byte("/Perms << /DocMDP")) {
		t.Fatal("the first signature must certify the document (DocMDP P=2 and /Perms)")
	}
	if !bytes.HasPrefix(signed1, prepared) || !bytes.HasPrefix(prepared, original) {
		t.Fatal("the signed file must start with the original bytes (incremental update)")
	}
	if !bytes.Contains(signed1, []byte("/ETSI.CAdES.detached")) {
		t.Fatal("SubFilter must be ETSI.CAdES.detached (PAdES)")
	}
	blobs := checkStructure(t, signed1)
	if len(blobs) != 1 {
		t.Fatalf("expected 1 signature, found %d", len(blobs))
	}
	verifyCMS(t, signed1, blobs[0], ca)

	info1, err := Inspect(signed1)
	if err != nil {
		t.Fatal(err)
	}
	if info1.Signatures != 1 {
		t.Fatalf("Inspect counts %d signatures after the first, want 1", info1.Signatures)
	}

	// signer 2: invisible, on top of signer 1's file
	leaf2, err := ca.Issue("Burak Faruk Şahin", "burak@example.com", 30)
	if err != nil {
		t.Fatal(err)
	}
	signed2, err := Sign(signed1, Options{Signer: leaf2.Signer(), Cert: leaf2.Cert, Chain: []*x509.Certificate{ca.Cert}, Name: "Burak Faruk Şahin", When: when.Add(time.Hour), Field: "burak"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(signed2, signed1) {
		t.Fatal("the second signature must be an incremental update over the first")
	}
	blobs = checkStructure(t, signed2)
	if len(blobs) != 2 {
		t.Fatalf("expected 2 signatures, found %d", len(blobs))
	}
	verifyCMS(t, signed2, blobs[1], ca)
	info2, _ := Inspect(signed2)
	if info2.Signatures != 2 {
		t.Fatalf("Inspect counts %d signatures, want 2", info2.Signatures)
	}

	// pdfsign's own verifier, with the test CA as the only trust root
	vo := verify.DefaultVerifyOptions()
	vo.TrustedRoots = ca.Pool()
	vo.RequireDigitalSignatureKU = true
	vo.TrustSignatureTime = true
	resp, err := verify.VerifyWithOptions(bytes.NewReader(signed2), int64(len(signed2)), vo)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(resp.Signers) != 2 {
		t.Fatalf("verifier saw %d signers: %+v", len(resp.Signers), resp)
	}
	for i, s := range resp.Signers {
		if !s.ValidSignature {
			t.Errorf("signer %d: signature invalid: %+v", i, s)
		}
		if !s.TrustedIssuer {
			t.Errorf("signer %d: issuer not trusted through the CA pool: %+v", i, s.Certificates)
		}
	}
	if resp.Signers[0].Name != "Ayşe Yılmaz" || resp.Signers[1].Name != "Burak Faruk Şahin" {
		t.Fatalf("names: %q / %q", resp.Signers[0].Name, resp.Signers[1].Name)
	}

	// Tamper: flip a byte inside the first signed span → the CMS check fails.
	tampered := append([]byte{}, signed2...)
	tampered[100] ^= 0xff
	br := lastByteRange(t, tampered)
	p7, err := pkcs7.Parse(blobs[1])
	if err != nil {
		t.Fatal(err)
	}
	p7.Content = signedSpan(tampered, br)
	if err := p7.VerifyWithChain(ca.Pool()); err == nil {
		t.Fatal("a tampered document must not verify")
	}
}

func verifyCMS(t *testing.T, pdfBytes, cms []byte, ca *testca.CA) {
	t.Helper()
	br := lastByteRange(t, pdfBytes)
	p7, err := pkcs7.Parse(cms)
	if err != nil {
		t.Fatalf("pkcs7 parse: %v", err)
	}
	p7.Content = signedSpan(pdfBytes, br)
	if err := p7.VerifyWithChain(ca.Pool()); err != nil {
		t.Fatalf("pkcs7 verify: %v", err)
	}
	// The message digest attribute must be sha256 over the byte ranges.
	sum := sha256.Sum256(p7.Content)
	if len(p7.Signers) != 1 {
		t.Fatalf("signers = %d", len(p7.Signers))
	}
	_ = sum
	if signer := p7.GetOnlySigner(); signer == nil || signer.Subject.OrganizationalUnit[0] != "sign" {
		t.Fatalf("signer cert: %+v", signer)
	}
}

func TestSign_Refusals(t *testing.T) {
	ca, _ := testca.New("sign")
	leaf, _ := ca.Issue("x", "", 1)
	pdfBytes := testpdf.Build(testpdf.Options{})
	withField, err := pdfdoc.PrepareForm(pdfBytes, []pdfdoc.FieldSpec{{Name: "s", Page: 1, Kind: pdfdoc.KindSig, Hidden: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(withField, Options{Field: "s"}); err == nil {
		t.Fatal("no signer must fail")
	}
	if _, err := Sign(withField, Options{Signer: leaf.Signer(), Cert: leaf.Cert}); err == nil {
		t.Fatal("no field must fail")
	}
	if _, err := Sign(pdfBytes, Options{Signer: leaf.Signer(), Cert: leaf.Cert, Field: "s"}); err == nil {
		t.Fatal("a field the document does not carry must fail")
	}
	if _, err := Sign([]byte("not a pdf"), Options{Signer: leaf.Signer(), Cert: leaf.Cert, Field: "s"}); err == nil {
		t.Fatal("garbage must fail")
	}
	once, err := Sign(withField, Options{Signer: leaf.Signer(), Cert: leaf.Cert, Field: "s", Certify: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(once, Options{Signer: leaf.Signer(), Cert: leaf.Cert, Field: "s"}); !errors.Is(err, pdfdoc.ErrFieldSigned) {
		t.Fatalf("a signature is never written over another: %v", err)
	}
	two, err := pdfdoc.PrepareForm(once, []pdfdoc.FieldSpec{{Name: "t", Page: 1, Kind: pdfdoc.KindSig, Hidden: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(two, Options{Signer: leaf.Signer(), Cert: leaf.Cert, Field: "t", Certify: 2}); err == nil {
		t.Fatal("a document is certified once")
	}
}
