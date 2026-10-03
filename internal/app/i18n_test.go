package app

// es, de and fr, asked for by the owner on 2026-09-22 ("translate every text
// the signing app carries"), measured the way a person meets them: a whole
// request in each language — the mails an outside signer reads, the notices
// in filex, the audit trail, the Verify screen — and every letter those
// languages print checked against the faces the PDFs embed.
//
// ⚠ The host (filex ≤ v0.43 without feat/043-srvtext) collapses every
// locale that is not Turkish to "en" before it calls an app, so today a
// German viewer still reads English. These tests hand the app the locale
// directly — which is exactly what the host does once that branch lands.

import (
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/plugintest"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/host"
	"github.com/brf-tech/filex-sign/internal/stamp"
	"github.com/brf-tech/filex-sign/internal/views"
)

// completeIn is completeTwoSigners with the request sent in one language:
// the language of the request is what its mails, its trail and the lines
// under its signatures speak.
func completeIn(t *testing.T, a *App, f *host.Fake, lang string) []byte {
	t.Helper()
	s := walkRequestFull(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5),
	}, nil, nil, map[string]any{"lock_signed": true})
	job := jobFrom(t, s, burak, docName)
	job.Locale = lang
	if out, err := a.actionRequest(job); err != nil || !out.OK {
		t.Fatalf("the request failed: %v %+v", err, out)
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
			vals = padValue(t, "sig-2")
		}
		if page, err = a.pageSigner(pageEvent("submit", "", page.State, vals)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := a.actionApply(jobFrom(t, fromShare(t, page, ali.PageToken), burak, docName))
	if err != nil || !out.OK {
		t.Fatalf("the last signature failed: %v %v", err, out)
	}
	return commit(t, f, out)
}

func TestEveryLanguage_ARequestFromInvitationToSeal(t *testing.T) {
	for _, c := range []struct {
		lang                          string
		invite, sealed, trail, verify []string
	}{
		{"es",
			[]string{"le pide que firme un documento", "Abra este enlace para firmar"},
			[]string{"El documento firmado está listo", "SHA-256 del archivo firmado", "Cómo comprobarlo", "bloqueado de forma permanente"},
			[]string{"Registro de auditoría de firmas - ", "Certificación y sello", "Registro de eventos"},
			[]string{"Todas las firmas son válidas", "Sellado por filex", "Certificado por"}},
		{"de",
			[]string{"bittet Sie, ein Dokument zu unterschreiben", "Öffnen Sie diesen Link, um zu unterschreiben"},
			[]string{"Das unterschriebene Dokument ist fertig", "SHA-256 der unterschriebenen Datei", "So prüfen Sie das", "dauerhaft gesperrt"},
			[]string{"Audit-Protokoll der Unterschriften - ", "Zertifizierung und Siegel", "Ereignisprotokoll"},
			[]string{"Alle Unterschriften sind gültig", "Von filex versiegelt", "Zertifiziert von"}},
		{"fr",
			[]string{"vous demande de signer un document", "Ouvrez ce lien pour signer"},
			[]string{"Le document signé est prêt", "SHA-256 du fichier signé", "Comment vérifier", "verrouillé définitivement"},
			[]string{"Journal d’audit des signatures - ", "Certification et sceau", "Journal des événements"},
			[]string{"Toutes les signatures sont valides", "Scellé par filex", "Certifié par"}},
	} {
		t.Run(c.lang, func(t *testing.T) {
			a, f := newApp(t)
			completeIn(t, a, f, c.lang)
			l := views.Of(c.lang)

			// The outside signer's mails, in order: the invitation first, the
			// receipt, and the completion notice with the hash last.
			var toAli []string
			for _, m := range f.Mails {
				if m.To == "ali@ornek.com" {
					toAli = append(toAli, m.Subject+"\n"+m.Body)
				}
			}
			if len(toAli) < 3 {
				t.Fatalf("the outside signer got %d mail(s), want the invitation, the receipt and the completion", len(toAli))
			}
			invite, sealed := toAli[0], toAli[len(toAli)-1]
			says(t, "the invitation", invite, c.invite, []string{"asks you to sign", "Open this link"})
			says(t, "the completion mail", sealed, c.sealed, []string{"How to check", "SHA-256 of the signed file"})

			// The inside signer's completion notice, in the request's language.
			var notice string
			for _, n := range f.Notices {
				if n.ToUserID == gokce.ID && strings.Contains(views.In(n.Body, l), "SHA-256") {
					notice = views.In(n.Title, l) + "\n" + views.In(n.Body, l)
				}
			}
			says(t, "the inside signer's notice", notice, c.sealed[:1], []string{"The signed document is ready"})

			env := loadEnv(t, a)
			lines, title := a.auditLines(env, l)
			says(t, "the audit trail", trailText(lines, title), c.trail, []string{"Signature audit trail", "Event log"})

			in := viewInput(ViewVerify, "open", "", nil, nil)
			in.Context.Locale = c.lang
			s, err := a.viewVerify(in)
			if err != nil {
				t.Fatal(err)
			}
			says(t, "Verify", wordsIn(s, l), c.verify, []string{"Every signature is valid", "Sealed by filex"})
		})
	}
}

// says checks that a text is in the language: every expected phrase, and
// none of the English ones it would fall back to.
func says(t *testing.T, what, text string, want, english []string) {
	t.Helper()
	if text == "" {
		t.Errorf("%s: nothing was sent", what)
		return
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("%s does not say %q:\n%s", what, w, text)
		}
	}
	for _, w := range english {
		if strings.Contains(text, w) {
			t.Errorf("%s still says %q in English:\n%s", what, w, text)
		}
	}
}

// wordsIn is every word a surface shows in one language.
func wordsIn(s *wire.Surface, l views.Lang) string {
	var b strings.Builder
	for _, x := range plugintest.SurfaceTexts(s, views.Languages()) {
		b.WriteString(views.In(x.Text, l) + "\n")
	}
	return b.String()
}

// The request wizard drawn in every declared language is the same wizard:
// no section only one language has, no label left empty — plugintest's
// locale parity, which also reports words that came out identical in two
// languages (what a missing translation looks like from outside).
func TestEveryLanguage_TheWizardIsTheSameInEach(t *testing.T) {
	a, _ := newApp(t)
	byLocale := map[string]*wire.Surface{}
	for _, lang := range views.Languages() {
		in := viewInput(ViewRequest, "open", "", nil, nil)
		in.Context.Locale = lang
		s, err := a.viewRequest(in)
		if err != nil {
			t.Fatal(err)
		}
		byLocale[lang] = s
	}
	plugintest.CheckLocaleParityOpts(t, byLocale, plugintest.LangOpts{SameAllowed: []string{"PIN", "filex", "PDF", "No", "Texto"}})
}

// ⚠ Every letter the audit trail prints, in every language, is in the
// embedded Inter subset — ß, é, ñ, œ, the guillemets, and the two
// no-break spaces French typography puts before “:” and “;” (a space the
// face lacks is a hole in the line, not a space).
func TestAudit_EveryLanguageIsInTheEmbeddedFace(t *testing.T) {
	a, _ := newApp(t)
	inter := fontkit.Get(fontkit.Inter)
	must := map[views.Lang]string{
		views.ES: "ßéñœÍ¿¡",
		views.DE: "ßéñœäöü„“",
		views.FR: "ßéñœ«»’\u00a0\u202f",
	}
	for _, lang := range []views.Lang{views.EN, views.TR, views.ES, views.DE, views.FR} {
		env := turkishHeavy(string(lang))
		env.Requester.Name = "Íñigo Peña"
		env.Signers[1].Person.Name = "Jürgen Weiß"
		env.Signers[2].Person.Name = "Hélène Lœuvre"
		env.Options.Message = "¿Podría firmar antes del viernes? ¡Gracias!"
		lines, title := a.auditLines(env, lang)
		all := title
		for _, l := range lines {
			all += l.Text + "\n"
		}
		seen := map[rune]bool{}
		for _, r := range all {
			if seen[r] || r == ' ' || r == '\n' {
				continue
			}
			seen[r] = true
			g, err := inter.Shape(string(r))
			if err != nil {
				t.Fatal(err)
			}
			if len(g) != 1 {
				t.Errorf("%s trail: %q (U+%04X) is not in the Inter subset and would be borrowed or dropped", lang, r, r)
			}
		}
		for _, r := range must[lang] {
			if !seen[r] {
				t.Errorf("the %s fixture no longer prints %q (U+%04X); the check above proves less than it says", lang, r, r)
			}
		}
	}
}

// The lines printed under a signature, in every language, are all in the
// face the stamp is drawn with.
func TestStamp_EveryLanguageIsInTheStampFace(t *testing.T) {
	face := fontkit.StampFace()
	f := stamp.Facts{Name: "Jürgen Weiß-Peña", Email: "francoise@exemple.fr", When: time.Date(2026, 9, 22, 8, 44, 0, 0, time.UTC),
		IP: "203.0.113.9", CertFP: "AB12 CD34 EF56 7890 1122", Serial: "4F2A", Authority: "filex signing CA"}
	for _, lang := range views.Languages() {
		text := strings.Join(stamp.Lines(lang, stamp.IDs(), f), "\n")
		if text == "" {
			t.Fatalf("%s: no lines", lang)
		}
		for _, r := range text {
			if r == ' ' || r == '\n' {
				continue
			}
			if g, err := face.Shape(string(r)); err != nil || len(g) != 1 {
				t.Errorf("%s stamp: %q (U+%04X) is not in the stamp face", lang, r, r)
			}
		}
	}
	// The date is written the way each language's readers write it.
	for lang, want := range map[string]string{"en": "Signed 2026-09-22 08:44 UTC", "tr": "İmza tarihi: 22.09.2026 08:44 UTC",
		"de": "Unterschrieben am 22.09.2026 08:44 UTC", "fr": "Signé le 22/09/2026 08:44 UTC", "es": "Firmado el 22/09/2026 08:44 UTC"} {
		if got := stamp.DateLine(f.When, lang); got != want {
			t.Errorf("%s: the date line is %q, want %q", lang, got, want)
		}
	}
}
