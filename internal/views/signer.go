package views

import (
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/fontkit"
)

// ── The signer's screens ───────────────────────────────────────────────
//
// The same shell as the requester's wizard: a step strip, one question
// per step, one primary button plus Back. First what is being asked,
// then a plain form of NAMED boxes (the signature pad sits in the row of
// the box it belongs to), and last the document as it will be — which is
// what a person means by "let me see what I am signing".

// Steps of the signing flow.
const (
	FillStepIntro  = 1
	FillStepFill   = 2
	FillStepReview = 3
)

// FillState is what the signing screens carry between steps. Values are
// keyed by field id; a signature box holds the pad's PNG as base64,
// already shrunk to the size a job param may carry.
type FillState struct {
	Step   int               `json:"step"`
	Signer string            `json:"signer"`
	Values map[string]string `json:"values"`

	// Preview is what the approve step shows in each of the signer's
	// signature boxes: the picture the job will print there (the drawing
	// and the chosen lines, composed by the same function the job uses),
	// field id → PNG base64. Lines is the same lines as text, readable.
	// Worked out by the app for the step that shows them; never carried in
	// the surface state (`json:"-"`) — a picture per box would not fit it.
	//
	// ⚠⚠ The owner, 2026-09-21: the signer sees EXACTLY what will be printed
	// under their signature before they sign — an IP address is personal
	// data, and it must not reach the paper unseen.
	Preview map[string]string   `json:"-"`
	Lines   map[string][]string `json:"-"`
}

func (st FillState) withDefaults() FillState {
	if st.Step < FillStepIntro {
		st.Step = FillStepIntro
	}
	if st.Step > FillStepReview {
		st.Step = FillStepReview
	}
	if st.Values == nil {
		st.Values = map[string]string{}
	}
	return st
}

// FillStateOf is the state as the surface carries it.
func FillStateOf(st FillState) map[string]any {
	st = st.withDefaults()
	return map[string]any{"view": "fill", "step": st.Step, "signer": st.Signer, "values": st.Values}
}

func fillStrip(l Lang, step int) wire.Node {
	return steps(stepStates(step,
		T("What is asked", "Ne isteniyor"),
		T("Fill it in", "Doldurun"),
		T("See and approve", "Gör ve onayla"))...)
}

// MyFields lists the boxes a signer may act on, in reading order, so the
// form asks for them the way the page shows them.
func MyFields(env *envelope.Envelope, signerID string) []envelope.Field {
	return InReadingOrder(env.FieldsFor(signerID))
}

// InReadingOrder sorts boxes down the pages.
func InReadingOrder(fs []envelope.Field) []envelope.Field {
	out := append([]envelope.Field(nil), fs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Page != out[j].Page {
			return out[i].Page < out[j].Page
		}
		return out[i].Y < out[j].Y
	})
	return out
}

// Fill draws the signer's screen at st.Step. It is the same builder for
// somebody signing inside filex and for an outside signer on their own
// link — one flow, one look, one set of rules.
func Fill(l Lang, doc Doc, env *envelope.Envelope, sg *envelope.Signer, st FillState, errs map[string]wire.Text) *wire.Surface {
	st = st.withDefaults()
	st.Signer = sg.ID
	// ⚠⚠ Not everybody signs (the owner, 2026-09-23). Every word on this
	// screen follows the act the person is really being asked for; calling
	// a form to fill in "signing" is asking them to agree to something
	// nobody described.
	signs := env.Signs(sg.ID)
	title := Tf("Fill in %s", "%s - doldur", env.Document)
	if signs {
		title = Tf("Sign %s", "%s - imzala", env.Document)
	}
	s := &wire.Surface{
		Title:  title,
		Size:   "xl",
		State:  FillStateOf(st),
		Errors: errs,
	}
	if msg := closedWords(env, sg); msg != nil {
		s.Nodes = []wire.Node{info(msg)}
		return s
	}
	if !env.Turn(sg.ID) {
		who := "-"
		if n := env.Next(); n != nil {
			who = n.Person.Display()
		}
		s.Nodes = []wire.Node{info(Tf("This document is signed one signer at a time, and it is %s's turn. You will hear from us when it is yours.",
			"Bu belge birer birer imzalanıyor, şu an sıra %s adlı kişide. Sıra size gelince haber vereceğiz.", who))}
		return s
	}
	s.Nodes = []wire.Node{fillStrip(l, st.Step)}

	switch st.Step {
	case FillStepIntro:
		s.Nodes = append(s.Nodes, introNodes(l, env, sg, signs)...)
		s.Actions = []wire.SurfaceAction{primary("next", T("Start", "Başla"))}
		if env.Options.AllowDecline {
			no := T("I will not take part", "Katılmayacağım")
			if signs {
				no = T("I will not sign", "İmzalamayacağım")
			}
			s.Actions = append(s.Actions, dangerButton("decline", no))
		}

	case FillStepFill:
		s.Nodes = append(s.Nodes, fillNodes(l, env, sg, st)...)
		s.Actions = []wire.SurfaceAction{backButton(), nextButton()}

	default:
		s.Nodes = append(s.Nodes, reviewFillNodes(l, doc, env, sg, st)...)
		// ⚠ The action id stays `sign` — it is the same submission, and the
		// same certificate commits it. Only the WORD changes.
		done := T("Confirm", "Onayla")
		if signs {
			done = T("Sign", "İmzala")
		}
		s.Actions = []wire.SurfaceAction{backButton(), primary("sign", done)}
	}
	return s
}

func introNodes(l Lang, env *envelope.Envelope, sg *envelope.Signer, signs bool) []wire.Node {
	ask := Tf("%s asks you to fill in “%s”.", "%s sizden “%s” belgesini doldurmanızı istiyor.",
		env.Requester.Display(), env.Document)
	if signs {
		ask = Tf("%s asks you to sign “%s”.", "%s sizden “%s” belgesini imzalamanızı istiyor.",
			env.Requester.Display(), env.Document)
	}
	n := []wire.Node{heading(ask)}
	if env.Options.Message != "" {
		n = append(n, text(Plain("“"+env.Options.Message+"”")))
	}
	var rows []row
	for _, f := range MyFields(env, sg.ID) {
		what := Plain(NameIn(l, env.Fields, f))
		if f.Required {
			// The same mark the form puts on a required box, so the list of
			// what is asked and the form that asks it agree.
			what = Plain(NameIn(l, env.Fields, f) + " *")
		}
		rows = append(rows, row{ID: f.ID, Cells: map[string]wire.Text{
			"what": what,
			"kind": kindWords(f),
			"page": Tf("page %d", "sayfa %d", f.Page),
		}})
	}
	n = append(n, list([]column{
		{Key: "what", Label: T("Asked of you", "Sizden istenen")},
		{Key: "kind", Label: T("Kind", "Tür")},
		{Key: "page", Label: T("Where", "Nerede")},
	}, rows, T("Nothing but your signature", "Yalnızca imzanız")))

	if env.Options.Deadline != "" {
		if signs {
			n = append(n, muted(Tf("Please sign by %s.", "Lütfen %s tarihine kadar imzalayın.", Day(env.Options.Deadline))))
		} else {
			n = append(n, muted(Tf("Please finish by %s.", "Lütfen %s tarihine kadar tamamlayın.", Day(env.Options.Deadline))))
		}
	}
	if env.Sequential() {
		n = append(n, muted(T("This document is signed one signer at a time, and it is your turn.",
			"Bu belge birer birer imzalanıyor; sıra sizde.")))
	}
	who := sg.Person.Display()
	if sg.Person.Email != "" {
		who += " <" + sg.Person.Email + ">"
	}
	// ⚠⚠ Somebody who only fills in boxes is still committing their work
	// with a certificate — the submission is signed over their own revision,
	// invisibly, so the document keeps an unbroken chain and Verify can say
	// who changed what. The sentence says so rather than pretending there is
	// no cryptography, and rather than calling it a signature on the page.
	if signs {
		n = append(n, muted(Tf("You are signing as %s. A certificate is issued in that name, for this one signature, by the signing authority of %s's filex; the key that made it is destroyed the moment the signature is written.",
			"%s olarak imzalıyorsunuz. Bu tek imza için o ada, %s kurulumundaki filex'in imza makamınca bir sertifika üretilir; onu üreten anahtar imza yazılır yazılmaz yok edilir.",
			who, env.Requester.Display())))
		return n
	}
	n = append(n, muted(Tf("You are filling this in as %s. No signature of yours goes on the page, but what you enter is sealed to your name: a certificate is issued in that name, for this one submission, by the signing authority of %s's filex, and the key that made it is destroyed the moment it is used.",
		"%s olarak dolduruyorsunuz. Sayfaya imzanız konmaz, ancak girdikleriniz adınıza mühürlenir: bu tek gönderim için o ada, %s kurulumundaki filex'in imza makamınca bir sertifika üretilir ve onu üreten anahtar kullanılır kullanılmaz yok edilir.",
		who, env.Requester.Display())))
	return n
}

func kindWords(f envelope.Field) wire.Text {
	switch fields.NormalizeType(f.Type) {
	case fields.TypeSignature:
		return T("your signature", "imzanız")
	case fields.TypeInitials:
		return T("your initials", "parafınız")
	case fields.TypeDate:
		return T("a date", "bir tarih")
	case fields.TypeCheckbox:
		return T("a tick", "bir onay")
	}
	switch fields.NormalizeRule(f.Rule) {
	case fields.RuleNumber:
		return T("a number", "bir sayı")
	case fields.RuleEmail:
		return T("an e-mail address", "bir e-posta adresi")
	}
	return T("some text", "bir metin")
}

// fillNodes is the plain form: one row per named box, with the signature
// pad inline where a signature is what is being asked for.
func fillNodes(l Lang, env *envelope.Envelope, sg *envelope.Signer, st FillState) []wire.Node {
	n := []wire.Node{text(T("Fill in what is asked of you. Each row is one box on the document.",
		"Sizden istenenleri doldurun. Her satır belgedeki bir kutuya karşılık gelir."))}
	return append(n, FieldForm(l, env.Fields, MyFields(env, sg.ID), st.Values)...)
}

// PadProps is one signature box's pad: labelled with the box's name, with
// the `*` a required box wears everywhere else, and offering exactly what
// the requester chose — a drawing (drawn or a picture of one), or a typed
// name in the face they picked.
//
// ⚠ The owner, 2026-09-21: "imzalama ekranında imza zorunluydu ama
// imzanın zorunlu olduğunu `*` ile belirtmiyoruz". The name used to be a
// plain line of text above the pad, so the one required box that had no
// star was the signature.
func PadProps(name string, f envelope.Field) map[string]any {
	p := map[string]any{"label": Plain(name), "required": f.Required}
	if f.Typed() {
		face := fontkit.WireID(fields.NormalizeFont(f.Font))
		p["modes"] = []string{"type"}
		p["font"] = face
		p["fonts"] = []string{face}
	} else {
		p["modes"] = []string{"draw", "upload"}
	}
	return p
}

// FieldForm is the heart of the fill step, shared by the signer's screen
// and by signing a document yourself: one row per NAMED box, the pad in
// the row of the box it belongs to. `all` is every box of the document,
// which is what an unnamed box's number is counted among.
func FieldForm(l Lang, all, fs []envelope.Field, held map[string]string) []wire.Node {
	var n []wire.Node
	var typed []wire.Field
	values := map[string]any{}
	var pads, notes []wire.Node
	for _, f := range fs {
		name := NameIn(l, all, f)
		if fields.Drawn(f.Type) {
			pads = append(pads,
				wire.Node{ID: PadFor(f.ID), Type: "signature-pad", Props: PadProps(name, f)})
			continue
		}
		fld, val := typedField(l, f, name, held[f.ID])
		if f.Label == "" {
			// A box nobody named is shown under OUR name for its kind — a
			// translation, so it travels in every language (fieldIn).
			f := f
			fld = fieldIn(fld, Each(func(l Lang) string { return NameIn(l, all, f) }))
		}
		typed = append(typed, fld)
		if val != nil {
			values[f.ID] = val
		}
		if fields.NormalizeType(f.Type) != fields.TypeCheckbox {
			notes = append(notes, PrintNotes(f.ID, name, held[f.ID], fontkit.Get(fields.NormalizeFont(f.Font)), InTheDocument)...)
		}
	}
	if len(typed) > 0 {
		// The notes sit right under the boxes they are about, and change as
		// the person types (the screen is asked again at every pause).
		n = append(n, form(typed, values))
		n = append(n, notes...)
	}
	if len(pads) > 0 {
		if len(typed) > 0 {
			n = append(n, divider())
		}
		n = append(n, pads...)
	}
	if len(typed) == 0 && len(pads) == 0 {
		n = append(n, info(T("Nothing has to be filled in - go on and approve the document.",
			"Doldurulacak bir şey yok - devam edip belgeyi onaylayın.")))
	}
	return n
}

// typedField turns one box into one form row, with the rule spelled out
// under it so the screen says what it wants before it refuses anything.
func typedField(l Lang, f envelope.Field, name, value string) (wire.Field, any) {
	switch fields.NormalizeType(f.Type) {
	case fields.TypeCheckbox:
		fld := labelled(f.ID, "bool", name)
		fld.Default = false
		if f.Required {
			fld = required(fld)
		}
		return fld, fields.IsTicked(value)
	case fields.TypeDate:
		fld := withHelpf(withHint(labelled(f.ID, "string", name), fields.Example(f.Format)),
			l, "A date, written as %s.", "Tarih, %s biçiminde.", fields.Example(f.Format))
		if f.Required {
			fld = required(fld)
		}
		return fld, value
	}
	fld := labelled(f.ID, "string", name)
	switch fields.NormalizeRule(f.Rule) {
	case fields.RuleNumber:
		fld = withHelp(fld, l, "Numbers only.", "Yalnız sayı.")
	case fields.RuleEmail:
		fld = withHelp(withHint(fld, "ad@ornek.com"), l, "An e-mail address.", "Bir e-posta adresi.")
	default:
		switch {
		case f.MinLen > 0 && f.MaxLen > 0:
			fld = withHelpf(fld, l, "Between %d and %d characters.", "%d ile %d karakter arası.", f.MinLen, f.MaxLen)
		case f.MaxLen > 0:
			fld = withHelpf(fld, l, "At most %d characters.", "En fazla %d karakter.", f.MaxLen)
		case f.MinLen > 0:
			fld = withHelpf(fld, l, "At least %d characters.", "En az %d karakter.", f.MinLen)
		}
	}
	if f.Required {
		fld = required(fld)
	}
	return fld, value
}

// reviewFillNodes is the last step: the document with everything in
// place, and a line-by-line list of what is about to be signed.
func reviewFillNodes(l Lang, doc Doc, env *envelope.Envelope, sg *envelope.Signer, st FillState) []wire.Node {
	// ⚠ The document shows each value AS IT WILL BE PRINTED — without the
	// characters no face can draw right now — and the notes under it say
	// which those are: what is approved here is what is stamped.
	seeded := make([]envelope.Field, 0, len(env.Fields))
	var notes []wire.Node
	for _, f := range env.Fields {
		if v, ok := st.Values[f.ID]; ok && v != "" {
			f.Value = v
			if !fields.Drawn(f.Type) && fields.NormalizeType(f.Type) != fields.TypeCheckbox {
				face := fontkit.Get(fields.NormalizeFont(f.Font))
				f.Value = AsPrinted(face, v)
				notes = append(notes, PrintNotes(f.ID, NameIn(l, env.Fields, f), v, face, InTheDocument)...)
			}
		}
		seeded = append(seeded, f)
	}
	// ⚠ The signature boxes show the PICTURE THE JOB WILL PRINT — drawing
	// and lines — not the bare drawing: this step is "the document as it
	// will be", and a preview without the lines would hide exactly what the
	// signer has to approve.
	for i := range seeded {
		if pic, ok := st.Preview[seeded[i].ID]; ok && pic != "" {
			seeded[i].Value = pic
		}
	}
	props := editorProps(l, doc, "fill", seeded, SignerLabels(env.Signers))
	props["signer"] = sg.ID

	var rows []row
	var printed []wire.Node
	for _, f := range MyFields(env, sg.ID) {
		v := st.Values[f.ID]
		shown := Plain(v)
		if p := AsPrinted(fontkit.Get(fields.NormalizeFont(f.Font)), v); p != v && !fields.Drawn(f.Type) && f.Type != fields.TypeCheckbox {
			shown = Tf("%s - printed as “%s”", "%s - “%s” olarak basılacak", v, p)
		}
		switch {
		case fields.Drawn(f.Type):
			shown = T("drawn", "çizildi")
			if f.Typed() {
				shown = T("typed", "yazıldı")
			}
			if v == "" {
				shown = T("still empty", "hâlâ boş")
			}
		case f.Type == fields.TypeCheckbox:
			shown = T("not ticked", "işaretlenmedi")
			if fields.IsTicked(v) {
				shown = T("ticked", "işaretlendi")
			}
		case strings.TrimSpace(v) == "":
			shown = T("left empty", "boş bırakıldı")
		}
		if lines, ok := st.Lines[f.ID]; ok && fields.Drawn(f.Type) {
			printed = append(printed, printedUnder(l, NameIn(l, env.Fields, f), lines)...)
		}
		rows = append(rows, row{ID: f.ID, Cells: map[string]wire.Text{
			"what": Plain(NameIn(l, env.Fields, f)), "value": shown}})
	}
	// ⚠⚠ Right under the document: the lines each signature will carry,
	// EXACTLY as the paper will print them (in the request's language,
	// whatever this screen is in) — one paragraph per signature box, which
	// wraps at any width. The owner, 2026-09-21: an IP address is personal
	// data, and it must not reach the paper unseen. (A paragraph per LINE
	// took the height the document needed and shrank it to a thumbnail; a
	// table column was cut off on a phone.)
	check := T("This is the document as it will be. Check it, then confirm.",
		"Belge böyle olacak. Kontrol edip onaylayın.")
	if env.Signs(sg.ID) {
		check = T("This is the document as it will be. Check it, then sign.",
			"Belge böyle olacak. Kontrol edin ve imzalayın.")
	}
	out := []wire.Node{
		text(check),
		wire.Node{ID: IDFields, Type: "pdf-fields", Props: props},
	}
	if len(printed) > 0 {
		notes = append(notes, PrintNotes("signer", sg.Person.CertName(), sg.Person.CertName(), fontkit.StampFace(), UnderTheSignature)...)
	}
	out = append(out, notes...)
	if len(printed) > 0 {
		out = append(out, printed...)
		out = append(out, muted(T("The date and time, and the certificate, are those of the moment you press Sign.",
			"Tarih ve saat ile sertifika, İmzala'ya bastığınız ana aittir.")))
	}
	return append(out, list([]column{
		{Key: "what", Label: T("Box", "Kutu")},
		{Key: "value", Label: T("What you entered", "Girdiğiniz")},
	}, rows, T("Nothing to fill in", "Doldurulacak bir şey yok")))
}

// printedUnder says, in one paragraph, what will be printed under one
// signature: the lines exactly as the paper carries them.
func printedUnder(l Lang, box string, lines []string) []wire.Node {
	if len(lines) == 0 {
		return []wire.Node{muted(Tf("Nothing is printed under “%s”.", "“%s” altına bir şey yazılmıyor.", box))}
	}
	return []wire.Node{text(Tf("Printed under “%s”: %s", "“%s” altına yazılacaklar: %s", box, strings.Join(lines, " · ")))}
}

// Decline asks for a reason before a refusal is recorded.
func Decline(l Lang, env *envelope.Envelope, sg *envelope.Signer, errs map[string]wire.Text) *wire.Surface {
	return &wire.Surface{
		Title: T("Refuse to sign", "İmzalamayı reddet"),
		Size:  "md",
		State: map[string]any{"view": "fill", "signer": sg.ID, "step": "decline"},
		Nodes: []wire.Node{
			heading(T("Why will you not sign?", "Neden imzalamayacaksınız?")),
			text(Tf("%s will be told, the request closes for everybody, and nobody can sign “%s” afterwards.",
				"%s kişisine iletilir, istek herkes için kapanır ve sonrasında “%s” belgesini kimse imzalayamaz.",
				env.Requester.Display(), env.Document)),
			form([]wire.Field{
				withHintIn(longField(l, "decline_reason", "Reason (optional)", "Gerekçe (isteğe bağlı)"),
					l, "The amount is wrong.", "Tutar yanlış."),
			}, nil),
		},
		Actions: []wire.SurfaceAction{
			backButton(),
			dangerButton("decline_confirm", T("Refuse", "Reddet")),
		},
	}
}

// closedWords is the one line a signer sees instead of the screen when
// there is nothing left to do.
func closedWords(env *envelope.Envelope, sg *envelope.Signer) wire.Text {
	switch {
	case sg.Status == envelope.SignerDeclined && !env.Signs(sg.ID):
		return T("You refused to take part in this document.", "Bu belgeye katılmayı reddettiniz.")
	case sg.Status == envelope.SignerDeclined:
		return T("You refused to sign this document.", "Bu belgeyi imzalamayı reddettiniz.")
	case env.Status == envelope.StatusCancelled:
		return T("This signature request was cancelled by the requester.", "Bu imza isteği, isteği gönderen kişi tarafından iptal edildi.")
	case env.Status == envelope.StatusExpired:
		return T("This signature request has expired.", "Bu imza isteğinin süresi doldu.")
	case env.Status == envelope.StatusDeclined:
		return T("This signature request was closed because a signer refused.", "Bir imzacı reddettiği için bu imza isteği kapandı.")
	case env.Status.Closed() || sg.Status == envelope.SignerVoid:
		return T("This signature request is closed.", "Bu imza isteği kapandı.")
	}
	return nil
}

// NotASigner is what somebody who is not on the list sees.
func NotASigner(l Lang, env *envelope.Envelope, canManage bool) *wire.Surface {
	s := &wire.Surface{Title: T("Signatures", "İmzalar"), Size: "md", State: map[string]any{"view": "fill"}}
	s.Nodes = []wire.Node{info(Tf("“%s” is waiting for %s. You are not one of its signers.",
		"“%s” belgesi %s kişisini bekliyor. Siz imzacılarından biri değilsiniz.", env.Document, signerNames(env)))}
	if canManage {
		s.Nodes = append(s.Nodes, muted(T("You sent this request: open the file's details panel → Signatures to follow, remind or cancel it.",
			"Bu isteği siz gönderdiniz: takip etmek, hatırlatmak ya da iptal etmek için dosyanın Ayrıntılar panelinde İmzalar bölümünü açın.")))
	}
	return s
}

func signerNames(env *envelope.Envelope) string {
	var names []string
	for _, s := range env.Signers {
		if !s.Done() {
			names = append(names, s.Person.Display())
		}
	}
	if len(names) == 0 {
		return "-"
	}
	return strings.Join(names, ", ")
}

// Received is what a public page shows after a submission was accepted.
func Received() *wire.Surface {
	return &wire.Surface{Title: T("Thank you", "Teşekkürler"),
		Nodes: []wire.Node{info(T("Your signature was received and is being applied to the document. Your receipt follows in a moment.",
			"İmzanız alındı ve belgeye uygulanıyor. Makbuzunuz birazdan iletilecek."))}}
}

// Gone is what a link that no longer belongs to a request shows.
func Gone(msg wire.Text) *wire.Surface {
	return &wire.Surface{Title: T("Sign", "İmzala"), Nodes: []wire.Node{danger(msg)}}
}
