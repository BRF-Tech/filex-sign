package app

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/geometry"
	"github.com/brf-tech/filex-sign/internal/host"
	"github.com/brf-tech/filex-sign/internal/pdfdoc"
	"github.com/brf-tech/filex-sign/internal/pdfsig"
	"github.com/brf-tech/filex-sign/internal/stamp"
	"github.com/brf-tech/filex-sign/internal/views"
)

// ── intake: the document, which has to be a PDF ────────────────────────
//
// ⚠⚠ The owner, 2026-10-02: an office document is not signed here, and is
// not converted here either. Turning a DOCX into a PDF is the Convert app's
// work; this app signs PDFs and needs no engine of any kind. It used to run
// LibreOffice itself (engines:libreoffice, a hidden `convert` action, a
// "Convert to PDF" screen), which made signing depend on a converter; that
// is gone. A document that is not a PDF is refused in words that say what
// to do instead (views.NotPDFWords).

// document is the PDF a job works on.
type document struct {
	Bytes []byte
	Info  *pdfsig.Info
}

func extOf(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 && i < len(name)-1 {
		return strings.ToLower(name[i+1:])
	}
	return ""
}

// openDocument reads the input and hands it back when it is a PDF. It
// goes by what the bytes are, not by the name: a PDF without the
// extension is still signed, and a .docx is refused whatever it carries.
func (a *App) openDocument(ref string) (*document, error) {
	raw, err := a.H.ReadInput(ref)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(bytes.TrimLeft(raw, "\xef\xbb\xbf \r\n\t"), []byte("%PDF-")) {
		return nil, pdfsig.ErrNotPDF
	}
	info, err := pdfsig.Inspect(raw)
	if err != nil {
		return nil, err
	}
	return &document{Bytes: raw, Info: info}, nil
}

// intakeWords turns a refusal into words the person can act on.
//
// ⚠ Never the error's own words: they are English, and they were spliced
// into a Turkish sentence ("Belge okunamadı: libreoffice produced no PDF
// (exit 1)"). The error itself goes to the app's log.
func (a *App) intakeWords(err error, name string) wire.Text {
	switch {
	case errors.Is(err, pdfsig.ErrEncrypted):
		return views.T("The PDF is password-protected. Remove the password and try again.", "PDF parola korumalı. Parolayı kaldırıp yeniden deneyin.")
	case errors.Is(err, pdfsig.ErrXFA):
		return views.T("This is an XFA form, which cannot be signed. Flatten it to a plain PDF first.", "Bu bir XFA formu; imzalanamaz. Önce düz PDF'e dönüştürün.")
	case errors.Is(err, pdfsig.ErrNotPDF):
		return views.NotPDFWords(name)
	}
	a.logf("warn", "reading %s: %v", name, err)
	return views.Tf("“%s” could not be read - it may be damaged, or not a kind of document this app can read.",
		"“%s” okunamadı - bozuk olabilir ya da bu uygulamanın okuyabildiği türden bir belge değil.", name)
}

// ── shared: one signature by one person ────────────────────────────────

type signRequest struct {
	Doc      []byte
	Info     *pdfsig.Info
	Name     string
	Email    string
	Reason   string
	Locale   string
	Field    *envelope.Field // where the visible signature goes (nil = invisible)
	Drawing  []byte          // the pad image (nil = text-only stamp)
	Location string
	// IP is the address the signing request came from — the visitor's on
	// a signing link, the person's own in the app — for the IP line a
	// requester may have chosen to print under the signature.
	IP string
	// SigField is the signature field this signature goes into (its /T
	// and, for a legacy document that does not carry it yet, how to make
	// it).
	SigField pdfdoc.FieldSpec
	// Certify makes this the document's certification signature (DocMDP
	// P); 0 = an approval signature.
	Certify int
}

// facts is what the signing record holds at the instant the signature is
// written: the person, the moment, the address, and the certificate that
// was issued for them a moment earlier. ⚠ These are the ONLY values that
// reach the lines under a signature (stamp.Lines) — never anything the
// requester typed.
func (a *App) facts(r signRequest, cert *x509.Certificate, when time.Time) stamp.Facts {
	f := stamp.Facts{Name: r.Name, Email: r.Email, When: when, IP: r.IP, Authority: a.ca().Name}
	if cert != nil {
		sum := sha256.Sum256(cert.Raw)
		f.CertFP = groupHex(hex.EncodeToString(sum[:]))
		f.Serial = strings.ToUpper(cert.SerialNumber.Text(16))
	}
	return f
}

type signResult struct {
	Out   []byte
	Cert  *x509.Certificate
	Chain []*x509.Certificate
	// CertDER is the leaf as it will be kept on the document's state, so
	// the receipt can be built again later — a certificate thrown away
	// the moment it is used is a receipt nobody can ever be given twice.
	CertDER     []byte
	CertSerial  string
	CertExpires string
	CertFP      string
	Timestamped bool
}

// issued is a certificate made for one signature, and the key behind it —
// alive only until release is called.
type issued struct {
	signer  crypto.Signer
	cert    *x509.Certificate
	chain   []*x509.Certificate
	expires string
	release func()
}

// issue has the signing authority make a certificate for the person.
//
// ⚠⚠ It runs FIRST now, before anything is put on the paper: the lines a
// requester may print under a signature include the certificate's
// fingerprint and serial, and a fact that does not exist yet cannot be
// printed. The key is destroyed by release — which the caller defers the
// moment it has one — whatever happens after the issue.
func (a *App) issue(r signRequest) (*issued, error) {
	// The certificate names the person: their name when they have one,
	// their address otherwise. The e-mail SAN is written only when there
	// IS an address — an empty field is worse than a missing one.
	iss, err := a.H.CertIssue(r.Name, r.Email, certDays)
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	release := func() {
		if err := a.H.KeyDestroy(iss.KeyRef); err != nil {
			a.logf("warn", "key_destroy %s: %v", iss.KeyRef, err)
		}
	}
	signer, cert, err := a.H.Signer(iss)
	if err != nil {
		release()
		return nil, err
	}
	return &issued{signer: signer, cert: cert, chain: host.ParseChainPEM(iss.ChainPEM), expires: iss.NotAfter, release: release}, nil
}

// signOnce signs with a certificate already issued (issue), drawing the
// visible signature with the lines its box chose.
func (a *App) signOnce(r signRequest, iss *issued, when time.Time) (*signResult, error) {
	var img []byte
	rotate := 0
	if r.Field != nil {
		box, err := r.Info.Box(r.Field.Page)
		if err != nil {
			return nil, err
		}
		rect := geometry.ToRect(geometry.Frac{X: r.Field.X, Y: r.Field.Y, W: r.Field.W, H: r.Field.H}, box)
		lines := stamp.Lines(r.Locale, stamp.Chosen(r.Field.Lines), a.facts(r, iss.cert, when))
		if img, err = pdfsig.Compose(rect, r.Drawing, lines); err != nil {
			return nil, err
		}
		rotate = box.Rotate
	}
	signer, cert, chain := iss.signer, iss.cert, iss.chain
	opts := pdfsig.Options{
		Signer: signer, Cert: cert, Chain: chain,
		Name: r.Name, Reason: r.Reason, Location: r.Location, ContactInfo: r.Email, When: when,
		Field: r.SigField.Name, Image: img, Rotate: rotate, Certify: r.Certify,
	}
	stamped := false
	var out []byte
	if url := a.tsa(); url != "" {
		opts.TSAURL = url
		if b, err := pdfsig.Sign(r.Doc, opts); err == nil {
			out, stamped = b, true
		} else {
			// A time-stamping authority that cannot be reached must not cost
			// anybody their signature: sign without one, and say so in the
			// report and in the trail.
			a.logf("warn", "time stamp from %s failed, signing without one: %v", url, err)
			opts.TSAURL = ""
		}
	}
	if out == nil {
		b, err := pdfsig.Sign(r.Doc, opts)
		if err != nil {
			return nil, err
		}
		out = b
	}
	sum := sha256.Sum256(cert.Raw)
	return &signResult{Out: out, Cert: cert, Chain: chain, CertDER: cert.Raw,
		CertSerial: strings.ToUpper(cert.SerialNumber.Text(16)), CertExpires: iss.expires,
		CertFP: groupHex(hex.EncodeToString(sum[:])), Timestamped: stamped}, nil
}

func groupHex(h string) string {
	var b strings.Builder
	for i := 0; i < len(h); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strings.ToUpper(h[i:min(i+4, len(h))]))
	}
	return b.String()
}

// ── putting the values on the paper ────────────────────────────────────

// plan is what one signer's submission puts on the document.
//
// Two shapes, and which one is used is the whole point of schema 3:
//
//	Items  — the old way: draw on the page. Kept for requests opened by
//	         an older build, whose boxes were never created as fields.
//	Specs  — the boxes the document should carry as real AcroForm fields,
//	         created once before anything of ours is signed.
//	Values — what this signer puts into them: the field's own value and
//	         appearance, and nothing else on the page.
type plan struct {
	Items    []pdfdoc.Item
	Specs    []pdfdoc.FieldSpec
	Values   []pdfdoc.FieldValue
	Consumed []envelope.Field
	// Visible is the box the signature widget goes into.
	Visible *envelope.Field
	// Pictures are the signer's OTHER signature boxes on the form path:
	// each is filled with its own drawing and its own chosen lines, drawn
	// only once the certificate exists (applyAndSign), because the lines
	// may name it.
	Pictures []picture
}

// picture is a signature box whose appearance is composed at signing.
type picture struct {
	Field   envelope.Field
	Drawing []byte
	Text    string // the field's value: the signer's name (or initials)
	Rotate  int
}

// planInput is everything planning needs to know.
type planInput struct {
	Info *pdfsig.Info
	// All is every box of the document, Mine the ones this signer may
	// act on. For somebody signing their own document the two are equal.
	All      []envelope.Field
	Mine     []envelope.Field
	Values   fill
	SignerID string
	Name     string
	Now      time.Time
	Lang     views.Lang
	// Form says the document carries (or is about to carry) real form
	// fields; false is the old page-drawing path, for older records.
	Form bool
	// Visible names every box a SIGNATURE WIDGET will go into, over the
	// whole request — those never become form fields, because pdfsign
	// puts its own widget there.
	Visible map[string]bool
	// Drawing is the signer's first drawing — what a signature box with no
	// drawing of its own shows.
	Drawing []byte
}

// planFill checks every value against its box's rules and turns the ones
// that pass into either stamps or form values. The refusal is the box's
// own name and words, so the signer is told which one is wrong and why.
func planFill(in planInput) (*plan, *fields.Problem) {
	p := &plan{Visible: firstDrawnField(in.Mine, in.SignerID)}
	visible := map[string]bool{}
	for k, v := range in.Visible {
		visible[k] = v
	}
	if p.Visible != nil {
		visible[p.Visible.ID] = true
	}

	if in.Form {
		for _, f := range in.All {
			if visible[f.ID] {
				continue
			}
			sp, ok := specFor(in.Info, in.All, f, in.Lang)
			if !ok {
				continue
			}
			p.Specs = append(p.Specs, sp)
		}
	}

	for i := range in.Mine {
		f := in.Mine[i]
		spec := f.Spec()
		val := strings.TrimSpace(in.Values[f.ID].Value)
		font := fields.NormalizeFont(pick(in.Values[f.ID].Font, f.Font))

		if fields.Drawn(f.Type) {
			if p.Visible != nil && f.ID == p.Visible.ID {
				continue // the signature widget draws this one
			}
			shown := in.Name
			if f.Type == fields.TypeInitials {
				shown = initialsOf(in.Name)
			}
			// A second box for the same person, on a form: its OWN drawing
			// (the pad asked for one per box) and its own lines, composed
			// when the certificate exists. Without a drawing of its own it
			// wears the first one.
			if in.Form {
				drawing := decodeImage(in.Values[f.ID].Value)
				if len(drawing) == 0 {
					drawing = in.Drawing
				}
				if len(drawing) > 0 {
					if box, err := in.Info.Box(f.Page); err == nil {
						p.Pictures = append(p.Pictures, picture{Field: f, Drawing: drawing, Text: shown, Rotate: box.Rotate})
						p.Consumed = append(p.Consumed, envelope.Field{ID: f.ID})
						continue
					}
				}
			}
			// The old page-drawing path (a request opened by an older
			// build), or no drawing at all: their name in their hand.
			if f.Font == "" {
				font = fontkit.Caveat
			}
			p.put(in, f, pdfdoc.KindText, shown, font, envelope.Field{ID: f.ID})
			continue
		}

		// A date box nobody typed into is today's date: that is what a
		// signer means by tapping it, and it is what they expect on paper.
		if f.Type == fields.TypeDate && val == "" && f.Required {
			val = in.Now.UTC().Format(fields.ISO)
		}
		if prob := fields.Check(spec, val); prob != nil {
			return nil, labelled(in.Lang, in.All, f, prob)
		}
		if val == "" {
			continue
		}
		if f.Type == fields.TypeCheckbox {
			if !fields.IsTicked(val) {
				continue
			}
			p.put(in, f, pdfdoc.KindCheck, "", font, envelope.Field{ID: f.ID, Value: "true"})
			continue
		}
		shown := fields.Display(spec, val)
		if shown == "" {
			continue
		}
		p.put(in, f, pdfdoc.KindText, shown, font, envelope.Field{ID: f.ID, Value: shown})
	}
	return p, nil
}

// put records one value the way this document takes values.
func (p *plan) put(in planInput, f envelope.Field, kind, text, font string, consumed envelope.Field) {
	box, err := in.Info.Box(f.Page)
	if err != nil {
		return
	}
	if in.Form {
		p.Values = append(p.Values, pdfdoc.FieldValue{
			Name: f.FormName(), Kind: kind, Text: text, Font: font, Rotate: box.Rotate})
		p.Consumed = append(p.Consumed, consumed)
		return
	}
	rect := geometry.ToRect(geometry.Frac{X: f.X, Y: f.Y, W: f.W, H: f.H}, box)
	p.Items = append(p.Items, pdfdoc.Item{Page: f.Page, Rect: rect, Rotate: box.Rotate,
		Kind: kind, Text: text, Font: font})
	p.Consumed = append(p.Consumed, consumed)
}

// specFor is one box as the PDF form field it becomes: named by its
// IDENTITY (/T — ASCII, derived, never shown) and titled by its NAME (/TU,
// the tooltip a reader shows — exactly as the requester typed it).
func specFor(info *pdfsig.Info, all []envelope.Field, f envelope.Field, l views.Lang) (pdfdoc.FieldSpec, bool) {
	box, err := info.Box(f.Page)
	if err != nil {
		return pdfdoc.FieldSpec{}, false
	}
	kind := pdfdoc.KindText
	if f.Type == fields.TypeCheckbox {
		kind = pdfdoc.KindCheck
	}
	rect := geometry.ToRect(geometry.Frac{X: f.X, Y: f.Y, W: f.W, H: f.H}, box)
	return pdfdoc.FieldSpec{Name: f.FormName(), Page: f.Page, Rect: rect, Rotate: box.Rotate,
		Kind: kind, Label: views.NameIn(l, all, f)}, true
}

// visibleBoxes is every box a signature widget will go into, worked out
// the same way for the whole request so a box is never both a form field
// and a signature.
func visibleBoxes(env *envelope.Envelope) map[string]bool {
	out := map[string]bool{}
	for i := range env.Signers {
		sg := env.Signers[i]
		if f := firstDrawnField(env.FieldsFor(sg.ID), sg.ID); f != nil {
			out[f.ID] = true
		}
	}
	return out
}

func labelled(l views.Lang, all []envelope.Field, f envelope.Field, p *fields.Problem) *fields.Problem {
	name := views.NameIn(l, all, f)
	q := *p
	q.Name = name
	return &q
}

func pick(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// initialsOf is the first letter of each word, for an initials box.
func initialsOf(name string) string {
	var b strings.Builder
	for _, part := range strings.Fields(name) {
		r := []rune(part)
		if len(r) == 0 {
			continue
		}
		b.WriteString(strings.ToUpper(string(r[0])))
	}
	if b.Len() == 0 {
		return "-"
	}
	return b.String()
}

// applyAndSign puts the values on the paper and then signs the result,
// in that order: the signature has to cover what it was given to sign.
//
// ⚠ On the form path the values become field values and appearance
// streams — NOT page content. That is what lets a second signer fill
// their boxes without making the first signature read as "the document
// changed after it was signed".
func (a *App) applyAndSign(doc *document, p *plan, r signRequest) (*signResult, error) {
	iss, err := a.issue(r)
	if err != nil {
		return nil, err
	}
	defer iss.release()
	// ONE instant for the whole signature: the date line printed in every
	// box, and the signing time inside the signature itself.
	when := a.H.Now()
	facts := a.facts(r, iss.cert, when)
	for _, pic := range p.Pictures {
		box, err := doc.Info.Box(pic.Field.Page)
		if err != nil {
			return nil, err
		}
		rect := geometry.ToRect(geometry.Frac{X: pic.Field.X, Y: pic.Field.Y, W: pic.Field.W, H: pic.Field.H}, box)
		img, err := pdfsig.Compose(rect, pic.Drawing, stamp.Lines(r.Locale, stamp.Chosen(pic.Field.Lines), facts))
		if err != nil {
			return nil, err
		}
		p.Values = append(p.Values, pdfdoc.FieldValue{Name: pic.Field.FormName(), Kind: pdfdoc.KindImage,
			Text: pic.Text, Image: img, Rotate: pic.Rotate})
	}
	body := doc.Bytes
	if len(p.Specs) > 0 {
		prepared, err := pdfdoc.PrepareForm(body, p.Specs)
		if err != nil {
			return nil, fmt.Errorf("preparing the fields: %w", err)
		}
		body = prepared
	}
	if len(p.Values) > 0 {
		filled, err := pdfdoc.FillForm(body, p.Values)
		if err != nil {
			return nil, fmt.Errorf("filling the fields: %w", err)
		}
		body = filled
	}
	if len(p.Items) > 0 {
		stamped, err := pdfdoc.Stamp(body, p.Items)
		if err != nil {
			return nil, fmt.Errorf("filling the fields: %w", err)
		}
		body = stamped
	}
	body, r.SigField, err = ensureSigField(body, r.SigField)
	if err != nil {
		return nil, err
	}
	r.Doc = body
	r.Info = doc.Info
	r.Field = p.Visible
	return a.signOnce(r, iss, when)
}

// ── the signature fields, the certification and the seal ───────────────
//
// ⚠⚠ The owner, 2026-09-22: "after a document is fully signed and someone
// changes it, does the signature say so — or do we lock the file? Both,
// plus a seal." What that takes, in the order it happens:
//
//  1. Every SIGNATURE field is created with the form, before anything of
//     ours is signed (sigSpecs → pdfdoc.PrepareForm): each signer's first
//     signature box, an invisible field for a signer with none, and the
//     seal's — invisible, and locked (/Lock P=1).
//  2. The FIRST signature certifies the document, DocMDP P=2: from then on
//     filling in the form and signing are permitted, nothing else. A
//     document that already carried somebody else's signature cannot be
//     certified (a certification must be the first), and is not.
//  3. Every later signature fills its own field and signs its own field —
//     exactly what P=2 permits, so none of them trips the certification.
//  4. The moment the LAST signature lands, filex seals the whole document
//     with the installation's own key (sealDocument), into the locked
//     field: after that, a reader reports ANY change as not permitted.
//     The seal, not the last signer, carries the lock: a lock on the last
//     signer's signature would make the seal itself a forbidden change.
//  5. The SHA-256 of exactly those sealed bytes is what is written, what
//     is delivered, and what every party is told.

// SealField is the /T of the platform seal's field.
const SealField = "filex-seal"

// sealSpec is the seal's field: invisible, on the first page, locked.
func sealSpec() pdfdoc.FieldSpec {
	return pdfdoc.FieldSpec{Name: SealField, Page: 1, Kind: pdfdoc.KindSig, Hidden: true, Lock: true, Label: "filex seal"}
}

// sigSpecs are a document's signature fields: one per signer — the box
// their signature widget goes into (firstDrawnField, the same rule
// visibleBoxes follows), or an invisible field for a signer with no
// signature box — and the seal's. fieldsOf answers a signer's boxes.
func sigSpecs(info *pdfsig.Info, all []envelope.Field, signers []string, fieldsOf func(string) []envelope.Field, l views.Lang) (map[string]pdfdoc.FieldSpec, []pdfdoc.FieldSpec) {
	bySigner := map[string]pdfdoc.FieldSpec{}
	var out []pdfdoc.FieldSpec
	seen := map[string]bool{}
	for _, id := range signers {
		var sp pdfdoc.FieldSpec
		if f := firstDrawnField(fieldsOf(id), id); f != nil {
			if s, ok := specFor(info, all, *f, l); ok {
				sp = s
				sp.Kind = pdfdoc.KindSig
			}
		}
		if sp.Name == "" {
			name := "signature"
			if id != "" {
				name += "-" + id
			}
			sp = pdfdoc.FieldSpec{Name: name, Page: 1, Kind: pdfdoc.KindSig, Hidden: true}
		}
		bySigner[id] = sp
		if !seen[sp.Name] {
			seen[sp.Name] = true
			out = append(out, sp)
		}
	}
	return bySigner, append(out, sealSpec())
}

// ensureSigField makes sure the signature field exists and is empty: a
// request opened by an older build never created it, and a box that
// belongs to ANYONE may already carry the first taker's signature — the
// second then gets a field of its own beside it. Either is a new field
// after whatever was signed before, which only an uncertified document
// ever needs, or — for "anyone" — what P=2 permits as signing.
func ensureSigField(body []byte, sp pdfdoc.FieldSpec) ([]byte, pdfdoc.FieldSpec, error) {
	exists, signed := pdfdoc.SigFieldState(body, sp.Name)
	if exists && !signed {
		return body, sp, nil
	}
	if signed {
		base := sp.Name
		for i := 2; ; i++ {
			sp.Name = fmt.Sprintf("%s-%d", base, i)
			if e, _ := pdfdoc.SigFieldState(body, sp.Name); !e {
				break
			}
		}
	}
	out, err := pdfdoc.PrepareForm(body, []pdfdoc.FieldSpec{sp})
	if err != nil {
		return nil, sp, fmt.Errorf("preparing the signature field: %w", err)
	}
	return out, sp, nil
}

// sealed is what the seal left.
type sealed struct {
	Out    []byte
	SHA256 string // lower-case hex of Out
	CertFP string // the seal certificate's fingerprint, grouped
}

// sealDocument signs the finished document with the installation's own
// seal (host: cert_issue purpose "platform"), into its locked field, and
// hashes the result. ⚠ The hash is taken of the bytes RETURNED — after the
// seal — because those are the bytes written, delivered and announced.
func (a *App) sealDocument(body []byte, locale string, when time.Time) (*sealed, error) {
	iss, err := a.H.PlatformSeal()
	if err != nil {
		return nil, fmt.Errorf("the platform seal: %w", err)
	}
	signer, cert, err := a.H.Signer(iss)
	if err != nil {
		return nil, err
	}
	body, sp, err := ensureSigField(body, sealSpec())
	if err != nil {
		return nil, err
	}
	opts := pdfsig.Options{
		Signer: signer, Cert: cert, Chain: host.ParseChainPEM(iss.ChainPEM),
		// ⚠ The certificate's own name, not "filex": Verify holds a typed
		// /Name against the certificate, and a seal named otherwise than
		// its certificate was reported as "the certificate says
		// otherwise" — about our own seal (2026-09-22, e2e 101).
		Name: cert.Subject.CommonName, When: when, Field: sp.Name,
		Reason: a.say(locale, "Sealed by filex when every signature was in", "Tüm imzalar tamamlandığında filex tarafından mühürlendi"),
	}
	var out []byte
	if url := a.tsa(); url != "" {
		opts.TSAURL = url
		if b, err := pdfsig.Sign(body, opts); err == nil {
			out = b
		} else {
			a.logf("warn", "time stamp for the seal from %s failed, sealing without one: %v", url, err)
			opts.TSAURL = ""
		}
	}
	if out == nil {
		if out, err = pdfsig.Sign(body, opts); err != nil {
			return nil, err
		}
	}
	sum := sha256.Sum256(out)
	fp := sha256.Sum256(cert.Raw)
	return &sealed{Out: out, SHA256: hex.EncodeToString(sum[:]), CertFP: groupHex(hex.EncodeToString(fp[:]))}, nil
}

// ── where the result goes ──────────────────────────────────────────────

// outputOf is the job's effective output: what the screen chose, or the
// envelope's own choice when a job arrives without one.
func outputOf(in *wire.ActionRunInput, fallback envelope.Output) envelope.Output {
	if in.Output.Mode != "" {
		return envelope.Output{Mode: in.Output.Mode, Name: in.Output.Name}.Normalized()
	}
	return fallback.Normalized()
}

// writeSigned writes the signed PDF under the name the effective output
// asks for.
func (a *App) writeSigned(in *wire.ActionRunInput, out envelope.Output, body []byte) (wire.OutputRef, string, error) {
	inputName := in.Inputs[0].Name
	name := inputName
	if out.Mode == envelope.OutputSibling {
		name = expandName(out.Name, inputName)
	}
	ref, err := a.H.WriteOutput(name, body)
	return ref, name, err
}

// ── sign (yourself) ────────────────────────────────────────────────────

func (a *App) actionSign(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
	if len(in.Inputs) == 0 {
		return fail("No document was given.", "Belge verilmedi.")
	}
	// A `sign` job with op=convert is what a "Convert to PDF" screen before
	// 0.1.1 queued. This app converts nothing any more; such a job, still in
	// a queue across the upgrade, is answered in words instead of signing
	// something nobody placed a box on.
	if str(in.Params, "op") == "convert" {
		return failText(views.NotPDFWords(in.Inputs[0].Name))
	}
	if reason, ok := a.signingReady(); !ok {
		return failf("Signing is not available: %s", "İmzalama kullanılamıyor: %s", views.SigningUnavailable(reason))
	}
	l := views.Of(in.Locale)
	a.step(in.Locale, 1, 4, "reading the document", "belge okunuyor")
	doc, err := a.openDocument(in.Inputs[0].Ref)
	if err != nil {
		return failText(a.intakeWords(err, in.Inputs[0].Name))
	}
	flds, err := parseFields(in.Params["fields"], fontkit.DefaultID, l)
	if err != nil {
		return failPlain(err.Error())
	}
	who := actorPerson(&in.Actor)
	values := parseFill(in.Params["values"])
	// The boxes' identities (their names in the PDF), derived now — this is
	// the moment signing your own document "sends" it.
	envelope.AssignKeys(flds, pdfdoc.FieldNames(doc.Bytes))
	drawing := decodeImage(str(in.Params, "png_b64"))
	p, prob := planFill(planInput{Info: doc.Info, All: flds, Mine: flds, Values: values,
		Name: who.CertName(), Now: a.H.Now(), Lang: l, Form: true, Drawing: drawing})
	if prob != nil {
		return failText(views.Problem(prob))
	}
	// Signing your own document is a request with one signer, and it ends
	// the same way: the signature certifies (when it is the document's
	// first), filex seals, and the hash of the sealed bytes is the answer.
	self := func(string) []envelope.Field { return flds }
	_, specs := sigSpecs(doc.Info, flds, []string{""}, self, l)
	p.Specs = append(p.Specs, specs...)
	certify := 0
	if doc.Info.Signatures == 0 {
		certify = CertifyPermission
	}
	out := outputOf(in, envelope.Output{Mode: envelope.OutputSibling, Name: envelope.DefaultSiblingName})
	a.step(in.Locale, 2, 4, "issuing a certificate for %s", "%s için sertifika üretiliyor", who.CertName())
	res, err := a.applyAndSign(doc, p, signRequest{
		Name: who.CertName(), Email: who.Email, Reason: str(in.Params, "reason"), Locale: in.Locale,
		Drawing: drawing, IP: str(in.Params, "visitor_ip"),
		SigField: ownSigSpec(doc.Info, flds, p.Visible, "", l), Certify: certify,
	})
	if err != nil {
		return failf("Signing failed: %v", "İmzalama başarısız: %v", err)
	}
	seal, err := a.sealDocument(res.Out, in.Locale, a.H.Now())
	if err != nil {
		return failf("The document could not be sealed: %v", "Belge mühürlenemedi: %v", err)
	}
	a.step(in.Locale, 3, 4, "writing the signed copy", "imzalı kopya yazılıyor")
	ref, outName, err := a.writeSigned(in, out, seal.Out)
	if err != nil {
		return nil, err
	}
	a.markSigned(in.Inputs[0].Ref, in.Actor.ID)
	if err := a.H.StateSet(in.Inputs[0].Ref, envelope.SealedKey, seal.SHA256+" "+outName); err != nil {
		a.logf("warn", "recording the sealed hash: %v", err)
	}
	a.step(in.Locale, 4, 4, "done", "bitti")
	return &wire.ActionRunOutput{OK: true, Outputs: []wire.OutputRef{ref}, Message: views.Each(func(l views.Lang) string {
		what := outName
		if out.Mode == envelope.OutputVersion {
			what = l.Sf("%s (new version)", "%s (yeni sürüm)", in.Inputs[0].Name)
		}
		return l.Sf("Signed as %s → %s · certificate %s · sealed by filex · SHA-256 %s",
			"%s olarak imzalandı → %s · sertifika %s · filex tarafından mühürlendi · SHA-256 %s", who.CertName(), what, res.CertFP, seal.SHA256)
	})}, nil
}

// ── verify ─────────────────────────────────────────────────────────────

// actionVerify exists so the menu row does; the screen it opens does the
// reading. A run that reaches here answers in words rather than quietly
// doing nothing.
func (a *App) actionVerify(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
	if len(in.Inputs) == 0 {
		return fail("No document was given.", "Belge verilmedi.")
	}
	rep, err := a.report(in.Inputs[0].Ref)
	if err != nil {
		return failText(a.intakeWords(err, in.Inputs[0].Name))
	}
	if !rep.Signed() {
		return fail("This document carries no electronic signature.", "Bu belgede elektronik imza yok.")
	}
	return &wire.ActionRunOutput{OK: true, Message: views.Tf("%s carries %d signature(s). Open “Verify” for the report.",
		"%s içinde %d imza var. Rapor için “Doğrula” ekranını açın.", in.Inputs[0].Name, len(rep.Signatures))}, nil
}

// ── odds and ends ──────────────────────────────────────────────────────

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(h[:])
}

func shortErr(err error) string {
	var he *pluginkit.HostError
	if errors.As(err, &he) {
		return he.Code
	}
	// ⚠ A code, never the error's own (English) words: this is spliced
	// into a person's sentence through views.ErrWords, and stored on the
	// envelope to be said later in the requester's language. The caller
	// logs the error itself.
	return "internal"
}

func b64of(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
