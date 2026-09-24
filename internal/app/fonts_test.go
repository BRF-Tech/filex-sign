package app

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/fontkit/notofixture"
	"github.com/brf-tech/filex-sign/internal/host"
)

// ── text in any script: fetched fonts, and honest screens ─────────────

// withNotoOnNetwork puts the pinned files of these families on the fake
// host's network — the real bytes, so the pin is checked — or skips when
// they cannot be had here (unless FILEX_SIGN_REQUIRE_FONTS says they must).
func withNotoOnNetwork(t *testing.T, f *host.Fake, families ...string) {
	t.Helper()
	if f.Network == nil {
		f.Network = map[string][]byte{}
	}
	for _, fam := range families {
		found := false
		for _, n := range fontkit.PinnedFonts() {
			if n.Family != fam {
				continue
			}
			found = true
			b, err := notofixture.Fetch(n.URL, n.SHA256)
			if err != nil {
				if notofixture.Required() {
					t.Fatalf("%s: %v", fam, err)
				}
				t.Skipf("%s is not reachable here (%v)", fam, err)
			}
			f.Network[n.URL] = b
		}
		if !found {
			t.Fatalf("%s is not pinned", fam)
		}
	}
}

// fillVia answers a fill-screen event the way filex calls it: through the
// registered handler, which starts every call with a clean slate for the
// fonts (a face that failed in the previous call is asked for again).
func fillVia(a *App, in *wire.ViewEventInput) (*wire.Surface, error) {
	return a.Plugin().Views[ViewFill](in)
}

func nodeByID(s *wire.Surface, id string) *wire.Node {
	var found *wire.Node
	var walk func([]wire.Node)
	walk = func(ns []wire.Node) {
		for i := range ns {
			if ns[i].ID == id {
				found = &ns[i]
			}
			walk(ns[i].Children)
		}
	}
	walk(s.Nodes)
	return found
}

func notesOf(s *wire.Surface) []string {
	var out []string
	var walk func([]wire.Node)
	walk = func(ns []wire.Node) {
		for _, n := range ns {
			if strings.HasPrefix(n.ID, "print-note:") {
				out = append(out, n.ID)
			}
			walk(n.Children)
		}
	}
	walk(s.Nodes)
	return out
}

// fillStepFor queues a request with one text box for Gökçe and opens her
// fill step.
func fillStepFor(t *testing.T, a *App) *wire.Surface {
	t.Helper()
	boxes := []map[string]any{sigBox("sig-1", "s1", .1), merge(textBox("text-1", "s1", .5), map[string]any{"label": "Ad soyad"})}
	s := walkRequest(t, a, "", boxes, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	scr, err := a.viewFill(viewInputAs(gokce, ViewFill, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	scr, err = a.viewFill(viewInputAs(gokce, ViewFill, "submit", "", scr.State, nil))
	if err != nil {
		t.Fatal(err)
	}
	return scr
}

// ⚠⚠ The owner, 2026-09-21: characters that will not print are SAID while
// the person types — not discovered on the paper. Offline, a name typed in
// Japanese gets a line under the form at the next pause in typing (the
// host's `change` event); when the network is back the font is fetched
// ONCE, the line goes, and the next pause costs no download at all.
func TestFill_SaysWhatWillNotPrintWhileTyping(t *testing.T) {
	a, f := newApp(t)
	scr := fillStepFor(t, a)
	f.Offline = true

	// Kana beside the kanji: Japanese, so the Japanese face (bare kanji
	// would be read as Chinese — fontkit's hanKey).
	typed := map[string]any{"text-1": "山田 たろう"}
	scr, err := fillVia(a, viewInputAs(gokce, ViewFill, "change", "", scr.State, typed))
	if err != nil {
		t.Fatal(err)
	}
	note := nodeByID(scr, "print-note:text-1:unavailable")
	if note == nil {
		t.Fatalf("typing Japanese offline says nothing; notes: %v", notesOf(scr))
	}
	msg := note.Props["text"].(wire.Text)
	if !strings.Contains(msg["en"], "“Ad soyad”: 山田 たろう cannot be printed right now") ||
		!strings.Contains(msg["tr"], "“Ad soyad”: 山田 たろう şu anda basılamıyor") {
		t.Errorf("the note does not name the box and the characters: %v", msg)
	}
	if note.Props["tone"] != "danger" {
		t.Errorf("a note about lost text must stand out: tone %v", note.Props["tone"])
	}

	// The network comes back; at the next pause the face is fetched once.
	f.Offline = false
	withNotoOnNetwork(t, f, "Noto Sans JP")
	scr, err = fillVia(a, viewInputAs(gokce, ViewFill, "change", "", scr.State, typed))
	if err != nil {
		t.Fatal(err)
	}
	if n := notesOf(scr); len(n) != 0 {
		t.Errorf("with the font at hand nothing should be said: %v", n)
	}
	if len(f.Downloads) != 1 {
		t.Fatalf("one download expected, got %v", f.Downloads)
	}
	scr, err = fillVia(a, viewInputAs(gokce, ViewFill, "change", "", scr.State, typed))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Downloads) != 1 || len(notesOf(scr)) != 0 {
		t.Errorf("the second pause must cost no download: %v %v", f.Downloads, notesOf(scr))
	}
}

// ⚠ The approve step shows the value AS IT WILL BE PRINTED: offline, the
// document carries " Taro" in the box, the list says "printed as", and the
// note says why — what is approved is what is stamped.
func TestFill_ApproveStepShowsTheValueAsPrinted(t *testing.T) {
	a, f := newApp(t)
	scr := fillStepFor(t, a)
	f.Offline = true
	vals := map[string]any{"text-1": "山田 Taro", "pad:sig-1": map[string]any{"png_b64": padPNG(t), "mode": "draw"}}
	scr, err := fillVia(a, viewInputAs(gokce, ViewFill, "submit", "", scr.State, vals))
	if err != nil {
		t.Fatal(err)
	}
	if len(scr.Errors) > 0 {
		t.Fatalf("refused: %+v", scr.Errors)
	}
	doc := findNode(scr, "pdf-fields")
	if doc == nil {
		t.Fatal("no document on the approve step")
	}
	for _, fp := range doc.Props["fields"].([]map[string]any) {
		if fp["id"] == "text-1" && fp["value"] != " Taro" {
			t.Errorf("the document previews %q, the paper will carry %q", fp["value"], " Taro")
		}
	}
	words := surfaceWords(scr)
	for _, want := range []string{"山田 Taro — printed as “ Taro”", "山田 Taro — “ Taro” olarak basılacak"} {
		if !strings.Contains(words, want) {
			t.Errorf("the approve step does not say %q", want)
		}
	}
	if nodeByID(scr, "print-note:text-1:unavailable") == nil {
		t.Errorf("the approve step does not say why: %v", notesOf(scr))
	}
}

// A face the host is still downloading is "a moment", not "no internet".
func TestFill_AFontOnItsWayIsSaidAsSuch(t *testing.T) {
	a, f := newApp(t)
	scr := fillStepFor(t, a)
	f.Pending = true
	scr, err := fillVia(a, viewInputAs(gokce, ViewFill, "change", "", scr.State, map[string]any{"text-1": "محمد"}))
	if err != nil {
		t.Fatal(err)
	}
	note := nodeByID(scr, "print-note:text-1:downloading")
	if note == nil {
		t.Fatalf("notes: %v", notesOf(scr))
	}
	if msg := note.Props["text"].(wire.Text); !strings.Contains(msg["en"], "still being downloaded") || !strings.Contains(msg["tr"], "hâlâ indiriliyor") {
		t.Errorf("%v", msg)
	}
}

// ⚠ An installation that cannot fetch is the normal state on a LAN, and
// the screen asks at every pause. Every call is a fresh module, so the app
// cannot remember having said it: it says nothing, and the host logs the
// outage once (measured in filex: three pauses wrote fifteen lines while
// the app logged it itself).
func TestFill_AnUnreachableFontIsNotLoggedPerKeystroke(t *testing.T) {
	a, f := newApp(t)
	scr := fillStepFor(t, a)
	f.Offline = true
	before := len(f.Logs)
	for i := 0; i < 3; i++ {
		var err error
		scr, err = fillVia(a, viewInputAs(gokce, ViewFill, "change", "", scr.State, map[string]any{"text-1": strings.Repeat("山", i+1)}))
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range f.Logs[before:] {
		if strings.Contains(l, "could not be fetched") {
			t.Errorf("the app logged a failure the host already reports: %s", l)
		}
	}
	if nodeByID(scr, "print-note:text-1:unavailable") == nil {
		t.Errorf("…but the screen still says it: %v", notesOf(scr))
	}
}
