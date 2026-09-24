package envelope

import (
	"errors"
	"fmt"
	"time"
)

// ── The state machine ──────────────────────────────────────────────────
//
// Apply(env, event) → (env', effects, error). It never touches the host:
// the caller (internal/app) performs the effects — revoking pages,
// lifting the file lock, raising notifications, inviting whoever is next
// in a sequential request — and stores env'. Keeping it pure is what
// makes the transitions testable as a table, and it is why no exit of
// the flow can forget the lock: every path that closes the envelope
// emits the same unlock effect.

// Input events.
const (
	EvNotified  = "notified"  // the invitation went out (signer)
	EvViewed    = "viewed"    // the signer opened their screen (signer, ip)
	EvSigned    = "signed"    // the signer's submission was applied (signer, ip, fields)
	EvReminded  = "reminded"  // the requester sent a reminder (signer)
	EvDeclined  = "declined"  // the signer refused (signer, note = reason)
	EvCancelled = "cancelled" // the requester cancelled
	EvExpired   = "expired"   // the links expired
	// EvLinkEnded: a signer's link was ended OUTSIDE this app — revoked or
	// deleted on filex's Shares screen (signer; note = "revoked" |
	// "deleted"). That signer can no longer sign, so the request cannot
	// finish as asked and it closes as cancelled, with this event saying
	// why. The app learns of it by asking (tick.go, endedLinks); filex
	// sends it nothing.
	EvLinkEnded = "link_ended"
)

// Input is one event to apply.
type Input struct {
	Type   string
	Signer string
	IP     string
	At     time.Time
	// Fields consumed by a Signed event (ids + values for typed fields),
	// recorded on the envelope.
	Fields []Field
	// Cert facts recorded on a Signed event.
	CertSerial  string
	CertExpires string
	Note        string
}

// Effect is something the caller must do after a transition.
type Effect struct {
	Kind   string // revoke_page | notify_requester | unlock_file | invite
	Token  string // revoke_page
	Signer string
	// notify_requester: which notice (viewed | signer_signed | completed |
	// cancelled | expired | declined | link_ended)
	Notice string
}

const (
	EffectRevokePage      = "revoke_page"
	EffectNotifyRequester = "notify_requester"
	EffectUnlockFile      = "unlock_file"
	EffectInvite          = "invite"
)

// ErrClosed is returned for events that need an open envelope.
var ErrClosed = errors.New("envelope: already closed")

// ErrNotYourTurn is returned when a sequential request is waiting for
// somebody earlier in the list.
var ErrNotYourTurn = errors.New("envelope: it is not this signer's turn yet")

// Apply runs one transition on a copy of env.
func Apply(env Envelope, in Input) (Envelope, []Effect, error) {
	e := clone(env)
	at := in.At
	if at.IsZero() {
		at = time.Now()
	}
	stamp := Stamp(at)
	var effects []Effect
	record := func(t, note string) {
		e.Events = append(e.Events, Event{At: stamp, Type: t, Signer: in.Signer, Note: note, IP: in.IP})
		if len(e.Events) > MaxEvents {
			e.Events = e.Events[len(e.Events)-MaxEvents:]
		}
		e.UpdatedAt = stamp
	}
	// finish is every ending of the flow: the pages go, the lock goes, the
	// requester hears about it. Nothing may close an envelope without it.
	finish := func(status Status, notice string) {
		for _, tok := range e.OpenPageTokens() {
			effects = append(effects, Effect{Kind: EffectRevokePage, Token: tok})
		}
		for i := range e.Signers {
			if !e.Signers[i].Done() {
				e.Signers[i].Status = SignerVoid
			}
		}
		e.Status = status
		e.ClosedAt = stamp
		if e.Locked {
			effects = append(effects, Effect{Kind: EffectUnlockFile})
		}
		effects = append(effects, Effect{Kind: EffectNotifyRequester, Notice: notice, Signer: in.Signer})
	}

	switch in.Type {
	case EvNotified, EvViewed, EvReminded:
		if e.Status.Closed() {
			return env, nil, ErrClosed
		}
		s := e.Signer(in.Signer)
		if s == nil {
			return env, nil, fmt.Errorf("envelope: unknown signer %q", in.Signer)
		}
		if s.Status == SignerSigned {
			return env, nil, fmt.Errorf("envelope: %s already signed", s.Person.Display())
		}
		switch in.Type {
		case EvNotified:
			if s.Status == SignerPending {
				s.Status = SignerNotified
			}
			s.NotifiedAt = stamp
		case EvViewed:
			if s.Status == SignerPending || s.Status == SignerNotified {
				s.Status = SignerViewed
			}
			if s.ViewedAt == "" {
				s.ViewedAt = stamp
				effects = append(effects, Effect{Kind: EffectNotifyRequester, Notice: "viewed", Signer: s.ID})
			}
		case EvReminded:
			s.RemindedAt = stamp
			if s.Status == SignerPending {
				s.Status = SignerNotified
			}
		}
		record(in.Type, in.Note)
		return e, effects, nil

	case EvSigned:
		if e.Status.Closed() {
			return env, nil, ErrClosed
		}
		s := e.Signer(in.Signer)
		if s == nil {
			return env, nil, fmt.Errorf("envelope: unknown signer %q", in.Signer)
		}
		if s.Status == SignerSigned {
			return env, nil, fmt.Errorf("envelope: %s already signed", s.Person.Display())
		}
		if !e.Turn(s.ID) {
			return env, nil, ErrNotYourTurn
		}
		s.Status = SignerSigned
		s.SignedAt = stamp
		s.SignedIP = in.IP
		s.CertSerial = in.CertSerial
		s.CertExpires = in.CertExpires
		for _, filled := range in.Fields {
			for i := range e.Fields {
				if e.Fields[i].ID == filled.ID {
					e.Fields[i].SignedBy = s.ID
					if !isDrawn(e.Fields[i].Type) {
						e.Fields[i].Value = filled.Value
					}
				}
			}
		}
		e.SignCount++
		if s.PageToken != "" {
			effects = append(effects, Effect{Kind: EffectRevokePage, Token: s.PageToken, Signer: s.ID})
		}
		signed, total := e.Progress()
		if signed == total {
			record(in.Type, in.Note)
			record("completed", "")
			finish(StatusCompleted, "completed")
			return e, effects, nil
		}
		e.Status = StatusInProgress
		record(in.Type, in.Note)
		effects = append(effects, Effect{Kind: EffectNotifyRequester, Notice: "signer_signed", Signer: s.ID})
		// In a sequential request the next person only hears about it now.
		if e.Sequential() {
			if next := e.Next(); next != nil {
				effects = append(effects, Effect{Kind: EffectInvite, Signer: next.ID})
			}
		}
		return e, effects, nil

	case EvDeclined:
		if e.Status.Closed() {
			return env, nil, ErrClosed
		}
		s := e.Signer(in.Signer)
		if s == nil {
			return env, nil, fmt.Errorf("envelope: unknown signer %q", in.Signer)
		}
		if s.Status == SignerSigned {
			return env, nil, fmt.Errorf("envelope: %s already signed", s.Person.Display())
		}
		s.Status = SignerDeclined
		s.DeclinedAt = stamp
		s.DeclineReason = in.Note
		record(in.Type, in.Note)
		finish(StatusDeclined, "declined")
		return e, effects, nil

	case EvCancelled, EvExpired:
		if e.Status.Closed() {
			return env, nil, ErrClosed
		}
		record(in.Type, in.Note)
		if in.Type == EvCancelled {
			finish(StatusCancelled, "cancelled")
		} else {
			finish(StatusExpired, "expired")
		}
		return e, effects, nil

	case EvLinkEnded:
		if e.Status.Closed() {
			return env, nil, ErrClosed
		}
		s := e.Signer(in.Signer)
		if s == nil {
			return env, nil, fmt.Errorf("envelope: unknown signer %q", in.Signer)
		}
		if s.Done() {
			// Their link ending is the app's own doing (a signature revokes
			// it) or already accounted for: nothing is lost, nothing closes.
			return env, nil, fmt.Errorf("envelope: %s is not waiting to sign", s.Person.Display())
		}
		record(in.Type, in.Note)
		// ⚠ Cancelled, not a status of its own: the request ended before it
		// was finished and nobody refused, which is what "cancelled" says
		// everywhere this app shows a status. The event above is what says
		// WHY, and the panel, the notice and the audit trail read it.
		finish(StatusCancelled, "link_ended")
		return e, effects, nil
	}
	return env, nil, fmt.Errorf("envelope: unknown event %q", in.Type)
}

func isDrawn(t string) bool { return t == "signature" || t == "initials" }

func clone(e Envelope) Envelope {
	c := e
	c.Signers = append([]Signer(nil), e.Signers...)
	c.Fields = append([]Field(nil), e.Fields...)
	c.Events = append([]Event(nil), e.Events...)
	return c
}
