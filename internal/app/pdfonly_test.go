package app

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	filexsign "github.com/brf-tech/filex-sign"
	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/testpdf"
	"github.com/brf-tech/filex-sign/internal/views"
)

// ⚠⚠ The owner, 2026-10-02: "İmzalama uygulamasında lütfen docx vb
// şeylerde önce pdf'e çevirmeden imzala işlemini getirme ekrana zaten.
// Dependent ise oradan ayıralım" - this app signs PDFs, and nothing else
// reaches it. An office document is turned into a PDF by the Convert app
// (or an office program) first; this app runs no engine and depends on no
// other app. Until then a DOCX was offered "Sign…" and "Request
// signatures…" wherever LibreOffice was installed (applies.engine_ext), and
// both screens offered "Convert to PDF", which ran LibreOffice from inside
// this app (engines:libreoffice, a hidden `convert` action).

// officeNames are the documents the old manifest offered "Sign…" on while
// LibreOffice was there - every one of them must now be offered nothing.
var officeNames = []string{
	"teklif.docx", "teklif.doc", "teklif.dotx", "metin.odt", "metin.ott", "metin.fodt", "metin.rtf",
	"tablo.xlsx", "tablo.xls", "tablo.ods", "sunum.pptx", "sunum.ppt", "sunum.odp", "cizim.odg", "notlar.txt",
}

// everyEngine is a server that has every engine filex knows: the case in
// which the old manifest offered the most.
func everyEngine() map[string]bool {
	out := map[string]bool{}
	for name := range plugintest.KnownEngines {
		out[name] = true
	}
	return out
}

// offeredOn is filex's rule for whether a file meets an `applies`
// (wasmplugin effectiveActions + wire.Applies.Matches, no administrator
// override): the manifest's extensions, plus each engine's while that
// engine is present; no extension and no MIME type at all means any file.
func offeredOn(a wire.Applies, name string, engines map[string]bool) bool {
	if a.Kind != "" && a.Kind != "file" {
		return false
	}
	exts := append([]string(nil), a.Ext...)
	for engine, more := range a.EngineExt {
		if engines[engine] {
			exts = append(exts, more...)
		}
	}
	if len(exts) == 0 && len(a.Mime) == 0 {
		return true
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	for _, e := range exts {
		if strings.TrimPrefix(strings.ToLower(e), ".") == ext {
			return true
		}
	}
	return false
}

// ⭐ Not one of the app's actions - the menu rows and the hidden ones a
// screen queues - and not its details-panel section applies to an office
// document, whatever engines the server has. On a PDF the rows are there.
func TestPDFOnly_NothingIsOfferedOnAnOfficeDocument(t *testing.T) {
	m := filexsign.Manifest()
	engines := everyEngine()
	for _, name := range officeNames {
		for _, act := range m.Actions {
			if offeredOn(act.Applies, name, engines) {
				t.Errorf("action %q applies to %s (applies %+v): an office document is converted with the Convert app first, never here",
					act.ID, name, act.Applies)
			}
		}
		for _, v := range m.Views {
			a := v.Applies
			if a.Kind == "" && len(a.Ext) == 0 && len(a.Mime) == 0 && len(a.EngineExt) == 0 {
				continue // a page or home screen, opened by an action, not by a file
			}
			if offeredOn(a, name, engines) {
				t.Errorf("view %q is shown on %s (applies %+v)", v.ID, name, a)
			}
		}
	}
	for _, id := range []string{ActionSign, ActionRequest, ActionVerify, ActionApply} {
		found := false
		for _, act := range m.Actions {
			if act.ID == id {
				found = true
				if !offeredOn(act.Applies, "sözleşme.PDF", nil) {
					t.Errorf("action %q must still apply to a PDF: %+v", id, act.Applies)
				}
			}
		}
		if !found {
			t.Errorf("action %q is missing from the manifest", id)
		}
	}
}

// The app runs no engine: no `engines:` permission (so the install wizard
// does not ask an administrator for LibreOffice), no reason for one, no
// engine-gated extensions, and no `convert` action.
func TestPDFOnly_ManifestAsksForNoEngine(t *testing.T) {
	m := filexsign.Manifest()
	for _, p := range m.Permissions {
		if strings.HasPrefix(p, "engines:") {
			t.Errorf("the manifest asks for %q: signing depends on no engine", p)
		}
	}
	for p := range m.PermissionReasons {
		if strings.HasPrefix(p, "engines:") {
			t.Errorf("the manifest explains %q, which it must not ask for", p)
		}
	}
	for _, act := range m.Actions {
		if len(act.Applies.EngineExt) > 0 {
			t.Errorf("action %q still has engine-gated extensions: %+v", act.ID, act.Applies.EngineExt)
		}
		if act.ID == "convert" {
			t.Error("the hidden `convert` action is still declared: converting is the Convert app's work")
		}
	}
	for _, v := range m.Views {
		if len(v.Applies.EngineExt) > 0 {
			t.Errorf("view %q still has engine-gated extensions: %+v", v.ID, v.Applies.EngineExt)
		}
	}
}

// saysConvertFirst checks the words a person gets about a document that is
// not a PDF: what it is, where it is converted, in both languages, and no
// promise that this app converts it.
func saysConvertFirst(t *testing.T, what, en, tr string) {
	t.Helper()
	for _, want := range []string{"is not a PDF", "Convert app"} {
		if !strings.Contains(en, want) {
			t.Errorf("%s: the English does not say %q: %q", what, want, en)
		}
	}
	for _, want := range []string{"PDF değil", "Dönüştür uygulamasıyla"} {
		if !strings.Contains(tr, want) {
			t.Errorf("%s: the Turkish does not say %q: %q", what, want, tr)
		}
	}
	for _, gone := range []string{"LibreOffice", "Convert to PDF", "PDF'e dönüştür"} {
		if strings.Contains(en, gone) || strings.Contains(tr, gone) {
			t.Errorf("%s: still mentions %q: %q / %q", what, gone, en, tr)
		}
	}
}

// ⭐ The screens, reached on a DOCX some way other than the menu (a
// bookmarked address, a screen left open across the upgrade): one message,
// no button - even on a server that has LibreOffice. A click on the old
// "Convert to PDF" button, posted by a screen drawn before the upgrade,
// queues nothing.
func TestPDFOnly_ScreensOnAnOfficeDocumentSayConvertFirst(t *testing.T) {
	for _, flow := range []struct {
		view string
		fn   func(a *App) func(*wire.ViewEventInput) (*wire.Surface, error)
	}{
		{ViewSignSelf, func(a *App) func(*wire.ViewEventInput) (*wire.Surface, error) { return a.viewSignSelf }},
		{ViewRequest, func(a *App) func(*wire.ViewEventInput) (*wire.Surface, error) { return a.viewRequest }},
	} {
		t.Run(flow.view, func(t *testing.T) {
			a, f := newApp(t)
			f.Inputs["in:0"] = []byte("PK\x03\x04 an office document")
			open := func(event, actionID string, state map[string]any) *wire.ViewEventInput {
				in := viewInput(flow.view, event, actionID, state, nil)
				in.Context.Inputs[0].Name = "teklif.docx"
				in.Context.Inputs[0].Path = "docs://sozlesmeler/teklif.docx"
				in.Context.Engines = everyEngine()
				return in
			}
			s, err := flow.fn(a)(open("open", "", nil))
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Actions) != 0 {
				t.Errorf("a document that is not a PDF offers no button here: %+v", s.Actions)
			}
			words := surfaceWords(s)
			saysConvertFirst(t, flow.view, words, words)

			// The old screen's primary button, as filex posted it.
			for _, event := range []string{"submit", "action"} {
				next, err := flow.fn(a)(open(event, "convert", map[string]any{"view": flow.view, "office": true}))
				if err != nil {
					t.Fatal(err)
				}
				if next.Job != nil || next.Open != nil {
					t.Fatalf("a %s/convert from an old screen must queue nothing: job %+v, open %+v", event, next.Job, next.Open)
				}
				if len(next.Actions) != 0 || !strings.Contains(surfaceWords(next), "Convert app") {
					t.Errorf("a %s/convert from an old screen lands on the same message: %+v", event, next.Actions)
				}
			}
			if len(f.Outputs) != 0 || len(f.Locks) != 0 || len(f.State) != 0 {
				t.Errorf("nothing may be written: %d outputs, %d locks, %d state rows", len(f.Outputs), len(f.Locks), len(f.State))
			}
		})
	}
}

// ⭐ A job that reaches the app on an office document anyway (a direct run,
// or one queued by an old screen and still waiting across the upgrade) is
// refused in words, writes nothing, freezes nothing, tells nobody.
func TestPDFOnly_JobsRefuseAnOfficeDocumentInWords(t *testing.T) {
	docx := []wire.FileRef{{Ref: "in:0", Name: "teklif.docx", Path: "docs://sozlesmeler/teklif.docx", Size: 1000}}
	for _, c := range []struct {
		name string
		run  func(a *App) (*wire.ActionRunOutput, error)
	}{
		{"sign", func(a *App) (*wire.ActionRunOutput, error) {
			return a.actionSign(&wire.ActionRunInput{ActionID: ActionSign, Locale: "tr", Actor: burak, Inputs: docx})
		}},
		{"sign op=convert, queued by a screen before 0.1.1", func(a *App) (*wire.ActionRunOutput, error) {
			return a.actionSign(&wire.ActionRunInput{ActionID: ActionSign, Locale: "tr", Actor: burak, Inputs: docx,
				Params: map[string]any{"op": "convert"}})
		}},
		{"request", func(a *App) (*wire.ActionRunOutput, error) {
			return a.actionRequest(&wire.ActionRunInput{ActionID: ActionRequest, Locale: "tr", Actor: burak, Inputs: docx,
				Params: map[string]any{"signers": []map[string]any{{"email": "ali@ornek.com"}}}})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, f := newApp(t)
			f.Inputs["in:0"] = []byte("PK\x03\x04 an office document")
			f.Register("in:0", "docs://sozlesmeler/teklif.docx", "teklif.docx")
			out, err := c.run(a)
			if err != nil {
				t.Fatal(err)
			}
			if out.OK || len(out.Outputs) != 0 {
				t.Fatalf("an office document cannot be signed here: %+v", out)
			}
			saysConvertFirst(t, c.name, out.Message["en"], out.Message["tr"])
			if !strings.Contains(out.Message["en"], "teklif.docx") {
				t.Errorf("the answer does not name the document: %q", out.Message["en"])
			}
			if len(f.Outputs) != 0 || len(f.Locks) != 0 || len(f.State) != 0 || len(f.Shares) != 0 || len(f.Mails) != 0 || len(f.Notices) != 0 {
				t.Errorf("nothing may be written or sent: %d outputs, %d locks, %d state rows, %d shares, %d mails, %d notices",
					len(f.Outputs), len(f.Locks), len(f.State), len(f.Shares), len(f.Mails), len(f.Notices))
			}
		})
	}
}

// Guard (the same before and after the change; nothing here is new
// behaviour): what an older version left on an office document is kept.
// 0.1.0 signed an office document by converting it, wrote the signed PDF
// beside it, and put the `signed` badge and the `sealed` record on the
// DOCX itself. No released version could open a REQUEST on one (the job
// refused it since the first office support), so this is the only state an
// office document can carry. It is never deleted: the person still finds
// the document under "Signed", and Verify on the signed PDF still finds the
// record that sealed it.
func TestPDFOnly_WhatAnOlderVersionLeftOnAnOfficeDocumentIsKept(t *testing.T) {
	a, f := newApp(t)
	signed := testpdf.Build(testpdf.Options{Pages: []testpdf.Page{testpdf.Letter()}})
	f.Inputs["in:0"] = signed
	f.Register("in:0", "docs://sozlesmeler/teklif-signed.pdf", "teklif-signed.pdf")
	f.Inputs["in:1"] = []byte("PK\x03\x04 an office document")
	f.Register("in:1", "docs://sozlesmeler/teklif.docx", "teklif.docx")
	sum := sha256.Sum256(signed)
	sealed := hex.EncodeToString(sum[:])
	if err := f.StateSet("in:1", envelope.SignedKey, envelope.AddSigner("", burak.ID)); err != nil {
		t.Fatal(err)
	}
	if err := f.StateSet("in:1", envelope.SealedKey, sealed+" teklif-signed.pdf"); err != nil {
		t.Fatal(err)
	}

	home := homeAs(t, a, burak, "open", "", map[string]any{"section": views.SectionSigned})
	if _, listed := rowsOf(home)["docs://sozlesmeler/teklif.docx"]; !listed {
		t.Errorf("the document an older version signed is no longer under Signed: %v", rowsOf(home))
	}
	got := a.sentHash(sealed)
	if got == nil || got.Document != "teklif.docx" || got.Output != "teklif-signed.pdf" || !got.Matches {
		t.Errorf("Verify on the signed PDF no longer finds the record that sealed it: %+v", got)
	}
	if v, ok, _ := f.StateGet("in:1", envelope.SignedKey); !ok || !envelope.HasSigner(v, burak.ID) {
		t.Error("the badge on the office document was touched")
	}
}
