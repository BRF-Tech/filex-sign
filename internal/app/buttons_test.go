package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/testpdf"
	"github.com/brf-tech/filex-sign/internal/views"
)

// ⚠⚠ A footer button reaches the app the way filex's renderer sends it,
// and ONLY that way: `submit` when the button is the primary one,
// `action` otherwise, with the button's id in action_id
// (packages/core usePluginSurface.press — the same code draws the modal,
// the full page and the embedded explorer's popup).
//
// 2026-09-26, the maintainer: "Docx'e direk imza iste dediğimde pdf'e çevir
// butonu çıkıyor tıklayınca çalışmıyor." The "Convert to PDF" button is
// primary, so filex posted `submit/convert`; the app only listened for
// `action/convert` and answered with the same screen — nothing queued,
// nothing said. The old test drove it with `action`, an event the renderer
// never sends for that button, and stayed green. Every button test below
// asks the SCREEN how the button is pressed.

// pressedAs is the event filex posts for the button id on s.
func pressedAs(t *testing.T, s *wire.Surface, id string) string {
	t.Helper()
	for _, b := range s.Actions {
		if b.ID != id {
			continue
		}
		if b.Disabled {
			t.Fatalf("the %q button is disabled on this screen", id)
		}
		if b.Primary {
			return "submit"
		}
		return "action"
	}
	t.Fatalf("no %q button on this screen: %+v", id, s.Actions)
	return ""
}

// press answers the button id on s as filex would deliver the click.
func press(t *testing.T, s *wire.Surface, id string, in *wire.ViewEventInput, view func(*wire.ViewEventInput) (*wire.Surface, error)) *wire.Surface {
	t.Helper()
	in.Event = pressedAs(t, s, id)
	in.ActionID = id
	in.State = s.State
	next, err := view(in)
	if err != nil {
		t.Fatalf("pressing %q: %v", id, err)
	}
	return next
}

// dead reports whether a button's answer is the screen it was pressed on,
// unchanged: nothing queued, nowhere to go, nothing said.
func dead(before, after *wire.Surface) bool {
	if after == nil {
		return true
	}
	if after.Job != nil || after.Open != nil || after.Done {
		return false
	}
	return reflect.DeepEqual(before, after)
}

func officeIn(view, name string, engine bool) *wire.ViewEventInput {
	in := viewInput(view, "open", "", nil, nil)
	in.Context.Inputs[0].Name = name
	in.Context.Inputs[0].Path = "docs://sozlesmeler/" + name
	in.Context.Engines[officeEngine] = engine
	return in
}

// ⭐ The whole conversion, from the button filex draws to the PDF beside
// the original — for both flows that offer it.
func TestOffice_ConvertButton_AsFilexPressesIt(t *testing.T) {
	for _, flow := range []struct {
		name string
		view string
		fn   func(a *App) func(*wire.ViewEventInput) (*wire.Surface, error)
	}{
		{"request signatures", ViewRequest, func(a *App) func(*wire.ViewEventInput) (*wire.Surface, error) { return a.viewRequest }},
		{"sign yourself", ViewSignSelf, func(a *App) func(*wire.ViewEventInput) (*wire.Surface, error) { return a.viewSignSelf }},
	} {
		t.Run(flow.name, func(t *testing.T) {
			a, f := newApp(t)
			f.Engines[officeEngine] = true
			pdfBytes := testpdf.Build(testpdf.Options{Pages: []testpdf.Page{testpdf.Letter()}})
			var ran []pluginkit.EngineRequest
			f.EngineFn = func(req pluginkit.EngineRequest) (*pluginkit.EngineResult, error) {
				ran = append(ran, req)
				return &pluginkit.EngineResult{Outputs: []wire.OutputRef{f.AddArtefact("in.pdf", pdfBytes)}}, nil
			}
			view := flow.fn(a)
			s, err := view(officeIn(flow.view, "teklif.docx", true))
			if err != nil {
				t.Fatal(err)
			}
			next := press(t, s, "convert", officeIn(flow.view, "teklif.docx", true), view)
			if dead(s, next) {
				t.Fatal("“Convert to PDF” answered with the same screen: the button does nothing")
			}
			if next.Job == nil || next.Job.ActionID != ActionConvert {
				t.Fatalf("the button should queue the conversion: %+v", next.Job)
			}
			if o := next.Job.Output; o == nil || o.Mode != envelope.OutputSibling {
				t.Fatalf("the PDF goes beside the original: %+v", next.Job.Output)
			}

			job := jobFrom(t, next, burak, "teklif.docx")
			f.Inputs["in:0"] = []byte("PK\x03\x04 an office document")
			out, err := a.actionConvert(job)
			if err != nil {
				t.Fatal(err)
			}
			if !out.OK || len(out.Outputs) != 1 || out.Outputs[0].Name != "teklif.pdf" {
				t.Fatalf("the conversion should write teklif.pdf: %+v / %v", out.Outputs, out.Message)
			}
			if len(ran) != 1 || ran[0].Engine != officeEngine {
				t.Fatalf("LibreOffice should have run once: %+v", ran)
			}
			// The person is told, in their language, what came out and what
			// to do with it — and the ops tray showed progress on the way.
			if msg := out.Message["tr"]; !strings.Contains(msg, "teklif.pdf") {
				t.Errorf("the finished job does not name the PDF: %q", msg)
			}
			if len(f.Progressed) == 0 {
				t.Error("a conversion that reports no progress looks like a job that hangs")
			}
		})
	}
}

// With no LibreOffice there is nothing to press: the screen says why, in
// both languages, and a conversion that is queued anyway (the engine went
// away between the screen and the job) fails in words, never in silence.
func TestOffice_WithoutLibreOffice_NoDeadButton(t *testing.T) {
	for _, view := range []string{ViewRequest, ViewSignSelf} {
		t.Run(view, func(t *testing.T) {
			a, f := newApp(t)
			f.Engines[officeEngine] = false
			fn := a.viewRequest
			if view == ViewSignSelf {
				fn = a.viewSignSelf
			}
			s, err := fn(officeIn(view, "teklif.docx", false))
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Actions) != 0 {
				t.Errorf("with nothing to convert with, offer no button: %+v", s.Actions)
			}
			words := surfaceWords(s)
			for _, want := range []string{"LibreOffice", "no LibreOffice", "LibreOffice yok"} {
				if !strings.Contains(words, want) {
					t.Errorf("the screen does not say %q:\n%s", want, words)
				}
			}
		})
	}

	a, f := newApp(t)
	f.Engines[officeEngine] = false
	f.Inputs["in:0"] = []byte("PK\x03\x04 an office document")
	out, err := a.actionSign(&wire.ActionRunInput{ActionID: ActionSign, Locale: "tr", Actor: burak,
		Params: map[string]any{"op": "convert"},
		Inputs: []wire.FileRef{{Ref: "in:0", Name: "teklif.docx", Size: 1000}}})
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Fatal("a conversion without LibreOffice cannot succeed")
	}
	if !strings.Contains(out.Message["tr"], "LibreOffice yok") || !strings.Contains(out.Message["en"], "no LibreOffice") {
		t.Errorf("the failure should say LibreOffice is missing, in both languages: %+v", out.Message)
	}
}

// ⭐ No button on these screens is dead. Each one is pressed the way filex
// presses it, and each must queue a job, go somewhere, close, or at least
// change the screen.
func TestEveryFooterButton_DoesSomething(t *testing.T) {
	type screen struct {
		name string
		make func(t *testing.T) (*App, *wire.Surface, func() *wire.ViewEventInput, func(*App) func(*wire.ViewEventInput) (*wire.Surface, error))
	}
	screens := []screen{
		{"office document, request signatures", func(t *testing.T) (*App, *wire.Surface, func() *wire.ViewEventInput, func(*App) func(*wire.ViewEventInput) (*wire.Surface, error)) {
			a, f := newApp(t)
			f.Engines[officeEngine] = true
			mk := func() *wire.ViewEventInput { return officeIn(ViewRequest, "teklif.odt", true) }
			s, err := a.viewRequest(mk())
			if err != nil {
				t.Fatal(err)
			}
			return a, s, mk, func(a *App) func(*wire.ViewEventInput) (*wire.Surface, error) { return a.viewRequest }
		}},
		{"office document, sign yourself", func(t *testing.T) (*App, *wire.Surface, func() *wire.ViewEventInput, func(*App) func(*wire.ViewEventInput) (*wire.Surface, error)) {
			a, f := newApp(t)
			f.Engines[officeEngine] = true
			mk := func() *wire.ViewEventInput { return officeIn(ViewSignSelf, "teklif.xlsx", true) }
			s, err := a.viewSignSelf(mk())
			if err != nil {
				t.Fatal(err)
			}
			return a, s, mk, func(a *App) func(*wire.ViewEventInput) (*wire.Surface, error) { return a.viewSignSelf }
		}},
		{"a second request while the first is open", func(t *testing.T) (*App, *wire.Surface, func() *wire.ViewEventInput, func(*App) func(*wire.ViewEventInput) (*wire.Surface, error)) {
			a, _ := newApp(t)
			sendRequest(t, a)
			mk := func() *wire.ViewEventInput { return requestIn("open", nil, nil, ptr(7)) }
			s, err := a.viewRequest(mk())
			if err != nil {
				t.Fatal(err)
			}
			return a, s, mk, func(a *App) func(*wire.ViewEventInput) (*wire.Surface, error) { return a.viewRequest }
		}},
		{"the Signatures panel of a request that ran out", func(t *testing.T) (*App, *wire.Surface, func() *wire.ViewEventInput, func(*App) func(*wire.ViewEventInput) (*wire.Surface, error)) {
			a, f := newApp(t)
			s := walkRequestTimed(t, a, "ali@ornek.com", []map[string]any{
				sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)},
				nil, map[string]any{"deadline": "2026-09-21"})
			if out, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil || !out.OK {
				t.Fatalf("the request failed: %v %v", err, out)
			}
			f.Clock = f.Clock.AddDate(0, 0, 3)
			mk := func() *wire.ViewEventInput { return viewInput(ViewStatus, "open", "", nil, nil) }
			panel, err := a.viewStatus(mk())
			if err != nil {
				t.Fatal(err)
			}
			return a, panel, mk, func(a *App) func(*wire.ViewEventInput) (*wire.Surface, error) { return a.viewStatus }
		}},
	}
	for _, sc := range screens {
		t.Run(sc.name, func(t *testing.T) {
			_, s, mk, fn := sc.make(t)
			if len(s.Actions) == 0 {
				t.Fatal("this screen was expected to offer a button")
			}
			for _, b := range s.Actions {
				// Each press on a fresh copy of the app, as a person who
				// pressed only this one.
				a, s2, _, _ := sc.make(t)
				next := press(t, s2, b.ID, mk(), fn(a))
				if dead(s2, next) {
					t.Errorf("the %q button (%s) answers with the same screen: it does nothing when clicked",
						b.ID, views.In(b.Label, views.TR))
				}
			}
		})
	}
}
