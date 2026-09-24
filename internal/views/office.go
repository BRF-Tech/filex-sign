package views

import (
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// ── Documents that are not PDFs yet ────────────────────────────────────
//
// Nobody can place a box on a page that has not been rendered, so an
// office document gets one question and one button: convert it. Once the
// PDF is there the ordinary flow works, boxes and all.

// SignOffice is the "Sign…" screen for a document that is not a PDF.
func SignOffice(l Lang, doc Doc, engine bool) *wire.Surface {
	s := &wire.Surface{Title: Tf("Sign %s", "%s — imzala", doc.Name), Size: "md",
		State: map[string]any{"view": "sign-self", "office": true}}
	if !engine {
		s.Nodes = []wire.Node{danger(Tf("“%s” is not a PDF, and this filex has no LibreOffice to convert it. Convert it to PDF yourself and sign that.",
			"“%s” bir PDF değil ve bu filex kurulumunda dönüştürecek LibreOffice yok. Kendiniz PDF'e çevirip onu imzalayın.", doc.Name))}
		return s
	}
	s.Nodes = []wire.Node{
		heading(T("This has to be a PDF first", "Önce PDF olması gerekiyor")),
		text(Tf("“%s” is an office document. Its pages are not drawn yet, so no box can be placed on them. filex converts it to PDF beside the original, which is never changed — then open the PDF and sign it the ordinary way.",
			"“%s” bir ofis belgesi. Sayfaları henüz çizilmediği için üzerine kutu konulamaz. filex onu aslının yanına PDF olarak çevirir, aslına dokunmaz — sonra PDF'i açıp her zamanki gibi imzalarsınız.", doc.Name)),
	}
	s.Actions = []wire.SurfaceAction{primary("convert", T("Convert to PDF", "PDF'e dönüştür"))}
	return s
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
	title, view := Tf("Sign %s", "%s — imzala", doc.Name), "sign-self"
	why := Tf("“%s” is on a storage that takes no changes, so the signed copy could not be saved beside it. Copy it to a folder you can write to and sign it there.",
		"“%s” değişiklik kabul etmeyen bir depoda; imzalı kopya yanına kaydedilemez. Yazabildiğiniz bir klasöre kopyalayıp orada imzalayın.", doc.Name)
	if request {
		title, view = Tf("Request signatures — %s", "İmza iste — %s", doc.Name), "request"
		why = Tf("“%s” is on a storage that takes no changes, so the signed document could never be saved when the last signer answers. Nothing was sent. Copy it to a folder you can write to and ask for signatures there.",
			"“%s” değişiklik kabul etmeyen bir depoda; son imzacı cevap verdiğinde imzalı belge kaydedilemezdi. Hiçbir şey gönderilmedi. Yazabildiğiniz bir klasöre kopyalayıp imzayı orada isteyin.", doc.Name)
	}
	return &wire.Surface{Title: title, Size: "md", State: map[string]any{"view": view, "read_only": true},
		Nodes: []wire.Node{danger(why)}}
}

// RequestOffice is the "Request signatures…" screen for the same case.
func RequestOffice(l Lang, doc Doc, engine bool) *wire.Surface {
	s := &wire.Surface{Title: Tf("Request signatures — %s", "İmza iste — %s", doc.Name), Size: "md",
		State: map[string]any{"view": "request", "office": true}}
	if !engine {
		s.Nodes = []wire.Node{danger(Tf("“%s” is not a PDF, and this filex has no LibreOffice to convert it. Convert it to PDF yourself and ask for signatures on the PDF.",
			"“%s” bir PDF değil ve bu filex kurulumunda dönüştürecek LibreOffice yok. Kendiniz PDF'e çevirip imzayı PDF üzerinde isteyin.", doc.Name))}
		return s
	}
	s.Nodes = []wire.Node{
		heading(T("This has to be a PDF first", "Önce PDF olması gerekiyor")),
		text(Tf("“%s” has to be a PDF before signature boxes can be placed on it. filex can convert it now, beside the original. Then open the PDF and choose “Request signatures…” again.",
			"“%s” üzerine imza kutusu koyabilmek için önce PDF olmalı. filex şimdi, aslının yanına dönüştürebilir. Sonra PDF'i açıp yeniden “İmza iste…” deyin.", doc.Name)),
	}
	s.Actions = []wire.SurfaceAction{primary("convert", T("Convert to PDF", "PDF'e dönüştür"))}
	return s
}
