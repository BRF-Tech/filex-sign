package app

import (
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/pdfdoc"
	"github.com/brf-tech/filex-sign/internal/views"
)

// auditLang is the language a request's audit trail is written in: the
// REQUESTER's, as their request job recorded it (Options.Locale). The trail
// is their record of the round, so it speaks their language whoever's job
// happens to write it — usually the last signer's, who may be an outsider
// reading English. `fallback` is the running job's own language, for a
// request recorded before the locale was.
//
// ⚠ The audit PDF was English-only while every screen, notice and mail of
// the same flow followed the person's language (found by an audit of
// milestone 3, before the first release): a Turkish requester got a Turkish flow and an English record of
// it.
func auditLang(env *envelope.Envelope, fallback string) views.Lang {
	if env != nil && env.Options.Locale != "" {
		return views.Of(env.Options.Locale)
	}
	return views.Of(fallback)
}

// auditPDF writes the trail of a finished request: who was asked, what
// each of them did and when, which certificate carried their signature,
// and what they typed into the document. It is a separate file on
// purpose — appending it to the signed PDF would break the signatures it
// is meant to explain.
//
// ⚠ Every letter it prints must be in the embedded Inter subset, or it is
// borrowed from another face mid-word. TestAudit_TheTrailSpeaksTheRequestersLanguage
// checks every rune of the Turkish trail against the subset.
func (a *App) auditPDF(env *envelope.Envelope, l views.Lang) ([]byte, error) {
	lines, title := a.auditLines(env, l)
	return pdfdoc.Audit(title, lines)
}

// auditLines is the trail as text, apart from the PDF it is drawn into, so
// a test can read what a person would read.
func (a *App) auditLines(env *envelope.Envelope, l views.Lang) ([]pdfdoc.AuditLine, string) {
	ca := a.ca()
	read := func(t wire.Text) string { return views.In(t, l) }
	line := func(s string) pdfdoc.AuditLine { return pdfdoc.AuditLine{Text: s} }
	muted := func(s string) pdfdoc.AuditLine { return pdfdoc.AuditLine{Text: s, Muted: true} }
	head := func(s string) pdfdoc.AuditLine { return pdfdoc.AuditLine{Text: s, Head: true} }
	yes := func(b bool) string {
		if b {
			return l.S("yes", "evet")
		}
		return l.S("no", "hayır")
	}
	order := l.S("everybody at once", "hepsi aynı anda")
	if env.Sequential() {
		order = l.S("one after another", "birer birer")
	}
	pin := l.S("a PIN per outside signer", "dış imzacı başına PIN")
	if env.Options.PIN == "none" {
		pin = l.S("no PIN", "PIN yok")
	}

	lines := []pdfdoc.AuditLine{
		line(l.S("Document: ", "Belge: ") + env.Document),
		line(l.S("Request: ", "İstek: ") + env.ID + " · " + read(views.StatusWords(env.Status))),
		line(l.S("Requested by: ", "İsteyen: ") + env.Requester.Identity()),
		muted(l.Sf("Opened %s · closed %s", "Açıldı %s · kapandı %s", env.CreatedAt, orDash(env.ClosedAt))),
		muted(l.Sf("Order: %s · links %d days · deadline %s · %s · file frozen: %s",
			"Sıra: %s · bağlantılar %d gün · son tarih %s · %s · dosya donduruldu: %s",
			order, env.Options.ExpiryDays, orDash(env.Options.Deadline), pin, yes(env.Options.Lock))),
	}
	if env.Options.Message != "" {
		lines = append(lines, muted(l.S("Message: ", "Mesaj: ")+env.Options.Message))
	}

	// ⚠⚠ "Participants", not "Signers": a request may hold somebody who
	// only fills boxes in (the owner, 2026-09-23), and the trail names the
	// act each of them performed rather than one word for all of them.
	lines = append(lines, head(l.S("Participants (identity: a name, an e-mail address, or both)", "Katılımcılar (kimlik: ad, e-posta adresi ya da ikisi)")))
	for i, sg := range env.Signers {
		signs := env.Signs(sg.ID)
		where := l.S("outside signer, private link", "dış imzacı, özel bağlantı")
		if sg.Internal() {
			where = l.S("signed in filex", "filex içinde imzaladı")
		}
		if !signs {
			where = l.S("outside participant, private link", "dış katılımcı, özel bağlantı")
			if sg.Internal() {
				where = l.S("filled in inside filex", "filex içinde doldurdu")
			}
		}
		lines = append(lines, line(fmt.Sprintf("%d. %s - %s (%s)", i+1, sg.Person.Identity(), read(views.ActWords(sg.Status, signs)), where)))
		var when []string
		// ⚠ Both words stay WRITTEN HERE, each with a timestamp of its own,
		// and only one of the two is ever set. internal/i18n refuses a text
		// whose words are not literals at the call site, and a sentence it
		// cannot read is a sentence that stays English in three languages.
		signedAt, filledAt := sg.SignedAt, ""
		if !signs {
			signedAt, filledAt = "", sg.SignedAt
		}
		for _, p := range []struct{ en, tr, at string }{
			{"invited", "davet edildi", sg.NotifiedAt}, {"reminded", "hatırlatıldı", sg.RemindedAt},
			{"opened", "açtı", sg.ViewedAt},
			{"signed", "imzaladı", signedAt}, {"filled in", "doldurdu", filledAt},
			{"refused", "reddetti", sg.DeclinedAt},
		} {
			if p.at != "" {
				when = append(when, l.S(p.en, p.tr)+" "+p.at)
			}
		}
		if sg.SignedIP != "" {
			when = append(when, l.S("from ", "adres ")+sg.SignedIP)
		}
		if len(when) > 0 {
			lines = append(lines, muted("   "+strings.Join(when, " · ")))
		}
		if sg.CertSerial != "" {
			lines = append(lines, muted("   "+l.Sf("certificate %s, valid to %s", "sertifika %s, %s tarihine kadar geçerli", sg.CertSerial, orDash(sg.CertExpires))))
		}
		if sg.CertFP != "" {
			lines = append(lines, muted("   SHA-256 "+sg.CertFP))
		}
		if sg.DeclineReason != "" {
			lines = append(lines, muted("   "+l.S("reason: ", "gerekçe: ")+sg.DeclineReason))
		}
	}

	var filled []pdfdoc.AuditLine
	for _, f := range env.Fields {
		if fields.Drawn(f.Type) || f.Value == "" {
			continue
		}
		// The box's name as the requester typed it, or its kind's name in the
		// trail's language — never the internal id ("text-1"), which an
		// unnamed box would otherwise print now that a default name is no
		// longer written into the record.
		name := views.NameIn(l, env.Fields, f)
		// ⚠ A name in a script the trail's embedded faces do not carry
		// (Japanese, say — names are free text in any script) would lose
		// every letter and print "(page 1): …" with nothing before it. The
		// kind's name stands in, so the line still says which box it was.
		if !fontkit.Covers(fontkit.Get(fontkit.Inter), name) {
			name = views.LabelFor(l, envelope.Field{Type: f.Type}, 1)
		}
		by := f.SignedBy
		if sg := env.Signer(f.SignedBy); sg != nil {
			by = sg.Person.Display()
		}
		filled = append(filled, line(l.Sf("%s (page %d): %s - %s", "%s (sayfa %d): %s - %s", name, f.Page, f.Value, orDash(by))))
	}
	if len(filled) > 0 {
		lines = append(lines, head(l.S("Filled in", "Doldurulanlar")))
		lines = append(lines, filled...)
	}

	lines = append(lines, head(l.S("Event log", "Olay günlüğü")))
	for _, e := range env.Events {
		who := e.Signer
		if sg := env.Signer(e.Signer); sg != nil {
			who = sg.Person.Display()
		}
		lines = append(lines, muted(strings.TrimSpace(fmt.Sprintf("%s  %-10s %s %s %s", e.At, eventWord(l, e.Type, env.Signs(e.Signer)), who, e.IP, eventNote(l, env, e)))))
	}
	lines = append(lines, head(l.S("Signing authority", "İmza makamı")))
	if ca.Name != "" {
		lines = append(lines, line(ca.Name), muted("SHA-256 "+ca.FP))
		if ca.Retired > 0 {
			lines = append(lines, muted(l.Sf("%d retired authority/authorities are still trusted for the signatures they made",
				"Kullanımdan kaldırılan %d makam, attığı imzalar için hâlâ güvenilir sayılıyor", ca.Retired)))
		}
	} else {
		lines = append(lines, muted(l.S("not available when this trail was written", "Bu iz yazılırken erişilemiyordu")))
	}

	// The completion: what the document permits, filex's seal, and the
	// SHA-256 every party was sent — the one fact that lets anybody, without
	// filex, check that a copy is the file that was signed.
	if env.Certified > 0 || env.Sealed != nil {
		lines = append(lines, head(l.S("Certification and seal", "Onay ve mühür")))
		if env.Certified > 0 {
			lines = append(lines, line(l.S("Certified by the first signature: after it, only filling in the form and signing were permitted (DocMDP P=2).",
				"İlk imzayla onaylandı: sonrasında yalnızca formu doldurmaya ve imzalamaya izin verildi (DocMDP P=2).")))
		}
		if s := env.Sealed; s != nil {
			lines = append(lines,
				line(l.Sf("Sealed by filex %s: no change of any kind is permitted after the seal.",
					"filex tarafından %s tarihinde mühürlendi: mühürden sonra hiçbir değişikliğe izin verilmiyor.", s.At)),
				muted("   "+l.S("seal certificate SHA-256 ", "mühür sertifikası SHA-256 ")+s.SealFP),
				line(l.Sf("SHA-256 of the signed file (%s), sent to every party:", "İmzalı dosyanın (%s) her tarafa gönderilen SHA-256 özeti:", orDash(s.Output))),
				line("   "+s.SHA256),
			)
			if s.LockedForGood {
				lines = append(lines, muted(l.S("The signed file is locked for good in filex; only an administrator can lift the lock, and lifting it is recorded.",
					"İmzalı dosya filex'te kalıcı olarak kilitlendi; kilidi yalnızca bir yönetici kaldırabilir ve kaldırma kayda geçer.")))
			}
		}
	}

	lines = append(lines,
		head(l.S("How to verify", "Nasıl doğrulanır")),
		muted(l.S("Every signature is PAdES-B (ETSI.CAdES.detached, ECDSA P-256 / SHA-256) made by a certificate this installation issued for that one signing. Right-click the document in filex and choose Verify for the full report; outside filex, import the authority certificate above into your PDF reader once.",
			"Her imza PAdES-B'dir (ETSI.CAdES.detached, ECDSA P-256 / SHA-256) ve bu kurulumun yalnız o imza için verdiği bir sertifikayla atılmıştır. Tam rapor için filex'te belgeye sağ tıklayıp Doğrula'yı seçin; filex dışında yukarıdaki makam sertifikasını PDF okuyucunuza bir kez içe aktarın.")),
		// The same paragraph as every screen, word for word: a wrong
		// expectation is worse than no signature at all.
		muted(read(views.Expectation(l))),
		muted(l.S("This trail was written by the filex e-Signature app when the request closed. It is a separate file: adding pages to the signed document would break the signatures.",
			"Bu iz, istek kapandığında filex e-İmza uygulaması tarafından yazıldı. Ayrı bir dosyadır: imzalı belgeye sayfa eklemek imzaları bozardı.")),
	)
	return lines, l.S("Signature audit trail - ", "İmza denetim izi - ") + env.Document
}

// eventWord is an event type as the trail prints it.
// eventWord is one event in the trail's language. ⚠ `signs` is what the
// participant of THIS event was asked for: the record keeps one event for
// "did their part", and the trail has two words for it, because a person
// who was never asked for a signature did not sign (the owner,
// 2026-09-23).
func eventWord(l views.Lang, typ string, signs bool) string {
	if typ == envelope.EvSigned && !signs {
		return l.S("filled in", "doldurdu")
	}
	words := map[string]struct{ en, tr string }{
		"created":            {en: "created", tr: "oluşturuldu"},
		envelope.EvNotified:  {en: "invited", tr: "davet edildi"},
		envelope.EvViewed:    {en: "opened", tr: "açtı"},
		envelope.EvReminded:  {en: "reminded", tr: "hatırlatıldı"},
		envelope.EvSigned:    {en: "signed", tr: "imzaladı"},
		envelope.EvDeclined:  {en: "refused", tr: "reddetti"},
		"completed":          {en: "completed", tr: "tamamlandı"},
		envelope.EvCancelled: {en: "cancelled", tr: "iptal"},
		envelope.EvExpired:   {en: "expired", tr: "süre doldu"},
		envelope.EvLinkEnded: {en: "link ended", tr: "bağlantı sonlandı"},
	}
	if w, ok := words[typ]; ok {
		return l.S(w.en, w.tr)
	}
	return typ
}

// eventNote is an event's note in the trail's language. The notes the app
// writes itself are said again in that language; anything a person typed
// (a refusal's reason) is theirs and printed as they wrote it.
func eventNote(l views.Lang, env *envelope.Envelope, e envelope.Event) string {
	switch e.Type {
	case "created":
		return l.Sf("%d signer(s)", "%d imzacı", len(env.Signers))
	case envelope.EvLinkEnded:
		if e.Note == "deleted" {
			return l.S("deleted on the Shares screen", "Paylaşımlar ekranında silindi")
		}
		return l.S("revoked on the Shares screen", "Paylaşımlar ekranında iptal edildi")
	}
	return e.Note
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
