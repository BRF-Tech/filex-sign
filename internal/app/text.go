package app

import (
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/views"
)

// ── mail and notification wording ──────────────────────────────────────
//
// Who hears what is decided by ONE question: what is in the signer's
// identity.
//
//	an account here    → filex's own notification, which carries them
//	                     straight into the signing screen
//	an e-mail address  → a mail with their private link
//	a name and nothing → nothing is sent; the requester was shown the
//	                     link and the PIN and hands them over
//
// ⚠ A PIN is NEVER in a mail — not the signing link's, not the receipt
// share's, not the delivery share's. Every one of them goes to the
// requester, through the job message and the bell.
//
// ⚠ Every sentence below is ONE English–Turkish pair (`l.Sf`), in the
// request's language: the other languages come from internal/i18n keyed
// by that English, so a sentence split differently in two languages, or
// built in a `"tr"` branch of its own, is one no catalogue can reach
// (2026-09-22, when es/de/fr were added).

func inviteMail(locale string, env *envelope.Envelope, sg *envelope.Signer) (subject, body string) {
	l := views.Of(locale)
	who := env.Requester.Identity()
	// ⚠ A participant with no signature box of their own is asked to FILL
	// the document in, and the mail that reaches them says that (the owner,
	// 2026-09-23). Two whole sentences, not one sentence assembled out of
	// pieces: a sentence built at run time is one no catalogue can hold.
	signs := env.Signs(sg.ID)
	subject = l.Sf("%s asks you to fill in a document: %s", "%s sizden bir belgeyi doldurmanızı istiyor: %s", who, env.Document)
	if signs {
		subject = l.Sf("%s asks you to sign a document: %s", "%s sizden bir belgeyi imzalamanızı istiyor: %s", who, env.Document)
	}
	var b strings.Builder
	if signs {
		b.WriteString(l.Sf("Hello %s,\n\n%s asks you to electronically sign “%s”.\n\n",
			"Merhaba %s,\n\n%s sizden “%s” belgesini elektronik olarak imzalamanızı istiyor.\n\n", sg.Person.Identity(), who, env.Document))
	} else {
		b.WriteString(l.Sf("Hello %s,\n\n%s asks you to fill in “%s”. You are not asked for a signature.\n\n",
			"Merhaba %s,\n\n%s sizden “%s” belgesini doldurmanızı istiyor. Sizden imza istenmiyor.\n\n", sg.Person.Identity(), who, env.Document))
	}
	if env.Options.Message != "" {
		b.WriteString(l.Sf("Message: %s\n\n", "Mesaj: %s\n\n", env.Options.Message))
	}
	if signs {
		b.WriteString(l.Sf("Open this link to sign:\n%s\n\n", "İmzalamak için bağlantı:\n%s\n\n", sg.PageURL))
	} else {
		b.WriteString(l.Sf("Open this link to fill it in:\n%s\n\n", "Doldurmak için bağlantı:\n%s\n\n", sg.PageURL))
	}
	if env.Options.PIN != "none" {
		b.WriteString(l.S("The link is protected by a PIN, which you will receive separately (not in this e-mail).\n\n",
			"Bağlantı bir PIN ile korunuyor; PIN size ayrıca iletilecek (bu e-postada değil).\n\n"))
	}
	if env.Options.Deadline != "" {
		if signs {
			b.WriteString(l.Sf("Please sign by %s.\n", "Son imza tarihi: %s\n", views.Day(env.Options.Deadline)))
		} else {
			b.WriteString(l.Sf("Please finish by %s.\n", "Son tamamlama tarihi: %s\n", views.Day(env.Options.Deadline)))
		}
	}
	if sg.PageExpires != "" {
		b.WriteString(l.Sf("The link is valid until %s.\n", "Bağlantı %s tarihine kadar geçerli.\n", views.Day(sg.PageExpires)))
	}
	if env.Sequential() {
		b.WriteString(l.S("This document is signed one signer at a time, and it is your turn.\n",
			"Bu belge birer birer imzalanıyor; sıra sizde.\n"))
	}
	if env.Options.AllowDecline {
		if signs {
			b.WriteString(l.S("If you will not sign, the page has a button that says so.\n",
				"İmzalamak istemiyorsanız sayfadaki “İmzalamayacağım” düğmesini kullanabilirsiniz.\n"))
		} else {
			b.WriteString(l.S("If you will not take part, the page has a button that says so.\n",
				"Katılmak istemiyorsanız sayfadaki “Katılmayacağım” düğmesini kullanabilirsiniz.\n"))
		}
	}
	if signs {
		b.WriteString(l.S("\nNo account is needed. Your signature is applied with a certificate issued in your name, for this one signing, by the signing authority of the requester's filex; a receipt reaches you the moment you are done.\n",
			"\nHesap açmanız gerekmez. İmzanız, isteği gönderenin filex kurulumundaki imza makamının bu tek işlem için adınıza ürettiği bir sertifikayla belgeye eklenir; imzanız biter bitmez size bir makbuz iletilir.\n"))
	} else {
		b.WriteString(l.S("\nNo account is needed. Nothing of yours is drawn on the page, but what you fill in is sealed to your name with a certificate issued for this one submission by the signing authority of the requester's filex; a receipt reaches you the moment you are done.\n",
			"\nHesap açmanız gerekmez. Sayfaya sizden bir şey çizilmez, ancak doldurduklarınız, isteği gönderenin filex kurulumundaki imza makamının bu tek gönderim için adınıza ürettiği bir sertifikayla mühürlenir; işiniz biter bitmez size bir makbuz iletilir.\n"))
	}
	return subject, b.String()
}

func reminderMail(locale string, env *envelope.Envelope, sg *envelope.Signer) (subject, body string) {
	l := views.Of(locale)
	s, b := inviteMail(locale, env, sg)
	return l.Sf("Reminder: %s", "Hatırlatma: %s", s), l.S("This is a reminder.\n\n", "Bu bir hatırlatmadır.\n\n") + b
}

// receiptMail tells an outside signer where their receipt is. The
// certificate itself is on the page, not in the mail: filex's mail is
// plain text and carries no attachment.
func receiptMail(locale string, env *envelope.Envelope, sg *envelope.Signer, res *signResult, ca authority) (subject, body string) {
	l := views.Of(locale)
	subject = l.Sf("Your signature receipt: %s", "İmza makbuzunuz: %s", env.Document)
	var b strings.Builder
	b.WriteString(l.Sf("Hello %s,\n\nYou signed “%s”. Your receipt is here:\n%s\n\n",
		"Merhaba %s,\n\n“%s” belgesini imzaladınız. Makbuzunuz burada:\n%s\n\n", sg.Person.Identity(), env.Document, sg.ReceiptURL))
	if env.Options.PIN != "none" {
		b.WriteString(l.S("The page is protected by a PIN, which you will receive separately (not in this e-mail).\n\n",
			"Sayfa bir PIN ile korunuyor; PIN size ayrıca iletilecek (bu e-postada değil).\n\n"))
	}
	b.WriteString(l.Sf("The SHA-256 fingerprint of your certificate:\n%s\n\n", "Sertifikanızın SHA-256 parmak izi:\n%s\n\n", res.CertFP))
	if ca.FP != "" {
		b.WriteString(l.Sf("Signing authority: %s\nSHA-256: %s\n\n", "İmza makamı: %s\nSHA-256: %s\n\n", ca.Name, ca.FP))
	}
	b.WriteString(l.S("This is an identity receipt, not a signing capability: it proves who signed and what was signed, and it cannot sign anything. The private key that made your signature was destroyed the moment the signature was written.\n",
		"Bu bir kimlik makbuzudur, imza yeteneği değildir: kimin neyi imzaladığını kanıtlar, kendisiyle hiçbir şey imzalanamaz. İmzanızı üreten özel anahtar imza yazılır yazılmaz yok edildi.\n"))
	if sg.ReceiptExpires != "" {
		b.WriteString(l.Sf("\nThe page stays open until %s; download the files and keep them.\n",
			"\nSayfa %s tarihine kadar açık; dosyaları indirip saklayın.\n", views.Day(sg.ReceiptExpires)))
	}
	return subject, b.String()
}

// deliveryMail is the completion notice for an OUTSIDE signer: the
// signed document (a filex link, when the requester chose to send it),
// and — always — the SHA-256 of the exact file, the seal's fingerprint and
// how to check both.
//
// ⚠⚠ The owner, 2026-09-22: "the hash goes to everyone". A party who
// holds the hash can prove, years later and without filex, that the file
// in front of them is the one everybody signed.
func deliveryMail(locale string, env *envelope.Envelope, sg *envelope.Signer) (subject, body string) {
	l := views.Of(locale)
	subject = l.Sf("The signed document is ready: %s", "İmzalı belge hazır: %s", env.Document)
	var b strings.Builder
	b.WriteString(l.Sf("Hello %s,\n\nEvery signature on “%s” is in.", "Merhaba %s,\n\n“%s” belgesinin bütün imzaları tamamlandı.",
		sg.Person.Identity(), env.Document))
	if env.DeliveryURL != "" {
		b.WriteString(l.Sf(" The signed document is here:\n%s\n\n", " İmzalı hâli burada:\n%s\n\n", env.DeliveryURL))
		if env.Options.DeliveryPIN != "none" {
			b.WriteString(l.S("The link is protected by a PIN, which you will receive separately (not in this e-mail).\n\n",
				"Bağlantı bir PIN ile korunuyor; PIN size ayrıca iletilecek (bu e-postada değil).\n\n"))
		}
		if env.DeliveryExpires != "" {
			b.WriteString(l.Sf("The link is valid until %s.\n\n", "Bağlantı %s tarihine kadar geçerli.\n\n", views.Day(env.DeliveryExpires)))
		}
	} else {
		b.WriteString(l.Sf(" %s will hand you the signed document.\n\n", " İmzalı belgeyi %s size iletecek.\n\n", env.Requester.Identity()))
	}
	b.WriteString(sealedBlock(l, env))
	return subject, b.String()
}

// sealedBlock is the part of every completion notice that proves the file:
// its SHA-256, the seal, and how to check both — with filex, or with the
// computer's own tools, which need no filex at all.
func sealedBlock(l views.Lang, env *envelope.Envelope) string {
	if env.Sealed == nil {
		return ""
	}
	name := env.Sealed.Output
	if name == "" {
		name = env.Document
	}
	var b strings.Builder
	b.WriteString(l.S("The document is sealed by filex: after the last signature, filex signed the whole document with its own seal. A PDF reader reports any change made after that as a change that is not permitted.\n\n",
		"Belge filex tarafından mühürlendi: son imzadan sonra filex, kendi mührüyle bütün belgeyi imzaladı. Bundan sonra yapılan her değişikliği bir PDF okuyucu “izin verilmeyen değişiklik” olarak gösterir.\n\n"))
	b.WriteString(l.Sf("SHA-256 of the signed file (%s):\n%s\n\n", "İmzalı dosyanın SHA-256 özeti (%s):\n%s\n\n", name, env.Sealed.SHA256))
	b.WriteString(l.Sf("Fingerprint of the seal's certificate (SHA-256):\n%s\n\n", "Mühür sertifikasının parmak izi (SHA-256):\n%s\n\n", env.Sealed.SealFP))
	b.WriteString(l.S("How to check:\n", "Nasıl doğrularsınız:\n"))
	b.WriteString(l.S("• In filex, open the file and choose “Verify”: it shows the signatures, the seal, and whether the file's hash is the one in this e-mail.\n",
		"• filex'te dosyayı açın ve “Doğrula”yı seçin: imzaları, mührü ve özetin bu e-postadakiyle aynı olduğunu gösterir.\n"))
	b.WriteString(l.Sf("• Or compute the hash yourself - Linux/macOS: sha256sum \"%s\" · Windows PowerShell: Get-FileHash \"%s\" -Algorithm SHA256 - and compare it with the one above. If a single character differs, the file was changed.\n",
		"• Ya da özeti kendiniz hesaplayın - Linux/macOS: sha256sum \"%s\" · Windows PowerShell: Get-FileHash \"%s\" -Algorithm SHA256 - ve yukarıdakiyle karşılaştırın. Tek bir karakteri bile farklıysa dosya değiştirilmiştir.\n", name, name))
	if env.Sealed.LockedForGood {
		b.WriteString(l.S("\nThe signed file is also locked for good in filex.\n", "\nİmzalı dosya ayrıca filex'te kalıcı olarak kilitlendi.\n"))
	}
	return b.String()
}

// lapsedMail tells an OUTSIDE signer that the request they were holding a
// link for has run out. The link is already revoked by the time this
// goes, so it says what to do instead of pointing at a dead page.
func lapsedMail(locale string, env *envelope.Envelope, sg *envelope.Signer) (subject, body string) {
	l := views.Of(locale)
	subject = l.Sf("The signature request expired: %s", "İmza isteğinin süresi doldu: %s", env.Document)
	var b strings.Builder
	b.WriteString(l.Sf("Hello %s,\n\nThe signature request for “%s” has expired, and your signing link no longer works.\n\n",
		"Merhaba %s,\n\n“%s” için imza isteğinin süresi doldu; imza bağlantınız artık çalışmıyor.\n\n", sg.Person.Identity(), env.Document))
	if env.Options.Deadline != "" {
		b.WriteString(l.Sf("The deadline was %s.\n\n", "Son imza tarihi %s idi.\n\n", views.Day(env.Options.Deadline)))
	}
	b.WriteString(l.Sf("If you still need to sign, speak to %s, who can open a new request.\n",
		"Hâlâ imzalamanız gerekiyorsa %s ile görüşün; yeni bir istek açabilir.\n", env.Requester.Identity()))
	return subject, b.String()
}

// withdrawnMail tells a signer WITHOUT an account that the request they
// were holding a link for was closed before everybody could sign — the
// closure has just revoked their link.
func withdrawnMail(locale string, env *envelope.Envelope, sg *envelope.Signer) (subject, body string) {
	l := views.Of(locale)
	subject = l.Sf("The signature request was closed: %s", "İmza isteği kapandı: %s", env.Document)
	var b strings.Builder
	b.WriteString(l.Sf("Hello %s,\n\nThe signature request for “%s” was closed before everybody had signed. Your signing link no longer works, and nothing more is asked of you.\n\n",
		"Merhaba %s,\n\n“%s” için imza isteği herkes imzalamadan kapatıldı; imza bağlantınız artık çalışmıyor ve sizden başka bir şey istenmiyor.\n\n",
		sg.Person.Identity(), env.Document))
	b.WriteString(l.Sf("If you still need to sign, speak to %s, who can open a new request.\n",
		"Hâlâ imzalamanız gerekiyorsa %s ile görüşün; yeni bir istek açabilir.\n", env.Requester.Identity()))
	return subject, b.String()
}

// withdrawnNotice is the same news for a signer WITH an account.
func withdrawnNotice(env *envelope.Envelope) (title, body wire.Text) {
	title = views.T("A signature request was closed", "Bir imza isteği kapandı")
	return title, views.Tf("“%s” was waiting for your signature; the request was closed before everybody had signed, and nothing more is asked of you.\nIf you still need to sign, %s can open a new request.",
		"“%s” imzanızı bekliyordu; istek herkes imzalamadan kapatıldı ve sizden başka bir şey istenmiyor.\nHâlâ imzalamanız gerekiyorsa %s yeni bir istek açabilir.",
		env.Document, env.Requester.Identity())
}

// lastEventNote is the note of the newest event of one type ("" if none).
func lastEventNote(env *envelope.Envelope, typ string) string {
	for i := len(env.Events) - 1; i >= 0; i-- {
		if env.Events[i].Type == typ {
			return env.Events[i].Note
		}
	}
	return ""
}

// lapsedNotice is the same news for a signer WITH an account.
func lapsedNotice(env *envelope.Envelope, sg *envelope.Signer) (title, body wire.Text) {
	title = views.T("A signature request expired", "Bir imza isteğinin süresi doldu")
	return title, views.Each(func(l views.Lang) string {
		var b strings.Builder
		b.WriteString(l.Sf("“%s” was waiting for your signature and the request has run out; it is closed and nothing more is asked of you.",
			"“%s” imzanızı bekliyordu ve isteğin süresi doldu; istek kapandı, sizden başka bir şey istenmiyor.", env.Document))
		if env.Options.Deadline != "" {
			b.WriteString(l.Sf("\nThe deadline was %s.", "\nSon imza tarihi %s idi.", views.Day(env.Options.Deadline)))
		}
		b.WriteString(l.Sf("\nIf you still need to sign, %s can open a new request.", "\nHâlâ imzalamanız gerekiyorsa %s yeni bir istek açabilir.", env.Requester.Identity()))
		return b.String()
	})
}

// deliveryNotice is the finished document for a signer WITH an account:
// the SAME link deliveryMail carries, in their bell instead of an inbox.
func deliveryNotice(env *envelope.Envelope, sg *envelope.Signer) (title, body wire.Text) {
	title = views.T("The signed document is ready", "İmzalı belge hazır")
	return title, views.Each(func(l views.Lang) string {
		var b strings.Builder
		b.WriteString(l.Sf("Every signature on “%s” is in.", "“%s” belgesinin bütün imzaları tamamlandı.", env.Document))
		if env.DeliveryURL != "" {
			b.WriteString(l.Sf(" The signed document is here:\n%s", " İmzalı hâli burada:\n%s", env.DeliveryURL))
			if env.Options.DeliveryPIN != "none" {
				b.WriteString(l.S("\nThe link is protected by a PIN, which you will receive separately.",
					"\nBağlantı bir PIN ile korunuyor; PIN size ayrıca iletilecek."))
			}
			if env.DeliveryExpires != "" {
				b.WriteString(l.Sf("\nThe link is valid until %s.", "\nBağlantı %s tarihine kadar geçerli.", views.Day(env.DeliveryExpires)))
			}
		}
		if env.Sealed != nil {
			b.WriteString("\n\n" + strings.TrimSpace(sealedBlock(l, env)))
		}
		return b.String()
	})
}

// inviteNotice is what a signer WITH an account sees in their bell: one
// click opens the signing screen on the document itself.
func inviteNotice(env *envelope.Envelope, sg *envelope.Signer) (title, body wire.Text) {
	signs := env.Signs(sg.ID)
	title = views.Tf("%s asks you to fill in a document", "%s sizden bir belgeyi doldurmanızı istiyor", env.Requester.Identity())
	if signs {
		title = views.Tf("%s asks you to sign a document", "%s sizden bir belgeyi imzalamanızı istiyor", env.Requester.Identity())
	}
	return title, views.Each(func(l views.Lang) string {
		var b strings.Builder
		if signs {
			b.WriteString(l.Sf("“%s” is waiting for your signature. Open it to sign and fill in the boxes marked for you.",
				"“%s” imzanızı bekliyor. Açıp imzalayın ve size ayrılmış kutuları doldurun.", env.Document))
		} else {
			b.WriteString(l.Sf("“%s” is waiting for you. Open it and fill in the boxes marked for you; no signature is asked of you.",
				"“%s” sizi bekliyor. Açıp size ayrılmış kutuları doldurun; sizden imza istenmiyor.", env.Document))
		}
		if env.Options.Message != "" {
			b.WriteString(l.Sf("\n%s says: %s", "\n%s diyor ki: %s", env.Requester.Identity(), env.Options.Message))
		}
		if env.Options.Deadline != "" {
			if signs {
				b.WriteString(l.Sf("\nPlease sign by %s.", "\nLütfen %s tarihine kadar imzalayın.", views.Day(env.Options.Deadline)))
			} else {
				b.WriteString(l.Sf("\nPlease finish by %s.", "\nLütfen %s tarihine kadar tamamlayın.", views.Day(env.Options.Deadline)))
			}
		}
		if env.Sequential() {
			b.WriteString(l.S("\nThis document is signed one signer at a time, and it is your turn.", "\nBu belge birer birer imzalanıyor; sıra sizde."))
		}
		return b.String()
	})
}

// receiptNotice is what a signer WITH an account gets the moment their
// signature lands: their certificate's fingerprint, and a click that
// opens the verification report on the document.
func receiptNotice(env *envelope.Envelope, sg *envelope.Signer, res *signResult, ca authority) (title, body wire.Text) {
	title = views.T("Your signature receipt", "İmza makbuzunuz")
	return title, views.Each(func(l views.Lang) string {
		var b strings.Builder
		b.WriteString(l.Sf("You signed “%s”.\nYour certificate: serial %s, SHA-256 %s.", "“%s” belgesini imzaladınız.\nSertifikanız: seri %s, SHA-256 %s.",
			env.Document, res.CertSerial, res.CertFP))
		if ca.FP != "" {
			b.WriteString(l.Sf("\nSigning authority: %s (SHA-256 %s).", "\nİmza makamı: %s (SHA-256 %s).", ca.Name, ca.FP))
		}
		b.WriteString(l.S("\nThis is an identity receipt, not a signing capability: the private key that made your signature was destroyed the moment the signature was written.",
			"\nBu bir kimlik makbuzudur, imza yeteneği değildir: imzanızı üreten özel anahtar imza yazılır yazılmaz yok edildi."))
		b.WriteString(l.S("\nOpen the document and choose “Verify” to see the report - the fingerprints there should match these.",
			"\nRaporu görmek için belgeyi açıp “Doğrula” deyin - oradaki parmak izleri bunlarla aynı olmalı."))
		return b.String()
	})
}

func reminderTitle(t wire.Text) wire.Text {
	return views.Each(func(l views.Lang) string { return l.Sf("Reminder: %s", "Hatırlatma: %s", views.In(t, l)) })
}

// requesterNotice is the bell notification after sending: the PINs, the
// links of anybody filex cannot reach, and whatever could not be
// delivered.
func requesterNotice(env *envelope.Envelope, pins map[string]string) wire.Text {
	inside := 0
	for _, sg := range env.Signers {
		if sg.Internal() {
			inside++
		}
	}
	return views.Each(func(l views.Lang) string {
		var b strings.Builder
		b.WriteString(l.Sf("Signature request for “%s” sent to %d signer(s), %d of them here in filex.",
			"“%s” için imza isteği %d imzacıya gönderildi; %d tanesi filex içinde.", env.Document, len(env.Signers), inside))
		// The day the HOST gave the links, not the day that was asked for:
		// the installation's share ceiling may have cut it, and this line is
		// where the requester finds out which it was.
		if until := linksUntil(env); until != "" {
			b.WriteString(l.Sf("\nThe signing links work until %s.", "\nİmza bağlantıları %s tarihine kadar çalışır.", views.Day(until)))
		}
		if env.Sequential() {
			if next := env.Next(); next != nil {
				b.WriteString(l.Sf("\nSigned one at a time - %s is first; the others hear from us in turn.",
					"\nBirer birer imzalanacak - ilk sıra %s; diğerlerine sırası gelince haber verilecek.", next.Person.Identity()))
			}
		}
		if env.Locked {
			b.WriteString(l.Sf("\nThe file is frozen until %s; nobody can change it while signatures are collected.",
				"\nDosya %s tarihine kadar dondurulmuş; imzalar toplanırken kimse değiştiremez.", views.Day(env.LockUntil)))
		}
		for i := range env.Signers {
			sg := &env.Signers[i]
			if sg.HandOver() && sg.PageURL != "" {
				b.WriteString(l.Sf("\n%s has no address - give them this link yourself: %s",
					"\n%s için adres yok - bu bağlantıyı siz iletin: %s", sg.Person.Identity(), sg.PageURL))
			}
			if pin := pins[sg.ID]; pin != "" {
				b.WriteString(l.Sf("\nPIN for %s: %s", "\n%s için PIN: %s", sg.Person.Identity(), pin))
			}
			if sg.MailError == "" {
				continue
			}
			if sg.Internal() {
				b.WriteString(l.Sf("\n%s could not be notified (%s) - ask them to open the file and use “Sign / Fill”.",
					"\n%s kişisine haber verilemedi (%s) - dosyayı açıp “İmzala / Doldur” demesini isteyin.", sg.Person.Identity(), views.ErrWords(sg.MailError)))
				continue
			}
			b.WriteString(l.Sf("\nMail to %s could not be sent (%s) - pass the link on yourself: %s",
				"\n%s adresine e-posta gönderilemedi (%s) - bağlantıyı kendiniz iletin: %s", sg.Person.Identity(), views.ErrWords(sg.MailError), sg.PageURL))
		}
		return b.String()
	})
}

// linksUntil is the earliest day an open signing link stops, as the host
// reported it at share_create ("" when nobody signs through a link).
func linksUntil(env *envelope.Envelope) string {
	first := ""
	var at time.Time
	for _, sg := range env.Signers {
		if sg.Done() || sg.PageExpires == "" {
			continue
		}
		t, err := parseStamp(sg.PageExpires)
		if err != nil {
			continue
		}
		if first == "" || t.Before(at) {
			first, at = sg.PageExpires, t
		}
	}
	return first
}

func noticeFor(kind string, env *envelope.Envelope, sg *envelope.Signer) (title, body wire.Text) {
	who := ""
	if sg != nil {
		who = sg.Person.Identity()
	}
	signed, total := env.Progress()
	switch kind {
	case "viewed":
		return views.Tf("%s opened the document", "%s belgeyi açtı", who),
			views.Tf("“%s”: %s opened it to sign.", "“%s”: %s imzalamak için açtı.", env.Document, who)
	case "signer_signed":
		body := views.Each(func(l views.Lang) string {
			s := l.Sf("“%s”: %s signed (%d/%d).", "“%s”: %s imzaladı (%d/%d).", env.Document, who, signed, total)
			if env.Sequential() {
				if next := env.Next(); next != nil {
					s += l.Sf(" %s is next and has been told.", " Sıra %s kişisinde; haber verildi.", next.Person.Identity())
				}
			}
			return s
		})
		return views.Tf("%s signed", "%s imzaladı", who), body
	case "completed":
		body := views.Each(func(l views.Lang) string {
			s := l.Sf("“%s” was signed by all %d signer(s). The document now carries every signature.",
				"“%s” belgesini %d imzacının hepsi imzaladı. Belge artık tüm imzaları taşıyor.", env.Document, total)
			if env.Sealed != nil {
				s += "\n\n" + strings.TrimSpace(sealedBlock(l, env))
			}
			return s
		})
		return views.T("All signatures collected", "Tüm imzalar toplandı"), body
	case "declined":
		reason := ""
		if sg != nil && sg.DeclineReason != "" {
			reason = " - " + sg.DeclineReason
		}
		return views.Tf("%s refused to sign", "%s imzalamayı reddetti", who),
			views.Tf("“%s”: %s will not sign%s. The request is closed and the other links no longer work.",
				"“%s”: %s imzalamayacak%s. İstek kapandı ve diğer bağlantılar artık çalışmıyor.", env.Document, who, reason)
	case "cancelled":
		return views.T("Signature request cancelled", "İmza isteği iptal edildi"),
			views.Tf("The request for “%s” was cancelled; open links no longer work.",
				"“%s” için istek iptal edildi; açık bağlantılar artık çalışmıyor.", env.Document)
	case "expired":
		return views.T("Signature request expired", "İmza isteğinin süresi doldu"),
			views.Tf("The request for “%s” expired with %d/%d signed.", "“%s” için istek %d/%d imzayla süresi dolarak kapandı.", env.Document, signed, total)
	case "link_ended":
		deleted := lastEventNote(env, "link_ended") == "deleted"
		return views.T("Signature request closed: a signing link was ended", "İmza isteği kapandı: bir imza bağlantısı sonlandırıldı"),
			views.Each(func(l views.Lang) string {
				if deleted {
					return l.Sf("“%s”: the signing link for %s was deleted on the Shares screen, so they can no longer sign and the request was closed with %d/%d signed. The other links no longer work and the document is released. To ask again, start a new request.",
						"“%s”: %s için imza bağlantısı Paylaşımlar ekranında silindi; artık imzalayamayacağı için istek %d/%d imzayla kapatıldı. Diğer bağlantılar da artık çalışmıyor ve belge serbest bırakıldı. Yeniden istemek için yeni bir istek başlatın.",
						env.Document, who, signed, total)
				}
				return l.Sf("“%s”: the signing link for %s was revoked on the Shares screen, so they can no longer sign and the request was closed with %d/%d signed. The other links no longer work and the document is released. To ask again, start a new request.",
					"“%s”: %s için imza bağlantısı Paylaşımlar ekranında iptal edildi; artık imzalayamayacağı için istek %d/%d imzayla kapatıldı. Diğer bağlantılar da artık çalışmıyor ve belge serbest bırakıldı. Yeniden istemek için yeni bir istek başlatın.",
					env.Document, who, signed, total)
			})
	}
	return views.Plain(kind), views.Plain(env.Document)
}
