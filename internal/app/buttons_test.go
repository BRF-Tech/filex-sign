package app

import (
	"reflect"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

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
// asks the SCREEN how the button is pressed. (That screen is gone since
// 2026-10-02: this app no longer converts, and a document that is not a PDF
// gets a message with no button, pdfonly_test.go. The rule stays.)

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

// ⭐ No button on these screens is dead. Each one is pressed the way filex
// presses it, and each must queue a job, go somewhere, close, or at least
// change the screen.
func TestEveryFooterButton_DoesSomething(t *testing.T) {
	type screen struct {
		name string
		make func(t *testing.T) (*App, *wire.Surface, func() *wire.ViewEventInput, func(*App) func(*wire.ViewEventInput) (*wire.Surface, error))
	}
	screens := []screen{
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
