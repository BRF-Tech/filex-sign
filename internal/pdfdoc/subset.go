package pdfdoc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// ── A TrueType face cut down to the glyphs one document draws ──────────
//
// ⚠ Why: a Noto face fetched for a script is megabytes (Noto Sans JP:
// 5.3 MB) and embedding it whole would put all of it into every signed
// document that has one Japanese name in it. The bundled faces are already
// small subsets and are embedded as they are; a fetched face is cut here to
// the glyphs the document uses (and the components those are built from).
//
// Glyph ids are RENUMBERED (0, 1, 2 …) so the file's tables shrink with the
// glyph count, and the PDF keeps writing the ORIGINAL ids in its content
// streams: the CID font carries a /CIDToGIDMap from the original id to the
// new one. The content never has to know a subset was made.
//
// Only the tables a PDF reader draws with are written: head, hhea, maxp,
// hmtx, loca, glyf, post (format 3), and the hinting programs when the face
// has them (cvt, fpgm, prep — glyph instructions refer to them). Variation
// tables are not: the files the app fetches are static instances.

type sfntTable struct {
	tag  string
	data []byte
}

// subsetTrueType keeps glyph 0, the glyphs in `keep`, and every component a
// kept composite glyph is built from. It returns the new font and, per
// original id, the new one.
func subsetTrueType(font []byte, keep map[uint16]bool) ([]byte, map[uint16]uint16, error) {
	tables, err := readTables(font)
	if err != nil {
		return nil, nil, err
	}
	head, hhea, maxp, hmtx, loca, glyf := tables["head"], tables["hhea"], tables["maxp"], tables["hmtx"], tables["loca"], tables["glyf"]
	if head == nil || hhea == nil || maxp == nil || hmtx == nil || loca == nil || glyf == nil {
		return nil, nil, errors.New("pdfdoc: not a TrueType (glyf) face")
	}
	if len(head) < 54 || len(hhea) < 36 || len(maxp) < 6 {
		return nil, nil, errors.New("pdfdoc: truncated font tables")
	}
	numGlyphs := int(binary.BigEndian.Uint16(maxp[4:]))
	longLoca := int16(binary.BigEndian.Uint16(head[50:])) == 1
	offset := func(gid int) (int, int, bool) {
		if gid < 0 || gid >= numGlyphs {
			return 0, 0, false
		}
		var a, b int
		if longLoca {
			if 4*gid+8 > len(loca) {
				return 0, 0, false
			}
			a, b = int(binary.BigEndian.Uint32(loca[4*gid:])), int(binary.BigEndian.Uint32(loca[4*gid+4:]))
		} else {
			if 2*gid+4 > len(loca) {
				return 0, 0, false
			}
			a, b = 2*int(binary.BigEndian.Uint16(loca[2*gid:])), 2*int(binary.BigEndian.Uint16(loca[2*gid+2:]))
		}
		if a > b || b > len(glyf) {
			return 0, 0, false
		}
		return a, b, true
	}

	// The glyph set, closed over composite components.
	set := map[uint16]bool{0: true}
	var queue []uint16
	for g := range keep {
		if int(g) < numGlyphs && !set[g] {
			set[g] = true
			queue = append(queue, g)
		}
	}
	for len(queue) > 0 {
		g := queue[0]
		queue = queue[1:]
		a, b, ok := offset(int(g))
		if !ok || b-a < 10 {
			continue
		}
		for _, c := range components(glyf[a:b]) {
			if int(c.gid) < numGlyphs && !set[c.gid] {
				set[c.gid] = true
				queue = append(queue, c.gid)
			}
		}
	}
	old := make([]int, 0, len(set))
	for g := range set {
		old = append(old, int(g))
	}
	sort.Ints(old)
	newID := make(map[uint16]uint16, len(old))
	for i, g := range old {
		newID[uint16(g)] = uint16(i)
	}

	// glyf + loca (long offsets), components renumbered.
	var newGlyf []byte
	newLoca := make([]byte, 4*(len(old)+1))
	for i, g := range old {
		binary.BigEndian.PutUint32(newLoca[4*i:], uint32(len(newGlyf)))
		a, b, ok := offset(g)
		if !ok || a == b {
			continue
		}
		data := append([]byte(nil), glyf[a:b]...)
		for _, c := range components(data) {
			binary.BigEndian.PutUint16(data[c.at:], newID[c.gid])
		}
		newGlyf = append(newGlyf, data...)
		for len(newGlyf)%4 != 0 {
			newGlyf = append(newGlyf, 0)
		}
	}
	binary.BigEndian.PutUint32(newLoca[4*len(old):], uint32(len(newGlyf)))

	// hmtx: one full metric per new glyph.
	numH := int(binary.BigEndian.Uint16(hhea[34:]))
	newHmtx := make([]byte, 4*len(old))
	for i, g := range old {
		var adv uint16
		var lsb uint16
		switch {
		case g < numH && 4*g+4 <= len(hmtx):
			adv, lsb = binary.BigEndian.Uint16(hmtx[4*g:]), binary.BigEndian.Uint16(hmtx[4*g+2:])
		case numH > 0 && 4*numH <= len(hmtx):
			adv = binary.BigEndian.Uint16(hmtx[4*(numH-1):])
			if p := 4*numH + 2*(g-numH); p+2 <= len(hmtx) {
				lsb = binary.BigEndian.Uint16(hmtx[p:])
			}
		}
		binary.BigEndian.PutUint16(newHmtx[4*i:], adv)
		binary.BigEndian.PutUint16(newHmtx[4*i+2:], lsb)
	}

	newHead := append([]byte(nil), head...)
	binary.BigEndian.PutUint32(newHead[8:], 0)  // checkSumAdjustment, set below
	binary.BigEndian.PutUint16(newHead[50:], 1) // long loca
	newHhea := append([]byte(nil), hhea...)
	binary.BigEndian.PutUint16(newHhea[34:], uint16(len(old)))
	newMaxp := append([]byte(nil), maxp...)
	binary.BigEndian.PutUint16(newMaxp[4:], uint16(len(old)))
	post := make([]byte, 32)
	if p := tables["post"]; len(p) >= 32 {
		copy(post, p[:32])
	}
	binary.BigEndian.PutUint32(post[0:], 0x00030000)

	out := []sfntTable{
		{"glyf", newGlyf}, {"head", newHead}, {"hhea", newHhea}, {"hmtx", newHmtx},
		{"loca", newLoca}, {"maxp", newMaxp}, {"post", post},
	}
	for _, tag := range []string{"cvt ", "fpgm", "prep"} {
		if t := tables[tag]; t != nil {
			out = append(out, sfntTable{tag, t})
		}
	}
	return writeSFNT(out), newID, nil
}

type component struct {
	at  int // offset of the glyph index inside the glyph data
	gid uint16
}

// components lists the component glyphs of a composite glyph (none for a
// simple one).
func components(g []byte) []component {
	if len(g) < 10 || int16(binary.BigEndian.Uint16(g)) >= 0 {
		return nil
	}
	var out []component
	p := 10
	for p+4 <= len(g) {
		flags := binary.BigEndian.Uint16(g[p:])
		out = append(out, component{at: p + 2, gid: binary.BigEndian.Uint16(g[p+2:])})
		p += 4
		if flags&0x0001 != 0 { // ARG_1_AND_2_ARE_WORDS
			p += 4
		} else {
			p += 2
		}
		switch {
		case flags&0x0008 != 0: // WE_HAVE_A_SCALE
			p += 2
		case flags&0x0040 != 0: // WE_HAVE_AN_X_AND_Y_SCALE
			p += 4
		case flags&0x0080 != 0: // WE_HAVE_A_TWO_BY_TWO
			p += 8
		}
		if flags&0x0020 == 0 { // MORE_COMPONENTS
			break
		}
	}
	return out
}

func readTables(font []byte) (map[string][]byte, error) {
	if len(font) < 12 {
		return nil, errors.New("pdfdoc: font too short")
	}
	n := int(binary.BigEndian.Uint16(font[4:]))
	if 12+16*n > len(font) {
		return nil, errors.New("pdfdoc: font table directory is truncated")
	}
	out := make(map[string][]byte, n)
	for i := 0; i < n; i++ {
		rec := font[12+16*i:]
		tag := string(rec[:4])
		off, ln := int(binary.BigEndian.Uint32(rec[8:])), int(binary.BigEndian.Uint32(rec[12:]))
		if off < 0 || ln < 0 || off+ln > len(font) {
			return nil, fmt.Errorf("pdfdoc: font table %q is out of bounds", tag)
		}
		out[tag] = font[off : off+ln]
	}
	return out, nil
}

// writeSFNT writes a font file: the directory, then every table padded to
// four bytes, with checksums and head's checkSumAdjustment.
func writeSFNT(tables []sfntTable) []byte {
	sort.Slice(tables, func(i, j int) bool { return tables[i].tag < tables[j].tag })
	n := len(tables)
	entry := 0
	for 1<<(entry+1) <= n {
		entry++
	}
	search := (1 << entry) * 16
	hdr := make([]byte, 12+16*n)
	binary.BigEndian.PutUint32(hdr[0:], 0x00010000)
	binary.BigEndian.PutUint16(hdr[4:], uint16(n))
	binary.BigEndian.PutUint16(hdr[6:], uint16(search))
	binary.BigEndian.PutUint16(hdr[8:], uint16(entry))
	binary.BigEndian.PutUint16(hdr[10:], uint16(n*16-search))
	body := []byte{}
	offset := len(hdr)
	headAt := -1
	for i, t := range tables {
		rec := hdr[12+16*i:]
		copy(rec, t.tag)
		binary.BigEndian.PutUint32(rec[4:], checksum(t.data))
		binary.BigEndian.PutUint32(rec[8:], uint32(offset+len(body)))
		binary.BigEndian.PutUint32(rec[12:], uint32(len(t.data)))
		if t.tag == "head" {
			headAt = offset + len(body)
		}
		body = append(body, t.data...)
		for len(body)%4 != 0 {
			body = append(body, 0)
		}
	}
	out := append(hdr, body...)
	if headAt >= 0 && headAt+12 <= len(out) {
		binary.BigEndian.PutUint32(out[headAt+8:], 0xB1B0AFBA-checksum(out))
	}
	return out
}

func checksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i < len(b); i += 4 {
		var w [4]byte
		copy(w[:], b[i:])
		sum += binary.BigEndian.Uint32(w[:])
	}
	return sum
}
