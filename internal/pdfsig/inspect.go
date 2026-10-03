// Package pdfsig is the signing pipeline: intake checks on the PDF, the
// visible appearance (the signer's drawing plus name and date, composed
// into one image), and the PAdES-B signature itself through
// digitorus/pdfsign with whatever crypto.Signer it is handed — the host's
// HostSigner in production, a throw-away key in tests.
package pdfsig

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/digitorus/pdf"

	"github.com/brf-tech/filex-sign/internal/geometry"
)

// Intake refusals, each with words the person can act on.
var (
	ErrNotPDF    = errors.New("not a PDF file")
	ErrEncrypted = errors.New("the PDF is encrypted; remove the password first")
	ErrXFA       = errors.New("XFA forms cannot be signed; flatten the form to a plain PDF first")
	ErrNoPages   = errors.New("the PDF has no pages")
)

// PageInfo is what geometry needs about one page.
type PageInfo struct {
	Box geometry.Box
}

// Info is the intake summary.
type Info struct {
	Pages      []PageInfo
	Signatures int // signature fields that already hold a value
}

// Inspect parses the document and refuses what the pipeline cannot sign.
func Inspect(b []byte) (*Info, error) {
	if !bytes.HasPrefix(bytes.TrimLeft(b, "\xef\xbb\xbf \r\n\t"), []byte("%PDF-")) {
		return nil, ErrNotPDF
	}
	rdr, err := pdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "encrypt") || trailerHasEncrypt(b) {
			return nil, ErrEncrypted
		}
		return nil, fmt.Errorf("%w: %v", ErrNotPDF, err)
	}
	if !rdr.Trailer().Key("Encrypt").IsNull() {
		return nil, ErrEncrypted
	}
	root := rdr.Trailer().Key("Root")
	acro := root.Key("AcroForm")
	if !acro.IsNull() && !acro.Key("XFA").IsNull() {
		return nil, ErrXFA
	}
	n := rdr.NumPage()
	if n < 1 {
		return nil, ErrNoPages
	}
	info := &Info{}
	for i := 1; i <= n; i++ {
		p := rdr.Page(i)
		if p.V.IsNull() {
			return nil, fmt.Errorf("page %d is unreadable", i)
		}
		info.Pages = append(info.Pages, PageInfo{Box: pageBox(p.V)})
	}
	info.Signatures = countSignatures(acro.Key("Fields"), 0)
	return info, nil
}

// trailerHasEncrypt scans the file's last revision for an /Encrypt entry;
// the parser refuses some encrypted files with an unrelated message, and the
// person deserves the right words.
func trailerHasEncrypt(b []byte) bool {
	tail := b
	if len(tail) > 4096 {
		tail = tail[len(tail)-4096:]
	}
	i := bytes.LastIndex(tail, []byte("trailer"))
	if i < 0 {
		i = bytes.LastIndex(tail, []byte("/Type/XRef"))
		if i < 0 {
			i = bytes.LastIndex(tail, []byte("/Type /XRef"))
		}
	}
	if i < 0 {
		return false
	}
	return bytes.Contains(tail[i:], []byte("/Encrypt"))
}

// inherited walks the page's /Parent chain for a key (digitorus/pdf keeps
// its own helper unexported and its box helpers commented out).
func inherited(v pdf.Value, key string) pdf.Value {
	for depth := 0; !v.IsNull() && depth < 64; depth++ {
		if r := v.Key(key); !r.IsNull() {
			return r
		}
		v = v.Key("Parent")
	}
	return pdf.Value{}
}

func pageBox(page pdf.Value) geometry.Box {
	box := inherited(page, "CropBox")
	if box.IsNull() || box.Len() != 4 {
		box = inherited(page, "MediaBox")
	}
	b := geometry.Box{X0: 0, Y0: 0, X1: 612, Y1: 792}
	if !box.IsNull() && box.Len() == 4 {
		b = geometry.Box{X0: box.Index(0).Float64(), Y0: box.Index(1).Float64(), X1: box.Index(2).Float64(), Y1: box.Index(3).Float64()}
	}
	if r := inherited(page, "Rotate"); !r.IsNull() {
		b.Rotate = int(r.Int64())
	}
	return b.Normalized()
}

func countSignatures(fields pdf.Value, depth int) int {
	if fields.IsNull() || depth > 8 {
		return 0
	}
	n := 0
	for i := 0; i < fields.Len(); i++ {
		f := fields.Index(i)
		if f.Key("FT").Name() == "Sig" && !f.Key("V").IsNull() {
			n++
		}
		n += countSignatures(f.Key("Kids"), depth+1)
	}
	return n
}

// Box returns the box of a 1-based page, or an error.
func (i *Info) Box(page int) (geometry.Box, error) {
	if page < 1 || page > len(i.Pages) {
		return geometry.Box{}, fmt.Errorf("page %d is out of range (1-%d)", page, len(i.Pages))
	}
	return i.Pages[page-1].Box, nil
}
