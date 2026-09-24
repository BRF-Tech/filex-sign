package app

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/views"
)

// ── the PINs the requester was promised ────────────────────────────────
//
// The owner, 2026-09-23: "imzalama pinlerini sadece siz görürsünüz dedin
// ama o pinleri göstermiyorsun bir yerde. O imzalama isteğinin ve imzalar
// bittikten sonraki dosya indirme paylaşımının pinlerini imzalar sayfasında
// ayrı bir sekmede tutuyor olmalıyız."
//
// ⚠ The app keeps NO PIN of its own. Every value on this screen comes from
// the platform's own store, through one audited host call (share_pin), and
// only for the person who made the link.

// pinRowsOf reads the PINs table: row id → the PIN cell, in Turkish.
func pinRowsOf(t *testing.T, s *wire.Surface) map[string]string {
	t.Helper()
	list := findNode(s, "list")
	if list == nil {
		t.Fatal("no PINs table")
	}
	out := map[string]string{}
	for _, r := range list.Props["rows"].([]map[string]any) {
		cells := r["cells"].(map[string]wire.Text)
		out[r["id"].(string)] = cells["pin"]["tr"]
	}
	return out
}

func pinRowID(t *testing.T, s *wire.Surface, kind string) string {
	t.Helper()
	for id := range pinRowsOf(t, s) {
		if strings.Contains(id, "|"+kind+"|") {
			return id
		}
	}
	t.Fatalf("no %s row in the PINs table", kind)
	return ""
}

func openPins(t *testing.T, a *App, actor wire.Actor) *wire.Surface {
	t.Helper()
	return homeAs(t, a, actor, "open", "", map[string]any{"section": views.SectionPins})
}

// The section exists, lists the requester's own links, and shows NOTHING
// until somebody asks: drawing the table must not read a single PIN, or a
// glance at the screen writes an audit row per link.
func TestPins_TheSectionListsTheLinksAndReadsNothing(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{sigBox("sig-1", "s2", .1), textBox("text-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	reads := len(f.PINReads)

	scr := openPins(t, a, burak)
	rows := pinRowsOf(t, scr)
	if len(rows) == 0 {
		t.Fatal("the outside signer's link is not listed")
	}
	for id, cell := range rows {
		if cell != "gizli" {
			t.Errorf("row %s shows %q before anybody asked", id, cell)
		}
	}
	if len(f.PINReads) != reads {
		t.Errorf("drawing the table read %d PINs", len(f.PINReads)-reads)
	}
	if !strings.Contains(surfaceWords(scr), "denetim izine yazılır") {
		t.Error("the screen must say that a read is recorded")
	}
}

// Asking shows the PIN — the one the link really carries — and the one the
// person can copy. Exactly one link is read.
func TestPins_ShowReadsExactlyOneAndItIsTheRealPIN(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{sigBox("sig-1", "s2", .1), textBox("text-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	env := loadEnv(t, a)
	token := env.Signers[1].PageToken
	want := f.Shares[token].PIN
	if want == "" {
		t.Fatal("the fixture has no PIN on the signing link")
	}

	scr := openPins(t, a, burak)
	id := pinRowID(t, scr, "signing")
	f.PINReads = nil
	shown := homeAs(t, a, burak, "action", views.ShowPIN, map[string]any{"row_id": id, "section": views.SectionPins})
	if got := pinRowsOf(t, shown)[id]; got != want {
		t.Errorf("the row shows %q, the link's PIN is %q", got, want)
	}
	if len(f.PINReads) != 1 || f.PINReads[0] != token {
		t.Errorf("one link was asked for, %v were read", f.PINReads)
	}
	// ...and it is offered where it can be copied, the way a link is.
	if !strings.Contains(surfaceWords(shown), want) {
		t.Error("the PIN is not offered to copy")
	}
}

// A PIN the platform cannot recover says SO, and says what does work.
func TestPins_WhenItCannotBeShownItSaysSoAndWhatToDo(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{sigBox("sig-1", "s2", .1), textBox("text-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	env := loadEnv(t, a)
	f.PINUnrecoverable[env.Signers[1].PageToken] = true

	scr := openPins(t, a, burak)
	id := pinRowID(t, scr, "signing")
	shown := homeAs(t, a, burak, "action", views.ShowPIN, map[string]any{"row_id": id, "section": views.SectionPins})
	words := surfaceWords(shown)
	if !strings.Contains(words, "artık gösterilemiyor") {
		t.Errorf("the screen must say the PIN is gone, not show a blank:\n%s", words)
	}
	if !strings.Contains(words, "yeniden gönderin") {
		t.Errorf("...and what does work:\n%s", words)
	}
	if got := pinRowsOf(t, shown)[id]; got == "" || got == "gizli" {
		t.Errorf("the row still pretends there is something to show: %q", got)
	}
}

// The links of a request somebody ELSE asked for are not this person's to
// read — not even an administrator's, on this screen.
func TestPins_OnlyTheRequestersOwnLinksAreListed(t *testing.T) {
	a, _ := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{sigBox("sig-1", "s2", .1), textBox("text-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	if len(pinRowsOf(t, openPins(t, a, burak))) == 0 {
		t.Fatal("the requester sees their own")
	}
	other := openPins(t, a, gokce)
	if n := len(pinRowsOf(t, other)); n != 0 {
		t.Errorf("somebody else's PINs are listed to %s: %d rows", gokce.Email, n)
	}
}

// A signing link that has ended offers no PIN: it would open nothing, and
// reading it would still be a read in the audit trail.
func TestPins_AnEndedLinkOffersNothing(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{sigBox("sig-1", "s2", .1), textBox("text-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	signAs(t, a, f, gokce, "", map[string]any{"text-1": "Genel Müdür"})
	env := loadEnv(t, a)
	ali := env.Signers[1]
	f.BindShare(ali.PageToken)
	page, err := a.pageSigner(pageEvent("open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4 && page.Job == nil; i++ {
		vals := map[string]any{}
		if page.State["step"] == views.FillStepFill {
			vals = padValue(t, "sig-1")
		}
		if page, err = a.pageSigner(pageEvent("submit", "", page.State, vals)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := a.actionApply(jobFrom(t, fromShare(t, page, ali.PageToken), burak, docName))
	if err != nil || !out.OK {
		t.Fatalf("the last signature failed: %v %v", err, out)
	}
	commit(t, f, out)

	scr := openPins(t, a, burak)
	rows := pinRowsOf(t, scr)
	id := pinRowID(t, scr, "signing")
	if rows[id] != "bağlantı sona erdi" {
		t.Errorf("a finished signing link still offers its PIN: %q", rows[id])
	}
	// ...and the download share the completed file went out on IS listed,
	// which is the second half of what was asked for.
	if _, ok := rows[pinRowID(t, scr, "delivery")]; !ok {
		t.Error("the download link is not in the table")
	}
}
