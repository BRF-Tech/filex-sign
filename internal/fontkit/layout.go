package fontkit

import (
	"bytes"
	"errors"
	"sort"

	"github.com/go-text/typesetting/di"
	gtfont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"
)

// ── One line of text, laid out for printing ────────────────────────────
//
// What a PDF needs to print a line in any script, in the order it is done:
//
//  1. DIRECTION. The Unicode bidirectional algorithm (UAX #9) gives every
//     character a level: an Arabic or Hebrew phrase runs right to left, a
//     number inside it left to right, and the paragraph takes the direction
//     of its first strong letter.
//  2. SCRIPT. Every character belongs to a script; punctuation and digits
//     (Common) and combining marks (Inherited) take the script around them.
//  3. FACE. The chosen bundled face draws what it can (Latin, Turkish); every
//     other character is drawn by the Noto face for its script, fetched the
//     first time it is needed (noto.go).
//  4. SHAPING. A run in a Noto face goes through HarfBuzz (go-text's port):
//     Arabic letters take their joined forms, Indic conjuncts form, marks
//     sit on their bases. The very large CJK faces are mapped character by
//     character — ideographs and kana need no shaping in a horizontal line.
//  5. ORDER. The runs are put in visual order (UAX #9 rule L2), and a right-
//     to-left run's glyphs come out of the shaper already right to left.
//
// ⚠⚠ Nothing is dropped silently: a character no face can draw is reported
// in Line.Missing with the reason, and the screens say so while the person
// types (the owner, 2026-09-21: "never print empty boxes silently").

// Line is one laid-out line: its runs in visual (left-to-right drawing)
// order, whether the paragraph reads right to left, and what could not be
// drawn.
type Line struct {
	Runs    []Run
	RTL     bool
	Missing []Missing
}

// Width is the line's advance at size points.
func (l Line) Width(size float64) float64 { return RunsWidth(l.Runs, size) }

// Why a character cannot be drawn.
const (
	// NoFont: no Noto face draws this script at all.
	NoFont = "no_font"
	// Unavailable: a face exists but could not be fetched (offline, the
	// server refused, the bytes did not match their pinned hash).
	Unavailable = "unavailable"
	// Downloading: the face is on its way — the host is still downloading
	// it and this call could not wait longer. A later call will have it.
	Downloading = "downloading"
	// NoGlyph: the face for the script has no glyph for this character.
	NoGlyph = "no_glyph"
)

// Missing is one character that will not be printed, and why. At is its
// index among the line's runes, so a screen can show the line exactly as
// it will be printed (Printed).
type Missing struct {
	Rune   rune
	Script string
	Reason string
	At     int
}

// item is a stretch of the line in one face, direction and script.
type item struct {
	start, end int
	level      int
	face       *Face
	script     string
}

// LayoutLine lays s out in primary (the face the person chose), borrowing
// from the bundled fallbacks and the Noto faces what primary cannot draw.
func LayoutLine(primary *Face, s string) (Line, error) {
	if primary == nil {
		primary = Default()
	}
	if err := primary.load(); err != nil {
		return Line{}, err
	}
	runes := []rune(s)
	if len(runes) == 0 {
		return Line{}, nil
	}

	// 1. direction (bidi.go)
	levels, rtl := bidiLevels(runes)
	line := Line{RTL: rtl}

	// 2. script, resolved: a weak character takes the script before it, or
	//    the one after it at the start of the line.
	scripts := make([]string, len(runes))
	for i, r := range runes {
		scripts[i] = ScriptOf(r)
	}
	prev := ""
	for i := range scripts {
		if !weak(scripts[i]) {
			prev = scripts[i]
		} else if prev != "" {
			scripts[i] = prev
		}
	}
	// Still weak: nothing strong before it. Take the first strong after.
	next := ""
	for i := len(scripts) - 1; i >= 0; i-- {
		if own := ScriptOf(runes[i]); !weak(own) {
			next = own
		} else if weak(scripts[i]) && next != "" {
			scripts[i] = next
		}
	}
	han := hanKey(runes)

	// 3. face, and the items it makes
	var items []item
	for i, r := range runes {
		face, miss := faceFor(primary, r, scripts[i], han)
		if face == nil {
			miss.At = i
			line.Missing = append(line.Missing, miss)
			continue
		}
		if n := len(items); n > 0 && items[n-1].end == i && items[n-1].face == face &&
			items[n-1].level == levels[i] && items[n-1].script == scripts[i] {
			items[n-1].end = i + 1
			continue
		}
		items = append(items, item{start: i, end: i + 1, level: levels[i], face: face, script: scripts[i]})
	}

	// 4. glyphs
	runs := make([]Run, 0, len(items))
	for _, it := range items {
		run, missing, err := glyphsOf(runes, it)
		if err != nil {
			return Line{}, err
		}
		line.Missing = append(line.Missing, missing...)
		// Kept even when empty, so runs[i] stays items[i] for the reordering.
		runs = append(runs, run)
	}

	// 5. visual order (L2): from the highest level down to the lowest odd
	//    one, reverse every maximal sequence at that level or above.
	lv := make([]int, len(items))
	maxL, minOdd := 0, 1<<30
	for i, it := range items {
		lv[i] = it.level
		if it.level > maxL {
			maxL = it.level
		}
		if it.level%2 == 1 && it.level < minOdd {
			minOdd = it.level
		}
	}
	for l := maxL; l >= minOdd && l > 0; l-- {
		for i := 0; i < len(runs); {
			if lv[i] < l {
				i++
				continue
			}
			j := i
			for j < len(runs) && lv[j] >= l {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				runs[a], runs[b] = runs[b], runs[a]
				lv[a], lv[b] = lv[b], lv[a]
			}
			i = j
		}
	}
	for _, r := range runs {
		if len(r.Glyphs) > 0 {
			line.Runs = append(line.Runs, r)
		}
	}
	return line, nil
}

// faceFor picks the face that draws r: the chosen face, its bundled
// fallbacks, then the Noto faces for r's resolved script, for its own
// script, and for the shared characters. Nil with the reason when none can.
func faceFor(primary *Face, r rune, script, han string) (*Face, Missing) {
	if _, ok := primary.glyph(r); ok {
		return primary, Missing{}
	}
	for _, f := range fallbacks(primary) {
		if _, ok := f.glyph(r); ok {
			return f, Missing{}
		}
	}
	own := ScriptOf(r)
	keys := []string{script}
	if script == "Han" {
		keys[0] = han
	}
	if own != script && !weak(own) {
		keys = append(keys, own)
	}
	// Why it will be missing, if it is: a script with no face at all is
	// NoFont, and stays NoFont whatever the last-resort Common faces answer —
	// telling somebody "the font could not be downloaded" for a script no
	// font exists for would send them looking for a network problem.
	reason := NoFont
	for _, key := range keys {
		if len(notoScripts[key]) > 0 {
			reason = NoGlyph
		}
	}
	if weak(own) {
		reason = NoGlyph
	}
	keys = append(keys, "Common")
	for k, key := range keys {
		for _, fam := range notoScripts[key] {
			face, err := notoFace(fam)
			if err != nil {
				if (reason == NoGlyph || reason == Unavailable) && (k < len(keys)-1 || weak(own)) {
					reason = Unavailable
					if errors.Is(err, ErrStillDownloading) {
						reason = Downloading
					}
				}
				continue
			}
			if _, ok := face.glyph(r); ok {
				return face, Missing{}
			}
		}
	}
	if r == ' ' || r == '\u00a0' {
		// A space nobody draws is still a space: the primary face's.
		return primary, Missing{}
	}
	return nil, Missing{Rune: r, Script: own, Reason: reason}
}

// glyphsOf turns one item into glyphs: HarfBuzz for a shaped face, the
// character map otherwise. Glyphs come back in visual order.
func glyphsOf(runes []rune, it item) (Run, []Missing, error) {
	rtl := it.level%2 == 1
	if !it.face.Shaped {
		run := Run{Face: it.face}
		for i := it.start; i < it.end; i++ {
			r := runes[i]
			if rtl {
				r = mirror(r)
			}
			g, ok := it.face.glyph(r)
			if !ok {
				continue
			}
			if g.Text == "" { // a space drawn with the face's own keeps " "
				g.Text = string(runes[i])
			}
			run.Glyphs = append(run.Glyphs, g)
		}
		if rtl {
			for a, b := 0, len(run.Glyphs)-1; a < b; a, b = a+1, b-1 {
				run.Glyphs[a], run.Glyphs[b] = run.Glyphs[b], run.Glyphs[a]
			}
		}
		return run, nil, nil
	}

	gf, err := it.face.shapingFace()
	if err != nil {
		return Run{}, nil, err
	}
	dir := di.DirectionLTR
	if rtl {
		dir = di.DirectionRTL
	}
	script := language.Common
	for i := it.start; i < it.end; i++ {
		if s := language.LookupScript(runes[i]); s.Strong() {
			script = s
			break
		}
	}
	var sh shaping.HarfbuzzShaper
	out := sh.Shape(shaping.Input{
		Text: runes, RunStart: it.start, RunEnd: it.end,
		Direction: dir, Face: gf, Size: fixed.I(1000),
		Script: script, Language: language.NewLanguage(""),
	})
	// Cluster boundaries, for the text each glyph stands for.
	starts := map[int]bool{}
	for _, g := range out.Glyphs {
		starts[g.ClusterIndex] = true
	}
	var bounds []int
	for c := range starts {
		bounds = append(bounds, c)
	}
	sort.Ints(bounds)
	textOf := func(c int) string {
		i := sort.SearchInts(bounds, c)
		end := it.end
		if i+1 < len(bounds) {
			end = bounds[i+1]
		}
		if c < 0 || c >= end || end > len(runes) {
			return ""
		}
		return string(runes[c:end])
	}
	run := Run{Face: it.face, Shaped: true}
	var missing []Missing
	seen := map[int]bool{}
	for _, g := range out.Glyphs {
		if g.GlyphID == 0 {
			if !seen[g.ClusterIndex] && g.ClusterIndex >= 0 && g.ClusterIndex < len(runes) {
				r := runes[g.ClusterIndex]
				missing = append(missing, Missing{Rune: r, Script: ScriptOf(r), Reason: NoGlyph, At: g.ClusterIndex})
			}
			seen[g.ClusterIndex] = true
			continue
		}
		text, cont := "", seen[g.ClusterIndex]
		if !cont {
			text = textOf(g.ClusterIndex)
			seen[g.ClusterIndex] = true
		}
		var r rune
		if g.ClusterIndex >= 0 && g.ClusterIndex < len(runes) {
			r = runes[g.ClusterIndex]
		}
		run.Glyphs = append(run.Glyphs, Glyph{
			GID: uint16(g.GlyphID), Rune: r, Text: text, Cont: cont,
			Advance: float64(g.XAdvance) / 64,
			XOffset: float64(g.XOffset) / 64, YOffset: float64(g.YOffset) / 64,
		})
	}
	return run, missing, nil
}

// shapingFace is the face parsed for HarfBuzz, once.
func (f *Face) shapingFace() (*gtfont.Face, error) {
	f.gtOnce.Do(func() {
		face, err := gtfont.ParseTTF(bytes.NewReader(f.TTF))
		if err != nil {
			f.gtErr = err
			return
		}
		f.gt = face
	})
	if f.gt == nil && f.gtErr == nil {
		return nil, errors.New("fontkit: " + f.ID + " could not be parsed for shaping")
	}
	return f.gt, f.gtErr
}

// mirror is the glyph a paired character takes in a right-to-left run
// (UAX #9 rule L4) — for the few a face drawn without shaping carries.
func mirror(r rune) rune {
	switch r {
	case '(':
		return ')'
	case ')':
		return '('
	case '[':
		return ']'
	case ']':
		return '['
	case '{':
		return '}'
	case '}':
		return '{'
	case '<':
		return '>'
	case '>':
		return '<'
	case '«':
		return '»'
	case '»':
		return '«'
	}
	return r
}
