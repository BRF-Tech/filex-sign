package views

import (
	"fmt"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/stamp"
)

// Node ids the app reads values from.
const (
	IDFields  = "fields"
	IDPad     = "pad"
	IDSigners = "signers"
)

// PadFor is the id of the signature pad drawn for one named box on the
// signer's fill step. Each signature box gets its own pad under its own
// name, because "sign here" and "and initial here" are two questions.
func PadFor(fieldID string) string { return IDPad + ":" + fieldID }

// Doc names the document a `pdf-fields` node draws.
//
// Ref is the call-scoped handle (`in:0` in a view, `pub:0` on a public
// page). Path is the adapter-qualified storage path (`docs://a/b.pdf`)
// when the call knows it; a view resolves `src.path` through the
// explorer's own preview route and `src.ref` through the page's file
// resolver, so both are sent and the renderer picks what it can load.
type Doc struct {
	Ref  string
	Path string
	Name string
	// Authority is the installation's signing authority, by name — what
	// the "signing authority" line under a signature prints.
	Authority string
	// ReadOnly: the document's storage takes no writes (the host says so on
	// every input, wire.FileRef.ReadOnly).
	ReadOnly bool
}

func (d Doc) src() map[string]any {
	m := map[string]any{}
	if d.Ref != "" {
		m["ref"] = d.Ref
	}
	if d.Path != "" {
		m["path"] = d.Path
	}
	return m
}

// SignerLabel is what the editor shows for one signer.
type SignerLabel struct {
	ID    string
	Label string
	Color string
	// Name and Email are the person behind the label, for the define
	// step's preview of what will be printed under their signature.
	Name  string
	Email string
	// Self marks the label of the person at the screen (signing their own
	// document): their preview says "you", and their address is KNOWN (IP,
	// from context.actor.ip) rather than "given when they sign".
	Self bool
	IP   string
}

var palette = []string{"#2563eb", "#16a34a", "#d97706", "#db2777", "#7c3aed", "#0891b2", "#dc2626", "#65a30d"}

// SignerLabels builds the editor's signer list from an envelope's signers.
func SignerLabels(signers []envelope.Signer) []SignerLabel {
	out := make([]SignerLabel, 0, len(signers))
	for i, s := range signers {
		out = append(out, SignerLabel{ID: s.ID, Label: s.Person.Display(), Color: palette[i%len(palette)],
			Name: s.Person.CertName(), Email: s.Person.Email})
	}
	return out
}

func signersProp(labels []SignerLabel) []map[string]any {
	var out []map[string]any
	for _, l := range labels {
		out = append(out, map[string]any{"id": l.ID, "label": Plain(l.Label), "color": l.Color})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out
}

// editorProps are the props every `pdf-fields` node carries: what may be
// placed, the catalogues the editor offers per box, and the NAMES a box
// is offered by default. A box has a name because the signer fills a
// form of names, not a hunt across a page.
func editorProps(l Lang, doc Doc, mode string, fs []envelope.Field, labels []SignerLabel) map[string]any {
	return map[string]any{
		"src": doc.src(), "mode": mode, "fields": fieldsProp(fs), "signers": signersProp(labels),
		"types": fields.Types(), "fonts": FontOptions(l), "rules": RuleOptions(l),
		"formats": FormatOptions(), "date_labels": DateLabels(), "labels": DefaultLabels(l),
		"stamp_lines": StampLineOptions(labels, doc.Authority, previewClock),
	}
}

// previewClock is the moment a define-step preview shows in its date line.
// A variable so a test can pin it; the screens use the real clock.
var previewClock = time.Now

// StampLineOptions is the catalogue a signature box chooses its lines
// from — the node's `stamp_lines`: each line's name, and how THIS app will
// word it for each signer (`examples`, keyed by signer id, `*` for a box
// that belongs to anyone), so the define card previews the paper in the
// app's own words.
//
// ⚠ Worded by stamp.Lines, the function that prints them — never here. The
// values are the signer's real name and address as the request knows them;
// the facts nobody knows yet (their address on the day, the certificate)
// say when they will be known.
func StampLineOptions(labels []SignerLabel, authority string, now func() time.Time) []map[string]any {
	when := now()
	facts := func(name, email string) stamp.Facts {
		return stamp.Facts{Name: name, Email: email, When: when, PendingIP: true, PendingCert: true,
			PendingWho: stamp.They, Authority: authority}
	}
	factsOf := func(lab SignerLabel) stamp.Facts {
		f := facts(lab.Name, lab.Email)
		if lab.Self {
			f.PendingWho = stamp.You
			if lab.IP != "" {
				f.IP, f.PendingIP = lab.IP, false
			}
		}
		return f
	}
	anyone := func(lang string) stamp.Facts {
		f := facts(Lang(lang).S("Whoever signs", "İmzalayan kişi"), "")
		return f
	}
	out := make([]map[string]any, 0, len(stamp.IDs()))
	isDefault := map[string]bool{}
	for _, id := range stamp.Defaults() {
		isDefault[id] = true
	}
	for _, id := range stamp.IDs() {
		examples := map[string]any{}
		for _, lab := range labels {
			ex := wire.Text{}
			for _, lang := range Languages() {
				lines := stamp.Lines(lang, []string{id}, factsOf(lab))
				if len(lines) == 0 {
					// Only an address can be missing: say it will be left
					// out, which is what the paper will do.
					ex[lang] = Lang(lang).S("(no e-mail address — left out)", "(e-posta adresi yok — yazılmaz)")
					continue
				}
				// As the stamp's face will print it: a name in a script
				// that cannot be printed right now is previewed without
				// those letters, and the define step says why.
				ex[lang] = AsPrinted(fontkit.StampFace(), lines[0])
			}
			examples[lab.ID] = ex
		}
		anyEx := wire.Text{}
		for _, lang := range Languages() {
			lines := stamp.Lines(lang, []string{id}, anyone(lang))
			if len(lines) == 0 {
				anyEx[lang] = Lang(lang).S("(the e-mail address of whoever signs)", "(imzalayanın e-posta adresi)")
				continue
			}
			anyEx[lang] = lines[0]
		}
		examples["*"] = anyEx
		out = append(out, map[string]any{"id": id, "label": stampLineLabel(id), "examples": examples, "default": isDefault[id]})
	}
	return out
}

// stampLineLabel is what a line is called on the choice button.
func stampLineLabel(id string) wire.Text {
	switch id {
	case stamp.Name:
		return T("Signer's name", "İmzacının adı")
	case stamp.Email:
		return T("E-mail address", "E-posta adresi")
	case stamp.Date:
		return T("Date and time", "Tarih ve saat")
	case stamp.IP:
		return T("IP address", "IP adresi")
	case stamp.Cert:
		return T("Certificate fingerprint", "Sertifika parmak izi")
	case stamp.Serial:
		return T("Certificate serial", "Sertifika seri numarası")
	case stamp.Authority:
		return T("Signing authority", "İmza makamı")
	}
	return Plain(id)
}

// FieldProp is the wire shape of one field for `pdf-fields`. Beside the
// geometry it carries the box's NAME and the rules it was placed with,
// so the editor can show them and the filling screen can enforce them
// before the person presses Sign.
func FieldProp(f envelope.Field) map[string]any {
	m := map[string]any{"id": f.ID, "type": f.Type, "page": f.Page, "x": f.X, "y": f.Y, "w": f.W, "h": f.H}
	if f.Assignee != "" {
		m["assignee"] = f.Assignee
	}
	if f.Required {
		m["required"] = true
	}
	if f.Label != "" {
		m["label"] = f.Label
	}
	// ⚠⚠ The WIRE's shapes, not the record's. A text box's rule travels as
	// an OBJECT (`{kind: "any"|"number"|"email", min?, max?}` —
	// docs/APP-PLUGINS-API.md → "Faces and rules"), while the record keeps a
	// bare word beside its own length bounds; a date box travels with a
	// `format` and no rule at all. The translation lives HERE, at the
	// boundary, so every envelope already saved keeps the shape it was
	// written in. Sending the bare word is what made the browser post back
	// something parseFields could not read, and a whole screen of boxes
	// vanished without a word.
	switch fields.NormalizeType(f.Type) {
	case fields.TypeText:
		if r := ruleProp(f); r != nil {
			m["rule"] = r
		}
	case fields.TypeDate:
		if f.Format != "" {
			m["format"] = f.Format
		}
	}
	if f.MinLen > 0 {
		m["min_len"] = f.MinLen
	}
	if f.MaxLen > 0 {
		m["max_len"] = f.MaxLen
	}
	if f.Font != "" {
		m["font"] = fontkit.WireID(f.Font)
	}
	if f.Value != "" {
		m["value"] = f.Value
	}
	// A signature box's two choices: drawn or typed, and what is printed
	// under it. ⚠ `lines` travels only when the box made a choice — absent
	// means the defaults, and an empty list ("nothing under it") must come
	// back empty rather than as absent.
	if fields.Drawn(f.Type) {
		if f.Typed() {
			m["style"] = "typed"
		}
		if f.Lines != nil {
			m["lines"] = stamp.Chosen(f.Lines)
		}
	}
	// Only "not yet" travels: placed is the ordinary state and says nothing.
	if !f.IsPlaced() {
		m["placed"] = false
	}
	return m
}

// ruleKind is the record's word for a text rule in the contract's
// spelling: `free` is the wire's `any`, and so is anything this build does
// not recognise — a box that quietly stops accepting what the person types
// is worse than a rule that stopped being enforced.
func ruleKind(rule string) string {
	switch fields.NormalizeRule(rule) {
	case fields.RuleNumber:
		return "number"
	case fields.RuleEmail:
		return "email"
	}
	return "any"
}

// ruleProp is a text box's rule as the contract's object, or nil when the
// box asks for nothing: a bare `{kind: "any"}` with no bounds says nothing
// and the renderer drops it, so sending it would be noise that travels
// back as noise.
func ruleProp(f envelope.Field) map[string]any {
	kind := ruleKind(f.Rule)
	if kind == "any" && f.MinLen <= 0 && f.MaxLen <= 0 {
		return nil
	}
	m := map[string]any{"kind": kind}
	if f.MinLen > 0 {
		m["min"] = f.MinLen
	}
	if f.MaxLen > 0 {
		m["max"] = f.MaxLen
	}
	return m
}

func fieldsProp(fs []envelope.Field) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, FieldProp(f))
	}
	return out
}

// ── names ──────────────────────────────────────────────────────────────

// DefaultLabels is the name offered for each kind of box when the person
// placing it types none. The editor shows them; the plugin applies the
// same words to anything that still arrives unnamed, so a nameless box
// is impossible by the time a signer sees it.
func DefaultLabels(l Lang) map[string]string {
	return map[string]string{
		fields.TypeSignature: l.S("Signature", "İmza"),
		fields.TypeInitials:  l.S("Initials", "Paraf"),
		fields.TypeText:      l.S("Text", "Metin"),
		fields.TypeDate:      l.S("Date", "Tarih"),
		fields.TypeCheckbox:  l.S("Tick box", "Onay kutusu"),
	}
}

// NameIn is the name a box is shown under on a screen in l.
//
// ⚠⚠ A name somebody TYPED is shown exactly as typed, in any script — it
// is the requester's word, not ours (the owner, 2026-09-21: "adam isterse
// Japonca yazsın alanı"). A box NOBODY named is shown under its kind's
// name in the READER's language, numbered among the boxes of that kind in
// the document ("Signature 2"). That default used to be written into the
// record in the requester's language, so a Turkish signer of an English
// requester's document was asked for "Signature", "Date" and "Text" — a
// Turkish screen with English boxes, measured on the signing link
// 2026-09-21.
func NameIn(l Lang, all []envelope.Field, f envelope.Field) string {
	if f.Label != "" {
		return f.Label
	}
	kind := fields.NormalizeType(f.Type)
	n, of := 0, 0
	for _, g := range all {
		if fields.NormalizeType(g.Type) != kind || g.Label != "" {
			continue
		}
		of++
		if g.ID == f.ID {
			n = of
		}
	}
	name := LabelFor(l, envelope.Field{Type: f.Type}, 1)
	if of > 1 && n > 0 {
		// Numbered from the first, so "Signature 1" and "Signature 2" are
		// told apart — a bare "Signature" beside "Signature 2" reads as a
		// different kind of box.
		return fmt.Sprintf("%s %d", name, n)
	}
	return name
}

// LabelFor is the name a box is shown under: what it was called, or the
// default for its kind with a number when a document carries several.
func LabelFor(l Lang, f envelope.Field, n int) string {
	if f.Label != "" {
		return f.Label
	}
	name := DefaultLabels(l)[fields.NormalizeType(f.Type)]
	if name == "" {
		name = l.S("Field", "Alan")
	}
	if n > 1 {
		return fmt.Sprintf("%s %d", name, n)
	}
	return name
}

// ── catalogues the editor offers ───────────────────────────────────────

// FontOptions lists the embedded faces for the editor's own font menu:
// handwriting first, official after.
func FontOptions(l Lang) []map[string]any {
	out := make([]map[string]any, 0, len(fontkit.All()))
	for _, f := range fontkit.All() {
		kind := T("Official", "Resmî")
		if f.Script {
			kind = T("Handwriting", "El yazısı")
		}
		out = append(out, map[string]any{"id": f.ID, "label": Plain(f.Family), "kind": kind, "script": f.Script})
	}
	return out
}

// FontSelect is the same list as a form select — five options, drawn as
// five buttons.
func FontSelect(l Lang, key, en, tr string) wire.Field {
	var opts []wire.FieldOption
	for _, f := range fontkit.All() {
		suffix := l.S(" (official)", " (resmî)")
		if f.Script {
			suffix = l.S(" (handwriting)", " (el yazısı)")
		}
		opts = append(opts, wire.FieldOption{Value: f.ID, Label: f.Family + suffix})
	}
	return choice(l, key, en, tr, fontkit.DefaultID, opts...)
}

// RuleOptions lists the rules a text box may carry. A date is NOT one of
// them: `date` is a field type, and two ways to ask for the same thing
// is how you get two answers.
func RuleOptions(l Lang) []map[string]any {
	return []map[string]any{
		{"id": fields.RuleFree, "label": T("Any text", "Serbest metin")},
		{"id": fields.RuleNumber, "label": T("Numbers only", "Yalnız sayı")},
		{"id": fields.RuleEmail, "label": T("An e-mail address", "E-posta adresi")},
	}
}

// FormatOptions lists the date layouts, each with its example — and with
// the TWO ANSWERS that made it, so the editor can ask the two questions
// the owner asked for (2026-09-23: "tarih biçimi ve ayraçlarını ayrı ayrı
// seçebilir olalım") out of this one catalogue.
//
// ⚠ One prop, not three. A control whose catalogue needs a prop of its own
// is a control somebody forgets to pass down the SurfaceRenderer →
// SurfacePdfFields → PdfFieldEditor chain, and a prop missing from one
// link of that chain is a control that is never drawn at all (lesson #229).
// The id a box stores is still the whole pattern.
func FormatOptions() []map[string]any {
	out := make([]map[string]any, 0, len(fields.Formats()))
	for _, o := range fields.DateOrders() {
		for _, s := range fields.DateSeparators() {
			f := fields.DateFormat(o, s)
			out = append(out, map[string]any{
				"id": f, "label": Plain(f), "example": fields.Example(f),
				"order": o, "order_label": dateOrderLabel(o),
				"separator": s, "separator_label": dateSeparatorLabel(s),
			})
		}
	}
	return out
}

// dateOrderLabel names an arrangement in the reader's own letters: a
// German reader is not helped by "DD MM YYYY", and "TT MM JJJJ" is the
// same answer written in the alphabet they count in.
func dateOrderLabel(order string) wire.Text {
	switch order {
	case fields.OrderDMY2:
		return T("DD MM YY", "GG AA YY")
	case fields.OrderMDY4:
		return T("MM DD YYYY", "AA GG YYYY")
	case fields.OrderMDY2:
		return T("MM DD YY", "AA GG YY")
	case fields.OrderYMD:
		return T("YYYY MM DD", "YYYY AA GG")
	}
	return T("DD MM YYYY", "GG AA YYYY")
}

// dateSeparatorLabel names a separator. ⚠ The space is NAMED, never shown
// as itself: a button with one space on it is a button with nothing on it.
func dateSeparatorLabel(sep string) wire.Text {
	switch sep {
	case fields.SepSlash:
		return T("Slash /", "Bölü /")
	case fields.SepDash:
		return T("Dash -", "Tire -")
	case fields.SepSpace:
		return T("Space", "Boşluk")
	}
	return T("Dot .", "Nokta .")
}

// DateLabels are the three captions the editor's date control needs: the
// two questions and the word over the live example. They ride with the
// node so they are the APP's words in the app's five languages, the way
// the stamp lines' names already are.
func DateLabels() map[string]any {
	return map[string]any{
		"order":     T("Date order", "Tarih sırası"),
		"separator": T("Separator", "Ayraç"),
		"example":   T("Example", "Örnek"),
	}
}
