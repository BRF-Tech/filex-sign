package views

import (
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
)

// ── A second request on the same document ───────────────────────────────
//
// A document carries ONE signature request at a time: one record, one
// `pending` mark, one freeze, one set of boxes in the file. A second
// request is legitimate once the first has ENDED (completed, cancelled,
// expired, refused) — it starts the next round on the same file. While the
// first is OPEN it is not: two rounds would each sign a version of the file
// the other one keeps changing.
//
// ⚠⚠ The refusal used to be the host's alone. `request` is offered only
// where `pending` is not set, and filex re-checks that when the job is
// queued — so somebody who reached the wizard anyway (a menu drawn before
// the first request went out, a bookmarked address) walked all eight steps
// and was told, at Send, "Invalid data": the host's 422, whose English
// sentence the client does not show. Measured on a live instance,
// 2026-09-21. The app knows why and can say it in the person's language, so
// it says it FIRST, on the wizard's opening screen, and again at Send for a
// request somebody else opened in the meantime.

// ActionOpenStatus is AlreadyRequested's one button: it takes the person to
// the document with its Signatures panel open (Surface.Open → the `status`
// view), where the open request can be followed or cancelled.
const ActionOpenStatus = "open_status"

// AlreadyRequested is the wizard's answer on a document whose request is
// still open: who asked whom, how far it got, and the one way forward.
func AlreadyRequested(l Lang, doc Doc, env *envelope.Envelope) *wire.Surface {
	signed, total := env.Progress()
	var names []string
	for _, sg := range env.Signers {
		names = append(names, sg.Person.Display())
	}
	who := strings.Join(names, ", ")
	s := &wire.Surface{
		Title: Tf("Request signatures — %s", "İmza iste — %s", doc.Name),
		Size:  "lg",
		State: map[string]any{"view": "request", "refused": "open_request"},
		Nodes: []wire.Node{
			heading(T("This document already has a signature request", "Bu belgenin zaten bir imza isteği var")),
			// ⚠ Explicit argument indexes: Turkish puts the day before the
			// signers, so the two sentences take the same arguments in a
			// different order.
			text(Tf("%[1]s asked %[2]s to sign it on %[3]s, and it is still open: %[4]d of %[5]d signed.",
				"%[1]s, %[3]s tarihinde %[2]s kişisinden imza istedi ve istek hâlâ açık: %[4]d/%[5]d imzalandı.",
				env.Requester.Identity(), who, Day(env.CreatedAt), signed, total)),
			text(T("A document carries one signature request at a time, so a second one cannot start while this one is open. Follow it — or cancel it — in the document's Signatures panel; once it has ended you can ask again here.",
				"Bir belge aynı anda tek bir imza isteği taşır; bu yüzden bu istek açıkken ikincisi başlatılamaz. İsteği belgenin İmzalar panelinden izleyin ya da iptal edin; istek sona erince buradan yeniden isteyebilirsiniz.")),
		},
	}
	if doc.Path != "" {
		s.Actions = []wire.SurfaceAction{primary(ActionOpenStatus, T("Open its Signatures panel", "İmzalar panelini aç"))}
	}
	return s
}

// previousNote is what the first step says when the document's last
// request has ended: the new one takes its place in the Signatures panel,
// and anything of it that exists nowhere but in that record should be
// saved first.
func previousNote(prev *envelope.Envelope) []wire.Node {
	if prev == nil || !prev.Status.Closed() {
		return nil
	}
	signed, total := prev.Progress()
	day := prev.ClosedAt
	if day == "" {
		day = prev.UpdatedAt
	}
	out := []wire.Node{muted(Each(func(l Lang) string {
		return l.Sf("This document had a signature request before (%s, %s, %d/%d signed). Sending this one replaces that record in the Signatures panel; the signatures already in the file stay there, and Verify still reports them.",
			"Bu belgenin daha önce bir imza isteği vardı (%s, %s, %d/%d imzalandı). Bunu göndermek, İmzalar panelindeki o kaydın yerini alır; dosyadaki imzalar yerinde kalır ve Doğrula onları raporlamayı sürdürür.",
			In(StatusWords(prev.Status), l), Day(day), signed, total)
	}))}
	// The one thing the record holds that no file does: a trail that was
	// asked for but could not ride along with a new-version output (the
	// status panel's "Save the audit trail" writes it from the record).
	if prev.Status == envelope.StatusCompleted && prev.Options.Audit && prev.Options.Output.Normalized().Mode != envelope.OutputSibling {
		out = append(out, info(T("Its audit trail was never written as a file. If you need it, save it from the Signatures panel before you send this request.",
			"Onun denetim izi hiçbir zaman dosya olarak yazılmadı. Gerekiyorsa bu isteği göndermeden önce İmzalar panelinden kaydedin.")))
	}
	return out
}
