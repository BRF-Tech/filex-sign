package i18n

import (
	"strings"
	"testing"
	"time"
)

// A date a person reads is written the way filex writes dates, in each
// language: the app printed "2026-09-29" in its mails, notices and screens
// beside filex's "22 Eyl 2026" (v0.43.0 wave 2). Sf, Tf and the table
// helpers write a Day / When / Span in the reader's own words.
func TestDatesAreWrittenInEachLanguagesWords(t *testing.T) {
	msg := Tf("The link is valid until %s.", "Bağlantı %s tarihine kadar geçerli.", Day("2026-09-29T11:03:30Z"))
	want := map[Lang]string{
		EN: "Sep 29, 2026",
		TR: "29 Eyl 2026",
		DE: "29. Sept. 2026",
		ES: "29 sept 2026",
		FR: "29 sept. 2026",
	}
	for l, day := range want {
		if !strings.Contains(msg[string(l)], day) {
			t.Errorf("%s: %q does not carry %q", l, msg[string(l)], day)
		}
		if strings.Contains(msg[string(l)], "2026-09-29") {
			t.Errorf("%s: %q prints the ISO day", l, msg[string(l)])
		}
	}

	if got := TR.Sf("%s UTC", "%s UTC", When(time.Date(2026, 9, 22, 11, 14, 0, 0, time.UTC))); got != "22 Eyl 2026, 11:14 UTC" {
		t.Errorf("When in Turkish: %q", got)
	}
	span := SpanText(time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC), time.Date(2027, 9, 22, 0, 0, 0, 0, time.UTC))
	if span[string(EN)] != "Sep 22, 2026 - Sep 22, 2027" || span[string(TR)] != "22 Eyl 2026 - 22 Eyl 2027" {
		t.Errorf("Span: %v", span)
	}
	cell := DayText("2026-09-29")
	if cell[string(DE)] != "29. Sept. 2026" {
		t.Errorf("DayText in German: %q", cell[string(DE)])
	}
	// A value that is not a date is left as it is — a dash stays a dash.
	if got := DayText("—")[string(TR)]; got != "—" {
		t.Errorf("a dash became %q", got)
	}
	// Plain arguments are untouched.
	if got := EN.Sf("%s and %d", "%s ve %d", "a", 3); got != "a and 3" {
		t.Errorf("plain args: %q", got)
	}
}
