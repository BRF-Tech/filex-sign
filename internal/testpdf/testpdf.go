// Package testpdf writes tiny, valid, uncompressed PDFs for tests: one or
// more pages with a MediaBox, an optional CropBox and /Rotate, a line of
// text each, and a classic xref table. It is not imported by the plugin.
package testpdf

import (
	"bytes"
	"fmt"
	"strings"
)

// Page describes one page to emit.
type Page struct {
	Width, Height float64
	CropBox       *[4]float64
	Rotate        int
	Text          string
}

// Options for Build.
type Options struct {
	Pages   []Page
	Encrypt bool // emit a (bogus) /Encrypt entry in the trailer
	XFA     bool // emit an AcroForm with an /XFA entry
	// XRefStream writes a PDF 1.5 cross-reference stream instead of a
	// classic table, so an incremental update has to answer in the same
	// dialect.
	XRefStream bool
	// SharedResources puts the pages' /Resources on the shared /Pages node
	// instead of each page, which is the inherited case a stamper has to
	// merge rather than edit.
	SharedResources bool
}

// Letter is a plain 612×792 page.
func Letter() Page { return Page{Width: 612, Height: 792, Text: "Hello"} }

// Build returns the PDF bytes.
func Build(o Options) []byte {
	if len(o.Pages) == 0 {
		o.Pages = []Page{Letter()}
	}
	var objs [][]byte
	add := func(s string) int {
		objs = append(objs, []byte(s))
		return len(objs)
	}
	// 1 catalog, 2 pages, then per page: page obj + content obj, then font
	catalogID := add("")
	pagesID := add("")
	fontID := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	var kids []string
	for _, p := range o.Pages {
		content := fmt.Sprintf("BT /F1 24 Tf 72 %.0f Td (%s) Tj ET", p.Height-100, p.Text)
		contentID := add(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
		extra := ""
		if p.CropBox != nil {
			extra += fmt.Sprintf(" /CropBox [%g %g %g %g]", p.CropBox[0], p.CropBox[1], p.CropBox[2], p.CropBox[3])
		}
		if p.Rotate != 0 {
			extra += fmt.Sprintf(" /Rotate %d", p.Rotate)
		}
		res := fmt.Sprintf(" /Resources << /Font << /F1 %d 0 R >> >>", fontID)
		if o.SharedResources {
			res = ""
		}
		pageID := add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %g %g]%s%s /Contents %d 0 R >>",
			pagesID, p.Width, p.Height, extra, res, contentID))
		kids = append(kids, fmt.Sprintf("%d 0 R", pageID))
	}
	shared := ""
	if o.SharedResources {
		shared = fmt.Sprintf(" /Resources << /Font << /F1 %d 0 R >> >>", fontID)
	}
	objs[pagesID-1] = []byte(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d%s >>", joinSpace(kids), len(kids), shared))
	acro := ""
	if o.XFA {
		xfaID := add("<< /Length 5 >>\nstream\n<xdp>\nendstream")
		acro = fmt.Sprintf(" /AcroForm << /Fields [] /XFA %d 0 R >>", xfaID)
	}
	objs[catalogID-1] = []byte(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R%s >>", pagesID, acro))

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	enc := ""
	if o.Encrypt {
		enc = " /Encrypt << /Filter /Standard /V 1 /R 2 /Length 40 /P -1 /O <" + strings.Repeat("00", 32) + "> /U <" + strings.Repeat("00", 32) + "> >> /ID [<" + strings.Repeat("ab", 16) + "> <" + strings.Repeat("ab", 16) + ">]"
	}
	if o.XRefStream {
		xrefID := len(objs) + 1
		start := buf.Len()
		var data bytes.Buffer
		row := func(t byte, off, gen int) {
			data.WriteByte(t)
			data.WriteByte(byte(off >> 24))
			data.WriteByte(byte(off >> 16))
			data.WriteByte(byte(off >> 8))
			data.WriteByte(byte(off))
			data.WriteByte(byte(gen >> 8))
			data.WriteByte(byte(gen))
		}
		row(0, 0, 65535)
		for _, off := range offsets {
			row(1, off, 0)
		}
		row(1, start, 0)
		fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /XRef /Size %d /Index [0 %d] /W [1 4 2] /Root %d 0 R%s /Length %d >>\nstream\n",
			xrefID, xrefID+1, xrefID+1, catalogID, enc, data.Len())
		buf.Write(data.Bytes())
		buf.WriteString("\nendstream\nendobj\n")
		fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", start)
		return buf.Bytes()
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objs)+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root %d 0 R%s >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, catalogID, enc, xref)
	return buf.Bytes()
}

func joinSpace(parts []string) string {
	var b bytes.Buffer
	for i, p := range parts {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(p)
	}
	return b.String()
}
