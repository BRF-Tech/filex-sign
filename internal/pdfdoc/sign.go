package pdfdoc

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/digitorus/pdf"
)

// ── Writing a signature into a signature field ─────────────────────────
//
// ⚠⚠ Why this is ours and not digitorus/pdfsign's (2026-09-22). The owner
// decided that a finished document must SAY when it was changed afterwards
// — "changes not permitted", not merely "modified after signing" — which
// takes three things the library could not give:
//
//  1. A CERTIFICATION signature (DocMDP P=2) as the FIRST signature: the
//     library writes the /Reference but never the catalogue's /Perms
//     /DocMDP entry, without which a reader does not treat the document as
//     certified at all; and it refuses a VISIBLE certification signature,
//     while the first signer's box is visible.
//  2. Signatures that fill fields prepared BEFORE the certification: under
//     P=2 a later signer may fill in forms and sign, and signing an empty
//     signature field is the one shape every reader accepts. The library
//     creates a new field and widget for every signature and rewrites the
//     catalogue's form dictionary (dropping /DR and /DA) each time.
//  3. A LOCKED last signature: the field's /Lock (P=1) must be echoed as a
//     FieldMDP reference in the signature, the way Acrobat's "lock document
//     after signing" writes it.
//
// What stays the library's: the CMS (digitorus/pkcs7) and the reading
// (digitorus/pdf). What is here is PDF: the signature dictionary, the
// field's value and appearance, the catalogue's /Perms, one incremental
// update — and the byte range, which is where a signature lives or dies.

// SigRequest is one signature to write into an existing, unsigned /Sig
// field.
type SigRequest struct {
	Field       string // the field's /T
	Name        string
	Reason      string
	Location    string
	ContactInfo string
	When        time.Time
	// Certify makes this the certification (author) signature with the
	// DocMDP permission P (1–3). 0 is an approval signature.
	Certify int
	// Image is the widget's appearance as a PNG; nil leaves the widget as
	// it is (an invisible field — the seal).
	Image  []byte
	Rotate int
	// Reserve is the room kept for the CMS, in bytes.
	Reserve int
}

// Prepared is a document with a signature dictionary whose /Contents is
// still empty: Content is what the signature must cover, Embed puts it in.
type Prepared struct {
	doc   []byte
	start int // offset of '<'
	end   int // offset just after '>'
}

// Content is the bytes the signature covers: everything but /Contents.
func (p *Prepared) Content() []byte {
	out := make([]byte, 0, len(p.doc)-(p.end-p.start))
	out = append(out, p.doc[:p.start]...)
	return append(out, p.doc[p.end:]...)
}

// ByteRange is the signature's /ByteRange as written.
func (p *Prepared) ByteRange() [4]int {
	return [4]int{0, p.start, p.end, len(p.doc) - p.end}
}

// Embed writes the CMS into /Contents and returns the signed document.
func (p *Prepared) Embed(cms []byte) ([]byte, error) {
	h := strings.ToUpper(hex.EncodeToString(cms))
	room := p.end - p.start - 2
	if len(h) > room {
		return nil, fmt.Errorf("pdfdoc: the signature is %d bytes, %d were reserved", len(cms), room/2)
	}
	out := append([]byte(nil), p.doc...)
	copy(out[p.start+1:], h)
	return out, nil
}

const byteRangePlaceholder = "/ByteRange [0 ********** ********** **********]"

// ErrFieldSigned is PrepareSignature's answer for a field that already
// carries a signature: a signature is never written over another one.
var ErrFieldSigned = errors.New("pdfdoc: that signature field is already signed")

// PrepareSignature appends ONE incremental update: the signature
// dictionary (its /Contents reserved), the field pointing at it with its
// appearance, and — for a certification — the catalogue's /Perms. Nothing
// else is touched: the pages, their content and their /Annots stay the
// objects they were, which is what lets every earlier signature, and a
// certification's P=2, hold.
func PrepareSignature(in []byte, r SigRequest) (*Prepared, error) {
	if r.Certify < 0 || r.Certify > 3 {
		return nil, fmt.Errorf("pdfdoc: DocMDP permission %d is not 1–3", r.Certify)
	}
	if r.Reserve <= 0 {
		r.Reserve = 16 << 10
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
	acro := root.Key("AcroForm")
	fields := map[string]pdf.Value{}
	collectFields(acro.Key("Fields"), fields, 0)
	field, ok := fields[r.Field]
	if !ok {
		return nil, fmt.Errorf("pdfdoc: the document has no signature field %q", r.Field)
	}
	if field.Key("FT").Name() != "Sig" {
		return nil, fmt.Errorf("pdfdoc: field %q is not a signature field", r.Field)
	}
	if !field.Key("V").IsNull() {
		return nil, ErrFieldSigned
	}
	if r.Certify > 0 {
		if !root.Key("Perms").Key("DocMDP").IsNull() {
			return nil, errors.New("pdfdoc: the document is already certified; a document has one certification signature")
		}
	}

	b := newBuilder(facts.size)

	// The appearance first, so the field can name it.
	ap := 0
	if len(r.Image) > 0 {
		rect, ok := rectOf(field)
		if !ok {
			return nil, fmt.Errorf("pdfdoc: field %q has no rectangle", r.Field)
		}
		d, err := drawValue(newFontSet(), rect, FieldValue{Name: r.Field, Kind: KindImage, Image: r.Image, Rotate: r.Rotate})
		if err != nil {
			return nil, err
		}
		if d.pixels != nil {
			d.imgObj = addImage(b, d.pixels)
		}
		ap = b.addStream(d.dict(), d.ops)
	}

	rootID := int(root.GetPtr().GetID())
	sig := b.add(sigDict(r, field, fmt.Sprintf("%d %d R", rootID, root.GetPtr().GetGen())))

	fptr := field.GetPtr()
	b.replace(int(fptr.GetID()), int(fptr.GetGen()), signedField(field, sig, ap))

	// The form must say it carries signatures (SigFlags 3: SignaturesExist
	// and AppendOnly). PrepareForm already wrote it for fields it created;
	// a document prepared elsewhere gets it here.
	needFlags := acro.Key("SigFlags").Int64()&3 != 3
	switch {
	case r.Certify > 0:
		b.replace(rootID, int(root.GetPtr().GetGen()), certifiedCatalogue(root, sig, needFlags))
		if needFlags && isRef(acro, rootID) {
			ptr := acro.GetPtr()
			b.replace(int(ptr.GetID()), int(ptr.GetGen()), acroFormWithFlags(acro, int(ptr.GetID())))
		}
	case needFlags:
		if isRef(acro, rootID) {
			ptr := acro.GetPtr()
			b.replace(int(ptr.GetID()), int(ptr.GetGen()), acroFormWithFlags(acro, int(ptr.GetID())))
		} else {
			b.replace(rootID, int(root.GetPtr().GetGen()), certifiedCatalogue(root, 0, true))
		}
	}

	out, err := appendUpdate(in, b, facts)
	if err != nil {
		return nil, err
	}
	return locate(out, r.Reserve)
}

// sigDict is the signature dictionary with its two placeholders.
func sigDict(r SigRequest, field pdf.Value, rootRef string) []byte {
	var b bytes.Buffer
	b.WriteString("<< /Type /Sig /Filter /Adobe.PPKLite /SubFilter /ETSI.CAdES.detached\n")
	b.WriteString(byteRangePlaceholder + "\n")
	b.WriteString("/Contents <")
	b.Write(bytes.Repeat([]byte("0"), 2*r.Reserve))
	b.WriteString(">\n")
	var refs []string
	if r.Certify > 0 {
		// The certification: what may still happen to the document (P=2:
		// filling in forms and signing, nothing else).
		refs = append(refs, fmt.Sprintf("<< /Type /SigRef /TransformMethod /DocMDP /TransformParams << /Type /TransformParams /P %d /V /1.2 >> >>", r.Certify))
	}
	if lock := field.Key("Lock"); lock.Kind() == pdf.Dict {
		// The field's lock, echoed as a FieldMDP reference the way Acrobat's
		// "lock document after signing" writes it — /P included, which is
		// PDF 2.0's field-lock permission and what makes P=1 lock the whole
		// document, not just the form fields.
		var tp strings.Builder
		tp.WriteString("<< /Type /TransformParams")
		action := lock.Key("Action").Name()
		if action == "" {
			action = "All"
		}
		fmt.Fprintf(&tp, " /Action /%s", action)
		if fs := lock.Key("Fields"); fs.Kind() == pdf.Array {
			tp.WriteString(" /Fields [")
			for i := 0; i < fs.Len(); i++ {
				if i > 0 {
					tp.WriteByte(' ')
				}
				tp.WriteString(pdfString(fs.Index(i).RawString()))
			}
			tp.WriteString("]")
		}
		if p := lock.Key("P"); p.Kind() == pdf.Integer {
			fmt.Fprintf(&tp, " /P %d", p.Int64())
		}
		tp.WriteString(" /V /1.2 >>")
		refs = append(refs, fmt.Sprintf("<< /Type /SigRef /TransformMethod /FieldMDP /Data %s /TransformParams %s >>", rootRef, tp.String()))
	}
	if len(refs) > 0 {
		b.WriteString("/Reference [" + strings.Join(refs, " ") + "]\n")
	}
	if r.Name != "" {
		b.WriteString("/Name " + pdfTextString(r.Name) + "\n")
	}
	if r.Reason != "" {
		b.WriteString("/Reason " + pdfTextString(r.Reason) + "\n")
	}
	if r.Location != "" {
		b.WriteString("/Location " + pdfTextString(r.Location) + "\n")
	}
	if r.ContactInfo != "" {
		b.WriteString("/ContactInfo " + pdfTextString(r.ContactInfo) + "\n")
	}
	when := r.When
	if when.IsZero() {
		when = time.Now()
	}
	// /M is PAdES' claimed signing time: the CMS carries no signing-time
	// attribute (ETSI EN 319 142-1).
	b.WriteString("/M " + pdfDate(when) + "\n")
	b.WriteString("/Prop_Build << /App << /Name /filex-sign >> >>\n>>")
	return b.Bytes()
}

// pdfDate is a PDF date string: (D:YYYYMMDDHHmmSS+HH'mm').
func pdfDate(t time.Time) string {
	_, off := t.Zone()
	sign := '+'
	if off < 0 {
		sign, off = '-', -off
	}
	return fmt.Sprintf("(D:%s%c%02d'%02d')", t.Format("20060102150405"), sign, off/3600, (off%3600)/60)
}

// signedField is the field with its value and appearance; every other
// entry — /Lock included — is copied as it stands.
func signedField(f pdf.Value, sig, ap int) []byte {
	id := int(f.GetPtr().GetID())
	var b bytes.Buffer
	b.WriteString("<<")
	for _, key := range f.Keys() {
		switch key {
		case "V":
			continue
		case "AP":
			if ap != 0 {
				continue
			}
		}
		fmt.Fprintf(&b, " /%s %s", key, serialise(f.Key(key), id))
	}
	fmt.Fprintf(&b, " /V %d 0 R", sig)
	if ap != 0 {
		fmt.Fprintf(&b, " /AP << /N %d 0 R >>", ap)
	}
	b.WriteString(" >>")
	return b.Bytes()
}

// certifiedCatalogue is the catalogue with /Perms /DocMDP pointing at the
// certification (sig 0 writes no /Perms) and, when asked, a direct
// /AcroForm given /SigFlags 3.
func certifiedCatalogue(root pdf.Value, sig int, flags bool) []byte {
	rootID := int(root.GetPtr().GetID())
	var b bytes.Buffer
	b.WriteString("<<\n")
	keys := root.Keys()
	sort.Strings(keys)
	for _, key := range keys {
		switch key {
		case "Perms":
			if sig != 0 {
				continue
			}
		case "AcroForm":
			if flags && !isRef(root.Key(key), rootID) {
				fmt.Fprintf(&b, "  /AcroForm %s\n", acroFormWithFlags(root.Key(key), rootID))
				continue
			}
		}
		fmt.Fprintf(&b, "  /%s %s\n", key, serialise(root.Key(key), rootID))
	}
	if sig != 0 {
		b.WriteString("  /Perms <<")
		perms := root.Key("Perms")
		for _, k := range perms.Keys() {
			if k == "DocMDP" {
				continue
			}
			fmt.Fprintf(&b, " /%s %s", k, serialise(perms.Key(k), rootID))
		}
		fmt.Fprintf(&b, " /DocMDP %d 0 R >>\n", sig)
	}
	b.WriteString(">>")
	return b.Bytes()
}

// acroFormWithFlags is the form dictionary with /SigFlags 3.
func acroFormWithFlags(acro pdf.Value, ownerID int) []byte {
	var b bytes.Buffer
	b.WriteString("<<")
	for _, key := range acro.Keys() {
		if key == "SigFlags" {
			continue
		}
		fmt.Fprintf(&b, " /%s %s", key, serialise(acro.Key(key), ownerID))
	}
	b.WriteString(" /SigFlags 3 >>")
	return b.Bytes()
}

// locate finds the placeholders the update just wrote and fills in the
// byte range. Both are searched from the END: the new update is the last
// thing in the file, and an earlier signature's /Contents never starts
// with a run of zeros.
func locate(out []byte, reserve int) (*Prepared, error) {
	br := bytes.LastIndex(out, []byte(byteRangePlaceholder))
	contents := bytes.LastIndex(out, append([]byte("/Contents <"), bytes.Repeat([]byte("0"), 2*reserve)...))
	if br < 0 || contents < 0 || contents < br {
		return nil, errors.New("pdfdoc: the signature placeholders were not written")
	}
	start := contents + len("/Contents ")
	end := start + 2 + 2*reserve
	if out[end-1] != '>' {
		return nil, errors.New("pdfdoc: the signature placeholder is malformed")
	}
	numbers := fmt.Sprintf("/ByteRange [0 %-10d %-10d %-10d]", start, end, len(out)-end)
	if len(numbers) != len(byteRangePlaceholder) {
		return nil, errors.New("pdfdoc: the document is too large to sign")
	}
	copy(out[br:], numbers)
	return &Prepared{doc: out, start: start, end: end}, nil
}

// SigFieldState reports whether the document has a signature field named
// name, and whether it is signed already.
func SigFieldState(in []byte, name string) (exists, signed bool) {
	rdr, err := pdf.NewReader(bytes.NewReader(in), int64(len(in)))
	if err != nil {
		return false, false
	}
	fields := map[string]pdf.Value{}
	collectFields(rdr.Trailer().Key("Root").Key("AcroForm").Key("Fields"), fields, 0)
	f, ok := fields[name]
	if !ok || f.Key("FT").Name() != "Sig" {
		return ok, false
	}
	return true, !f.Key("V").IsNull()
}
