package fontkit

import "testing"

// A space the face does not carry is drawn with the face's own space — never
// dropped, never borrowed — and copies out as a plain space. See glyph():
// the French audit trail's narrow no-break spaces were dropped until this
// held (2026-09-22).
func TestSpaces_AFaceDrawsEverySpaceWithItsOwn(t *testing.T) {
	for _, f := range append(All(), StampFace()) {
		plain, err := f.Shape(" ")
		if err != nil || len(plain) != 1 {
			t.Fatalf("%s: no space at all (%v)", f.ID, err)
		}
		for _, r := range []rune{' ', ' ', ' ', ' '} {
			g, err := f.Shape(string(r))
			if err != nil {
				t.Fatal(err)
			}
			if len(g) != 1 {
				t.Errorf("%s: U+%04X was dropped", f.ID, r)
				continue
			}
			if g[0].Rune != r {
				t.Errorf("%s: U+%04X came back as U+%04X", f.ID, r, g[0].Rune)
			}
			if g[0].GID == plain[0].GID && g[0].Text != " " {
				t.Errorf("%s: U+%04X shares the space glyph but would copy out as %q — every space of the document would", f.ID, r, g[0].Text)
			}
		}
	}
	// The line layout — what the PDFs draw — keeps that text too: a laid-out
	// French line's narrow no-break space is a glyph, and copies as " ".
	line, err := LayoutLine(Get(Inter), "Pourquoi ?")
	if err != nil {
		t.Fatal(err)
	}
	var drawn, spaces int
	for _, run := range line.Runs {
		for _, g := range run.Glyphs {
			drawn++
			if g.Rune == ' ' {
				spaces++
				if g.Text != " " {
					t.Errorf("the laid-out narrow no-break space copies out as %q", g.Text)
				}
			}
		}
	}
	if drawn != len([]rune("Pourquoi ?")) || spaces != 1 {
		t.Errorf("the laid-out line drew %d glyphs (%d spaces) for %d characters", drawn, spaces, len([]rune("Pourquoi ?")))
	}
	// ...and a letter the face lacks is still missing: only spaces fall back.
	if g, _ := Get(Inter).Shape("中"); len(g) != 0 {
		t.Error("a missing letter must not be drawn as a space")
	}
}
