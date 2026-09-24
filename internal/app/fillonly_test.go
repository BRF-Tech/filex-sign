package app

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/verify"
	"github.com/brf-tech/filex-sign/internal/views"
)

// ── not everybody signs ────────────────────────────────────────────────
//
// The owner, 2026-09-23: "her kişi için imza yerleştirmek zorunlu olmasın
// bazı kişiler sadece metin doldurabilir. Bu kuralı da kapatalım lütfen."
//
// A participant may be asked only to FILL boxes in. What they submit is
// still committed cryptographically — an invisible signature over their
// own revision, so the chain of custody stays unbroken and Verify can say
// who changed what — but every word shown to them, and every word written
// about them, says FILLED rather than SIGNED.

// fillOnlyRequest: Gökçe signs, the outside person only fills a box in.
func fillOnlyRequest(t *testing.T, a *App) *wire.Surface {
	t.Helper()
	return walkRequest(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1),
		textBox("text-1", "s2", .5),
	}, nil)
}

// The wizard SENDS it. Before this it refused: "Place a signature box for:
// Ali Yılmaz" — and there was no way to say "Ali only fills in a field".
func TestFillOnly_TheWizardSendsIt(t *testing.T) {
	a, _ := newApp(t)
	s := fillOnlyRequest(t, a)
	out, err := a.actionRequest(jobFrom(t, s, burak, docName))
	if err != nil || !out.OK {
		t.Fatalf("a fill-only participant must be sendable: %v %v", err, out)
	}
	env := loadEnv(t, a)
	if env.Signs("s2") {
		t.Error("s2 has no signature box: the record must say so")
	}
	if !env.Signs("s1") {
		t.Error("s1 does have one")
	}
}

// The review says what each person is ASKED FOR, in those words.
func TestFillOnly_TheReviewSaysWhatEachPersonDoes(t *testing.T) {
	a, _ := newApp(t)
	s, err := a.viewRequest(viewInput(ViewRequest, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	boxes := []map[string]any{sigBox("sig-1", "s1", .1), textBox("text-1", "s2", .5), textBox("text-2", "s2", .3)}
	answers := map[int]map[string]any{
		views.StepSigners: {views.IDSigners: []map[string]any{{"user_id": 7, "email": gokce.Email, "name": gokce.Name}},
			"identities": "ali@ornek.com"},
		views.StepBoxes: {views.IDFields: boxes}, views.StepPlace: {views.IDFields: boxes},
	}
	for i := 0; i < 8; i++ {
		if step, _ := s.State["step"].(int); step == views.StepReview {
			break
		} else if s, err = a.viewRequest(viewInput(ViewRequest, "submit", "", s.State, answers[step])); err != nil {
			t.Fatal(err)
		}
	}
	list := findNode(s, "list")
	if list == nil {
		t.Fatal("no review table")
	}
	cells := map[string]wire.Text{}
	for _, r := range list.Props["rows"].([]map[string]any) {
		cells[r["id"].(string)] = r["cells"].(map[string]wire.Text)["boxes"]
	}
	if got := cells["s1"]["tr"]; got != "imzalar" {
		t.Errorf("the signer's row: %q", got)
	}
	if got := cells["s2"]["tr"]; got != "2 kutu doldurur" {
		t.Errorf("the fill-only row: %q", got)
	}
}

// Their own screen, their invitation and their button say FILL.
func TestFillOnly_TheirScreenAndTheirInvitationSayFill(t *testing.T) {
	a, _ := newApp(t)
	if _, err := a.actionRequest(jobFrom(t, fillOnlyRequest(t, a), burak, docName)); err != nil {
		t.Fatal(err)
	}
	env := loadEnv(t, a)
	ali := &env.Signers[1]

	scr := views.Fill(views.TR, views.Doc{}, env, ali, views.FillState{}, nil)
	words := surfaceWords(scr)
	for _, want := range []string{"doldurmanızı istiyor", "adınıza mühürlenir", "Katılmayacağım"} {
		if !strings.Contains(words, want) {
			t.Errorf("the fill-only screen does not say %q:\n%s", want, words)
		}
	}
	for _, never := range []string{"imzalamanızı istiyor", "İmzalamayacağım"} {
		if strings.Contains(words, never) {
			t.Errorf("the fill-only screen still says %q", never)
		}
	}
	if strings.Contains(scr.Title["tr"], "imzala") {
		t.Errorf("the title calls it signing: %q", scr.Title["tr"])
	}
	// ...and the signer who really signs still reads about signing.
	signer := views.Fill(views.TR, views.Doc{}, env, &env.Signers[0], views.FillState{}, nil)
	if !strings.Contains(surfaceWords(signer), "imzalamanızı istiyor") {
		t.Error("a signer is still asked to sign")
	}

	subject, body := inviteMail("tr", env, ali)
	if !strings.Contains(subject, "doldurmanızı") || !strings.Contains(body, "Sizden imza istenmiyor") {
		t.Errorf("the invitation does not ask for what is asked:\n%s\n%s", subject, body)
	}
	if strings.Contains(body, "İmzalamak için bağlantı") {
		t.Error("the invitation still offers a link to sign with")
	}
}

// End to end: a request whose only signature is somebody else's completes,
// is sealed, and the PDF says which act each party performed.
func TestFillOnly_CompletesSealedAndTheReportNamesTheAct(t *testing.T) {
	a, f := newApp(t)
	if _, err := a.actionRequest(jobFrom(t, fillOnlyRequest(t, a), burak, docName)); err != nil {
		t.Fatal(err)
	}
	signAs(t, a, f, gokce, "sig-1", nil)

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
			vals = map[string]any{"text-1": "Genel Müdür"}
		}
		if page, err = a.pageSigner(pageEvent("submit", "", page.State, vals)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := a.actionApply(jobFrom(t, fromShare(t, page, ali.PageToken), burak, docName))
	if err != nil || !out.OK {
		t.Fatalf("the fill-only submission failed: %v %v", err, out)
	}
	if !strings.Contains(out.Message["tr"], "doldurdu") {
		t.Errorf("the requester is told what happened: %q", out.Message["tr"])
	}
	final := commit(t, f, out)

	env = loadEnv(t, a)
	if env.Status != envelope.StatusCompleted {
		t.Fatalf("a request is complete when everybody has done THEIR part: %s", env.Status)
	}
	if env.Sealed == nil || env.Sealed.SHA256 == "" {
		t.Fatal("the final seal and the hash every party is sent are unchanged")
	}

	// ⚠⚠ The design decision, stated where it is measured: a fill-only
	// participant DOES sign, invisibly, over their own revision — that is
	// what keeps the chain attributable. What must never happen is the
	// report calling it a signature: the reason written inside the
	// signature, which every PDF reader shows, names the act.
	rep, err := verify.Inspect(final, bundle(t, f))
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, sig := range rep.Signatures {
		reasons = append(reasons, sig.Name+": "+sig.Reason)
	}
	joined := strings.Join(reasons, " | ")
	if !strings.Contains(joined, "dolduruldu") {
		t.Errorf("no signature says it was a filling in:\n%s", joined)
	}
	if !strings.Contains(joined, "imzalandı") {
		t.Errorf("the real signature must still say it was signed:\n%s", joined)
	}

	// The trail names the act too, per participant. ⚠ Measured on the
	// PARTICIPANT's own two lines, not on the whole page: the event log at
	// the bottom says "doldurdu" as well, so a search over the whole trail
	// stays green with the word put back to "imzaladı" where it matters.
	lines, title := a.auditLines(env, views.TR)
	trail := trailText(lines, title)
	if !strings.Contains(trail, "Katılımcılar") {
		t.Errorf("the trail still heads them all as signers:\n%s", trail)
	}
	for who, want := range map[string]string{"ali@ornek.com": "doldurdu", gokce.Email: "imzaladı"} {
		when := lineAfter(t, trail, who)
		if !strings.Contains(when, want) {
			t.Errorf("%s: the trail says %q, want %q:\n%s", who, when, want, trail)
		}
	}
	if strings.Contains(lineAfter(t, trail, "ali@ornek.com"), "imzaladı") {
		t.Error("the fill-only participant's own line calls it a signature")
	}
}

// lineAfter is the line that follows the one naming `who` — the "when"
// line the trail writes under each participant.
func lineAfter(t *testing.T, trail, who string) string {
	t.Helper()
	lines := strings.Split(trail, "\n")
	for i, l := range lines {
		if strings.Contains(l, who) && i+1 < len(lines) {
			return lines[i+1]
		}
	}
	t.Fatalf("%s is not in the trail:\n%s", who, trail)
	return ""
}

// A request with NO signature box at all — everybody only fills — still
// goes out, completes and is sealed.
func TestFillOnly_ARequestNobodySignsStillWorks(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "", []map[string]any{textBox("text-1", "s1", .1)}, nil)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	signAs(t, a, f, gokce, "", map[string]any{"text-1": "Genel Müdür"})
	env := loadEnv(t, a)
	if env.Status != envelope.StatusCompleted || env.Sealed == nil {
		t.Fatalf("a document nobody signs is still completed and sealed: %s %+v", env.Status, env.Sealed)
	}
}

// ...and the rule that is LEFT still bites: somebody invited to a document
// with nothing on it for them is a mistake, and the step says so.
func TestFillOnly_APersonWithNothingToDoIsStillRefused(t *testing.T) {
	a, _ := newApp(t)
	s, err := a.viewRequest(viewInput(ViewRequest, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	boxes := []map[string]any{sigBox("sig-1", "s1", .1)}
	answers := map[int]map[string]any{
		views.StepSigners: {views.IDSigners: []map[string]any{{"user_id": 7, "email": gokce.Email, "name": gokce.Name}},
			"identities": "ali@ornek.com"},
		views.StepBoxes: {views.IDFields: boxes},
	}
	for i := 0; i < 4; i++ {
		step, _ := s.State["step"].(int)
		if step == views.StepBoxes {
			s, err = a.viewRequest(viewInput(ViewRequest, "submit", "", s.State, answers[step]))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if s, err = a.viewRequest(viewInput(ViewRequest, "submit", "", s.State, answers[step])); err != nil {
			t.Fatal(err)
		}
	}
	msg := s.Errors[views.IDFields]
	if msg == nil || !strings.Contains(msg["tr"], "yapacağı bir şey yok") {
		t.Errorf("a person with no box at all must be refused, by name: %v", msg)
	}
	if strings.Contains(msg["tr"], "imza kutusu yerleştirin") {
		t.Error("the refusal must not ask for a SIGNATURE box")
	}
}
