package pdfdoc

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/brf-tech/filex-sign/internal/fontkit"
)

// ResourcePrefix names the font resources this package adds to a page. It
// is deliberately unlikely to collide with a name the document already
// uses, because a page's resource dictionary is merged, not replaced.
const ResourcePrefix = "FxSg"

// fontUse is one face the update draws with, and the glyphs it drew.
type fontUse struct {
	face *fontkit.Face
	name string             // resource name inside the page (/FxSg0)
	used map[uint16]float64 // glyph id → advance per 1000
	runs map[uint16]string  // glyph id → the text it stands for, for /ToUnicode
	obj  int                // the Type0 font object, once written
}

// fontSet collects the faces an update needs so each is embedded once,
// however many fields use it.
type fontSet struct {
	order []*fontUse
	byID  map[string]*fontUse
}

func newFontSet() *fontSet { return &fontSet{byID: map[string]*fontUse{}} }

func (s *fontSet) use(face *fontkit.Face) *fontUse {
	if u, ok := s.byID[face.ID]; ok {
		return u
	}
	u := &fontUse{face: face, name: fmt.Sprintf("%s%d", ResourcePrefix, len(s.order)),
		used: map[uint16]float64{}, runs: map[uint16]string{}}
	s.byID[face.ID] = u
	s.order = append(s.order, u)
	return u
}

// note records the glyphs a line drew. The width a reader keeps for a glyph
// is the FACE's advance for it (a shaped glyph may be placed with another);
// its text is the first text it was drawn for — a joined Arabic form or an
// Indic conjunct stands for the letters it was shaped from.
func (u *fontUse) note(glyphs []fontkit.Glyph) {
	for _, g := range glyphs {
		if _, seen := u.used[g.GID]; !seen {
			u.used[g.GID] = u.face.AdvanceOf(g.GID)
		}
		if _, seen := u.runs[g.GID]; !seen {
			text := g.Text
			if text == "" && g.Rune != 0 && !g.Cont {
				text = string(g.Rune)
			}
			u.runs[g.GID] = text
		}
	}
}

// hexGlyphs is the string an Identity-H Tj draws: two bytes per glyph.
func hexGlyphs(glyphs []fontkit.Glyph) string {
	var b strings.Builder
	b.WriteByte('<')
	for _, g := range glyphs {
		fmt.Fprintf(&b, "%04X", g.GID)
	}
	b.WriteByte('>')
	return b.String()
}

// emit writes every used face into the update: the embedded subset, its
// descriptor, the CID font with the widths actually drawn, the Type0
// wrapper and a ToUnicode map so the stamped text can still be copied
// out of the document.
func (s *fontSet) emit(b *builder) {
	for _, u := range s.order {
		if len(u.used) == 0 {
			continue
		}
		program, psName, cidMap := u.face.TTF, u.face.PSName, "/Identity"
		if u.face.Fetched {
			// A fetched face is megabytes: embed only what this document
			// draws (subset.go), under a subset tag as the PDF spec asks.
			keep := make(map[uint16]bool, len(u.used))
			for gid := range u.used {
				keep[gid] = true
			}
			cut, newID, err := subsetTrueType(u.face.TTF, keep)
			if err == nil {
				program = cut
				psName = subsetTag(u.used) + "+" + u.face.PSName
				cidMap = fmt.Sprintf("%d 0 R", b.addStream("/Filter /FlateDecode", deflate(cidToGIDMap(newID))))
			}
		}
		file := b.addStream(fmt.Sprintf("/Length1 %d", len(program)), program)
		bbox := u.face.BBox()
		flags := 32 // non-symbolic
		if u.face.Script {
			flags |= 1 << 6 // italic-ish script face
		}
		desc := b.add([]byte(fmt.Sprintf(
			"<< /Type /FontDescriptor /FontName /%s /Flags %d /FontBBox [%.0f %.0f %.0f %.0f]"+
				" /ItalicAngle 0 /Ascent %.0f /Descent %.0f /CapHeight %.0f /StemV 80 /FontFile2 %d 0 R >>",
			psName, flags, bbox[0], bbox[1], bbox[2], bbox[3],
			u.face.Ascent(), u.face.Descent(), u.face.CapHeight(), file)))
		cid := b.add([]byte(fmt.Sprintf(
			"<< /Type /Font /Subtype /CIDFontType2 /BaseFont /%s"+
				" /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >>"+
				" /FontDescriptor %d 0 R /DW 1000 /W [%s] /CIDToGIDMap %s >>",
			psName, desc, widthsArray(u.used), cidMap)))
		toUni := b.addStream("", toUnicodeCMap(u.runs))
		u.obj = b.add([]byte(fmt.Sprintf(
			"<< /Type /Font /Subtype /Type0 /BaseFont /%s /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>",
			psName, cid, toUni)))
	}
}

// widthsArray writes /W as one run per glyph — short, and never wrong
// about a gap in the ids.
func widthsArray(used map[uint16]float64) string {
	ids := make([]int, 0, len(used))
	for id := range used {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	var b strings.Builder
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d [%.0f]", id, used[uint16(id)])
	}
	return b.String()
}

// cidToGIDMap is the /CIDToGIDMap stream of a subset face: for every
// original glyph id (the CID the content writes), its id in the subset.
func cidToGIDMap(newID map[uint16]uint16) []byte {
	max := 0
	for old := range newID {
		if int(old) > max {
			max = int(old)
		}
	}
	out := make([]byte, 2*(max+1))
	for old, n := range newID {
		out[2*int(old)] = byte(n >> 8)
		out[2*int(old)+1] = byte(n)
	}
	return out
}

// subsetTag is the six capital letters a subset font's name begins with
// (PDF 32000 9.6.4), derived from the glyphs so the same subset gets the
// same name.
func subsetTag(used map[uint16]float64) string {
	ids := make([]int, 0, len(used))
	for id := range used {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	var h uint32 = 2166136261
	for _, id := range ids {
		h = (h ^ uint32(id)) * 16777619
	}
	tag := make([]byte, 6)
	for i := range tag {
		tag[i] = byte('A' + h%26)
		h /= 26
	}
	return string(tag)
}

// toUnicodeCMap maps the glyph ids back to text so a reader can copy the
// stamped value — a shaped glyph back to the letters it was made from.
func toUnicodeCMap(runs map[uint16]string) []byte {
	ids := make([]int, 0, len(runs))
	for id, text := range runs {
		// A glyph with no text of its own (the second glyph of a cluster)
		// has no entry: copying the line gives each letter once.
		if text != "" {
			ids = append(ids, int(id))
		}
	}
	sort.Ints(ids)
	var b bytes.Buffer
	b.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n")
	b.WriteString("/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n")
	b.WriteString("/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n")
	b.WriteString("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		fmt.Fprintf(&b, "%d beginbfchar\n", end-start)
		for _, id := range ids[start:end] {
			fmt.Fprintf(&b, "<%04X> <%s>\n", id, utf16BEText(runs[uint16(id)]))
		}
		b.WriteString("endbfchar\n")
	}
	b.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	return b.Bytes()
}

// utf16BEText is a whole string in UTF-16BE hex.
func utf16BEText(s string) string {
	var b strings.Builder
	for _, r := range s {
		b.WriteString(utf16BE(r))
	}
	return b.String()
}

func utf16BE(r rune) string {
	if r > 0xFFFF {
		r -= 0x10000
		return fmt.Sprintf("%04X%04X", 0xD800+(r>>10), 0xDC00+(r&0x3FF))
	}
	return fmt.Sprintf("%04X", r)
}
