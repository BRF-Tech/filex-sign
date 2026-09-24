package app

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/pdfdoc"
	"github.com/brf-tech/filex-sign/internal/views"
)

// actionRequest opens a signature request: the record, one signing share
// per outside signer, and the invitations.
func (a *App) actionRequest(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
	if reason, ok := a.signingReady(); !ok {
		return failf("Signing is not available: %s", "İmzalama kullanılamıyor: %s", reason)
	}
	if len(in.Inputs) == 0 {
		return fail("No document was given.", "Belge verilmedi.")
	}
	// ⚠⚠ Before ANYTHING — the freeze, the links, the notices: the request
	// writes nothing now, but it ends in a write (the signed version), and
	// on a storage that takes none it could never finish. The wizard says
	// this at its first screen (views.ReadOnlyDoc); this is the boundary for
	// a job queued some other way. 2026-09-21: such a request was "sent",
	// the file frozen, the signer notified — and it could never complete.
	if in.Inputs[0].ReadOnly {
		return failf("“%s” is on a storage that takes no changes, so the signed document could never be saved. Nothing was sent. Copy it to a folder you can write to and ask for signatures there.",
			"“%s” değişiklik kabul etmeyen bir depoda; imzalı belge kaydedilemezdi. Hiçbir şey gönderilmedi. Yazabildiğiniz bir klasöre kopyalayıp imzayı orada isteyin.", in.Inputs[0].Name)
	}
	l := views.Of(in.Locale)
	ref := in.Inputs[0].Ref
	if v, found, _ := a.H.StateGet(ref, envelope.StateKey); found {
		if old, err := envelope.Decode(v); err == nil && !old.Status.Closed() {
			// The wizard says this first (views.AlreadyRequested); this is
			// the boundary for a job queued some other way.
			return fail("This document already has an open signature request, and a document carries one at a time. Follow or cancel it in the details panel → Signatures, then ask again.",
				"Bu belgenin zaten açık bir imza isteği var ve bir belge aynı anda tek istek taşır. İsteği ayrıntı paneli → İmzalar'dan izleyin ya da iptal edin, sonra yeniden isteyin.")
		}
	}
	a.step(in.Locale, 1, 5, "reading the document", "belge okunuyor")
	doc, err := a.openDocument(in.Locale, ref, in.Inputs[0].Name)
	if err != nil {
		return failText(intakeWords(err, in.Inputs[0].Name))
	}
	if doc.Converted() {
		return failf("“%s” is not a PDF. Convert it first — “Sign…” offers that — and ask for signatures on the PDF, where the boxes can be placed.",
			"“%s” bir PDF değil. Önce dönüştürün — “İmzala…” bunu sunar — ve kutuların yerleştirilebildiği PDF üzerinde imza isteyin.", in.Inputs[0].Name)
	}

	people := parsePeople(in.Params["signers"])
	flds, err := parseFields(in.Params["fields"], fontkit.DefaultID, l)
	if err != nil {
		return failPlain(err.Error())
	}
	for i := range flds {
		flds[i].Value = "" // the signers fill these in, not the requester
	}
	// ⚠ The links' life as the HOST will give it, read from this very
	// call: the wizard offered no more than this, but an administrator can
	// lower the ceiling between that screen and this job, and the record,
	// the freeze and the mails must all say what the links will really do.
	life := a.linkLife(in.ShareMaxTTLDays)
	opts := optionsFrom(in.Params, in.Locale, life)
	env := &envelope.Envelope{
		Schema: envelope.SchemaVersion, ID: a.H.NewID(), Status: envelope.StatusSent, Document: in.Inputs[0].Name,
		Requester: actorPerson(&in.Actor),
		Options:   opts, Fields: flds, Form: true,
		CreatedAt: envelope.Stamp(a.H.Now()), UpdatedAt: envelope.Stamp(a.H.Now()),
	}
	for i, p := range people {
		kind := envelope.KindExternal
		if p.UserID != 0 {
			kind = envelope.KindInternal
		}
		env.Signers = append(env.Signers, envelope.Signer{ID: fmt.Sprintf("s%d", i+1), Kind: kind, Person: p, Status: envelope.SignerPending})
	}
	if err := env.Validate(); err != nil {
		return failPlain(err.Error())
	}
	// ⚠ The one place a name is trimmed. It is kept exactly as typed all
	// the way through the wizard (a trim between two keystrokes makes a
	// two-word name impossible to type — app/values.go, clipName); the
	// stored record wants it tidy, and the identity is derived from the
	// tidy one just below.
	for i := range env.Fields {
		env.Fields[i].Label = strings.TrimSpace(env.Fields[i].Label)
	}
	// ⚠⚠ The boxes' IDENTITIES are fixed HERE, once: they followed the
	// names while the wizard was open, and from this moment they are the
	// names of fields that exist in a sent document (envelope.AssignKeys).
	// The document's own form fields are avoided, not overwritten.
	envelope.AssignKeys(env.Fields, pdfdoc.FieldNames(doc.Bytes))
	if msg := everybodyHasSomething(env); msg != nil {
		return failText(msg)
	}
	// ⚠ The wizard asks for a place for every box, one step before Send —
	// but the job is the boundary, not the screen. A box with nowhere to go
	// would be stamped at whatever rectangle it was born with, on page 1,
	// which is a signature in the wrong place rather than an error.
	if n := len(envelope.Unplaced(env.Fields)); n > 0 {
		return failf("%d box(es) have no place on the document yet.",
			"%d kutunun belgede henüz yeri yok.", n)
	}

	ttl := ttlDays(opts, a.H.Now(), life)
	if opts.Lock {
		// The freeze reason is what a refused rename tells whoever tried —
		// a manifest message, so filex says it in THEIR language, not in
		// the requester's (v0.43.0 wave 2).
		until, err := a.H.FileLockMessage(ref, ttl, LockCollecting, nil)
		if err != nil {
			return failf("The document could not be frozen (%s). Start the request again without it, or free the file first.",
				"Belge dondurulamadı (%s). İsteği dondurmadan yeniden başlatın ya da önce dosyayı serbest bırakın.", shortErr(err))
		}
		env.Locked = true
		env.LockUntil = envelope.Stamp(until)
	}
	undo := func() {
		for _, sg := range env.Signers {
			if sg.PageToken != "" {
				_ = a.H.ShareRevoke(sg.PageToken)
			}
		}
		if env.Locked {
			_ = a.H.FileUnlock(ref)
		}
	}

	// One signing share per OUTSIDE signer, whether or not they have an
	// address: somebody the requester will reach by hand still needs a
	// link and a PIN, and those are shown to the requester.
	pins := map[string]string{}
	pin := "auto"
	if opts.PIN == "none" {
		pin = ""
	}
	for i := range env.Signers {
		sg := &env.Signers[i]
		if sg.Internal() {
			continue
		}
		a.step(in.Locale, 2, 5, "opening a signing link for %s", "%s için imza bağlantısı açılıyor", sg.Person.Identity())
		pg, err := a.H.ShareCreate(pluginkit.PageCreate{
			// ⚠⚠ No subject. The public shell prints it under the page's
			// title, and it is a STRING, frozen in the requester's language:
			// a Turkish signer of an English requester's link read "Please
			// sign contract.pdf" under "contract.pdf — imzala", on a page
			// otherwise in Turkish — and a file share, whose header this page
			// should match, carries no such line either. The title the app
			// draws is in the visitor's language; the requester's message, if
			// any, is on the first step, in their own words.
			PageID:  PageSigner,
			Subject: "",
			PIN:     pin, TTLDays: ttl,
			State: map[string]any{"envelope_id": env.ID, "signer_id": sg.ID},
			Files: []wire.OutputRef{{Ref: ref, Name: env.Document}},
		})
		if err != nil {
			undo()
			return failf("Could not open the signing link for %s: %v", "%s için imza bağlantısı açılamadı: %v", sg.Person.Identity(), err)
		}
		sg.PageToken, sg.PageTokenHash, sg.PageURL, sg.PageExpires = pg.Token, tokenHash(pg.Token), pg.URL, pg.ExpiresAt
		if pg.PIN != "" {
			pins[sg.ID] = pg.PIN
		}
	}
	env.Events = append(env.Events, envelope.Event{At: env.CreatedAt, Type: "created", Note: fmt.Sprintf("%d signer(s)", len(env.Signers))})
	if err := a.store(ref, env); err != nil {
		undo()
		return nil, err
	}

	// Invitations: everybody at once, or only whoever is first in line.
	for i := range env.Signers {
		sg := &env.Signers[i]
		if env.Sequential() && i > 0 {
			continue
		}
		a.step(in.Locale, 3, 5, "inviting %s", "%s davet ediliyor", sg.Person.Identity())
		a.invite(ref, env, sg)
	}
	if err := a.store(ref, env); err != nil {
		return nil, err
	}
	a.step(in.Locale, 4, 5, "notifying you", "size haber veriliyor")
	body := requesterNotice(env, pins)
	a.notifyRequester(ref, env, views.T("Signature request sent", "İmza isteği gönderildi"), body, "info")
	a.step(in.Locale, 5, 5, "done", "bitti")
	return &wire.ActionRunOutput{OK: true, Message: body}, nil
}

// invite tells one signer it is their turn.
//
// Three ways, decided by ONE question — what is in their identity:
//
//	an account here     → a notification that opens the signing screen
//	an e-mail address   → their private link, by mail
//	a name and nothing  → nothing is sent; the link and the PIN are shown
//	                      to the requester, who hands them over
func (a *App) invite(ref string, env *envelope.Envelope, sg *envelope.Signer) {
	switch {
	case sg.Internal():
		title, body := inviteNotice(env, sg)
		if _, err := a.H.NotifySend(pluginkit.Notice{
			Title: title, Body: body, Severity: "info", ToUserID: sg.Person.UserID,
			Meta:   map[string]any{"envelope": env.ID, "document": env.Document},
			Target: &pluginkit.NoticeTarget{Ref: ref, Action: ActionFill},
		}); err != nil {
			sg.MailError = shortErr(err)
			a.logf("warn", "notify %s: %v", sg.Person.Identity(), err)
			return
		}
		sg.MailSent = true
	case sg.Person.HasEmail():
		subject, body := inviteMail(env.Options.Locale, env, sg)
		if err := a.H.MailSend(sg.Person.Email, subject, body); err != nil {
			sg.MailError = shortErr(err)
			a.logf("warn", "mail to %s: %v", sg.Person.Email, err)
			return
		}
		sg.MailSent = true
	default:
		// Nothing to send, and that is not a failure: the requester was
		// told the link and the PIN and is the delivery channel.
		sg.MailSent = false
	}
	if next, _, err := envelope.Apply(*env, envelope.Input{Type: envelope.EvNotified, Signer: sg.ID, At: a.H.Now()}); err == nil {
		*env = next
	}
}

// linkLife is what the links of a request may live: the signing page's own
// default and ceiling from the manifest, pulled down to the installation's
// share ceiling (CallContext / ActionRunInput .ShareMaxTTLDays) when that
// is lower. See views.LinkLife for the defect this closes.
func (a *App) linkLife(ceiling *int) views.LinkLife {
	var life views.LinkLife
	for _, p := range a.M.PublicPages {
		if p.ID == PageSigner {
			life.Default, life.Max = p.DefaultTTLDays, p.MaxTTLDays
		}
	}
	life = life.OrDefaults()
	if ceiling != nil && *ceiling > 0 && *ceiling < life.Max {
		life.Max, life.Capped = *ceiling, true
		if life.Default > life.Max {
			life.Default = life.Max
		}
	}
	return life
}

// optionsFrom reads the wizard's option steps.
func optionsFrom(params map[string]any, locale string, life views.LinkLife) envelope.Options {
	o := envelope.Options{
		PIN: pinOption(str(params, "pin")), ExpiryDays: life.Clamp(num(params, "expiry")),
		Message: clip(str(params, "message"), 500), Locale: locale,
		Order: envelope.OrderParallel, RemindEveryDays: num(params, "remind_every"),
		AllowDecline: boolOf(params, "allow_decline"), Lock: boolOf(params, "lock"), Audit: boolOf(params, "audit"),
		LockSigned: boolOf(params, "lock_signed"),
		Output:     outputFromValues(params),
		Deliver:    envelope.DeliverShare, DeliveryPIN: pinOption(str(params, "delivery_pin")),
	}
	if str(params, "deliver") == envelope.DeliverNone {
		o.Deliver = envelope.DeliverNone
	}
	if str(params, "order") == envelope.OrderSequential {
		o.Order = envelope.OrderSequential
	}
	if d := str(params, "deadline"); d != "" {
		if t, ok := fields.ParseDate(d, fields.DateYMD); ok {
			o.Deadline = t.Format(fields.ISO)
		}
	}
	if o.RemindEveryDays < 0 {
		o.RemindEveryDays = 0
	}
	if o.RemindEveryDays > 60 {
		o.RemindEveryDays = 60
	}
	return o
}

// ttlDays is how long the links — and the freeze — live: what was asked
// for, inside what the installation allows, and no longer than it takes to
// reach the end of the sign-by day.
//
// ⚠ ROUNDED UP to that day's end, not down. share_create counts whole days
// from NOW, and rounding down made a link opened at 11:00 for "sign by the
// 24th" die at 11:00 ON the 24th — the day the request said was still
// open. Rounded up, the link outlives the day by less than one, and the
// request itself closes at the day's last minute (lapseAt, run by the
// hourly wake-up and by every touch), which revokes the link with it.
func ttlDays(o envelope.Options, now time.Time, life views.LinkLife) int {
	days := life.Clamp(o.ExpiryDays)
	if o.Deadline == "" {
		return days
	}
	t, ok := fields.ParseDate(o.Deadline, fields.DateYMD)
	if !ok {
		return days
	}
	// The deadline day itself counts, so a request due today still has one.
	left := int(math.Ceil(t.AddDate(0, 0, 1).Sub(now).Hours() / 24))
	if left < 1 {
		left = 1
	}
	if left < days {
		return left
	}
	return days
}

func pinOption(v string) string {
	if v == "none" {
		return "none"
	}
	return "auto"
}

// everybodyHasSomething checks every participant has a box to act on.
//
// ⚠⚠ It used to insist on a SIGNATURE for each of them, and that rule is
// gone (the owner, 2026-09-23: "her kişi için imza yerleştirmek zorunlu
// olmasın bazı kişiler sadece metin doldurabilir. Bu kuralı da
// kapatalım"). What is left is the part that still means something: a
// person invited to a document with NOTHING on it for them was invited by
// mistake, and would be handed a screen asking them for nothing.
//
// A box that belongs to anybody counts for one unclaimed person, the same
// arithmetic as before.
func everybodyHasSomething(env *envelope.Envelope) wire.Text {
	spare := 0
	own := map[string]bool{}
	for _, f := range env.Fields {
		if f.Assignee == "" {
			spare++
		} else {
			own[f.Assignee] = true
		}
	}
	var names []string
	for _, s := range env.Signers {
		if !own[s.ID] {
			names = append(names, s.Person.Identity())
		}
	}
	if len(names) > spare {
		return views.Tf("These people have nothing to do on the document. Give each of them a box — a signature or something to fill in — or take them off: %s",
			"Şu kişilerin belgede yapacağı bir şey yok. Her birine bir kutu verin — imza ya da doldurulacak bir şey — ya da onları listeden çıkarın: %s",
			strings.Join(names, ", "))
	}
	return nil
}
