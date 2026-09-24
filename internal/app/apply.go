package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/pdfdoc"
	"github.com/brf-tech/filex-sign/internal/pdfsig"
	"github.com/brf-tech/filex-sign/internal/views"
)

// actionApply is the second half of every flow: a signer's submission, a
// reminder, a refusal, the end of a request.
func (a *App) actionApply(in *wire.ActionRunInput) (*wire.ActionRunOutput, error) {
	if len(in.Inputs) == 0 {
		return fail("No document was given.", "Belge verilmedi.")
	}
	ref := in.Inputs[0].Ref
	env, err := a.load(ref)
	if err != nil {
		return fail("This document has no signature request. Start one with “Request signatures…”.", "Bu belgenin imza isteği yok. “İmza iste…” ile başlatın.")
	}
	op := str(in.Params, "op")
	scheduled := boolOf(in.Params, paramScheduled)
	// ⚠ EVERY job is a chance to notice that the request ran out. The
	// hourly wake-up now closes a lapsed request on its own minute
	// (tick.go), but it can only see what it was told about: a request on
	// a file past the listing limit, or one that lapsed while the app was
	// uninstalled, still needs the first passer-by to close it. `expire`
	// IS that closure and does the same work below, so it is the one op
	// that does not sweep first.
	//
	// The same goes for a signing link a person ended on the Shares screen
	// (links.go): the wake-up closes the request within seconds, and every
	// job asks too, for an installation whose wake-up is off.
	if op != opExpire && op != opLinkEnded {
		env = a.sweepExpired(ref, env)
		env = a.sweepEndedLinks(ref, env)
	}
	a.unfreezeClosed(ref, env)
	switch op {
	case "cancel":
		return a.opCancel(ref, env)
	case opExpire:
		return a.opExpire(ref, env, scheduled)
	case opLinkEnded:
		return a.opLinkEnded(ref, env, scheduled)
	case opRemind:
		return a.opRemind(ref, env, str(in.Params, "signer_id"), scheduled)
	case "decline":
		return a.opDecline(in, ref, env)
	case "viewed":
		return a.opViewed(in, ref, env)
	case "audit":
		return a.opAudit(in, ref, env)
	case "", "sign":
		return a.opSign(in, ref, env)
	default:
		return failf("Unknown operation %q.", "Bilinmeyen işlem %q.", op)
	}
}

func (a *App) opCancel(ref string, env *envelope.Envelope) (*wire.ActionRunOutput, error) {
	next, effects, err := envelope.Apply(*env, envelope.Input{Type: envelope.EvCancelled, At: a.H.Now()})
	if err != nil {
		return fail("The request is already closed.", "İstek zaten kapalı.")
	}
	a.runEffects(ref, &next, effects)
	if err := a.store(ref, &next); err != nil {
		return nil, err
	}
	return &wire.ActionRunOutput{OK: true, Message: views.T("Signature request cancelled.", "İmza isteği iptal edildi.")}, nil
}

func (a *App) opExpire(ref string, env *envelope.Envelope, scheduled bool) (*wire.ActionRunOutput, error) {
	if _, err := a.closeExpired(ref, env); err != nil {
		if errors.Is(err, envelope.ErrClosed) {
			// ⚠ Pressed by a person this is an answer: the panel they were
			// looking at was stale. Run from the SCHEDULE it is not a
			// fault at all — the last signature arriving in the hour
			// between the wake-up and the minute it named is the flow
			// working. A red ops row for that would send an administrator
			// hunting a fault that never happened.
			if scheduled {
				return &wire.ActionRunOutput{OK: true, Message: views.T(
					"Nothing to close: the request had already ended.",
					"Kapatılacak bir şey yok: istek zaten sonlanmıştı.")}, nil
			}
			return fail("The request is already closed.", "İstek zaten kapalı.")
		}
		return nil, err
	}
	return &wire.ActionRunOutput{OK: true, Message: views.T(
		"The expired request was closed and the document released.",
		"Süresi dolan istek kapatıldı ve belge serbest bırakıldı.")}, nil
}

// ── running out ────────────────────────────────────────────────────────
//
// A request that lapses at 03:00 closes at 03:00: the hourly wake-up
// (tick.go) asks filex to run `apply` with `op: expire` on this document
// at exactly that minute, with nobody present. That is the braces.
//
// ⚠ This sweep is the BELT, and it stays. The wake-up can only name work
// it can see and the host will run: a request past the listing limit, one
// that lapsed while the app was disabled or uninstalled, one on an
// instance whose administrator never granted `schedule` — every one of
// those still has to close the moment somebody comes near it. sweepExpired
// runs on every job and on the signer's own page, which between them cover
// every writable call this app gets, and it performs the SAME closure the
// scheduled job does: the file is released, the links are revoked and BOTH
// SIDES are told — the requester by the machine's own notice, the signers
// by tellLapsed.

// sweepExpired closes a request whose time is up and answers what is now
// stored. An envelope that is still open, or that could not be written,
// comes back unchanged: a sweep may never be the reason a job fails.
func (a *App) sweepExpired(ref string, env *envelope.Envelope) *envelope.Envelope {
	if env == nil || !a.expired(env) {
		return env
	}
	next, err := a.closeExpired(ref, env)
	if err != nil {
		a.logf("warn", "closing the lapsed request: %v", err)
		return env
	}
	return next
}

// closeExpired applies the expiry, performs its effects, tells everybody
// and stores the result.
func (a *App) closeExpired(ref string, env *envelope.Envelope) (*envelope.Envelope, error) {
	next, effects, err := envelope.Apply(*env, envelope.Input{Type: envelope.EvExpired, At: a.H.Now()})
	if err != nil {
		return nil, err
	}
	a.runEffects(ref, &next, effects)
	a.tellLapsed(ref, &next)
	if err := a.store(ref, &next); err != nil {
		return nil, err
	}
	return &next, nil
}

// unfreezeClosed lifts a freeze that a CLOSED request is still carrying.
//
// ⚠ It is the second half of a sweep that ran somewhere the lock could
// not be lifted. filex lifts a lock from an ACTION JOB only, so when the
// signer's page is the one that closes a lapsed request, the file stays
// frozen and the record says so. This is the next job, and it finishes
// the job that call could not — which is what keeps the promise that no
// ending of the flow leaves a file frozen.
func (a *App) unfreezeClosed(ref string, env *envelope.Envelope) {
	if env == nil || !env.Status.Closed() || !env.Locked {
		return
	}
	if err := a.H.FileUnlock(ref); err != nil {
		a.logf("warn", "releasing a closed request's file: %v", err)
		return
	}
	env.Locked = false
	if err := a.store(ref, env); err != nil {
		a.logf("warn", "recording the release: %v", err)
	}
}

// tellLapsed tells the SIGNERS that a request they were holding is over.
// The requester hears it through the machine's own notice; somebody who
// was still sitting on a signing link would otherwise find a dead page
// and no explanation.
//
// ⚠ The rule the invitation, the reminder, the receipt and the delivery
// all follow holds here too: a signer with an account is told IN filex,
// never mailed.
func (a *App) tellLapsed(ref string, env *envelope.Envelope) {
	for i := range env.Signers {
		sg := &env.Signers[i]
		if sg.Status == envelope.SignerSigned {
			continue // they are finished and already have their receipt
		}
		switch {
		case sg.Internal():
			title, body := lapsedNotice(env, sg)
			if _, err := a.H.NotifySend(pluginkit.Notice{
				Title: title, Body: body, Severity: "warning", ToUserID: sg.Person.UserID,
				Meta:   map[string]any{"envelope": env.ID, "document": env.Document},
				Target: &pluginkit.NoticeTarget{Ref: ref},
			}); err != nil {
				a.logf("warn", "expiry notice to %s: %v", sg.Person.Identity(), err)
			}
		case sg.Person.HasEmail():
			subject, body := lapsedMail(env.Options.Locale, env, sg)
			if err := a.H.MailSend(sg.Person.Email, subject, body); err != nil {
				a.logf("warn", "expiry mail to %s: %v", sg.Person.Email, err)
			}
		}
	}
}

// ── opening the document ───────────────────────────────────────────────

// opViewed records that a signer opened the document, as a JOB.
//
// ⚠ It exists because the in-app filling screen is a VIEW, and filex
// refuses `state_set` outside a job (`hostfn.go`: "this call may not
// write state"). A screen that cannot write cannot record anything, so
// the one path that CAN is this op.
func (a *App) opViewed(in *wire.ActionRunInput, ref string, env *envelope.Envelope) (*wire.ActionRunOutput, error) {
	sg := env.Signer(str(in.Params, "signer_id"))
	if sg == nil {
		sg = env.SignerForUser(in.Actor.ID, in.Actor.Email)
	}
	if sg == nil {
		return fail("You are not one of this document's signers.", "Bu belgenin imzacılarından biri değilsiniz.")
	}
	if env.Status.Closed() {
		return fail("The request is closed.", "İstek kapalı.")
	}
	// Opening it twice is not an event. The machine keeps the promise
	// (Signer.ViewedAt), and saying so here keeps the job honest too.
	if sg.ViewedAt != "" {
		return &wire.ActionRunOutput{OK: true, Message: views.Tf("%s was already open.", "%s zaten açılmıştı.", env.Document)}, nil
	}
	a.markViewed(ref, env, sg, str(in.Params, "visitor_ip"))
	return &wire.ActionRunOutput{OK: true, Message: views.Tf("%s opened “%s”.", "%s “%s” belgesini açtı.", sg.Person.Identity(), env.Document)}, nil
}

// markViewed records that a signer opened the document and tells the
// requester — ONCE per signer, whichever screen they opened it on, the
// app's own or their private link. The machine raises the notice only the
// first time (Signer.ViewedAt), so a second open is silent, and the
// requester's words come from the same noticeFor("viewed") on both paths.
//
// ⚠ The RECORD comes first and the notice second. Reversed, a write that
// the host refused would leave the requester hearing "X opened the
// document" again on every single open.
func (a *App) markViewed(ref string, env *envelope.Envelope, sg *envelope.Signer, ip string) *envelope.Envelope {
	if env == nil || sg == nil {
		return env
	}
	next, effects, err := envelope.Apply(*env, envelope.Input{
		Type: envelope.EvViewed, Signer: sg.ID, IP: ip, At: a.H.Now()})
	if err != nil {
		return env
	}
	if err := a.store(ref, &next); err != nil {
		a.logf("warn", "recording the open: %v", err)
		return env
	}
	a.runEffects(ref, &next, effects)
	return &next
}

func (a *App) opRemind(ref string, env *envelope.Envelope, signerID string, scheduled bool) (*wire.ActionRunOutput, error) {
	sg := env.Signer(signerID)
	if sg == nil {
		return fail("Unknown signer.", "Bilinmeyen imzacı.")
	}
	// The same courtesy the scheduled closure gets: a signer who acted
	// between the wake-up and its minute has made this reminder pointless,
	// not failed.
	if scheduled && (env.Status.Closed() || sg.Done()) {
		return &wire.ActionRunOutput{OK: true, Message: views.Tf(
			"No reminder needed: %s has already acted.",
			"%s zaten hareket etmiş; hatırlatmaya gerek kalmadı.", sg.Person.Identity())}, nil
	}
	if !sg.Internal() && !sg.Person.HasEmail() {
		return failf("%s has no address for filex to write to — pass their link on yourself: %s",
			"%s için filex'in yazabileceği bir adres yok — bağlantısını kendiniz iletin: %s", sg.Person.Identity(), sg.PageURL)
	}
	next, _, err := envelope.Apply(*env, envelope.Input{Type: envelope.EvReminded, Signer: signerID, At: a.H.Now()})
	if err != nil {
		return failPlain(err.Error())
	}
	if sg.Internal() {
		title, body := inviteNotice(env, sg)
		if _, err := a.H.NotifySend(pluginkit.Notice{
			Title: reminderTitle(title), Body: body, Severity: "info", ToUserID: sg.Person.UserID,
			Target: &pluginkit.NoticeTarget{Ref: ref, Action: ActionFill},
		}); err != nil {
			return failf("The reminder could not be sent (%s).", "Hatırlatma gönderilemedi (%s).", shortErr(err))
		}
	} else {
		subject, body := reminderMail(env.Options.Locale, env, sg)
		if err := a.H.MailSend(sg.Person.Email, subject, body); err != nil {
			return failf("Mail could not be sent (%s). Pass the link on yourself: %s", "E-posta gönderilemedi (%s). Bağlantıyı kendiniz iletin: %s", shortErr(err), sg.PageURL)
		}
	}
	if err := a.store(ref, &next); err != nil {
		return nil, err
	}
	return &wire.ActionRunOutput{OK: true, Message: views.Tf("Reminder sent to %s.", "%s kişisine hatırlatma gönderildi.", sg.Person.Identity())}, nil
}

func (a *App) opDecline(in *wire.ActionRunInput, ref string, env *envelope.Envelope) (*wire.ActionRunOutput, error) {
	sg, bad := a.whoSigns(in, env)
	if bad != nil {
		return failText(views.Problem(bad))
	}
	if !env.Options.AllowDecline {
		return fail("This request does not allow refusing.", "Bu istek reddetmeye izin vermiyor.")
	}
	next, effects, err := envelope.Apply(*env, envelope.Input{
		Type: envelope.EvDeclined, Signer: sg.ID, At: a.H.Now(), IP: str(in.Params, "visitor_ip"),
		Note: clip(str(in.Params, "reason"), 300),
	})
	if err != nil {
		return failPlain(err.Error())
	}
	a.runEffects(ref, &next, effects)
	if err := a.store(ref, &next); err != nil {
		return nil, err
	}
	return &wire.ActionRunOutput{OK: true, Message: views.Tf("%s refused to sign “%s”.", "%s “%s” belgesini imzalamayı reddetti.",
		sg.Person.Identity(), env.Document)}, nil
}

// whoSigns answers which signer this submission belongs to, and refuses
// anyone signing as somebody else: a share may only act for its own
// signer, and a person in the app only for themselves.
func (a *App) whoSigns(in *wire.ActionRunInput, env *envelope.Envelope) (*envelope.Signer, *fields.Problem) {
	if envID := str(in.Params, "envelope_id"); envID != "" && envID != env.ID {
		return nil, &fields.Problem{
			EN: "This submission belongs to an older request for this document.",
			TR: "Bu gönderim bu belgenin daha eski bir isteğine ait."}
	}
	if h := str(in.Params, "page_token_hash"); h != "" {
		sg := env.SignerByTokenHash(h)
		if sg == nil {
			return nil, &fields.Problem{EN: "This link is not a signer's own.", TR: "Bu bağlantı bir imzacıya ait değil."}
		}
		if id := str(in.Params, "signer_id"); id != "" && id != sg.ID {
			return nil, &fields.Problem{EN: "This link is not that signer's own.", TR: "Bu bağlantı o imzacıya ait değil."}
		}
		return sg, nil
	}
	sg := env.SignerForUser(in.Actor.ID, in.Actor.Email)
	if sg == nil {
		return nil, &fields.Problem{
			EN: "You are not one of this document's signers.", TR: "Bu belgenin imzacılarından biri değilsiniz."}
	}
	if id := str(in.Params, "signer_id"); id != "" && id != sg.ID {
		return nil, &fields.Problem{EN: "You can only sign as yourself.", TR: "Yalnızca kendi adınıza imzalayabilirsiniz."}
	}
	return sg, nil
}

func (a *App) opSign(in *wire.ActionRunInput, ref string, env *envelope.Envelope) (*wire.ActionRunOutput, error) {
	if reason, ok := a.signingReady(); !ok {
		return failf("Signing is not available: %s", "İmzalama kullanılamıyor: %s", reason)
	}
	sg, bad := a.whoSigns(in, env)
	if bad != nil {
		return failText(views.Problem(bad))
	}
	if env.Status.Closed() {
		return fail("The request is closed.", "İstek kapalı.")
	}
	if sg.Status == envelope.SignerSigned {
		return failf("%s already signed.", "%s zaten imzaladı.", sg.Person.Identity())
	}
	if !env.Turn(sg.ID) {
		return fail("This document is signed one signer at a time and it is somebody else's turn.",
			"Bu belge birer birer imzalanıyor ve şu an sıra başkasında.")
	}
	l := views.Of(in.Locale)
	a.step(in.Locale, 1, 5, "reading the document", "belge okunuyor")
	doc, err := a.openDocument(in.Locale, ref, env.Document)
	if err != nil {
		return failText(intakeWords(err, env.Document))
	}
	values := parseFill(in.Params["values"])
	drawing := decodeImage(str(in.Params, "png_b64"))
	p, prob := planFill(planInput{
		Info: doc.Info, All: env.Fields, Mine: env.FieldsFor(sg.ID), Values: values,
		SignerID: sg.ID, Name: sg.Person.CertName(), Now: a.H.Now(), Lang: l,
		Form: env.Form && env.Schema >= 3, Visible: visibleBoxes(env), Drawing: drawing,
	})
	if prob != nil {
		return failText(views.Problem(prob))
	}
	// The signature fields — every signer's, and the seal's — are made with
	// the form, before the first signature certifies it (actions.go).
	formPath := env.Form && env.Schema >= 3
	_, specs := sigSpecs(doc.Info, env.Fields, signerIDs(env), env.FieldsFor, l)
	if formPath {
		p.Specs = append(p.Specs, specs...)
	}
	certify := 0
	if formPath && doc.Info.Signatures == 0 {
		certify = CertifyPermission
	}
	signedSoFar, total := env.Progress()
	completing := signedSoFar+1 == total
	out := outputOf(in, env.Options.Output)
	a.step(in.Locale, 2, 5, "issuing a certificate for %s", "%s için sertifika üretiliyor", sg.Person.CertName())
	// ⚠⚠ What the PDF says this person DID.
	//
	// A participant who was asked for no signature still signs
	// cryptographically — an invisible signature over their own revision,
	// so the chain of custody stays unbroken and Verify can still say who
	// changed what. But the reason is printed INSIDE the signature and
	// every PDF reader shows it for ever, so it must not call a fill a
	// signature: this line, the audit trail and the verification report
	// name the same act with the same word (the owner, 2026-09-23: "bazı
	// kişiler sadece metin doldurabilir").
	reason := a.say(env.Options.Locale, "Filled in at the request of %s", "%s isteğiyle dolduruldu", env.Requester.Identity())
	if env.Signs(sg.ID) {
		reason = a.say(env.Options.Locale, "Signed at the request of %s", "%s isteğiyle imzalandı", env.Requester.Identity())
	}
	res, err := a.applyAndSign(doc, p, signRequest{
		Name: sg.Person.CertName(), Email: sg.Person.Email, Locale: env.Options.Locale,
		// ⚠ The reason is printed INSIDE the signature: every PDF reader
		// shows it, for ever. It follows the request's own language.
		Reason:  reason,
		Drawing: drawing,
		// The address the SUBMIT came from — the visitor's on a signing
		// link, the person's own in the app (context.actor.ip) — which is
		// the address the signer was shown on the approve step.
		IP:       str(in.Params, "visitor_ip"),
		SigField: ownSigSpec(doc.Info, env.Fields, p.Visible, sg.ID, l),
		Certify:  certify,
	})
	if err != nil {
		return failf("Signing failed: %v", "İmzalama başarısız: %v", err)
	}
	final := res.Out
	var seal *sealed
	if completing {
		// The last signature: filex seals the whole document, and the hash
		// every party is told is of THESE bytes.
		a.step(in.Locale, 3, 5, "sealing the document", "belge mühürleniyor")
		if seal, err = a.sealDocument(res.Out, env.Options.Locale, a.H.Now()); err != nil {
			return failf("The document could not be sealed: %v", "Belge mühürlenemedi: %v", err)
		}
		final = seal.Out
	}
	a.step(in.Locale, 3, 5, "saving the signed document", "imzalı belge kaydediliyor")
	outRef, outName, err := a.writeSigned(in, doc, out, final)
	if err != nil {
		return nil, err
	}
	next, effects, err := envelope.Apply(*env, envelope.Input{
		Type: envelope.EvSigned, Signer: sg.ID, IP: str(in.Params, "visitor_ip"), At: a.H.Now(),
		Fields: p.Consumed, CertSerial: res.CertSerial, CertExpires: res.CertExpires,
	})
	if err != nil {
		return failPlain(err.Error())
	}
	if s := next.Signer(sg.ID); s != nil {
		s.CertFP = res.CertFP
	}
	if certify > 0 {
		next.Certified = certify
	}
	if seal != nil && next.Status == envelope.StatusCompleted {
		next.Sealed = &envelope.Sealed{SHA256: seal.SHA256, SealFP: seal.CertFP, At: envelope.Stamp(a.H.Now()), Output: outName}
		// What Verify looks the hash up by, on any copy of the file.
		if err := a.H.StateSet(ref, envelope.SealedKey, seal.SHA256+" "+outName); err != nil {
			a.logf("warn", "recording the sealed hash: %v", err)
		}
	}
	// The certificate is kept WITH the document: a receipt that can only
	// be handed over once is a receipt somebody will lose.
	if err := a.H.StateSet(ref, envelope.CertPrefix+sg.ID, b64of(res.CertDER)); err != nil {
		a.logf("warn", "keeping the certificate: %v", err)
	}
	a.markSigned(ref, sg.Person.UserID)
	a.runEffects(ref, &next, effects)
	if next.Sealed != nil && next.Options.LockSigned {
		// AFTER the effects: finishing lifts the freeze the request may have
		// held, and this lock is the one that stays.
		a.lockForGood(ref, &next, out, outRef)
	}

	a.step(in.Locale, 4, 5, "handing over the receipt", "makbuz veriliyor")
	receiptPIN := a.handReceipt(ref, &next, next.Signer(sg.ID), res)

	outputs := []wire.OutputRef{outRef}
	trail := ""
	if next.Status == envelope.StatusCompleted && next.Options.Audit {
		if out.Mode == envelope.OutputSibling {
			name := stem(in.Inputs[0].Name) + "-audit.pdf"
			if pdfBytes, err := a.auditPDF(&next, auditLang(&next, in.Locale)); err != nil {
				a.logf("warn", "audit trail: %v", err)
			} else if r, err := a.H.WriteOutput(name, pdfBytes); err != nil {
				a.logf("warn", "audit trail: %v", err)
			} else {
				outputs = append(outputs, r)
				trail = name
			}
		} else {
			trail = "panel"
		}
	}
	deliveryPIN := ""
	if next.Status == envelope.StatusCompleted {
		// Every party hears it — with the hash — whether or not the
		// requester chose to send the document itself.
		deliveryPIN = a.deliver(ref, &next, outRef, outName, linkSection(&next, in.Actor))
	}
	if err := a.store(ref, &next); err != nil {
		return nil, err
	}
	a.step(in.Locale, 5, 5, "done", "bitti")

	signed, total := next.Progress()
	msg := views.Each(func(l views.Lang) string {
		var b strings.Builder
		if env.Signs(sg.ID) {
			b.WriteString(l.Sf("%s signed “%s” (%d/%d) → %s", "%s “%s” belgesini imzaladı (%d/%d) → %s",
				sg.Person.Identity(), env.Document, signed, total, outName))
		} else {
			b.WriteString(l.Sf("%s filled in “%s” (%d/%d) → %s", "%s “%s” belgesini doldurdu (%d/%d) → %s",
				sg.Person.Identity(), env.Document, signed, total, outName))
		}
		b.WriteString(l.Sf(" · certificate %s", " · sertifika %s", res.CertFP))
		if !res.Timestamped && a.tsa() != "" {
			b.WriteString(l.S(" · no time stamp (the authority could not be reached)", " · zaman damgası yok (makama ulaşılamadı)"))
		}
		if receiptPIN != "" {
			b.WriteString(l.Sf(" · receipt PIN for %s: %s", " · %s için makbuz PIN'i: %s", sg.Person.Identity(), receiptPIN))
		}
		if next.Sealed != nil {
			b.WriteString(l.Sf(" · sealed by filex · SHA-256 %s", " · filex tarafından mühürlendi · SHA-256 %s", next.Sealed.SHA256))
			if next.Sealed.LockedForGood {
				b.WriteString(l.S(" · the signed file is locked for good", " · imzalı dosya kalıcı olarak kilitlendi"))
			}
		}
		if next.DeliveryURL != "" {
			b.WriteString(l.Sf(" · the signed copy went out as %s", " · imzalı kopya şu bağlantıyla gönderildi: %s", next.DeliveryURL))
		}
		if deliveryPIN != "" {
			b.WriteString(" (PIN " + deliveryPIN + ")")
		}
		switch trail {
		case "":
		case "panel":
			b.WriteString(l.S(". The audit trail is one click away in the Signatures panel.", ". Denetim izi, İmzalar panelinde tek tıkla alınabilir."))
		default:
			b.WriteString(" · " + trail)
		}
		return b.String()
	})
	return &wire.ActionRunOutput{OK: true, Outputs: outputs, Message: msg}, nil
}

// opAudit writes the trail on its own, for a request whose signed file
// went in as a new version (a job commits one output mode, so the trail
// cannot ride along with it).
func (a *App) opAudit(in *wire.ActionRunInput, _ string, env *envelope.Envelope) (*wire.ActionRunOutput, error) {
	pdfBytes, err := a.auditPDF(env, auditLang(env, in.Locale))
	if err != nil {
		return failf("The audit trail could not be written: %v", "Denetim izi yazılamadı: %v", err)
	}
	name := stem(in.Inputs[0].Name) + "-audit.pdf"
	ref, err := a.H.WriteOutput(name, pdfBytes)
	if err != nil {
		return nil, err
	}
	return &wire.ActionRunOutput{OK: true, Outputs: []wire.OutputRef{ref},
		Message: views.Tf("Audit trail written → %s", "Denetim izi yazıldı → %s", name)}, nil
}

// runEffects performs what the machine asked for. The unfreeze is one of
// them, which is why no ending of the flow can leave the file locked.
func (a *App) runEffects(ref string, env *envelope.Envelope, effects []envelope.Effect) {
	for _, e := range effects {
		switch e.Kind {
		case envelope.EffectRevokePage:
			if err := a.H.ShareRevoke(e.Token); err != nil {
				a.logf("warn", "revoke share: %v", err)
			}
		case envelope.EffectUnlockFile:
			if err := a.H.FileUnlock(ref); err != nil {
				// ⚠ A SCREEN may not lift a freeze — filex answers "locks are
				// lifted from action jobs only" — and the signer's page is a
				// screen that can now close a lapsed request. So the record
				// keeps saying the file is frozen, which is the truth, and
				// unfreezeClosed finishes it on the next job. Clearing the
				// flag here would hide a frozen file behind a closed request.
				a.logf("warn", "unlock: %v", err)
				break
			}
			env.Locked = false
		case envelope.EffectInvite:
			if sg := env.Signer(e.Signer); sg != nil {
				a.invite(ref, env, sg)
			}
		case envelope.EffectNotifyRequester:
			title, body := noticeFor(e.Notice, env, env.Signer(e.Signer))
			sev := "info"
			switch e.Notice {
			case "expired", "link_ended":
				sev = "warning"
			case "declined":
				sev = "error"
			}
			a.notifyRequester(ref, env, title, body, sev)
		}
	}
}

// notifyRequester tells whoever asked. ⚠ The click opens the Signatures
// PAGE at "I asked for these" (a home page's section — the owner,
// 2026-09-21: "opening it from a notification lands on the right
// section"), where every request they sent is one row with its progress,
// and the row takes them on to the document's own panel.
func (a *App) notifyRequester(ref string, env *envelope.Envelope, title, body wire.Text, severity string) {
	n := pluginkit.Notice{Title: title, Body: body, Severity: severity,
		Meta:   map[string]any{"envelope": env.ID, "document": env.Document, "status": string(env.Status)},
		Target: &pluginkit.NoticeTarget{View: ViewHome, Section: views.SectionRequested}}
	if env.Requester.UserID != 0 {
		n.ToUserID = env.Requester.UserID
	}
	if _, err := a.H.NotifySend(n); err != nil {
		a.logf("warn", "notify: %v", err)
	}
}

// markSigned records that this app signed the document, and which of
// the people with an account here did it -- the one thing the Signatures
// screen cannot work out for a document that carries no request.
func (a *App) markSigned(ref string, userID int64) {
	badge, _, err := a.H.StateGet(ref, envelope.SignedKey)
	if err != nil {
		a.logf("warn", "reading the signed badge: %v", err)
	}
	if err := a.H.StateSet(ref, envelope.SignedKey, envelope.AddSigner(badge, userID)); err != nil {
		a.logf("warn", "signed badge: %v", err)
	}
}

// ── state ──────────────────────────────────────────────────────────────

func (a *App) load(ref string) (*envelope.Envelope, error) {
	v, found, err := a.H.StateGet(ref, envelope.StateKey)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, envelope.ErrNotFound
	}
	return envelope.Decode(v)
}

// store writes the envelope and keeps the `pending` marker in step with
// it: that key is what filex's listings expose as `sign:pending`, and it
// is what puts "Sign / Fill" in the right-click menu of exactly the
// documents that are waiting for one.
//
// ⚠ `signed` is a BADGE, not a gate: it says this document carries our
// signatures, and it is never used to decide whether "Verify" is
// offered. A document signed somewhere else is the one that most needs
// checking.
func (a *App) store(ref string, env *envelope.Envelope) error {
	s, err := envelope.Encode(env)
	if err != nil {
		return err
	}
	if err := a.H.StateSet(ref, envelope.StateKey, s); err != nil {
		return err
	}
	a.markTurns(ref, env)
	if env.Status.Closed() {
		if err := a.H.StateDelete(ref, envelope.PendingKey); err != nil {
			a.logf("warn", "clearing the pending marker: %v", err)
		}
		return nil
	}
	if err := a.H.StateSet(ref, envelope.PendingKey, "1"); err != nil {
		a.logf("warn", "setting the pending marker: %v", err)
	}
	return nil
}

// markTurns keeps one PERSONAL marker per signer with an account:
// `todo@<user id>` (wire.PersonalState) while it is their turn and they have
// not answered, gone otherwise. filex shows it to that person only, as
// `todo@me`, and "Sign / Fill" asks for exactly that key — so the menu offers
// it to the people who have something to sign on this file and to nobody
// else (v0.43.0 wave 2: it was offered on every pending document, and the
// page then told a non-signer they were not one). The page still checks;
// the marker only decides what the menu offers.
func (a *App) markTurns(ref string, env *envelope.Envelope) {
	for _, sg := range env.Signers {
		if sg.Person.UserID == 0 {
			continue
		}
		key := wire.PersonalState(envelope.TodoKey, sg.Person.UserID)
		var err error
		if !env.Status.Closed() && !sg.Done() && env.Turn(sg.ID) {
			err = a.H.StateSet(ref, key, "1")
		} else {
			err = a.H.StateDelete(ref, key)
		}
		if err != nil {
			a.logf("warn", "the turn marker of %s: %v", sg.Person.Display(), err)
		}
	}
}

// labelsOf builds the editor's signer list from people (wizard step 1),
// with the same positional ids the request job assigns.
func labelsOf(people []envelope.Person) []views.SignerLabel {
	var signers []envelope.Signer
	for i, p := range people {
		signers = append(signers, envelope.Signer{ID: fmt.Sprintf("s%d", i+1), Person: p})
	}
	return views.SignerLabels(signers)
}

// CertifyPermission is the DocMDP permission the first signature certifies
// a request's document with: filling in forms and signing, nothing else.
const CertifyPermission = 2

// signerIDs lists a request's signers in order.
func signerIDs(env *envelope.Envelope) []string {
	out := make([]string, 0, len(env.Signers))
	for _, s := range env.Signers {
		out = append(out, s.ID)
	}
	return out
}

// ownSigSpec is the field THIS signature goes into: the box the signature
// widget is drawn in (plan.Visible), or the signer's invisible field.
func ownSigSpec(info *pdfsig.Info, all []envelope.Field, visible *envelope.Field, signerID string, l views.Lang) pdfdoc.FieldSpec {
	if visible != nil {
		if sp, ok := specFor(info, all, *visible, l); ok {
			sp.Kind = pdfdoc.KindSig
			return sp
		}
	}
	name := "signature"
	if signerID != "" {
		name += "-" + signerID
	}
	return pdfdoc.FieldSpec{Name: name, Page: 1, Kind: pdfdoc.KindSig, Hidden: true}
}

// lockForGood keeps the signed file under filex's lock with no end: the
// request's "lock the signed file when every signature is in". A new
// version IS the document the request was on; a file beside it is this
// job's output, whose lock the host takes the moment it is written.
//
// ⚠ A lock that could not be taken is said, not hidden: the request is
// complete and sealed either way, and the certification and the hash
// every party holds still show any change — but the requester asked for a
// lock and must learn that there is none.
func (a *App) lockForGood(ref string, env *envelope.Envelope, out envelope.Output, outRef wire.OutputRef) {
	target := outRef.Ref
	if out.Mode == envelope.OutputVersion {
		target = ref
	}
	if _, err := a.H.FileLockMessage(target, pluginkit.LockUntilLifted, LockSealed, nil); err != nil {
		a.logf("warn", "locking the signed file: %v", err)
		title := views.T("The signed file could not be locked", "İmzalı dosya kilitlenemedi")
		body := views.Tf("“%s” is signed by everybody and sealed, but the lock you asked for could not be taken (%s). The certification and the SHA-256 every party was sent still show any change.",
			"“%s” herkesçe imzalandı ve mühürlendi, ancak istediğiniz kilit alınamadı (%s). Onay ve her tarafa gönderilen SHA-256 özeti her değişikliği yine gösterir.",
			env.Document, shortErr(err))
		a.notifyRequester(ref, env, title, body, "warning")
		return
	}
	env.Sealed.LockedForGood = true
}
