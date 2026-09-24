package views

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	filexsign "github.com/brf-tech/filex-sign"
	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/verify"
)

// ── fixtures ───────────────────────────────────────────────────────────

func doc() Doc { return Doc{Ref: "in:0", Path: "docs://sözleşme.pdf", Name: "sözleşme.pdf"} }

func env() *envelope.Envelope {
	return &envelope.Envelope{
		Schema: envelope.SchemaVersion, ID: "e1", Status: envelope.StatusSent, Document: "sözleşme.pdf",
		Requester: envelope.Person{UserID: 1, Name: "Burak Faruk Şahin", Email: "burak@example.com"},
		Form:      true,
		Options: envelope.Options{PIN: "auto", ExpiryDays: 14, AllowDecline: true, Audit: true,
			Deliver: envelope.DeliverShare, DeliveryPIN: "auto", Message: "Lütfen cumaya kadar.",
			Output: envelope.Output{Mode: envelope.OutputVersion}},
		Signers: []envelope.Signer{
			{ID: "s1", Kind: envelope.KindInternal, Person: envelope.Person{UserID: 7, Name: "Gökçe"}, Status: envelope.SignerNotified},
			{ID: "s2", Kind: envelope.KindExternal, Person: envelope.Person{Email: "ali@ornek.com"}, Status: envelope.SignerPending,
				PageToken: "tok", PageURL: "https://filex.example/s/tok", PageExpires: "2026-10-01T00:00:00Z"},
			{ID: "s3", Kind: envelope.KindExternal, Person: envelope.Person{Name: "Elden Veren"}, Status: envelope.SignerPending,
				PageToken: "tok3", PageURL: "https://filex.example/s/tok3"},
		},
		Fields: []envelope.Field{
			{ID: "sig-1", Type: fields.TypeSignature, Page: 1, X: .1, Y: .8, W: .3, H: .08, Assignee: "s1", Label: "İmza"},
			{ID: "sig-2", Type: fields.TypeSignature, Page: 1, X: .5, Y: .8, W: .3, H: .08, Assignee: "s2", Label: "İmza"},
			{ID: "sig-3", Type: fields.TypeSignature, Page: 1, X: .5, Y: .6, W: .3, H: .08, Assignee: "s3", Label: "İmza"},
			{ID: "text-1", Type: fields.TypeText, Page: 1, X: .1, Y: .5, W: .3, H: .04, Assignee: "s1",
				Label: "Görev unvanı", Required: true, MaxLen: 40},
			{ID: "date-1", Type: fields.TypeDate, Page: 1, X: .1, Y: .4, W: .2, H: .04, Assignee: "s1",
				Label: "Tarih", Format: fields.DateDMY},
			{ID: "chk-1", Type: fields.TypeCheckbox, Page: 1, X: .1, Y: .3, W: .03, H: .03, Assignee: "s1", Label: "Kabul"},
		},
		CreatedAt: "2026-09-01T09:00:00Z", UpdatedAt: "2026-09-01T09:00:00Z",
	}
}

func requestState(step int) RequestState {
	return RequestState{Step: step,
		Signers:    []envelope.Person{{UserID: 7, Name: "Gökçe", Email: "gokce@example.com"}},
		Identities: "ali@ornek.com\nElden Veren",
		Fields:     env().Fields,
	}
}

// everySurface is every screen this package can draw, in one language.
func everySurface(t *testing.T, l Lang) map[string]*wire.Surface {
	t.Helper()
	e := env()
	labels := SignerLabels(e.Signers)
	out := map[string]*wire.Surface{
		"sign-self-1":           SignSelf(l, doc(), envelope.Person{Name: "Burak"}, SelfState{Step: SelfStepFields, Fields: e.Fields}, nil),
		"sign-self-2":           SignSelf(l, doc(), envelope.Person{Name: "Burak"}, SelfState{Step: SelfStepFill, Fields: e.Fields}, nil),
		"sign-self-3":           SignSelf(l, doc(), envelope.Person{Name: "Burak"}, SelfState{Step: SelfStepResult, Fields: e.Fields}, nil),
		"office-sign":           SignOffice(l, Doc{Name: "a.docx"}, true),
		"office-sign-no-engine": SignOffice(l, Doc{Name: "a.docx"}, false),
		"office-request":        RequestOffice(l, Doc{Name: "a.docx"}, true),
		"decline":               Decline(l, e, &e.Signers[0], nil),
		"not-a-signer":          NotASigner(l, e, true),
		"received":              Received(),
		"gone":                  Gone(T("gone", "yok")),
		"status":                Status(l, StatusInput{Doc: doc(), Env: e, SignaturesInFile: 1, CAName: "filex", CAFP: "AAAA"}),
		"status-empty":          Status(l, StatusInput{Doc: doc(), SignaturesInFile: 2}),
		"home":                  Home(l, HomeInput{CAOK: true, CAName: "filex", CAFP: "AAAA", Office: true, Admin: true}),
		"home-cards": Home(l, HomeInput{CAOK: true, Office: false, Truncated: true,
			ToSign:    []Card{{Document: "a.pdf", Path: "docs://a.pdf", Status: envelope.StatusSent, Signed: 0, Total: 2, Waiting: "Gökçe"}},
			Requested: []Card{{Document: "b.pdf", Path: "docs://b.pdf", Status: envelope.StatusInProgress, Signed: 1, Total: 2, Locked: true}},
			Signed:    []Card{{Document: "c.pdf", Path: "docs://c.pdf", Status: envelope.StatusCompleted, Signed: 2, Total: 2}}}),
		"verify-none":  Verify(l, VerifyInput{Doc: doc(), Report: &verify.Report{}}),
		"verify-some":  Verify(l, VerifyInput{Doc: doc(), Report: signedReport()}),
		"verify-error": Verify(l, VerifyInput{Doc: doc(), Err: "broken"}),
		"receipt": Receipt(l, ReceiptInput{Document: "sözleşme.pdf", Signer: e.Signers[0].Person,
			SignedAt: "2026-09-02", Serial: "AB", CertFP: "FFFF", CAName: "filex", CAFP: "AAAA",
			Files: []ReceiptFile{{Name: "a.p7b", What: T("bundle", "demet")}}, InApp: true}),
	}
	for _, step := range []int{StepSigners, StepOrder, StepBoxes, StepPlace, StepTiming, StepOptions, StepResult, StepReview} {
		st := requestState(step)
		out["request-"+stepTitle(EN, step)["en"]] = Request(l, doc(), st, labels, nil)
	}
	for _, step := range []int{FillStepIntro, FillStepFill, FillStepReview} {
		out["fill-"+string(rune('0'+step))] = Fill(l, doc(), e, &e.Signers[0], FillState{Step: step}, nil)
	}
	// ⚠ Boxes nobody named: their names come from DefaultLabels, and those
	// words only reach a screen through THIS fixture. Without it the
	// default name for a box could go back to being English-only and no
	// scan would notice — it happened once, which is why it is here.
	unnamed := env()
	for i := range unnamed.Fields {
		unnamed.Fields[i].Label = ""
	}
	for _, step := range []int{FillStepIntro, FillStepFill} {
		out["fill-unnamed-"+string(rune('0'+step))] =
			Fill(l, doc(), unnamed, &unnamed.Signers[0], FillState{Step: step}, nil)
	}
	return out
}

func signedReport() *verify.Report {
	return &verify.Report{FileBytes: 100, Pages: 1,
		Authorities: []verify.Cert{{Subject: "filex signing", FP: "AAAA"}},
		Signatures: []verify.Signature{{Index: 1, Name: "Gökçe", Email: "gokce@example.com",
			Valid: true, Trusted: true, CoversWholeFile: true, LaterChanges: verify.ChangedNothing,
			Cert:   verify.Cert{Subject: "Gökçe", Serial: "AB", FP: "FFFF", EKU: []string{"document signing"}},
			Issuer: verify.Cert{Subject: "filex signing", FP: "AAAA"}, HasIssuer: true,
			ChainRoot: "filex signing", RootTrusted: true, Algorithm: "ECDSA-SHA256"}}}
}

// ── the rules, checked by the SDK's own kit ────────────────────────────
//
// ⚠ These used to be hand-rolled here. They are the SDK's job: filex
// ships `pluginkit/plugintest` so every plugin is measured against the
// SAME renderer rules and the SAME language rules, and a rule that lives
// in one plugin's test file is a rule the next plugin gets wrong.
// What stays below is what is particular to THIS app.

func manifest() wire.Manifest { return filexsign.Manifest() }

// Every screen, in every declared language, against the renderer's rules
// and the language rules the host also applies at install.
func TestEverySurfaceIsDrawableAndSpeaksEveryLanguage(t *testing.T) {
	m := manifest()
	for _, l := range []Lang{EN, TR} {
		for name, s := range everySurface(t, l) {
			t.Run(name+"/"+string(l), func(t *testing.T) {
				plugintest.CheckSurface(t, m, s)
				plugintest.CheckLanguages(t, m, s)
			})
		}
	}
}

// The same screen drawn once per language has to be the SAME screen: a
// label that did not change is what a missing translation looks like
// from the outside. ⚠ This is the "Çiz / Yaz / Yükle under an English
// heading" bug, caught by the kit instead of by a person.
func TestEverySurfaceHasLocaleParity(t *testing.T) {
	en, tr := everySurface(t, EN), everySurface(t, TR)
	opts := plugintest.LangOpts{Strict: true, SameAllowed: sameInBothLanguages}
	for name, se := range en {
		st, ok := tr[name]
		if !ok {
			t.Fatalf("%s: only one language drew this screen", name)
		}
		t.Run(name, func(t *testing.T) {
			plugintest.CheckLocaleParityOpts(t, map[string]*wire.Surface{"en": se, "tr": st}, opts)
		})
	}
}

// A box's name is whoever placed it talking, a date example is a date,
// and a font family is a font family: none of them is a translation that
// went missing.
var sameInBothLanguages = []string{
	// What a person typed: a box's name, a signer's identity, a message,
	// a file name, a path. The app does not translate somebody's words.
	"İmza", "Görev unvanı", "Tarih", "Kabul", "“Lütfen cumaya kadar.”",
	// ...and the same names with the mark every required box wears.
	"İmza *", "Görev unvanı *", "Tarih *", "Kabul *",
	"Gökçe", "Gökçe (gokce@example.com)", "Burak", "Ali", "Elden Veren",
	"ali@ornek.com", "gokce@example.com",
	"sözleşme.pdf", "a.pdf", "b.pdf", "c.pdf", "kendi.pdf", "teklif.pdf", "baska.pdf", "a.p7b",
	"docs://a.pdf", "docs://b.pdf", "docs://c.pdf", "docs://a/sözleşme.pdf",
	// Formats, names and machine words: the layout IS the layout, a font
	// family is its family, a fingerprint is a fingerprint.
	"ad@ornek.com",
	"Caveat", "Dancing Script", "Homemade Apple", "Inter", "Source Serif 4",
	"filex", "filex signing", "ECDSA-SHA256", "document signing",
	"AA BB", "AAAA", "FFFF", "AB", "2026-09-02", "2026-12-31",
	"{stem}-signed{ext}",
}

// ⚠ Every date PATTERN and the example beside it, GENERATED rather than
// typed out. A layout is the same in every language (`DD.MM.YYYY` is not
// an English sentence), and since it became an arrangement and a separator
// chosen separately there are twenty of them — a hand-written list would
// go stale the day a separator is added, and go stale as a false FAILURE.
func init() {
	for _, f := range fields.Formats() {
		sameInBothLanguages = append(sameInBothLanguages, f, fields.Example(f))
	}
}

func TestTurkishCharactersSurviveEverywhere(t *testing.T) {
	bad := []string{"Imza", "imzaci", "Belge sec", "Gozden", "Duzenle", "Ileri", "Iptal", "Guvenlik", "Dogrula"}
	for _, l := range []Lang{EN, TR} {
		for name, s := range everySurface(t, l) {
			walkTexts(t, name, s, func(where string, txt wire.Text) {
				for _, b := range bad {
					if strings.Contains(txt["tr"], b) {
						t.Errorf("%s: %s reads as ASCII Turkish (%q): %q", name, where, b, txt["tr"])
					}
				}
			})
		}
	}
}

// ── the surface rules ──────────────────────────────────────────────────

// One step asks one thing: at most one primary button, plus Back.
func TestAtMostOnePrimaryActionPerSurface(t *testing.T) {
	for _, l := range []Lang{EN, TR} {
		for name, s := range everySurface(t, l) {
			n := 0
			for _, a := range s.Actions {
				if a.Primary {
					n++
				}
			}
			if n > 1 {
				t.Errorf("%s: %d primary buttons", name, n)
			}
		}
	}
}

// A select is drawn as a row of choice buttons, so its options have to
// stay few enough to read without clicking.
func TestSelectsStayReadable(t *testing.T) {
	for name, s := range everySurface(t, EN) {
		for _, f := range formFields(s) {
			if f.Type != "select" {
				continue
			}
			if len(f.Options) < 2 {
				t.Errorf("%s: select %q has %d options", name, f.Key, len(f.Options))
			}
			if len(f.Options) > 6 {
				t.Errorf("%s: select %q has %d options — too many to draw as buttons", name, f.Key, len(f.Options))
			}
		}
	}
}

// show_when / required_when must name a field of the SAME form, or the
// renderer hides a field forever and the host drops its value.
func TestConditionsReferenceRealFields(t *testing.T) {
	for name, s := range everySurface(t, EN) {
		for _, node := range flatten(s.Nodes) {
			if node.Type != "form" {
				continue
			}
			keys := map[string]bool{}
			fs := fieldsOf(node)
			for _, f := range fs {
				keys[f.Key] = true
			}
			for _, f := range fs {
				for _, c := range []*wire.Condition{f.ShowWhen, f.RequiredWhen} {
					if c == nil {
						continue
					}
					if !keys[c.Key] {
						t.Errorf("%s: %q depends on %q, which is not in the same form", name, f.Key, c.Key)
					}
					if len(c.Equals) == 0 {
						t.Errorf("%s: %q depends on %q but names no value", name, f.Key, c.Key)
					}
				}
			}
		}
	}
}

// ⚠ Burak's own bug: "the signed document goes: a new version of this
// file" while a file-name box sits above it asking for a name it will
// not use. The name is shown — and required — only for a new file.
func TestTheFileNameIsAskedOnlyWhenThereIsANewFile(t *testing.T) {
	for _, s := range []*wire.Surface{
		Request(EN, doc(), requestState(StepResult), nil, nil),
		SignSelf(EN, doc(), envelope.Person{Name: "Burak"}, SelfState{Step: SelfStepResult}, nil),
	} {
		var name *wire.Field
		for _, f := range formFields(s) {
			if f.Key == "output_name" {
				g := f
				name = &g
			}
		}
		if name == nil {
			t.Fatal("no output_name field on the step that chooses the output")
		}
		if name.ShowWhen == nil || name.ShowWhen.Key != "output_mode" ||
			len(name.ShowWhen.Equals) != 1 || name.ShowWhen.Equals[0] != envelope.OutputSibling {
			t.Errorf("output_name is not hidden behind output_mode = sibling: %+v", name.ShowWhen)
		}
		if name.RequiredWhen == nil || name.RequiredWhen.Key != "output_mode" {
			t.Errorf("output_name is not required when it is shown: %+v", name.RequiredWhen)
		}
		if name.Required {
			t.Error("output_name must not be unconditionally required — it is hidden most of the time")
		}
	}
}

// ── the wizard ─────────────────────────────────────────────────────────

func TestRequestWizardWalksItsSteps(t *testing.T) {
	one := RequestState{Signers: []envelope.Person{{Name: "Tek"}}, Step: StepSigners}
	if one.Next() != StepBoxes {
		t.Errorf("with one signer the order question must be skipped, went to %d", one.Next())
	}
	two := requestState(StepSigners)
	if two.Next() != StepOrder {
		t.Errorf("with two signers the order question is asked, went to %d", two.Next())
	}
	back := requestState(StepBoxes)
	if back.Prev() != StepOrder {
		t.Errorf("back from the boxes goes to the order, went to %d", back.Prev())
	}
	if requestState(StepPlace).Prev() != StepBoxes {
		t.Error("back from placing goes to the boxes themselves")
	}
	if requestState(StepBoxes).Next() != StepPlace {
		t.Error("naming the boxes is followed by placing them")
	}
}

// ⚠⚠ TWO steps, and each asks ONE thing.
//
// Naming a box and finding a place for it were one screen, and the owner's
// words for it were "you are still doing both at once". `define` draws the
// boxes as cards and NO document; `place` draws the document and nothing to
// type into. Either of them showing the other's controls is the bug back.
func TestBoxesAreDefinedFirstAndPlacedAfter(t *testing.T) {
	for _, tc := range []struct {
		step int
		mode string
	}{{StepBoxes, "define"}, {StepPlace, "place"}} {
		s := Request(TR, doc(), requestState(tc.step), nil, nil)
		editors, forms := 0, 0
		mode := ""
		for _, n := range flatten(s.Nodes) {
			switch n.Type {
			case "pdf-fields":
				editors++
				mode, _ = n.Props["mode"].(string)
			case "form":
				forms++
			}
		}
		if editors != 1 {
			t.Errorf("step %d should draw exactly one editor, drew %d", tc.step, editors)
		}
		if mode != tc.mode {
			t.Errorf("step %d draws the editor in %q, expected %q", tc.step, mode, tc.mode)
		}
		if forms != 0 {
			t.Errorf("step %d must ask nothing else, drew %d forms", tc.step, forms)
		}
	}
}

// A box that nobody has placed travels as `placed: false`, and that is the
// only way the screen (and the job) can tell it is still waiting.
func TestUnplacedBoxesTravelAsSuch(t *testing.T) {
	no := false
	f := envelope.Field{ID: "sig-1", Type: "signature", Page: 1, W: 0.2, H: 0.05, Placed: &no}
	if p := FieldProp(f); p["placed"] != false {
		t.Errorf("an unplaced box must say so on the wire: %+v", p)
	}
	if p := FieldProp(envelope.Field{ID: "sig-2", Type: "signature", Page: 1}); p["placed"] != nil {
		t.Errorf("a placed box says nothing about it: %+v", p)
	}
	if !(envelope.Field{ID: "x"}).IsPlaced() {
		t.Error("a field written before this reads as placed")
	}
	if n := len(envelope.Unplaced([]envelope.Field{f, {ID: "sig-2"}})); n != 1 {
		t.Errorf("one box is waiting, counted %d", n)
	}
}

func TestIdentityIsAName_AnAddress_OrBoth(t *testing.T) {
	people := RequestState{Identities: "Ali Yılmaz <ali@ornek.com>\nyalnız@adres.com\nYalnız Ad"}.People()
	if len(people) != 3 {
		t.Fatalf("three identities, got %d: %+v", len(people), people)
	}
	if people[0].Name != "Ali Yılmaz" || people[0].Email != "ali@ornek.com" {
		t.Errorf("name and address: %+v", people[0])
	}
	if people[1].Name != "" || people[1].Email != "yalnız@adres.com" {
		t.Errorf("address only: %+v", people[1])
	}
	if people[2].Name != "Yalnız Ad" || people[2].Email != "" {
		t.Errorf("name only: %+v", people[2])
	}
	if people[2].HasEmail() {
		t.Error("a name-only identity has no address to write to")
	}
	if people[2].Identity() != "Yalnız Ad" {
		t.Errorf("a name-only identity prints as its name, got %q", people[2].Identity())
	}
}

// ── the signer ─────────────────────────────────────────────────────────

// Every box has a name, the signer fills a form of those names, and each
// signature box gets its own pad in its own row.
func TestSignerFillsAFormOfNames(t *testing.T) {
	e := env()
	s := Fill(TR, doc(), e, &e.Signers[0], FillState{Step: FillStepFill}, nil)
	var keys []string
	for _, f := range formFields(s) {
		keys = append(keys, f.Key)
		if strings.TrimSpace(f.Label) == "" {
			t.Errorf("box %q is asked for without a name", f.Key)
		}
	}
	for _, want := range []string{"text-1", "date-1", "chk-1"} {
		if !contains(keys, want) {
			t.Errorf("the form does not ask for %q: %v", want, keys)
		}
	}
	pads := 0
	for _, n := range flatten(s.Nodes) {
		if n.Type == "signature-pad" {
			pads++
			if n.ID != PadFor("sig-1") {
				t.Errorf("the pad is not bound to its box: %q", n.ID)
			}
		}
	}
	if pads != 1 {
		t.Errorf("one signature box, %d pads", pads)
	}
	if editors := countType(s, "pdf-fields"); editors != 0 {
		t.Errorf("the fill step is a plain form, not the document: %d editors", editors)
	}
}

// …and THEN they see the document as it will be.
func TestSignerSeesTheDocumentBeforeApproving(t *testing.T) {
	e := env()
	st := FillState{Step: FillStepReview, Values: map[string]string{"text-1": "Müdür", "sig-1": "PNG"}}
	s := Fill(TR, doc(), e, &e.Signers[0], st, nil)
	if countType(s, "pdf-fields") != 1 {
		t.Error("the review step must show the document")
	}
	if !strings.Contains(render(s), "Müdür") {
		t.Error("the review step must list what was entered")
	}
	primary := ""
	for _, a := range s.Actions {
		if a.Primary {
			primary = a.ID
		}
	}
	if primary != "sign" {
		t.Errorf("the last step signs, its primary is %q", primary)
	}
}

func TestSignerWaitsItsTurnInASequentialRequest(t *testing.T) {
	e := env()
	e.Options.Order = envelope.OrderSequential
	s := Fill(TR, doc(), e, &e.Signers[1], FillState{Step: FillStepFill}, nil)
	if countType(s, "signature-pad") != 0 {
		t.Error("somebody whose turn it is not must not be offered a pad")
	}
	if !strings.Contains(render(s), "Gökçe") {
		t.Error("the screen should say who is being waited for")
	}
}

func TestSignerSeesOnlyItsOwnBoxes(t *testing.T) {
	e := env()
	mine := MyFields(e, "s2")
	for _, f := range mine {
		if f.Assignee != "" && f.Assignee != "s2" {
			t.Errorf("%s belongs to %s", f.ID, f.Assignee)
		}
	}
	if len(mine) != 1 {
		t.Errorf("s2 has one box, got %d", len(mine))
	}
}

// ── status and verification ────────────────────────────────────────────

func TestStatusNamesIdentitiesNotAddresses(t *testing.T) {
	e := env()
	s := Status(TR, StatusInput{Doc: doc(), Env: e, CAName: "filex", CAFP: "AA BB"})
	body := render(s)
	for _, want := range []string{"Kimlik", "Gökçe", "ali@ornek.com", "Elden Veren", "AA BB"} {
		if !strings.Contains(body, want) {
			t.Errorf("the panel does not mention %q", want)
		}
	}
	if strings.Contains(body, "E-posta: —") || strings.Contains(body, "e-mail: —") {
		t.Error("an empty half of an identity must not be printed")
	}
}

func TestVerifySaysWhatItFoundAndWhatItIsWorth(t *testing.T) {
	none := render(Verify(TR, VerifyInput{Doc: doc(), Report: &verify.Report{}}))
	if !strings.Contains(none, "elektronik imza yok") {
		t.Error("an unsigned document must say so plainly")
	}
	some := render(Verify(TR, VerifyInput{Doc: doc(), Report: signedReport()}))
	for _, want := range []string{"Kimlik", "İmza makamı", "SHA-256", "geçerlilik bilinmiyor", "e-imza"} {
		if !strings.Contains(some, want) {
			t.Errorf("the report does not mention %q", want)
		}
	}
}

// ⚠⚠ Both languages, in the one place a reader looks for a straight answer.
// The heading was built with a verdict that had ALREADY been resolved to one
// language and then dropped into both variants, so a Turkish screen read
// "1. imza — valid, from an authority this filex trusts".
func TestVerifyHeadingIsWrittenInBothLanguages(t *testing.T) {
	rep := signedReport()
	for _, l := range []Lang{EN, TR} {
		s := Verify(l, VerifyInput{Doc: doc(), Report: rep})
		var heads []wire.Text
		for _, n := range flatten(s.Nodes) {
			if n.Type != "text" {
				continue
			}
			txt, _ := n.Props["text"].(wire.Text)
			if strings.Contains(txt["en"], "Signature 1 —") {
				heads = append(heads, txt)
			}
		}
		if len(heads) != 1 {
			t.Fatalf("%s: expected one signature heading, got %d", l, len(heads))
		}
		if !strings.Contains(heads[0]["en"], "valid, from an authority this filex trusts") {
			t.Errorf("%s: the English heading lost its verdict: %q", l, heads[0]["en"])
		}
		if !strings.Contains(heads[0]["tr"], "geçerli, bu filex'in güvendiği bir makamdan") {
			t.Errorf("%s: the Turkish heading carries the wrong language: %q", l, heads[0]["tr"])
		}
	}
}

// The verification library speaks English only. Its sentences are translated
// where we know them, dropped where this card already says the same thing
// better, and shown as they are when we do not know them — never passed
// through as the only English on a Turkish screen.
func TestVerifyTranslatesTheVerifiersWarnings(t *testing.T) {
	rep := signedReport()
	rep.Signatures[0].Warnings = []string{
		"Using signature time as fallback - this time is provided by the signatory and should be considered untrusted",
		"No timestamp to validate",
		"something we have never seen",
	}
	body := render(Verify(TR, VerifyInput{Doc: doc(), Report: rep}))
	if strings.Contains(body, "provided by the signatory") {
		t.Error("the fallback-time warning repeats what the “Signed” row already says, in English")
	}
	if !strings.Contains(body, "zaman damgası yok") {
		t.Error("the missing-time-stamp warning was not translated")
	}
	if !strings.Contains(body, "something we have never seen") {
		t.Error("an unknown warning must still reach the reader")
	}
}

func TestReceiptSaysItCannotSign(t *testing.T) {
	for _, l := range []Lang{EN, TR} {
		s := Receipt(l, ReceiptInput{Document: "a.pdf", Signer: envelope.Person{Name: "Ali"}, CertFP: "FFFF"})
		body := render(s)
		want := l.S("identity receipt, not a signing capability", "kimlik makbuzudur, imza yeteneği değildir")
		if !strings.Contains(body, want) {
			t.Errorf("%s: the receipt does not say %q", l, want)
		}
		if !strings.Contains(body, "FFFF") {
			t.Error("the receipt must show the certificate fingerprint")
		}
	}
}

// ── helpers ────────────────────────────────────────────────────────────

func flatten(nodes []wire.Node) []wire.Node {
	var out []wire.Node
	for _, n := range nodes {
		out = append(out, n)
		out = append(out, flatten(n.Children)...)
	}
	return out
}

func countType(s *wire.Surface, typ string) int {
	n := 0
	for _, node := range flatten(s.Nodes) {
		if node.Type == typ {
			n++
		}
	}
	return n
}

func fieldsOf(n wire.Node) []wire.Field {
	fs, _ := n.Props["fields"].([]wire.Field)
	return fs
}

func formFields(s *wire.Surface) []wire.Field {
	var out []wire.Field
	for _, n := range flatten(s.Nodes) {
		if n.Type == "form" {
			out = append(out, fieldsOf(n)...)
		}
	}
	return out
}

// walkTexts visits every wire.Text a surface carries, wherever it sits.
func walkTexts(t *testing.T, name string, s *wire.Surface, fn func(where string, txt wire.Text)) {
	t.Helper()
	if s == nil {
		t.Fatalf("%s: no surface", name)
	}
	if s.Title != nil {
		fn("title", s.Title)
	}
	if s.Toast != nil {
		fn("toast", s.Toast)
	}
	for k, v := range s.Errors {
		fn("error "+k, v)
	}
	for _, a := range s.Actions {
		fn("action "+a.ID, a.Label)
	}
	for _, n := range flatten(s.Nodes) {
		walkValue(n.Props, n.Type, fn)
	}
}

func walkValue(v any, where string, fn func(string, wire.Text)) {
	switch x := v.(type) {
	case wire.Text:
		fn(where, x)
	case map[string]any:
		for k, sub := range x {
			walkValue(sub, where+"."+k, fn)
		}
	case []map[string]any:
		for _, sub := range x {
			walkValue(sub, where, fn)
		}
	case []any:
		for _, sub := range x {
			walkValue(sub, where, fn)
		}
	case map[string]wire.Text:
		for k, sub := range x {
			fn(where+"."+k, sub)
		}
	case []column:
		for _, c := range x {
			fn(where+"."+c.Key, c.Label)
		}
	}
}

// render is every word a surface shows, for a "does it say this" check.
func render(s *wire.Surface) string {
	var b strings.Builder
	walkAll(s, func(txt wire.Text) {
		b.WriteString(txt["en"])
		b.WriteByte('\n')
		b.WriteString(txt["tr"])
		b.WriteByte('\n')
	})
	for _, f := range formFields(s) {
		b.WriteString(f.Label + "\n" + f.Help + "\n" + f.Placeholder + "\n")
		for _, o := range f.Options {
			b.WriteString(o.Label + "\n")
		}
	}
	return b.String()
}

func walkAll(s *wire.Surface, fn func(wire.Text)) {
	if s.Title != nil {
		fn(s.Title)
	}
	if s.Toast != nil {
		fn(s.Toast)
	}
	for _, v := range s.Errors {
		fn(v)
	}
	for _, a := range s.Actions {
		fn(a.Label)
	}
	for _, n := range flatten(s.Nodes) {
		walkValue(n.Props, n.Type, func(_ string, txt wire.Text) { fn(txt) })
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// ── the Signatures screen ──────────────────────────────────────────────

// ⚠ The screen lists real documents now: filex grew `state_list`, so the
// "nothing was handed to me" apology is gone and must stay gone.
func TestHomeListsDocumentsAndOffersARowAction(t *testing.T) {
	in := HomeInput{CAOK: true, Admin: true,
		ToSign:    []Card{{Document: "sözleşme.pdf", Path: "docs://sözleşme.pdf", Status: envelope.StatusSent, Signed: 0, Total: 2, Waiting: "Gökçe", Deadline: "2026-12-31"}},
		Requested: []Card{{Document: "teklif.pdf", Path: "docs://teklif.pdf", Status: envelope.StatusInProgress, Signed: 1, Total: 2, Locked: true}},
		Signed:    []Card{{Document: "kendi.pdf", Path: "docs://kendi.pdf", Status: envelope.StatusCompleted, Signed: 1, Total: 1}},
		All:       []Card{{Document: "baska.pdf", Path: "docs://baska.pdf", Status: envelope.StatusSent, Signed: 0, Total: 1, Requester: "Ali"}}}
	// ⚠ One section per page now (the owner, 2026-09-21: "her biri ayrı bir
	// menü içinde farklı tablolar"): the page is walked the way a person
	// walks it, one menu entry at a time.
	var body string
	var pages []*wire.Surface
	for _, sec := range HomeSections(in) {
		in.Section = sec
		s := Home(TR, in)
		if s.Section != sec {
			t.Fatalf("asked for section %q, drew %q", sec, s.Section)
		}
		pages = append(pages, s)
		body += render(s)
	}
	for _, want := range []string{"sözleşme.pdf", "teklif.pdf", "kendi.pdf", "baska.pdf", "Gökçe", "2026-12-31", "dondurulmuş", "isteyen Ali"} {
		if !strings.Contains(body, want) {
			t.Errorf("the screen does not mention %q", want)
		}
	}
	if strings.Contains(body, "belgesiz açar") || strings.Contains(body, "without a document") {
		t.Error("the apology for having nothing to list is still there")
	}
	// Every row is addressable, and offers the one action its section is for.
	want := map[string]string{
		"docs://sözleşme.pdf": OpenSign,
		"docs://teklif.pdf":   OpenFollow,
		"docs://kendi.pdf":    OpenVerify,
		"docs://baska.pdf":    OpenFollow,
	}
	got := map[string]string{}
	var nodes []wire.Node
	for _, s := range pages {
		nodes = append(nodes, flatten(s.Nodes)...)
	}
	for _, n := range nodes {
		if n.Type != "list" {
			continue
		}
		rows, _ := n.Props["rows"].([]map[string]any)
		for _, r := range rows {
			id, _ := r["id"].(string)
			acts, _ := r["actions"].([]map[string]any)
			if len(acts) != 1 {
				t.Fatalf("row %q offers %d actions, expected one", id, len(acts))
			}
			got[id], _ = acts[0]["id"].(string)
		}
	}
	for path, action := range want {
		if got[path] != action {
			t.Errorf("row %q offers %q, expected %q", path, got[path], action)
		}
	}
	// The due day is a DATE column: filex prints it in the reader's words
	// ("31 Ara 2026"), not the ISO day the cell carries.
	dueIsDate := false
	for _, n := range nodes {
		if n.Type != "list" {
			continue
		}
		cols, _ := n.Props["columns"].([]map[string]any)
		for _, c := range cols {
			if c["key"] == "due" {
				dueIsDate = c["format"] == "date"
			}
		}
	}
	if !dueIsDate {
		t.Error(`the "Due" column must declare format "date"`)
	}
}

// Each section opens the document on the screen that section is about,
// and never names an action AND a view: the contract takes one.
func TestRowActionsNameTheRightScreen(t *testing.T) {
	want := map[string][2]string{
		OpenSign:   {"fill", ""},
		OpenVerify: {"verify", ""},
		OpenFollow: {"", "status"},
	}
	for rowAction, expect := range want {
		action, view := Target(rowAction)
		if action != expect[0] || view != expect[1] {
			t.Errorf("%s opens action=%q view=%q, expected %q/%q", rowAction, action, view, expect[0], expect[1])
		}
		if action != "" && view != "" {
			t.Errorf("%s names both an action and a view", rowAction)
		}
		if action == "" && view == "" {
			t.Errorf("%s names no screen at all", rowAction)
		}
	}
	if a, v := Target("nonsense"); a != "" || v != "" {
		t.Errorf("an unknown row action should name nothing, got %q/%q", a, v)
	}
}

// Every kind of box has a name in every declared language. The names
// reach a screen only when somebody left a box unnamed, so they are
// checked here as well as through the surfaces.
func TestDefaultBoxNamesSpeakEveryLanguage(t *testing.T) {
	en, tr := DefaultLabels(EN), DefaultLabels(TR)
	if len(en) != len(fields.Types()) {
		t.Fatalf("%d kinds of box, %d names", len(fields.Types()), len(en))
	}
	for _, kind := range fields.Types() {
		if strings.TrimSpace(en[kind]) == "" || strings.TrimSpace(tr[kind]) == "" {
			t.Errorf("%s has no name in one of the languages: %q / %q", kind, en[kind], tr[kind])
		}
		if en[kind] == tr[kind] {
			t.Errorf("%s is called %q in both languages — a name that did not change is a translation that went missing", kind, en[kind])
		}
	}
	// …and the fallback for a kind nobody knows.
	odd := envelope.Field{Type: "nonsense"}
	if LabelFor(EN, odd, 1) == LabelFor(TR, odd, 1) {
		t.Error("even the fallback name has to follow the language")
	}
}
