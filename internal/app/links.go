package app

// A signing link ended OUTSIDE this app.
//
// A signing link is a real filex share, so a person can end it from the
// Shares screen — the requester under My shares, an administrator under
// Admin → Shares (Revoke, or Delete). Until this release that stopped the
// link and nothing else: the request it belonged to stayed "Sent · 0/1
// signed", the file stayed frozen, the requester waited for a signature
// that could never come (measured on a live instance, 2026-09-21: revoke,
// then a wake-up that saw the request and scheduled nothing).
//
// ⚠⚠ filex does not tell an app that its link was ended, and it should not
// have to: the link's facts are the app's to ASK for (share_state → expires
// and whether it still opens), and the app already asks the host questions
// every hour, unattended — the `tick` wake-up. So the wake-up asks after
// every open link, and a link that stopped EARLIER than the day filex gave
// it at share_create (a revoke moves expires_at to the moment of the revoke)
// or that is gone altogether (a delete) is a link a person ended. One
// mechanism, the wake-up; one way of learning, asking. The host only brings
// the wake-up forward when a person ends one of an app's links (filex:
// Registry.WakeSoon), so the answer comes in seconds instead of within the
// hour.
//
// What the request then does is what it does when a signer refuses: that
// signer can no longer sign, so the request cannot finish as asked — it
// closes (cancelled, with an event saying why), the other links are
// revoked, the file is released, the requester is told and so is every
// other signer still holding a link.
//
// ⚠ A link that ran out ON its day is not "ended": that is lapseAt's
// business (tick.go) and closes as expired. And a link whose facts cannot
// be read is left alone — a request is never closed on a guess.

import (
	"errors"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/views"
)

// opLinkEnded is the op a scheduled item (and nobody else) queues.
const opLinkEnded = "link_ended"

// endedEarlySlack is how much earlier than the recorded day a link has to
// stop before it counts as ended by somebody. Stamps travel as RFC 3339
// with the database's own precision; a minute is far above that and far
// below anything a person does.
const endedEarlySlack = time.Minute

// linkEnd is one signer whose link somebody ended.
type linkEnd struct {
	Signer  string
	Deleted bool // gone altogether (an administrator's Delete), not revoked
}

// endedLinks asks the host about every link a waiting signer still holds
// and answers the ones a person ended.
//
// ⚠ Never from a signer's PAGE: inside a page call filex answers share_state
// about the link being visited, whatever token is named (host.Host
// ShareInfo). Jobs, views and the wake-up get the link that was asked about.
func (a *App) endedLinks(env *envelope.Envelope) []linkEnd {
	if env == nil || env.Status.Closed() {
		return nil
	}
	var out []linkEnd
	for i := range env.Signers {
		sg := &env.Signers[i]
		if sg.Done() || sg.PageToken == "" {
			continue
		}
		facts, err := a.H.ShareInfo(sg.PageToken)
		switch {
		case pluginkit.IsNotFound(err):
			out = append(out, linkEnd{Signer: sg.ID, Deleted: true})
		case err != nil:
			a.logf("warn", "reading the link of %s: %v", sg.Person.Identity(), err)
		case facts.Revoked && endedEarly(facts.ExpiresAt, sg.PageExpires):
			out = append(out, linkEnd{Signer: sg.ID})
		}
	}
	return out
}

// endedEarly reports whether a link stopped before the day it was given.
func endedEarly(actual *time.Time, recorded string) bool {
	if actual == nil || recorded == "" {
		return false
	}
	given, err := parseStamp(recorded)
	if err != nil {
		return false // cannot tell: leave the request as it is
	}
	return actual.Before(given.Add(-endedEarlySlack))
}

// closeEnded closes the request for the first link a person ended, tells
// everybody and stores the result. The same closure for the scheduled job
// and for the sweep every other job runs first.
func (a *App) closeEnded(ref string, env *envelope.Envelope, end linkEnd) (*envelope.Envelope, error) {
	note := "revoked"
	if end.Deleted {
		note = "deleted"
	}
	next, effects, err := envelope.Apply(*env, envelope.Input{Type: envelope.EvLinkEnded, Signer: end.Signer, At: a.H.Now(), Note: note})
	if err != nil {
		return nil, err
	}
	a.runEffects(ref, &next, effects)
	a.tellWithdrawn(ref, &next, end.Signer)
	if err := a.store(ref, &next); err != nil {
		return nil, err
	}
	return &next, nil
}

// sweepEndedLinks is the belt beside the wake-up: every job asks, so an
// installation whose wake-up is off (a demo) still closes the request the
// first time anybody comes near it. A sweep never fails the job it runs in.
func (a *App) sweepEndedLinks(ref string, env *envelope.Envelope) *envelope.Envelope {
	ends := a.endedLinks(env)
	if len(ends) == 0 {
		return env
	}
	next, err := a.closeEnded(ref, env, ends[0])
	if err != nil {
		a.logf("warn", "closing a request whose link was ended: %v", err)
		return env
	}
	return next
}

// opLinkEnded is the scheduled closure. It asks again rather than trusting
// the wake-up: a link the requester re-opened, or a request that finished,
// in the seconds between the two is not closed by a stale answer.
func (a *App) opLinkEnded(ref string, env *envelope.Envelope, scheduled bool) (*wire.ActionRunOutput, error) {
	ends := a.endedLinks(env)
	if len(ends) == 0 {
		if scheduled {
			return &wire.ActionRunOutput{OK: true, Message: views.T(
				"Nothing to close: every signing link of the request still works, or it had already ended.",
				"Kapatılacak bir şey yok: isteğin bütün imza bağlantıları çalışıyor ya da istek zaten sonlanmıştı.")}, nil
		}
		return fail("Every signing link of this request still works.", "Bu isteğin bütün imza bağlantıları çalışıyor.")
	}
	next, err := a.closeEnded(ref, env, ends[0])
	if err != nil {
		if errors.Is(err, envelope.ErrClosed) && scheduled {
			return &wire.ActionRunOutput{OK: true, Message: views.T(
				"Nothing to close: the request had already ended.",
				"Kapatılacak bir şey yok: istek zaten sonlanmıştı.")}, nil
		}
		return nil, err
	}
	who := "-"
	if sg := next.Signer(ends[0].Signer); sg != nil {
		who = sg.Person.Identity()
	}
	return &wire.ActionRunOutput{OK: true, Message: views.Tf(
		"The signing link for %s was ended from the Shares screen, so the request was closed and the document released.",
		"%s için imza bağlantısı Paylaşımlar ekranından sonlandırıldı; bu yüzden istek kapatıldı ve belge serbest bırakıldı.", who)}, nil
}

// tellWithdrawn tells the OTHER signers still holding a link that the
// request is over — the closure has just revoked their links, and a dead
// page with no explanation is what they would otherwise find. The signer
// whose link a person ended is not written to: that link was ended on
// purpose, perhaps because it went to the wrong address.
//
// ⚠ The rule the invitation, the reminder and the receipt follow holds here
// too: somebody with an account is told in filex, never mailed.
func (a *App) tellWithdrawn(ref string, env *envelope.Envelope, endedSigner string) {
	for i := range env.Signers {
		sg := &env.Signers[i]
		if sg.ID == endedSigner || sg.Status == envelope.SignerSigned || sg.Status == envelope.SignerDeclined {
			continue
		}
		switch {
		case sg.Internal():
			title, body := withdrawnNotice(env)
			if _, err := a.H.NotifySend(pluginkit.Notice{
				Title: title, Body: body, Severity: "warning", ToUserID: sg.Person.UserID,
				Meta:   map[string]any{"envelope": env.ID, "document": env.Document},
				Target: &pluginkit.NoticeTarget{Ref: ref},
			}); err != nil {
				a.logf("warn", "withdrawal notice to %s: %v", sg.Person.Identity(), err)
			}
		case sg.Person.HasEmail():
			subject, body := withdrawnMail(env.Options.Locale, env, sg)
			if err := a.H.MailSend(sg.Person.Email, subject, body); err != nil {
				a.logf("warn", "withdrawal mail to %s: %v", sg.Person.Email, err)
			}
		}
	}
}
