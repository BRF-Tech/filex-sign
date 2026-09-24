package receipt

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/digitorus/pkcs7"

	"github.com/brf-tech/filex-sign/internal/testca"
)

func build(t *testing.T, locale string) (*Files, *testca.CA, *testca.Leaf) {
	t.Helper()
	ca, err := testca.New("sign")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.Issue("Gökçe", "gokce@example.com", 3650)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Build(Input{Document: "sözleşme.pdf", Identity: "Gökçe (gokce@example.com)",
		SignedAt: "2026-09-20", Leaf: leaf.Cert, Authority: ca.Cert, Locale: locale})
	if err != nil {
		t.Fatal(err)
	}
	return f, ca, leaf
}

// The bundle is certs ONLY: no signer, no content, nothing that signs.
func TestP7BCarriesBothCertificatesAndNoSigner(t *testing.T) {
	f, ca, leaf := build(t, "en")
	p7, err := pkcs7.Parse(f.P7B)
	if err != nil {
		t.Fatalf("the bundle is not readable as PKCS#7: %v", err)
	}
	if len(p7.Certificates) != 2 {
		t.Fatalf("the leaf and its authority, got %d certificates", len(p7.Certificates))
	}
	if len(p7.Signers) != 0 {
		t.Errorf("a receipt signs nothing, but it carries %d signer(s)", len(p7.Signers))
	}
	var haveLeaf, haveCA bool
	for _, c := range p7.Certificates {
		if c.Equal(leaf.Cert) {
			haveLeaf = true
		}
		if c.Equal(ca.Cert) {
			haveCA = true
		}
	}
	if !haveLeaf || !haveCA {
		t.Errorf("leaf: %v, authority: %v", haveLeaf, haveCA)
	}
}

func TestPEMIsTheSameTwoCertificates(t *testing.T) {
	f, ca, leaf := build(t, "en")
	var got []*x509.Certificate
	rest := f.PEM
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, c)
	}
	if len(got) != 2 || !got[0].Equal(leaf.Cert) || !got[1].Equal(ca.Cert) {
		t.Fatalf("expected the leaf then its authority, got %d", len(got))
	}
}

// ⚠ The sentence that keeps a "certificate" file from being read as a
// power to sign. It has to be there, in the reader's own language.
func TestTheSummarySaysWhatItIsNot(t *testing.T) {
	for _, c := range []struct{ locale, want string }{
		{"en", "identity receipt, not a signing capability"},
		{"tr", "kimlik makbuzudur, imza yeteneği değildir"},
	} {
		f, ca, leaf := build(t, c.locale)
		body := string(f.Text)
		if !strings.Contains(body, c.want) {
			t.Errorf("%s: the summary does not say %q:\n%s", c.locale, c.want, body)
		}
		for _, want := range []string{Fingerprint(leaf.Cert), Fingerprint(ca.Cert), "sözleşme.pdf", "Gökçe"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: the summary is missing %q", c.locale, want)
			}
		}
		if strings.Contains(body, "proven by a time-stamping authority") {
			t.Error("without a stamp the summary must not claim the time is proven")
		}
	}
}

func TestTimestampedSummarySaysSo(t *testing.T) {
	ca, err := testca.New("sign")
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := ca.Issue("Ali", "", 3650)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Build(Input{Document: "a.pdf", Identity: "Ali", SignedAt: "2026-09-20",
		Leaf: leaf.Cert, Authority: ca.Cert, Timestamped: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(f.Text), "proven by a time-stamping authority") {
		t.Error("a stamped signature should say its time is proven")
	}
	// No address: no empty half of an identity is printed.
	if strings.Contains(string(f.Text), "E-mail:") {
		t.Error("an identity with no address must not print an empty e-mail line")
	}
}

func TestFileNamesAreSafe(t *testing.T) {
	got := Stem("söz leşme.pdf", "Gökçe (gokce@example.com)")
	if strings.ContainsAny(got, "/\\:*?\"<>| ") {
		t.Errorf("a file name has to be a file name: %q", got)
	}
	if !strings.HasSuffix(got, "-receipt") {
		t.Errorf("the name should say what it is: %q", got)
	}
	if Stem("a.pdf", "") != "a-receipt" {
		t.Errorf("without an identity: %q", Stem("a.pdf", ""))
	}
}

func TestFingerprintIsReadable(t *testing.T) {
	_, ca, _ := build(t, "en")
	fp := Fingerprint(ca.Cert)
	if len(strings.Fields(fp)) != 16 {
		t.Errorf("a SHA-256 in groups of four: %q", fp)
	}
	if fp != strings.ToUpper(fp) {
		t.Errorf("one case only, so two screens can be compared: %q", fp)
	}
	if Fingerprint(nil) != "" {
		t.Error("no certificate, no fingerprint")
	}
}
