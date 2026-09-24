package app

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/views"
)

// ── a box's NAME takes spaces, in any script ───────────────────────────
//
// The owner, 2026-09-23: "kutucuğa isim verirken isimde boşluk
// bırakamıyorum hiç izin vermiyor". The screen trimmed the name on every
// keystroke and wrote the trimmed one straight back into the input, so the
// space typed after a word was removed before the next letter arrived. The
// plugin's half of that is here: what it hands back is what it was given,
// and the record is trimmed once, when the request is sent.

// stateFields reads the boxes the plugin is holding between steps.
func stateFields(t *testing.T, s *wire.Surface) []envelope.Field {
	t.Helper()
	var st views.RequestState
	if err := roundTrip(s.State, &st); err != nil {
		t.Fatalf("unreadable wizard state: %v", err)
	}
	return st.Fields
}

func TestBoxName_RoundTripsWithItsSpaces(t *testing.T) {
	a, _ := newApp(t)
	s, err := a.viewRequest(viewInput(ViewRequest, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]any{"step": views.StepBoxes, "identities": "ali@ornek.com"}
	typed := ""
	for _, r := range "Ali Veli" {
		typed += string(r)
		box := map[string]any{"id": "text-1", "type": "text", "page": 1,
			"x": .1, "y": .1, "w": .2, "h": .04, "label": typed}
		s, err = a.viewRequest(viewInput(ViewRequest, "change", "", state,
			map[string]any{views.IDFields: []map[string]any{box}}))
		if err != nil {
			t.Fatal(err)
		}
		state = s.State
		got := stateFields(t, s)
		// ⚠ "Ali " is the whole bug: the moment the plugin hands back "Ali",
		// the input is rewritten and the next letter lands against the word.
		if len(got) != 1 || got[0].Label != typed {
			t.Fatalf("after typing %q the plugin holds %q", typed, labelOfFirst(got))
		}
	}
}

// A name written in one script and a name written in another are both
// names. The IDENTITY is what has to be ASCII, and it is derived, never
// shown, and unique in the document.
func TestBoxName_AnyScript_AndTheIdentityStaysValid(t *testing.T) {
	fs := []envelope.Field{
		{ID: "a", Type: "text", Label: "Müşteri adı"},
		{ID: "b", Type: "text", Label: "Müşteri  adı"}, // folds to the same identity
		{ID: "c", Type: "text", Label: "顧客名"},          // nothing ASCII at all
		{ID: "d", Type: "signature", Label: strings.Repeat("çok uzun ad ", 20)},
	}
	envelope.AssignKeys(fs, nil)
	want := []string{"musteri-adi", "musteri-adi-2", "text", "cok-uzun-ad-cok-uzun-ad-cok-uzun-ad-cok"}
	seen := map[string]bool{}
	for i, f := range fs {
		if f.Key != want[i] {
			t.Errorf("box %s: identity %q, want %q", f.ID, f.Key, want[i])
		}
		if f.Key == "" || len(f.Key) > fields.MaxSlug || seen[f.Key] {
			t.Errorf("box %s: an identity must be there, short and unique: %q", f.ID, f.Key)
		}
		seen[f.Key] = true
		for _, r := range f.Key {
			if r > 0x7f {
				t.Errorf("box %s: the identity is ASCII: %q", f.ID, f.Key)
			}
		}
		// ...and the PDF names the field by the identity, never by the name.
		if f.FormName() != f.Key {
			t.Errorf("box %s: the PDF must use the identity", f.ID)
		}
	}
}

// A name that is nothing but spaces is REFUSED, with a reason. It is not
// folded into "no name": a box nobody named wears its kind's name, which
// is a choice, and making that choice silently for somebody who meant to
// name the box is how a name disappears without a word.
func TestBoxName_SpacesAloneAreRefusedWithAReason(t *testing.T) {
	a, _ := newApp(t)
	box := map[string]any{"id": "text-1", "type": "text", "page": 1,
		"x": .1, "y": .1, "w": .2, "h": .04, "label": "   "}
	s, err := a.viewRequest(viewInput(ViewRequest, "submit", "",
		map[string]any{"step": views.StepBoxes, "identities": "ali@ornek.com"},
		map[string]any{views.IDFields: []map[string]any{box}}))
	if err != nil {
		t.Fatal(err)
	}
	if step, _ := s.State["step"].(int); step != views.StepBoxes {
		t.Errorf("the step moved on with a box named with spaces: %d", step)
	}
	msg := s.Errors[views.IDFields]
	if msg == nil || !strings.Contains(msg["tr"], "boşluk") || !strings.Contains(msg["en"], "spaces") {
		t.Errorf("the refusal must say why: %v", msg)
	}
}

// The ONE place a name is trimmed: the record the request is sent with.
func TestBoxName_TheSentRecordIsTrimmedOnce(t *testing.T) {
	a, _ := newApp(t)
	boxes := []map[string]any{{
		"id": "sig-1", "type": "signature", "page": 1, "x": .1, "y": .8, "w": .3, "h": .08,
		"assignee": "s1", "label": "  Yetkili  imzası  ",
	}}
	s := walkRequest(t, a, "", boxes, nil)
	job := jobFrom(t, s, burak, docName)
	out, err := a.actionRequest(job)
	if err != nil || !out.OK {
		t.Fatalf("the request was refused: %v %v", err, out)
	}
	env := loadEnv(t, a)
	if got := env.Fields[0].Label; got != "Yetkili  imzası" {
		t.Errorf("the stored name is trimmed at the ends and untouched inside: %q", got)
	}
	if got := env.Fields[0].Key; got != "yetkili-imzasi" {
		t.Errorf("the identity is derived from the tidy name: %q", got)
	}
}

func labelOfFirst(fs []envelope.Field) string {
	if len(fs) == 0 {
		return "<nothing>"
	}
	return fs[0].Label
}
