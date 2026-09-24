// Package pdfdoc writes PDF: the incremental update that stamps a
// signer's field values onto the pages (Stamp) and the one-page audit
// trail a finished request can leave beside the document (Audit).
//
// It writes only what it must. Reading is digitorus/pdf's job (the same
// parser pdfsign drives), and the update it appends is the same shape
// pdfsign appends for a signature: the original bytes are never touched,
// so every earlier signature keeps verifying over its own byte range.
package pdfdoc

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/digitorus/pdf"
)

// object is one indirect object of the update.
type object struct {
	id   int
	gen  int
	body []byte // between "N G obj\n" and "\nendobj"
}

// builder collects the objects an update adds or replaces.
type builder struct {
	next int
	objs []object
}

func newBuilder(firstFreeID int) *builder {
	if firstFreeID < 1 {
		firstFreeID = 1
	}
	return &builder{next: firstFreeID}
}

// add appends a new object and returns its id.
func (b *builder) add(body []byte) int {
	id := b.next
	b.next++
	b.objs = append(b.objs, object{id: id, body: body})
	return id
}

// replace overwrites an object that already exists in the base file,
// keeping its generation number.
func (b *builder) replace(id, gen int, body []byte) {
	b.objs = append(b.objs, object{id: id, gen: gen, body: body})
}

// addStream appends a stream object; extra holds dictionary entries
// beside the /Length this writes itself.
func (b *builder) addStream(extra string, data []byte) int {
	var buf bytes.Buffer
	buf.WriteString("<< /Length ")
	buf.WriteString(strconv.Itoa(len(data)))
	if extra != "" {
		buf.WriteString(" ")
		buf.WriteString(extra)
	}
	buf.WriteString(" >>\nstream\n")
	buf.Write(data)
	buf.WriteString("\nendstream")
	return b.add(buf.Bytes())
}

// maxID is the largest object id the update touches.
func (b *builder) maxID() int {
	m := 0
	for _, o := range b.objs {
		if o.id > m {
			m = o.id
		}
	}
	return m
}

// ── serialising values read back from the document ─────────────────────

// isRef reports whether v was reached through an indirect reference from
// an object whose id is ownerID. digitorus/pdf hands a direct value the
// *containing* object's pointer, so "its pointer is not the owner's" is
// exactly "it is its own object".
func isRef(v pdf.Value, ownerID int) bool {
	id := int(v.GetPtr().GetID())
	return id != 0 && id != ownerID
}

func refOf(v pdf.Value) string {
	p := v.GetPtr()
	return fmt.Sprintf("%d %d R", p.GetID(), p.GetGen())
}

// serialise writes a value back as PDF source. A value that is its own
// object (or any stream) is written as a reference so the update never
// inlines half the document; everything else is written by the parser's
// own formatter, which leaves nested references unresolved.
func serialise(v pdf.Value, ownerID int) string {
	if v.Kind() == pdf.Stream || isRef(v, ownerID) {
		return refOf(v)
	}
	if v.Kind() == pdf.String {
		return pdfString(v.RawString())
	}
	return v.String()
}

// pdfString writes a literal string with the three characters that can
// end it escaped, and falls back to hex for anything non-printable.
func pdfString(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return hexString(s)
		}
	}
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return "(" + r.Replace(s) + ")"
}

func hexString(s string) string {
	var b strings.Builder
	b.WriteByte('<')
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, "%02X", s[i])
	}
	b.WriteByte('>')
	return b.String()
}

// ── the incremental update ─────────────────────────────────────────────

// trailerFacts are what the new trailer has to carry over.
type trailerFacts struct {
	root string // "12 0 R"
	info string // "" when the file has none
	id   string // "[<..> <..>]", "" when the file has none
	size int
	prev int
	strm bool // the base file's last cross-reference is a stream
}

func readTrailer(base []byte, rdr *pdf.Reader) (trailerFacts, error) {
	t := rdr.Trailer()
	root := t.Key("Root")
	if root.IsNull() {
		return trailerFacts{}, fmt.Errorf("pdfdoc: the document has no catalogue")
	}
	f := trailerFacts{root: refOf(root), size: int(t.Key("Size").Int64())}
	if n := len(rdr.Xref()); n > f.size {
		f.size = n
	}
	if info := t.Key("Info"); !info.IsNull() && info.GetPtr().GetID() != 0 {
		f.info = refOf(info)
	}
	if ids := t.Key("ID"); ids.Kind() == pdf.Array && ids.Len() == 2 {
		f.id = "[" + hexString(ids.Index(0).RawString()) + " " + hexString(ids.Index(1).RawString()) + "]"
	}
	prev, isStream, err := lastXref(base)
	if err != nil {
		return trailerFacts{}, err
	}
	f.prev, f.strm = prev, isStream
	return f, nil
}

// lastXref finds the offset the base file's startxref names and whether
// what sits there is a cross-reference stream rather than a table. An
// update has to answer in the same dialect: a classic table appended to
// an xref-stream document is a hybrid no reader promises to follow.
func lastXref(base []byte) (int, bool, error) {
	tail := base
	if len(tail) > 2048 {
		tail = tail[len(tail)-2048:]
	}
	i := bytes.LastIndex(tail, []byte("startxref"))
	if i < 0 {
		return 0, false, fmt.Errorf("pdfdoc: the document has no startxref")
	}
	rest := strings.TrimLeft(string(tail[i+len("startxref"):]), " \r\n\t")
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	off, err := strconv.Atoi(rest[:end])
	if err != nil || off <= 0 || off >= len(base) {
		return 0, false, fmt.Errorf("pdfdoc: the document's startxref does not point into the file")
	}
	at := bytes.TrimLeft(base[off:], " \r\n\t")
	return off, !bytes.HasPrefix(at, []byte("xref")), nil
}

// appendUpdate writes base plus the builder's objects and a cross
// reference in the base file's own dialect.
func appendUpdate(base []byte, b *builder, t trailerFacts) ([]byte, error) {
	if len(b.objs) == 0 {
		return base, nil
	}
	var out bytes.Buffer
	out.Grow(len(base) + 4096)
	out.Write(base)
	if n := out.Len(); n > 0 && base[n-1] != '\n' {
		out.WriteByte('\n')
	}

	objs := append([]object(nil), b.objs...)
	sort.SliceStable(objs, func(i, j int) bool { return objs[i].id < objs[j].id })
	offsets := make(map[int]int, len(objs))

	write := func(o object) {
		offsets[o.id] = out.Len()
		fmt.Fprintf(&out, "%d %d obj\n", o.id, o.gen)
		out.Write(o.body)
		out.WriteString("\nendobj\n")
	}
	for _, o := range objs {
		write(o)
	}

	size := b.maxID() + 1
	if t.size > size {
		size = t.size
	}
	if t.strm {
		// The stream is itself an object, and its own entry has to be in it.
		xrefID := b.next
		b.next++
		size = max(size, xrefID+1)
		entries := xrefEntries(objs, xrefID)
		start := out.Len()
		offsets[xrefID] = start
		data := xrefStreamData(entries, offsets)
		dict := fmt.Sprintf("/Type /XRef /Size %d /Index [%s] /W [1 4 2] /Root %s /Prev %d",
			size, indexOf(entries), t.root, t.prev)
		if t.info != "" {
			dict += " /Info " + t.info
		}
		if t.id != "" {
			dict += " /ID " + t.id
		}
		fmt.Fprintf(&out, "%d 0 obj\n<< %s /Length %d >>\nstream\n", xrefID, dict, len(data))
		out.Write(data)
		out.WriteString("\nendstream\nendobj\n")
		fmt.Fprintf(&out, "startxref\n%d\n%%%%EOF\n", start)
		return out.Bytes(), nil
	}

	start := out.Len()
	out.WriteString("xref\n")
	for _, run := range runs(objs) {
		fmt.Fprintf(&out, "%d %d\n", run[0].id, len(run))
		for _, o := range run {
			fmt.Fprintf(&out, "%010d %05d n \n", offsets[o.id], o.gen)
		}
	}
	trailer := fmt.Sprintf("<< /Size %d /Root %s /Prev %d", size, t.root, t.prev)
	if t.info != "" {
		trailer += " /Info " + t.info
	}
	if t.id != "" {
		trailer += " /ID " + t.id
	}
	trailer += " >>"
	fmt.Fprintf(&out, "trailer\n%s\nstartxref\n%d\n%%%%EOF\n", trailer, start)
	return out.Bytes(), nil
}

// runs groups the objects into the contiguous id ranges an xref
// subsection describes.
func runs(objs []object) [][]object {
	var out [][]object
	for _, o := range objs {
		if n := len(out); n > 0 {
			last := out[n-1]
			if last[len(last)-1].id+1 == o.id {
				out[n-1] = append(last, o)
				continue
			}
		}
		out = append(out, []object{o})
	}
	return out
}

func xrefEntries(objs []object, xrefID int) []object {
	all := append([]object(nil), objs...)
	all = append(all, object{id: xrefID})
	sort.SliceStable(all, func(i, j int) bool { return all[i].id < all[j].id })
	return all
}

func indexOf(entries []object) string {
	var parts []string
	for _, run := range runs(entries) {
		parts = append(parts, fmt.Sprintf("%d %d", run[0].id, len(run)))
	}
	return strings.Join(parts, " ")
}

// xrefStreamData writes the [type][offset:4][gen:2] rows uncompressed.
func xrefStreamData(entries []object, offsets map[int]int) []byte {
	var buf bytes.Buffer
	for _, e := range entries {
		off := offsets[e.id]
		buf.WriteByte(1)
		buf.WriteByte(byte(off >> 24))
		buf.WriteByte(byte(off >> 16))
		buf.WriteByte(byte(off >> 8))
		buf.WriteByte(byte(off))
		buf.WriteByte(byte(e.gen >> 8))
		buf.WriteByte(byte(e.gen))
	}
	return buf.Bytes()
}
