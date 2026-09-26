package app

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/geometry"
	"github.com/brf-tech/filex-sign/internal/pdfsig"
	"github.com/brf-tech/filex-sign/internal/stamp"
	"github.com/brf-tech/filex-sign/internal/views"
)

// office reports whether a screen is looking at a document that has to
// go through LibreOffice before anything can be placed on it.
func office(doc views.Doc, in *wire.ViewEventInput) (isOffice, engine bool) {
	ext := extOf(doc.Name)
	if ext == "pdf" || ext == "" {
		return false, in.Context.Engines[officeEngine]
	}
	return isOfficeExt(ext), in.Context.Engines[officeEngine]
}

// jobOutput is the per-job output a surface attaches to the action it
// queues — the wizard's "same file / beside it / this name" choice.
func jobOutput(o envelope.Output) *wire.Output {
	n := o.Normalized()
	return &wire.Output{Mode: n.Mode, Name: n.Name}
}

func withPath(s *wire.Surface, doc views.Doc) *wire.Surface {
	if doc.Path != "" {
		if s.State == nil {
			s.State = map[string]any{}
		}
		s.State["doc_path"] = doc.Path
	}
	return s
}

// pressed reports whether the event is a click on the footer button id.
//
// ⚠⚠ filex posts a footer button as `submit` when it is the screen's
// primary button and as `action` otherwise, with the id in action_id
// (packages/core usePluginSurface.press — one code path for the dialog,
// the full page and an embedded explorer's popup). A handler that listens
// for only one of the two is a dead button: "Convert to PDF", "Open its
// Signatures panel" and "Close the expired request" were all primary and
// all listened for `action` alone, so a click redrew the same screen and
// nothing happened (2026-09-26, a DOCX sent for signature). Ask for the
// button, never for the event that carries it.
func pressed(in *wire.ViewEventInput, id string) bool {
	return in != nil && in.ActionID == id && (in.Event == "action" || in.Event == "submit")
}

func convertJob() *wire.Surface {
	return &wire.Surface{Job: &wire.JobRequest{ActionID: ActionConvert, Params: map[string]any{"op": "convert"},
		Output: &wire.Output{Mode: envelope.OutputSibling, Name: "{stem}.pdf"}}}
}

// ── sign yourself (a full page, three steps) ───────────────────────────

func (a *App) viewSignSelf(in *wire.ViewEventInput) (*wire.Surface, error) {
	l := langOfView(in)
	doc := docOf(in, docRef)
	doc.Authority = a.ca().Name
	if doc.ReadOnly {
		return withPath(views.ReadOnlyDoc(l, doc, false), doc), nil
	}
	if isOffice, engine := office(doc, in); isOffice {
		if pressed(in, "convert") {
			return convertJob(), nil
		}
		return withPath(views.SignOffice(l, doc, engine), doc), nil
	}
	st := selfStateOf(in)
	st.IP = actorIP(in)
	vals := valuesOf(in)
	errs := map[string]wire.Text{}
	who := actorPerson(in.Context.Actor)

	switch {
	case in.Event == "open":
		st.Step = views.SelfStepFields
	case pressed(in, "back"):
		if err := a.absorbSelf(&st, vals, l); err != nil {
			a.logf("warn", "sign yourself: the posted boxes could not be read: %v", err)
			errs[views.IDFields] = unreadableBoxes()
			break
		}
		if st.Step > views.SelfStepFields {
			st.Step--
		}
	case in.Event == "submit" || in.Event == "change":
		if err := a.absorbSelf(&st, vals, l); err != nil {
			a.logf("warn", "sign yourself: the posted boxes could not be read: %v", err)
			errs[views.IDFields] = unreadableBoxes()
			break
		}
		if in.Event == "change" {
			break
		}
		switch st.Step {
		case views.SelfStepFields:
			if firstDrawnField(st.Fields, "") == nil {
				errs[views.IDFields] = views.T("Place a signature box on the document.", "Belgeye bir imza kutusu yerleştirin.")
			}
			if len(errs) == 0 {
				st.Step = views.SelfStepFill
			}
		case views.SelfStepFill:
			checkValues(l, st.Fields, st.Fields, st.Values, errs)
			if len(errs) == 0 {
				st.Step = views.SelfStepResult
			}
		default:
			return a.queueSelfSign(st, actorIP(in)), nil
		}
	}
	if st.Step == views.SelfStepResult {
		// The same preview a signer on a request gets before Sign: what each
		// signature box will carry under it, worded by the printer's own
		// function, with this person's own facts.
		f := stamp.Facts{Name: who.CertName(), Email: who.Email, When: a.H.Now(), IP: actorIP(in),
			Authority: doc.Authority, PendingCert: true, PendingWho: stamp.You}
		st.Lines = linesFor(in.Context.Locale, st.Fields, f)
	}
	return withPath(views.SignSelf(l, doc, who, st, errs), doc), nil
}

// actorIP is the address a signed-in person's screen was drawn for
// (context.actor.ip; empty on a host older than v0.43.0).
func actorIP(in *wire.ViewEventInput) string {
	if in == nil || in.Context.Actor == nil {
		return ""
	}
	return in.Context.Actor.IP
}

// linesFor is what every signature box among fs prints under it, for the
// given facts: field id → lines.
func linesFor(lang string, fs []envelope.Field, f stamp.Facts) map[string][]string {
	out := map[string][]string{}
	for _, b := range fs {
		if fields.Drawn(b.Type) {
			// As the stamp's face prints them: the words under the picture
			// must not promise letters the picture leaves out.
			out[b.ID] = views.PrintedLines(stamp.Lines(lang, stamp.Chosen(b.Lines), f))
		}
	}
	return out
}

// previewScale is the resolution of the approve step's pictures: half the
// printed one. ⚠ The same Compose draws both; only the pixel count
// differs, so a preview cannot show a line the paper will not carry.
const previewScale = 2.0

// previews composes, for the approve step, the picture the job will print
// in each of the signer's signature boxes — their drawing and the lines
// the box chose — field id → PNG base64. A box whose picture cannot be
// drawn is simply left out: the step then shows the bare drawing, and the
// lines are still listed in words under the document.
func (a *App) previews(ref, lang string, mine []envelope.Field, held map[string]string, f stamp.Facts) map[string]string {
	out := map[string]string{}
	raw, err := a.H.ReadInput(ref)
	if err != nil {
		a.logf("warn", "preview: reading the document: %v", err)
		return out
	}
	info, err := pdfsig.Inspect(raw)
	if err != nil {
		return out
	}
	first := ""
	for _, b := range mine {
		if fields.Drawn(b.Type) && held[b.ID] != "" {
			first = held[b.ID]
			break
		}
	}
	for _, b := range mine {
		if !fields.Drawn(b.Type) {
			continue
		}
		drawing := held[b.ID]
		if drawing == "" {
			drawing = first
		}
		box, err := info.Box(b.Page)
		if err != nil {
			continue
		}
		rect := geometry.ToRect(geometry.Frac{X: b.X, Y: b.Y, W: b.W, H: b.H}, box)
		png, err := pdfsig.ComposeScaled(rect, decodeImage(drawing), stamp.Lines(lang, stamp.Chosen(b.Lines), f), previewScale)
		if err != nil {
			continue
		}
		out[b.ID] = b64of(png)
	}
	return out
}

func selfStateOf(in *wire.ViewEventInput) views.SelfState {
	var st views.SelfState
	if in.State != nil {
		_ = roundTrip(in.State, &st)
	}
	if st.Step < views.SelfStepFields {
		st.Step = views.SelfStepFields
	}
	if st.Values == nil {
		st.Values = map[string]string{}
	}
	return st
}

// absorbSelf copies the posted values of the current step into the state.
// ⚠⚠ A field list that cannot be read comes back as an ERROR, never as a
// list quietly left as it was: swallowing it told somebody who had just
// placed five boxes that they had placed none, and their work was gone.
func (a *App) absorbSelf(st *views.SelfState, vals map[string]any, l views.Lang) error {
	switch st.Step {
	case views.SelfStepFields:
		if v, ok := vals[views.IDFields]; ok {
			fs, err := parseFields(v, "", l)
			if err != nil {
				return err
			}
			for i := range fs {
				fs[i].Value = ""
			}
			st.Fields = fs
		}
	case views.SelfStepFill:
		a.absorbValues(st.Fields, vals, st.Values)
	case views.SelfStepResult:
		if has(vals, "output_mode") {
			st.Opts.Output = outputFromValues(vals)
		}
		if has(vals, "reason") {
			st.Reason = clip(str(vals, "reason"), 200)
		}
	}
	return nil
}

// unreadableBoxes is what somebody is told when the box list a screen
// posted could not be read. It says nothing was changed on purpose: the
// person's next move is to try again, not to start over.
func unreadableBoxes() wire.Text {
	return views.T("This screen's boxes could not be read, so nothing was changed. Please try again.",
		"Bu ekrandaki kutular okunamadı, bu yüzden hiçbir şey değiştirilmedi. Lütfen yeniden deneyin.")
}

func (a *App) queueSelfSign(st views.SelfState, ip string) *wire.Surface {
	var props []map[string]any
	for _, f := range st.Fields {
		props = append(props, views.FieldProp(f))
	}
	png, first := "", ""
	if v := firstDrawnField(st.Fields, ""); v != nil {
		png, first = st.Values[v.ID], v.ID
	}
	params := map[string]any{
		"fields": props, "values": valueParams(st.Fields, st.Values, first), "png_b64": png, "reason": st.Reason,
	}
	if ip != "" {
		params["visitor_ip"] = ip
	}
	return &wire.Surface{Job: &wire.JobRequest{ActionID: ActionSign, Params: params, Output: jobOutput(st.Opts.Output)}}
}

// ── request wizard (a full page, eight steps) ──────────────────────────

func requestStateOf(in *wire.ViewEventInput) views.RequestState {
	var st views.RequestState
	if in.State != nil {
		_ = roundTrip(in.State, &st)
	}
	if st.Step < views.StepSigners {
		st.Step = views.StepSigners
	}
	return st
}

func (a *App) viewRequest(in *wire.ViewEventInput) (*wire.Surface, error) {
	l := langOfView(in)
	doc := docOf(in, docRef)
	doc.Authority = a.ca().Name
	if doc.ReadOnly {
		return withPath(views.ReadOnlyDoc(l, doc, true), doc), nil
	}
	if isOffice, engine := office(doc, in); isOffice {
		if pressed(in, "convert") {
			return convertJob(), nil
		}
		return withPath(views.RequestOffice(l, doc, engine), doc), nil
	}
	// ⚠⚠ Asked on EVERY event, not only on open: somebody else may have
	// sent a request on this document while this wizard was being filled
	// in, and Send must not become the host's bare "Invalid data".
	prev, _ := a.load(docRef)
	if pressed(in, views.ActionOpenStatus) && doc.Path != "" {
		return &wire.Surface{Open: &wire.OpenRequest{Path: doc.Path, View: ViewStatus}}, nil
	}
	if prev != nil && !prev.Status.Closed() {
		return withPath(views.AlreadyRequested(l, doc, prev), doc), nil
	}
	st := requestStateOf(in)
	st.Life = a.linkLife(in.Context.ShareMaxTTLDays)
	vals := valuesOf(in)
	errs := map[string]wire.Text{}

	switch {
	case in.Event == "open":
		// The wizard as a person first sees it: the declared defaults, and
		// the links' life as this installation allows it.
		life := st.Life
		st = views.NewRequestState()
		st.Life = life
	case pressed(in, "back"):
		if err := absorb(&st, vals, l); err != nil {
			a.logf("warn", "request wizard: the posted boxes could not be read: %v", err)
			errs[views.IDFields] = unreadableBoxes()
			break
		}
		st.Step = st.Prev()
	case in.Event == "submit" || in.Event == "change":
		if err := absorb(&st, vals, l); err != nil {
			a.logf("warn", "request wizard: the posted boxes could not be read: %v", err)
			errs[views.IDFields] = unreadableBoxes()
			break
		}
		if in.Event == "change" {
			break
		}
		switch st.Step {
		case views.StepSigners:
			people := st.People()
			if len(people) == 0 {
				errs[views.IDSigners] = views.T("Add at least one signer: pick somebody, or write a name or an e-mail address.",
					"En az bir imzacı ekleyin: birini seçin ya da bir ad veya e-posta adresi yazın.")
			}
			if len(people) > envelope.MaxSigners {
				errs[views.IDSigners] = views.Tf("At most %d signers.", "En fazla %d imzacı.", envelope.MaxSigners)
			}
		case views.StepBoxes:
			// Only what the boxes ARE is checked here. Where they sit is the
			// next step's business, and refusing this one for a box nobody
			// has placed yet would be refusing somebody for not having done
			// the thing we are about to ask them to do.
			tmp := &envelope.Envelope{Fields: st.Fields}
			for i, p := range st.People() {
				tmp.Signers = append(tmp.Signers, envelope.Signer{ID: fmt.Sprintf("s%d", i+1), Person: p})
			}
			// ⚠ A name may be written in any script and may hold spaces; one
			// that is NOTHING BUT spaces is refused here rather than folded
			// to "no name", because the box would then quietly wear its
			// kind's name and the person would never learn that the name
			// they typed was thrown away.
			if n := blankNames(st.Fields); n > 0 {
				errs[views.IDFields] = views.Tf("%d box(es) are named with spaces alone. Write a name, or clear it and the kind's own name stands in.",
					"%d kutunun adı yalnız boşluktan oluşuyor. Bir ad yazın ya da adı tamamen silin, kutu kendi türünün adıyla anılsın.", n)
			} else if msg := everybodyHasSomething(tmp); msg != nil {
				errs[views.IDFields] = msg
			}
		case views.StepPlace:
			if n := len(envelope.Unplaced(st.Fields)); n > 0 {
				errs[views.IDFields] = views.Tf("%d box(es) still have no place on the document.",
					"%d kutunun belgede yeri yok.", n)
			}
		case views.StepTiming:
			if d := str(vals, "deadline"); d != "" && st.Opts.Deadline == "" {
				errs["deadline"] = views.T("Write the day as YYYY-MM-DD, or leave it empty.", "Günü YYYY-AA-GG biçiminde yazın ya da boş bırakın.")
			}
		case views.StepReview:
			return a.queueRequest(st), nil
		}
		if len(errs) == 0 {
			st.Step = st.Next()
		}
	}
	st.Previous = prev
	st.LinkDays = ttlDays(st.WithDefaults().Opts, a.H.Now(), st.Life)
	return withPath(views.Request(l, doc, st, labelsOf(st.People()), errs), doc), nil
}

// blankNames counts the boxes whose name is there but says nothing.
func blankNames(fs []envelope.Field) int {
	n := 0
	for _, f := range fs {
		if blankName(f.Label) {
			n++
		}
	}
	return n
}

func (a *App) queueRequest(st views.RequestState) *wire.Surface {
	st = st.WithDefaults()
	var props []map[string]any
	for _, f := range st.Fields {
		props = append(props, views.FieldProp(f))
	}
	var people []map[string]any
	for _, p := range st.People() {
		people = append(people, map[string]any{"user_id": p.UserID, "email": p.Email, "name": p.Name})
	}
	o := st.Opts
	return &wire.Surface{Job: &wire.JobRequest{ActionID: ActionRequest, Params: map[string]any{
		"signers": people, "fields": props,
		"pin": o.PIN, "expiry": o.ExpiryDays, "message": o.Message, "order": o.Order,
		"deadline": o.Deadline, "remind_every": o.RemindEveryDays,
		"allow_decline": o.AllowDecline, "lock": o.Lock, "lock_signed": o.LockSigned, "audit": o.Audit,
		"deliver": o.Deliver, "delivery_pin": o.DeliveryPIN,
		"output_mode": o.Output.Normalized().Mode, "output_name": o.Output.Normalized().Name,
	}, Output: &wire.Output{Mode: envelope.OutputNone}}}
}

// absorb copies the posted values of the current step into the state.
// ⚠⚠ A field list that cannot be read comes back as an ERROR, never as a
// list quietly left as it was — see absorbSelf.
func absorb(st *views.RequestState, vals map[string]any, l views.Lang) error {
	switch st.Step {
	case views.StepSigners:
		if v, ok := vals[views.IDSigners]; ok {
			st.Signers = parsePeople(v)
		}
		if has(vals, "identities") {
			st.Identities = clip(str(vals, "identities"), 2000)
		}
	case views.StepOrder:
		if v := str(vals, "order"); v != "" {
			st.Opts.Order = envelope.OrderParallel
			if v == envelope.OrderSequential {
				st.Opts.Order = envelope.OrderSequential
			}
		}
	case views.StepBoxes, views.StepPlace:
		if v, ok := vals[views.IDFields]; ok {
			fs, err := parseFields(v, "", l)
			if err != nil {
				return err
			}
			for i := range fs {
				fs[i].Value = ""
			}
			st.Fields = fs
		}
	case views.StepTiming:
		if has(vals, "expiry") {
			st.Opts.ExpiryDays = st.Life.Clamp(num(vals, "expiry"))
		}
		if has(vals, "deadline") {
			st.Opts.Deadline = ""
			if d := str(vals, "deadline"); d != "" {
				if t, ok := fields.ParseDate(d, fields.DateYMD); ok {
					st.Opts.Deadline = t.Format(fields.ISO)
				}
			}
		}
		if has(vals, "remind_every") {
			st.Opts.RemindEveryDays = num(vals, "remind_every")
		}
	case views.StepOptions:
		if v := str(vals, "pin"); v != "" {
			st.Opts.PIN = pinOption(v)
		}
		if has(vals, "allow_decline") {
			st.Opts.AllowDecline = boolOf(vals, "allow_decline")
		}
		if has(vals, "lock") {
			st.Opts.Lock = boolOf(vals, "lock")
		}
		if has(vals, "lock_signed") {
			st.Opts.LockSigned = boolOf(vals, "lock_signed")
		}
		if has(vals, "message") {
			st.Opts.Message = clip(str(vals, "message"), 500)
		}
	case views.StepResult:
		if has(vals, "output_mode") {
			st.Opts.Output = outputFromValues(vals)
		}
		if v := str(vals, "deliver"); v != "" {
			st.Opts.Deliver = envelope.DeliverShare
			if v == envelope.DeliverNone {
				st.Opts.Deliver = envelope.DeliverNone
			}
		}
		if v := str(vals, "delivery_pin"); v != "" {
			st.Opts.DeliveryPIN = pinOption(v)
		}
		if has(vals, "audit") {
			st.Opts.Audit = boolOf(vals, "audit")
		}
	}
	return nil
}

// ── sign-fill: a signer, in the app or on their own link ───────────────

func (a *App) viewFill(in *wire.ViewEventInput) (*wire.Surface, error) {
	l := langOfView(in)
	doc := docOf(in, docRef)
	env, err := a.load(docRef)
	if err != nil {
		return views.Gone(views.T("This document has no open signature request.", "Bu belgenin açık bir imza isteği yok.")), nil
	}
	doc.Name = env.Document
	var actorID int64
	email := ""
	if in.Context.Actor != nil {
		actorID, email = in.Context.Actor.ID, in.Context.Actor.Email
	}
	sg := env.SignerForUser(actorID, email)
	if sg == nil {
		return withPath(views.NotASigner(l, env, env.Requester.UserID == actorID), doc), nil
	}
	if sg.Status == envelope.SignerSigned {
		// They are done: the useful screen is their receipt, not a form.
		return withPath(views.Receipt(l, a.receiptOf(docRef, env, sg, l, true)), doc), nil
	}
	// ⚠⚠ Opening is NOT recorded here, and nothing is attempted. This screen
	// is a `view_event`, and filex never lets a view write state — by design:
	// a screen is asked on every render and every change, by anybody who may
	// open it. It used to try anyway ("the host may let it"), the write was
	// refused on EVERY open, and every open logged "recording the open:
	// permission_denied" (2026-09-21) — a warning for a condition that is
	// certain teaches an administrator to stop reading the log.
	//
	// What that costs, said plainly: the requester hears that an INSIDE
	// signer opened the document only from the signing link's page (a page
	// call may write — page.go) or from the `apply` job's `viewed` op, and in
	// the app they hear it when the signer signs or refuses. Recording it from
	// here needs filex to let a screen start a job without replacing itself
	// (the SIGN.md wish list), not a write the host is known to refuse.
	// A signer in the app is known by the address of their own screen, as
	// a stranger on a link is by the visitor's — the IP line under a
	// signature must not print for one and not the other.
	return a.fillFlow(l, in, doc, env, sg, true, actorIP(in))
}

// fillFlow is the signer's three steps, identical for somebody inside
// filex and for an outside signer on their own link.
func (a *App) fillFlow(l views.Lang, in *wire.ViewEventInput, doc views.Doc, env *envelope.Envelope,
	sg *envelope.Signer, inApp bool, visitorIP string) (*wire.Surface, error) {

	st := fillStateOf(in, sg.ID)
	vals := valuesOf(in)
	errs := map[string]wire.Text{}

	if (pressed(in, "decline") || pressed(in, "decline_confirm")) && !env.Options.AllowDecline {
		// The button is not drawn, but a crafted event must not open a door
		// the requester closed.
		return withPath(views.Fill(l, doc, env, sg, st, nil), doc), nil
	}
	switch {
	case pressed(in, "decline"):
		return views.Decline(l, env, sg, nil), nil
	case pressed(in, "decline_confirm"):
		params := map[string]any{"op": "decline", "envelope_id": env.ID, "signer_id": sg.ID,
			"reason": str(vals, "decline_reason")}
		if visitorIP != "" {
			params["visitor_ip"] = visitorIP
		}
		return &wire.Surface{Job: &wire.JobRequest{ActionID: ActionApply, Params: params,
			Output: &wire.Output{Mode: envelope.OutputNone}}}, nil
	case pressed(in, "back"):
		a.absorbFill(env, sg, &st, vals)
		if st.Step > views.FillStepIntro {
			st.Step--
		}
	case in.Event == "submit" || in.Event == "change":
		a.absorbFill(env, sg, &st, vals)
		if in.Event == "change" {
			break
		}
		switch st.Step {
		case views.FillStepIntro:
			st.Step = views.FillStepFill
		case views.FillStepFill:
			mine := env.FieldsFor(sg.ID)
			checkValues(l, env.Fields, mine, st.Values, errs)
			if v := firstDrawnField(mine, sg.ID); v != nil && st.Values[v.ID] == "" {
				errs[views.PadFor(v.ID)] = views.T("Draw, type or upload your signature.", "İmzanızı çizin, yazın ya da yükleyin.")
			}
			if len(errs) == 0 {
				st.Step = views.FillStepReview
			}
		default:
			return a.submitFill(l, doc, env, sg, st, inApp, visitorIP)
		}
	}
	if st.Step == views.FillStepReview && len(errs) == 0 {
		// ⚠⚠ What will be printed under each of their signatures, before
		// they press Sign — composed by the printer's own function with the
		// facts that will be printed (the owner, 2026-09-21: an IP address
		// is personal data and must not reach the paper unseen).
		f := stamp.Facts{Name: sg.Person.CertName(), Email: sg.Person.Email, When: a.H.Now(), IP: visitorIP,
			Authority: a.ca().Name, PendingCert: true, PendingWho: stamp.You}
		mine := env.FieldsFor(sg.ID)
		st.Lines = linesFor(env.Options.Locale, mine, f)
		st.Preview = a.previews(doc.Ref, env.Options.Locale, mine, st.Values, f)
	}
	return withPath(views.Fill(l, doc, env, sg, st, errs), doc), nil
}

func fillStateOf(in *wire.ViewEventInput, signerID string) views.FillState {
	var st views.FillState
	if in.State != nil {
		_ = roundTrip(in.State, &st)
	}
	if st.Step < views.FillStepIntro {
		st.Step = views.FillStepIntro
	}
	if st.Values == nil {
		st.Values = map[string]string{}
	}
	st.Signer = signerID
	return st
}

func (a *App) absorbFill(env *envelope.Envelope, sg *envelope.Signer, st *views.FillState, vals map[string]any) {
	if st.Step != views.FillStepFill && st.Step != views.FillStepReview {
		return
	}
	a.absorbValues(env.FieldsFor(sg.ID), vals, st.Values)
}

// drawingBudget is how many bytes each of a signer's drawings may take.
//
// ⚠⚠ Every drawing rides in ONE job's parameters, which filex caps at
// 64 KiB, and each box has a pad of its own. At 40 KiB apiece two
// signature boxes asked for 80 — the job was refused at the door (413)
// and the signer, who had done everything right, could not sign. The
// budget is shared out now, with room left for the typed values.
func drawingBudget(fs []envelope.Field) int {
	n := 0
	for _, f := range fs {
		if fields.Drawn(f.Type) {
			n++
		}
	}
	if n <= 1 {
		return pngBudget
	}
	return min(pngBudget, drawingsBudget/n)
}

// absorbValues reads one fill step: the form fields under their own
// keys, and each signature box's pad under the pad's own id. A drawing
// is shrunk here, once, so the state a browser carries between steps
// never holds a full-size image.
func (a *App) absorbValues(fs []envelope.Field, vals map[string]any, into map[string]string) {
	budget := drawingBudget(fs)
	for _, f := range fs {
		if fields.Drawn(f.Type) {
			png, _ := parsePad(vals[views.PadFor(f.ID)])
			if png == "" {
				continue
			}
			raw := decodeImage(png)
			small, err := pdfsig.Normalize(raw, budget)
			if err != nil {
				a.logf("warn", "signature image: %v", err)
				continue
			}
			into[f.ID] = b64of(small)
			continue
		}
		if !has(vals, f.ID) {
			continue
		}
		if f.Type == fields.TypeCheckbox {
			if boolOf(vals, f.ID) {
				into[f.ID] = "true"
			} else {
				delete(into, f.ID)
			}
			continue
		}
		if v := clip(str(vals, f.ID), 400); v != "" {
			into[f.ID] = v
		} else {
			delete(into, f.ID)
		}
	}
}

// checkValues runs every box's own rule before anything is queued: a
// refusal here is a sentence the signer can act on, not a failed job.
func checkValues(l views.Lang, all, fs []envelope.Field, held map[string]string, errs map[string]wire.Text) {
	for _, f := range fs {
		if fields.Drawn(f.Type) {
			// ⚠ A required signature box of the signer's OWN that was left
			// empty is refused here like any other required box — not only
			// the first one, which was all that was ever checked. A box that
			// belongs to ANYONE is not forced on whoever gets there first:
			// it may be the one another signer is counting on.
			if f.Required && f.Assignee != "" && strings.TrimSpace(held[f.ID]) == "" {
				errs[views.PadFor(f.ID)] = views.T("Draw, type or upload your signature.", "İmzanızı çizin, yazın ya da yükleyin.")
				if f.Typed() {
					errs[views.PadFor(f.ID)] = views.T("Type your name for this signature.", "Bu imza için adınızı yazın.")
				}
			}
			continue
		}
		val := strings.TrimSpace(held[f.ID])
		if f.Type == fields.TypeDate && val == "" && f.Required {
			continue // the job stamps today
		}
		if prob := fields.Check(f.Spec(), val); prob != nil {
			name := views.NameIn(l, all, f)
			q := *prob
			q.Name = name
			errs[f.ID] = views.Problem(&q)
		}
	}
}

// submitFill turns the finished screen into the `apply` job.
func (a *App) submitFill(l views.Lang, doc views.Doc, env *envelope.Envelope, sg *envelope.Signer,
	st views.FillState, inApp bool, visitorIP string) (*wire.Surface, error) {

	mine := env.FieldsFor(sg.ID)
	errs := map[string]wire.Text{}
	checkValues(l, env.Fields, mine, st.Values, errs)
	png, first := "", ""
	if v := firstDrawnField(mine, sg.ID); v != nil {
		png, first = st.Values[v.ID], v.ID
		if png == "" {
			errs[views.PadFor(v.ID)] = views.T("Draw, type or upload your signature.", "İmzanızı çizin, yazın ya da yükleyin.")
		}
	}
	if len(errs) > 0 {
		st.Step = views.FillStepFill
		return views.Fill(l, doc, env, sg, st, errs), nil
	}
	params := map[string]any{
		"op": "sign", "envelope_id": env.ID, "signer_id": sg.ID, "png_b64": png,
		"values": valueParams(mine, st.Values, first),
	}
	if visitorIP != "" {
		params["visitor_ip"] = visitorIP
	}
	return &wire.Surface{Job: &wire.JobRequest{ActionID: ActionApply, Params: params,
		Output: jobOutput(env.Options.Output)}}, nil
}

// valueParams is what the job is handed: field id → value and the face
// it is stamped in. The face rides WITH the value because the job never
// sees the screen. `skip` is the box whose drawing already travels as
// `png_b64`: sending it twice spent a third of the 64 KiB a job may carry.
func valueParams(fs []envelope.Field, held map[string]string, skip string) map[string]any {
	out := map[string]any{}
	for _, f := range fs {
		v, ok := held[f.ID]
		if !ok || v == "" || f.ID == skip {
			continue
		}
		m := map[string]any{"value": v}
		if f.Font != "" {
			m["font"] = f.Font
		}
		out[f.ID] = m
	}
	return out
}

// ── status (details panel) ─────────────────────────────────────────────

func (a *App) viewStatus(in *wire.ViewEventInput) (*wire.Surface, error) {
	l := langOfView(in)
	doc := docOf(in, docRef)
	env, err := a.load(docRef)
	if err != nil && !errors.Is(err, envelope.ErrNotFound) {
		a.logf("warn", "status: %v", err)
	}
	ca := a.ca()
	st := views.StatusInput{Doc: doc, Env: env, SignaturesInFile: a.countSignatures(in),
		CAName: ca.Name, CAFP: ca.FP}
	if env != nil {
		st.Expired = a.expired(env)
		st.RemindDue = a.remindDue(env)
	}
	switch {
	case pressed(in, "link"):
		rowID, _ := in.Data["row_id"].(string)
		st.ShowLinkFor = rowID
		return views.Status(l, st), nil
	case pressed(in, "remind"):
		rowID, _ := in.Data["row_id"].(string)
		if env == nil || env.Signer(rowID) == nil {
			st.Toast = views.T("Unknown signer.", "Bilinmeyen imzacı.")
			return views.Status(l, st), nil
		}
		return &wire.Surface{Job: &wire.JobRequest{ActionID: ActionApply,
			Params: map[string]any{"op": "remind", "signer_id": rowID}, Output: &wire.Output{Mode: envelope.OutputNone}}}, nil
	case pressed(in, "cancel"):
		if env == nil {
			return views.Status(l, st), nil
		}
		return &wire.Surface{Job: &wire.JobRequest{ActionID: ActionApply,
			Params: map[string]any{"op": "cancel"}, Output: &wire.Output{Mode: envelope.OutputNone}}}, nil
	case pressed(in, "expire"):
		if env == nil {
			return views.Status(l, st), nil
		}
		return &wire.Surface{Job: &wire.JobRequest{ActionID: ActionApply,
			Params: map[string]any{"op": "expire"}, Output: &wire.Output{Mode: envelope.OutputNone}}}, nil
	case pressed(in, "audit"):
		if env == nil {
			return views.Status(l, st), nil
		}
		return &wire.Surface{Job: &wire.JobRequest{ActionID: ActionApply, Params: map[string]any{"op": "audit"},
			Output: &wire.Output{Mode: envelope.OutputSibling, Name: "{stem}-audit.pdf"}}}, nil
	}
	return views.Status(l, st), nil
}

// expired reports whether the request has run out. Views may not write
// state, so the panel only draws it and offers the button that closes the
// request — which is also what releases the file.
//
// ⚠ WHEN it runs out is lapseAt's business and nobody else's (tick.go):
// the hourly wake-up schedules the closure for exactly that instant, so a
// second opinion here would mean the panel and the unattended job
// disagreed about whether a request was over.
func (a *App) expired(env *envelope.Envelope) bool {
	if env == nil || env.Status.Closed() {
		return false
	}
	at := lapseAt(env)
	return !at.IsZero() && !at.After(a.H.Now())
}

// remindDue reports whether nobody has moved for as long as the
// requester said they were willing to wait — the same arithmetic the
// scheduled reminder uses, asked of the present moment.
func (a *App) remindDue(env *envelope.Envelope) bool {
	if env.Status.Closed() {
		return false
	}
	now := a.H.Now()
	for i := range env.Signers {
		at := remindAt(env, &env.Signers[i])
		if !at.IsZero() && !at.After(now) {
			return true
		}
	}
	return false
}

// ── home ──────────────────────────────────────────────────────────────

// homeListLimit is how many documents the Signatures screen asks the
// host for. It is a LIST, not a search: the explorer is where somebody
// looks for one file among thousands, and the screen says so when the
// host had more.
const homeListLimit = 100

func (a *App) viewHome(in *wire.ViewEventInput) (*wire.Surface, error) {
	l := langOfView(in)
	ca := a.ca()
	out := views.HomeInput{
		CAOK: ca.OK, CAReason: ca.Reason, CAName: ca.Name, CAFP: ca.FP,
		Office: in.Context.Engines[officeEngine],
		// The actor carries an ACL role, not filex's own administrator
		// flag, so this is the closest the guest can get. It only decides
		// whether an extra SECTION is drawn: every row in it came through
		// the asker's own permissions, so it can widen nothing.
		Admin: in.Context.Actor != nil && strings.EqualFold(in.Context.Actor.Role, "admin"),
		// The section the page's address names (`data.section` on the
		// opening event), else the one this screen was already showing.
		Section: homeSectionOf(in),
	}
	var actorID int64
	email := ""
	if in.Context.Actor != nil {
		actorID, email = in.Context.Actor.ID, in.Context.Actor.Email
	}

	// One call answers every section: the record itself comes back with
	// the row, so there is nothing to fetch per document.
	names := map[string]string{}
	// Row id → the link's token. Built here and used here: a token never
	// reaches the browser, so the row the person pressed is resolved back
	// to its link from this call's own map (views.PinLinksOf).
	pinTokens := map[string]string{}
	items, err := a.H.StateList(envelope.StateKey, homeListLimit)
	if err != nil {
		a.logf("warn", "listing the requests: %v", err)
	}
	out.Truncated = len(items) >= homeListLimit
	withRequest := map[string]bool{}
	for _, it := range items {
		env, err := envelope.Decode(it.Value)
		if err != nil {
			a.logf("warn", "unreadable request on %s: %v", it.Path, err)
			continue
		}
		withRequest[it.Path] = true
		c := cardOf(env, it)
		names[c.Path] = c.Document
		sg := env.SignerForUser(actorID, email)
		switch {
		case sg != nil && sg.Status == envelope.SignerSigned:
			out.Signed = append(out.Signed, c)
		case sg != nil && !sg.Done() && !env.Status.Closed():
			out.ToSign = append(out.ToSign, c)
		}
		if actorID != 0 && env.Requester.UserID == actorID {
			out.Requested = append(out.Requested, c)
			// ⚠⚠ The PIN rows are the REQUESTER's alone — the person the PIN
			// was promised to. The host would let an administrator read
			// somebody else's (it is the same door "My shares" opens), but
			// this screen does not offer it: a secret's audience is not
			// widened because a section next to it happens to be wider.
			rows, toks := views.PinLinksOf(env, c.Path, c.Document, a.H.Now())
			out.Pins = append(out.Pins, rows...)
			for id, tok := range toks {
				pinTokens[id] = tok
			}
		}
		// ⚠ EVERY request, the administrator's own included. The section
		// is titled "every request in this installation", and a list that
		// silently leaves out the reader's own rows is a list that lies:
		// an administrator counting open requests would come up short by
		// exactly the ones they started themselves.
		if out.Admin {
			all := c
			all.Requester = env.Requester.Display()
			out.All = append(out.All, all)
		}
	}

	// A document somebody signed by themselves carries no request at all,
	// only the badge — and the badge names the signers with an account,
	// which is the one thing that cannot be worked out from the file.
	badges, err := a.H.StateList(envelope.SignedKey, homeListLimit)
	if err != nil {
		a.logf("warn", "listing the signed documents: %v", err)
	}
	for _, it := range badges {
		if withRequest[it.Path] || !envelope.HasSigner(it.Value, actorID) {
			continue
		}
		name := it.Name
		if name == "" {
			name = it.Path
		}
		names[it.Path] = name
		out.Signed = append(out.Signed, views.Card{Document: name, Path: it.Path,
			Status: envelope.StatusCompleted, Signed: 1, Total: 1})
	}

	for _, cards := range [][]views.Card{out.ToSign, out.Requested, out.Signed, out.All} {
		sort.SliceStable(cards, func(i, j int) bool { return cards[i].Updated > cards[j].Updated })
	}

	// A row IS a document, so clicking it goes there — on the screen that
	// row's section is about. The path only ever comes from a row this
	// very call drew, so a crafted event cannot name somebody else's file;
	// the host re-checks it anyway and drops the link when the asker may
	// not see it.
	if in.Event == "action" {
		switch in.ActionID {
		case views.ShowPIN:
			id, _ := in.Data["row_id"].(string)
			if tok := pinTokens[id]; tok != "" {
				pin, reason, err := a.H.SharePIN(tok)
				if err != nil {
					a.logf("warn", "reading a link's PIN: %v", err)
					reason = "not_recoverable"
				}
				for i := range out.Pins {
					if out.Pins[i].ID != id {
						continue
					}
					out.Pins[i].PIN, out.Pins[i].Reason = pin, reason
					shown := out.Pins[i]
					out.Shown = &shown
				}
			}
			out.Section = views.SectionPins
		case views.OpenSign, views.OpenFollow, views.OpenVerify:
			path, _ := in.Data["row_id"].(string)
			if _, listed := names[path]; listed {
				action, view := views.Target(in.ActionID)
				return &wire.Surface{Open: pluginkit.OpenFile(path, action, view)}, nil
			}
		}
	}
	return views.Home(l, out), nil
}

// homeSectionOf is the Signatures page's section this call is about: the
// one its address asked for when it was opened, the one on screen after
// that. Views.Home falls back to the default for anything it does not know.
func homeSectionOf(in *wire.ViewEventInput) string {
	if in.Data != nil {
		if s, ok := in.Data["section"].(string); ok && s != "" {
			return s
		}
	}
	if in.State != nil {
		if s, ok := in.State["section"].(string); ok {
			return s
		}
	}
	return ""
}

// cardOf is one row: the host's own name and path for the file, and the
// facts the record carries.
func cardOf(env *envelope.Envelope, it pluginkit.StateItem) views.Card {
	signed, total := env.Progress()
	var waiting []string
	for _, s := range env.Signers {
		if !s.Done() {
			waiting = append(waiting, s.Person.Display())
		}
	}
	name := it.Name
	if name == "" {
		name = env.Document
	}
	return views.Card{
		Document: name, Path: it.Path, Status: env.Status,
		Signed: signed, Total: total, Waiting: strings.Join(waiting, ", "),
		Deadline: dueDay(env), Locked: env.Locked, Updated: envelope.Day(env.UpdatedAt),
	}
}

// dueDay is the "Due" column: the last day an open request can still be
// signed on — the sign-by day when one was asked for, otherwise the day its
// last signing link runs out. Empty for a closed request and for one that
// never runs out by itself.
//
// ⚠⚠ It used to print Options.Deadline alone, which is set only when the
// requester typed a sign-by day, so the column read "—" on every request
// (2026-09-21, a tester) although each one expires on a real date — the day
// its links die, which is when the request closes (lapseAt). lapseAt is the
// ONE definition of "over"; this reads it, a second opinion would drift.
func dueDay(env *envelope.Envelope) string {
	if env == nil || env.Status.Closed() {
		return ""
	}
	at := lapseAt(env)
	if at.IsZero() {
		return ""
	}
	// lapseAt is the instant it is over; the day a person reads is the last
	// one on which it is still open — a request over at midnight after the
	// 24th is due ON the 24th.
	return at.Add(-time.Second).UTC().Format(fields.ISO)
}
