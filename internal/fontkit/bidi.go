package fontkit

import (
	xbidi "golang.org/x/text/unicode/bidi"
)

// ── Levels: the Unicode bidirectional algorithm for one line ────────────
//
// ⚠⚠ Written here instead of taken from a library, on measurement
// (2026-09-21). go-text's bidi put "42” — filled by " at level 2 inside a
// left-to-right paragraph ("Box “שלום עולם 42” — filled by …"), which printed
// the second number at the first quote; x/text's Ordering put "abc محمد 12
// def" as "abc محمد 12 def" instead of "abc 12 محمد def". Both disagree with
// UAX #9 on text a form field really gets. This is the algorithm's weak,
// neutral and implicit rules (W1–W7, N1–N2, I1–I2) and the line rules (L1,
// L2) over the Unicode Character Database's bidi classes (x/text supplies
// the classes; they are data, not the algorithm).
//
// What it leaves out, deliberately: explicit embeddings and isolates
// (LRE…PDF, LRI…PDI — control characters nobody types into a signature box;
// they are treated as boundary-neutral and ignored) and bracket pairing
// (N0), whose absence only affects which side a bracket's glyph sits on when
// it encloses text of the other direction.

type bidiClass uint8

const (
	cL bidiClass = iota
	cR
	cAL
	cEN
	cES
	cET
	cAN
	cCS
	cNSM
	cBN
	cB
	cS
	cWS
	cON
)

func classOf(r rune) bidiClass {
	p, _ := xbidi.LookupRune(r)
	switch p.Class() {
	case xbidi.L:
		return cL
	case xbidi.R:
		return cR
	case xbidi.AL:
		return cAL
	case xbidi.EN:
		return cEN
	case xbidi.ES:
		return cES
	case xbidi.ET:
		return cET
	case xbidi.AN:
		return cAN
	case xbidi.CS:
		return cCS
	case xbidi.NSM:
		return cNSM
	case xbidi.B:
		return cB
	case xbidi.S:
		return cS
	case xbidi.WS:
		return cWS
	case xbidi.ON:
		return cON
	}
	// BN and the explicit formatting codes.
	return cBN
}

// bidiLevels resolves every character's embedding level for one line and
// reports whether the paragraph reads right to left.
func bidiLevels(runes []rune) ([]int, bool) {
	n := len(runes)
	cls := make([]bidiClass, n)
	for i, r := range runes {
		cls[i] = classOf(r)
	}
	// P2–P3: the first strong character sets the paragraph level.
	para := 0
	for _, c := range cls {
		if c == cL {
			break
		}
		if c == cR || c == cAL {
			para = 1
			break
		}
	}
	sos := cL
	if para == 1 {
		sos = cR
	}
	// X9: boundary neutrals take no part; they get the level of what is
	// before them at the end.
	t := make([]bidiClass, n)
	copy(t, cls)

	// W1: a mark takes the type of the character before it.
	prev := sos
	for i := range t {
		switch t[i] {
		case cBN:
			continue
		case cNSM:
			t[i] = prev
		}
		prev = t[i]
	}
	// W2: a European number after Arabic letters is an Arabic number.
	last := sos
	for i := range t {
		switch t[i] {
		case cL, cR, cAL:
			last = t[i]
		case cEN:
			if last == cAL {
				t[i] = cAN
			}
		}
	}
	// W3: Arabic letters are right-to-left letters from here on.
	for i := range t {
		if t[i] == cAL {
			t[i] = cR
		}
	}
	// W4: a single separator between two numbers of one kind joins them.
	for i := 1; i+1 < n; i++ {
		a, b := t[i-1], t[i+1]
		if t[i] == cES && a == cEN && b == cEN {
			t[i] = cEN
		} else if t[i] == cCS && a == b && (a == cEN || a == cAN) {
			t[i] = a
		}
	}
	// W5: currency and percent signs next to a European number join it.
	for i := 0; i < n; i++ {
		if t[i] != cET {
			continue
		}
		j := i
		for j < n && (t[j] == cET || t[j] == cBN) {
			j++
		}
		if (i > 0 && t[i-1] == cEN) || (j < n && t[j] == cEN) {
			for k := i; k < j; k++ {
				if t[k] == cET {
					t[k] = cEN
				}
			}
		}
		i = j - 1
	}
	// W6: the separators and terminators left over are neutral.
	for i := range t {
		switch t[i] {
		case cES, cET, cCS:
			t[i] = cON
		}
	}
	// W7: a European number after left-to-right text is left to right.
	last = sos
	for i := range t {
		switch t[i] {
		case cL, cR:
			last = t[i]
		case cEN:
			if last == cL {
				t[i] = cL
			}
		}
	}
	// N1–N2: a run of neutrals between two of one direction takes it
	// (numbers count as right to left); otherwise the paragraph's.
	strong := func(c bidiClass) (bidiClass, bool) {
		switch c {
		case cL:
			return cL, true
		case cR, cEN, cAN:
			return cR, true
		}
		return 0, false
	}
	eos := sos
	for i := 0; i < n; i++ {
		switch t[i] {
		case cB, cS, cWS, cON, cBN:
		default:
			continue
		}
		j := i
		for j < n && (t[j] == cB || t[j] == cS || t[j] == cWS || t[j] == cON || t[j] == cBN) {
			j++
		}
		before := sos
		if i > 0 {
			if d, ok := strong(t[i-1]); ok {
				before = d
			}
		}
		after := eos
		if j < n {
			if d, ok := strong(t[j]); ok {
				after = d
			}
		}
		dir := sos // the embedding direction
		if before == after {
			dir = before
		}
		for k := i; k < j; k++ {
			t[k] = dir
		}
		i = j - 1
	}
	// I1–I2: levels.
	levels := make([]int, n)
	for i, c := range t {
		lv := para
		if para%2 == 0 {
			switch c {
			case cR:
				lv = para + 1
			case cAN, cEN:
				lv = para + 2
			}
		} else {
			switch c {
			case cL, cEN, cAN:
				lv = para + 1
			}
		}
		levels[i] = lv
	}
	// L1: whitespace at the end of the line goes back to the paragraph
	// level (so a trailing space never floats into the middle).
	for i := n - 1; i >= 0; i-- {
		c := cls[i]
		if c != cWS && c != cBN && c != cS && c != cB {
			break
		}
		levels[i] = para
	}
	return levels, para == 1
}
