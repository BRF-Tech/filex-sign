package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/humandate"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	filexsign "github.com/brf-tech/filex-sign"
	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/host"
	"github.com/brf-tech/filex-sign/internal/testpdf"
	"github.com/brf-tech/filex-sign/internal/verify"
	"github.com/brf-tech/filex-sign/internal/views"
)

// ── fixtures ───────────────────────────────────────────────────────────

const docName = "sözleşme.pdf"

var burak = wire.Actor{ID: 1, Email: "burak@example.com", Name: "Burak Faruk Şahin", Role: "owner"}
var gokce = wire.Actor{ID: 7, Email: "gokce@example.com", Name: "Gökçe", Role: "editor"}

func padPNG(t *testing.T) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 300, 100))
	for x := 20; x < 280; x++ {
		for dy := 0; dy < 3; dy++ {
			img.Set(x, 50+(x/20)%7+dy, color.NRGBA{R: 10, G: 20, B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

const docPath = "docs://sozlesmeler/" + docName

func newApp(t *testing.T) (*App, *host.Fake) {
	t.Helper()
	f := host.NewFake()
	f.Inputs["in:0"] = testpdf.Build(testpdf.Options{Pages: []testpdf.Page{
		testpdf.Letter(), {Width: 612, Height: 792, Rotate: 90, Text: "p2"}}})
	f.Register("in:0", docPath, docName)
	return New(f, filexsign.Manifest()), f
}

// homeAs opens the Signatures screen as somebody, the way filex does: no
// document at all.
func homeAs(t *testing.T, a *App, actor wire.Actor, event, actionID string, data map[string]any) *wire.Surface {
	t.Helper()
	in := &wire.ViewEventInput{ViewID: ViewHome, Event: event, ActionID: actionID,
		Data:    data,
		Context: wire.CallContext{Actor: &actor, Locale: "tr"}}
	if in.Data == nil {
		in.Data = map[string]any{}
	}
	s, err := a.viewHome(in)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// rowsOf collects every list row of a surface as id → its one action.
func rowsOf(s *wire.Surface) map[string]string {
	out := map[string]string{}
	var walk func(nodes []wire.Node)
	walk = func(nodes []wire.Node) {
		for _, n := range nodes {
			if n.Type == "list" {
				rows, _ := n.Props["rows"].([]map[string]any)
				for _, r := range rows {
					id, _ := r["id"].(string)
					acts, _ := r["actions"].([]map[string]any)
					if len(acts) > 0 {
						out[id], _ = acts[0]["id"].(string)
					}
				}
			}
			walk(n.Children)
		}
	}
	walk(s.Nodes)
	return out
}

func viewInput(view, event, actionID string, state, values map[string]any) *wire.ViewEventInput {
	return viewInputAs(burak, view, event, actionID, state, values)
}

func viewInputAs(actor wire.Actor, view, event, actionID string, state, values map[string]any) *wire.ViewEventInput {
	in := &wire.ViewEventInput{ViewID: view, Event: event, ActionID: actionID, State: state,
		Data: map[string]any{"values": values},
		Context: wire.CallContext{Inputs: []wire.FileRef{{Ref: "in:0", Name: docName, Size: 1000}},
			Actor: &actor, Locale: "tr"}}
	if values == nil {
		in.Data["values"] = map[string]any{}
	}
	return in
}

// jobFrom turns a surface's queued job into the input the host would hand
// the guest, output override included — the whole point of job.output.
func jobFrom(t *testing.T, s *wire.Surface, actor wire.Actor, name string) *wire.ActionRunInput {
	t.Helper()
	if s == nil || s.Job == nil {
		t.Fatalf("no job queued: %+v", s)
	}
	in := &wire.ActionRunInput{JobID: "job1", ActionID: s.Job.ActionID, Locale: "tr", Actor: actor,
		Params: jsonRound[map[string]any](t, s.Job.Params),
		Inputs: []wire.FileRef{{Ref: "in:0", Name: name, Size: 1000, Mime: "application/pdf"}}}
	if s.Job.Output != nil {
		in.Output = *s.Job.Output
	}
	return in
}

// jsonRound pushes a value through JSON, as the host does, so the
// map[string]any shapes a handler sees are realistic.
func jsonRound[T any](t *testing.T, v any) T {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// commit stands in for what filex does with a job's output in `version`
// mode: the document becomes what the job wrote, so the NEXT signer
// works on the signed file.
func commit(t *testing.T, f *host.Fake, out *wire.ActionRunOutput) []byte {
	t.Helper()
	if out == nil || len(out.Outputs) == 0 {
		t.Fatalf("the job produced no file: %+v", out)
	}
	for _, o := range f.Outputs {
		if o.Ref == out.Outputs[0].Ref {
			f.Inputs["in:0"] = append([]byte(nil), o.Data...)
			return o.Data
		}
	}
	t.Fatalf("output %s is not among the written files", out.Outputs[0].Ref)
	return nil
}

func padValue(t *testing.T, fieldID string) map[string]any {
	return map[string]any{views.PadFor(fieldID): map[string]any{"png_b64": padPNG(t), "mode": "draw"}}
}

func merge(maps ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func sigBox(id, assignee string, x float64) map[string]any {
	return map[string]any{"id": id, "type": "signature", "page": 1, "x": x, "y": 0.8, "w": 0.3, "h": 0.08,
		"assignee": assignee, "label": "İmza"}
}

func textBox(id, assignee string, x float64) map[string]any {
	return map[string]any{"id": id, "type": "text", "page": 1, "x": x, "y": 0.6, "w": 0.3, "h": 0.04,
		"assignee": assignee, "required": true, "label": "Görev unvanı"}
}

func dateBox(id, assignee string, x float64) map[string]any {
	return map[string]any{"id": id, "type": "date", "page": 1, "x": x, "y": 0.5, "w": 0.2, "h": 0.04,
		"assignee": assignee, "label": "Tarih"}
}

// noticesTitled picks the notifications carrying one title, so a test can
// ask both "was this said?" and "how many times?" — the second question
// being the whole point of an event that may only happen once.
func noticesTitled(list []pluginkit.Notice, titleTR string) []pluginkit.Notice {
	var out []pluginkit.Notice
	for _, n := range list {
		if n.Title["tr"] == titleTR {
			out = append(out, n)
		}
	}
	return out
}

// mailsTo picks the mails sent to one address.
func mailsTo(list []host.FakeMail, to string) []host.FakeMail {
	var out []host.FakeMail
	for _, m := range list {
		if m.To == to {
			out = append(out, m)
		}
	}
	return out
}

func loadEnv(t *testing.T, a *App) *envelope.Envelope {
	t.Helper()
	env, err := a.load("in:0")
	if err != nil {
		t.Fatalf("no envelope: %v", err)
	}
	return env
}

// ── the manifest is the contract ───────────────────────────────────────

// The manifest against the SDK's own rules first: the host applies them
// at install, and a test says so before the install does.
func TestManifestPassesTheSDKsOwnChecks(t *testing.T) {
	a, _ := newApp(t)
	// No local additions to plugintest.Permissions: the kit knows every
	// permission the host accepts (`schedule` since v0.43.0), so an unknown
	// one here is a real refusal waiting at install.
	plugintest.CheckManifest(t, filexsign.Manifest())
	plugintest.CheckManifestLanguages(t, filexsign.Manifest())
	plugintest.CheckRegistered(t, a.Plugin())
}

func TestManifestAndHandlersAgree(t *testing.T) {
	a, _ := newApp(t)
	p := a.Plugin()
	m := filexsign.Manifest()

	for _, act := range m.Actions {
		if _, ok := p.Actions[act.ID]; !ok {
			t.Errorf("the manifest offers action %q with no handler", act.ID)
		}
	}
	for _, v := range m.Views {
		if _, ok := p.Views[v.ID]; !ok {
			t.Errorf("the manifest offers view %q with no handler", v.ID)
		}
	}
	for _, pg := range m.PublicPages {
		if _, ok := p.Pages[pg.ID]; !ok {
			t.Errorf("the manifest offers page %q with no handler", pg.ID)
		}
	}
	// …and the other way: a handler nobody can reach is dead code.
	for id := range p.Views {
		if !hasView(m, id) {
			t.Errorf("view %q has a handler but is not in the manifest", id)
		}
	}
	for id := range p.Pages {
		if !hasPage(m, id) {
			t.Errorf("page %q has a handler but is not in the manifest", id)
		}
	}
	if got, want := m.Languages, views.Languages(); !equal(got, want) {
		t.Errorf("the manifest declares %v, the code speaks %v", got, want)
	}
	for _, want := range []string{"settings", "http:" + TSAHost, "public_pages", "sign"} {
		if !contains(m.Permissions, want) {
			t.Errorf("the manifest does not ask for %q", want)
		}
	}
	// ⚠ Every pinned font lives on a host the manifest asks for: a face on
	// another host would be refused by asset_fetch at the first Arabic
	// signature, and the text would silently fall to "cannot be printed".
	for _, f := range fontkit.PinnedFonts() {
		u, err := url.Parse(f.URL)
		if err != nil || u.Scheme != "https" {
			t.Errorf("%s: %q is not an https URL", f.Family, f.URL)
			continue
		}
		if !contains(m.Permissions, "http:"+u.Hostname()) {
			t.Errorf("%s is fetched from %s, which the manifest does not ask for", f.Family, u.Hostname())
		}
	}
	for _, perm := range m.Permissions {
		r, ok := m.PermissionReasons[perm]
		if !ok {
			t.Errorf("%q is asked for with no reason", perm)
			continue
		}
		for _, lang := range views.Languages() {
			if strings.TrimSpace(r[lang]) == "" {
				t.Errorf("the reason for %q has no %s", perm, lang)
			}
		}
	}
	keys := map[string]bool{}
	for _, s := range m.Settings {
		keys[s.Key] = true
		if s.ShowWhen != nil && !keys[s.ShowWhen.Key] {
			t.Errorf("setting %q depends on %q, which is not declared before it", s.Key, s.ShowWhen.Key)
		}
	}
	for _, want := range []string{SettingTSAEnabled, SettingTSAURL} {
		if !keys[want] {
			t.Errorf("the manifest does not declare the %q setting", want)
		}
	}
}

// ⚠⚠ Asking people to sign is a permission the administrator hands out;
// signing is not (Burak, 2026-09-28: "imza isteme bir yetki arkasında
// olmalı; signlama izni diye bir şeye gerek yok"). filex refuses an action
// or a view that `requires` a user permission to an account that does not
// hold it — the menu row disappears, a direct run, a screen's open and its
// events answer 403 — so what carries the requirement decides who is shut
// out:
//
//   - `request` (the menu row) and the `request` wizard: that is asking.
//   - NOT `apply`. It is hidden and queued by BOTH sides: the signer's
//     Sign / Fill screen hands in a signature through it, and the
//     Signatures panel's Remind / Cancel / Close the expired request go
//     through it too. Gated, a signer without the permission could not
//     sign, and a requester whose permission was taken away could no longer
//     cancel the request that keeps somebody's file frozen.
//   - NOT `status` (the details panel) or `envelopes` (the Signatures
//     screen): they show a signer what is waiting for them and let a
//     requester follow what was already sent.
//   - NOT sign, fill, verify, convert, or the outside signer's page.
func TestManifest_AskingIsAPermissionSigningIsNot(t *testing.T) {
	m := filexsign.Manifest()
	if len(m.UserPermissions) != 1 || m.UserPermissions[0].ID != PermRequest {
		t.Fatalf("user_permissions: want exactly %q, got %+v", PermRequest, m.UserPermissions)
	}
	up := m.UserPermissions[0]
	// `user`: accounts that can change files keep asking as they did before
	// the permission existed; a read-only account never could (min_role
	// editor on the file), and the administrator narrows it from there.
	if up.Default != "user" {
		t.Errorf("the request permission defaults to %q, want user", up.Default)
	}
	for _, lang := range views.Languages() {
		if strings.TrimSpace(up.Label[lang]) == "" || strings.TrimSpace(up.Description[lang]) == "" {
			t.Errorf("the request permission has no %s label or description", lang)
		}
	}
	// filex before 0.49.0 does not know user_permissions or requires and
	// refuses the whole manifest, so the range has to say so up front.
	if m.Filex != ">=0.49.0" {
		t.Errorf("filex: %q, want >=0.49.0 (the first filex that knows user_permissions)", m.Filex)
	}

	gatedActions := map[string]bool{ActionRequest: true}
	gatedViews := map[string]bool{ViewRequest: true}
	viewNeeds := map[string]string{}
	for _, v := range m.Views {
		want := ""
		if gatedViews[v.ID] {
			want = PermRequest
		}
		if v.Requires != want {
			t.Errorf("view %s requires %q, want %q", v.ID, v.Requires, want)
		}
		viewNeeds[v.ID] = v.Requires
	}
	for _, act := range m.Actions {
		want := ""
		if gatedActions[act.ID] {
			want = PermRequest
		}
		if act.Requires != want {
			t.Errorf("action %s requires %q, want %q", act.ID, act.Requires, want)
		}
		// A menu row whose screen needs more than the row does is offered
		// and then refused; one that needs less lets the screen's job run
		// what the row would have refused.
		if act.View != "" && viewNeeds[act.View] != act.Requires {
			t.Errorf("action %s requires %q, its view %s requires %q", act.ID, act.Requires, act.View, viewNeeds[act.View])
		}
	}
}

// ⚠ Verify has NO state gate. The plugin only knows what it signed
// itself, so gating on `signed` would hide the button on exactly the
// documents that most need checking.
func TestManifestRules(t *testing.T) {
	m := filexsign.Manifest()
	for _, act := range m.Actions {
		switch act.ID {
		case ActionSign, ActionRequest:
			if !contains(act.Applies.NoState, envelope.PendingKey) {
				t.Errorf("%s should not be offered while a request is open", act.ID)
			}
			// ⚠ A PDF, and only a PDF, whatever engines the server has: an
			// office document is converted with the Convert app first
			// (pdfonly_test.go).
			if !equal(act.Applies.Ext, []string{"pdf"}) || len(act.Applies.EngineExt) != 0 {
				t.Errorf("%s: only a PDF can be signed: %+v", act.ID, act.Applies)
			}
		case ActionFill:
			// ⚠ Only to a person who has something to sign on it NOW: the
			// personal marker (wire.PersonalState), which filex shows to its
			// person alone. `pending` offered it to everybody who could see
			// the document (v0.43.0 wave 2).
			if !contains(act.Applies.State, envelope.TodoKey+wire.PersonalStateSuffix) {
				t.Errorf("%s should only be offered to a signer whose turn it is", act.ID)
			}
		case ActionVerify:
			if len(act.Applies.State) != 0 || len(act.Applies.NoState) != 0 {
				t.Errorf("verify must not be gated on state: %+v", act.Applies)
			}
			if act.MinRole != "viewer" {
				t.Errorf("anybody who may read the file may check it: %q", act.MinRole)
			}
		case ActionApply:
			if !act.Hidden {
				t.Error("apply is the second half of a flow, never a menu row")
			}
		}
	}
	for _, v := range m.Views {
		switch v.ID {
		case ViewSignSelf, ViewRequest, ViewFill, ViewVerify:
			if v.Placement != "page" {
				t.Errorf("%s should be a page, is %q", v.ID, v.Placement)
			}
		case ViewStatus:
			if v.Placement != "inspector" {
				t.Errorf("status is a details-panel section, is %q", v.Placement)
			}
		case ViewHome:
			if v.Placement != "home" {
				t.Errorf("envelopes is the home screen, is %q", v.ID)
			}
		}
	}
}

// ── signing your own document: three steps ─────────────────────────────

func TestSelfSign_ThreeStepsThenJob(t *testing.T) {
	a, f := newApp(t)

	s, err := a.viewSignSelf(viewInput(ViewSignSelf, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.State["step"]; got != views.SelfStepFields {
		t.Fatalf("the first step places the boxes, got %v", got)
	}

	// 1. the boxes
	s, err = a.viewSignSelf(viewInput(ViewSignSelf, "submit", "", s.State,
		map[string]any{views.IDFields: []map[string]any{sigBox("sig-1", "", .5), textBox("text-1", "", .1)}}))
	if err != nil {
		t.Fatal(err)
	}
	if s.State["step"] != views.SelfStepFill {
		t.Fatalf("expected the fill step, got %v (%+v)", s.State["step"], s.Errors)
	}

	// 2. the values — a rule is enforced here, not in the job
	s, err = a.viewSignSelf(viewInput(ViewSignSelf, "submit", "", s.State,
		merge(padValue(t, "sig-1"), map[string]any{"text-1": ""})))
	if err != nil {
		t.Fatal(err)
	}
	if s.Errors["text-1"] == nil {
		t.Fatalf("a required box left empty should be refused on the screen: %+v", s.Errors)
	}
	s, err = a.viewSignSelf(viewInput(ViewSignSelf, "submit", "", s.State,
		merge(padValue(t, "sig-1"), map[string]any{"text-1": "Müdür"})))
	if err != nil {
		t.Fatal(err)
	}
	if s.State["step"] != views.SelfStepResult {
		t.Fatalf("expected the output step, got %v (%+v)", s.State["step"], s.Errors)
	}

	// 3. where it goes
	s, err = a.viewSignSelf(viewInput(ViewSignSelf, "submit", "", s.State,
		map[string]any{"output_mode": envelope.OutputSibling, "output_name": "{stem}-imzali{ext}", "reason": "Onaylandı"}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := a.actionSign(jobFrom(t, s, burak, docName))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("the job failed: %v", out.Message)
	}
	if len(f.Outputs) != 1 || f.Outputs[0].Name != "sözleşme-imzali.pdf" {
		t.Fatalf("unexpected output: %+v", f.Outputs)
	}
	rep, err := verify.Inspect(f.Outputs[0].Data, bundle(t, f))
	if err != nil {
		t.Fatal(err)
	}
	// The person's signature, which certifies the document, and filex's
	// seal over the whole of it.
	if len(rep.Signatures) != 2 {
		t.Fatalf("the signature and the seal expected, got %d", len(rep.Signatures))
	}
	sig, seal := rep.Signatures[0], rep.Signatures[1]
	if !sig.Valid || !sig.Trusted || !seal.Valid || !seal.Trusted {
		t.Errorf("the signature and the seal should be valid and trusted: %+v / %+v", sig, seal)
	}
	if sig.Certification != CertifyPermission || !seal.Seal || !seal.CoversWholeFile || !rep.Sealed || rep.NotPermitted {
		t.Errorf("certified, then sealed: cert=%d seal=%v whole=%v sealed=%v", sig.Certification, seal.Seal, seal.CoversWholeFile, rep.Sealed)
	}
	if !strings.Contains(out.Message["en"], "SHA-256 "+rep.SHA256) {
		t.Errorf("the job's answer carries the sealed file's hash: %s", out.Message["en"])
	}
	if sig.Name != burak.Name {
		t.Errorf("the certificate names %q, expected %q", sig.Name, burak.Name)
	}
	if len(f.LiveKeys()) != 0 {
		t.Errorf("a signing key outlived the job: %v", f.LiveKeys())
	}
	if _, ok, _ := f.StateGet("in:0", envelope.SignedKey); !ok {
		t.Error("the signed badge was not set")
	}
}

// ── asking others: the wizard ──────────────────────────────────────────

// walkRequest runs the wizard to its end and returns the queued job.
func walkRequest(t *testing.T, a *App, identities string, boxes []map[string]any, extra map[string]any) *wire.Surface {
	t.Helper()
	return walkRequestTimed(t, a, identities, boxes, extra, nil)
}

// walkRequestTimed is walkRequest with the wizard's timing step open to
// the caller — a deadline, chiefly, which is the one option a request can
// run out of on its own.
func walkRequestTimed(t *testing.T, a *App, identities string, boxes []map[string]any, extra, timing map[string]any) *wire.Surface {
	t.Helper()
	return walkRequestFull(t, a, identities, boxes, extra, timing, nil)
}

// walkRequestFull is walkRequestTimed with the options step's answers too.
func walkRequestFull(t *testing.T, a *App, identities string, boxes []map[string]any, extra, timing, options map[string]any) *wire.Surface {
	t.Helper()
	s, err := a.viewRequest(viewInput(ViewRequest, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	answers := map[int]map[string]any{
		views.StepSigners: {views.IDSigners: []map[string]any{{"user_id": 7, "email": gokce.Email, "name": gokce.Name}},
			"identities": identities},
		views.StepOrder:   {"order": envelope.OrderParallel},
		views.StepBoxes:   {views.IDFields: boxes},
		views.StepPlace:   {views.IDFields: boxes},
		views.StepTiming:  merge(map[string]any{"expiry": 7}, timing),
		views.StepOptions: merge(map[string]any{"allow_decline": true, "lock": false}, options),
		views.StepResult: merge(map[string]any{"output_mode": envelope.OutputVersion,
			"deliver": envelope.DeliverShare, "delivery_pin": "auto", "audit": true}, extra),
		views.StepReview: {},
	}
	for i := 0; i < 10; i++ {
		step, _ := s.State["step"].(int)
		s, err = a.viewRequest(viewInput(ViewRequest, "submit", "", s.State, answers[step]))
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Errors) > 0 {
			t.Fatalf("step %d refused: %+v", step, s.Errors)
		}
		if s.Job != nil {
			return s
		}
	}
	t.Fatalf("the wizard never queued the request, stopped at %v", s.State["step"])
	return nil
}

func TestRequest_WizardThenSharesAndInvitations(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com\nElden Veren", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .4), sigBox("sig-3", "s3", .7),
		textBox("text-1", "s1", .1),
	}, nil)

	out, err := a.actionRequest(jobFrom(t, s, burak, docName))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("the request failed: %v", out.Message)
	}
	env := loadEnv(t, a)
	if len(env.Signers) != 3 {
		t.Fatalf("three signers expected, got %d", len(env.Signers))
	}
	if env.Schema != envelope.SchemaVersion || !env.Form {
		t.Errorf("a new request is schema %d with real form fields, got %d/%v", envelope.SchemaVersion, env.Schema, env.Form)
	}

	// Gökçe is here: a notification, no mail, no link.
	if env.Signers[0].PageToken != "" {
		t.Error("somebody with an account needs no link")
	}
	if len(f.Notices) == 0 {
		t.Fatal("nobody was notified")
	}
	// Ali has an address: a share and a mail.
	if env.Signers[1].PageURL == "" {
		t.Error("an outside signer must get a link")
	}
	if len(f.Mails) != 1 || f.Mails[0].To != "ali@ornek.com" {
		t.Fatalf("exactly one mail, to the one identity with an address: %+v", f.Mails)
	}
	for _, m := range f.Mails {
		if strings.Contains(m.Body, f.Shares[env.Signers[1].PageToken].PIN) {
			t.Error("a PIN travelled in a mail")
		}
	}
	// The hand-over signer has a link and a PIN, and the REQUESTER was
	// told both, because filex has no way to reach them.
	third := env.Signers[2]
	if !third.HandOver() || third.PageURL == "" {
		t.Fatalf("a name-only identity still needs a link: %+v", third)
	}
	body := out.Message["tr"]
	if !strings.Contains(body, third.PageURL) {
		t.Errorf("the requester was not shown the hand-over link:\n%s", body)
	}
	if pin := f.Shares[third.PageToken].PIN; pin == "" || !strings.Contains(body, pin) {
		t.Errorf("the requester was not shown the hand-over PIN:\n%s", body)
	}
	if v, ok, _ := f.StateGet("in:0", envelope.PendingKey); !ok || v == "" {
		t.Error("the pending marker was not set")
	}
}

// ── a signer's three steps, and the receipt ────────────────────────────

// signAs walks the signer's screens and runs the job the last one queues.
func signAs(t *testing.T, a *App, f *host.Fake, actor wire.Actor, fieldID string, values map[string]any) *wire.ActionRunOutput {
	t.Helper()
	s, err := a.viewFill(viewInputAs(actor, ViewFill, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		vals := map[string]any{}
		if s.State["step"] == views.FillStepFill {
			vals = merge(padValue(t, fieldID), values)
		}
		s, err = a.viewFill(viewInputAs(actor, ViewFill, "submit", "", s.State, vals))
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Errors) > 0 {
			t.Fatalf("the signer's screen refused: %+v", s.Errors)
		}
		if s.Job != nil {
			out, err := a.actionApply(jobFrom(t, s, actor, docName))
			if err != nil {
				t.Fatal(err)
			}
			if !out.OK {
				t.Fatalf("the signature failed: %v", out.Message)
			}
			commit(t, f, out)
			return out
		}
	}
	t.Fatal("the signer's screens never queued a signature")
	return nil
}

func TestSigner_ThreeStepsAndAReceipt(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "", []map[string]any{sigBox("sig-1", "s1", .1), textBox("text-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	notices := len(f.Notices)

	// The screens: what is asked → fill → see and approve.
	scr, err := a.viewFill(viewInputAs(gokce, ViewFill, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if scr.State["step"] != views.FillStepIntro {
		t.Fatalf("a signer starts by being told what is asked, got %v", scr.State["step"])
	}
	out := signAs(t, a, f, gokce, "sig-1", map[string]any{"text-1": "Müdür"})
	if !strings.Contains(out.Message["tr"], "sertifika") {
		t.Errorf("the certificate fingerprint should be in the result: %q", out.Message["tr"])
	}

	env := loadEnv(t, a)
	if env.Status != envelope.StatusCompleted {
		t.Fatalf("one signer, one signature: %s", env.Status)
	}
	sg := env.Signers[0]
	if sg.CertFP == "" || sg.CertSerial == "" {
		t.Error("the signer's certificate facts were not recorded")
	}
	if _, ok, _ := f.StateGet("in:0", envelope.CertPrefix+sg.ID); !ok {
		t.Error("the certificate was thrown away — the receipt can never be given again")
	}
	// Somebody with an account is handed the receipt where they are.
	got := ""
	for _, n := range f.Notices[notices:] {
		if n.ToUserID == gokce.ID && strings.Contains(n.Body["tr"], sg.CertFP) {
			got = n.Body["tr"]
			if n.Target == nil || n.Target.View != ViewVerify {
				t.Errorf("the receipt notice should open Verify, opens %+v", n.Target)
			}
		}
	}
	if got == "" {
		t.Fatalf("the signer was not handed a receipt: %+v", f.Notices[notices:])
	}
	if !strings.Contains(got, "imza yeteneği değildir") {
		t.Errorf("the receipt does not say what it is not:\n%s", got)
	}
	// …and re-opening the signing screen shows it again.
	after, err := a.viewFill(viewInputAs(gokce, ViewFill, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if after.State["view"] != "receipt" {
		t.Errorf("a signer who is done should see their receipt, got %v", after.State["view"])
	}
}

// ⚠⚠ "X opened the document" used to reach the requester for OUTSIDE
// signers only. The public page applied the event; the in-app filling
// screen — the very screen the invitation's notification opens — did not,
// so a colleague sat at `notified` right up to their signature and the
// requester could not tell "has not looked" from "looked and is thinking
// about it". Both paths now raise the SAME sentence, and each raises it
// once: a second open is not news.
// The in-app signing screen is a VIEW, and filex refuses a view's state
// write. The screen used to try anyway, the write was refused on every open,
// and every open logged "recording the open: permission_denied" (2026-09-21).
// It must not try: nothing written, nothing refused, nothing logged — and,
// honestly, nothing recorded (the page and the `viewed` op below are the two
// paths that can).
func TestViewed_TheInAppScreenAttemptsNoWrite(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	f.ReadOnlyState() // what filex answers a view
	logs := len(f.Logs)
	if _, err := a.viewFill(viewInputAs(gokce, ViewFill, "open", "", nil, nil)); err != nil {
		t.Fatal(err)
	}
	if len(f.Refused) != 0 {
		t.Errorf("the screen tried a write filex refuses: %v", f.Refused)
	}
	for _, l := range f.Logs[logs:] {
		if strings.Contains(l, "recording the open") || strings.Contains(l, "permission_denied") {
			t.Errorf("an expected refusal was logged: %q", l)
		}
	}
}

func TestViewed_TheRequesterIsToldOncePerSigner(t *testing.T) {
	for _, tc := range []struct {
		name   string
		signer int
		open   func(t *testing.T, a *App, f *host.Fake, env *envelope.Envelope)
	}{
		{name: "the link an outside signer opens", signer: 1,
			open: func(t *testing.T, a *App, f *host.Fake, env *envelope.Envelope) {
				t.Helper()
				f.BindShare(env.Signers[1].PageToken)
				if _, err := a.pageSigner(pageEvent("open", "", nil, nil)); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "the job the in-app screen's host can queue", signer: 0,
			open: func(t *testing.T, a *App, _ *host.Fake, env *envelope.Envelope) {
				t.Helper()
				out, err := a.actionApply(&wire.ActionRunInput{ActionID: ActionApply, Locale: "tr", Actor: gokce,
					Params: map[string]any{"op": "viewed", "signer_id": env.Signers[0].ID},
					Inputs: []wire.FileRef{{Ref: "in:0", Name: docName, Size: 1000}}})
				if err != nil {
					t.Fatal(err)
				}
				if !out.OK {
					t.Fatalf("the viewed op failed: %v", out.Message)
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, f := newApp(t)
			s := walkRequest(t, a, "ali@ornek.com", []map[string]any{
				sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)}, nil)
			if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
				t.Fatal(err)
			}
			env := loadEnv(t, a)
			who := env.Signers[tc.signer].Person.Identity()
			before := len(f.Notices)

			tc.open(t, a, f, env)

			env = loadEnv(t, a)
			sg := env.Signers[tc.signer]
			if sg.ViewedAt == "" {
				t.Fatalf("opening the document was not recorded: %+v", sg)
			}
			if sg.Status != envelope.SignerViewed {
				t.Errorf("a signer who opened it is %q, not %q", sg.Status, envelope.SignerViewed)
			}
			told := noticesTitled(f.Notices[before:], who+" belgeyi açtı")
			if len(told) != 1 {
				t.Fatalf("the requester should hear it exactly once, heard it %d times: %+v", len(told), f.Notices[before:])
			}
			if told[0].ToUserID != burak.ID {
				t.Errorf("the notice went to %d, not to the requester (%d)", told[0].ToUserID, burak.ID)
			}
			// ⚠ The sentence is pinned, not compared to whatever the code
			// happens to produce: "the same on both paths" is only worth
			// something if the test knows what the words are.
			wantTR := fmt.Sprintf("“%s”: %s imzalamak için açtı.", env.Document, who)
			wantEN := fmt.Sprintf("“%s”: %s opened it to sign.", env.Document, who)
			if told[0].Body["tr"] != wantTR || told[0].Body["en"] != wantEN {
				t.Errorf("the requester's words differ between the paths:\n got tr %q\nwant tr %q\n got en %q\nwant en %q",
					told[0].Body["tr"], wantTR, told[0].Body["en"], wantEN)
			}

			// Opening it a second time is not an event.
			again := len(f.Notices)
			tc.open(t, a, f, env)
			if extra := noticesTitled(f.Notices[again:], who+" belgeyi açtı"); len(extra) != 0 {
				t.Errorf("a second open told the requester all over again: %+v", extra)
			}
			if got := loadEnv(t, a).Signers[tc.signer].ViewedAt; got != sg.ViewedAt {
				t.Errorf("the first open is the one that counts: %q became %q", sg.ViewedAt, got)
			}
		})
	}
}

// ⚠⚠ What filex actually allows, pinned so nobody reads the test above
// as a promise the host does not keep: a plain `view_event` MAY NOT WRITE
// STATE ("this call may not write state"), so the in-app screen's own
// attempt is refused. The rule that matters then is the one a refused
// write must not break — say NOTHING rather than say it on every open —
// and the `apply` job's `viewed` op is the path that always works.
func TestViewed_AScreenThatMayNotWriteSaysNothingRatherThanRepeatItself(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "", []map[string]any{sigBox("sig-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	who := loadEnv(t, a).Signers[0].Person.Identity()
	before := len(f.Notices)
	f.ReadOnlyState() // the screen is a view: filex refuses the write

	for i := 0; i < 3; i++ {
		if _, err := a.viewFill(viewInputAs(gokce, ViewFill, "open", "", nil, nil)); err != nil {
			t.Fatal(err)
		}
	}
	if told := noticesTitled(f.Notices[before:], who+" belgeyi açtı"); len(told) != 0 {
		t.Fatalf("a notice went out that nothing could record, so it would go out again on every open: %+v", told)
	}
	if env := loadEnv(t, a); env.Signers[0].ViewedAt != "" {
		t.Fatal("a refused write must not look like a record")
	}

	// The job may write, and then it is said exactly once.
	f.StateErr = nil
	for i := 0; i < 2; i++ {
		out, err := a.actionApply(&wire.ActionRunInput{ActionID: ActionApply, Locale: "tr", Actor: gokce,
			Params: map[string]any{"op": "viewed"},
			Inputs: []wire.FileRef{{Ref: "in:0", Name: docName, Size: 1000}}})
		if err != nil {
			t.Fatal(err)
		}
		if !out.OK {
			t.Fatalf("the viewed op failed: %v", out.Message)
		}
	}
	if told := noticesTitled(f.Notices[before:], who+" belgeyi açtı"); len(told) != 1 {
		t.Fatalf("the job should tell the requester exactly once, told them %d times", len(told))
	}
}

func TestReceipt_OutsideSignerGetsAShareOfItsOwn(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	env := loadEnv(t, a)
	ali := env.Signers[1]
	f.BindShare(ali.PageToken)

	// Ali signs through their own link.
	page, err := a.pageSigner(pageEvent("open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4 && page.Job == nil; i++ {
		vals := map[string]any{}
		if page.State["step"] == views.FillStepFill {
			vals = padValue(t, "sig-2")
		}
		page, err = a.pageSigner(pageEvent("submit", "", page.State, vals))
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Errors) > 0 {
			t.Fatalf("the public screen refused: %+v", page.Errors)
		}
	}
	if page.Job == nil {
		t.Fatal("the public screens never queued a signature")
	}
	mails := len(f.Mails)
	out, err := a.actionApply(jobFrom(t, fromShare(t, page, ali.PageToken), burak, docName))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("the signature failed: %v", out.Message)
	}
	env = loadEnv(t, a)
	ali = env.Signers[1]
	if ali.ReceiptURL == "" || ali.ReceiptToken == "" {
		t.Fatalf("the outside signer got no receipt share: %+v", ali)
	}
	// ⚠ It is NOT the signing link: that one is revoked the moment the
	// last signature lands, which would take the receipt with it.
	if ali.ReceiptToken == ali.PageToken {
		t.Error("the receipt rides on the signing link, which is revoked")
	}
	share := f.Shares[ali.ReceiptToken]
	if share == nil || len(share.Req.Files) != 3 {
		t.Fatalf("the receipt share should carry the three artefacts: %+v", share)
	}
	for _, want := range []string{".p7b", ".pem", ".txt"} {
		found := false
		for _, file := range share.Req.Files {
			if strings.HasSuffix(file.Name, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no %s in the receipt", want)
		}
	}
	// The mail carries the link and the fingerprint, never the PIN.
	var receiptMail *host.FakeMail
	for i := range f.Mails[mails:] {
		m := f.Mails[mails+i]
		if strings.Contains(m.Body, ali.ReceiptURL) {
			receiptMail = &m
		}
	}
	if receiptMail == nil {
		t.Fatalf("the receipt link was not mailed: %+v", f.Mails[mails:])
	}
	if share.PIN != "" && strings.Contains(receiptMail.Body, share.PIN) {
		t.Error("the receipt PIN travelled in a mail")
	}
	if !strings.Contains(receiptMail.Body, ali.CertFP) {
		t.Error("the receipt mail does not carry the certificate fingerprint")
	}
	// The receipt page itself.
	f.BindShare(ali.ReceiptToken)
	rec, err := a.pageReceipt(pageEvent("open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if rec.State["view"] != "receipt" {
		t.Errorf("the receipt share draws the receipt, got %v", rec.State["view"])
	}
}

// ⚠ The criterion this round was measured by: sign twice, then verify —
// the FIRST signature must not read as "the document changed after it
// was signed".
func TestTwoSignatures_TheFirstOneStaysClean(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5),
		textBox("text-1", "s1", .1), textBox("text-2", "s2", .5),
	}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	signAs(t, a, f, gokce, "sig-1", map[string]any{"text-1": "Müdür"})

	env := loadEnv(t, a)
	ali := env.Signers[1]
	f.BindShare(ali.PageToken)
	page, err := a.pageSigner(pageEvent("open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4 && page.Job == nil; i++ {
		vals := map[string]any{}
		if page.State["step"] == views.FillStepFill {
			vals = merge(padValue(t, "sig-2"), map[string]any{"text-2": "Tedarikçi"})
		}
		page, err = a.pageSigner(pageEvent("submit", "", page.State, vals))
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Errors) > 0 {
			t.Fatalf("refused: %+v", page.Errors)
		}
	}
	out, err := a.actionApply(jobFrom(t, fromShare(t, page, ali.PageToken), burak, docName))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("the second signature failed: %v", out.Message)
	}
	final := commit(t, f, out)

	rep, err := verify.Inspect(final, bundle(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Signatures) != 3 {
		t.Fatalf("two signatures and the seal expected, got %d (%+v)", len(rep.Signatures), rep.Err)
	}
	first, second, seal := rep.Signatures[0], rep.Signatures[1], rep.Signatures[2]
	if first.LaterChanges != verify.ChangedFields {
		t.Errorf("the first signature reads as %q — filling a form must not look like a redrawn page",
			first.LaterChanges)
	}
	// The first CERTIFIED the document, and what the second signer did —
	// fill a field, sign a field — is what the certification permits.
	if first.Certification != CertifyPermission || !first.After.Permitted {
		t.Errorf("the certification must hold through the second signature: %+v", first.After)
	}
	if first.CoversWholeFile || second.CoversWholeFile {
		t.Error("the signers' signatures cannot cover bytes that were appended after them")
	}
	if !seal.Seal || !seal.CoversWholeFile {
		t.Error("filex's seal is last and covers the whole file")
	}
	for _, sig := range rep.Signatures {
		if !sig.Valid {
			t.Errorf("signature %d does not check out: %+v", sig.Index, sig.Errors)
		}
		if !sig.Trusted {
			t.Errorf("signature %d is not trusted against this instance's own authority", sig.Index)
		}
		if !sig.WithinValidity {
			t.Errorf("signature %d falls outside its certificate's window — is certDays long enough?", sig.Index)
		}
	}
	// Both values are on the paper, as real form fields.
	env = loadEnv(t, a)
	if env.Status != envelope.StatusCompleted {
		t.Fatalf("both signed, status is %s", env.Status)
	}
	if got := env.Field("text-1").Value; got != "Müdür" {
		t.Errorf("the first signer's value was lost: %q", got)
	}
	if got := env.Field("text-2").Value; got != "Tedarikçi" {
		t.Errorf("the second signer's value was lost: %q", got)
	}
}

func TestDelivery_TheSignedCopyGoesOutAsAShare(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "", []map[string]any{sigBox("sig-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	mails, notices := len(f.Mails), len(f.Notices)
	out := signAs(t, a, f, gokce, "sig-1", nil)

	env := loadEnv(t, a)
	if env.DeliveryURL == "" {
		t.Fatal("the finished document was not shared")
	}
	share := f.Shares[env.DeliveryToken]
	if share == nil || len(share.Req.Files) != 1 {
		t.Fatalf("the delivery share should carry the signed document: %+v", share)
	}
	if share.PIN == "" {
		t.Error("the requester asked for a PIN and got none")
	}
	// Named for what it is in the creator's My shares (a page-less link has
	// no manifest page to say it): Gökçe's own job finished the request, so
	// it is hers and opens "I have signed these".
	if p := share.Req.Purpose; p == nil || p.Label["tr"] != "İmzalı kopya" || p.Revoke["en"] == "" || p.Section != views.SectionSigned {
		t.Errorf("the delivery link says nothing about what it is: %+v", share.Req.Purpose)
	}
	if !strings.Contains(out.Message["tr"], share.PIN) {
		t.Error("the delivery PIN was not shown to the requester")
	}
	for _, m := range f.Mails[mails:] {
		if strings.Contains(m.Body, share.PIN) {
			t.Error("the delivery PIN travelled in a mail")
		}
	}
	// ⚠ Gökçe has an account AND an address — the directory lookup put one
	// there. She is told IN filex, and NOT mailed: the same rule the
	// invitation, the reminder and the receipt keep. (This assertion used
	// to read the other way round, and that is exactly the hole: a plain
	// "has an address?" mails every colleague in the request.)
	for _, m := range f.Mails[mails:] {
		if m.To == gokce.Email {
			t.Errorf("a signer with an account was mailed the signed copy: %+v", m)
		}
	}
	told := false
	for _, n := range f.Notices[notices:] {
		if n.ToUserID == gokce.ID && strings.Contains(n.Body["tr"], env.DeliveryURL) {
			told = true
		}
	}
	if !told {
		t.Errorf("the signer with an account was not given the signed copy: %+v", f.Notices[notices:])
	}
}

// ⚠⚠ The finished document goes to every signer, each the way this app
// reaches them: an outside signer by mail, a signer with an account IN
// filex. The delivery step used to loop over "has an address?" alone —
// and a colleague carries one, because the directory lookup put it there
// — so the one rule the invitation, the reminder and the receipt all keep
// was broken at the last step of the flow. Same link either way: nobody
// may lose the finished document because of how they are reached.
func TestDelivery_ReachesEachSignerTheWayThisAppReachesThem(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	env := loadEnv(t, a)
	ali := env.Signers[1]
	f.BindShare(ali.PageToken)

	// Ali signs on their link, Gökçe in the app: the last signature is
	// what opens the delivery.
	page, err := a.pageSigner(pageEvent("open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4 && page.Job == nil; i++ {
		vals := map[string]any{}
		if page.State["step"] == views.FillStepFill {
			vals = padValue(t, "sig-2")
		}
		if page, err = a.pageSigner(pageEvent("submit", "", page.State, vals)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := a.actionApply(jobFrom(t, fromShare(t, page, ali.PageToken), burak, docName))
	if err != nil {
		t.Fatal(err)
	}
	commit(t, f, out)
	mails, notices := len(f.Mails), len(f.Notices)
	signAs(t, a, f, gokce, "sig-1", nil)

	env = loadEnv(t, a)
	if env.Status != envelope.StatusCompleted || env.DeliveryURL == "" {
		t.Fatalf("the finished document was not shared: %s / %q", env.Status, env.DeliveryURL)
	}
	link := env.DeliveryURL

	for _, tc := range []struct {
		name string
		// what the signer gets, and what they must NOT get
		check func(t *testing.T)
	}{
		{name: "an outside signer is mailed the link", check: func(t *testing.T) {
			got := mailsTo(f.Mails[mails:], "ali@ornek.com")
			if len(got) != 1 {
				t.Fatalf("exactly one delivery mail expected: %+v", f.Mails[mails:])
			}
			if !strings.Contains(got[0].Body, link) {
				t.Errorf("the delivery mail carries no link:\n%s", got[0].Body)
			}
			if pin := f.Shares[env.DeliveryToken].PIN; pin != "" && strings.Contains(got[0].Body, pin) {
				t.Error("the delivery PIN travelled in a mail")
			}
		}},
		{name: "a signer with an account is told in filex, with the SAME link", check: func(t *testing.T) {
			told := noticesTitled(f.Notices[notices:], "İmzalı belge hazır")
			if len(told) != 1 {
				t.Fatalf("exactly one delivery notice expected: %+v", f.Notices[notices:])
			}
			if told[0].ToUserID != gokce.ID {
				t.Errorf("the notice went to %d, not to the signer (%d)", told[0].ToUserID, gokce.ID)
			}
			for _, lang := range []string{"tr", "en"} {
				if !strings.Contains(told[0].Body[lang], link) {
					t.Errorf("the %s notice carries no link:\n%s", lang, told[0].Body[lang])
				}
			}
		}},
		{name: "and that signer is NOT mailed, address or no address", check: func(t *testing.T) {
			if got := mailsTo(f.Mails[mails:], gokce.Email); len(got) != 0 {
				t.Errorf("a signer with an account was mailed the signed copy: %+v", got)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.check(t) })
	}
}

func TestDelivery_CanBeSwitchedOff(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "", []map[string]any{sigBox("sig-1", "s1", .1)},
		map[string]any{"deliver": envelope.DeliverNone})
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	signAs(t, a, f, gokce, "sig-1", nil)
	if env := loadEnv(t, a); env.DeliveryURL != "" {
		t.Error("nothing was promised to the signers, so nothing should have been sent")
	}
}

// ── verification ───────────────────────────────────────────────────────

func TestVerify_SaysSoWhenThereIsNothingToVerify(t *testing.T) {
	a, _ := newApp(t)
	s, err := a.viewVerify(viewInput(ViewVerify, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	body := surfaceWords(s)
	if !strings.Contains(body, "elektronik imza yok") {
		t.Errorf("an unsigned document must say so:\n%s", body)
	}
	out, err := a.actionVerify(&wire.ActionRunInput{ActionID: ActionVerify,
		Inputs: []wire.FileRef{{Ref: "in:0", Name: docName}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Error("a run on an unsigned document should answer that there is nothing there")
	}
}

func TestVerify_NamesTheAuthorityAndWhatTheCertificateAttests(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "", []map[string]any{sigBox("sig-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	signAs(t, a, f, gokce, "sig-1", nil)

	rep, err := a.report("in:0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Signatures) != 2 || !rep.Signatures[1].Seal {
		t.Fatalf("the signature and filex's seal expected, got %d", len(rep.Signatures))
	}
	sig := rep.Signatures[0]
	if sig.Name != gokce.Name {
		t.Errorf("the identity comes from the certificate: %q", sig.Name)
	}
	if !sig.HasIssuer || sig.Issuer.FP == "" {
		t.Errorf("the report must name the issuing authority: %+v", sig.Issuer)
	}
	if !sig.IssuerOurs {
		t.Error("the issuer is this instance's own authority and should be recognised as such")
	}
	if !contains(sig.Cert.EKU, "document signing") {
		t.Errorf("the certificate should be marked for document signing: %v", sig.Cert.EKU)
	}
	if sig.TimeProven {
		t.Error("without a time-stamping authority nothing is proven about the time")
	}
	if len(rep.Authorities) == 0 || rep.Authorities[0].FP == "" {
		t.Error("the report should list the authorities it trusts")
	}
	// An authority that was rotated away still verifies its old work.
	f.RetiredCAs = append(f.RetiredCAs, f.CA.Cert)
	rep2, err := a.report("in:0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep2.Authorities) != 2 {
		t.Errorf("the retired authority should still be in the pool: %d", len(rep2.Authorities))
	}
}

// ── refusing, cancelling, expiring, freezing ───────────────────────────

func TestDecline_ClosesTheRequestAndReleasesTheFile(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)},
		map[string]any{"lock": true})
	job := jobFrom(t, s, burak, docName)
	job.Params["lock"] = true
	if _, err := a.actionRequest(job); err != nil {
		t.Fatal(err)
	}
	if len(f.LockedRefs()) != 1 {
		t.Fatalf("the file should be frozen: %v", f.LockedRefs())
	}
	scr, err := a.viewFill(viewInputAs(gokce, ViewFill, "action", "decline", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	scr, err = a.viewFill(viewInputAs(gokce, ViewFill, "action", "decline_confirm", scr.State,
		map[string]any{"decline_reason": "Tutar yanlış."}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := a.actionApply(jobFrom(t, scr, gokce, docName))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("the refusal failed: %v", out.Message)
	}
	env := loadEnv(t, a)
	if env.Status != envelope.StatusDeclined {
		t.Fatalf("the request should be closed: %s", env.Status)
	}
	if len(f.LockedRefs()) != 0 {
		t.Errorf("a closed request must release the file: %v", f.LockedRefs())
	}
	if !f.Shares[env.Signers[1].PageToken].Revoked {
		t.Error("the other signer's link must stop working")
	}
	if _, ok, _ := f.StateGet("in:0", envelope.PendingKey); ok {
		t.Error("the pending marker outlived the request")
	}
}

func TestCancel_RevokesOpenSharesAndUnfreezes(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)}, nil)
	job := jobFrom(t, s, burak, docName)
	job.Params["lock"] = true
	if _, err := a.actionRequest(job); err != nil {
		t.Fatal(err)
	}
	scr, err := a.viewStatus(viewInput(ViewStatus, "action", "cancel", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.actionApply(jobFrom(t, scr, burak, docName)); err != nil {
		t.Fatal(err)
	}
	env := loadEnv(t, a)
	if env.Status != envelope.StatusCancelled || env.Locked {
		t.Fatalf("cancelling must close and release: %s / %v", env.Status, env.Locked)
	}
	if len(f.Unlocks) == 0 {
		t.Error("the file was never released")
	}
}

// ⚠ The BELT. A lapsed request is closed on its own minute by the hourly
// wake-up now (tick.go, and TestTick_TheScheduledJobClosesItAndTellsBothSidesOnce
// is that half), but the wake-up only sees what it is told about: a
// request past the listing limit, one that lapsed while the app was
// disabled, one on an instance whose administrator never granted
// `schedule`. So every writable call this app gets still sweeps first:
// every job, and the signer's own page. Whoever touches it first closes
// it, and BOTH sides hear about it.
func TestExpiry_TheFirstTouchAfterTheDeadlineClosesIt(t *testing.T) {
	// The deadline day itself still counts, so a request due on the 21st
	// is live on the 21st and over on the 23rd.
	const deadline = "2026-09-21"

	openLapsed := func(t *testing.T) (*App, *host.Fake, *envelope.Envelope) {
		t.Helper()
		a, f := newApp(t)
		s := walkRequestTimed(t, a, "ali@ornek.com", []map[string]any{
			sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)},
			map[string]any{"lock": true}, map[string]any{"deadline": deadline})
		job := jobFrom(t, s, burak, docName)
		job.Params["lock"] = true
		if _, err := a.actionRequest(job); err != nil {
			t.Fatal(err)
		}
		if len(f.LockedRefs()) != 1 {
			t.Fatalf("the file should be frozen while signatures are collected: %v", f.LockedRefs())
		}
		env := loadEnv(t, a)
		f.Clock = f.Clock.AddDate(0, 0, 3) // past the deadline, nobody signed
		return a, f, env
	}

	for _, tc := range []struct {
		name  string
		touch func(t *testing.T, a *App, f *host.Fake, env *envelope.Envelope)
	}{
		{name: "a job the requester runs", touch: func(t *testing.T, a *App, _ *host.Fake, env *envelope.Envelope) {
			t.Helper()
			if _, err := a.actionApply(&wire.ActionRunInput{ActionID: ActionApply, Locale: "tr", Actor: burak,
				Params: map[string]any{"op": "remind", "signer_id": env.Signers[1].ID},
				Inputs: []wire.FileRef{{Ref: "in:0", Name: docName, Size: 1000}}}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "the link an outside signer opens", touch: func(t *testing.T, a *App, f *host.Fake, env *envelope.Envelope) {
			t.Helper()
			f.BindShare(env.Signers[1].PageToken)
			s, err := a.pageSigner(pageEvent("open", "", nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			// ⚠ And they are TOLD, not handed a form whose job would
			// refuse them.
			if body := surfaceWords(s); !strings.Contains(body, "süresi doldu") {
				t.Errorf("the visitor was not told the request had run out:\n%s", body)
			}
		}},
		{name: "a signer's own submission", touch: func(t *testing.T, a *App, _ *host.Fake, env *envelope.Envelope) {
			t.Helper()
			out, err := a.actionApply(&wire.ActionRunInput{ActionID: ActionApply, Locale: "tr", Actor: gokce,
				Params: map[string]any{"op": "sign", "envelope_id": env.ID, "signer_id": env.Signers[0].ID,
					"png_b64": padPNG(t), "values": map[string]any{}},
				Inputs: []wire.FileRef{{Ref: "in:0", Name: docName, Size: 1000}}})
			if err != nil {
				t.Fatal(err)
			}
			if out.OK {
				t.Error("a signature after the deadline must not be applied")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, f, env := openLapsed(t)
			notices, mails := len(f.Notices), len(f.Mails)

			tc.touch(t, a, f, env)

			after := loadEnv(t, a)
			if after.Status != envelope.StatusExpired {
				t.Fatalf("the lapsed request should have closed itself, is %q", after.Status)
			}
			if after.ClosedAt == "" {
				t.Error("a closed request records when")
			}
			if len(f.LockedRefs()) != 0 {
				t.Errorf("a closed request must release the file: %v", f.LockedRefs())
			}
			if !f.Shares[env.Signers[1].PageToken].Revoked {
				t.Error("the signing link must stop working")
			}
			if _, ok, _ := f.StateGet("in:0", envelope.PendingKey); ok {
				t.Error("the pending marker outlived the request")
			}
			// The requester.
			if told := noticesTitled(f.Notices[notices:], "İmza isteğinin süresi doldu"); len(told) != 1 {
				t.Fatalf("the requester was not told exactly once: %+v", f.Notices[notices:])
			}
			// The signer with an account: in filex, and NOT by mail.
			told := noticesTitled(f.Notices[notices:], "Bir imza isteğinin süresi doldu")
			if len(told) != 1 || told[0].ToUserID != gokce.ID {
				t.Fatalf("the signer with an account was not told: %+v", f.Notices[notices:])
			}
			if got := mailsTo(f.Mails[mails:], gokce.Email); len(got) != 0 {
				t.Errorf("a signer with an account was mailed: %+v", got)
			}
			// The outside signer: by mail, because that is all there is.
			got := mailsTo(f.Mails[mails:], "ali@ornek.com")
			if len(got) != 1 {
				t.Fatalf("the outside signer was not told exactly once: %+v", f.Mails[mails:])
			}
			// …as a date in the mail's own words ("21 Eyl 2026"), not the
			// ISO day the record keeps.
			said := humandate.Stamp("tr", deadline)
			if !strings.Contains(got[0].Body, said) && !strings.Contains(got[0].Body, humandate.Stamp("en", deadline)) {
				t.Errorf("the mail does not say what the deadline was (%s):\n%s", said, got[0].Body)
			}
			if strings.Contains(got[0].Body, deadline) {
				t.Errorf("the mail prints the ISO day %s:\n%s", deadline, got[0].Body)
			}

			// ⚠ And it happens ONCE. The next call finds a closed request
			// and says nothing to anybody.
			again, mailsAgain := len(f.Notices), len(f.Mails)
			tc.touch(t, a, f, env)
			if len(f.Notices) != again || len(f.Mails) != mailsAgain {
				t.Errorf("the second touch told everybody again: %+v / %+v", f.Notices[again:], f.Mails[mailsAgain:])
			}
		})
	}
}

// ⚠⚠ Measured on the running demo: when the signer's PAGE is the call
// that sweeps a lapsed request, filex refuses the unfreeze — "locks are
// lifted from action jobs only". The request must still close and
// everybody must still be told, but the record may NOT then claim the
// file is free: a closed request over a frozen file, with nothing left
// that would ever lift it, is how a document stays read-only for ever.
func TestExpiry_AScreenThatCannotUnfreezeLeavesItForTheNextJob(t *testing.T) {
	a, f := newApp(t)
	s := walkRequestTimed(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)},
		map[string]any{"lock": true}, map[string]any{"deadline": "2026-09-21"})
	job := jobFrom(t, s, burak, docName)
	job.Params["lock"] = true
	if _, err := a.actionRequest(job); err != nil {
		t.Fatal(err)
	}
	env := loadEnv(t, a)
	f.Clock = f.Clock.AddDate(0, 0, 3)
	f.UnlockErr = &pluginkit.HostError{Code: wire.ErrPermissionDenied, Message: "locks are lifted from action jobs only"}

	f.BindShare(env.Signers[1].PageToken)
	if _, err := a.pageSigner(pageEvent("open", "", nil, nil)); err != nil {
		t.Fatal(err)
	}
	after := loadEnv(t, a)
	if after.Status != envelope.StatusExpired {
		t.Fatalf("the page should still close a lapsed request, status %q", after.Status)
	}
	if !after.Locked {
		t.Error("the freeze is still on the file, so the record must say so")
	}
	if len(f.LockedRefs()) != 1 {
		t.Fatalf("the host still holds the lock: %v", f.LockedRefs())
	}

	// The next job finishes what the screen could not.
	f.UnlockErr = nil
	if _, err := a.actionApply(&wire.ActionRunInput{ActionID: ActionApply, Locale: "tr", Actor: burak,
		Params: map[string]any{"op": "audit"},
		Inputs: []wire.FileRef{{Ref: "in:0", Name: docName, Size: 1000}}}); err != nil {
		t.Fatal(err)
	}
	if len(f.LockedRefs()) != 0 {
		t.Errorf("a closed request must not leave the file frozen: %v", f.LockedRefs())
	}
	if loadEnv(t, a).Locked {
		t.Error("the record still says the file is frozen")
	}
}

func TestLockFailureStopsTheRequestBeforeAnybodyIsInvited(t *testing.T) {
	a, f := newApp(t)
	f.LockErr = &pluginkit.HostError{Code: wire.ErrBusy, Message: "another app holds it"}
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)}, nil)
	job := jobFrom(t, s, burak, docName)
	job.Params["lock"] = true
	out, err := a.actionRequest(job)
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Fatal("a request that cannot freeze the file it promised to freeze must stop")
	}
	if len(f.Mails) != 0 || len(f.Shares) != 0 {
		t.Errorf("nothing should have gone out: %d mails, %d shares", len(f.Mails), len(f.Shares))
	}
}

// ── odds and ends ──────────────────────────────────────────────────────

func TestExpandName(t *testing.T) {
	for _, c := range []struct{ pattern, in, want string }{
		{"{stem}-signed{ext}", "a.pdf", "a-signed.pdf"},
		{"", "a.pdf", "a-signed.pdf"},
		{"imzali", "a.pdf", "imzali.pdf"},
		{"{name}", "a.pdf", "a.pdf"},
	} {
		if got := expandName(c.pattern, c.in); got != c.want {
			t.Errorf("%q + %q → %q, want %q", c.pattern, c.in, got, c.want)
		}
	}
}

func TestInitialsOf(t *testing.T) {
	if got := initialsOf("Burak Faruk Şahin"); got != "BFŞ" {
		t.Errorf("initials → %q", got)
	}
	if got := initialsOf(""); got != "-" {
		t.Errorf("no name → %q", got)
	}
}

// ⚠⚠ A box nobody named stays UNNAMED in the record, and every screen
// names it in its own reader's language (views.NameIn). Parsing used to
// write the requester's default into the record, so a Turkish signer of an
// English requester's document was asked for "Signature" and "Date"
// (measured on a signing link, 2026-09-21). A name somebody typed is kept
// exactly as typed, in any script.
func TestParseFields_KeepsNamesAsTypedAndMigratesTheOldDateRule(t *testing.T) {
	fs, err := parseFields([]map[string]any{
		{"id": "text-1", "type": "text", "page": 1, "rule": "date", "format": fields.DateDMY},
		{"id": "sig-1", "type": "signature", "page": 1},
		{"id": "sig-2", "type": "signature", "page": 1},
		{"id": "text-2", "type": "text", "page": 1, "label": "  顧客名 · Müşteri adı  "},
	}, "", views.TR)
	if err != nil {
		t.Fatal(err)
	}
	if fs[0].Type != fields.TypeDate || fs[0].Rule != "" {
		t.Errorf("an old text+date box should become a date box: %+v", fs[0])
	}
	if fs[1].Label != "" || fs[2].Label != "" {
		t.Errorf("an unnamed box must stay unnamed in the record: %q %q", fs[1].Label, fs[2].Label)
	}
	// ⚠⚠ EXACTLY as typed, spaces and all. This list round-trips through
	// the screen on every `change` while the name is being typed, so a trim
	// here hands the browser back the name with the space it just typed
	// removed — and a two-word name cannot be typed at all (the owner,
	// 2026-09-23). The record is trimmed once, when the request is sent.
	if got := fs[3].Label; got != "  顧客名 · Müşteri adı  " {
		t.Errorf("a typed name is kept exactly as typed: %q", got)
	}
	// ...and each screen names the unnamed ones in ITS language, numbered.
	if got := views.NameIn(views.TR, fs, fs[1]); got != "İmza 1" {
		t.Errorf("tr: %q", got)
	}
	if got := views.NameIn(views.EN, fs, fs[2]); got != "Signature 2" {
		t.Errorf("en: %q", got)
	}
	if got := views.NameIn(views.EN, fs, fs[0]); got != "Date" {
		t.Errorf("a single date box is not numbered: %q", got)
	}
}

// ── the rule on the wire is an object; the record keeps a word ─────────
//
// The contract (docs/APP-PLUGINS-API.md → "Faces and rules") sends a text
// box's rule as `{kind, min?, max?}`. The record has held a bare word
// since schema 2, so BOTH have to be readable here: the object because
// that is what a browser posts, the word because that is what a job
// queued by an older screen carries — and because every envelope on disk
// is full of them.

func TestParseFields_ReadsARuleInEitherShape(t *testing.T) {
	for _, c := range []struct {
		name     string
		box      map[string]any
		wantType string
		wantRule string
		wantFmt  string
		wantMin  int
		wantMax  int
	}{
		{name: "the contract's object, with bounds",
			box:      map[string]any{"id": "text-1", "type": "text", "page": 1, "rule": map[string]any{"kind": "email", "min": 5, "max": 60}},
			wantType: fields.TypeText, wantRule: fields.RuleEmail, wantMin: 5, wantMax: 60},
		{name: "the contract's `any` is the record's free text",
			box:      map[string]any{"id": "text-2", "type": "text", "page": 1, "rule": map[string]any{"kind": "any"}},
			wantType: fields.TypeText, wantRule: fields.RuleFree},
		{name: "a kind this build does not know still takes what is typed",
			box:      map[string]any{"id": "text-3", "type": "text", "page": 1, "rule": map[string]any{"kind": "iban"}},
			wantType: fields.TypeText, wantRule: fields.RuleFree},
		{name: "the removed `date` kind is read as free text, not as a second date control",
			box:      map[string]any{"id": "text-4", "type": "text", "page": 1, "rule": map[string]any{"kind": "date"}},
			wantType: fields.TypeText, wantRule: fields.RuleFree},
		{name: "an object clears a bound the person emptied",
			box: map[string]any{"id": "text-5", "type": "text", "page": 1, "min_len": 9, "max_len": 9,
				"rule": map[string]any{"kind": "number", "max": 11}},
			wantType: fields.TypeText, wantRule: fields.RuleNumber, wantMin: 0, wantMax: 11},
		{name: "the old bare word, with the box's own bounds beside it",
			box:      map[string]any{"id": "text-6", "type": "text", "page": 1, "rule": "number", "min_len": 3, "max_len": 8},
			wantType: fields.TypeText, wantRule: fields.RuleNumber, wantMin: 3, wantMax: 8},
		{name: "no rule at all is free text",
			box:      map[string]any{"id": "text-7", "type": "text", "page": 1},
			wantType: fields.TypeText, wantRule: fields.RuleFree},
		{name: "a rule of a shape nobody sends costs that box, never the screen",
			box:      map[string]any{"id": "text-8", "type": "text", "page": 1, "rule": 7},
			wantType: fields.TypeText, wantRule: fields.RuleFree},
		{name: "a date box keeps its layout and carries no rule",
			box:      map[string]any{"id": "date-1", "type": "date", "page": 1, "format": fields.DateMDY},
			wantType: fields.TypeDate, wantRule: "", wantFmt: fields.DateMDY},
		{name: "a date box with no layout takes the day-first default",
			box:      map[string]any{"id": "date-2", "type": "date", "page": 1},
			wantType: fields.TypeDate, wantRule: "", wantFmt: fields.DateDMY},
		{name: "a signature box carries neither",
			box:      map[string]any{"id": "sig-1", "type": "signature", "page": 1, "rule": map[string]any{"kind": "email"}, "format": fields.DateMDY},
			wantType: fields.TypeSignature, wantRule: "", wantFmt: ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			fs, err := parseFields([]map[string]any{c.box}, "", views.TR)
			if err != nil {
				t.Fatal(err)
			}
			if len(fs) != 1 {
				t.Fatalf("one box in, %d out", len(fs))
			}
			got := fs[0]
			if got.Type != c.wantType || got.Rule != c.wantRule || got.Format != c.wantFmt ||
				got.MinLen != c.wantMin || got.MaxLen != c.wantMax {
				t.Errorf("got type=%q rule=%q format=%q min=%d max=%d, want type=%q rule=%q format=%q min=%d max=%d",
					got.Type, got.Rule, got.Format, got.MinLen, got.MaxLen,
					c.wantType, c.wantRule, c.wantFmt, c.wantMin, c.wantMax)
			}
		})
	}
}

func TestFieldProp_SpeaksTheContractsRuleAndFormat(t *testing.T) {
	for _, c := range []struct {
		name       string
		field      envelope.Field
		wantRule   map[string]any
		wantNoRule bool
		wantFormat string
	}{
		{name: "a text box's rule is an object, in the contract's own words",
			field:    envelope.Field{ID: "text-1", Type: fields.TypeText, Page: 1, Rule: fields.RuleEmail, MinLen: 5, MaxLen: 60},
			wantRule: map[string]any{"kind": "email", "min": 5, "max": 60}},
		{name: "the record's `free` is the wire's `any`",
			field:    envelope.Field{ID: "text-2", Type: fields.TypeText, Page: 1, Rule: fields.RuleFree, MaxLen: 40},
			wantRule: map[string]any{"kind": "any", "max": 40}},
		{name: "free text with no bounds asks for nothing, so it says nothing",
			field:      envelope.Field{ID: "text-3", Type: fields.TypeText, Page: 1, Rule: fields.RuleFree},
			wantNoRule: true},
		{name: "a date box carries its layout and no rule",
			field:      envelope.Field{ID: "date-1", Type: fields.TypeDate, Page: 1, Format: fields.DateYMD},
			wantNoRule: true, wantFormat: fields.DateYMD},
		{name: "a signature box carries neither",
			field:      envelope.Field{ID: "sig-1", Type: fields.TypeSignature, Page: 1},
			wantNoRule: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := views.FieldProp(c.field)
			if c.wantNoRule {
				if _, ok := m["rule"]; ok {
					t.Errorf("no rule expected on the wire, got %#v", m["rule"])
				}
			} else if got, ok := m["rule"].(map[string]any); !ok || !reflect.DeepEqual(got, c.wantRule) {
				t.Errorf("rule → %#v, want %#v", m["rule"], c.wantRule)
			}
			if got, _ := m["format"].(string); got != c.wantFormat {
				t.Errorf("format → %q, want %q", got, c.wantFormat)
			}
		})
	}
}

// What FieldProp writes, parseFields has to be able to read: the two are
// the same conversation, and a wizard step is exactly one round of it.
func TestFieldPropAndParseFieldsAgreeOnTheWire(t *testing.T) {
	in := []envelope.Field{
		{ID: "text-1", Type: fields.TypeText, Page: 1, Label: "Vergi no", Rule: fields.RuleNumber, MinLen: 10, MaxLen: 11},
		{ID: "date-1", Type: fields.TypeDate, Page: 1, Label: "Tarih", Format: fields.DateMDY},
		{ID: "sig-1", Type: fields.TypeSignature, Page: 1, Label: "İmza", Assignee: "s1", Required: true},
	}
	var props []map[string]any
	for _, f := range in {
		props = append(props, views.FieldProp(f))
	}
	out, err := parseFields(props, "", views.TR)
	if err != nil {
		t.Fatalf("the plugin could not read back what it sent: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("%d boxes went out, %d came back", len(in), len(out))
	}
	for i, want := range in {
		got := out[i]
		if got.ID != want.ID || got.Type != want.Type || got.Rule != want.Rule ||
			got.Format != want.Format || got.MinLen != want.MinLen || got.MaxLen != want.MaxLen ||
			got.Label != want.Label || got.Assignee != want.Assignee || got.Required != want.Required {
			t.Errorf("box %d came back changed:\n got %+v\nwant %+v", i, got, want)
		}
	}
}

// ⚠⚠ The bug this whole translation exists for: the browser posts the
// contract's object, the plugin could not read it, and `absorb` kept the
// previous (empty) list — so every box the person had defined disappeared
// and the step answered "place a signature box for …", as if they had done
// nothing at all.
func TestRequestWizard_KeepsTheBoxesWhenOneCarriesARule(t *testing.T) {
	a, _ := newApp(t)
	s := walkRequest(t, a, "", []map[string]any{
		sigBox("sig-1", "s1", .1),
		merge(textBox("text-1", "s1", .5), map[string]any{"rule": map[string]any{"kind": "email", "min": 5, "max": 60}}),
		merge(dateBox("date-1", "s1", .1), map[string]any{"format": fields.DateMDY}),
	}, nil)

	flds := jsonRound[[]map[string]any](t, s.Job.Params["fields"])
	if len(flds) != 3 {
		t.Fatalf("three boxes were defined, %d reached the job: %+v", len(flds), flds)
	}
	rule, ok := flds[1]["rule"].(map[string]any)
	if !ok {
		t.Fatalf("the text box lost its rule on the way to the job: %+v", flds[1])
	}
	if rule["kind"] != "email" || rule["min"] != float64(5) || rule["max"] != float64(60) {
		t.Errorf("the rule reached the job changed: %+v", rule)
	}
	if got := flds[2]["format"]; got != fields.DateMDY {
		t.Errorf("the date box's layout reached the job as %v, want %q", got, fields.DateMDY)
	}

	out, err := a.actionRequest(jobFrom(t, s, burak, docName))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("the request failed: %v", out.Message)
	}
	env := loadEnv(t, a)
	if len(env.Fields) != 3 {
		t.Fatalf("three boxes were defined, %d were stored: %+v", len(env.Fields), env.Fields)
	}
	if f := env.Field("text-1"); f == nil || f.Rule != fields.RuleEmail || f.MinLen != 5 || f.MaxLen != 60 {
		t.Errorf("the stored text box lost its rule: %+v", f)
	}
	if f := env.Field("date-1"); f == nil || f.Format != fields.DateMDY {
		t.Errorf("the stored date box lost its layout: %+v", f)
	}
}

func TestParseFields_AnUnreadableListIsAnError(t *testing.T) {
	for _, c := range []struct {
		name string
		v    any
	}{
		{"not a list at all", "sig-1"},
		{"a list of something else", []any{"sig-1", "sig-2"}},
		{"a box whose geometry is not a number", []map[string]any{{"id": "sig-1", "type": "signature", "x": "left"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fs, err := parseFields(c.v, "", views.TR)
			if err == nil {
				t.Fatalf("an unreadable list has to be an error, got %d boxes", len(fs))
			}
			if fs != nil {
				t.Errorf("nothing may be returned beside the error: %+v", fs)
			}
		})
	}
}

// ⚠ A list that cannot be read is told to the person. Swallowing it is
// what turned a parsing bug into lost work: the screen looked like it had
// simply forgotten everything.
func TestRequestWizard_UnreadableBoxesAreAnErrorNotSilence(t *testing.T) {
	a, _ := newApp(t)
	held := []map[string]any{sigBox("sig-1", "s1", .1), textBox("text-1", "s1", .5)}
	state := map[string]any{"step": views.StepBoxes, "identities": "ali@ornek.com", "fields": held}

	s, err := a.viewRequest(viewInput(ViewRequest, "submit", "", state,
		map[string]any{views.IDFields: "not a list"}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Errors[views.IDFields] == nil {
		t.Fatalf("an unreadable box list has to be said out loud: %+v", s.Errors)
	}
	st := jsonRound[views.RequestState](t, s.State)
	if st.Step != views.StepBoxes {
		t.Errorf("the step may not advance past a list nobody could read: %d", st.Step)
	}
	if len(st.Fields) != len(held) {
		t.Errorf("the boxes already defined were dropped: %+v", st.Fields)
	}
}

func TestSigningUnavailableIsRefusedEarly(t *testing.T) {
	a, f := newApp(t)
	f.SignUnavailable = true
	out, err := a.actionSign(&wire.ActionRunInput{ActionID: ActionSign, Actor: burak,
		Inputs: []wire.FileRef{{Ref: "in:0", Name: docName}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Fatal("signing should be refused when the host cannot sign")
	}
	if len(f.Outputs) != 0 {
		t.Error("nothing should have been written")
	}
}

// ── helpers ────────────────────────────────────────────────────────────

func pageEvent(event, actionID string, state, values map[string]any) *wire.ViewEventInput {
	in := &wire.ViewEventInput{ViewID: PageSigner, Event: event, ActionID: actionID, State: state,
		Data: map[string]any{"values": values, "page": map[string]any{"visitor_ip": "203.0.113.9"}},
		Context: wire.CallContext{Locale: "tr",
			Inputs: []wire.FileRef{{Ref: "in:0", Name: docName}, {Ref: "pub:0", Name: docName}}}}
	if values == nil {
		in.Data["values"] = map[string]any{}
	}
	return in
}

// fromShare adapts a public page's queued job the way filex does: it runs
// as the share's CREATOR, with the share's token hash added, which is
// what binds the submission to one signer.
func fromShare(t *testing.T, s *wire.Surface, token string) *wire.Surface {
	t.Helper()
	if s.Job == nil {
		t.Fatal("no job queued")
	}
	s.Job.Params["page_token_hash"] = tokenHash(token)
	return s
}

func bundle(t *testing.T, f *host.Fake) string {
	t.Helper()
	info, err := f.SignInfo()
	if err != nil {
		t.Fatal(err)
	}
	return info.Authorities
}

func hasView(m wire.Manifest, id string) bool {
	for _, v := range m.Views {
		if v.ID == id {
			return true
		}
	}
	return false
}

func hasPage(m wire.Manifest, id string) bool {
	for _, p := range m.PublicPages {
		if p.ID == id {
			return true
		}
	}
	return false
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// surfaceWords is every word a surface shows, for a "does it say this"
// check.
func surfaceWords(s *wire.Surface) string {
	var b strings.Builder
	write := func(v any) {
		switch x := v.(type) {
		case wire.Text:
			b.WriteString(x["en"] + "\n" + x["tr"] + "\n")
		}
	}
	write(s.Title)
	for _, a := range s.Actions {
		write(a.Label)
	}
	var walk func(nodes []wire.Node)
	walk = func(nodes []wire.Node) {
		for _, n := range nodes {
			for _, p := range n.Props {
				write(p)
				if m, ok := p.([]map[string]any); ok {
					for _, row := range m {
						for _, c := range row {
							write(c)
							if cells, ok := c.(map[string]wire.Text); ok {
								for _, t := range cells {
									write(t)
								}
							}
						}
					}
				}
			}
			walk(n.Children)
		}
	}
	walk(s.Nodes)
	return b.String()
}

// ── the Signatures screen, over state_list ─────────────────────────────

// ⚠ filex opens a home view with NO document. Before `state_list` the
// screen could only apologise; now it lists the plugin's own work.
func TestHome_ListsWhatEachPersonHasToDoWithIt(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}

	// The requester sees it under "I asked for these", with the document's
	// own name and an addressable row.
	mine := homeAs(t, a, burak, "open", "", nil)
	rows := rowsOf(mine)
	if rows[docPath] != views.OpenFollow {
		t.Fatalf("the requester's row is %q, expected %q (%v)", rows[docPath], views.OpenFollow, rows)
	}
	body := surfaceWords(mine)
	if !strings.Contains(body, docName) {
		t.Errorf("the screen does not name the document: %s", body)
	}
	if !strings.Contains(body, "Gökçe") || !strings.Contains(body, "ali@ornek.com") {
		t.Error("the screen should say who is being waited on")
	}

	// The signer sees the same document under "waiting for my signature".
	waiting := homeAs(t, a, gokce, "open", "", nil)
	if got := rowsOf(waiting)[docPath]; got != views.OpenSign {
		t.Errorf("the signer's row is %q, expected %q", got, views.OpenSign)
	}

	// After signing it moves to "I have signed these".
	signAs(t, a, f, gokce, "sig-1", nil)
	after := homeAs(t, a, gokce, "open", "", nil)
	if got := rowsOf(after)[docPath]; got != views.OpenVerify {
		t.Errorf("after signing the row is %q, expected %q", got, views.OpenVerify)
	}
}

// "Every request in this installation" means every one, the reader's own
// included. An administrator counting the open requests of the place they
// run must not come up short by exactly the ones they started themselves.
func TestHome_AdminSectionCountsTheAdminsOwnRequests(t *testing.T) {
	a, _ := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)}, nil)
	admin := wire.Actor{ID: burak.ID, Email: burak.Email, Name: burak.Name, Role: "admin"}
	if _, err := a.actionRequest(jobFrom(t, s, admin, docName)); err != nil {
		t.Fatal(err)
	}

	screen := homeAs(t, a, admin, "open", "", map[string]any{"section": views.SectionAll})
	words := surfaceWords(screen)
	if screen.Section != views.SectionAll {
		t.Errorf("the page drew section %q, asked for %q", screen.Section, views.SectionAll)
	}
	if !strings.Contains(words, "Yalnız görebileceğiniz belgeler listelenir") {
		t.Fatalf("an administrator gets no installation-wide section:\n%s", words)
	}
	// The row is the administrator’s own request, and it says so: only
	// the installation-wide section names the person who asked.
	if !strings.Contains(words, "isteyen "+burak.Name) {
		t.Errorf("the administrator’s own request is missing from the installation-wide section:\n%s", words)
	}

	// Somebody who does not run the place never sees that section at all —
	// not in the menu, and not by asking for it in the address.
	asked := homeAs(t, a, gokce, "open", "", map[string]any{"section": views.SectionAll})
	if strings.Contains(surfaceWords(asked), "Yalnız görebileceğiniz belgeler listelenir") || asked.Section == views.SectionAll {
		t.Error("a non-administrator is shown the installation-wide section")
	}
	for _, sec := range asked.Sections {
		if sec.ID == views.SectionAll {
			t.Error("a non-administrator's menu offers the installation-wide section")
		}
	}
}

// A document somebody signed by themselves carries no request at all —
// only the badge, which is why the badge names the signers.
func TestHome_ListsADocumentSignedWithoutARequest(t *testing.T) {
	a, f := newApp(t)
	scr, err := a.viewSignSelf(viewInput(ViewSignSelf, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	scr, _ = a.viewSignSelf(viewInput(ViewSignSelf, "submit", "", scr.State,
		map[string]any{views.IDFields: []map[string]any{sigBox("sig-1", "", .5)}}))
	scr, _ = a.viewSignSelf(viewInput(ViewSignSelf, "submit", "", scr.State, padValue(t, "sig-1")))
	scr, _ = a.viewSignSelf(viewInput(ViewSignSelf, "submit", "", scr.State,
		map[string]any{"output_mode": envelope.OutputVersion}))
	if _, err := a.actionSign(jobFrom(t, scr, burak, docName)); err != nil {
		t.Fatal(err)
	}
	badge, ok, _ := f.StateGet("in:0", envelope.SignedKey)
	if !ok || !envelope.HasSigner(badge, burak.ID) {
		t.Fatalf("the badge should name the signer, got %q", badge)
	}

	if got := rowsOf(homeAs(t, a, burak, "open", "", nil))[docPath]; got != views.OpenVerify {
		t.Errorf("the person who signed it should see it under what they signed, got %q", got)
	}
	// …and somebody else does not claim it.
	if _, listed := rowsOf(homeAs(t, a, gokce, "open", "", nil))[docPath]; listed {
		t.Error("a document signed by somebody else must not appear as mine")
	}
}

// ⚠ A row IS a document: clicking it goes there, on the screen that
// section is about. Before filex grew `Surface.Open` this screen could
// only name the file and hand over its path.
func TestHome_RowActionOpensTheDocumentOnTheRightScreen(t *testing.T) {
	a, _ := newApp(t)
	s := walkRequest(t, a, "", []map[string]any{sigBox("sig-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		who          wire.Actor
		rowAction    string
		action, view string
	}{
		{gokce, views.OpenSign, ActionFill, ""},
		{burak, views.OpenFollow, "", ViewStatus},
		{burak, views.OpenVerify, ActionVerify, ""},
	} {
		got := homeAs(t, a, c.who, "action", c.rowAction,
			map[string]any{"values": map[string]any{}, "row_id": docPath})
		if got.Open == nil {
			t.Fatalf("%s did not open anything: %+v", c.rowAction, got.State)
		}
		if got.Open.Path != docPath {
			t.Errorf("%s opened %q, expected the adapter-qualified %q", c.rowAction, got.Open.Path, docPath)
		}
		if got.Open.Action != c.action || got.Open.View != c.view {
			t.Errorf("%s opened action=%q view=%q, expected %q/%q",
				c.rowAction, got.Open.Action, got.Open.View, c.action, c.view)
		}
		if got.Open.Action != "" && got.Open.View != "" {
			t.Errorf("%s named both an action and a view", c.rowAction)
		}
		// The screen it names has to be one of ours, or the host refuses.
		if got.Open.Action != "" && a.Plugin().Actions[got.Open.Action] == nil {
			t.Errorf("%s opens action %q, which this plugin does not have", c.rowAction, got.Open.Action)
		}
		if got.Open.View != "" && a.Plugin().Views[got.Open.View] == nil {
			t.Errorf("%s opens view %q, which this plugin does not have", c.rowAction, got.Open.View)
		}
	}
}

// A path the screen did not draw is not opened: the row id only ever
// comes from a row this very call listed.
func TestHome_RowActionRefusesAPathItDidNotList(t *testing.T) {
	a, _ := newApp(t)
	got := homeAs(t, a, burak, "action", views.OpenSign,
		map[string]any{"values": map[string]any{}, "row_id": "docs://baskasinin/gizli.pdf"})
	if got.Open != nil {
		t.Fatalf("a crafted row id opened %+v", got.Open)
	}
}

// A state row written before the host kept the path (migration 00048) is
// not listed, and does not break the screen.
func TestHome_SkipsARowWithNoPathYet(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "", []map[string]any{sigBox("sig-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	f.Register("in:0", "", docName)
	if len(rowsOf(homeAs(t, a, burak, "open", "", nil))) != 0 {
		t.Error("a row with no path must not be listed")
	}
}

// ⚠ A screen is not the only place words reach somebody. The progress
// line in the tray, the subject of the page a stranger opens, the reason
// printed inside the signature and the reason a refused rename gives are
// all read by people — and all of them used to be English whatever the
// caller's language was.
func TestAJobSpeaksTheCallersLanguage(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)}, nil)
	job := jobFrom(t, s, burak, docName) // Locale: "tr"
	job.Params["lock"] = true
	if _, err := a.actionRequest(job); err != nil {
		t.Fatal(err)
	}

	// The tray.
	turkish := 0
	for _, line := range f.Progressed {
		if strings.Contains(line, "belge okunuyor") || strings.Contains(line, "davet ediliyor") ||
			strings.Contains(line, "imza bağlantısı açılıyor") || strings.Contains(line, "bitti") {
			turkish++
		}
		if strings.Contains(line, "reading the document") || strings.Contains(line, "inviting ") {
			t.Errorf("a Turkish job reported progress in English: %q", line)
		}
	}
	if turkish == 0 {
		t.Fatalf("no progress line was in Turkish: %v", f.Progressed)
	}

	// The page a stranger opens.
	env := loadEnv(t, a)
	share := f.Shares[env.Signers[1].PageToken]
	if share == nil {
		t.Fatal("no signing share")
	}
	// ⚠ And the page a stranger opens carries NO frozen line of the
	// requester's language: a subject is a string, printed under a title
	// the app draws in the VISITOR's language (2026-09-21: "Please sign
	// contract.pdf" on an otherwise Turkish page).
	if share.Req.Subject != "" {
		t.Errorf("the signing page carries a subject frozen in one language: %q", share.Req.Subject)
	}

	// The reason printed inside the signature, for ever, by every reader.
	signAs(t, a, f, gokce, "sig-1", nil)
	rep, err := a.report("in:0")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Signatures) != 1 {
		t.Fatalf("one signature expected, got %d", len(rep.Signatures))
	}
	if !strings.Contains(rep.Signatures[0].Reason, "isteğiyle imzalandı") {
		t.Errorf("the signature's reason is not in the request's language: %q", rep.Signatures[0].Reason)
	}
}

// …and the same job in English stays English.
func TestAnEnglishJobStaysEnglish(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "", []map[string]any{sigBox("sig-1", "s1", .1)}, nil)
	job := jobFrom(t, s, burak, docName)
	job.Locale = "en"
	if _, err := a.actionRequest(job); err != nil {
		t.Fatal(err)
	}
	for _, line := range f.Progressed {
		if strings.Contains(line, "belge okunuyor") || strings.Contains(line, "bitti") {
			t.Errorf("an English job reported progress in Turkish: %q", line)
		}
	}
}
