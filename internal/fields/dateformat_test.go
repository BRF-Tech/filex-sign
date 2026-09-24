package fields

import (
	"testing"
	"time"
)

// A date layout is an ORDER and a SEPARATOR, chosen separately and stored as
// the pattern they make (the owner, 2026-09-23). The three patterns this app
// has always written are members of the new set, so nothing a request was
// made with is re-formatted under a signature somebody already gave.
func TestDateFormat_TwoAnswersMakeOnePattern(t *testing.T) {
	for _, c := range []struct{ order, sep, want string }{
		{OrderDMY4, SepDot, "DD.MM.YYYY"},
		{OrderDMY2, SepSlash, "DD/MM/YY"},
		{OrderMDY4, SepSlash, "MM/DD/YYYY"},
		{OrderMDY2, SepDash, "MM-DD-YY"},
		{OrderYMD, SepDash, "YYYY-MM-DD"},
		{OrderDMY4, SepSpace, "DD MM YYYY"},
		// Anything unknown falls back to the default pair, never to junk.
		{"NOPE", "!", "DD.MM.YYYY"},
	} {
		if got := DateFormat(c.order, c.sep); got != c.want {
			t.Errorf("DateFormat(%q, %q) = %q, want %q", c.order, c.sep, got, c.want)
		}
	}
	// ...and every pattern comes back apart into the two answers that made it.
	for _, f := range Formats() {
		o, s := SplitFormat(f)
		if back := DateFormat(o, s); back != f {
			t.Errorf("SplitFormat(%q) = (%q, %q) → %q", f, o, s, back)
		}
	}
	if n := len(Formats()); n != len(DateOrders())*len(DateSeparators()) {
		t.Errorf("the catalogue is the whole grid: %d patterns", n)
	}
	// The three patterns written before this change are still in the set,
	// which is what keeps an open request's layout its own.
	for _, old := range []string{DateDMY, DateMDY, DateYMD} {
		if NormalizeFormat(old) != old {
			t.Errorf("%q must survive as itself", old)
		}
	}
}

// The layout decides how a date is STAMPED and what a typed entry may look
// like — and a two-digit year is read back as the year it means.
func TestDateFormat_StampsAndParses(t *testing.T) {
	day := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct{ format, want string }{
		{"DD.MM.YYYY", "23.09.2026"},
		{"DD/MM/YY", "23/09/26"},
		{"MM-DD-YYYY", "09-23-2026"},
		{"YYYY MM DD", "2026 09 23"},
	} {
		if got := FormatDate(day, c.format); got != c.want {
			t.Errorf("FormatDate(%q) = %q, want %q", c.format, got, c.want)
		}
		if got := Display(Spec{Type: TypeDate, Format: c.format}, "2026-09-23"); got != c.want {
			t.Errorf("Display(%q) = %q, want %q", c.format, got, c.want)
		}
		// What the browser's date control posts, what the layout asks for,
		// and — on a two-digit box — a full year typed by hand.
		for _, in := range []string{"2026-09-23", c.want} {
			if _, ok := ParseDate(in, c.format); !ok {
				t.Errorf("ParseDate(%q, %q) refused it", in, c.format)
			}
		}
	}
	// A FULL year typed into a two-digit box means the year. The layout the
	// box carries is not among the three this app used to offer, so nothing
	// but the pattern's own full-year sibling can read it (dateLayouts).
	for _, in := range []string{"09-23-2026", "09-23-26"} {
		got, ok := ParseDate(in, "MM-DD-YY")
		if !ok || got.Year() != 2026 || got.Month() != 9 || got.Day() != 23 {
			t.Errorf("ParseDate(%q, MM-DD-YY) = %v, %v", in, got, ok)
		}
	}
	// The example is the layout spelled out, in the layout's own shape.
	if got := Example("MM/DD/YY"); got != "12/31/00" {
		t.Errorf("Example(MM/DD/YY) = %q", got)
	}
}
