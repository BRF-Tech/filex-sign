package app

import (
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	filexsign "github.com/brf-tech/filex-sign"
	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/views"
)

// ── a document on a storage that takes no writes ───────────────────────
//
// 2026-09-21, a tester: a signing request on a read-only storage was
// accepted and "sent" — the file frozen, the signer notified — and the
// signer's answer then failed with 409 read_only, for ever. The request
// writes nothing itself, so filex had nothing to refuse; the app is told
// (wire.FileRef.ReadOnly) and refuses at the start.

func readOnlyInput(view string) *wire.ViewEventInput {
	in := viewInput(view, "open", "", nil, nil)
	in.Context.Inputs[0].ReadOnly = true
	return in
}

func TestReadOnly_TheWizardsSayItAtTheirFirstScreen(t *testing.T) {
	a, f := newApp(t)
	for _, c := range []struct {
		view string
		fn   func(*wire.ViewEventInput) (*wire.Surface, error)
		word string
	}{
		{ViewRequest, a.viewRequest, "Hiçbir şey gönderilmedi"},
		{ViewSignSelf, a.viewSignSelf, "imzalı kopya yanına kaydedilemez"},
	} {
		s, err := c.fn(readOnlyInput(c.view))
		if err != nil {
			t.Fatal(err)
		}
		txt := surfaceWords(s)
		if !strings.Contains(txt, "değişiklik kabul etmeyen bir depoda") || !strings.Contains(txt, c.word) {
			t.Errorf("%s: the first screen does not say why:\n%s", c.view, txt)
		}
		if len(s.Actions) != 0 || s.Job != nil {
			t.Errorf("%s: a screen that refuses offers nothing to press: %+v", c.view, s.Actions)
		}
	}
	if len(f.Notices) != 0 || len(f.Mails) != 0 || len(f.Shares) != 0 {
		t.Error("opening a wizard told somebody something")
	}
}

func TestReadOnly_TheRequestJobRefusesBeforeAnythingHappens(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com\nElden Veren", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .4), sigBox("sig-3", "s3", .7),
	}, nil)
	in := jobFrom(t, s, burak, docName)
	in.Inputs[0].ReadOnly = true
	out, err := a.actionRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Fatal("a request on a read-only storage was accepted")
	}
	if !strings.Contains(out.Message["tr"], "Hiçbir şey gönderilmedi") || !strings.Contains(out.Message["en"], "Nothing was sent") {
		t.Errorf("the refusal does not say that nothing happened: %v", out.Message)
	}
	if len(f.Notices) != 0 || len(f.Mails) != 0 || len(f.Shares) != 0 {
		t.Errorf("somebody was reached: notices %d, mails %d, links %d", len(f.Notices), len(f.Mails), len(f.Shares))
	}
	if _, found, _ := f.StateGet("in:0", envelope.StateKey); found {
		t.Error("a request record was written")
	}
	if _, found, _ := f.StateGet("in:0", envelope.PendingKey); found {
		t.Error("the file was marked as waiting")
	}
	if len(f.LockedRefs()) != 0 {
		t.Error("the file was frozen")
	}
}

// ── the Signatures home's "Due" column ─────────────────────────────────
//
// 2026-09-21, a tester: "Son tarih" read "—" on every row, because it
// printed the optional sign-by day alone; every request expires on a real
// day — the day its links die, which is when it closes.
func TestHome_DueIsTheDayTheRequestRunsOut(t *testing.T) {
	links := time.Date(2026, 9, 28, 14, 30, 0, 0, time.UTC).Format(time.RFC3339)
	env := &envelope.Envelope{Status: envelope.StatusSent, Signers: []envelope.Signer{{ID: "s1", PageExpires: links}}}
	if got := dueDay(env); got != "2026-09-28" {
		t.Errorf("due on the day the last link dies: %q", got)
	}
	env.Options.Deadline = "2026-09-24"
	if got := dueDay(env); got != "2026-09-24" {
		t.Errorf("a sign-by day before that wins, and is the day itself: %q", got)
	}
	env.Status = envelope.StatusCompleted
	if got := dueDay(env); got != "" {
		t.Errorf("a finished request is due on no day: %q", got)
	}
	inside := &envelope.Envelope{Status: envelope.StatusSent, Signers: []envelope.Signer{{ID: "s1"}}}
	if got := dueDay(inside); got != "" {
		t.Errorf("an inside-only request without a sign-by day never runs out: %q", got)
	}

	// ...and the column on the page prints it: a real request, sent with
	// links and no sign-by day, listed under "requested".
	a, _ := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com\nElden Veren", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .4), sigBox("sig-3", "s3", .7),
	}, nil)
	if out, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil || !out.OK {
		t.Fatalf("request: %v %v", err, out)
	}
	sent := loadEnv(t, a)
	want := dueDay(sent)
	if want == "" {
		t.Fatal("a request with links has a day it runs out")
	}
	home := homeAs(t, a, burak, "open", "", map[string]any{"section": views.SectionRequested})
	if !strings.Contains(surfaceWords(home), want) {
		t.Errorf("the Due column does not print %s:\n%s", want, surfaceWords(home))
	}
}

// ── the manifest ───────────────────────────────────────────────────────

// Every setting speaks both languages (2026-09-21: the settings were
// English inside the Turkish admin panel).
func TestManifest_SettingsSpeakBothLanguages(t *testing.T) {
	m := filexsign.Manifest()
	for _, s := range m.Settings {
		if s.I18n == nil {
			t.Errorf("setting %q has one-language texts", s.Key)
			continue
		}
		for _, lang := range views.Languages() {
			if s.I18n.Label[lang] == "" {
				t.Errorf("setting %q: no %s label", s.Key, lang)
			}
			if s.Help != "" && s.I18n.Help[lang] == "" {
				t.Errorf("setting %q: no %s help", s.Key, lang)
			}
		}
	}
}

// A signing link says what it is, in a list of links, and what revoking it
// does (the owner's decision of 2026-09-21), and opens a section that the
// Signatures page really has.
func TestManifest_LinksSayWhatTheyAre(t *testing.T) {
	m := filexsign.Manifest()
	sections := map[string]bool{}
	for _, s := range views.HomeSections(views.HomeInput{Admin: true}) {
		sections[s] = true
	}
	for _, p := range m.PublicPages {
		if p.Purpose == nil {
			t.Errorf("page %q lists as a plain share", p.ID)
			continue
		}
		for _, lang := range views.Languages() {
			if p.Purpose.Label[lang] == "" || p.Purpose.Revoke[lang] == "" {
				t.Errorf("page %q: purpose not said in %s", p.ID, lang)
			}
		}
		if !sections[p.Purpose.Section] {
			t.Errorf("page %q opens section %q, which the Signatures page does not have", p.ID, p.Purpose.Section)
		}
	}
	for _, a := range m.Actions {
		if a.ID == ActionApply && !a.Hidden {
			t.Error("apply is started by the app, never offered to a person")
		}
	}
}

// The section a link opens belongs to the person whose link it is.
func TestLinkSection_FollowsWhoseLinkItIs(t *testing.T) {
	env := &envelope.Envelope{Requester: envelope.Person{UserID: 1}}
	if got := linkSection(env, wire.Actor{ID: 1}); got != views.SectionRequested {
		t.Errorf("the requester's link: %q", got)
	}
	if got := linkSection(env, wire.Actor{ID: 2}); got != views.SectionSigned {
		t.Errorf("a signer's link: %q", got)
	}
}

// ── a person on the Signatures home and the status table ───────────────
//
// filex names a person one way on its screens — the display name, else the
// username, else the e-mail (v0.43.0; the app is handed the name, never the
// username). The tables did "Name (e-mail)" instead. The certificate and the
// signature line keep the e-mail on purpose (a certificate identifies the
// person outside filex); the tables follow filex.
func TestTables_NameAPersonTheWayFilexDoes(t *testing.T) {
	env := &envelope.Envelope{
		Status:    envelope.StatusSent,
		Requester: envelope.Person{UserID: 1, Name: "Burak Faruk Şahin", Email: "burak@ornek.com"},
		Signers: []envelope.Signer{
			{ID: "s1", Person: envelope.Person{UserID: 3, Name: "Gülşen", Email: "gulsen@local"}},
			{ID: "s2", Person: envelope.Person{Email: "ayse@ornek.com"}},
		},
	}
	card := cardOf(env, pluginkit.StateItem{Name: "sözleşme.pdf", Path: "docs://sözleşme.pdf"})
	if card.Waiting != "Gülşen, ayse@ornek.com" {
		t.Errorf("the waiting column: %q", card.Waiting)
	}
	status := views.Status(views.TR, views.StatusInput{Env: env})
	words := surfaceWords(status)
	if strings.Contains(words, "gulsen@local") {
		t.Errorf("the status table prints the e-mail of a person who has a name:\n%s", words)
	}
	if !strings.Contains(words, "Gülşen") || !strings.Contains(words, "ayse@ornek.com") {
		t.Errorf("the status table does not name the signers:\n%s", words)
	}
}

// ── wave 2: what the menu is told ─────────────────────────────────────

// The manifest says what filex needs to offer the actions to the right
// people only: "Request signatures" ends in a write (hidden on a read-only
// storage), "Sign / Fill" asks for the PERSONAL marker, and every lock
// reason the code names is a message the manifest carries in every language.
func TestManifest_TellsTheMenuWhoAndWhere(t *testing.T) {
	m := filexsign.Manifest()
	for _, a := range m.Actions {
		switch a.ID {
		case ActionRequest:
			if !a.Applies.Writable {
				t.Error("request must say its flow ends in a write (applies.writable)")
			}
		case ActionFill:
			if len(a.Applies.State) != 1 || a.Applies.State[0] != envelope.TodoKey+wire.PersonalStateSuffix {
				t.Errorf("fill must ask for the personal marker %q, got %v", envelope.TodoKey+wire.PersonalStateSuffix, a.Applies.State)
			}
		}
	}
	for _, key := range []string{LockCollecting, LockSealed} {
		msg, ok := m.Messages[key]
		if !ok {
			t.Fatalf("the manifest has no message %q", key)
		}
		for _, l := range m.Languages {
			if msg[l] == "" {
				t.Errorf("message %q has no %s", key, l)
			}
		}
	}
}

// The personal marker follows whose turn it is: set for each signer with an
// account who has to sign now, gone once they answered, gone for everybody
// when the request closes. In a sequential request only the one whose turn
// it is has it.
func TestTurnMarkers(t *testing.T) {
	a, f := newApp(t)
	ref := "in:0"
	env := &envelope.Envelope{Status: envelope.StatusSent, Options: envelope.Options{Order: envelope.OrderSequential},
		Signers: []envelope.Signer{
			{ID: "s1", Person: envelope.Person{UserID: 3, Name: "Gülşen"}},
			{ID: "s2", Person: envelope.Person{UserID: 4, Name: "Hans"}},
			{ID: "s3", Person: envelope.Person{Email: "dis@ornek.com"}},
		}}
	has := func(uid int64) bool {
		_, found, _ := f.StateGet(ref, wire.PersonalState(envelope.TodoKey, uid))
		return found
	}
	// Every write of the record keeps the markers in step (store).
	if err := a.store(ref, env); err != nil {
		t.Fatal(err)
	}
	if !has(3) || has(4) {
		t.Fatalf("sequential: only the first signer's turn (3:%v 4:%v)", has(3), has(4))
	}
	env.Signers[0].Status = envelope.SignerSigned
	a.markTurns(ref, env)
	if has(3) || !has(4) {
		t.Fatalf("after the first signed: the second's turn (3:%v 4:%v)", has(3), has(4))
	}
	env.Options.Order = ""
	env.Signers[0].Status = ""
	a.markTurns(ref, env)
	if !has(3) || !has(4) {
		t.Fatalf("parallel: everybody who has not answered (3:%v 4:%v)", has(3), has(4))
	}
	env.Status = envelope.StatusCancelled
	a.markTurns(ref, env)
	if has(3) || has(4) {
		t.Fatalf("a closed request leaves no marker (3:%v 4:%v)", has(3), has(4))
	}
}
