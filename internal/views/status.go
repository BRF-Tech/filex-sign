package views

import (
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
)

// StatusWords is the status label in both languages.
func StatusWords(s envelope.Status) wire.Text {
	switch s {
	case envelope.StatusSent:
		return T("Sent", "Gönderildi")
	case envelope.StatusInProgress:
		return T("In progress", "Sürüyor")
	case envelope.StatusCompleted:
		return T("Completed", "Tamamlandı")
	case envelope.StatusCancelled:
		return T("Cancelled", "İptal edildi")
	case envelope.StatusExpired:
		return T("Expired", "Süresi doldu")
	case envelope.StatusDeclined:
		return Tc("state of a request", "Refused", "Reddedildi")
	}
	return Plain(string(s))
}

// ActWords is one participant's progress, in the words of what they were
// asked to DO. ⚠⚠ A person who is asked for no signature does not "sign"
// (the owner, 2026-09-23): the record has one status for "did their part",
// and the report has two words for it, because the two acts are not the
// same act and a report that calls a fill a signature is a report that
// lies about the document.
func ActWords(s envelope.SignerStatus, signs bool) wire.Text {
	if s == envelope.SignerSigned && !signs {
		// ⚠ "Filled it in", not "Filled in": the trail already heads the
		// values on the paper with "Filled in", and one English with two
		// Turkish is exactly what the catalogue cannot hold.
		return T("Filled it in", "Doldurdu")
	}
	return SignerWords(s)
}

// SignerWords is one signer's progress in both languages — the status panel
// and the audit trail say it with the same words.
func SignerWords(s envelope.SignerStatus) wire.Text {
	switch s {
	case envelope.SignerPending:
		return T("Waiting", "Bekliyor")
	case envelope.SignerNotified:
		return T("Invited", "Davet edildi")
	case envelope.SignerViewed:
		return T("Opened it", "Açtı")
	case envelope.SignerSigned:
		return T("Signed", "İmzaladı")
	case envelope.SignerDeclined:
		return T("Refused", "Reddetti")
	case envelope.SignerVoid:
		return T("Closed", "Kapandı")
	}
	return Plain(string(s))
}

// StatusInput is everything the details panel needs beside the envelope.
type StatusInput struct {
	Doc              Doc
	Env              *envelope.Envelope
	SignaturesInFile int
	ShowLinkFor      string
	Expired          bool // the links have run out but nobody closed it yet
	RemindDue        bool
	Toast            wire.Text
	CAName           string
	CAFP             string
}

// Status draws the details-panel section.
func Status(l Lang, in StatusInput) *wire.Surface {
	env := in.Env
	s := &wire.Surface{Title: T("Signatures", "İmzalar"), State: map[string]any{"view": "status"}, Toast: in.Toast}
	if env == nil {
		s.Nodes = []wire.Node{muted(T("No signature request for this document.", "Bu belge için imza isteği yok."))}
		if in.SignaturesInFile > 0 {
			s.Nodes = append(s.Nodes, text(Tf("The file carries %d signature(s).", "Dosyada %d imza var.", in.SignaturesInFile)),
				muted(T("Right-click it → “Verify” to see who signed it and what each signature covers.",
					"Kimin imzaladığını ve her imzanın neyi kapsadığını görmek için sağ tıklayın → “Doğrula”.")))
		}
		s.Nodes = append(s.Nodes, muted(T("Use “Sign…” to sign it yourself or “Request signatures…” to invite others.",
			"Kendiniz imzalamak için “İmzala…”, başkalarını davet etmek için “İmza iste…” kullanın.")))
		return s
	}
	signed, total := env.Progress()
	s.Nodes = []wire.Node{
		heading(Each(func(l Lang) string {
			return l.Sf("%s · %d/%d done", "%s · %d/%d tamamladı", In(StatusWords(env.Status), l), signed, total)
		})),
		muted(Tf("Requested by %s on %s", "%s tarafından %s tarihinde istendi",
			env.Requester.Identity(), Day(env.CreatedAt))),
	}
	if env.Options.Message != "" {
		s.Nodes = append(s.Nodes, muted(Plain("“"+env.Options.Message+"”")))
	}
	var facts []wire.Text
	if env.Sequential() {
		facts = append(facts, T("signed one at a time", "birer birer imzalanıyor"))
	}
	if env.Options.Deadline != "" {
		facts = append(facts, Tf("due %s", "son tarih %s", Day(env.Options.Deadline)))
	}
	if env.Locked {
		facts = append(facts, Tf("the file is frozen until %s", "dosya %s tarihine kadar dondurulmuş", Day(env.LockUntil)))
	}
	facts = append(facts, OutputWords(env.Options.Output.Normalized()))
	if env.Options.Sends() {
		facts = append(facts, T("the signed copy is mailed to the signers as a link", "imzalı kopya imzacılara bağlantı olarak postalanır"))
	}
	s.Nodes = append(s.Nodes, muted(joinWords(facts)))

	var rows []row
	for _, sg := range env.Signers {
		signs := env.Signs(sg.ID)
		cells := map[string]wire.Text{"who": Plain(sg.Person.Display()), "state": ActWords(sg.Status, signs), "when": Plain("—")}
		switch {
		case sg.SignedAt != "":
			cells["when"] = DayText(sg.SignedAt)
		case sg.DeclinedAt != "":
			cells["when"] = DayText(sg.DeclinedAt)
		case sg.ViewedAt != "":
			cells["when"] = Tf("opened %s", "%s açtı", Day(sg.ViewedAt))
		case sg.PageExpires != "":
			cells["when"] = Tf("until %s", "%s tarihine kadar", Day(sg.PageExpires))
		}
		switch {
		case sg.Internal() && sg.Status != envelope.SignerSigned:
			state := ActWords(sg.Status, signs)
			cells["state"] = Each(func(l Lang) string { return l.Sf("%s (in filex)", "%s (filex içinde)", In(state, l)) })
		case sg.HandOver() && sg.Status != envelope.SignerSigned:
			state := ActWords(sg.Status, signs)
			cells["state"] = Each(func(l Lang) string {
				return l.Sf("%s (you hand the link over)", "%s (bağlantıyı siz veriyorsunuz)", In(state, l))
			})
		}
		var acts []rowAction
		if !env.Status.Closed() && !sg.Done() {
			if sg.Person.HasEmail() || sg.Internal() {
				acts = append(acts, rowAction{ID: "remind", Label: T("Remind", "Hatırlat")})
			}
			if sg.PageToken != "" {
				acts = append(acts, rowAction{ID: "link", Label: T("Show link", "Bağlantıyı göster")})
			}
		}
		rows = append(rows, row{ID: sg.ID, Cells: cells, Actions: acts})
	}
	s.Nodes = append(s.Nodes, list([]column{
		{Key: "who", Label: T("Identity", "Kimlik")},
		{Key: "state", Label: T("State", "Durum")},
		{Key: "when", Label: T("When", "Ne zaman")},
	}, rows, T("No signers", "İmzacı yok")))

	if in.RemindDue && !env.Status.Closed() {
		s.Nodes = append(s.Nodes, info(Tf("Nobody has moved for %d days. “Remind” sends the invitation again.",
			"%d gündür kimse kıpırdamadı. “Hatırlat” daveti yeniden gönderir.", env.Options.RemindEveryDays)))
	}
	if in.ShowLinkFor != "" {
		if sg := env.Signer(in.ShowLinkFor); sg != nil && sg.PageURL != "" {
			s.Nodes = append(s.Nodes, form([]wire.Field{
				withHelp(labelled("link", "string", l.Sf("Link for %s", "%s için bağlantı", sg.Person.Identity())), l,
					"Copy it and send it yourself. The PIN, if one was set, was shown when the request was sent (the job message and the bell) — it is never in a mail.",
					"Kopyalayıp kendiniz iletin. PIN koyduysanız, istek gönderilirken gösterildi (iş mesajı ve bildirim) — e-postada asla olmaz."),
			}, map[string]any{"link": sg.PageURL}))
		}
	}
	if sg := declinedSigner(env); sg != nil {
		reason := sg.DeclineReason
		if reason == "" {
			reason = "—"
		}
		s.Nodes = append(s.Nodes, danger(Tf("%s refused: %s", "%s reddetti: %s", sg.Person.Identity(), reason)))
	}
	if e := endedLinkEvent(env); e != nil {
		// The status alone says "Cancelled", and the requester did not
		// cancel anything: this line is WHY.
		who := e.Signer
		if sg := env.Signer(e.Signer); sg != nil {
			who = sg.Person.Identity()
		}
		if e.Note == "deleted" {
			s.Nodes = append(s.Nodes, danger(Tf("The signing link for %s was deleted on the Shares screen on %s, so the request was closed. To ask again, start a new request.",
				"%s için imza bağlantısı %s tarihinde Paylaşımlar ekranında silindi; bu yüzden istek kapatıldı. Yeniden istemek için yeni bir istek başlatın.", who, Day(e.At))))
		} else {
			s.Nodes = append(s.Nodes, danger(Tf("The signing link for %s was revoked on the Shares screen on %s, so the request was closed. To ask again, start a new request.",
				"%s için imza bağlantısı %s tarihinde Paylaşımlar ekranında iptal edildi; bu yüzden istek kapatıldı. Yeniden istemek için yeni bir istek başlatın.", who, Day(e.At))))
		}
	}
	if env.DeliveryURL != "" {
		s.Nodes = append(s.Nodes, form([]wire.Field{
			withHelp(strField(l, "delivery", "Link the signers were sent", "İmzacılara gönderilen bağlantı"), l,
				"The signed document as a filex share. Anybody without an address got nothing — pass this on to them.",
				"İmzalı belge, filex paylaşımı olarak. Adresi olmayanlara gönderilmedi — onlara bunu siz iletin."),
		}, map[string]any{"delivery": env.DeliveryURL}))
	}
	if in.SignaturesInFile > 0 {
		s.Nodes = append(s.Nodes, muted(Tf("The file carries %d signature(s). Right-click it → “Verify” for the full report.",
			"Dosyada %d imza var. Tam rapor için sağ tıklayın → “Doğrula”.", in.SignaturesInFile)))
	}
	if in.CAFP != "" {
		s.Nodes = append(s.Nodes, muted(Tf("Signing authority: %s · SHA-256 %s", "İmza makamı: %s · SHA-256 %s", in.CAName, in.CAFP)))
	}
	if env.Status == envelope.StatusCompleted && env.Options.Audit && env.Options.Output.Normalized().Mode != envelope.OutputSibling {
		// The signed file went in as a new version, and one job commits one
		// kind of output, so the trail is a click rather than a surprise.
		s.Nodes = append(s.Nodes, muted(T("An audit trail — who was asked, what each of them did, when, and with which certificate — can be written beside the document.",
			"Denetim izi — kimden istendi, her biri ne yaptı, ne zaman, hangi sertifikayla — belgenin yanına yazılabilir.")))
		s.Actions = append(s.Actions, button("audit", T("Save the audit trail", "Denetim izini kaydet")))
	}
	if !env.Status.Closed() {
		if in.Expired {
			s.Nodes = append(s.Nodes, danger(T("Every link has expired. Close the request to release the file.",
				"Bütün bağlantıların süresi doldu. Dosyayı serbest bırakmak için isteği kapatın.")))
			s.Actions = append(s.Actions, primary("expire", T("Close the expired request", "Süresi dolan isteği kapat")))
		}
		s.Actions = append(s.Actions, dangerButton("cancel", T("Cancel request", "İsteği iptal et")))
	}
	return s
}

// endedLinkEvent is the event that closed the request because a person
// ended a signing link on the Shares screen, or nil.
func endedLinkEvent(env *envelope.Envelope) *envelope.Event {
	for i := len(env.Events) - 1; i >= 0; i-- {
		if env.Events[i].Type == envelope.EvLinkEnded {
			return &env.Events[i]
		}
	}
	return nil
}

func declinedSigner(env *envelope.Envelope) *envelope.Signer {
	for i := range env.Signers {
		if env.Signers[i].Status == envelope.SignerDeclined {
			return &env.Signers[i]
		}
	}
	return nil
}
