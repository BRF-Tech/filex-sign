package app

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/digitorus/pdf"

	"github.com/brf-tech/filex-sign/internal/verify"
)

// rewriteFirstPage appends, after everything, what any PDF tool writes to
// change what a page shows without touching the page itself: a new
// definition of page 1's content stream (the old drawing plus "PAID IN
// FULL"), a classic cross-reference section for that one object, and a
// trailer that points back at the file's last one. Every signed byte stays
// as it was.
func rewriteFirstPage(t *testing.T, doc []byte) []byte {
	t.Helper()
	rdr, err := pdf.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		t.Fatal(err)
	}
	contents := rdr.Page(1).V.Key("Contents")
	if contents.Kind() == pdf.Array {
		contents = contents.Index(0)
	}
	id := int(contents.GetPtr().GetID())
	if id == 0 {
		t.Fatal("page 1 draws nothing of its own to rewrite")
	}
	old, err := readAll(contents)
	if err != nil {
		t.Fatal(err)
	}
	body := append(append([]byte{}, old...), []byte("\nBT /Helvetica 28 Tf 72 400 Td (PAID IN FULL) Tj ET")...)
	prev := regexp.MustCompile(`startxref\s+(\d+)`).FindAllSubmatch(doc, -1)
	last, _ := strconv.Atoi(string(prev[len(prev)-1][1]))
	var out bytes.Buffer
	out.Write(doc)
	if !bytes.HasSuffix(doc, []byte("\n")) {
		out.WriteByte('\n')
	}
	at := out.Len()
	fmt.Fprintf(&out, "%d 0 obj\n<< /Length %d >>\nstream\n", id, len(body))
	out.Write(body)
	out.WriteString("\nendstream\nendobj\n")
	xref := out.Len()
	root := rdr.Trailer().Key("Root").GetPtr()
	fmt.Fprintf(&out, "xref\n%d 1\n%010d 00000 n \ntrailer\n<< /Size %d /Root %d %d R /Prev %d >>\nstartxref\n%d\n%%%%EOF\n",
		id, at, rdr.Trailer().Key("Size").Int64(), root.GetID(), root.GetGen(), last, xref)
	return out.Bytes()
}

func readAll(v pdf.Value) ([]byte, error) {
	r := v.Reader()
	defer r.Close()
	var b bytes.Buffer
	_, err := b.ReadFrom(r)
	return b.Bytes(), err
}

// ⚠⚠ Measured in a browser, 2026-09-22 (e2e 101): the signed file with page
// 1's content stream rewritten in a later update — the page dictionary
// itself untouched — was reported as "object 5 was changed" instead of
// "page 1 draws something different", and the CERTIFICATION as "the
// signature does not match the bytes it covers", with its certificate "not
// stated". Neither was true: the change is to what page 1 draws, and the
// certification's bytes are intact — digitorus/pdfsign's own DocMDP check
// returns before it even parses the signature. What a reader must be told
// is that the signature is intact and the change after it is not
// permitted.
func TestVerify_APageRewrittenAfterTheSealIsNamedAndTheSignaturesStayIntact(t *testing.T) {
	a, f := newApp(t)
	final := completeTwoSigners(t, a, f, nil, nil)
	edited := rewriteFirstPage(t, final)

	rep, err := verify.Inspect(edited, bundle(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.NotPermitted {
		t.Fatal("a page redrawn after the seal must be reported as not permitted")
	}
	for _, s := range rep.Signatures {
		if !s.Valid || s.Cert.FP == "" {
			t.Errorf("signature %d: its bytes are intact and its certificate is in the file — valid=%v cert=%q errors=%v",
				s.Index, s.Valid, s.Cert.FP, s.Errors)
		}
		if s.After.Permitted {
			t.Errorf("signature %d: the change after it is not permitted", s.Index)
		}
		for _, v := range s.After.Violations {
			if v.Kind != "page_content" || v.Page != 1 {
				t.Errorf("signature %d: the change is to what page 1 draws, not %+v", s.Index, v)
			}
		}
	}
	// ⚠ Seen on the same screen: the seal carried /Name (filex) while its
	// certificate says "filex document seal", so Verify warned about our
	// own seal — "the certificate says otherwise".
	if seal := rep.Signatures[len(rep.Signatures)-1]; seal.NameConflict {
		t.Errorf("the seal names itself %q, its certificate %q", seal.DeclaredName, seal.Cert.Subject)
	}
	if strings.Contains(rep.Err, "DocMDP") {
		t.Errorf("the library's refusal is ours to say in words, not to pass on: %q", rep.Err)
	}

	f.Inputs["in:0"] = edited
	s, err := a.viewVerify(viewInput(ViewVerify, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	words := surfaceWords(s)
	for _, want := range []string{"do NOT permit", "Not permitted by the certification: page 1 draws something different",
		"Not permitted by filex's seal: page 1 draws something different", "intact, but changes it does not permit were made afterwards",
		"Every signature itself is intact"} {
		if !strings.Contains(words, want) {
			t.Errorf("the report does not say %q", want)
		}
	}
	for _, wrong := range []string{"object 5 was changed", "does not match the bytes it covers", "Every signature is valid", "DocMDP validation failed"} {
		if strings.Contains(words, wrong) {
			t.Errorf("the report says %q, which is not what happened", wrong)
		}
	}
}
