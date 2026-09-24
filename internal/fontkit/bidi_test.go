package fontkit

import (
	"fmt"
	"testing"
)

// UAX #9 levels for the lines a form really gets — the cases two libraries
// got wrong (bidi.go): a number after Hebrew inside a left-to-right line, a
// number after Arabic before Latin text, an Arabic paragraph with Latin and
// digits in it.
func TestBidi_Levels(t *testing.T) {
	cases := []struct {
		s    string
		rtl  bool
		want string // one digit per character
	}{
		{"Box “שלום עולם 42” — filled by שלום עולם 42", false, "0000011111111112200000000000000111111111122"},
		{"abc محمد 12 def", false, "000011111220000"},
		{"محمد عبد الله (Ali) 2026", true, "111111111111111222222222"},
		{"שלום עולם 42", true, "111111111122"},
		{"Ayşe Yılmaz", false, "00000000000"},
	}
	for _, c := range cases {
		lv, rtl := bidiLevels([]rune(c.s))
		got := ""
		for _, l := range lv {
			got += fmt.Sprint(l)
		}
		if got != c.want || rtl != c.rtl {
			t.Errorf("%q: levels %s rtl %v, want %s rtl %v", c.s, got, rtl, c.want, c.rtl)
		}
	}
}
