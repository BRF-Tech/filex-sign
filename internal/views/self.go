package views

import (
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fontkit"
)

// ── Signing a document yourself ────────────────────────────────────────
//
// The same shell again: place the boxes with the document filling the
// screen, fill them in (the pad in the row of the box it belongs to),
// say where the result goes. Nothing about this screen should tell a
// person they are in a different product from the one that asks others
// to sign.

// Steps of signing yourself.
const (
	SelfStepFields = 1
	SelfStepFill   = 2
	SelfStepResult = 3
)

// SelfState is what the screen carries between its steps.
type SelfState struct {
	Step   int               `json:"step"`
	Fields []envelope.Field  `json:"fields"`
	Values map[string]string `json:"values"`
	Reason string            `json:"reason"`
	Opts   envelope.Options  `json:"opts"`

	// Lines is what will be printed under each signature box, worked out
	// by the app for the last step (field id → lines) — the same preview a
	// signer on a request gets before pressing Sign. Never in the state.
	Lines map[string][]string `json:"-"`
	// IP is the address of the screen this person is signing from
	// (context.actor.ip), for the previews. Never in the state.
	IP string `json:"-"`
}

func (st SelfState) withDefaults() SelfState {
	if st.Step < SelfStepFields {
		st.Step = SelfStepFields
	}
	if st.Step > SelfStepResult {
		st.Step = SelfStepResult
	}
	if st.Values == nil {
		st.Values = map[string]string{}
	}
	if st.Opts.Output.Mode == "" {
		st.Opts.Output = envelope.Output{Mode: envelope.OutputSibling, Name: envelope.DefaultSiblingName}
	}
	return st
}

// SelfStateOf is the state as the surface carries it. The boxes travel in
// the RECORD's shape — see StateOf for why the wire's is wrong here.
func SelfStateOf(st SelfState) map[string]any {
	st = st.withDefaults()
	return map[string]any{"view": "sign-self", "step": st.Step, "fields": st.Fields,
		"values": st.Values, "reason": st.Reason, "opts": st.Opts}
}

// SignSelf draws the "Sign…" screen at st.Step.
func SignSelf(l Lang, doc Doc, who envelope.Person, st SelfState, errs map[string]wire.Text) *wire.Surface {
	st = st.withDefaults()
	me := []SignerLabel{{ID: "me", Label: who.Identity(), Color: palette[0], Name: who.CertName(), Email: who.Email,
		Self: true, IP: st.IP}}
	s := &wire.Surface{
		Title: Tf("Sign %s", "%s — imzala", doc.Name),
		Size:  "xl",
		State: SelfStateOf(st),
		Nodes: []wire.Node{steps(stepStates(st.Step,
			T("Place the boxes", "Kutuları yerleştir"),
			T("Sign", "İmzala"),
			T("Where it goes", "Nereye kaydedilsin"))...)},
		Errors: errs,
	}
	switch st.Step {
	case SelfStepFields:
		s.Nodes = append(s.Nodes,
			text(T("Put a signature box where your signature should appear, and a box for anything you want filled in. Name each box.",
				"İmzanızın görüneceği yere bir imza kutusu, doldurmak istediğiniz her şey için birer kutu koyun. Her kutuya bir ad verin.")),
			wire.Node{ID: IDFields, Type: "pdf-fields", Props: editorProps(l, doc, "edit", st.Fields, me)},
		)
		s.Actions = []wire.SurfaceAction{nextButton()}

	case SelfStepFill:
		s.Nodes = append(s.Nodes,
			text(T("Draw your signature, and fill in the boxes you placed.",
				"İmzanızı çizin ve yerleştirdiğiniz kutuları doldurun.")))
		s.Nodes = append(s.Nodes, FieldForm(l, st.Fields, InReadingOrder(st.Fields), st.Values)...)
		s.Actions = []wire.SurfaceAction{backButton(), nextButton()}

	default:
		out := st.Opts.Output.Normalized()
		s.Nodes = append(s.Nodes,
			heading(T("Where should the signed document go?", "İmzalı belge nereye kaydedilsin?")),
			form([]wire.Field{
				choice(l, "output_mode", "The signed document is", "İmzalı belge", envelope.OutputSibling,
					opt(l, envelope.OutputVersion, "A new version of this file", "Bu dosyanın yeni sürümü"),
					opt(l, envelope.OutputSibling, "A new file beside it", "Yanına yeni bir dosya")),
				withHelp(withHint(onlyWhen(strField(l, "output_name", "Name of the new file", "Yeni dosyanın adı"),
					"output_mode", envelope.OutputSibling), envelope.DefaultSiblingName),
					l, "{stem} is the name without its extension, {ext} the extension.",
					"{stem} uzantısız ad, {ext} uzantıdır."),
				withHint(strField(l, "reason", "Reason (optional)", "Gerekçe (isteğe bağlı)"),
					l.S("Approved", "Onaylandı")),
			}, map[string]any{"output_mode": out.Mode, "output_name": out.Name, "reason": st.Reason}),
		)
		// What goes under each signature, before Sign — as on a request.
		printed := 0
		for _, f := range InReadingOrder(st.Fields) {
			if lines, ok := st.Lines[f.ID]; ok {
				s.Nodes = append(s.Nodes, printedUnder(l, NameIn(l, st.Fields, f), lines)...)
				printed++
			}
		}
		if printed > 0 {
			s.Nodes = append(s.Nodes, PrintNotes("signer", who.CertName(), who.CertName(), fontkit.StampFace(), UnderTheSignature)...)
			s.Nodes = append(s.Nodes, muted(T("The date and time, and the certificate, are those of the moment you press Sign.",
				"Tarih ve saat ile sertifika, İmzala'ya bastığınız ana aittir.")))
		}
		s.Actions = []wire.SurfaceAction{backButton(), primary("sign", T("Sign", "İmzala"))}
	}
	return s
}
