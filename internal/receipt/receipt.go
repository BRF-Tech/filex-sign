// Package receipt builds what a signer is handed the moment their
// signature lands.
//
// ⚠ What it builds is an IDENTITY RECEIPT, not a signing capability.
// The private key that made the signature is destroyed by the job that
// used it, seconds after the signature is written, so the certificate in
// here can prove who signed and cannot sign anything new — not for the
// signer, not for the installation, not for anybody who steals the file.
// Every artefact says so in those words, because a file called
// "certificate" invites exactly the wrong assumption.
//
// Three files, all small, all offline:
//
//	.p7b  a certs-only PKCS#7 bundle (the leaf and the authority that
//	      issued it) — what Windows, Acrobat and openssl all import.
//	.pem  the same two certificates as text, for everything else.
//	.txt  the facts in words: who, what, when, the serial, and the two
//	      SHA-256 fingerprints to compare against a verification report.
package receipt

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	"github.com/digitorus/pkcs7"

	"github.com/brf-tech/filex-sign/internal/i18n"
)

// Input is one signature's receipt.
type Input struct {
	Document  string
	Identity  string // the signer as a line of text: name, address, or both
	SignedAt  string
	Leaf      *x509.Certificate
	Authority *x509.Certificate // the CA that issued the leaf; may be nil
	// Locale picks the language of the plain-text summary. Everything
	// else is language-free.
	Locale string
	// Timestamped says an RFC 3161 time stamp is embedded in the
	// signature, which changes what the summary may claim about "when".
	Timestamped bool
}

// Files is what the receipt consists of.
type Files struct {
	P7B  []byte
	PEM  []byte
	Text []byte
	// Names are the file names, derived from the document's own.
	P7BName, PEMName, TextName string
}

// Build makes the three files.
func Build(in Input) (*Files, error) {
	if in.Leaf == nil {
		return nil, errors.New("receipt: no certificate to put in it")
	}
	p7b, err := bundle(in.Leaf, in.Authority)
	if err != nil {
		return nil, err
	}
	stem := Stem(in.Document, in.Identity)
	return &Files{
		P7B: p7b, P7BName: stem + ".p7b",
		PEM: pemOf(in.Leaf, in.Authority), PEMName: stem + ".pem",
		Text: []byte(summary(in)), TextName: stem + ".txt",
	}, nil
}

// bundle writes a certs-only PKCS#7: a SignedData with no content and no
// signer, holding the certificates. That is the shape every certificate
// store expects from a ".p7b", and it carries no signature of its own,
// which is the point — there is nothing here that can sign.
func bundle(leaf, ca *x509.Certificate) ([]byte, error) {
	sd, err := pkcs7.NewSignedData(nil)
	if err != nil {
		return nil, fmt.Errorf("receipt: %w", err)
	}
	sd.AddCertificate(leaf)
	if ca != nil && !ca.Equal(leaf) {
		sd.AddCertificate(ca)
	}
	der, err := sd.Finish()
	if err != nil {
		return nil, fmt.Errorf("receipt: %w", err)
	}
	return der, nil
}

func pemOf(certs ...*x509.Certificate) []byte {
	var b bytes.Buffer
	for _, c := range certs {
		if c == nil {
			continue
		}
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	return b.Bytes()
}

// Fingerprint is the SHA-256 of a certificate, grouped so a person can
// read it off one screen and compare it with another.
func Fingerprint(c *x509.Certificate) string {
	if c == nil {
		return ""
	}
	sum := sha256.Sum256(c.Raw)
	h := hex.EncodeToString(sum[:])
	var b strings.Builder
	for i := 0; i < len(h); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strings.ToUpper(h[i:min(i+4, len(h))]))
	}
	return b.String()
}

// Stem is the base name the three files share.
func Stem(document, identity string) string {
	doc := safe(strings.TrimSuffix(document, ".pdf"))
	who := safe(identity)
	if who == "" {
		return doc + "-receipt"
	}
	return doc + "-" + who + "-receipt"
}

func safe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.' || r == '@':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// summary is the plain-text half: the facts, and the sentence that says
// what this file is and is not.
func summary(in Input) string {
	l := i18n.Of(in.Locale)
	var b strings.Builder
	line := func(en, tr string, args ...any) {
		b.WriteString(l.Sf(en, tr, args...) + "\n")
	}
	line("SIGNATURE RECEIPT", "İMZA MAKBUZU")
	line("=================", "==============")
	b.WriteString("\n")
	line("This is an identity receipt, not a signing capability.",
		"Bu bir kimlik makbuzudur, imza yeteneği değildir.")
	line("It proves who signed and what was signed. It cannot sign anything:",
		"Kimin neyi imzaladığını kanıtlar. Kendisiyle hiçbir şey imzalanamaz:")
	line("the private key that made the signature was destroyed the moment the",
		"imzayı üreten özel anahtar, imza yazılır yazılmaz yok edildi; onunla")
	line("signature was written, so nobody can sign something new with it.",
		"kimse yeni bir şey imzalayamaz.")
	b.WriteString("\n")
	line("Document: %s", "Belge: %s", in.Document)
	line("Identity: %s", "Kimlik: %s", in.Identity)
	if in.SignedAt != "" {
		if in.Timestamped {
			line("Signed:   %s (proven by a time-stamping authority)",
				"İmza:     %s (zaman damgası makamınca kanıtlandı)", in.SignedAt)
		} else {
			line("Signed:   %s (declared by the signer's own clock)",
				"İmza:     %s (imzacının kendi saatinin beyanı)", in.SignedAt)
		}
	}
	b.WriteString("\n")
	line("CERTIFICATE", "SERTİFİKA")
	line("Issued to:   %s", "Verilen:     %s", nameOf(in.Leaf))
	if len(in.Leaf.EmailAddresses) > 0 {
		line("E-mail:      %s", "E-posta:     %s", in.Leaf.EmailAddresses[0])
	}
	line("Serial:      %s", "Seri:        %s", strings.ToUpper(in.Leaf.SerialNumber.Text(16)))
	line("Valid:       %s to %s", "Geçerlilik:  %s - %s",
		in.Leaf.NotBefore.UTC().Format("2006-01-02"), in.Leaf.NotAfter.UTC().Format("2006-01-02"))
	line("SHA-256:     %s", "SHA-256:     %s", Fingerprint(in.Leaf))
	b.WriteString("\n")
	if in.Authority != nil {
		line("SIGNING AUTHORITY", "İMZA MAKAMI")
		line("Name:        %s", "Ad:          %s", nameOf(in.Authority))
		line("Valid:       %s to %s", "Geçerlilik:  %s - %s",
			in.Authority.NotBefore.UTC().Format("2006-01-02"), in.Authority.NotAfter.UTC().Format("2006-01-02"))
		line("SHA-256:     %s", "SHA-256:     %s", Fingerprint(in.Authority))
		b.WriteString("\n")
	}
	line("HOW TO CHECK IT", "NASIL DOĞRULANIR")
	line("1. Open the signed PDF in a reader that checks signatures.",
		"1. İmzalı PDF'i imza denetleyen bir okuyucuda açın.")
	line("2. Import the signing authority's certificate once (the .p7b or .pem",
		"2. İmza makamının sertifikasını bir kez içe aktarın (yanındaki .p7b ya da")
	line("   beside this file). The signature then shows as valid.",
		"   .pem dosyası). İmza bundan sonra geçerli görünür.")
	line("3. Compare the certificate SHA-256 above with the one the reader shows.",
		"3. Yukarıdaki sertifika SHA-256'sını okuyucunun gösterdiğiyle karşılaştırın.")
	b.WriteString("\n")
	line("The signature comes from this installation's own signing authority, not",
		"İmza, bu kurulumun kendi imza makamından gelir; kamuya açık bir kök")
	line("from a public root. A reader that has not imported that authority says",
		"makamdan değil. O makamı içe aktarmamış bir okuyucu \"geçerlilik bilinmiyor\"")
	line("the validity is unknown, which is not the same as invalid. Where the law",
		"der, ki bu \"geçersiz\" demek değildir. Kanunun nitelikli elektronik imza")
	line("asks for a qualified electronic signature, use e-imza or m-imza instead.",
		"istediği işlerde e-imza ya da m-imza kullanın.")
	return b.String()
}

func nameOf(c *x509.Certificate) string {
	if c == nil {
		return "-"
	}
	if cn := strings.TrimSpace(c.Subject.CommonName); cn != "" {
		return cn
	}
	if len(c.Subject.Organization) > 0 {
		return c.Subject.Organization[0]
	}
	return c.Subject.String()
}
