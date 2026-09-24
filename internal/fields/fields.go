// Package fields is the rule side of a fillable field: which kinds of
// box a document may carry, what a typed value has to look like, and how
// the value reads once it is stamped.
//
// Everything here is pure and bilingual: the same check runs in the
// screen the signer fills (so the refusal is immediate) and again in the
// job that stamps the document (so a crafted submission cannot slip a
// value past the screen).
package fields

import (
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex-sign/internal/fontkit"
)

// Field types. Signature and initials are drawn by the signature pad;
// the other three are typed, ticked or dated and stamped as text.
const (
	TypeSignature = "signature"
	TypeInitials  = "initials"
	TypeText      = "text"
	TypeDate      = "date"
	TypeCheckbox  = "checkbox"
)

// Rules a text field may carry. A date is a FIELD TYPE, never a text
// rule: two ways to ask for the same thing is how you get two answers
// (v3 §3.3). LegacyRuleDate is only recognised when an older record is
// read back, and Migrate turns such a field into a date field.
const (
	RuleFree   = "free"
	RuleNumber = "number"
	RuleEmail  = "email"

	// LegacyRuleDate is the rule schema 2 wrote on a text field.
	LegacyRuleDate = "date"
)

// Date layouts a date field may be written in. The value on the wire is
// always ISO (the `pdf-fields` component's own shape); the layout only
// decides how it is stamped and what a typed entry may look like.
//
// ⚠ These three are not the whole set any more — a layout is an ORDER and
// a SEPARATOR chosen separately (dateformat.go) and the pattern they make
// is what is stored. They are kept by name because they are what every
// request written before that carries, and because they are the sensible
// defaults.
const (
	DateDMY = "DD.MM.YYYY"
	DateMDY = "MM/DD/YYYY"
	DateYMD = "YYYY-MM-DD"
)

// ISO is the shape a date value travels in.
const ISO = "2006-01-02"

// Spec is one field's rules — the part of a stored field that decides
// whether a value is acceptable and how it is drawn.
type Spec struct {
	Type     string
	Rule     string
	Format   string
	MinLen   int
	MaxLen   int
	Required bool
	Font     string
}

// Problem is a refusal: an English–Turkish pair of format strings and
// their arguments. The other languages come from internal/i18n, keyed by
// the English AS WRITTEN — which is why the arguments travel beside it
// instead of being formatted in (a formatted sentence is no key).
type Problem struct {
	EN, TR string
	Args   []any
	// Name is the box it is about ("" when the words say it already).
	Name string
}

func (p *Problem) Error() string { return fmt.Sprintf(p.EN, p.Args...) }

func problem(en, tr string, args ...any) *Problem { return &Problem{EN: en, TR: tr, Args: args} }

// Types lists the field kinds a document may carry, in the order the
// editor offers them.
func Types() []string {
	return []string{TypeSignature, TypeInitials, TypeText, TypeDate, TypeCheckbox}
}

// ValidType reports whether t names a field kind.
func ValidType(t string) bool {
	for _, k := range Types() {
		if k == t {
			return true
		}
	}
	return false
}

// NormalizeType falls back to a signature box, the only kind that means
// something on its own.
func NormalizeType(t string) string {
	if ValidType(t) {
		return t
	}
	return TypeSignature
}

// Rules lists the text rules, in the order the editor offers them.
func Rules() []string { return []string{RuleFree, RuleNumber, RuleEmail} }

// Migrate upgrades a field placed by an older build: a text box that
// carried the removed `date` rule becomes a date box, which is the one
// way to ask for a date now. It returns the type and rule to store.
func Migrate(typ, rule string) (string, string) {
	if typ == TypeText && rule == LegacyRuleDate {
		return TypeDate, ""
	}
	if rule == LegacyRuleDate {
		return typ, ""
	}
	return typ, rule
}

// NormalizeRule keeps a rule the plugin understands; anything else is
// treated as free text rather than refused, so a field placed by a newer
// build still fills on an older one.
func NormalizeRule(r string) string {
	for _, k := range Rules() {
		if k == r {
			return k
		}
	}
	return RuleFree
}

// Formats lists every date layout: each arrangement with each separator,
// arrangement-first, so the editor's two rows of buttons come out of one
// catalogue in the order it offers them.
func Formats() []string {
	out := make([]string, 0, len(DateOrders())*len(DateSeparators()))
	for _, o := range DateOrders() {
		for _, s := range DateSeparators() {
			out = append(out, DateFormat(o, s))
		}
	}
	return out
}

// NormalizeFormat defaults to day-first with a dot, which is what a
// Turkish form expects. ⚠ A pattern it does not know is not refused — it
// is taken apart and put back together (SplitFormat), so a layout written
// by a newer build still stamps something sensible on an older one.
func NormalizeFormat(f string) string {
	for _, k := range Formats() {
		if k == f {
			return k
		}
	}
	return DateFormat(SplitFormat(f))
}

// NormalizeFont keeps a font this build embeds.
func NormalizeFont(id string) string {
	if fontkit.Valid(id) {
		return fontkit.Canonical(id)
	}
	return fontkit.DefaultID
}

// Typed reports whether the field's value is typed text rather than a
// drawing — the fields this plugin stamps itself.
func Typed(t string) bool {
	return t == TypeText || t == TypeDate || t == TypeCheckbox
}

// Drawn reports whether the field holds a signature image.
func Drawn(t string) bool { return t == TypeSignature || t == TypeInitials }

// Check validates a value against a field's rules. An empty value is a
// problem only when the field is required; the caller decides whether an
// empty optional field is worth stamping (it is not).
func Check(s Spec, value string) *Problem {
	v := strings.TrimSpace(value)
	switch s.Type {
	case TypeCheckbox:
		if s.Required && !IsTicked(v) {
			return problem("This box has to be ticked.", "Bu kutunun işaretlenmesi gerekiyor.")
		}
		return nil
	case TypeSignature, TypeInitials:
		if s.Required && v == "" {
			return problem("A signature is required here.", "Burada imza gerekiyor.")
		}
		return nil
	case TypeDate:
		if v == "" {
			if s.Required {
				return problem("This date is required.", "Bu tarih gerekli.")
			}
			return nil
		}
		if _, ok := ParseDate(v, s.Format); !ok {
			return problem("Write the date as %s.", "Tarihi %s biçiminde yazın.", Example(s.Format))
		}
		return nil
	}

	if v == "" {
		if s.Required {
			return problem("This field is required.", "Bu alan zorunlu.")
		}
		return nil
	}
	if n := len([]rune(v)); s.MinLen > 0 && n < s.MinLen {
		return problem("Write at least %d characters.", "En az %d karakter yazın.", s.MinLen)
	} else if s.MaxLen > 0 && n > s.MaxLen {
		return problem("Write at most %d characters.", "En fazla %d karakter yazın.", s.MaxLen)
	}
	switch NormalizeRule(s.Rule) {
	case RuleNumber:
		if !isNumber(v) {
			return problem("Only numbers here.", "Buraya yalnız sayı yazılır.")
		}
	case RuleEmail:
		if !isEmail(v) {
			return problem("Write an e-mail address.", "Bir e-posta adresi yazın.")
		}
	}
	return nil
}

// Display is what gets stamped into the document for a value: a date in
// the field's own layout, the value itself otherwise.
func Display(s Spec, value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	if s.Type == TypeCheckbox {
		return ""
	}
	if s.Type == TypeDate {
		if t, ok := ParseDate(v, s.Format); ok {
			return FormatDate(t, s.Format)
		}
	}
	return v
}

// IsTicked reads a checkbox value; the component posts `true`, and a
// stored value may be any of the obvious spellings.
func IsTicked(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on", "evet", "x", "✓":
		return true
	}
	return false
}

// ParseDate accepts the ISO shape the component posts and the layout the
// field asks for, so a typed entry is understood either way.
func ParseDate(v string, format string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	for _, layout := range dateLayouts(NormalizeFormat(format)) {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// FormatDate writes a date in the box's own layout.
func FormatDate(t time.Time, format string) string {
	return t.Format(goLayout(NormalizeFormat(format)))
}

// exampleDay is the date a layout is spelled out with: the last day of a
// year, so the day and the month can never be read for one another.
var exampleDay = time.Date(2000, 12, 31, 0, 0, 0, 0, time.UTC)

// Example is the layout spelled out for a helper line.
func Example(format string) string { return FormatDate(exampleDay, format) }

func isNumber(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if v[0] == '+' || v[0] == '-' {
		v = v[1:]
	}
	digits, seps := 0, 0
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.' || r == ',' || r == ' ':
			seps++
			if seps > 4 {
				return false
			}
		default:
			return false
		}
	}
	return digits > 0
}

func isEmail(v string) bool {
	v = strings.TrimSpace(v)
	at := strings.LastIndex(v, "@")
	if at < 1 || at == len(v)-1 || strings.ContainsAny(v, " \t\r\n") {
		return false
	}
	domain := v[at+1:]
	dot := strings.LastIndex(domain, ".")
	return dot > 0 && dot < len(domain)-1
}
