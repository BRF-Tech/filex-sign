package pdfsig

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"time"

	"github.com/digitorus/pkcs7"
	"github.com/digitorus/timestamp"
	"golang.org/x/crypto/cryptobyte"
	cbasn1 "golang.org/x/crypto/cryptobyte/asn1"

	"github.com/brf-tech/filex-sign/internal/pdfdoc"
	"github.com/brf-tech/filex-sign/internal/stamp"
)

// Options for one signature.
type Options struct {
	Signer      crypto.Signer
	Cert        *x509.Certificate
	Chain       []*x509.Certificate // issuer(s), embedded in the CMS so readers see the path
	Name        string
	Reason      string
	Location    string
	ContactInfo string
	When        time.Time

	// Field is the /Sig field the signature goes into. It must exist and be
	// empty (pdfdoc.PrepareForm creates them, KindSig).
	Field string
	// Image is the widget's appearance (Compose); nil = an invisible field.
	Image  []byte
	Rotate int
	// Certify makes this the document's certification signature with the
	// DocMDP permission P (2: filling in forms and signing only). 0 is an
	// approval signature. A field with a /Lock is honoured either way.
	Certify int

	// TSAURL asks an RFC 3161 time-stamping authority to date the
	// signature, so "when" stops being the signer's own claim. The
	// request carries the signature's DIGEST and a nonce — never the
	// document — so nothing of the content leaves the installation.
	// Empty leaves the signature untimestamped, which is the default:
	// a stamp is a network call in the middle of signing.
	TSAURL string
}

// reserve is the room kept for the CMS: the signer's certificate and the
// authority's (~1.5 KB for P-256), the signed attributes, and — when asked
// for — an RFC 3161 token, which carries the authority's own chain.
func (o Options) reserve() int {
	n := 8 << 10
	if o.TSAURL != "" {
		n += 12 << 10
	}
	return n
}

// Sign writes one PAdES-B (ETSI.CAdES.detached, sha256) signature into
// o.Field as an incremental update: the original bytes are copied
// unchanged, so every earlier signature stays verifiable, and the
// original is recoverable by truncating at its first %%EOF.
//
// The signer is asked for exactly one signature over the CMS signed
// attributes' sha256 digest — the host's HostSigner and a local ECDSA key
// behave the same.
func Sign(in []byte, o Options) ([]byte, error) {
	if o.Signer == nil || o.Cert == nil {
		return nil, errors.New("pdfsig: signer and certificate are required")
	}
	if o.Field == "" {
		return nil, errors.New("pdfsig: no signature field was named")
	}
	when := o.When
	if when.IsZero() {
		when = time.Now()
	}
	prep, err := pdfdoc.PrepareSignature(in, pdfdoc.SigRequest{
		Field: o.Field, Name: o.Name, Reason: o.Reason, Location: o.Location, ContactInfo: o.ContactInfo,
		When: when, Certify: o.Certify, Image: o.Image, Rotate: o.Rotate, Reserve: o.reserve(),
	})
	if err != nil {
		return nil, fmt.Errorf("pdfsig: %w", err)
	}
	cms, err := buildCMS(prep.Content(), o)
	if err != nil {
		return nil, fmt.Errorf("pdfsig: %w", err)
	}
	out, err := prep.Embed(cms)
	if err != nil {
		return nil, fmt.Errorf("pdfsig: %w", err)
	}
	return out, nil
}

// buildCMS is the detached CAdES signature over content: sha256, the
// ESS signing-certificate-v2 attribute PAdES requires, NO signing-time
// attribute (PAdES keeps the claimed time in /M), the chain embedded, and
// — when asked for — an RFC 3161 token over the signature value.
func buildCMS(content []byte, o Options) ([]byte, error) {
	sd, err := pkcs7.NewSignedData(content)
	if err != nil {
		return nil, fmt.Errorf("signed data: %w", err)
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	attr, err := signingCertificateV2(o.Cert)
	if err != nil {
		return nil, err
	}
	if err := sd.AddSignerChain(o.Cert, o.Signer, o.Chain, pkcs7.SignerInfoConfig{
		ExtraSignedAttributes: []pkcs7.Attribute{attr},
		SkipSigningTime:       true,
	}); err != nil {
		return nil, fmt.Errorf("signer: %w", err)
	}
	sd.Detach()
	if o.TSAURL != "" {
		info := &sd.GetSignedData().SignerInfos[0]
		token, err := timestampToken(o.TSAURL, info.EncryptedDigest)
		if err != nil {
			return nil, fmt.Errorf("time stamp: %w", err)
		}
		if err := info.SetUnauthenticatedAttributes([]pkcs7.Attribute{{
			Type:  asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 14},
			Value: asn1.RawValue{FullBytes: token},
		}}); err != nil {
			return nil, err
		}
	}
	return sd.Finish()
}

// signingCertificateV2 is ESS SigningCertificateV2 (RFC 5035) for a sha256
// certificate hash: the attribute that binds the signature to exactly this
// certificate, which PAdES requires.
func signingCertificateV2(cert *x509.Certificate) (pkcs7.Attribute, error) {
	sum := sha256.Sum256(cert.Raw)
	var b cryptobyte.Builder
	b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) { // SigningCertificateV2
		b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) { // certs
			// ESSCertIDv2; sha256 is the DEFAULT hash, which DER forbids
			// encoding, so there is no AlgorithmIdentifier.
			b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) {
				b.AddASN1OctetString(sum[:])
				b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) { // IssuerSerial
					b.AddASN1(cbasn1.SEQUENCE, func(b *cryptobyte.Builder) { // GeneralNames
						b.AddASN1(cbasn1.Tag(4).Constructed().ContextSpecific(), func(b *cryptobyte.Builder) {
							b.AddBytes(cert.RawIssuer)
						})
					})
					b.AddASN1BigInt(cert.SerialNumber)
				})
			})
		})
	})
	der, err := b.Bytes()
	if err != nil {
		return pkcs7.Attribute{}, err
	}
	return pkcs7.Attribute{
		Type:  asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 47},
		Value: asn1.RawValue{FullBytes: der},
	}, nil
}

// timestampToken asks an RFC 3161 authority for a token over data's
// sha256. The request goes through http.DefaultTransport, which the
// plugin replaces with the host's (hostnet): the host's grant decides.
func timestampToken(url string, data []byte) ([]byte, error) {
	nonce, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	req, err := timestamp.CreateRequest(bytes.NewReader(data), &timestamp.RequestOptions{Hash: crypto.SHA256, Certificates: true, Nonce: nonce})
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(req))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/timestamp-query")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("the authority answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	ts, err := timestamp.ParseResponse(body)
	if err != nil {
		return nil, err
	}
	if _, err := pkcs7.Parse(ts.RawToken); err != nil {
		return nil, err
	}
	return ts.RawToken, nil
}

// DateLine is the date line printed under a signature. ⚠ Worded in ONE
// place (stamp.DateLine), which the previews use too.
func DateLine(when time.Time, locale string) string { return stamp.DateLine(when, locale) }
