// Package testca is a throw-away certificate authority with the same shape
// as filex's per-tenant signing CA: ECDSA P-256, leaves with the
// document-signing EKU, e-mail SAN, OU = the plugin's name. The fake host
// (internal/host) and the signing tests use it; the plugin never does.
package testca

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"time"
)

// OIDDocumentSigning is id-kp-documentSigning (RFC 9336).
var OIDDocumentSigning = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 36}

// CA is the authority.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	OU   string
}

// New makes a CA valid for ten years, anchored on the wall clock.
func New(ou string) (*CA, error) { return NewAt(ou, time.Now()) }

// NewAt anchors the authority on a given moment.
//
// ⚠ A test harness that freezes the clock has to freeze the CA too. When
// the fake host signs at a fixed moment and the CA is issued at the wall
// clock, "was the signing moment inside the certificate's window?" is
// answered by what time it happens to be — a test that passes all morning
// and fails after lunch.
func NewAt(ou string, now time.Time) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "filex signing CA (test)", Organization: []string{"filex"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key, OU: ou}, nil
}

// Leaf is an issued signer certificate with its key.
type Leaf struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
}

// Signer is the key as a crypto.Signer (DER ECDSA over a sha256 digest,
// which is exactly what the host's host_sign answers).
func (l *Leaf) Signer() crypto.Signer { return l.Key }

// Issue mints a leaf like filex's cert_issue does, on the wall clock.
func (c *CA) Issue(commonName, email string, days int) (*Leaf, error) {
	return c.IssueAt(commonName, email, days, time.Now())
}

// IssueAt mints a leaf around a given moment (see NewAt).
func (c *CA) IssueAt(commonName, email string, days int, now time.Time) (*Leaf, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	if err != nil {
		return nil, err
	}
	if days <= 0 {
		days = 30
	}
	tpl := &x509.Certificate{
		SerialNumber:       serial,
		Subject:            pkix.Name{CommonName: commonName, Organization: []string{"filex"}, OrganizationalUnit: []string{c.OU}},
		NotBefore:          now.Add(-5 * time.Minute),
		NotAfter:           now.AddDate(0, 0, days),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		UnknownExtKeyUsage: []asn1.ObjectIdentifier{OIDDocumentSigning},
	}
	if email != "" {
		tpl.EmailAddresses = []string{email}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.Cert, &key.PublicKey, c.Key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Leaf{Cert: cert, Key: key}, nil
}

// PEM encodes a certificate.
func PEM(c *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
}

// Pool holds the CA as a trust root.
func (c *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c.Cert)
	return p
}
