package app

// The hourly wake-up, measured where a mistake costs seconds instead of
// an hour of wall clock.
//
// ⚠⚠ A tick is the one call with nobody in front of it. Every other
// mistake in this plugin shows up as a screen that looks wrong; a mistake
// here shows up as work that SILENTLY NEVER HAPPENED — a request that
// should have closed at 03:00 still sitting open at noon, with no error
// anywhere to read. So these tests do not ask "did it run": they ask what
// was scheduled, for exactly which minute, under which key, and they
// prove the wake-up itself wrote nothing at all.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/host"
)

// ── fixtures ───────────────────────────────────────────────────────────

// day is the clock host.NewFake starts on, to the hour.
var day = time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)

func at(d, h, m int) time.Time { return time.Date(2026, 9, d, h, m, 0, 0, time.UTC) }

// harness wraps the plugin in the SDK's own test kit, which is what runs
// the wake-up the way filex runs it — permission checked, read-only scope,
// window from `now` to the next hour boundary.
func harness(t *testing.T) (*plugintest.Harness, *App, *host.Fake) {
	t.Helper()
	a, f := newApp(t)
	// See TestManifestPassesTheSDKsOwnChecks: the kit's copy of the closed
	// permission set has no `schedule` in it yet, and Harness.Tick refuses
	// to run a wake-up the manifest does not ask for.
	plugintest.Permissions["schedule"] = true
	return plugintest.New(a.Plugin()), a, f
}

// request is one open signature request, before any mutation: an outside
// signer with a link that runs out, and a signer of this installation.
func request(id string, mut ...func(*envelope.Envelope)) *envelope.Envelope {
	env := &envelope.Envelope{
		Schema: envelope.SchemaVersion, ID: id, Status: envelope.StatusSent,
		Document:  id + ".pdf",
		Requester: envelope.Person{UserID: burak.ID, Email: burak.Email, Name: burak.Name},
		CreatedAt: envelope.Stamp(day), UpdatedAt: envelope.Stamp(day),
		Signers: []envelope.Signer{
			{ID: "s1", Kind: envelope.KindExternal, Status: envelope.SignerNotified,
				Person:      envelope.Person{Email: "ali@ornek.com", Name: "Ali"},
				PageToken:   "tok-" + id,
				PageExpires: envelope.Stamp(at(21, 9, 0)), NotifiedAt: envelope.Stamp(day)},
			{ID: "s2", Kind: envelope.KindInternal, Status: envelope.SignerNotified,
				Person:     envelope.Person{UserID: gokce.ID, Email: gokce.Email, Name: gokce.Name},
				NotifiedAt: envelope.Stamp(day)},
		},
	}
	for _, m := range mut {
		m(env)
	}
	return env
}

// seed puts a request on a file of its own, exactly as the plugin does:
// the record under the envelope key, and the host's path for it, which is
// the spelling StateList answers with and a schedule item must carry.
func seed(t *testing.T, a *App, f *host.Fake, n int, env *envelope.Envelope) string {
	t.Helper()
	ref := fmt.Sprintf("in:%d", n)
	f.Inputs[ref] = []byte("%PDF-1.7\n")
	f.Register(ref, "docs://sozlesmeler/"+env.Document, env.Document)
	// ⚠ Every link the record names is a share the host HAS, living exactly
	// as long as the record says: the wake-up asks the host about each one
	// (links.go), and a token the host does not know reads as a link an
	// administrator deleted — which is a different test.
	for _, sg := range env.Signers {
		if sg.PageToken == "" {
			continue
		}
		exp, err := time.Parse(time.RFC3339, sg.PageExpires)
		if err != nil {
			exp = f.Clock.AddDate(0, 0, 14)
		}
		f.Shares[sg.PageToken] = &host.FakeShare{Token: sg.PageToken, Expires: exp,
			Req: pluginkit.PageCreate{PageID: PageSigner}}
	}
	if err := a.store(ref, env); err != nil {
		t.Fatalf("seeding %s: %v", env.ID, err)
	}
	return ref
}

// wake runs the wake-up filex would run at `now` and checks the answer
// against the bounds the host publishes before looking at it any further.
func wake(t *testing.T, h *plugintest.Harness, now time.Time) *wire.TickOutput {
	t.Helper()
	in := h.TickInput(now)
	out, err := h.Tick(in)
	if err != nil {
		t.Fatalf("the wake-up failed: %v", err)
	}
	if err := plugintest.CheckSchedule(h.Manifest(), in, out); err != nil {
		t.Fatalf("filex would not accept this answer: %v", err)
	}
	return out
}

// itemFor finds the item for one key, or nil.
func itemFor(out *wire.TickOutput, key string) *wire.ScheduleItem {
	for i := range out.Items {
		if out.Items[i].Key == key {
			return &out.Items[i]
		}
	}
	return nil
}

// refFor is what filex does with an item's path when it mints the job:
// resolve the adapter-qualified spelling back to the file the plugin will
// be handed. ⚠ A test that hands the job a ref of its own instead would
// pass an item that names the WRONG DOCUMENT — the one mistake that would
// close somebody else's request.
func refFor(t *testing.T, f *host.Fake, path string) string {
	t.Helper()
	for ref, file := range f.Files {
		if file.Path == path {
			return ref
		}
	}
	t.Fatalf("the item names %q, which is no file on this host", path)
	return ""
}

func keysOf(out *wire.TickOutput) []string {
	var keys []string
	for _, it := range out.Items {
		keys = append(keys, it.Key)
	}
	return keys
}

// ── what a wake-up decides ─────────────────────────────────────────────

// The whole point of the feature, case by case: a request that runs out
// inside this hour is closed AT ITS OWN MINUTE, one that runs out later
// is left alone (and that is not an error — the wake-up whose window
// holds it will ask again), and a request that is over, or that cannot
// run out at all, asks for nothing.
func TestTick_SchedulesTheClosureAtTheMinuteTheRequestRunsOut(t *testing.T) {
	// The wake-up: 08:00, so the window is 08:00 → 09:00.
	now := at(21, 8, 0)

	for _, tc := range []struct {
		name string
		env  *envelope.Envelope
		want time.Time // zero = nothing scheduled for this request
	}{
		{name: "the link runs out inside the window: scheduled to the minute",
			env:  request("a1"),
			want: at(21, 9, 0)},
		{name: "the link runs out after the window: not scheduled, not an error",
			env: request("a2", func(e *envelope.Envelope) {
				e.Signers[0].PageExpires = envelope.Stamp(at(21, 11, 30))
			})},
		{name: "it ran out while nobody was awake: due in the past, so it runs at once",
			env: request("a3", func(e *envelope.Envelope) {
				e.Signers[0].PageExpires = envelope.Stamp(at(20, 23, 0))
			}),
			want: at(20, 23, 0)},
		{name: "the deadline is earlier than the link: the deadline decides",
			env: request("a4", func(e *envelope.Envelope) {
				// The deadline DAY counts, so a request due on the 20th is
				// over at midnight — before the 09:00 link.
				e.Options.Deadline = "2026-09-20"
			}),
			want: at(21, 0, 0)},
		{name: "the link is earlier than the deadline: the link decides",
			env: request("a5", func(e *envelope.Envelope) {
				e.Options.Deadline = "2026-09-30"
			}),
			want: at(21, 9, 0)},
		{name: "two links: the request is over when the LAST one dies",
			env: request("a6", func(e *envelope.Envelope) {
				e.Signers[1] = envelope.Signer{ID: "s2", Kind: envelope.KindExternal,
					Status: envelope.SignerNotified, Person: envelope.Person{Email: "veli@ornek.com"},
					PageToken: "tok-a6-2", PageExpires: envelope.Stamp(at(21, 8, 30))}
			}),
			want: at(21, 9, 0)},
		{name: "a signer who has finished holds nothing open",
			env: request("a7", func(e *envelope.Envelope) {
				e.Signers[0].Status = envelope.SignerSigned
				e.Signers[0].SignedAt = envelope.Stamp(day)
			})},
		{name: "a cancelled request schedules nothing",
			env: request("a8", func(e *envelope.Envelope) {
				e.Status = envelope.StatusCancelled
				e.ClosedAt = envelope.Stamp(day)
			})},
		{name: "a completed request schedules nothing",
			env: request("a9", func(e *envelope.Envelope) {
				e.Status = envelope.StatusCompleted
				e.ClosedAt = envelope.Stamp(day)
			})},
		{name: "a request that has already expired schedules nothing more",
			env: request("a10", func(e *envelope.Envelope) {
				e.Status = envelope.StatusExpired
				e.ClosedAt = envelope.Stamp(day)
			})},
		{name: "signers of this installation only: nothing runs out, so nothing is scheduled",
			env: request("a11", func(e *envelope.Envelope) {
				e.Signers = e.Signers[1:]
			})},
		{name: "a link stamp that cannot be read leaves the request open",
			env: request("a12", func(e *envelope.Envelope) {
				e.Signers[0].PageExpires = "yarın"
			})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, a, f := harness(t)
			seed(t, a, f, 1, tc.env)

			out := wake(t, h, now)

			it := itemFor(out, "expire:"+tc.env.ID)
			if tc.want.IsZero() {
				if it != nil {
					t.Fatalf("nothing should have been scheduled, got %+v", *it)
				}
				return
			}
			if it == nil {
				t.Fatalf("the request was not scheduled to close; answer: %v", keysOf(out))
			}
			if !it.DueAt.Equal(tc.want) {
				t.Errorf("due %s, want %s", it.DueAt.UTC().Format(time.RFC3339), tc.want.Format(time.RFC3339))
			}
			// ⚠ It must be the SAME door the person presses.
			if it.ActionID != ActionApply || it.Params["op"] != opExpire {
				t.Errorf("the item runs %q %v, not the app's own expire op", it.ActionID, it.Params)
			}
			if got := it.Paths; len(got) != 1 || got[0] != "docs://sozlesmeler/"+tc.env.Document {
				t.Errorf("the item names %v, not the document's adapter-qualified path", got)
			}
		})
	}
}

// ⚠ The key is an idempotency key: filex keeps at most one item per (app,
// key), so the SECOND wake-up must name the same key or the same closure
// is queued twice. A deadline that moved is the same envelope closing
// later — one item, moved — which is also why the key can never be the
// path: two names for one piece of work, and a file that was renamed
// between two hours would close twice.
func TestTick_TheKeyIsStableSoASecondWakeUpMovesTheItemRatherThanAddingOne(t *testing.T) {
	h, a, f := harness(t)
	env := request("b1")
	ref := seed(t, a, f, 1, env)

	first := wake(t, h, at(21, 8, 0))
	if len(first.Items) != 1 {
		t.Fatalf("one item expected, got %v", keysOf(first))
	}
	key := first.Items[0].Key
	if strings.Contains(key, "/") {
		t.Errorf("key %q carries a path; filex refuses a key outside [A-Za-z0-9_.:@-]", key)
	}
	if !strings.Contains(key, env.ID) {
		t.Errorf("key %q does not name the envelope, so next hour cannot recognise this work", key)
	}

	// The requester gives them another day and the wake-up comes round again.
	env.Signers[0].PageExpires = envelope.Stamp(at(22, 9, 0))
	if err := a.store(ref, env); err != nil {
		t.Fatal(err)
	}
	second := wake(t, h, at(22, 8, 0))
	if len(second.Items) != 1 {
		t.Fatalf("one item expected, got %v", keysOf(second))
	}
	if second.Items[0].Key != key {
		t.Fatalf("the key changed from %q to %q: filex would queue the closure twice", key, second.Items[0].Key)
	}
	if !second.Items[0].DueAt.Equal(at(22, 9, 0)) {
		t.Errorf("the item was not moved to the new deadline: %s", second.Items[0].DueAt.UTC())
	}
}

// Two requests, one wake-up: the answer carries one item per envelope and
// nothing is confused between them.
func TestTick_AnswersForEveryRequestItCanSee(t *testing.T) {
	h, a, f := harness(t)
	seed(t, a, f, 1, request("c1"))
	seed(t, a, f, 2, request("c2", func(e *envelope.Envelope) {
		e.Signers[0].PageExpires = envelope.Stamp(at(21, 8, 30))
	}))

	out := wake(t, h, at(21, 8, 0))

	if len(out.Items) != 2 {
		t.Fatalf("two requests, two items expected: %v", keysOf(out))
	}
	// Soonest first: if the answer is ever trimmed, it is trimmed from the
	// far end.
	if !out.Items[0].DueAt.Before(out.Items[1].DueAt) {
		t.Errorf("the items are not ordered by when they fall due: %s then %s",
			out.Items[0].DueAt.UTC(), out.Items[1].DueAt.UTC())
	}
	if out.Items[0].Paths[0] == out.Items[1].Paths[0] {
		t.Errorf("both items name the same document: %v", out.Items[0].Paths)
	}
}

// ⚠⚠ The refusal this test exists for is answered IN BAND: a wake-up that
// writes state does not trap, it gets `permission_denied` back and is free
// to ignore it. So "the items were right" says nothing about whether the
// call tried to write. host.Fake.TickScope refuses every write the real
// host refuses a tick and RECORDS each attempt; this asserts the list is
// empty, and that the records on disk are untouched afterwards.
func TestTick_WritesNothing(t *testing.T) {
	h, a, f := harness(t)
	env := request("d1")
	ref := seed(t, a, f, 1, env)
	before, _, _ := f.StateGet(ref, envelope.StateKey)
	notices, mails, outputs, shares := len(f.Notices), len(f.Mails), len(f.Outputs), len(f.Shares)

	f.TickScope()
	out := wake(t, h, at(21, 8, 0))

	if len(out.Items) != 1 {
		t.Fatalf("the wake-up should still decide with a read-only scope: %v", keysOf(out))
	}
	if len(f.Refused) != 0 {
		t.Fatalf("the wake-up tried to write: %v — decide in the tick, act in the action it schedules", f.Refused)
	}
	if after, _, _ := f.StateGet(ref, envelope.StateKey); after != before {
		t.Error("the record changed during a wake-up")
	}
	if len(f.Outputs) != outputs || len(f.Shares) != shares {
		t.Error("the wake-up created a file or a share")
	}
	// And it told nobody: the news of a closure belongs to the job that
	// performs it, or the same expiry is announced every hour until it runs.
	if len(f.Notices) != notices || len(f.Mails) != mails {
		t.Errorf("the wake-up sent word of its own accord: %d notice(s), %d mail(s)",
			len(f.Notices)-notices, len(f.Mails)-mails)
	}
}

// A wake-up with nothing due is the common case and must be cheap and
// quiet: no items, and a note that says so rather than nothing at all —
// the app's log is the only place anybody can see that the hour happened.
func TestTick_SaysSoWhenThereIsNothingToDo(t *testing.T) {
	h, a, f := harness(t)
	seed(t, a, f, 1, request("e1", func(e *envelope.Envelope) {
		e.Signers[0].PageExpires = envelope.Stamp(at(28, 9, 0))
	}))

	out := wake(t, h, at(21, 8, 0))

	if len(out.Items) != 0 {
		t.Fatalf("nothing is due this hour: %v", keysOf(out))
	}
	for _, lang := range []string{"en", "tr"} {
		if strings.TrimSpace(out.Note[lang]) == "" {
			t.Errorf("the note says nothing in %q: %v", lang, out.Note)
		}
	}
	// ⚠ And it says how many records it SAW. "Nothing was due" and "I could
	// see nothing at all" are the same sentence otherwise, and the second is
	// a real failure mode: a host that gives the wake-up no actor hands
	// `state_list` an empty answer for ever, with no error anywhere.
	if !strings.Contains(out.Note["en"], "1 open request") {
		t.Errorf("the note does not say how many requests the wake-up could see: %q", out.Note["en"])
	}
}

// ── the work itself ────────────────────────────────────────────────────

// The end of it: a request due at 09:00 is scheduled for 09:00, and the
// job filex mints at 09:00 closes the envelope, releases the file, revokes
// the link and tells BOTH SIDES — once each — with nobody present.
//
// ⚠ The job below is built from the ITEM, not from a screen: action,
// params and paths are the ones the wake-up named, run as SYSTEM (no
// actor, no locale), which is exactly what filex hands a scheduled job.
func TestTick_TheScheduledJobClosesItAndTellsBothSidesOnce(t *testing.T) {
	h, a, f := harness(t)
	s := walkRequestTimed(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)},
		map[string]any{"lock": true}, map[string]any{"deadline": "2026-09-21"})
	job := jobFrom(t, s, burak, docName)
	job.Params["lock"] = true
	if _, err := a.actionRequest(job); err != nil {
		t.Fatal(err)
	}
	env := loadEnv(t, a)
	if len(f.LockedRefs()) != 1 {
		t.Fatalf("the file should be frozen while signatures are collected: %v", f.LockedRefs())
	}
	notices, mails := len(f.Notices), len(f.Mails)

	// An hour before it runs out, filex wakes the app up.
	//
	// ⚠ It runs out when the sign-by day ENDS — midnight after the 21st —
	// not at 09:00 on the 21st. This test used to expect 09:00: the link
	// was cut to whole days rounded DOWN (1.6 days → 1), so it died at the
	// hour the request was sent, on the very day the requester said was
	// still open, and the request lapsed with it. ttlDays now rounds up to
	// the day's end and the deadline closes the request (request.go).
	lapses := at(22, 0, 0)
	out := wake(t, h, lapses.Add(-time.Hour))
	it := itemFor(out, "expire:"+env.ID)
	if it == nil {
		t.Fatalf("the lapsing request was not scheduled: %v", keysOf(out))
	}
	if !it.DueAt.Equal(lapses) {
		t.Fatalf("scheduled for %s, but the request runs out at %s", it.DueAt.UTC(), lapses)
	}
	// ⚠ And NOT before: nothing has happened yet.
	if after := loadEnv(t, a); after.Status != envelope.StatusSent {
		t.Fatalf("the wake-up closed the request an hour early: %s", after.Status)
	}
	if len(f.LockedRefs()) != 1 {
		t.Fatalf("the wake-up released the file an hour early: %v", f.LockedRefs())
	}

	// 09:00. filex runs the item as an ordinary job, with nobody behind it.
	f.Clock = it.DueAt
	res, err := a.actionApply(&wire.ActionRunInput{JobID: "sched-1", ActionID: it.ActionID,
		Params: it.Params,
		Inputs: []wire.FileRef{{Ref: refFor(t, f, it.Paths[0]), Name: docName, Size: 1000}}})
	if err != nil {
		t.Fatalf("the scheduled job failed: %v", err)
	}
	if !res.OK {
		t.Fatalf("the scheduled job reported a failure: %v", res.Message)
	}

	after := loadEnv(t, a)
	if after.Status != envelope.StatusExpired {
		t.Fatalf("the request should have closed, is %q", after.Status)
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
	if told := noticesTitled(f.Notices[notices:], "İmza isteğinin süresi doldu"); len(told) != 1 {
		t.Fatalf("the requester was not told exactly once: %+v", f.Notices[notices:])
	}
	told := noticesTitled(f.Notices[notices:], "Bir imza isteğinin süresi doldu")
	if len(told) != 1 || told[0].ToUserID != gokce.ID {
		t.Fatalf("the signer with an account was not told exactly once: %+v", f.Notices[notices:])
	}
	if got := mailsTo(f.Mails[mails:], "ali@ornek.com"); len(got) != 1 {
		t.Fatalf("the outside signer was not told exactly once: %+v", f.Mails[mails:])
	}

	// The next wake-up asks again, and a closed request is not work.
	if again := wake(t, h, at(22, 1, 0)); itemFor(again, "expire:"+env.ID) != nil {
		t.Error("a closed request was scheduled again")
	}
}

// ⚠ Somebody CAN finish the request in the hour between the wake-up and
// the minute it named. Pressed by a person, "already closed" is an answer;
// arriving as a scheduled job it is not a fault at all, and a red ops row
// would send an administrator hunting one. It also must not tell anybody
// the same news twice.
func TestTick_AScheduledClosureThatArrivesTooLateIsQuiet(t *testing.T) {
	_, a, f := harness(t)
	ref := seed(t, a, f, 1, request("g1"))
	if _, err := a.actionApply(&wire.ActionRunInput{ActionID: ActionApply, Actor: burak,
		Params: map[string]any{"op": "cancel"},
		Inputs: []wire.FileRef{{Ref: ref, Name: "g1.pdf"}}}); err != nil {
		t.Fatal(err)
	}
	notices, mails := len(f.Notices), len(f.Mails)

	res, err := a.actionApply(&wire.ActionRunInput{ActionID: ActionApply,
		Params: map[string]any{"op": opExpire, paramScheduled: true},
		Inputs: []wire.FileRef{{Ref: ref, Name: "g1.pdf"}}})
	if err != nil {
		t.Fatalf("the scheduled job failed: %v", err)
	}
	if !res.OK {
		t.Fatalf("a request somebody closed first is not a failed job: %v", res.Message)
	}
	if len(f.Notices) != notices || len(f.Mails) != mails {
		t.Errorf("the end was announced a second time: %d notice(s), %d mail(s)",
			len(f.Notices)-notices, len(f.Mails)-mails)
	}
	// The same arrival from a BUTTON still says what happened.
	byHand, err := a.actionApply(&wire.ActionRunInput{ActionID: ActionApply, Actor: burak,
		Params: map[string]any{"op": opExpire},
		Inputs: []wire.FileRef{{Ref: ref, Name: "g1.pdf"}}})
	if err != nil {
		t.Fatal(err)
	}
	if byHand.OK {
		t.Error("a person who pressed the button on a stale screen should be told it was already closed")
	}
}

// ── reminders ──────────────────────────────────────────────────────────

// "Remind every N days" used to fire only from a button, which made it a
// promise the app could not keep while nobody was looking. It rides the
// same mechanism now — with three refusals that keep an unattended
// reminder from becoming an hourly failure.
func TestTick_RemindsOnlyWhereAReminderCanLand(t *testing.T) {
	now := at(24, 8, 0)

	for _, tc := range []struct {
		name string
		env  *envelope.Envelope
		want map[string]time.Time // signer id → when, empty = nothing
	}{
		{name: "three days of silence: both signers are due",
			env: request("r1", func(e *envelope.Envelope) {
				e.Options.RemindEveryDays = 4
				e.Signers[0].NotifiedAt = envelope.Stamp(at(20, 8, 30))
				e.Signers[1].NotifiedAt = envelope.Stamp(at(20, 8, 30))
			}),
			want: map[string]time.Time{"s1": at(24, 8, 30), "s2": at(24, 8, 30)}},
		{name: "the reminder falls after this window: not yet",
			env: request("r2", func(e *envelope.Envelope) {
				e.Options.RemindEveryDays = 4
				e.Signers[0].NotifiedAt = envelope.Stamp(at(20, 15, 0))
				e.Signers[1].NotifiedAt = envelope.Stamp(at(20, 15, 0))
			})},
		{name: "due on the window's own boundary: still this hour's business",
			env: request("r2b", func(e *envelope.Envelope) {
				e.Options.RemindEveryDays = 4
				e.Signers[0].NotifiedAt = envelope.Stamp(at(20, 9, 0))
				e.Signers = e.Signers[:1]
			}),
			want: map[string]time.Time{"s1": at(24, 9, 0)}},
		{name: "the requester asked for no reminders",
			env: request("r3", func(e *envelope.Envelope) {
				e.Signers[0].NotifiedAt = envelope.Stamp(at(20, 8, 30))
			})},
		{name: "a signer who has signed is not nudged",
			env: request("r4", func(e *envelope.Envelope) {
				e.Options.RemindEveryDays = 4
				e.Signers[0].Status, e.Signers[0].SignedAt = envelope.SignerSigned, envelope.Stamp(day)
				e.Signers[1].Status = envelope.SignerDeclined
			})},
		{name: "⚠ no address to write to: a job that would fail every hour is never scheduled",
			env: request("r5", func(e *envelope.Envelope) {
				e.Options.RemindEveryDays = 4
				e.Signers[0].Person = envelope.Person{Name: "Elden Veren"}
				e.Signers[0].NotifiedAt = envelope.Stamp(at(20, 8, 30))
				e.Signers = e.Signers[:1]
			})},
		{name: "⚠ sequential: only the signer whose turn it is, because the machine refuses the rest",
			env: request("r6", func(e *envelope.Envelope) {
				e.Options.Order, e.Options.RemindEveryDays = envelope.OrderSequential, 4
				e.Signers[0].NotifiedAt = envelope.Stamp(at(20, 8, 30))
				e.Signers[1].NotifiedAt = envelope.Stamp(at(20, 8, 30))
			}),
			want: map[string]time.Time{"s1": at(24, 8, 30)}},
		{name: "the last reminder counts, not the invitation",
			env: request("r7", func(e *envelope.Envelope) {
				e.Options.RemindEveryDays = 4
				e.Signers[0].NotifiedAt = envelope.Stamp(at(18, 8, 0))
				e.Signers[0].RemindedAt = envelope.Stamp(at(20, 8, 45))
				e.Signers = e.Signers[:1]
			}),
			want: map[string]time.Time{"s1": at(24, 8, 45)}},
		{name: "a closed request reminds nobody",
			env: request("r8", func(e *envelope.Envelope) {
				e.Options.RemindEveryDays = 4
				e.Status, e.ClosedAt = envelope.StatusDeclined, envelope.Stamp(day)
				e.Signers[0].NotifiedAt = envelope.Stamp(at(20, 8, 30))
			})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, a, f := harness(t)
			// Far enough out that the links do not confuse the reading.
			tc.env.Signers[0].PageExpires = envelope.Stamp(at(30, 9, 0))
			seed(t, a, f, 1, tc.env)

			out := wake(t, h, now)

			for _, sg := range tc.env.Signers {
				key := "remind:" + tc.env.ID + ":" + sg.ID
				it, want := itemFor(out, key), tc.want[sg.ID]
				switch {
				case want.IsZero() && it != nil:
					t.Errorf("%s should not have been reminded: %+v", sg.ID, *it)
				case want.IsZero():
				case it == nil:
					t.Errorf("%s was not reminded; answer: %v", sg.ID, keysOf(out))
				default:
					if !it.DueAt.Equal(want) {
						t.Errorf("%s due %s, want %s", sg.ID, it.DueAt.UTC(), want)
					}
					if it.Params["op"] != opRemind || it.Params["signer_id"] != sg.ID {
						t.Errorf("%s: the item runs %v, not this app's remind op for this signer", sg.ID, it.Params)
					}
				}
			}
		})
	}
}

// The scheduled reminder is the same op the button queues, and once it
// lands the wake-up MOVES it a whole interval on rather than repeating it
// the next hour — which is what keeps "every 4 days" meaning every 4 days.
func TestTick_AReminderThatLandedIsMovedOnAWholeInterval(t *testing.T) {
	h, a, f := harness(t)
	env := request("s1", func(e *envelope.Envelope) {
		e.Options.RemindEveryDays = 4
		e.Signers[0].NotifiedAt = envelope.Stamp(at(20, 8, 30))
		e.Signers[0].PageExpires = envelope.Stamp(at(30, 9, 0))
		e.Signers = e.Signers[:1]
	})
	seed(t, a, f, 1, env)

	out := wake(t, h, at(24, 8, 0))
	it := itemFor(out, "remind:s1:s1")
	if it == nil {
		t.Fatalf("no reminder was scheduled: %v", keysOf(out))
	}

	f.Clock = it.DueAt
	mails := len(f.Mails)
	res, err := a.actionApply(&wire.ActionRunInput{ActionID: it.ActionID, Params: it.Params,
		Inputs: []wire.FileRef{{Ref: refFor(t, f, it.Paths[0]), Name: env.Document}}})
	if err != nil {
		t.Fatalf("the scheduled reminder failed: %v", err)
	}
	if !res.OK {
		t.Fatalf("the scheduled reminder reported a failure: %v", res.Message)
	}
	if got := mailsTo(f.Mails[mails:], "ali@ornek.com"); len(got) != 1 {
		t.Fatalf("the signer was not reminded exactly once: %+v", f.Mails[mails:])
	}

	next := wake(t, h, f.Clock.Add(time.Hour))
	if again := itemFor(next, "remind:s1:s1"); again != nil && again.DueAt.Before(f.Clock.AddDate(0, 0, 4)) {
		t.Errorf("the reminder was not moved a full interval on: %s", again.DueAt.UTC())
	}
}

// ── bounds ─────────────────────────────────────────────────────────────

// More work than one wake-up may carry: the answer is trimmed from the
// FAR end, because the extras are dropped by the host and the next hour
// is a whole hour away. What survives must be what was closest to running
// out.
func TestTick_KeepsTheMostUrgentWhenThereIsMoreThanItMayCarry(t *testing.T) {
	h, a, f := harness(t)
	for i := 0; i < 8; i++ {
		seed(t, a, f, i+1, request(fmt.Sprintf("m%d", i), func(e *envelope.Envelope) {
			e.Signers[0].PageExpires = envelope.Stamp(at(21, 8, 10+i))
		}))
	}
	in := h.TickInput(at(21, 8, 0))
	in.MaxItems = 3

	out, err := h.Tick(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := plugintest.CheckSchedule(h.Manifest(), in, out); err != nil {
		t.Fatalf("filex would not accept this answer: %v", err)
	}
	if len(out.Items) != 3 {
		t.Fatalf("the answer carries %d items, over the %d filex would keep", len(out.Items), in.MaxItems)
	}
	for i, want := range []string{"expire:m0", "expire:m1", "expire:m2"} {
		if out.Items[i].Key != want {
			t.Errorf("item %d is %q, want the more urgent %q", i, out.Items[i].Key, want)
		}
	}
}

// ⚠ The panel's "nobody has moved" hint and the scheduled reminder are
// ONE piece of arithmetic now (remindAt). This is what keeps them one: a
// screen that offers a reminder the schedule would never send, or a
// schedule that sends one the screen says is not due, is two answers to
// one question.
func TestTick_ThePanelHintAndTheScheduleAgree(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  *envelope.Envelope
		want bool
	}{
		{name: "a signer filex can write to, silent for long enough", want: true,
			env: request("p1", func(e *envelope.Envelope) {
				e.Options.RemindEveryDays = 4
				e.Signers[0].NotifiedAt = envelope.Stamp(at(20, 8, 30))
				e.Signers = e.Signers[:1]
			})},
		{name: "a signer with no address at all", want: false,
			env: request("p2", func(e *envelope.Envelope) {
				e.Options.RemindEveryDays = 4
				e.Signers[0].Person = envelope.Person{Name: "Elden Veren"}
				e.Signers[0].NotifiedAt = envelope.Stamp(at(20, 8, 30))
				e.Signers = e.Signers[:1]
			})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, a, f := harness(t)
			tc.env.Signers[0].PageExpires = envelope.Stamp(at(30, 9, 0))
			seed(t, a, f, 1, tc.env)
			f.Clock = at(24, 8, 30)

			hinted := a.remindDue(tc.env)
			scheduled := itemFor(wake(t, h, at(24, 8, 0)), "remind:"+tc.env.ID+":s1") != nil

			if hinted != tc.want || scheduled != tc.want {
				t.Errorf("the panel says %v and the wake-up says %v; both should say %v", hinted, scheduled, tc.want)
			}
		})
	}
}

// Two pieces of work on the same minute: the closure goes first. If the
// answer is ever trimmed, the request that ENDS beats the one that only
// wanted a nudge.
func TestTick_AClosureOutranksANudgeOnTheSameMinute(t *testing.T) {
	h, a, f := harness(t)
	seed(t, a, f, 1, request("t1", func(e *envelope.Envelope) {
		e.Options.RemindEveryDays = 1
		e.Signers[0].NotifiedAt = envelope.Stamp(at(20, 8, 30))
		e.Signers[0].PageExpires = envelope.Stamp(at(21, 8, 30))
		e.Signers = e.Signers[:1]
	}))

	out := wake(t, h, at(21, 8, 0))

	if got := keysOf(out); len(got) != 2 || got[0] != "expire:t1" {
		t.Fatalf("the closure should come first, got %v", got)
	}
	if !out.Items[0].DueAt.Equal(out.Items[1].DueAt) {
		t.Fatalf("this case is only about the tie: %s vs %s", out.Items[0].DueAt, out.Items[1].DueAt)
	}
}
