package views

import (
	"errors"
	"strings"
	"testing"
	"unicode"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fontkit"
)

func offline(t *testing.T) {
	t.Helper()
	fontkit.SetFetcher(func(fontkit.NotoFont) ([]byte, error) { return nil, errors.New("unavailable: no network") })
	t.Cleanup(func() { fontkit.SetFetcher(nil) })
}

func noteIn(s *wire.Surface, id string) wire.Text {
	for _, n := range s.Nodes {
		if n.ID == id {
			return n.Props["text"].(wire.Text)
		}
	}
	return nil
}

// ⚠ The define step: a box name the audit trail cannot print is said
// while it is typed — here in a script no font exists for at all, which is
// told apart from "the network is down" and names the script.
func TestRequest_ABoxNameThatWillNotPrintIsSaid(t *testing.T) {
	offline(t)
	var garay rune
	if tab := unicode.Scripts["Garay"]; tab != nil && len(tab.R32) > 0 {
		garay = rune(tab.R32[0].Lo)
	} else {
		t.Skip("this Go has no Garay table")
	}
	st := NewRequestState()
	st.Step = StepBoxes
	st.Fields = []envelope.Field{{ID: "text-1", Type: "text", Label: "Ad " + string(garay), Page: 1}}
	s := Request(EN, doc(), st, nil, nil)
	msg := noteIn(s, "print-note:box-text-1:no_font")
	if msg == nil {
		t.Fatalf("no note for a box name in Garay: %+v", s.Nodes)
	}
	if !strings.Contains(msg["en"], "draws the Garay script") || !strings.Contains(msg["tr"], "Garay yazısını çizmiyor") ||
		!strings.Contains(msg["en"], "call the box by its kind") {
		t.Errorf("%v", msg)
	}
}

// The signers step: a name typed in a script that cannot be printed right
// now is said at once — it is what goes under the signature.
func TestRequest_ASignerNameThatWillNotPrintIsSaid(t *testing.T) {
	offline(t)
	st := NewRequestState()
	st.Step = StepSigners
	st.Identities = "محمد عبد الله <m@example.com>\nAyşe Yılmaz"
	s := Request(TR, doc(), st, nil, nil)
	msg := noteIn(s, "print-note:signer-1:unavailable")
	if msg == nil {
		t.Fatalf("no note for an Arabic name offline: %+v", s.Nodes)
	}
	if !strings.Contains(msg["tr"], "“محمد عبد الله”: محمد عبد الله şu anda basılamıyor") || !strings.Contains(msg["tr"], "İmzanın altındaki satırlar") {
		t.Errorf("%v", msg["tr"])
	}
	if noteIn(s, "print-note:signer-2:unavailable") != nil {
		t.Error("a Turkish name prints: nothing to say")
	}
}

// ⚠ The option says what each answer means: "lock" alone reads like the
// freeze beside it.
func TestRequest_TheLockAfterCompletionSaysWhatEachChoiceMeans(t *testing.T) {
	st := NewRequestState()
	st.Step = StepOptions
	for _, l := range []Lang{EN, TR} {
		s := Request(l, doc(), st, nil, nil)
		var help string
		for _, n := range s.Nodes {
			if n.Type != "form" {
				continue
			}
			for _, f := range n.Props["fields"].([]wire.Field) {
				if f.Key == "lock_signed" {
					help = f.Help
				}
			}
		}
		yes, no := "Yes:", "No:"
		if l == TR {
			yes, no = "Evet:", "Hayır:"
		}
		if !strings.Contains(help, yes) || !strings.Contains(help, no) {
			t.Errorf("%s: the lock option must say what yes and no mean: %q", l, help)
		}
	}
}
