package views

import (
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/verify"
)

// ⚠⚠ A Turkish reader reads Turkish — every word this app puts in front of
// them, not only the words the parity test compares side by side.
//
// 2026-09-26, the maintainer: parts of the signing app were English while
// filex was Turkish. Some of it was the host (it told the app the ACCOUNT's
// language, not the screen's), and the rest was here, where no test looked:
//
//   - a form field's label, help and placeholder, and a select's options,
//     were ONE string in the language the app was told — nothing a Turkish
//     screen could pick from ("Identity", "One signer per line…");
//   - English that arrived as a VALUE inside a Turkish sentence: the
//     certificate's uses ("document signing, e-mail protection"), the host's
//     reason signing is off ("FILEX_SECRET_KEY is not set");
//   - a sentence whose Turkish said the numbers the wrong way round ("5000
//     baytın ilki 8000 bayt").
//
// Two rules, each red on the code before that day:
//
//  1. every field text the app translates travels in every language, so the
//     screen's own language wins whatever the host told the app;
//  2. no word of the ENGLISH build of a screen appears in its TURKISH build,
//     except the names, codes and user words listed below — which is what
//     English leaking into Turkish looks like from the outside.

// turkishScreens is everySurface plus the screens it does not draw.
func turkishScreens(t *testing.T, l Lang) map[string]*wire.Surface {
	t.Helper()
	out := everySurface(t, l)
	e := env()
	out["home-signing-off"] = Home(l, HomeInput{CAOK: false, CAReason: "FILEX_SECRET_KEY is not set"})
	out["home-signing-off-other"] = Home(l, HomeInput{CAOK: false, CAReason: "certificate: permission_denied: the app holds no sign grant"})
	out["verify-uses-and-part"] = Verify(l, VerifyInput{Doc: doc(), Report: partReport()})
	out["verify-unreadable"] = Verify(l, VerifyInput{Doc: doc(), Err: In(T("it is not a PDF", "PDF değil"), l)})
	out["already-requested"] = AlreadyRequested(l, doc(), e)
	out["read-only-sign"] = ReadOnlyDoc(l, doc(), false)
	out["read-only-request"] = ReadOnlyDoc(l, doc(), true)
	out["status-expired-link"] = Status(l, StatusInput{Doc: doc(), Env: e, SignaturesInFile: 1, Expired: true,
		RemindDue: true, ShowLinkFor: "s2", CAName: "filex", CAFP: "AAAA"})
	return out
}

// partReport is one signature over part of the file, with every use a
// certificate can state.
func partReport() *verify.Report {
	r := signedReport()
	s := &r.Signatures[0]
	s.CoversWholeFile, s.SignedBytes, s.FileBytes = false, 5000, 8000
	s.LaterChanges = verify.ChangedFields
	s.Cert.EKU = []string{"document signing", "e-mail protection", "client authentication", "usage 99"}
	s.Cert.KeyUsage = []string{"digital signature", "non-repudiation", "key encipherment", "certificate signing", "CRL signing"}
	return r
}

// allowedInTurkish are words the English build shares with the Turkish one
// on purpose: names, codes and what a person typed. A word goes here only
// when it is NOT a translation that went missing.
var allowedInTurkish = map[string]bool{
	// Products, formats, standards, algorithms.
	"filex": true, "adobe": true, "acrobat": true, "reader": true, "foxit": true,
	"pades": true, "etsi": true, "cades": true, "detached": true, "ecdsa": true, "sha256": true,
	"docmdp": true, "okular": true, "firefox": true, "noto": true, "google": true, "fonts": true,
	"filex_secret_key": true,
	// Font families.
	"caveat": true, "dancing": true, "script": true, "homemade": true, "apple": true, "inter": true, "source": true, "serif": true,
	// What the fixtures typed: names, addresses, files, a message.
	"burak": true, "faruk": true, "şahin": true, "gökçe": true, "elden": true, "veren": true, "ornek": true,
	"example": true, "gokce": true, "sözleşme": true, "teklif": true, "baska": true, "kendi": true,
	"docs": true, "docx": true, "https": true, "lütfen": true, "cumaya": true, "kadar": true, "kabul": true,
	"görev": true, "unvanı": true, "tarih": true, "imza": true,
	// The date example a date box shows is a date, in both.
	"yyyy": true,
	// Fixture fingerprints and a fixture's English error text (the words
	// the app itself puts there are checked by "verify-unreadable").
	"aaaa": true, "ffff": true, "broken": true, "yılmaz": true,
	// Turkish words that are also English ones: "size" (to you), "form".
	"size": true, "form": true,
}

// verbatim are names shown as they are, taken out of a string before its
// words are compared.
var verbatim = []string{
	// The host's own signing authority, by its certificate's name.
	"filex signing",
	// A file-name pattern's placeholders.
	"{stem}", "{ext}", "{name}",
	// ⚠ The default name of the signed copy, `{stem}-signed{ext}`, is a FILE
	// name, and changing it for Turkish readers changes what every Turkish
	// installation's signed files are called: that is the maintainer's
	// decision, asked for, not taken here.
	"-signed",
}

// tokens are the lower-case words of s: letters and underscores, four or
// more of them (shorter words collide across the two languages by chance).
func tokens(s string) []string {
	for _, v := range verbatim {
		s = strings.ReplaceAll(s, v, " ")
	}
	var out []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && r != '_' }) {
		w = strings.ToLower(w)
		if len([]rune(w)) >= 4 {
			out = append(out, w)
		}
	}
	return out
}

// everything is every string a surface shows in one language: the Texts in
// that language, and each form field's words — its map when it has one,
// else the one string the app picked.
func everything(s *wire.Surface, lang string) []string {
	var out []string
	walkAll(s, func(txt wire.Text) { out = append(out, txt[lang]) })
	for _, f := range formFields(s) {
		label, help, hint := f.Label, f.Help, f.Placeholder
		if f.I18n != nil {
			if v := f.I18n.Label[lang]; v != "" {
				label = v
			}
			if v := f.I18n.Help[lang]; v != "" {
				help = v
			}
			if v := f.I18n.Placeholder[lang]; v != "" {
				hint = v
			}
		}
		out = append(out, label, help, hint)
		for _, o := range f.Options {
			if v := o.LabelI18n[lang]; v != "" {
				out = append(out, v)
			} else {
				out = append(out, o.Label)
			}
		}
	}
	return out
}

func TestTurkish_EveryFieldTextTravelsInEveryLanguage(t *testing.T) {
	en, tr := turkishScreens(t, EN), turkishScreens(t, TR)
	for name, se := range en {
		st := tr[name]
		fe, ft := formFields(se), formFields(st)
		if len(fe) != len(ft) {
			t.Fatalf("%s: %d fields in English, %d in Turkish", name, len(fe), len(ft))
		}
		for i := range fe {
			a, b := fe[i], ft[i]
			// What differs between the two builds was translated by the app,
			// and must reach a Turkish SCREEN whatever language the app was
			// told: as a map carrying both.
			check := func(what, sEN, sTR string, m func(*wire.FieldI18n) wire.Text) {
				if sEN == sTR {
					return
				}
				var got wire.Text
				if b.I18n != nil {
					got = m(b.I18n)
				}
				if got["tr"] != sTR || got["en"] != sEN {
					t.Errorf("%s: field %q %s is one string in the language the app was told (%q / %q), not a text in every language: %v",
						name, b.Key, what, sEN, sTR, got)
				}
			}
			check("label", a.Label, b.Label, func(x *wire.FieldI18n) wire.Text { return x.Label })
			check("help", a.Help, b.Help, func(x *wire.FieldI18n) wire.Text { return x.Help })
			check("placeholder", a.Placeholder, b.Placeholder, func(x *wire.FieldI18n) wire.Text { return x.Placeholder })
			for j := range a.Options {
				if j >= len(b.Options) || a.Options[j].Label == b.Options[j].Label {
					continue
				}
				if o := b.Options[j]; o.LabelI18n["tr"] != o.Label || o.LabelI18n["en"] != a.Options[j].Label {
					t.Errorf("%s: field %q option %q is one string, not a text in every language: %q / %q",
						name, b.Key, o.Value, a.Options[j].Label, o.Label)
				}
			}
		}
	}
}

func TestTurkish_NoEnglishWordReachesATurkishReader(t *testing.T) {
	en, tr := turkishScreens(t, EN), turkishScreens(t, TR)
	english := map[string]bool{}
	for _, s := range en {
		for _, str := range everything(s, "en") {
			for _, w := range tokens(str) {
				english[w] = true
			}
		}
	}
	leaks := map[string][]string{}
	for name, s := range tr {
		for _, str := range everything(s, "tr") {
			for _, w := range tokens(str) {
				if english[w] && !allowedInTurkish[w] {
					leaks[name] = append(leaks[name], w+" ← "+str)
				}
			}
		}
	}
	names := make([]string, 0, len(leaks))
	for n := range leaks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, l := range leaks[n] {
			t.Errorf("%s: English in Turkish: %s", n, l)
		}
	}
}

// The bytes a partial signature covers, in the order Turkish says them.
func TestTurkish_PartialCoverageSaysTheNumbersTheRightWayRound(t *testing.T) {
	s := Verify(TR, VerifyInput{Doc: doc(), Report: partReport()})
	all := strings.Join(everything(s, "tr"), "\n")
	if !strings.Contains(all, "8000 baytın ilk 5000 baytı") {
		t.Errorf("the Turkish coverage line should read “8000 baytın ilk 5000 baytı”:\n%s", all)
	}
}
