package pdfdoc

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/brf-tech/filex-sign/internal/fontkit"
)

// A4 in points, and the margin the audit trail keeps.
const (
	auditW      = 595.28
	auditH      = 841.89
	auditMargin = 56.0
)

// AuditLine is one line of the trail. An empty Text leaves a gap.
type AuditLine struct {
	Text  string
	Size  float64 // 0 → 10
	Head  bool    // drawn larger, for a section
	Muted bool    // grey
}

// Audit builds a standalone PDF that records how a signature request
// went: who was asked, what they did and when. It is written from
// scratch (no incremental update) with the same embedded faces the
// stamper uses, so Turkish names read correctly wherever it is opened.
func Audit(title string, lines []AuditLine) ([]byte, error) {
	face := fontkit.Get(fontkit.Inter)
	fonts := newFontSet()

	type pageContent struct{ ops bytes.Buffer }
	var pages []*pageContent
	cur := &pageContent{}
	pages = append(pages, cur)
	y := auditH - auditMargin

	write := func(s string, size float64, grey bool) error {
		if y < auditMargin+size {
			cur = &pageContent{}
			pages = append(pages, cur)
			y = auditH - auditMargin
		}
		y -= size * 1.35
		if strings.TrimSpace(s) == "" {
			return nil
		}
		runs, err := fontkit.ShapeRuns(face, s)
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			return nil
		}
		shade := 0.15
		if grey {
			shade = 0.45
		}
		textOps(&cur.ops, fonts, runs, size, auditMargin, y, [3]float64{shade, shade, shade})
		return nil
	}

	if err := write(title, 16, false); err != nil {
		return nil, err
	}
	if err := write("", 6, false); err != nil {
		return nil, err
	}
	for _, l := range lines {
		size := l.Size
		if size == 0 {
			size = 10
		}
		if l.Head {
			size = 12
			if err := write("", 4, false); err != nil {
				return nil, err
			}
		}
		for _, part := range wrap(face, l.Text, auditW-2*auditMargin, size) {
			if err := write(part, size, l.Muted); err != nil {
				return nil, err
			}
		}
	}

	b := newBuilder(1)
	catalog := b.add(nil) // reserved, filled in below
	pagesObj := b.add(nil)
	fonts.emit(b)
	var kids []string
	for _, p := range pages {
		content := b.addStream("", p.ops.Bytes())
		var res bytes.Buffer
		res.WriteString("<< /Font <<")
		writeStampFonts(&res, fonts)
		res.WriteString(" >> >>")
		id := b.add([]byte(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %.2f %.2f] /Resources %s /Contents %d 0 R >>",
			pagesObj, auditW, auditH, res.String(), content)))
		kids = append(kids, fmt.Sprintf("%d 0 R", id))
	}
	b.objs[0].body = []byte(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesObj))
	b.objs[1].body = []byte(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids)))
	return writeDocument(b, catalog), nil
}

// wrap breaks a line on spaces so a long note still fits the page.
func wrap(face *fontkit.Face, s string, width, size float64) []string {
	if strings.TrimSpace(s) == "" {
		return []string{""}
	}
	if face.Width(s, size) <= width {
		return []string{s}
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		probe := word
		if line != "" {
			probe = line + " " + word
		}
		if face.Width(probe, size) <= width || line == "" {
			line = probe
			continue
		}
		out = append(out, line)
		line = word
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// writeDocument serialises a whole new file: header, objects, a classic
// cross-reference table and a trailer.
func writeDocument(b *builder, rootID int) []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(b.objs)+1)
	for _, o := range b.objs {
		offsets[o.id] = out.Len()
		fmt.Fprintf(&out, "%d %d obj\n", o.id, o.gen)
		out.Write(o.body)
		out.WriteString("\nendobj\n")
	}
	start := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(b.objs)+1)
	for id := 1; id <= len(b.objs); id++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[id])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(b.objs)+1, rootID, start)
	return out.Bytes()
}
