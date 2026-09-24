package app

// A signing link a PERSON ended on filex's Shares screen.
//
// Measured on a live instance before the fix (2026-09-21): the requester
// revoked the link under My shares, the wake-up ran, saw the request and
// scheduled nothing; the panel said "Sent · 0/1 signed" and the request would
// have stayed open with a link nobody could use. Nothing tells the app — it
// has to ask. These tests make the asking fail loudly when it stops happening.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/host"
	"github.com/brf-tech/filex-sign/internal/views"
)

// twoSigners opens a request, frozen, for an outside signer (Ali) and one of
// this installation (Gökçe), exactly as the wizard sends it.
func twoSigners(t *testing.T, a *App) *envelope.Envelope {
	t.Helper()
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)}, map[string]any{"lock": true})
	job := jobFrom(t, s, burak, docName)
	job.Params["lock"] = true
	if out, err := a.actionRequest(job); err != nil || !out.OK {
		t.Fatalf("the request failed: %v %v", err, out)
	}
	return loadEnv(t, a)
}

// runItem runs one scheduled item the way filex does: an ordinary job with
// nobody behind it.
func runItem(t *testing.T, a *App, f *host.Fake, it *wire.ScheduleItem) *wire.ActionRunOutput {
	t.Helper()
	out, err := a.actionApply(&wire.ActionRunInput{JobID: "sched", ActionID: it.ActionID, Params: it.Params,
		Inputs: []wire.FileRef{{Ref: refFor(t, f, it.Paths[0]), Name: docName, Size: 1000}}})
	if err != nil {
		t.Fatalf("the scheduled job failed: %v", err)
	}
	if !out.OK {
		t.Fatalf("the scheduled job reported a failure: %v", out.Message)
	}
	return out
}

// ⭐ Revoked on the Shares screen → the next wake-up closes the request, at
// once, and everybody who should hear about it does — once.
func TestLinkEnded_TheWakeUpClosesTheRequestAndTellsEverybody(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		name := map[bool]string{false: "revoked", true: "deleted"}[deleted]
		t.Run(name, func(t *testing.T) {
			h, a, f := harness(t)
			env := twoSigners(t, a)
			ali := env.Signers[1]
			notices, mails := len(f.Notices), len(f.Mails)

			f.Clock = f.Clock.Add(2 * time.Hour)
			f.EndFromShares(ali.PageToken, deleted)

			out := wake(t, h, f.Clock)
			it := itemFor(out, "ended:"+env.ID)
			if it == nil {
				t.Fatalf("the wake-up did not notice the ended link: %v", keysOf(out))
			}
			if it.DueAt.After(f.Clock) {
				t.Errorf("the closure waits until %s; the link is already dead", it.DueAt)
			}
			if len(out.Items) != 1 {
				t.Errorf("a request about to close was also given other work: %v", keysOf(out))
			}
			if !strings.Contains(out.Note["en"], "a link was ended on the Shares screen") {
				t.Errorf("the wake-up's log line does not say why: %q", out.Note["en"])
			}

			res := runItem(t, a, f, it)
			if !strings.Contains(res.Message["tr"], "Paylaşımlar ekranından sonlandırıldı") {
				t.Errorf("the job does not say what it did: %v", res.Message)
			}
			after := loadEnv(t, a)
			if after.Status != envelope.StatusCancelled || after.ClosedAt == "" {
				t.Fatalf("the request should be closed, is %q", after.Status)
			}
			last := after.Events[len(after.Events)-1]
			if last.Type != envelope.EvLinkEnded || last.Signer != ali.ID || last.Note != name {
				t.Errorf("the record does not say why it closed: %+v", last)
			}
			if len(f.LockedRefs()) != 0 {
				t.Errorf("a closed request must release the file: %v", f.LockedRefs())
			}
			if _, ok, _ := f.StateGet("in:0", envelope.PendingKey); ok {
				t.Error("the pending marker outlived the request")
			}
			// The requester, once, with the reason.
			told := noticesTitled(f.Notices[notices:], "İmza isteği kapandı: bir imza bağlantısı sonlandırıldı")
			if len(told) != 1 || told[0].ToUserID != burak.ID {
				t.Fatalf("the requester was not told exactly once: %+v", f.Notices[notices:])
			}
			how := map[bool]string{false: "iptal edildi", true: "silindi"}[deleted]
			if !strings.Contains(told[0].Body["tr"], "ali@ornek.com") || !strings.Contains(told[0].Body["tr"], how) {
				t.Errorf("the notice does not say whose link or what happened to it: %q", told[0].Body["tr"])
			}
			// Gökçe was waiting too: told in filex, never mailed.
			if got := noticesTitled(f.Notices[notices:], "Bir imza isteği kapandı"); len(got) != 1 || got[0].ToUserID != gokce.ID {
				t.Errorf("the other signer was not told exactly once: %+v", f.Notices[notices:])
			}
			// ⚠ Ali's link was ended ON PURPOSE — perhaps it went to the wrong
			// address. Nothing more is sent there.
			if got := mailsTo(f.Mails[mails:], "ali@ornek.com"); len(got) != 0 {
				t.Errorf("the address whose link was ended was written to: %+v", got)
			}
			// The panel says why a request nobody cancelled is "Cancelled".
			panel, err := a.viewStatus(viewInput(ViewStatus, "open", "", nil, nil))
			if err != nil {
				t.Fatal(err)
			}
			if words := surfaceWords(panel); !strings.Contains(words, "Paylaşımlar ekranında "+how) {
				t.Errorf("the panel does not say why the request closed:\n%s", words)
			}
			// …and the next wake-up has nothing to do.
			if again := wake(t, h, f.Clock.Add(time.Hour)); len(again.Items) != 0 {
				t.Errorf("a closed request was scheduled again: %v", keysOf(again))
			}
		})
	}
}

// A link that ran out ON its day was not ended by anybody: that is the
// lapse, and it closes as expired, at its own minute.
func TestLinkEnded_ALinkThatRanOutIsNotEnded(t *testing.T) {
	h, a, f := harness(t)
	env := twoSigners(t, a)
	exp, err := time.Parse(time.RFC3339, env.Signers[1].PageExpires)
	if err != nil {
		t.Fatal(err)
	}
	f.Clock = exp.Add(time.Minute)
	out := wake(t, h, f.Clock)
	if itemFor(out, "ended:"+env.ID) != nil {
		t.Fatalf("a link that simply ran out was taken for one a person ended: %v", keysOf(out))
	}
	if itemFor(out, "expire:"+env.ID) == nil {
		t.Fatalf("the lapse itself was not scheduled: %v", keysOf(out))
	}
}

// The app's OWN revoke — a signer's link stops the moment they have signed
// — is not a person ending it.
func TestLinkEnded_TheAppsOwnRevokeIsNotAPersons(t *testing.T) {
	h, a, f := harness(t)
	env := twoSigners(t, a)
	signAs(t, a, f, gokce, "sig-1", nil)
	// Ali signs through the link; the app revokes it as it lands.
	ali := env.Signers[1]
	f.BindShare(ali.PageToken)
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
	if _, err := a.actionApply(jobFrom(t, fromShare(t, page, ali.PageToken), burak, docName)); err != nil {
		t.Fatal(err)
	}
	if !f.Shares[ali.PageToken].Revoked {
		t.Fatal("the app should have revoked the link it no longer needs")
	}
	if got := loadEnv(t, a); got.Status != envelope.StatusCompleted {
		t.Fatalf("the request should have completed, is %q", got.Status)
	}
	if out := wake(t, h, f.Clock.Add(time.Hour)); len(out.Items) != 0 {
		t.Errorf("a completed request was scheduled for closing: %v", keysOf(out))
	}
}

// The belt: an installation whose wake-up is off (a demo) still closes the
// request the first time any job touches it.
func TestLinkEnded_EveryJobAsksToo(t *testing.T) {
	a, f := newApp(t)
	env := twoSigners(t, a)
	f.EndFromShares(env.Signers[1].PageToken, false)
	if _, err := a.actionApply(&wire.ActionRunInput{ActionID: ActionApply, Locale: "tr", Actor: burak,
		Params: map[string]any{"op": "remind", "signer_id": env.Signers[0].ID},
		Inputs: []wire.FileRef{{Ref: "in:0", Name: docName, Size: 1000}}}); err != nil {
		t.Fatal(err)
	}
	if got := loadEnv(t, a); got.Status != envelope.StatusCancelled {
		t.Fatalf("a job on a request with an ended link left it %q", got.Status)
	}
}

// ⚠ A host that cannot answer is not a host that said "gone": the request
// stays open. Closing a live request on a guess destroys it.
func TestLinkEnded_NeverOnAGuess(t *testing.T) {
	h, a, f := harness(t)
	env := twoSigners(t, a)
	f.ShareInfoErr = errors.New("database is locked")
	if out := wake(t, h, f.Clock.Add(time.Hour)); itemFor(out, "ended:"+env.ID) != nil {
		t.Fatalf("an unanswered question closed the request: %v", keysOf(out))
	}
	// And the scheduled job, run anyway, re-asks and closes nothing.
	res := runItem(t, a, f, &wire.ScheduleItem{ActionID: ActionApply, Paths: []string{docPath},
		Params: map[string]any{"op": opLinkEnded, paramScheduled: true}})
	if !strings.Contains(res.Message["en"], "Nothing to close") {
		t.Errorf("unexpected answer: %v", res.Message)
	}
	if got := loadEnv(t, a); got.Status.Closed() {
		t.Fatalf("the request was closed: %q", got.Status)
	}
}
