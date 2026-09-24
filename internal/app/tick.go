package app

// The hourly wake-up: the one call filex makes with nobody present.
//
// A signature request that lapses at 03:00 has to close at 03:00. Until
// filex grew a schedule this plugin could only close it when somebody
// happened to touch the document (see sweepExpired, which is still the
// belt), so a request that ran out overnight sat open — links dead, file
// frozen, neither side told — until morning. The `schedule` permission
// hands the plugin an hourly question instead: WHAT do you want done, and
// WHEN? It answers with items, and filex runs each one at the minute it
// named, as an ordinary job.
//
// ⚠⚠ THE WAKE-UP DECIDES, THE ACTION ACTS. `tick` runs with a screen's
// scope: it may read settings and its own state, look people up, notify
// and mail — it may NOT write state, write files, take locks, open shares
// or sign. Every one of those refusals is answered in band, so a tick
// that tries to write does not crash; it quietly does nothing. That is
// why the work below is named, never done, and why `TestTick_WritesNothing`
// runs the whole wake-up against a host that refuses every write and
// proves not one was even attempted.
//
// ⚠ And the work it names is the work the BUTTON runs: the scheduled item
// starts `apply` with `op: expire` — the same op the details panel's
// "Close the expired request" queues, and the same op sweepExpired's
// closeExpired performs. One ending, one set of notices, three doors.

import (
	"sort"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/views"
)

// tickListLimit is how many open requests one wake-up looks at. The host
// allows 500; this is a wake-up, not a report, and a request past the
// limit is not lost — it is closed by the belt the first time somebody
// touches it, and by the next wake-up once the busier ones are done.
const tickListLimit = 200

// Ops a scheduled item asks for. They are the SAME op strings the screens
// queue, because a second door would be a second behaviour.
const (
	opExpire = "expire"
	opRemind = "remind"
	// paramScheduled marks a job nobody started. It changes nothing about
	// what the job does; it only softens what the job SAYS when it finds
	// the work already done (see opExpire).
	paramScheduled = "scheduled"
)

// Tick answers the hourly wake-up with the work that falls due inside
// this window: requests to close at their deadline, and the reminders the
// requester asked for. Work due after the window is deliberately NOT
// named — the wake-up whose window contains it asks again, by which time
// the record may have moved.
func (a *App) Tick(in *wire.TickInput) (*wire.TickOutput, error) {
	rows, err := a.H.StateList(envelope.StateKey, tickListLimit)
	if err != nil {
		// The hour is lost, the next one asks again. Saying so out loud
		// is the whole difference between "nothing was due" and "I could
		// not see".
		return nil, err
	}

	var due []wire.ScheduleItem
	seen, beyond, closing, nudging, ended := 0, 0, 0, 0, 0
	for _, row := range rows {
		if row.Path == "" {
			continue // a state row the host cannot place on a file yet
		}
		env, err := envelope.Decode(row.Value)
		if err != nil {
			a.logf("warn", "unreadable request on %s: %v", row.Path, err)
			continue
		}
		seen++
		// ⚠ A link a person ended on the Shares screen (links.go). Nothing
		// tells the app; this is where it asks. The closure is due NOW and
		// it is the only thing named for this request: a closure and a
		// reminder in the same hour would be a reminder about a request
		// that no longer exists.
		if ends := a.endedLinks(env); len(ends) > 0 {
			ended++
			due = append(due, wire.ScheduleItem{
				Key: "ended:" + env.ID, DueAt: in.WindowStart, ActionID: ActionApply,
				Paths: []string{row.Path}, Params: map[string]any{"op": opLinkEnded, paramScheduled: true},
			})
			continue
		}
		for _, w := range wakeups(env) {
			if w.At.After(in.WindowEnd) {
				beyond++
				continue
			}
			switch w.Op {
			case opExpire:
				closing++
			case opRemind:
				nudging++
			}
			due = append(due, wire.ScheduleItem{
				Key: w.Key, DueAt: w.At, ActionID: ActionApply,
				Paths: []string{row.Path}, Params: w.Params(),
			})
		}
	}

	// Soonest first: if the answer is trimmed it is trimmed from the far
	// end, and what survives is what mattered most. On the same minute the
	// key decides — which puts "expire:…" before "remind:…", a closure
	// before a nudge, and makes two wake-ups order the same work the same
	// way.
	sort.SliceStable(due, func(i, j int) bool {
		if !due[i].DueAt.Equal(due[j].DueAt) {
			return due[i].DueAt.Before(due[j].DueAt)
		}
		return due[i].Key < due[j].Key
	})
	max := in.MaxItems
	if max <= 0 {
		max = wire.ScheduleMaxItems
	}
	dropped := 0
	if len(due) > max {
		dropped = len(due) - max
		due = due[:max]
	}

	return &wire.TickOutput{Items: due, Note: tickNote(seen, closing, nudging, ended, beyond, dropped)}, nil
}

// wakeup is one piece of work a single request wants run at a named
// minute. It is a plain value on purpose: deciding is a pure function of
// the record, which is what lets the table test below name a dozen cases
// without a host at all.
type wakeup struct {
	Key    string
	At     time.Time
	Op     string
	Signer string
}

// Params is what the job is started with.
func (w wakeup) Params() map[string]any {
	p := map[string]any{"op": w.Op, paramScheduled: true}
	switch w.Op {
	case opExpire:
		p["reason"] = "lapsed"
	case opRemind:
		p["signer_id"] = w.Signer
	}
	return p
}

// wakeups is everything one request needs done unattended, and when.
//
// ⚠ The key is an IDEMPOTENCY key, not a name: filex keeps at most one
// item per (app, key), so naming the same key again MOVES that item. That
// is exactly what a deadline extended by a day, or a reminder that has
// just gone out, should do — and it is why the key is the envelope (and
// the signer), never the path. A path has slashes in it and would be
// refused; worse, it would make the same envelope look like two different
// pieces of work after somebody moved the file.
func wakeups(env *envelope.Envelope) []wakeup {
	if env == nil || env.Status.Closed() {
		return nil // nothing more will happen to it
	}
	var out []wakeup
	if at := lapseAt(env); !at.IsZero() {
		out = append(out, wakeup{Key: "expire:" + env.ID, At: at, Op: opExpire})
	}
	for i := range env.Signers {
		sg := &env.Signers[i]
		if at := remindAt(env, sg); !at.IsZero() {
			out = append(out, wakeup{Key: "remind:" + env.ID + ":" + sg.ID, At: at, Op: opRemind, Signer: sg.ID})
		}
	}
	return out
}

// lapseAt is the instant a request runs out: the earlier of the day the
// requester asked for it by and the moment the last open signing link
// dies. Zero means it does not run out by itself — an internal-only
// request with no deadline waits as long as it takes.
//
// ⚠ It is the ONE definition of "over". `expired` asks it whether the
// moment has passed, and the wake-up asks it WHEN the moment is, so the
// button a person presses and the job nobody watches cannot drift apart.
func lapseAt(env *envelope.Envelope) time.Time {
	var at time.Time
	earlier := func(t time.Time) {
		if !t.IsZero() && (at.IsZero() || t.Before(at)) {
			at = t
		}
	}
	if env.Options.Deadline != "" {
		if t, ok := fields.ParseDate(env.Options.Deadline, fields.DateYMD); ok {
			// The deadline day itself counts, so the request is over at
			// the END of it — a request due on the 21st is live all day
			// on the 21st and over at midnight.
			earlier(t.AddDate(0, 0, 1))
		}
	}
	// The links: the request is only over when EVERY open one has run
	// out, so the moment is the last of them. A stamp that cannot be read
	// leaves the request open rather than closing it early — the reverse
	// of that mistake destroys a live request.
	var last time.Time
	open := 0
	for _, s := range env.Signers {
		if s.Done() || s.PageExpires == "" {
			continue
		}
		open++
		t, err := parseStamp(s.PageExpires)
		if err != nil {
			return at
		}
		if t.After(last) {
			last = t
		}
	}
	if open > 0 {
		earlier(last)
	}
	return at
}

// remindAt is when this signer should be nudged again: the reminder
// interval after the last thing that happened to them. Zero means never —
// and the reasons it says never are what keeps an unattended reminder
// from becoming an hourly failure:
//
//   - the requester asked for no reminders, or the signer is finished;
//   - it is not their turn yet in a sequential request, where the machine
//     itself would refuse the event;
//   - ⚠ there is no way to reach them. `opRemind` answers "no address for
//     filex to write to" for a signer whose link the requester passes on
//     by hand — pressed by a person that is an answer, scheduled it would
//     be a job that fails every hour until the request lapses.
func remindAt(env *envelope.Envelope, sg *envelope.Signer) time.Time {
	if env.Options.RemindEveryDays <= 0 || sg.Done() || !env.Turn(sg.ID) {
		return time.Time{}
	}
	if !sg.Internal() && !sg.Person.HasEmail() {
		return time.Time{}
	}
	t, err := parseStamp(lastTouch(env, sg))
	if err != nil {
		return time.Time{}
	}
	return t.AddDate(0, 0, env.Options.RemindEveryDays)
}

// lastTouch is when this signer was last moved: reminded, else seen, else
// invited, else the day the request went out.
func lastTouch(env *envelope.Envelope, sg *envelope.Signer) string {
	for _, when := range []string{sg.RemindedAt, sg.ViewedAt, sg.NotifiedAt, env.CreatedAt} {
		if when != "" {
			return when
		}
	}
	return ""
}

// tickNote is the line this wake-up leaves in the app's log in the admin
// panel. Nobody is watching the run, so the log IS the run: an hour that
// decided nothing says so, and an hour that had to drop work says how
// much.
func tickNote(seen, closing, nudging, ended, beyond, dropped int) wire.Text {
	return views.Each(func(l views.Lang) string {
		var parts []string
		if closing > 0 {
			parts = append(parts, l.Sf("%d closing", "%d kapanıyor", closing))
		}
		if nudging > 0 {
			parts = append(parts, l.Sf("%d reminder(s)", "%d hatırlatma", nudging))
		}
		if ended > 0 {
			parts = append(parts, l.Sf("%d closing (a link was ended on the Shares screen)", "%d kapanıyor (bir bağlantı Paylaşımlar ekranında sonlandırıldı)", ended))
		}
		if beyond > 0 {
			parts = append(parts, l.Sf("%d beyond this window", "%d bu pencerenin ötesinde", beyond))
		}
		if dropped > 0 {
			parts = append(parts, l.Sf("%d dropped", "%d düşürüldü", dropped))
		}
		// ⚠ The count of records SEEN is in the line on purpose. "Nothing due"
		// and "I could not see anything" look identical from outside, and the
		// second is exactly what a host that hands `tick` no actor produces:
		// `state_list` filters every row through the asking person's
		// permissions, and there is no person. Without this number an app
		// whose wake-up can see nothing at all reads as an app with nothing
		// to do — for ever, with no error anywhere.
		where := l.Sf(" (%d open request(s) seen)", " (%d açık istek görüldü)", seen)
		if len(parts) == 0 {
			return l.S("nothing due", "zamanı gelen iş yok") + where
		}
		return strings.Join(parts, ", ") + where
	})
}
