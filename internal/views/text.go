// Package views builds the screens: the requester's wizard, the signer's
// three steps, the details-panel section, the verification report, the
// home screen, and what an outside signer sees on their own link. Every
// builder is a pure function of data so the tests can assert on the
// surface a given person gets — in either language.
//
// ⚠ Two kinds of string cross this boundary and they are NOT the same.
//
//	wire.Text ({en, tr, es, de, fr}) — the host picks the language. Every
//	  Text this package builds carries EVERY declared language, always: the
//	  host refuses to install a plugin whose describe answer has a Text
//	  missing one, and a screen half in one language is a bug the author
//	  should see before a person does. They are built by T, Tf, Plain and
//	  Each (internal/i18n) and nothing else.
//	plain string — a form field's label, help and placeholder, and a
//	  select option's label, are plain strings on the wire, so the PLUGIN
//	  picks the language, from the locale of the call. That is what Lang
//	  is for: there is no such thing here as a label written straight into
//	  a surface.
package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/i18n"
)

// Lang is the language one call is answered in (internal/i18n).
type Lang = i18n.Lang

// The languages the manifest declares.
const (
	EN = i18n.EN
	TR = i18n.TR
	ES = i18n.ES
	DE = i18n.DE
	FR = i18n.FR
)

// Languages is what filex-app.json's `languages` must say; a test keeps
// the two in step.
func Languages() []string { return i18n.Languages() }

// Of reads the call's locale; a language this app does not speak is
// English.
func Of(locale string) Lang { return i18n.Of(locale) }

// T is a label in every language: English and Turkish here, the others
// from the catalogue.
func T(en, tr string) wire.Text { return i18n.T(en, tr) }

// Tf formats every language with the same arguments.
func Tf(en, tr string, args ...any) wire.Text { return i18n.Tf(en, tr, args...) }

// Plain is the same words in every language — a name, a file, a date.
func Plain(s string) wire.Text { return i18n.Plain(s) }

// Each builds a Text by asking f once per language.
func Each(f func(l Lang) string) wire.Text { return i18n.Each(f) }

// In reads a Text in one language.
func In(t wire.Text, l Lang) string { return i18n.In(t, l) }

// Dates for people, in each language's own words (i18n: Day, When, Span).
type (
	Day  = i18n.Day
	When = i18n.When
	Span = i18n.Span
)

// DayText is a stamp's day as a table cell; SpanText a validity window.
func DayText(stamp string) wire.Text        { return i18n.DayText(stamp) }
func SpanText(from, to time.Time) wire.Text { return i18n.SpanText(from, to) }

// Tc is a word said in a context (i18n.Tc): the same English, a different
// word in another language.
func Tc(ctx, en, tr string) wire.Text { return i18n.Tc(ctx, en, tr) }

// Problem is a field's complaint as words.
func Problem(p *fields.Problem) wire.Text {
	return Each(func(l Lang) string { return ProblemIn(l, p) })
}

// ProblemIn is a field's complaint in one language, the box's name first
// when it has one.
func ProblemIn(l Lang, p *fields.Problem) string {
	if p == nil {
		return ""
	}
	s := l.Sf(p.EN, p.TR, p.Args...)
	if p.Name != "" {
		return l.Sf("%s: %s", "%s: %s", p.Name, s)
	}
	return s
}

// ── nodes ──────────────────────────────────────────────────────────────

func text(t wire.Text) wire.Node {
	return wire.Node{Type: "text", Props: map[string]any{"text": t}}
}

func heading(t wire.Text) wire.Node {
	return wire.Node{Type: "text", Props: map[string]any{"text": t, "heading": true}}
}

func muted(t wire.Text) wire.Node {
	return wire.Node{Type: "text", Props: map[string]any{"text": t, "tone": "muted"}}
}

func danger(t wire.Text) wire.Node {
	return wire.Node{Type: "text", Props: map[string]any{"text": t, "tone": "danger"}}
}

func info(t wire.Text) wire.Node {
	return wire.Node{Type: "text", Props: map[string]any{"text": t, "tone": "info"}}
}

func divider() wire.Node { return wire.Node{Type: "divider"} }

func steps(items ...stepItem) wire.Node {
	var rows []map[string]any
	for _, it := range items {
		rows = append(rows, map[string]any{"id": it.ID, "label": it.Label, "state": it.State})
	}
	return wire.Node{Type: "steps", Props: map[string]any{"items": rows}}
}

type stepItem struct {
	ID    string
	Label wire.Text
	State string
}

func stepStates(current int, labels ...wire.Text) []stepItem {
	var out []stepItem
	for i, l := range labels {
		st := "todo"
		if i+1 < current {
			st = "done"
		} else if i+1 == current {
			st = "active"
		}
		out = append(out, stepItem{ID: fmt.Sprintf("step%d", i+1), Label: l, State: st})
	}
	return out
}

func form(fields []wire.Field, values map[string]any) wire.Node {
	p := map[string]any{"fields": fields}
	if values != nil {
		p["values"] = values
	}
	return wire.Node{Type: "form", Props: p}
}

type column struct {
	Key   string
	Label wire.Text
	// Format says what the cells are ("date": YYYY-MM-DD), so filex prints
	// them the way it prints every date — the reader's language and clock —
	// and sorts by the value. Empty: shown as sent.
	Format string
}

type row struct {
	ID      string
	Cells   map[string]wire.Text
	Actions []rowAction
}

type rowAction struct {
	ID     string
	Label  wire.Text
	Danger bool
}

func list(cols []column, rows []row, empty wire.Text) wire.Node {
	var c []map[string]any
	for _, x := range cols {
		m := map[string]any{"key": x.Key, "label": x.Label}
		if x.Format != "" {
			m["format"] = x.Format
		}
		c = append(c, m)
	}
	var r []map[string]any
	for _, x := range rows {
		m := map[string]any{"id": x.ID, "cells": x.Cells}
		if len(x.Actions) > 0 {
			var acts []map[string]any
			for _, a := range x.Actions {
				am := map[string]any{"id": a.ID, "label": a.Label}
				if a.Danger {
					am["danger"] = true
				}
				acts = append(acts, am)
			}
			m["actions"] = acts
		}
		r = append(r, m)
	}
	if r == nil {
		r = []map[string]any{}
	}
	return wire.Node{Type: "list", Props: map[string]any{"columns": c, "rows": r, "empty": empty}}
}

func button(id string, label wire.Text) wire.SurfaceAction {
	return wire.SurfaceAction{ID: id, Label: label}
}

func primary(id string, label wire.Text) wire.SurfaceAction {
	return wire.SurfaceAction{ID: id, Label: label, Primary: true}
}

func dangerButton(id string, label wire.Text) wire.SurfaceAction {
	return wire.SurfaceAction{ID: id, Label: label, Danger: true}
}

// backButton is the one button a step carries beside its single primary:
// "one step asks one thing, with at most one primary button plus Back".
func backButton() wire.SurfaceAction { return button("back", T("Back", "Geri")) }

func nextButton() wire.SurfaceAction { return primary("next", T("Next", "İleri")) }

// ── form fields ────────────────────────────────────────────────────────
//
// There is no `advanced` here and there never will be: a field is on the
// step or it is not in the manifest. A `select` is drawn as a row of
// choice buttons — never a dropdown — so the options stay few enough to
// read at a glance.

// ⚠⚠ Every text a field shows travels in EVERY language (wire.Field.I18n,
// wire.FieldOption.LabelI18n), beside the one string in the call's language.
//
// filex's renderer reads the map in the language ON SCREEN
// (packages/core surfaceValues.storageFieldOf → labelOf); the string is only
// the language the host TOLD the app. The two differ in an embedded explorer
// drawing Turkish over an account whose language is English: the request
// wizard's Turkish popup asked "Identity", with "One signer per line…" under
// it (2026-09-26). With the map the screen's own language wins whatever the
// host said; the string stays for a client that reads plain strings.
func fieldIn(f wire.Field, label wire.Text) wire.Field {
	if f.I18n == nil {
		f.I18n = &wire.FieldI18n{}
	} else {
		copied := *f.I18n
		f.I18n = &copied
	}
	f.I18n.Label = label
	return f
}

func strField(l Lang, key, en, tr string) wire.Field {
	return fieldIn(wire.Field{Key: key, Type: "string", Label: l.S(en, tr)}, T(en, tr))
}

func longField(l Lang, key, en, tr string) wire.Field {
	return fieldIn(wire.Field{Key: key, Type: "text", Label: l.S(en, tr)}, T(en, tr))
}

func intField(l Lang, key, en, tr string, def, lo, hi int) wire.Field {
	return fieldIn(wire.Field{Key: key, Type: "int", Label: l.S(en, tr), Default: def, Min: &lo, Max: &hi}, T(en, tr))
}

func boolField(l Lang, key, en, tr string, def bool) wire.Field {
	return fieldIn(wire.Field{Key: key, Type: "bool", Label: l.S(en, tr), Default: def}, T(en, tr))
}

func choice(l Lang, key, en, tr, def string, opts ...wire.FieldOption) wire.Field {
	return fieldIn(wire.Field{Key: key, Type: "select", Label: l.S(en, tr), Options: opts, Default: def}, T(en, tr))
}

func opt(l Lang, value, en, tr string) wire.FieldOption {
	return wire.FieldOption{Value: value, Label: l.S(en, tr), LabelI18n: T(en, tr)}
}

// optIn is an option whose label is already words in every language.
func optIn(l Lang, value string, label wire.Text) wire.FieldOption {
	return wire.FieldOption{Value: value, Label: In(label, l), LabelI18n: label}
}

func withHelp(f wire.Field, l Lang, en, tr string) wire.Field {
	f.Help = l.S(en, tr)
	return withHelpText(f, T(en, tr))
}

// withHelpf is withHelp for a sentence with values in it.
func withHelpf(f wire.Field, l Lang, en, tr string, args ...any) wire.Field {
	f.Help = l.Sf(en, tr, args...)
	return withHelpText(f, Tf(en, tr, args...))
}

func withHelpText(f wire.Field, help wire.Text) wire.Field {
	if f.I18n == nil {
		f.I18n = &wire.FieldI18n{}
	} else {
		copied := *f.I18n
		f.I18n = &copied
	}
	f.I18n.Help = help
	return f
}

// withHintIn is a placeholder in every language (withHint: one that is
// not ours to translate).
func withHintIn(f wire.Field, l Lang, en, tr string) wire.Field {
	f.Placeholder = l.S(en, tr)
	if f.I18n == nil {
		f.I18n = &wire.FieldI18n{}
	} else {
		copied := *f.I18n
		f.I18n = &copied
	}
	f.I18n.Placeholder = T(en, tr)
	return f
}

// labelledf is a field whose label is our sentence around somebody's name.
func labelledf(l Lang, key, typ, en, tr string, args ...any) wire.Field {
	return fieldIn(wire.Field{Key: key, Type: typ, Label: l.Sf(en, tr, args...)}, Tf(en, tr, args...))
}

// labelled is a field whose label is not ours to translate — the name a
// requester gave a box — or is already in the call's language.
func labelled(key, typ, label string) wire.Field {
	return wire.Field{Key: key, Type: typ, Label: label}
}

func withHint(f wire.Field, hint string) wire.Field {
	f.Placeholder = hint
	return f
}

func required(f wire.Field) wire.Field {
	f.Required = true
	return f
}

// onlyWhen hides a field until another field of the same form holds one
// of these values, AND makes it required exactly then. This is the
// answer to the contradiction Burak found: a file name asked for while
// the result is a new version of the same file.
func onlyWhen(f wire.Field, key string, equals ...string) wire.Field {
	f.ShowWhen = &wire.Condition{Key: key, Equals: equals}
	f.RequiredWhen = &wire.Condition{Key: key, Equals: equals}
	return f
}

// shownWhen hides a field without making it required.
func shownWhen(f wire.Field, key string, equals ...string) wire.Field {
	f.ShowWhen = &wire.Condition{Key: key, Equals: equals}
	return f
}

// joinWords is the one-line "facts" row every summary uses.
func joinWords(parts []wire.Text) wire.Text {
	return Each(func(l Lang) string {
		var out []string
		for _, p := range parts {
			if p != nil {
				out = append(out, In(p, l))
			}
		}
		return strings.Join(out, " · ")
	})
}
