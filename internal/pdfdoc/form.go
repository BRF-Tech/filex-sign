package pdfdoc

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/color"
	_ "image/png" // the pictures a signature box is filled with
	"sort"
	"strings"

	"github.com/digitorus/pdf"

	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/geometry"
)

// ── Real form fields, and why ──────────────────────────────────────────
//
// Stamp draws a value by rewriting the page's content stream and its
// resources. That is fine for a document signed once — the stamp goes on
// before the signature — but it is the wrong shape as soon as TWO people
// sign: the second person's stamp rewrites page content under the first
// person's signature, and a strict reader is right to say the document
// changed after it was signed (digitorus spells the rule out in
// verify.checkIncrementalUpdateScope; Adobe reaches the same verdict its
// own way).
//
// So a request's typed boxes become REAL AcroForm fields, created once
// while the document still carries no signature of ours (PrepareForm),
// and every signer afterwards only fills them (FillForm): one field
// object plus its appearance stream, never a page's content or
// resources. That is what "filling in a form" means to a verifier, and
// it is what keeps the first signature clean.
//
// The SIGNATURE boxes are created here too, as empty /Sig fields — and
// the platform seal's, invisible and locked (P=1). That is what lets the
// first signature CERTIFY the document (DocMDP P=2, sign.go): after it,
// every later signer only fills a field and signs a field that already
// exists, which is exactly what P=2 permits, and the seal's lock closes the
// document for good. (It used to be the reverse — "No DocMDP" — because
// pdfsign wrote a fresh widget into /Annots for every signature.)
//
// One deliberate omission: no /NeedAppearances. Leaving the drawing to the
// reader means a Turkish name renders in whatever the reader guesses; we
// write the appearance ourselves with the embedded face, as Stamp does.

// FieldSpec is one box to create as a form field.
type FieldSpec struct {
	Name   string        // the field's /T — the plugin's field id
	Page   int           // 1-based
	Rect   geometry.Rect // PDF user space of the unrotated page
	Rotate int           // the page's /Rotate
	Kind   string        // KindText | KindCheck | KindSig
	Label  string        // /TU, the tooltip a reader shows
	// Hidden makes a KindSig field invisible (a zero rectangle): the seal.
	Hidden bool
	// Lock gives a KindSig field a /Lock that closes the WHOLE document once
	// it is signed (Action All, P 1 — Acrobat's "lock document after
	// signing"): the seal's field.
	Lock bool
}

// KindSig is an empty signature field, signed later by PrepareSignature.
const KindSig = "sig"

// FieldValue is one field to fill.
type FieldValue struct {
	Name   string
	Kind   string
	Text   string
	Font   string
	Rotate int
	// Image is the appearance itself, as a PNG, for KindImage: a signature
	// box that is not the one the signature widget went into. Text is then
	// the field's value (the signer's name), and the picture is what is
	// seen — the same composed drawing and lines the signature widget shows.
	Image []byte
}

// KindImage fills a text field whose APPEARANCE is a picture.
//
// ⚠⚠ Why it exists. One signature puts one widget on the page, and a
// signer with two signature boxes used to get their drawing in the first
// and only their name, set in a handwriting face, in the second — while the
// pad had asked them to draw in both, and the second drawing was thrown
// away. With the lines printed under a signature chosen per box (2026-09-21),
// the second box would also have silently lost its lines: what the requester
// chose and the signer approved was not what was printed. The second box now
// shows its own drawing and its own lines, as a form field's appearance —
// which, like every value here, fills a field instead of rewriting the page,
// so it cannot disturb a signature already in the document.
const KindImage = "image"

// PrepareForm adds every named box to the document as an AcroForm field
// with no value yet, as an incremental update. Fields that already exist
// are left alone, so calling it twice is harmless.
//
// It rewrites page dictionaries (to list the new widgets in /Annots) and
// the catalogue (to point at the form) — the same objects pdfsign itself
// rewrites for every signature. It never touches a page's content stream
// or its resources.
func PrepareForm(in []byte, specs []FieldSpec) ([]byte, error) {
	if len(specs) == 0 {
		return in, nil
	}
	rdr, err := pdf.NewReader(bytes.NewReader(in), int64(len(in)))
	if err != nil {
		return nil, fmt.Errorf("pdfdoc: %w", err)
	}
	facts, err := readTrailer(in, rdr)
	if err != nil {
		return nil, err
	}
	root := rdr.Trailer().Key("Root")
	pagesRoot := root.Key("Pages")
	if pagesRoot.IsNull() {
		return nil, fmt.Errorf("pdfdoc: the document has no page tree")
	}
	acro := root.Key("AcroForm")
	existing := map[string]bool{}
	collectFieldNames(acro.Key("Fields"), existing, 0)

	var todo []FieldSpec
	for _, sp := range specs {
		if sp.Name == "" || existing[sp.Name] {
			continue
		}
		existing[sp.Name] = true
		todo = append(todo, sp)
	}
	if len(todo) == 0 {
		return in, nil
	}

	b := newBuilder(facts.size)
	byPage := map[int][]int{}
	var added []int
	sigs := false
	for _, sp := range todo {
		sigs = sigs || sp.Kind == KindSig
		page, err := findPage(pagesRoot, sp.Page)
		if err != nil {
			return nil, err
		}
		id := b.add(widgetBody(sp, refOf(page)))
		byPage[sp.Page] = append(byPage[sp.Page], id)
		added = append(added, id)
	}

	pages := make([]int, 0, len(byPage))
	for n := range byPage {
		pages = append(pages, n)
	}
	sort.Ints(pages)
	for _, n := range pages {
		page, err := findPage(pagesRoot, n)
		if err != nil {
			return nil, err
		}
		ptr := page.GetPtr()
		b.replace(int(ptr.GetID()), int(ptr.GetGen()), rewriteAnnots(page, byPage[n]))
	}

	rootID := int(root.GetPtr().GetID())
	switch {
	case acro.IsNull():
		flags := ""
		if sigs {
			flags = " /SigFlags 3"
		}
		id := b.add([]byte("<< /Fields [" + refs(added) + "] /DA " + defaultDA + " /DR " + defaultDR + flags + " >>"))
		b.replace(rootID, int(root.GetPtr().GetGen()), rewriteCatalogue(root, id))
	case isRef(acro, rootID):
		ptr := acro.GetPtr()
		b.replace(int(ptr.GetID()), int(ptr.GetGen()), rewriteAcroForm(acro, int(ptr.GetID()), added, sigs))
	default:
		// The form dictionary sits inside the catalogue: give it an object
		// of its own so the update does not rewrite the catalogue twice.
		id := b.add(rewriteAcroForm(acro, rootID, added, sigs))
		b.replace(rootID, int(root.GetPtr().GetGen()), rewriteCatalogue(root, id))
	}
	return appendUpdate(in, b, facts)
}

// FillForm writes values into fields that already exist, as an
// incremental update that touches nothing but those field objects and
// the appearance streams they point at. A filled field is locked
// read-only, so a later signer cannot quietly change what an earlier one
// signed over.
func FillForm(in []byte, vals []FieldValue) ([]byte, error) {
	if len(vals) == 0 {
		return in, nil
	}
	rdr, err := pdf.NewReader(bytes.NewReader(in), int64(len(in)))
	if err != nil {
		return nil, fmt.Errorf("pdfdoc: %w", err)
	}
	facts, err := readTrailer(in, rdr)
	if err != nil {
		return nil, err
	}
	fields := map[string]pdf.Value{}
	collectFields(rdr.Trailer().Key("Root").Key("AcroForm").Key("Fields"), fields, 0)

	b := newBuilder(facts.size)
	fonts := newFontSet()
	type pending struct {
		field pdf.Value
		val   FieldValue
		draw  drawn
		ap    int
	}
	// The appearances are drawn first so the faces know which glyphs they
	// have to embed, emitted second so they have object ids, and only
	// then written as streams that can name them.
	var queued []pending
	for _, v := range vals {
		f, ok := fields[v.Name]
		if !ok {
			return nil, fmt.Errorf("pdfdoc: the document has no field %q", v.Name)
		}
		rect, ok := rectOf(f)
		if !ok {
			return nil, fmt.Errorf("pdfdoc: field %q has no rectangle", v.Name)
		}
		d, err := drawValue(fonts, rect, v)
		if err != nil {
			return nil, err
		}
		queued = append(queued, pending{field: f, val: v, draw: d})
	}
	fonts.emit(b)
	for i := range queued {
		if px := queued[i].draw.pixels; px != nil {
			queued[i].draw.imgObj = addImage(b, px)
		}
		queued[i].ap = b.addStream(queued[i].draw.dict(), queued[i].draw.ops)
	}
	for _, q := range queued {
		ptr := q.field.GetPtr()
		b.replace(int(ptr.GetID()), int(ptr.GetGen()), rewriteField(q.field, q.val, q.ap))
	}
	return appendUpdate(in, b, facts)
}

// HasFields reports whether the document already carries every named
// field, which is how a job tells "prepared" from "not prepared yet"
// without trusting a flag on the record alone.
func HasFields(in []byte, names []string) bool {
	if len(names) == 0 {
		return true
	}
	rdr, err := pdf.NewReader(bytes.NewReader(in), int64(len(in)))
	if err != nil {
		return false
	}
	have := map[string]bool{}
	collectFieldNames(rdr.Trailer().Key("Root").Key("AcroForm").Key("Fields"), have, 0)
	for _, n := range names {
		if !have[n] {
			return false
		}
	}
	return true
}

// ── the objects ────────────────────────────────────────────────────────

// defaultDA / defaultDR are what a reader would use if it ever
// regenerated an appearance itself. It never should — we always write
// /AP — but a form without them is refused by some validators.
const (
	defaultDA = "(/Helv 9 Tf 0 g)"
	defaultDR = "<< /Font << /Helv << /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >> >> >>"
)

// readOnlyFlag is /Ff bit 1: the field may not be changed any more.
const readOnlyFlag = 1

func widgetBody(sp FieldSpec, pageRef string) []byte {
	var b bytes.Buffer
	b.WriteString("<< /Type /Annot /Subtype /Widget")
	flags := 4 // Print
	if sp.Kind == KindSig {
		// Print + Locked: the box of a signature is not moved or deleted by
		// a reader's form tools.
		flags = 132
		b.WriteString(sigWidgetRect(sp))
	} else {
		fmt.Fprintf(&b, " /Rect [%.3f %.3f %.3f %.3f]", sp.Rect.LLX(), sp.Rect.LLY(), sp.Rect.URX(), sp.Rect.URY())
	}
	fmt.Fprintf(&b, " /F %d /P %s /T %s", flags, pageRef, pdfTextString(sp.Name))
	if sp.Label != "" {
		fmt.Fprintf(&b, " /TU %s", pdfTextString(sp.Label))
	}
	b.WriteString(" /MK << >>")
	switch sp.Kind {
	case KindCheck:
		b.WriteString(" /FT /Btn /V /Off /AS /Off")
	case KindSig:
		b.WriteString(" /FT /Sig")
		if sp.Lock {
			b.WriteString(" /Lock << /Type /SigFieldLock /Action /All /P 1 >>")
		}
	default:
		b.WriteString(" /FT /Tx /DA " + defaultDA)
	}
	b.WriteString(" >>")
	return b.Bytes()
}

// sigWidgetRect is a KindSig field's rectangle: the box, or nothing at all
// for the invisible seal.
func sigWidgetRect(sp FieldSpec) string {
	if sp.Hidden {
		return " /Rect [0 0 0 0]"
	}
	return fmt.Sprintf(" /Rect [%.3f %.3f %.3f %.3f]", sp.Rect.LLX(), sp.Rect.LLY(), sp.Rect.URX(), sp.Rect.URY())
}

// rewriteAnnots re-emits a page with the new widgets appended to its
// /Annots. Everything else is copied reference for reference: the page's
// /Contents and /Resources come out as the very same objects, which is
// what keeps an earlier signature honest.
func rewriteAnnots(page pdf.Value, add []int) []byte {
	pageID := int(page.GetPtr().GetID())
	var b bytes.Buffer
	b.WriteString("<<\n")
	for _, key := range page.Keys() {
		if key == "Annots" {
			continue
		}
		fmt.Fprintf(&b, "  /%s %s\n", key, serialise(page.Key(key), pageID))
	}
	b.WriteString("  /Annots [")
	if a := page.Key("Annots"); a.Kind() == pdf.Array {
		for i := 0; i < a.Len(); i++ {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(serialise(a.Index(i), pageID))
		}
		if a.Len() > 0 {
			b.WriteByte(' ')
		}
	}
	b.WriteString(refs(add))
	b.WriteString("]\n>>")
	return b.Bytes()
}

func rewriteCatalogue(root pdf.Value, acroID int) []byte {
	rootID := int(root.GetPtr().GetID())
	var b bytes.Buffer
	b.WriteString("<<\n")
	for _, key := range root.Keys() {
		if key == "AcroForm" {
			continue
		}
		fmt.Fprintf(&b, "  /%s %s\n", key, serialise(root.Key(key), rootID))
	}
	fmt.Fprintf(&b, "  /AcroForm %d 0 R\n>>", acroID)
	return b.Bytes()
}

func rewriteAcroForm(acro pdf.Value, ownerID int, add []int, sigs bool) []byte {
	var b bytes.Buffer
	b.WriteString("<<")
	hasDA, hasDR := false, false
	for _, key := range acro.Keys() {
		switch key {
		case "Fields":
			continue
		case "SigFlags":
			if sigs {
				continue
			}
		case "DA":
			hasDA = true
		case "DR":
			hasDR = true
		case "NeedAppearances":
			// We write every appearance ourselves; asking the reader to
			// regenerate them is how a Turkish name turns into boxes.
			continue
		}
		fmt.Fprintf(&b, " /%s %s", key, serialise(acro.Key(key), ownerID))
	}
	b.WriteString(" /Fields [")
	if f := acro.Key("Fields"); f.Kind() == pdf.Array {
		for i := 0; i < f.Len(); i++ {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(serialise(f.Index(i), ownerID))
		}
		if f.Len() > 0 {
			b.WriteByte(' ')
		}
	}
	b.WriteString(refs(add))
	b.WriteString("]")
	if !hasDA {
		b.WriteString(" /DA " + defaultDA)
	}
	if !hasDR {
		b.WriteString(" /DR " + defaultDR)
	}
	if sigs {
		b.WriteString(" /SigFlags 3")
	}
	b.WriteString(" >>")
	return b.Bytes()
}

// rewriteField re-emits one field object with its value, its appearance
// and the read-only bit; every other entry is copied unchanged.
func rewriteField(f pdf.Value, v FieldValue, ap int) []byte {
	fieldID := int(f.GetPtr().GetID())
	flags := int(f.Key("Ff").Int64()) | readOnlyFlag
	var b bytes.Buffer
	b.WriteString("<<")
	for _, key := range f.Keys() {
		switch key {
		case "V", "AP", "AS", "Ff":
			continue
		}
		fmt.Fprintf(&b, " /%s %s", key, serialise(f.Key(key), fieldID))
	}
	fmt.Fprintf(&b, " /Ff %d", flags)
	if v.Kind == KindCheck {
		fmt.Fprintf(&b, " /V /Yes /AS /Yes /AP << /N << /Yes %d 0 R >> >>", ap)
	} else {
		fmt.Fprintf(&b, " /V %s /AP << /N %d 0 R >>", pdfTextString(v.Text), ap)
	}
	b.WriteString(" >>")
	return b.Bytes()
}

// drawn is one field's appearance before it has an object: the operators
// and the face they name. The face's object id only exists after the set
// is emitted, which is why the dictionary is built last.
type drawn struct {
	ops  []byte
	w, h float64
	m    [4]float64
	face *fontUse
	// pixels is a picture to place (KindImage), and imgObj its object once
	// it has been written.
	pixels *rgba
	imgObj int
	// faces are the other faces the text borrowed glyphs from (a Noto face
	// for a script the chosen one does not draw): every face the operators
	// name has to be in the appearance's resources.
	faces []*fontUse
}

func (d drawn) dict() string {
	res := "<< >>"
	switch {
	case d.imgObj != 0:
		res = fmt.Sprintf("<< /XObject << /Im0 %d 0 R >> >>", d.imgObj)
	case d.face != nil && d.face.obj != 0:
		var b strings.Builder
		fmt.Fprintf(&b, "<< /Font << /%s %d 0 R", d.face.name, d.face.obj)
		named := map[string]bool{d.face.name: true}
		for _, u := range d.faces {
			if u.obj != 0 && !named[u.name] {
				named[u.name] = true
				fmt.Fprintf(&b, " /%s %d 0 R", u.name, u.obj)
			}
		}
		b.WriteString(" >> >>")
		res = b.String()
	}
	return fmt.Sprintf("/Type /XObject /Subtype /Form /BBox [0 0 %.3f %.3f] /Matrix [%.4f %.4f %.4f %.4f 0 0] /Resources %s",
		d.w, d.h, d.m[0], d.m[1], d.m[2], d.m[3], res)
}

// drawValue draws the value into a box the size of the field, upright on
// a rotated page: the appearance is written in BBox space and the form's
// /Matrix turns it, which is what makes the reader fit it to /Rect.
func drawValue(fonts *fontSet, rect geometry.Rect, v FieldValue) (drawn, error) {
	box := newFrame(rect, v.Rotate)
	d := drawn{w: box.w, h: box.h, m: [4]float64{box.ax, box.ay, box.bx, box.by}}
	flat := frame{ax: 1, by: 1, w: box.w, h: box.h}
	var ops bytes.Buffer
	switch {
	case v.Kind == KindCheck:
		drawCheck(&ops, flat)
	case v.Kind == KindImage:
		px, err := decodeRGBA(v.Image)
		if err != nil {
			return drawn{}, err
		}
		d.pixels = px
		// The picture fills the box: it was composed at the box's own
		// proportions, so nothing is stretched.
		fmt.Fprintf(&ops, "q %.3f 0 0 %.3f 0 0 cm /Im0 Do Q\n", box.w, box.h)
	case strings.TrimSpace(v.Text) != "":
		d.face = fonts.use(fontkit.Get(v.Font))
		used, err := drawTextUsing(&ops, flat, Item{Kind: KindText, Text: v.Text, Font: v.Font}, fonts)
		if err != nil {
			return drawn{}, err
		}
		d.faces = used
	}
	d.ops = ops.Bytes()
	return d, nil
}

// rgba is a decoded picture split the way a PDF image wants it: colour
// in one stream, transparency in its soft mask.
type rgba struct {
	w, h  int
	rgb   []byte
	alpha []byte
}

func decodeRGBA(pngBytes []byte) (*rgba, error) {
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, fmt.Errorf("pdfdoc: the picture is unreadable: %w", err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("pdfdoc: the picture is empty")
	}
	out := &rgba{w: w, h: h, rgb: make([]byte, 0, w*h*3), alpha: make([]byte, 0, w*h)}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			out.rgb = append(out.rgb, c.R, c.G, c.B)
			out.alpha = append(out.alpha, c.A)
		}
	}
	return out, nil
}

// addImage writes the picture as an image XObject with its soft mask and
// answers the image's object id.
func addImage(b *builder, px *rgba) int {
	mask := b.addStream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode",
		px.w, px.h), deflate(px.alpha))
	return b.addStream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /SMask %d 0 R",
		px.w, px.h, mask), deflate(px.rgb))
}

func deflate(raw []byte) []byte {
	var buf bytes.Buffer
	// The default level: the best one costs several times the time for a
	// few percent (measured on the signature pictures, pdfsig.ComposeScaled),
	// and inside the wasm runtime that time is the signer's wait.
	zw, _ := zlib.NewWriterLevel(&buf, zlib.DefaultCompression)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	return buf.Bytes()
}

// FieldNames lists the names of the form fields a document already
// carries — what a new box's identity must not collide with.
func FieldNames(in []byte) map[string]bool {
	out := map[string]bool{}
	rdr, err := pdf.NewReader(bytes.NewReader(in), int64(len(in)))
	if err != nil {
		return out
	}
	collectFieldNames(rdr.Trailer().Key("Root").Key("AcroForm").Key("Fields"), out, 0)
	return out
}

// ── reading the form back ──────────────────────────────────────────────

func collectFields(node pdf.Value, out map[string]pdf.Value, depth int) {
	if depth > 16 || node.IsNull() || node.Kind() != pdf.Array {
		return
	}
	for i := 0; i < node.Len(); i++ {
		f := node.Index(i)
		if name := f.Key("T").RawString(); name != "" {
			if _, seen := out[name]; !seen {
				out[name] = f
			}
		}
		collectFields(f.Key("Kids"), out, depth+1)
	}
}

func collectFieldNames(node pdf.Value, out map[string]bool, depth int) {
	got := map[string]pdf.Value{}
	collectFields(node, got, depth)
	for k := range got {
		out[k] = true
	}
}

func rectOf(f pdf.Value) (geometry.Rect, bool) {
	r := f.Key("Rect")
	if r.Kind() != pdf.Array || r.Len() < 4 {
		return geometry.Rect{}, false
	}
	x0, y0 := r.Index(0).Float64(), r.Index(1).Float64()
	x1, y1 := r.Index(2).Float64(), r.Index(3).Float64()
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	return geometry.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, x1-x0 > 0 && y1-y0 > 0
}

// ── small helpers ──────────────────────────────────────────────────────

func refs(ids []int) string {
	var b strings.Builder
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d 0 R", id)
	}
	return b.String()
}

// pdfTextString writes a PDF text string: plain when it is ASCII, UTF-16
// with a byte-order mark when it is not, so a Turkish label survives.
func pdfTextString(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			ascii = false
			break
		}
	}
	if ascii {
		r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
		return "(" + r.Replace(s) + ")"
	}
	var b strings.Builder
	b.WriteString("<FEFF")
	for _, r := range s {
		b.WriteString(utf16BE(r))
	}
	b.WriteByte('>')
	return b.String()
}
