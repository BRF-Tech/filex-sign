package app

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/views"
)

// ⚠ "Request signatures…" is an app permission (PermRequest). filex refuses
// the action and its wizard to a person without it, but the hints that point
// at it are drawn by this app: the Signatures panel, the Verify screen, the
// Signatures screen's "How this works" and the answer to an apply on a
// document with no request. They used to be drawn for everybody, including a
// person the menu no longer offered it to (docs/SIGN.md §20). filex 0.49.0
// says which of the app's permissions the reader holds
// (wire.Actor.Permissions), and the hints follow it.
func TestHints_RequestSignaturesIsOfferedOnlyToWhoMayAsk(t *testing.T) {
	const ask = "Request signatures…"
	const askTR = "İmza iste…"
	holder := gokce
	holder.Permissions = []string{PermRequest}
	cases := []struct {
		name string
		who  wire.Actor
		want bool
	}{
		{"holds request", holder, true},
		{"does not hold request", gokce, false},
	}
	for _, c := range cases {
		a, _ := newApp(t)
		check := func(where, body string) {
			t.Helper()
			if got := strings.Contains(body, ask) && strings.Contains(body, askTR); got != c.want {
				t.Errorf("%s, %s: says “%s”=%v, want %v:\n%s", c.name, where, ask, got, c.want, body)
			}
			if !strings.Contains(body, "Sign…") || !strings.Contains(body, "İmzala…") {
				t.Errorf("%s, %s: signing yourself is always offered:\n%s", c.name, where, body)
			}
		}

		s, err := a.viewStatus(viewInputAs(c.who, ViewStatus, "open", "", nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		check("the Signatures panel", surfaceWords(s))

		s, err = a.viewVerify(viewInputAs(c.who, ViewVerify, "open", "", nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		check("Verify", surfaceWords(s))

		s = homeAs(t, a, c.who, "open", "", map[string]any{"section": views.SectionAbout})
		check("the Signatures screen", surfaceWords(s))

		out, err := a.actionApply(&wire.ActionRunInput{ActionID: ActionApply, Locale: "tr", Actor: c.who,
			Inputs: []wire.FileRef{{Ref: "in:0", Name: docName}}, Params: map[string]any{"op": "remind"}})
		if err != nil {
			t.Fatal(err)
		}
		if out.OK {
			t.Fatalf("%s: an apply on a document with no request must fail", c.name)
		}
		if got := strings.Contains(out.Message["en"], ask); got != c.want {
			t.Errorf("%s, apply: says “%s”=%v, want %v: %q", c.name, ask, got, c.want, out.Message["en"])
		}
		if !strings.Contains(out.Message["tr"], "imza isteği yok") {
			t.Errorf("%s, apply: %q", c.name, out.Message["tr"])
		}
	}

	// A filex that does not say (no actor on the call) is read as "does
	// not hold": a hint left out is a smaller wrong than a door offered and
	// then shut.
	a, _ := newApp(t)
	in := viewInputAs(gokce, ViewVerify, "open", "", nil, nil)
	in.Context.Actor = nil
	s, err := a.viewVerify(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(surfaceWords(s), ask) {
		t.Error("with no actor, Verify must not point at Request signatures…")
	}
}
