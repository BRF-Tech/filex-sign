package fields

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brf-tech/filex-sign/internal/fontkit"
)

func TestCheck_TextRules(t *testing.T) {
	cases := []struct {
		name  string
		spec  Spec
		value string
		ok    bool
	}{
		{"free text", Spec{Type: TypeText, Rule: RuleFree}, "anything at all", true},
		{"empty optional", Spec{Type: TypeText}, "", true},
		{"empty required", Spec{Type: TypeText, Required: true}, "  ", false},
		{"number", Spec{Type: TypeText, Rule: RuleNumber}, "1.250,45", true},
		{"number with a sign", Spec{Type: TypeText, Rule: RuleNumber}, "-42", true},
		{"not a number", Spec{Type: TypeText, Rule: RuleNumber}, "12a", false},
		{"no digits at all", Spec{Type: TypeText, Rule: RuleNumber}, "-", false},
		{"e-mail", Spec{Type: TypeText, Rule: RuleEmail}, "Ayşe@example.com", true},
		{"e-mail without a domain dot", Spec{Type: TypeText, Rule: RuleEmail}, "a@localhost", false},
		{"e-mail with a space", Spec{Type: TypeText, Rule: RuleEmail}, "a b@x.com", false},
		{"too short", Spec{Type: TypeText, MinLen: 5}, "abcd", false},
		{"long enough in runes, not bytes", Spec{Type: TypeText, MinLen: 5}, "şşşşş", true},
		{"too long", Spec{Type: TypeText, MaxLen: 3}, "abcd", false},
		{"date in the field's own layout", Spec{Type: TypeDate, Format: DateMDY}, "12/31/2000", true},
		{"date in ISO, which is what the component posts", Spec{Type: TypeDate, Format: DateMDY}, "2000-12-31", true},
		{"not a date", Spec{Type: TypeDate}, "yarın", false},
		{"optional date left empty", Spec{Type: TypeDate}, "", true},
		{"required date left empty", Spec{Type: TypeDate, Required: true}, "", false},
		{"checkbox ticked", Spec{Type: TypeCheckbox, Required: true}, "true", true},
		{"checkbox left alone", Spec{Type: TypeCheckbox, Required: true}, "", false},
		{"optional checkbox", Spec{Type: TypeCheckbox}, "", true},
		{"signature required", Spec{Type: TypeSignature, Required: true}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prob := Check(c.spec, c.value)
			if (prob == nil) != c.ok {
				t.Fatalf("Check(%+v, %q) = %v, want ok=%v", c.spec, c.value, prob, c.ok)
			}
			if prob != nil && (prob.EN == "" || prob.TR == "") {
				t.Fatalf("a refusal has to speak both languages: %+v", prob)
			}
		})
	}
}

func TestCheckRefusalNamesTheExpectedShape(t *testing.T) {
	prob := Check(Spec{Type: TypeDate, Format: DateMDY}, "nope")
	// The words are a format and its arguments (the other languages are
	// keyed by the English as written), so the layout is in what they SAY.
	if prob == nil || !strings.Contains(prob.Error(), "12/31/2000") || !strings.Contains(fmt.Sprintf(prob.TR, prob.Args...), "12/31/2000") {
		t.Fatalf("the refusal should show the layout: %+v", prob)
	}
}

func TestDisplay(t *testing.T) {
	if got := Display(Spec{Type: TypeDate, Format: DateDMY}, "2000-12-31"); got != "31.12.2000" {
		t.Errorf("date → %q", got)
	}
	if got := Display(Spec{Type: TypeDate, Format: DateMDY}, "2000-12-31"); got != "12/31/2000" {
		t.Errorf("date → %q", got)
	}
	if got := Display(Spec{Type: TypeText}, "  Şahin  "); got != "Şahin" {
		t.Errorf("text → %q", got)
	}
	if got := Display(Spec{Type: TypeCheckbox}, "true"); got != "" {
		t.Errorf("a checkbox is a tick, not a word: %q", got)
	}
}

func TestParseAndFormatDateRoundTrip(t *testing.T) {
	for _, f := range Formats() {
		want := time.Date(2000, 12, 31, 0, 0, 0, 0, time.UTC)
		s := FormatDate(want, f)
		got, ok := ParseDate(s, f)
		if !ok || !got.Equal(want) {
			t.Errorf("%s: %q → %v (%v)", f, s, got, ok)
		}
		if Example(f) != s {
			t.Errorf("%s: the example %q should be the layout itself (%q)", f, Example(f), s)
		}
	}
}

func TestNormalisers(t *testing.T) {
	if NormalizeRule("nonsense") != RuleFree {
		t.Error("an unknown rule must degrade to free text, not refuse the field")
	}
	if NormalizeFormat("") != DateDMY {
		t.Error("the default layout is day-first")
	}
	if NormalizeType("") != TypeSignature || NormalizeType("text") != TypeText {
		t.Error("type normalisation")
	}
	if NormalizeFont("nope") != fontkit.DefaultID {
		t.Error("an unknown font must fall back, so an old record still stamps")
	}
	if NormalizeFont(fontkit.Caveat) != fontkit.Caveat {
		t.Error("a known font must survive")
	}
	if !Drawn(TypeSignature) || !Drawn(TypeInitials) || Drawn(TypeText) {
		t.Error("Drawn")
	}
	if !Typed(TypeText) || !Typed(TypeDate) || !Typed(TypeCheckbox) || Typed(TypeSignature) {
		t.Error("Typed")
	}
}

func TestIsTicked(t *testing.T) {
	for _, s := range []string{"true", "1", "YES", "evet", "✓"} {
		if !IsTicked(s) {
			t.Errorf("%q should count as ticked", s)
		}
	}
	for _, s := range []string{"", "false", "0", "hayır"} {
		if IsTicked(s) {
			t.Errorf("%q should not count as ticked", s)
		}
	}
}

// A date is a field type, and only a field type: the `date` rule on a
// text box was removed because two ways to ask for the same thing is how
// you get two answers. A box placed by an older build still works — it
// becomes a date box.
func TestDateIsAFieldTypeNotATextRule(t *testing.T) {
	for _, r := range Rules() {
		if r == LegacyRuleDate {
			t.Fatalf("the removed date rule is still offered: %v", Rules())
		}
	}
	if typ, rule := Migrate(TypeText, LegacyRuleDate); typ != TypeDate || rule != "" {
		t.Errorf("an old text+date box should become a date box, got %q/%q", typ, rule)
	}
	if typ, rule := Migrate(TypeText, RuleEmail); typ != TypeText || rule != RuleEmail {
		t.Errorf("an ordinary rule must survive: %q/%q", typ, rule)
	}
	// Normalising the removed rule must not leave it in place.
	if NormalizeRule(LegacyRuleDate) == LegacyRuleDate {
		t.Error("the removed rule normalised to itself")
	}
}
