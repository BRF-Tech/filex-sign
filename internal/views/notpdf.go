package views

import (
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Documents a flow cannot start on ───────────────────────────────────
//
// A document that is not a PDF, and a document on a storage that takes no
// writes. Both are said at the FIRST screen, in words that say what to do
// instead, and neither screen has a button: there is nothing here to press.

// NotPDF is the "Sign…" (request false) or "Request signatures…" (request
// true) screen for a document that is not a PDF.
//
// ⚠⚠ The owner, 2026-10-02: this app signs PDFs and converts nothing.
// Turning an office document into a PDF is the Convert app's work, so the
// screen says to convert it there (or in an office program) and to come
// back with the PDF. It used to offer "Convert to PDF" and run LibreOffice
// itself, which made signing depend on a converter.
func NotPDF(l Lang, doc Doc, request bool) *wire.Surface {
	title, view, why := Tf("Sign %s", "%s - imzala", doc.Name), "sign-self", NotPDFWords(doc.Name)
	if request {
		title, view, why = Tf("Request signatures - %s", "İmza iste - %s", doc.Name), "request", NotPDFRequestWords(doc.Name)
	}
	return &wire.Surface{Title: title, Size: "md", State: map[string]any{"view": view, "not_pdf": true},
		Nodes: []wire.Node{heading(T("This has to be a PDF first", "Önce PDF olması gerekiyor")), text(why)}}
}

// NotPDFWords is what signing a document that is not a PDF is answered
// with, on the screen and from a job.
func NotPDFWords(name string) wire.Text {
	return Tf("“%s” is not a PDF, and e-Signature signs PDFs only. Convert it to PDF first, with the Convert app or your office program, then sign the PDF.",
		"“%s” bir PDF değil ve e-İmza yalnız PDF imzalar. Önce Dönüştür uygulamasıyla ya da ofis programınızla PDF'e çevirin, sonra PDF'i imzalayın.", name)
}

// NotPDFRequestWords is the same for asking others to sign.
func NotPDFRequestWords(name string) wire.Text {
	return Tf("“%s” is not a PDF, and signature boxes are placed on the pages of a PDF. Convert it to PDF first, with the Convert app or your office program, then open the PDF and choose “Request signatures…”.",
		"“%s” bir PDF değil; imza kutuları bir PDF'in sayfalarına yerleştirilir. Önce Dönüştür uygulamasıyla ya da ofis programınızla PDF'e çevirin, sonra PDF'i açıp “İmza iste…” deyin.", name)
}

// ReadOnlyDoc is the screen for a document whose storage takes no writes,
// for the flows that end in one: signing it yourself (a signed copy beside
// it) and asking others to sign (a new version when the last one signs).
//
// ⚠⚠ Said at the FIRST screen, before anything is frozen or anybody is
// notified (2026-09-21, a tester): a request on a read-only storage used to
// be accepted and "sent" — the file frozen, the signer notified — and the
// signer's answer then failed with 409 read_only, for ever. The request
// itself writes nothing, so the host had nothing to refuse; only this app
// knows that the flow it starts ends in a write.
func ReadOnlyDoc(l Lang, doc Doc, request bool) *wire.Surface {
	title, view := Tf("Sign %s", "%s - imzala", doc.Name), "sign-self"
	why := Tf("“%s” is on a storage that takes no changes, so the signed copy could not be saved beside it. Copy it to a folder you can write to and sign it there.",
		"“%s” değişiklik kabul etmeyen bir depoda; imzalı kopya yanına kaydedilemez. Yazabildiğiniz bir klasöre kopyalayıp orada imzalayın.", doc.Name)
	if request {
		title, view = Tf("Request signatures - %s", "İmza iste - %s", doc.Name), "request"
		why = Tf("“%s” is on a storage that takes no changes, so the signed document could never be saved when the last signer answers. Nothing was sent. Copy it to a folder you can write to and ask for signatures there.",
			"“%s” değişiklik kabul etmeyen bir depoda; son imzacı cevap verdiğinde imzalı belge kaydedilemezdi. Hiçbir şey gönderilmedi. Yazabildiğiniz bir klasöre kopyalayıp imzayı orada isteyin.", doc.Name)
	}
	return &wire.Surface{Title: title, Size: "md", State: map[string]any{"view": view, "read_only": true},
		Nodes: []wire.Node{danger(why)}}
}
