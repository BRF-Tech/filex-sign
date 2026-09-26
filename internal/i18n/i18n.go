// Package i18n is where every word the signing app shows, prints or mails
// gets its language.
//
// ⚠⚠ ONE rule for every text in this module: it is written at the call
// site as an English–Turkish pair — `T("Back", "Geri")`, `l.S(…)`,
// `l.Sf(…)` — and every other language comes from the catalogue in
// `catalogue/<lang>.json`, keyed by the English exactly as written
// (format verbs included). English and Turkish stay beside the code
// because that is where the author reads them; the other languages are
// data a translator edits without touching Go.
//
// The owner asked for es, de and fr on 2026-09-22 ("translate every text
// the signing app carries"). What keeps that true after the next string
// is added is `catalogue_test.go`: it reads the module's source, collects
// the English of every text call, and fails when a language lacks one,
// carries one nobody uses, drops or reorders a format verb, or is empty —
// and when a text is built some other way (a hand-made `wire.Text` map, a
// `"tr"` branch) that no catalogue could ever reach.
//
// ⚠ A wire.Text must carry EVERY declared language: filex refuses to
// install an app whose describe answer has a Text missing one. T, Tf,
// Plain and Each are the only ways this module builds one.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/humandate"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// Lang is the language one call is answered in.
type Lang string

// The languages the manifest declares, in its order.
const (
	EN Lang = "en"
	TR Lang = "tr"
	ES Lang = "es"
	DE Lang = "de"
	FR Lang = "fr"
)

// all is every declared language; catalogued are the ones read from
// catalogue/*.json (English and Turkish are written in the code).
var (
	all        = []Lang{EN, TR, ES, DE, FR}
	catalogued = []Lang{ES, DE, FR}
)

// Languages is what filex-app.json's `languages` must say; a test keeps
// the two in step.
func Languages() []string {
	out := make([]string, len(all))
	for i, l := range all {
		out[i] = string(l)
	}
	return out
}

// Catalogued are the languages whose words live in catalogue/*.json.
func Catalogued() []Lang { return append([]Lang(nil), catalogued...) }

// Of reads a call's locale ("de", "de-AT", "fr_CA", "TR"). A language
// this app does not speak is English, which is also what a Text falls
// back to.
//
// ⚠ Until filex passes the viewer's own language to apps (the host
// collapsed every non-Turkish locale to "en" before v0.43 — feat/043-srvtext
// fixes that), es/de/fr only reach this function from tests and from a
// request's recorded locale.
func Of(locale string) Lang {
	s := strings.ToLower(strings.TrimSpace(locale))
	for _, l := range all {
		if s == string(l) || strings.HasPrefix(s, string(l)+"-") || strings.HasPrefix(s, string(l)+"_") {
			return l
		}
	}
	return EN
}

// S picks the language's words for an English–Turkish pair.
func (l Lang) S(en, tr string) string {
	switch l {
	case EN:
		return en
	case TR:
		return tr
	}
	if s, ok := catalogue[l][en]; ok && s != "" {
		return s
	}
	// Unreachable while the catalogue test passes; English is the one
	// language every Text is guaranteed to read in.
	return en
}

// Sf formats the language's own sentence. A Day, When or Span among the
// arguments is written the way that language writes dates.
func (l Lang) Sf(en, tr string, args ...any) string {
	return fmt.Sprintf(l.S(en, tr), l.dated(args)...)
}

// ── dates ──────────────────────────────────────────────────────────────
//
// ⚠⚠ A date a person reads is written the way filex writes dates — the
// explorer's "22 Eyl 2026" / "Sep 22, 2026" — through the SDK's one
// formatter (pluginkit/humandate), never with a month table of this app's
// own and never as the ISO day. The app wrote "2026-09-29" in its mails,
// notices and screens a column away from filex's "22 Eyl 2026" (v0.43.0
// wave 2). Pass a Day / When / Span to Sf, Tf or Each's l.Sf and every
// language writes its own; `DayText` is the same for a table cell. The audit
// trail and a form field's value stay ISO: those must be exact.

// dateArg is a value that is written differently in each language.
type dateArg interface{ inLang(l Lang) string }

func (l Lang) dated(args []any) []any {
	var out []any
	for i, a := range args {
		var said string
		switch x := a.(type) {
		case dateArg:
			said = x.inLang(l)
		case wire.Text:
			// Words already in every language (a reason, an error named
			// for a person): each language takes its own. Spliced in as
			// one string they were the CALL's language in all five — an
			// English reason inside a Turkish sentence (2026-09-26).
			said = In(x, l)
		default:
			continue
		}
		if out == nil {
			out = append([]any(nil), args...)
		}
		out[i] = said
	}
	if out == nil {
		return args
	}
	return out
}

// Day is a stored stamp (RFC 3339, or a YYYY-MM-DD day) printed as the
// day it names: "29 Eyl 2026". Anything else prints as it is ("—").
type Day string

func (d Day) inLang(l Lang) string { return humandate.Stamp(string(l), string(d)) }

// When is an instant printed with its UTC time: "22 Eyl 2026, 11:14" — the
// sentence says "UTC" beside it.
type When time.Time

func (w When) inLang(l Lang) string { return humandate.DayTime(string(l), time.Time(w)) }

// Span is a validity window, "22 Eyl 2026 – 22 Eyl 2027" (UTC days).
type Span struct{ From, To time.Time }

func (s Span) inLang(l Lang) string {
	return humandate.Day(string(l), s.From) + " – " + humandate.Day(string(l), s.To)
}

// DayText is a stamp's day as a table cell, in every language.
func DayText(stamp string) wire.Text {
	return Each(func(l Lang) string { return Day(stamp).inLang(l) })
}

// SpanText is a validity window as a table cell, in every language.
func SpanText(from, to time.Time) wire.Text {
	return Each(func(l Lang) string { return Span{from, to}.inLang(l) })
}

// Sc is S for an English word that means two things: "Signed" is a
// signer's state, a column of counts and the label of a time, and each is
// a different word in German. The catalogue key is `ctx | en`; the
// English on screen is unchanged.
func (l Lang) Sc(ctx, en, tr string) string {
	switch l {
	case EN:
		return en
	case TR:
		return tr
	}
	if s, ok := catalogue[l][Key(ctx, en)]; ok && s != "" {
		return s
	}
	return en
}

// Key is the catalogue key of a word said in a context.
func Key(ctx, en string) string { return ctx + " | " + en }

// Tc is T for a word said in a context (see Sc).
func Tc(ctx, en, tr string) wire.Text {
	t := make(wire.Text, len(all))
	for _, l := range all {
		t[string(l)] = l.Sc(ctx, en, tr)
	}
	return t
}

// T is a label in every language.
func T(en, tr string) wire.Text {
	t := make(wire.Text, len(all))
	for _, l := range all {
		t[string(l)] = l.S(en, tr)
	}
	return t
}

// Tf formats every language with the same arguments. A translation that
// needs them in another order says so with explicit indexes (%[2]s).
func Tf(en, tr string, args ...any) wire.Text {
	t := make(wire.Text, len(all))
	for _, l := range all {
		t[string(l)] = l.Sf(en, tr, args...)
	}
	return t
}

// Plain is the same words in every language — a name, a file, a date, an
// error the host wrote. It still carries every key, because a Text
// missing one is what the install check refuses.
func Plain(s string) wire.Text {
	t := make(wire.Text, len(all))
	for _, l := range all {
		t[string(l)] = s
	}
	return t
}

// Each builds a Text by asking f once per language — for words composed
// of other words (a sentence that names a status, a list joined with
// " · ").
func Each(f func(l Lang) string) wire.Text {
	t := make(wire.Text, len(all))
	for _, l := range all {
		t[string(l)] = f(l)
	}
	return t
}

// In reads a Text in one language (English when it lacks it).
func In(t wire.Text, l Lang) string {
	if s, ok := t[string(l)]; ok {
		return s
	}
	return t[string(EN)]
}

//go:embed catalogue/*.json
var files embed.FS

// catalogue is language → English → words.
var catalogue = load()

func load() map[Lang]map[string]string {
	out := map[Lang]map[string]string{}
	for _, l := range catalogued {
		raw, err := files.ReadFile("catalogue/" + string(l) + ".json")
		if err != nil {
			panic(fmt.Sprintf("i18n: the %s catalogue is missing: %v", l, err))
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			panic(fmt.Sprintf("i18n: the %s catalogue is not valid JSON: %v", l, err))
		}
		out[l] = m
	}
	return out
}

// Entries is one language's catalogue, for the tests.
func Entries(l Lang) map[string]string { return catalogue[l] }
