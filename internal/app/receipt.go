package app

import (
	"crypto/x509"
	"encoding/base64"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/receipt"
	"github.com/brf-tech/filex-sign/internal/views"
)

// ── The receipt ────────────────────────────────────────────────────────
//
// A signer is handed their own certificate the moment their signature
// lands — not when the whole request finishes, and never through the
// signing link. The last signature REVOKES every open signing link, so a
// receipt attached to one would be taken away from the person the moment
// somebody else signed. It gets a share of its own.

// handReceipt gives the signer their certificate and returns the PIN of
// their receipt share, when one was minted (it is shown to the requester
// and never mailed, like every other PIN here).
func (a *App) handReceipt(ref string, env *envelope.Envelope, sg *envelope.Signer, res *signResult) string {
	if sg == nil || res == nil || res.Cert == nil {
		return ""
	}
	ca := a.ca()
	files, err := receipt.Build(receipt.Input{
		Document: env.Document, Identity: sg.Person.Identity(), SignedAt: sg.SignedAt,
		Leaf: res.Cert, Authority: issuerOf(res, ca.Live), Locale: env.Options.Locale,
		Timestamped: res.Timestamped,
	})
	if err != nil {
		a.logf("warn", "building the receipt: %v", err)
		return ""
	}
	if sg.Internal() {
		// Somebody with an account gets it where they already are: the
		// notification opens the Verify screen on the document itself.
		title, body := receiptNotice(env, sg, res, ca)
		if _, err := a.H.NotifySend(pluginkit.Notice{
			Title: title, Body: body, Severity: "info", ToUserID: sg.Person.UserID,
			Meta:   map[string]any{"envelope": env.ID, "document": env.Document, "cert": res.CertFP},
			Target: &pluginkit.NoticeTarget{Ref: ref, View: ViewVerify},
		}); err != nil {
			a.logf("warn", "receipt notice: %v", err)
		}
		return ""
	}

	refs, err := a.writeReceiptFiles(files)
	if err != nil {
		a.logf("warn", "writing the receipt files: %v", err)
		return ""
	}
	pin := "auto"
	if env.Options.PIN == "none" {
		pin = ""
	}
	share, err := a.H.ShareCreate(pluginkit.PageCreate{
		PageID: PageReceipt,
		// No subject, for the signing link's reason (request.go): a frozen
		// string in the requester's language under a title that is drawn
		// in the visitor's.
		Subject: "",
		PIN:     pin, TTLDays: receiptTTLDays,
		State: map[string]any{"envelope_id": env.ID, "signer_id": sg.ID},
		Files: refs,
	})
	if err != nil {
		a.logf("warn", "receipt share: %v", err)
		return ""
	}
	sg.ReceiptToken, sg.ReceiptURL, sg.ReceiptExpires = share.Token, share.URL, share.ExpiresAt
	if sg.Person.HasEmail() {
		subject, body := receiptMail(env.Options.Locale, env, sg, res, ca)
		if err := a.H.MailSend(sg.Person.Email, subject, body); err != nil {
			a.logf("warn", "receipt mail to %s: %v", sg.Person.Email, err)
		}
	}
	return share.PIN
}

// writeReceiptFiles puts the three artefacts into the call so the share
// can copy them out for the visitor. They are NOT returned as job
// outputs: a receipt belongs to the signer, not to the requester's
// folder.
func (a *App) writeReceiptFiles(f *receipt.Files) ([]wire.OutputRef, error) {
	var out []wire.OutputRef
	for _, x := range []struct {
		name string
		data []byte
	}{{f.P7BName, f.P7B}, {f.PEMName, f.PEM}, {f.TextName, f.Text}} {
		ref, err := a.H.WriteOutput(x.name, x.data)
		if err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, nil
}

func issuerOf(res *signResult, live *x509.Certificate) *x509.Certificate {
	if len(res.Chain) > 0 {
		return res.Chain[0]
	}
	return live
}

// receiptOf rebuilds one signer's receipt from what the document keeps,
// for the screen that shows it again later.
func (a *App) receiptOf(ref string, env *envelope.Envelope, sg *envelope.Signer, l views.Lang, inApp bool) views.ReceiptInput {
	ca := a.ca()
	in := views.ReceiptInput{
		Document: env.Document, Signer: sg.Person, SignedAt: envelope.Day(sg.SignedAt),
		Serial: sg.CertSerial, CertFP: sg.CertFP, CAName: ca.Name, CAFP: ca.FP, InApp: inApp,
	}
	if der, found, err := a.H.StateGet(ref, envelope.CertPrefix+sg.ID); err == nil && found {
		if raw, err := base64.StdEncoding.DecodeString(der); err == nil {
			if leaf, err := x509.ParseCertificate(raw); err == nil {
				if in.Serial == "" {
					in.Serial = leaf.SerialNumber.Text(16)
				}
				if in.CertFP == "" {
					in.CertFP = receipt.Fingerprint(leaf)
				}
				stem := receipt.Stem(env.Document, sg.Person.Identity())
				in.Files = []views.ReceiptFile{
					{Name: stem + ".p7b", What: views.T("your certificate and the authority's, as a PKCS#7 bundle",
						"sertifikanız ve makamın sertifikası, PKCS#7 paketi olarak")},
					{Name: stem + ".pem", What: views.T("the same two certificates as text", "aynı iki sertifika, metin olarak")},
					{Name: stem + ".txt", What: views.T("the facts in words, with both fingerprints", "bilgiler düz metin olarak, iki parmak iziyle birlikte")},
				}
			}
		}
	}
	return in
}

// ── delivering the finished document ───────────────────────────────────

// linkSection is the section of the Signatures page a link opens, for the
// person whose link it is — the one the job runs as (a link lists in its
// creator's My shares). The requester follows the request under "I asked for
// these"; anybody else (the last signer, when they are inside and their own
// job finished the request) under "I have signed these".
func linkSection(env *envelope.Envelope, actor wire.Actor) string {
	if env != nil && actor.ID != 0 && env.Requester.UserID == actor.ID {
		return views.SectionRequested
	}
	return views.SectionSigned
}

// deliver opens ONE share of the signed document and hands its link to
// every signer, each the way this app reaches them. The PIN — when the
// requester asked for one — is returned so it can be shown to them; it is
// never mailed, exactly as with the signing links.
//
// ⚠⚠ The SAME question decides here as at the invitation, the reminder
// and the receipt: what is in the signer's identity. Somebody with an
// account is told IN filex and NEVER mailed — and they DO carry an
// address, because the directory lookup put one there, so a plain
// "has an e-mail address?" quietly mails every colleague in the request.
// That is the bug this gate closes; the link they are given is the same
// one the mail carries, so nobody loses the finished document.
//
// ⚠⚠ Every party is told that the request is complete, with the SHA-256 of
// the sealed file, whether or not the requester chose to SEND the document
// (the owner, 2026-09-22: "the hash goes to everyone"). The share is made
// only when they did; without it, the notice says the requester will hand
// the document over.
func (a *App) deliver(ref string, env *envelope.Envelope, out wire.OutputRef, name, section string) string {
	pin := ""
	if env.Options.Sends() {
		pin = a.deliveryShare(env, out, name, section)
	}
	for i := range env.Signers {
		sg := &env.Signers[i]
		switch {
		case sg.Internal():
			title, body := deliveryNotice(env, sg)
			if _, err := a.H.NotifySend(pluginkit.Notice{
				Title: title, Body: body, Severity: "info", ToUserID: sg.Person.UserID,
				Meta:   map[string]any{"envelope": env.ID, "document": env.Document},
				Target: &pluginkit.NoticeTarget{Ref: ref, View: ViewVerify},
			}); err != nil {
				a.logf("warn", "completion notice to %s: %v", sg.Person.Identity(), err)
			}
		case sg.Person.HasEmail():
			subject, body := deliveryMail(env.Options.Locale, env, sg)
			if err := a.H.MailSend(sg.Person.Email, subject, body); err != nil {
				a.logf("warn", "completion mail to %s: %v", sg.Person.Email, err)
			}
		}
	}
	return pin
}

// deliveryShare opens the share of the signed document and records it on
// the envelope; the PIN comes back ("" when none, or when it failed).
func (a *App) deliveryShare(env *envelope.Envelope, out wire.OutputRef, name, section string) string {
	pin := "auto"
	if env.Options.DeliveryPIN == "none" {
		pin = ""
	}
	share, err := a.H.ShareCreate(pluginkit.PageCreate{
		// No page_id: this is a plain filex share of a file, the kind the
		// administrator already sees and revokes in Shares. Its subject is
		// the document's own name, which is nobody's language.
		Subject: env.Document, PIN: pin, TTLDays: receiptTTLDays,
		State: map[string]any{"envelope_id": env.ID, "kind": "delivery"},
		Files: []wire.OutputRef{{Ref: out.Ref, Name: name}},
		// ⚠ Named for what it is. A page-less link has no manifest page to
		// declare a purpose, so it listed in My shares as a share somebody
		// made by hand, beside the signing links marked as a signing request
		// (the owner's decision, 2026-09-21: they belong to the request).
		Purpose: &wire.PagePurpose{
			Label: views.T("Signed copy", "İmzalı kopya"),
			Revoke: views.T("Revoking this link takes the signed copy away from everybody it was sent to. The signed document itself is not touched.",
				"Bu bağlantıyı iptal etmek, imzalı kopyayı gönderildiği herkesten geri alır. İmzalı belgenin kendisine dokunulmaz."),
			Section: section,
		},
	})
	if err != nil {
		a.logf("warn", "delivery share: %v", err)
		return ""
	}
	env.DeliveryToken, env.DeliveryURL, env.DeliveryExpires = share.Token, share.URL, share.ExpiresAt
	return share.PIN
}

// ── the receipt share's own screen ─────────────────────────────────────

func (a *App) pageReceipt(in *wire.ViewEventInput) (*wire.Surface, error) {
	l := langOfView(in)
	var ps pageState
	if err := a.H.ShareState("", &ps); err != nil {
		a.logf("warn", "receipt share state: %v", err)
	}
	env, err := a.load(docRef)
	if err != nil {
		return views.Gone(views.T("This receipt no longer exists.", "Bu makbuz artık yok.")), nil
	}
	sg := env.Signer(ps.SignerID)
	if sg == nil || ps.EnvelopeID != env.ID {
		return views.Gone(views.T("This link does not belong to a signature of this document.",
			"Bu bağlantı bu belgenin bir imzasına ait değil.")), nil
	}
	return views.Receipt(l, a.receiptOf(docRef, env, sg, l, false)), nil
}
