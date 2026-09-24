package app

// What the request wizard SAYS against what then HAPPENS.
//
// Three findings of one kind, each measured on a live instance or in an
// audit before it was fixed:
//
//   - the links' life: the Time step offered 14 days and the review said
//     "links valid 14 days", and filex cut every link to its share ceiling
//     (7 days by default) without a word;
//   - the defaults: "Let a signer refuse" and "Write an audit trail PDF" were
//     declared ON and started OFF;
//   - a second request on a document whose first one was still open walked
//     all eight steps and ended in "Invalid data".
//
// ⚠ The walk below posts what a person who touches nothing would post: the
// value each form SHOWS (its `values`, else the field's declared default),
// because that is what the browser sends. Posting nothing would let the
// state keep whatever it started with and hide exactly the second defect.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/humandate"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/views"
)

func ptr(n int) *int { return &n }

// formsOf collects every form field of a surface with the value it shows.
func formsOf(s *wire.Surface) (fieldsByKey map[string]wire.Field, shown map[string]any) {
	fieldsByKey, shown = map[string]wire.Field{}, map[string]any{}
	var walk func(nodes []wire.Node)
	walk = func(nodes []wire.Node) {
		for _, n := range nodes {
			if n.Type == "form" {
				fs, _ := n.Props["fields"].([]wire.Field)
				vals, _ := n.Props["values"].(map[string]any)
				for _, f := range fs {
					fieldsByKey[f.Key] = f
					if v, ok := vals[f.Key]; ok {
						shown[f.Key] = v
					} else if f.Default != nil {
						shown[f.Key] = f.Default
					}
				}
			}
			walk(n.Children)
		}
	}
	walk(s.Nodes)
	return fieldsByKey, shown
}

// requestIn is a wizard call with the host's share ceiling in its context,
// the way filex (v0.43.0+) sends it.
func requestIn(event string, state, values map[string]any, ceiling *int) *wire.ViewEventInput {
	in := viewInput(ViewRequest, event, "", state, values)
	in.Context.ShareMaxTTLDays = ceiling
	in.Context.Inputs[0].Path = docPath // filex fills it whenever the storage is known
	return in
}

// walkShown runs the wizard from its first screen to Send, posting on every
// step what the screen shows, overridden only by `answers`.
func walkShown(t *testing.T, a *App, ceiling *int, answers map[int]map[string]any) (drawn map[int]*wire.Surface, job *wire.Surface) {
	t.Helper()
	drawn = map[int]*wire.Surface{}
	s, err := a.viewRequest(requestIn("open", nil, nil, ceiling))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		step, _ := s.State["step"].(int)
		drawn[step] = s
		_, shown := formsOf(s)
		s, err = a.viewRequest(requestIn("submit", s.State, merge(shown, answers[step]), ceiling))
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Errors) > 0 {
			t.Fatalf("step %d refused: %+v", step, s.Errors)
		}
		if s.Job != nil {
			return drawn, s
		}
	}
	t.Fatalf("the wizard never queued the request")
	return nil, nil
}

// oneOutsider is the least a request needs: one outside signer and the one
// box they sign in, already placed.
func oneOutsider(extra map[int]map[string]any) map[int]map[string]any {
	boxes := []map[string]any{sigBox("sig-1", "s1", .1)}
	out := map[int]map[string]any{
		views.StepSigners: {"identities": "Ali Yılmaz <ali@ornek.com>"},
		views.StepBoxes:   {views.IDFields: boxes},
		views.StepPlace:   {views.IDFields: boxes},
	}
	for k, v := range extra {
		out[k] = merge(out[k], v)
	}
	return out
}

// ── the links' life ─────────────────────────────────────────────────────

func TestLinkLife_TheWizardOffersNoMoreThanTheHostAllows(t *testing.T) {
	for _, tc := range []struct {
		name             string
		ceiling          *int
		wantMax, wantDef int
		capped           bool
	}{
		{"filex's default ceiling of 7 days", ptr(7), 7, 7, true},
		{"a ceiling between the page's default and its maximum", ptr(30), 30, 14, true},
		{"a ceiling above the page's own maximum", ptr(365), 90, 14, false},
		{"no ceiling at all", ptr(0), 90, 14, false},
		{"a host that does not say", nil, 90, 14, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newApp(t)
			drawn, _ := walkShown(t, a, tc.ceiling, oneOutsider(nil))

			fs, shown := formsOf(drawn[views.StepTiming])
			f, ok := fs["expiry"]
			if !ok {
				t.Fatal("the Time step asks nothing about the links' life")
			}
			if f.Max == nil || *f.Max != tc.wantMax {
				t.Errorf("the Time step accepts up to %v days, the links can live %d", deref(f.Max), tc.wantMax)
			}
			if f.Default != tc.wantDef || shown["expiry"] != tc.wantDef {
				t.Errorf("the Time step starts at %v (declared %v), want %d", shown["expiry"], f.Default, tc.wantDef)
			}
			said := strings.Contains(f.Help, fmt.Sprintf("En fazla %d", tc.wantMax))
			if said != tc.capped {
				t.Errorf("the reason for the ceiling said=%v, want %v: %q", said, tc.capped, f.Help)
			}

			review := surfaceWords(drawn[views.StepReview])
			if want := fmt.Sprintf("bağlantılar %d gün geçerli", tc.wantDef); !strings.Contains(review, want) {
				t.Errorf("the review does not say %q:\n%s", want, review)
			}
		})
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// ⭐ End to end: what the review said is what the link lives, and every
// record of it — the envelope, the host's link, the requester's notice —
// says the same day.
func TestLinkLife_TheLinkLivesWhatTheReviewSaid(t *testing.T) {
	a, f := newApp(t)
	f.ShareMaxTTLDays = 7 // what filex does to a link, whatever it is asked
	drawn, s := walkShown(t, a, ptr(7), oneOutsider(nil))
	if review := surfaceWords(drawn[views.StepReview]); !strings.Contains(review, "links valid 7 days") {
		t.Fatalf("the review promises something else:\n%s", review)
	}
	job := jobFrom(t, s, burak, docName)
	job.ShareMaxTTLDays = ptr(7)
	out, err := a.actionRequest(job)
	if err != nil || !out.OK {
		t.Fatalf("the request failed: %v %v", err, out)
	}
	env := loadEnv(t, a)
	if env.Options.ExpiryDays != 7 {
		t.Errorf("the record says the links live %d days", env.Options.ExpiryDays)
	}
	link := f.Shares[env.Signers[0].PageToken]
	if got := link.Expires.Sub(f.Clock); got != 7*24*time.Hour {
		t.Errorf("the link lives %s, the review said 7 days", got)
	}
	// …in the words filex writes dates with, not the ISO day (v0.43.0 wave 2).
	day := humandate.Stamp("en", env.Signers[0].PageExpires)
	if !strings.Contains(out.Message["en"], "The signing links work until "+day) {
		t.Errorf("the requester is not told the day the links stop (%s):\n%s", day, out.Message["en"])
	}
	if iso := envelope.Day(env.Signers[0].PageExpires); strings.Contains(out.Message["en"], iso) || strings.Contains(out.Message["tr"], iso) {
		t.Errorf("the message prints the ISO day %s:\n%s", iso, out.Message["tr"])
	}
}

// ⚠ A job re-reads the ceiling. A screen drawn under a higher one (the
// administrator lowered it while the wizard was open, or a job queued some
// other way) still gets a record, a freeze and links that agree with the
// host — the app asks for 7, it does not ask for 14 and let filex cut it.
func TestLinkLife_TheJobAsksForNoMoreThanTheCeiling(t *testing.T) {
	a, f := newApp(t)
	_, s := walkShown(t, a, ptr(30), oneOutsider(map[int]map[string]any{
		views.StepTiming: {"expiry": 14}, views.StepOptions: {"lock": true}}))
	job := jobFrom(t, s, burak, docName)
	job.ShareMaxTTLDays = ptr(7)
	if out, err := a.actionRequest(job); err != nil || !out.OK {
		t.Fatalf("the request failed: %v %v", err, out)
	}
	env := loadEnv(t, a)
	if env.Options.ExpiryDays != 7 {
		t.Errorf("the record says %d days under a 7-day ceiling", env.Options.ExpiryDays)
	}
	if asked := f.Shares[env.Signers[0].PageToken].Req.TTLDays; asked != 7 {
		t.Errorf("the app asked filex for %d days under a 7-day ceiling", asked)
	}
	if until := f.Locks["in:0"]; until.Sub(f.Clock) > 7*24*time.Hour {
		t.Errorf("the freeze outlives the links: until %s", until)
	}
}

// ⚠ The sign-by day counts to its END. The links used to be cut to whole
// days rounded DOWN, so a request sent at 09:00 "to sign by the 22nd" had
// its link die at 09:00 on the 22nd.
func TestLinkLife_TheSignByDayCountsToItsEnd(t *testing.T) {
	a, f := newApp(t) // the clock stands at 2026-09-20 09:00
	drawn, s := walkShown(t, a, ptr(7), oneOutsider(map[int]map[string]any{
		views.StepTiming: {"deadline": "2026-09-22"}}))
	if review := surfaceWords(drawn[views.StepReview]); !strings.Contains(review, "links valid until the end of Sep 22, 2026") {
		t.Errorf("the review does not say the links last the whole sign-by day:\n%s", review)
	}
	job := jobFrom(t, s, burak, docName)
	job.ShareMaxTTLDays = ptr(7)
	if out, err := a.actionRequest(job); err != nil || !out.OK {
		t.Fatalf("the request failed: %v %v", err, out)
	}
	env := loadEnv(t, a)
	endOfDay := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	exp := f.Shares[env.Signers[0].PageToken].Expires
	if exp.Before(endOfDay) {
		t.Errorf("the link dies at %s, before the sign-by day is over", exp)
	}
	if exp.After(endOfDay.Add(24 * time.Hour)) {
		t.Errorf("the link outlives the sign-by day by more than a day: %s", exp)
	}
	if at := lapseAt(env); !at.Equal(endOfDay) {
		t.Errorf("the request should close as the sign-by day ends, closes %s", at)
	}
}

// ── the defaults ────────────────────────────────────────────────────────

// ⭐ Somebody who presses Next on every step without touching anything gets
// every choice the screens declared — the class of defect, not the two
// instances: every field with a declared default, on every step, is checked.
func TestRequestWizard_WhatAPersonGetsIsWhatTheFormDeclares(t *testing.T) {
	a, _ := newApp(t)
	drawn, s := walkShown(t, a, ptr(7), oneOutsider(nil))
	for step, sf := range drawn {
		fs, _ := formsOf(sf)
		_, vals := formsOf(sf)
		for key, f := range fs {
			if f.Default == nil {
				continue
			}
			var raw map[string]any
			for _, n := range sf.Nodes {
				if n.Type == "form" {
					if v, ok := n.Props["values"].(map[string]any); ok {
						if _, has := v[key]; has {
							raw = v
						}
					}
				}
			}
			if raw == nil {
				continue // the screen shows the default itself
			}
			if fmt.Sprint(raw[key]) != fmt.Sprint(f.Default) {
				t.Errorf("step %d: %q is declared %v but starts as %v", step, key, f.Default, vals[key])
			}
		}
	}
	for key, want := range map[string]bool{"allow_decline": views.DefaultAllowDecline, "audit": views.DefaultAudit} {
		if got, _ := s.Job.Params[key].(bool); got != want {
			t.Errorf("nobody touched %q and the request went out with %v", key, got)
		}
	}
}

// ── a second request on the same document ─────────────────────────────

func sendRequest(t *testing.T, a *App) *envelope.Envelope {
	t.Helper()
	_, s := walkShown(t, a, ptr(7), oneOutsider(nil))
	job := jobFrom(t, s, burak, docName)
	job.ShareMaxTTLDays = ptr(7)
	out, err := a.actionRequest(job)
	if err != nil || !out.OK {
		t.Fatalf("the request failed: %v %v", err, out)
	}
	return loadEnv(t, a)
}

// ⭐ While the first request is open the wizard says so on its FIRST
// screen, in the person's language, with the way forward — not "Invalid
// data" after eight steps.
func TestSecondRequest_WhileTheFirstIsOpenIsRefusedAtOnce(t *testing.T) {
	a, _ := newApp(t)
	first := sendRequest(t, a)

	s, err := a.viewRequest(requestIn("open", nil, nil, ptr(7)))
	if err != nil {
		t.Fatal(err)
	}
	words := surfaceWords(s)
	for _, want := range []string{"Bu belgenin zaten bir imza isteği var", "Ali Yılmaz", "hâlâ açık", "aynı anda tek bir imza isteği"} {
		if !strings.Contains(words, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, words)
		}
	}
	if s.Job != nil || len(s.Actions) != 1 || s.Actions[0].ID != views.ActionOpenStatus {
		t.Fatalf("the refusal offers exactly one way forward, the Signatures panel: %+v", s.Actions)
	}
	open, err := a.viewRequest(func() *wire.ViewEventInput {
		in := requestIn("action", s.State, nil, ptr(7))
		in.ActionID = views.ActionOpenStatus
		return in
	}())
	if err != nil {
		t.Fatal(err)
	}
	if open.Open == nil || open.Open.View != ViewStatus || open.Open.Path != docPath {
		t.Fatalf("the button does not go to the document's Signatures panel: %+v", open.Open)
	}

	// A Send that arrives anyway — a wizard opened before the first request
	// went out — gets the same answer and queues nothing.
	st := views.StateOf(views.RequestState{Step: views.StepReview, Identities: "x@ornek.com",
		Fields: []envelope.Field{{ID: "sig-1", Type: "signature", Page: 1, X: .1, Y: .8, W: .3, H: .08, Assignee: "s1"}}})
	send, err := a.viewRequest(requestIn("submit", st, nil, ptr(7)))
	if err != nil {
		t.Fatal(err)
	}
	if send.Job != nil || !strings.Contains(surfaceWords(send), "Bu belgenin zaten bir imza isteği var") {
		t.Fatalf("a Send on a document with an open request was not refused by the app: %+v", send)
	}
	if after := loadEnv(t, a); after.ID != first.ID || after.Status.Closed() {
		t.Errorf("the open request was disturbed")
	}
}

// ⭐ Once the first request has ended, a second one is the next round on
// the same document: it works, and the first screen says what it replaces.
func TestSecondRequest_AfterTheFirstEndedIsTheNextRound(t *testing.T) {
	a, _ := newApp(t)
	first := sendRequest(t, a)
	if out, err := a.actionApply(&wire.ActionRunInput{ActionID: ActionApply, Locale: "tr", Actor: burak,
		Params: map[string]any{"op": "cancel"}, Inputs: []wire.FileRef{{Ref: "in:0", Name: docName}}}); err != nil || !out.OK {
		t.Fatalf("cancelling the first: %v %v", err, out)
	}

	s, err := a.viewRequest(requestIn("open", nil, nil, ptr(7)))
	if err != nil {
		t.Fatal(err)
	}
	if words := surfaceWords(s); !strings.Contains(words, "daha önce bir imza isteği vardı") || !strings.Contains(words, "İptal edildi") {
		t.Errorf("the first screen does not say a previous request is being replaced:\n%s", words)
	}
	second := sendRequest(t, a)
	if second.ID == first.ID || second.Status.Closed() {
		t.Fatalf("the second request did not start: %+v", second.Status)
	}
}

// The one thing a finished request's record holds that no file does: an
// audit trail asked for with a new-version output. The first screen of the
// next round says to save it first.
func TestSecondRequest_WarnsAboutAnUnsavedAuditTrail(t *testing.T) {
	a, _ := newApp(t)
	env := &envelope.Envelope{Schema: envelope.SchemaVersion, ID: "old1", Status: envelope.StatusCompleted,
		Document: docName, Requester: envelope.Person{UserID: burak.ID, Name: burak.Name},
		CreatedAt: envelope.Stamp(time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)), ClosedAt: envelope.Stamp(time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)),
		Options: envelope.Options{Audit: true, Output: envelope.Output{Mode: envelope.OutputVersion}},
		Signers: []envelope.Signer{{ID: "s1", Status: envelope.SignerSigned, Person: envelope.Person{Name: "Ali"}}}}
	if err := a.store("in:0", env); err != nil {
		t.Fatal(err)
	}
	s, err := a.viewRequest(requestIn("open", nil, nil, ptr(7)))
	if err != nil {
		t.Fatal(err)
	}
	if words := surfaceWords(s); !strings.Contains(words, "denetim izi hiçbir zaman dosya olarak yazılmadı") {
		t.Errorf("the unsaved audit trail is not mentioned:\n%s", words)
	}
}
