package pdfdoc

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/digitorus/pdf"

	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/geometry"
)

// Item kinds.
const (
	KindText  = "text"
	KindCheck = "check"
)

// Alignments.
const (
	AlignLeft   = "left"
	AlignCenter = "center"
	AlignRight  = "right"
)

// Item is one thing to draw on a page: a line of text in one of the
// embedded faces, or a tick in a checkbox.
type Item struct {
	Page int           // 1-based
	Rect geometry.Rect // PDF user space of the UNROTATED page, CropBox included
	// Rotate is the page's /Rotate, so the stamp reads upright to whoever
	// opens the document rather than lying on its side.
	Rotate int
	Kind   string
	Text   string
	Font   string  // a fontkit id; empty falls back to the default face
	Size   float64 // points; 0 fits the text to the box
	Align  string
}

// Stamp appends an incremental update that draws items over the pages
// they name and returns the new document. The original bytes are copied
// unchanged, so anything already signed keeps verifying.
func Stamp(in []byte, items []Item) ([]byte, error) {
	if len(items) == 0 {
		return in, nil
	}
	rdr, err := pdf.NewReader(bytes.NewReader(in), int64(len(in)))
	if err != nil {
		return nil, fmt.Errorf("pdfdoc: %w", err)
	}
	facts, err := readTrailer(in, rdr)
	if err != nil {
		return nil, err
	}
	pagesRoot := rdr.Trailer().Key("Root").Key("Pages")
	if pagesRoot.IsNull() {
		return nil, fmt.Errorf("pdfdoc: the document has no page tree")
	}

	byPage := map[int][]Item{}
	for _, it := range items {
		if it.Page < 1 {
			it.Page = 1
		}
		byPage[it.Page] = append(byPage[it.Page], it)
	}
	pages := make([]int, 0, len(byPage))
	for n := range byPage {
		pages = append(pages, n)
	}
	sort.Ints(pages)

	b := newBuilder(facts.size)
	fonts := newFontSet()
	type pending struct {
		page    pdf.Value
		content int
		save    int
	}
	var queued []pending
	for _, n := range pages {
		page, err := findPage(pagesRoot, n)
		if err != nil {
			return nil, err
		}
		ops, err := drawItems(byPage[n], fonts)
		if err != nil {
			return nil, err
		}
		if len(ops) == 0 {
			continue
		}
		queued = append(queued, pending{
			page: page,
			// The document's own content is wrapped in q … Q so an
			// unbalanced graphics state in it cannot leak into the stamp.
			save:    b.addStream("", []byte("q\n")),
			content: b.addStream("", append([]byte("Q\n"), ops...)),
		})
	}
	if len(queued) == 0 {
		return in, nil
	}
	fonts.emit(b)
	for _, q := range queued {
		body, err := rewritePage(q.page, q.save, q.content, fonts)
		if err != nil {
			return nil, err
		}
		ptr := q.page.GetPtr()
		b.replace(int(ptr.GetID()), int(ptr.GetGen()), body)
	}
	return appendUpdate(in, b, facts)
}

// ── drawing ────────────────────────────────────────────────────────────

// frame is the item's box as the reader sees it: an origin in user space
// plus the two unit vectors that make "right" and "up" on screen, so a
// stamp on a rotated page is written the way it will be read.
type frame struct {
	ox, oy float64
	ax, ay float64 // along the visual x
	bx, by float64 // along the visual y
	w, h   float64 // the visual box size
}

func newFrame(r geometry.Rect, rotate int) frame {
	switch geometry.NormRotation(rotate) {
	case 90:
		return frame{ox: r.X + r.W, oy: r.Y, ax: 0, ay: 1, bx: -1, by: 0, w: r.H, h: r.W}
	case 180:
		return frame{ox: r.X + r.W, oy: r.Y + r.H, ax: -1, ay: 0, bx: 0, by: -1, w: r.W, h: r.H}
	case 270:
		return frame{ox: r.X, oy: r.Y + r.H, ax: 0, ay: -1, bx: 1, by: 0, w: r.H, h: r.W}
	}
	return frame{ox: r.X, oy: r.Y, ax: 1, ay: 0, bx: 0, by: 1, w: r.W, h: r.H}
}

func (f frame) cm() string {
	return fmt.Sprintf("%.4f %.4f %.4f %.4f %.4f %.4f cm\n", f.ax, f.ay, f.bx, f.by, f.ox, f.oy)
}

// Size bounds for a fitted stamp.
const (
	minTextSize = 5
	maxTextSize = 28
)

var ink = [3]float64{0.10, 0.14, 0.44} // the same ballpoint blue as the pad

func drawItems(items []Item, fonts *fontSet) ([]byte, error) {
	var buf bytes.Buffer
	for _, it := range items {
		f := newFrame(it.Rect, it.Rotate)
		if f.w <= 1 || f.h <= 1 {
			continue
		}
		switch it.Kind {
		case KindCheck:
			drawCheck(&buf, f)
		default:
			if it.Text == "" {
				continue
			}
			if err := drawText(&buf, f, it, fonts); err != nil {
				return nil, err
			}
		}
	}
	return buf.Bytes(), nil
}

func drawText(buf *bytes.Buffer, f frame, it Item, fonts *fontSet) error {
	_, err := drawTextUsing(buf, f, it, fonts)
	return err
}

// drawTextUsing is drawText, also answering which faces it drew with.
func drawTextUsing(buf *bytes.Buffer, f frame, it Item, fonts *fontSet) ([]*fontUse, error) {
	face := fontkit.Get(it.Font)
	pad := f.h * 0.12
	if pad > 3 {
		pad = 3
	}
	boxW := f.w - 2*pad
	if boxW <= 0 {
		boxW = f.w
		pad = 0
	}
	size := it.Size
	if size <= 0 {
		size = face.FitSize(it.Text, boxW, f.h*0.92, minTextSize, maxTextSize)
	}
	text := clipToWidth(face, it.Text, boxW, size)
	line, err := fontkit.LayoutLine(face, text)
	if err != nil {
		return nil, fmt.Errorf("pdfdoc: font %s: %w", face.ID, err)
	}
	runs := line.Runs
	if len(runs) == 0 {
		return nil, nil
	}
	width := fontkit.RunsWidth(runs, size)
	align := it.Align
	// A right-to-left paragraph starts at the right edge of its box, as its
	// reader expects, unless the field asked for something else.
	if line.RTL && (align == AlignLeft || align == "") {
		align = AlignRight
	}
	x := pad
	switch align {
	case AlignCenter:
		x = (f.w - width) / 2
	case AlignRight:
		x = f.w - pad - width
	}
	if x < 0 {
		x = 0
	}
	lineH := (face.Ascent() - face.Descent()) * size / 1000
	y := (f.h-lineH)/2 + (-face.Descent())*size/1000

	buf.WriteString("q\n")
	buf.WriteString(f.cm())
	fmt.Fprintf(buf, "0 0 %.3f %.3f re W n\n", f.w, f.h)
	used := textOps(buf, fonts, runs, size, x, y, ink)
	buf.WriteString("Q\n")
	return used, nil
}

// textOps writes one line and returns the faces it drew with (an appearance
// XObject has to name every one of them in its resources).
//
// A run mapped character by character is one Tj, advanced by the widths the
// PDF records. A SHAPED run is one TJ too, with a correction after every
// glyph whose contextual advance differs from the width the PDF records for
// it (a joined Arabic letter, a vowel sign of no width), so a reader copying
// the line gets one word, not letters with gaps. A glyph the shaper OFFSETS
// (a mark placed over or under its base) stays in the same flow: moved
// sideways by corrections around it and raised with Ts.
//
// ⚠ A cluster of SEVERAL glyphs (a Devanagari syllable whose vowel sign is
// drawn before its consonants, a base with its marks) is wrapped in a
// marked-content span whose ActualText is the cluster's characters: the
// ToUnicode map is one string per glyph for the whole document, so it
// cannot say that [ि, त्र] reads "त्रि", and pdftotext gave "क्षत्रि य
// हस्ता क्षर" back until it could (2026-09-21). Per cluster, not per line:
// a whole line as ActualText came back REVERSED from poppler for Arabic
// and Hebrew (it reorders what it is given as if it were in visual order),
// while clusters of one glyph — every letter of an Arabic word — are left
// to the map, which readers already handle in both directions.
func textOps(buf *bytes.Buffer, fonts *fontSet, runs []fontkit.Run, size, x, y float64, shade [3]float64) []*fontUse {
	fmt.Fprintf(buf, "BT %.3f %.3f %.3f rg\n", shade[0], shade[1], shade[2])
	var used []*fontUse
	pen := x
	for _, run := range runs {
		use := fonts.use(run.Face)
		use.note(run.Glyphs)
		used = append(used, use)
		fmt.Fprintf(buf, "/%s %.3f Tf\n", use.name, size)
		if !run.Shaped {
			fmt.Fprintf(buf, "1 0 0 1 %.3f %.3f Tm %s Tj\n", pen, y, hexGlyphs(run.Glyphs))
			for _, g := range run.Glyphs {
				pen += g.Advance * size / 1000
			}
			continue
		}
		// The run is ONE flow of text from one position: every glyph moves
		// the pen by exactly its shaped advance (TJ corrections), so a
		// reader sees one word, not letters with gaps.
		fmt.Fprintf(buf, "1 0 0 1 %.3f %.3f Tm\n", pen, y)
		var tj strings.Builder
		open := false
		flush := func() {
			if open {
				fmt.Fprintf(buf, "[%s] TJ\n", tj.String())
				tj.Reset()
				open = false
			}
		}
		glyphs := run.Glyphs
		for i := 0; i < len(glyphs); {
			j := i + 1
			for j < len(glyphs) && glyphs[j].Cont {
				j++
			}
			span := j-i > 1 && glyphs[i].Text != ""
			if span {
				flush()
				fmt.Fprintf(buf, "/Span <</ActualText <FEFF%s>>> BDC\n", utf16BEText(glyphs[i].Text))
			}
			for _, g := range glyphs[i:j] {
				if g.XOffset != 0 || g.YOffset != 0 {
					// A mark the shaper OFFSETS (a vowel point over a
					// Hebrew letter, a Thaana sukun) stays in the flow:
					// shifted by a correction before it and raised with Ts,
					// then the pen goes on by the mark's own advance. It
					// used to be set apart with its own Tm, which drew the
					// same but read back as "ިދެވ ިހ" — every mark a word
					// of its own (pdftotext, 2026-09-21).
					flush()
					rise := g.YOffset * size / 1000
					if rise != 0 {
						fmt.Fprintf(buf, "%.3f Ts ", rise)
					}
					fmt.Fprintf(buf, "[%s<%04X>%s] TJ", tjAdjust(-g.XOffset), g.GID, tjAdjust(use.used[g.GID]+g.XOffset-g.Advance))
					if rise != 0 {
						buf.WriteString(" 0 Ts")
					}
					buf.WriteString("\n")
					pen += g.Advance * size / 1000
					continue
				}
				open = true
				fmt.Fprintf(&tj, "<%04X>", g.GID)
				if adj := use.used[g.GID] - g.Advance; adj > 0.01 || adj < -0.01 {
					fmt.Fprintf(&tj, " %.2f ", adj)
				}
				pen += g.Advance * size / 1000
			}
			if span {
				flush()
				buf.WriteString("EMC\n")
			}
			i = j
		}
		flush()
	}
	buf.WriteString("ET\n")
	return used
}

// tjAdjust is one TJ correction in thousandths of an em — positive moves
// the next glyph LEFT — or nothing when it is too small to matter.
func tjAdjust(v float64) string {
	if v > -0.01 && v < 0.01 {
		return ""
	}
	return fmt.Sprintf(" %.2f ", v)
}

// clipToWidth shortens text with an ellipsis when even the smallest size
// cannot hold it, so a stamp never runs past its box.
func clipToWidth(face *fontkit.Face, text string, boxW, size float64) string {
	if face.Width(text, size) <= boxW {
		return text
	}
	r := []rune(text)
	for len(r) > 1 {
		r = r[:len(r)-1]
		s := string(r) + "…"
		if face.Width(s, size) <= boxW {
			return s
		}
	}
	return string(r)
}

// drawCheck ticks a checkbox with two strokes, so it needs no font.
func drawCheck(buf *bytes.Buffer, f frame) {
	s := f.w
	if f.h < s {
		s = f.h
	}
	cx, cy := f.w/2, f.h/2
	w := s * 0.12
	if w < 0.6 {
		w = 0.6
	}
	buf.WriteString("q\n")
	buf.WriteString(f.cm())
	fmt.Fprintf(buf, "%.3f %.3f %.3f RG %.3f w 1 J 1 j\n", ink[0], ink[1], ink[2], w)
	fmt.Fprintf(buf, "%.3f %.3f m %.3f %.3f l %.3f %.3f l S\n",
		cx-s*0.30, cy+s*0.02,
		cx-s*0.08, cy-s*0.22,
		cx+s*0.32, cy+s*0.28)
	buf.WriteString("Q\n")
}

// ── the page object ────────────────────────────────────────────────────

// rewritePage re-emits a page dictionary with the stamp's content stream
// appended and the stamp's fonts merged into its resources. Every other
// entry is copied as it stands — references stay references, so nothing
// of the document is duplicated or resolved away.
func rewritePage(page pdf.Value, save, content int, fonts *fontSet) ([]byte, error) {
	pageID := int(page.GetPtr().GetID())
	var b bytes.Buffer
	b.WriteString("<<\n")
	for _, key := range page.Keys() {
		switch key {
		case "Contents", "Resources":
			continue
		}
		fmt.Fprintf(&b, "  /%s %s\n", key, serialise(page.Key(key), pageID))
	}

	b.WriteString("  /Contents [")
	fmt.Fprintf(&b, "%d 0 R", save)
	switch c := page.Key("Contents"); {
	case c.Kind() == pdf.Array:
		for i := 0; i < c.Len(); i++ {
			p := c.Index(i).GetPtr()
			if p.GetID() == 0 {
				continue
			}
			fmt.Fprintf(&b, " %d %d R", p.GetID(), p.GetGen())
		}
	case c.Kind() == pdf.Stream:
		p := c.GetPtr()
		fmt.Fprintf(&b, " %d %d R", p.GetID(), p.GetGen())
	}
	fmt.Fprintf(&b, " %d 0 R]\n", content)

	res, err := mergedResources(page, fonts)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "  /Resources %s\n", res)
	b.WriteString(">>")
	return b.Bytes(), nil
}

// mergedResources writes the page's effective resources (its own, or the
// ones it inherits) with the stamp's fonts added. The dictionary is
// written onto the page itself: a shared resource object is never edited,
// because other pages point at it.
func mergedResources(page pdf.Value, fonts *fontSet) (string, error) {
	res := inherited(page, "Resources")
	ownerID := int(res.GetPtr().GetID())
	var b bytes.Buffer
	b.WriteString("<<")
	fontsWritten := false
	for _, key := range res.Keys() {
		if key == "Font" {
			fontsWritten = true
			fontDict := res.Key("Font")
			fmt.Fprintf(&b, " /Font <<")
			fid := int(fontDict.GetPtr().GetID())
			for _, fk := range fontDict.Keys() {
				fmt.Fprintf(&b, " /%s %s", fk, serialise(fontDict.Key(fk), fid))
			}
			writeStampFonts(&b, fonts)
			b.WriteString(" >>")
			continue
		}
		fmt.Fprintf(&b, " /%s %s", key, serialise(res.Key(key), ownerID))
	}
	if !fontsWritten {
		b.WriteString(" /Font <<")
		writeStampFonts(&b, fonts)
		b.WriteString(" >>")
	}
	b.WriteString(" >>")
	return b.String(), nil
}

func writeStampFonts(b *bytes.Buffer, fonts *fontSet) {
	for _, u := range fonts.order {
		if u.obj == 0 {
			continue
		}
		fmt.Fprintf(b, " /%s %d 0 R", u.name, u.obj)
	}
}

// inherited walks a page's /Parent chain for an attribute pages may
// inherit (/Resources, /MediaBox, /Rotate).
func inherited(v pdf.Value, key string) pdf.Value {
	for depth := 0; !v.IsNull() && depth < 64; depth++ {
		if r := v.Key(key); !r.IsNull() {
			return r
		}
		v = v.Key("Parent")
	}
	return pdf.Value{}
}

// findPage walks the page tree for a 1-based page number.
func findPage(pages pdf.Value, number int) (pdf.Value, error) {
	v, left := findPageRec(pages, number, 0)
	if left != 0 {
		return pdf.Value{}, fmt.Errorf("pdfdoc: page %d is not in the document", number)
	}
	return v, nil
}

func findPageRec(node pdf.Value, number, depth int) (pdf.Value, int) {
	if depth > 64 || node.IsNull() {
		return pdf.Value{}, number
	}
	switch node.Key("Type").Name() {
	case "Pages":
		kids := node.Key("Kids")
		for i := 0; i < kids.Len(); i++ {
			v, left := findPageRec(kids.Index(i), number, depth+1)
			if left == 0 {
				return v, 0
			}
			number = left
		}
		return pdf.Value{}, number
	case "Page":
		if number == 1 {
			return node, 0
		}
		return pdf.Value{}, number - 1
	}
	// A node without /Type: treat a /Kids holder as a tree, anything else
	// as a leaf, which is what tolerant readers do.
	if !node.Key("Kids").IsNull() {
		kids := node.Key("Kids")
		for i := 0; i < kids.Len(); i++ {
			v, left := findPageRec(kids.Index(i), number, depth+1)
			if left == 0 {
				return v, 0
			}
			number = left
		}
		return pdf.Value{}, number
	}
	if number == 1 {
		return node, 0
	}
	return pdf.Value{}, number - 1
}
