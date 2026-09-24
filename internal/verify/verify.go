// Package verify reads the signatures a PDF already carries and says,
// in plain facts, what each one proves.
//
// It is deliberately offline: no OCSP, no CRL, no clock but the
// document's own and the timestamp's. A plugin runs inside a sandbox
// with no network of its own, and a verification that silently depends
// on reaching a responder is a verification that fails on a train.
//
// Three things are computed here rather than taken from the library,
// because they are what a person actually asks:
//
//	Does the signature cover the WHOLE file? — from /ByteRange, which
//	  the library does not report.
//	What did the later updates do? — the revision each signature covered
//	  is a valid PDF of its own (truncate at the end of its byte range),
//	  so the page content of then and of now can simply be compared. A
//	  filled form field leaves it identical; a redrawn page does not.
//	Who is the signing authority, and what does the certificate say? —
//	  issuer, fingerprints, validity, key usage, and whether the signing
//	  moment fell inside the certificate's own window.
package verify

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/digitorus/pdf"
	pdfverify "github.com/digitorus/pdfsign/verify"
)

// What the later updates did to the pages a signature covered.
const (
	ChangedNothing = "none"    // nothing was appended at all
	ChangedFields  = "fields"  // objects were added, page content is identical
	ChangedContent = "content" // a page draws something else now
	ChangedUnknown = "unknown" // the earlier revision could not be re-read
)

// Cert is what one certificate of a chain says.
type Cert struct {
	Subject   string
	Issuer    string
	Email     string
	Serial    string
	FP        string
	NotBefore time.Time
	NotAfter  time.Time
	KeyUsage  []string
	EKU       []string
	IsCA      bool
}

// Signature is one signature's whole story.
type Signature struct {
	Index int

	// Who. Name comes from the CERTIFICATE when the two disagree: the
	// signature dictionary's /Name is typed by whoever signed, the
	// certificate is what the signing authority put its name behind.
	Name         string
	DeclaredName string
	NameConflict bool
	Email        string
	Reason       string
	Location     string

	// When. Proven only when an RFC 3161 timestamp says so; otherwise it
	// is the signer's own clock and is labelled as declared.
	When            time.Time
	TimeProven      bool
	TimeSource      string
	TimestampStatus string

	// What it covers.
	CoversWholeFile bool
	SignedBytes     int64
	FileBytes       int64
	LaterChanges    string

	// Whether it holds up.
	Valid     bool
	Trusted   bool
	Revoked   bool
	Algorithm string
	Warnings  []string
	Errors    []string

	// The certificate and the authority behind it.
	Cert           Cert
	Issuer         Cert
	HasIssuer      bool
	WithinValidity bool
	ChainRoot      string
	RootTrusted    bool
	IssuerOurs     bool

	// Field is the signature field's name.
	Field string
	// Certification is the DocMDP permission when this is the document's
	// certification signature (1 no change, 2 form filling and signing, 3 …
	// and annotations); 0 for an approval signature.
	Certification int
	// Lock is the permission its field's lock leaves after it (1 = the
	// whole document closed); 0 when the field has no lock.
	Lock int
	// Seal marks filex's own seal: the certificate is the installation's
	// "filex document seal", issued by one of this installation's
	// authorities.
	Seal bool
	// After is what the changes made after this signature did against the
	// permissions in force (the certification's, and any lock's).
	After Permissions
}

// SealCN is the common name of the platform seal's certificate.
const SealCN = "filex document seal"

// Report is the whole document.
type Report struct {
	FileBytes  int64
	Pages      int
	Signatures []Signature
	// SHA256 is the file's own hash (lower-case hex), what a party who was
	// sent the hash compares.
	SHA256 string
	// Certified is the certification's DocMDP permission (0 = none).
	Certified int
	// Sealed: filex's seal is the last signature and covers the whole file.
	Sealed bool
	// NotPermitted: a change was made that a certification or a lock did
	// not permit — what a reader reports as "changes not permitted".
	NotPermitted bool
	// Authorities is every CA certificate this instance signs or has
	// signed with — a rotated or replaced authority is retired, never
	// deleted, so an old signature stays checkable.
	Authorities []Cert
	Err         string
}

// Signed reports whether the document carries any signature at all.
func (r *Report) Signed() bool { return len(r.Signatures) > 0 }

// Inspect verifies every signature in doc against the instance's own CA
// bundle (`host_sign_info.ca_certs_pem`: every authority it has ever
// signed with, live one first).
func Inspect(doc []byte, caBundlePEM string) (*Report, error) {
	sum := sha256.Sum256(doc)
	rep := &Report{FileBytes: int64(len(doc)), SHA256: hex.EncodeToString(sum[:])}
	roots := x509.NewCertPool()
	ours := map[string]bool{}
	for _, c := range ParsePEM(caBundlePEM) {
		roots.AddCert(c)
		ours[fingerprint(c)] = true
		rep.Authorities = append(rep.Authorities, certOf(c))
	}

	ranges, pages, err := scan(doc)
	if err != nil {
		return nil, err
	}
	rep.Pages = pages
	if len(ranges) == 0 {
		return rep, nil
	}

	opts := &pdfverify.VerifyOptions{
		// The pool is THIS tenant's authorities and nothing else: a
		// signature is trusted because this installation stands behind it,
		// not because a public root does.
		TrustedRoots:                  roots,
		RequiredEKUs:                  []x509.ExtKeyUsage{x509.ExtKeyUsage(36)}, // document signing, RFC 9336
		AllowedEKUs:                   []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection, x509.ExtKeyUsageClientAuth},
		RequireDigitalSignatureKU:     true,
		AllowUntrustedRoots:           false,
		SkipRevocationCheck:           true,
		EnableExternalRevocationCheck: false,
		ValidateTimestampCertificates: true,
		// Without a timestamp the only time on offer is the signer's own
		// clock. Trust it for the certificate window — and say so in the
		// report, which is the honest version of what every reader does.
		TrustSignatureTime: true,
	}
	res, err := pdfverify.VerifyWithOptions(bytes.NewReader(doc), int64(len(doc)), opts)
	if err != nil {
		rep.Err = err.Error()
	}
	var signers []pdfverify.Signer
	if res != nil {
		if res.Error != "" && rep.Err == "" {
			rep.Err = res.Error
		}
		signers = res.Signers
	}
	if cleared := ownRevisions(doc, ranges, signers, opts); cleared != "" && rep.Err == cleared {
		rep.Err = ""
	}

	for i, br := range ranges {
		sig := Signature{Index: i + 1, FileBytes: int64(len(doc)), SignedBytes: br.end,
			CoversWholeFile: br.end == int64(len(doc)), LaterChanges: ChangedNothing,
			Field: br.field, Certification: br.certify, Lock: br.lock}
		if !sig.CoversWholeFile {
			sig.LaterChanges = laterChanges(doc, br.end)
		}
		if i < len(signers) {
			fill(&sig, &signers[i], ours)
		}
		sig.Seal = sig.Cert.Subject == SealCN && sig.IssuerOurs
		if sig.Certification > 0 && !br.inPerms {
			// A DocMDP reference the catalogue does not point at is not a
			// certification: no reader enforces it.
			sig.Certification = 0
		}
		rep.Signatures = append(rep.Signatures, sig)
	}
	permissions(doc, rep)
	return rep, nil
}

// docMDPRefusal is how digitorus/pdfsign begins the error it records when
// its own DocMDP check refuses a signature.
const docMDPRefusal = "DocMDP validation failed"

// ownRevisions verifies again, against its OWN revision, every signature
// pdfsign refused over DocMDP, and puts that result in its place. It
// returns the refusal it cleared ("" when none).
//
// ⚠⚠ Measured in a browser, 2026-09-22: a certified, sealed document with
// page 1 redrawn afterwards showed the CERTIFICATION as "the signature
// does not match the bytes it covers", its certificate "not stated". Not
// true: pdfsign's DocMDP check returns before it even parses the
// signature, so the signature was never checked at all. Checked against
// the bytes it signed — the file as it was when it was made, where there
// is nothing after it to refuse — it is intact, and it is the certificate
// in it that says who certified. Whether what came after was permitted is
// this package's own analysis (`permissions`), which names the change;
// the library's one-line refusal is not passed on.
func ownRevisions(doc []byte, ranges []byteRange, signers []pdfverify.Signer, opts *pdfverify.VerifyOptions) string {
	cleared := ""
	for i := range signers {
		if i >= len(ranges) || !refusedOverDocMDP(&signers[i]) {
			continue
		}
		end := ranges[i].end
		if end <= 0 || end > int64(len(doc)) {
			continue
		}
		res, err := pdfverify.VerifyWithOptions(bytes.NewReader(doc[:end]), end, opts)
		if err != nil || res == nil {
			continue
		}
		// In its own revision the signature is one of those signed by
		// then, in the same order as in the whole file.
		at := 0
		for j := 0; j < i; j++ {
			if ranges[j].end <= end {
				at++
			}
		}
		if at >= len(res.Signers) || refusedOverDocMDP(&res.Signers[at]) {
			continue
		}
		for _, e := range signers[i].ValidationErrors {
			if e != nil && strings.HasPrefix(e.Error(), docMDPRefusal) && cleared == "" {
				cleared = e.Error()
			}
		}
		signers[i] = res.Signers[at]
	}
	return cleared
}

func refusedOverDocMDP(s *pdfverify.Signer) bool {
	for _, e := range s.ValidationErrors {
		if e != nil && strings.HasPrefix(e.Error(), docMDPRefusal) {
			return true
		}
	}
	return false
}

// permissions reads every revision after every signature against the
// policy in force there — the certification's P, lowered by each lock —
// and says, per signature, whether what happened since was permitted.
func permissions(doc []byte, rep *Report) {
	n := len(rep.Signatures)
	if n == 0 {
		return
	}
	// One analysis per stretch between consecutive signatures, and after
	// the last one.
	segs := make([]segment, n)
	for i := range rep.Signatures {
		to := int64(len(doc))
		if i+1 < n {
			to = rep.Signatures[i+1].SignedBytes
		}
		segs[i] = analyse(doc, rep.Signatures[i].SignedBytes, to)
	}
	policy := 0
	lower := func(p int) {
		if p > 0 && (policy == 0 || p < policy) {
			policy = p
		}
	}
	for i := range rep.Signatures {
		s := &rep.Signatures[i]
		if s.Certification > 0 {
			rep.Certified = s.Certification
			lower(s.Certification)
		}
		lower(s.Lock)
		// The signature's OWN promise is what it is judged by: the
		// certification's P, or its lock's; an approval signature with no
		// lock is held to the policy in force when it was made.
		own := policy
		if s.Certification > 0 {
			own = s.Certification
		}
		if s.Lock > 0 && (own == 0 || s.Lock < own) {
			own = s.Lock
		}
		s.After = Permissions{Policy: own, Permitted: true, Analysed: true}
		for j := i; j < n; j++ {
			if !segs[j].ok {
				s.After.Analysed = false
				continue
			}
			if own > 0 && segs[j].level > allowed(own) {
				s.After.Permitted = false
				s.After.Violations = append(s.After.Violations, segs[j].violations...)
				if len(segs[j].violations) == 0 {
					s.After.Violations = append(s.After.Violations, Violation{Kind: "form"})
				}
			}
		}
		if !s.After.Permitted && (s.Certification > 0 || s.Lock > 0) {
			rep.NotPermitted = true
		}
	}
	last := rep.Signatures[n-1]
	rep.Sealed = last.Seal && last.CoversWholeFile && last.Valid
}

// allowed is the highest change level a permission lets through.
func allowed(p int) int {
	switch p {
	case 1:
		return levelNone
	case 2:
		return levelForm
	}
	return levelForm + 1
}

func fill(sig *Signature, s *pdfverify.Signer, ours map[string]bool) {
	sig.DeclaredName = strings.TrimSpace(s.Name)
	sig.Name = sig.DeclaredName
	sig.Reason = strings.TrimSpace(s.Reason)
	sig.Location = strings.TrimSpace(s.Location)
	sig.Email = strings.TrimSpace(s.ContactInfo)
	sig.Valid = s.ValidSignature
	sig.Trusted = s.TrustedIssuer
	sig.Revoked = s.RevokedCertificate
	sig.TimestampStatus = s.TimestampStatus
	sig.TimeSource = s.TimeSource
	for _, w := range s.Warnings {
		if w != nil {
			sig.Warnings = append(sig.Warnings, w.Error())
		}
	}
	for _, e := range s.ValidationErrors {
		if e != nil {
			sig.Errors = append(sig.Errors, e.Error())
		}
	}
	switch {
	case s.TimeStamp != nil && !s.TimeStamp.Time.IsZero():
		sig.When, sig.TimeProven = s.TimeStamp.Time, s.TimestampTrusted
	case s.SignatureTime != nil:
		sig.When = *s.SignatureTime
	case s.VerificationTime != nil:
		sig.When = *s.VerificationTime
	}
	if len(s.Certificates) == 0 {
		return
	}
	leaf := s.Certificates[0].Certificate
	if leaf == nil {
		return
	}
	sig.Cert = certOf(leaf)
	sig.Algorithm = leaf.SignatureAlgorithm.String()
	if cn := strings.TrimSpace(leaf.Subject.CommonName); cn != "" {
		// The certificate wins: /Name is typed by whoever signed.
		sig.NameConflict = sig.DeclaredName != "" && !strings.EqualFold(sig.DeclaredName, cn)
		sig.Name = cn
	}
	if sig.Email == "" && len(leaf.EmailAddresses) > 0 {
		sig.Email = leaf.EmailAddresses[0]
	}
	if !sig.When.IsZero() {
		sig.WithinValidity = !sig.When.Before(leaf.NotBefore) && !sig.When.After(leaf.NotAfter)
	}
	if len(s.Certificates) > 1 && s.Certificates[1].Certificate != nil {
		sig.Issuer = certOf(s.Certificates[1].Certificate)
		sig.HasIssuer = true
		sig.IssuerOurs = ours[sig.Issuer.FP]
	}
	root := s.Certificates[len(s.Certificates)-1].Certificate
	if root != nil {
		sig.ChainRoot = name(root.Subject.CommonName, root.Subject.Organization, root.Subject.String())
		sig.RootTrusted = ours[fingerprint(root)]
	}
}

// ── what the file itself says ──────────────────────────────────────────

// byteRange is one signature field's facts from the file itself.
type byteRange struct {
	end     int64
	field   string
	certify int  // DocMDP P in the signature's /Reference
	lock    int  // P from the field's /Lock, or its FieldMDP reference
	inPerms bool // the catalogue's /Perms /DocMDP points at this signature
}

// scan walks the form for signature fields and reads each one's
// /ByteRange, in the order the library walks them, so the coverage facts
// line up with the signers it reports.
func scan(doc []byte) ([]byteRange, int, error) {
	rdr, err := pdf.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return nil, 0, fmt.Errorf("verify: %w", err)
	}
	var out []byteRange
	root := rdr.Trailer().Key("Root")
	docmdp := int(root.Key("Perms").Key("DocMDP").GetPtr().GetID())
	collect(root.Key("AcroForm").Key("Fields"), &out, 0, docmdp)
	sort.SliceStable(out, func(i, j int) bool { return out[i].end < out[j].end })
	return out, rdr.NumPage(), nil
}

func collect(node pdf.Value, out *[]byteRange, depth, docmdp int) {
	if depth > 16 || node.Kind() != pdf.Array {
		return
	}
	for i := 0; i < node.Len(); i++ {
		f := node.Index(i)
		if f.Key("FT").Name() == "Sig" {
			v := f.Key("V")
			br := v.Key("ByteRange")
			if br.Kind() == pdf.Array && br.Len() >= 4 {
				r := byteRange{end: br.Index(2).Int64() + br.Index(3).Int64(), field: f.Key("T").Text()}
				refs := v.Key("Reference")
				for j := 0; j < refs.Len(); j++ {
					ref := refs.Index(j)
					switch ref.Key("TransformMethod").Name() {
					case "DocMDP":
						r.certify = int(ref.Key("TransformParams").Key("P").Int64())
						if r.certify == 0 {
							r.certify = 2 // the spec's default
						}
					case "FieldMDP":
						if p := ref.Key("TransformParams").Key("P"); p.Kind() == pdf.Integer {
							r.lock = int(p.Int64())
						}
					}
				}
				if p := f.Key("Lock").Key("P"); r.lock == 0 && p.Kind() == pdf.Integer {
					r.lock = int(p.Int64())
				}
				r.inPerms = docmdp != 0 && int(v.GetPtr().GetID()) == docmdp
				*out = append(*out, r)
			}
		}
		collect(f.Key("Kids"), out, depth+1, docmdp)
	}
}

// laterChanges compares the pages of the revision a signature covered
// with the pages of the document as it stands now. Identical content
// streams mean the updates since only added objects — a filled form
// field, another signature — which is exactly what a multi-signer
// document is supposed to look like.
func laterChanges(doc []byte, signedEnd int64) string {
	if signedEnd <= 0 || signedEnd > int64(len(doc)) {
		return ChangedUnknown
	}
	before, err := pageDigests(doc[:signedEnd])
	if err != nil {
		return ChangedUnknown
	}
	after, err := pageDigests(doc)
	if err != nil {
		return ChangedUnknown
	}
	if len(before) != len(after) {
		return ChangedContent
	}
	for i := range before {
		if before[i] != after[i] {
			return ChangedContent
		}
	}
	return ChangedFields
}

// pageDigests is one hash per page over what that page draws.
func pageDigests(doc []byte) ([]string, error) {
	rdr, err := pdf.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return nil, err
	}
	n := rdr.NumPage()
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		p := rdr.Page(i)
		if p.V.IsNull() {
			return nil, fmt.Errorf("verify: page %d is unreadable", i)
		}
		h := sha256.New()
		switch c := p.V.Key("Contents"); c.Kind() {
		case pdf.Array:
			for j := 0; j < c.Len(); j++ {
				writeStream(h, c.Index(j))
			}
		default:
			writeStream(h, c)
		}
		out = append(out, hex.EncodeToString(h.Sum(nil)))
	}
	return out, nil
}

func writeStream(h io.Writer, v pdf.Value) {
	if v.Kind() != pdf.Stream {
		return
	}
	r := v.Reader()
	if r == nil {
		return
	}
	defer r.Close()
	_, _ = io.Copy(h, r)
}

// ── certificates ───────────────────────────────────────────────────────

// ParsePEM decodes every certificate in a PEM bundle, skipping anything
// that is not one.
func ParsePEM(text string) []*x509.Certificate {
	var out []*x509.Certificate
	rest := []byte(text)
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			return out
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(blk.Bytes); err == nil {
			out = append(out, c)
		}
	}
}

// Fingerprint is the SHA-256 of a certificate, in the shape a person can
// read off a screen and compare with a receipt.
func Fingerprint(c *x509.Certificate) string { return fingerprint(c) }

func fingerprint(c *x509.Certificate) string {
	if c == nil {
		return ""
	}
	sum := sha256.Sum256(c.Raw)
	return group(hex.EncodeToString(sum[:]))
}

func group(h string) string {
	var b strings.Builder
	for i := 0; i < len(h); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strings.ToUpper(h[i:min(i+4, len(h))]))
	}
	return b.String()
}

func certOf(c *x509.Certificate) Cert {
	if c == nil {
		return Cert{}
	}
	out := Cert{
		Subject:   name(c.Subject.CommonName, c.Subject.Organization, c.Subject.String()),
		Issuer:    name(c.Issuer.CommonName, c.Issuer.Organization, c.Issuer.String()),
		Serial:    strings.ToUpper(c.SerialNumber.Text(16)),
		FP:        fingerprint(c),
		NotBefore: c.NotBefore, NotAfter: c.NotAfter, IsCA: c.IsCA,
	}
	if len(c.EmailAddresses) > 0 {
		out.Email = c.EmailAddresses[0]
	}
	out.KeyUsage = keyUsages(c.KeyUsage)
	out.EKU = extUsages(c)
	return out
}

func name(cn string, org []string, full string) string {
	if strings.TrimSpace(cn) != "" {
		return strings.TrimSpace(cn)
	}
	if len(org) > 0 && strings.TrimSpace(org[0]) != "" {
		return strings.TrimSpace(org[0])
	}
	return full
}

func keyUsages(u x509.KeyUsage) []string {
	var out []string
	for _, p := range []struct {
		bit  x509.KeyUsage
		name string
	}{
		{x509.KeyUsageDigitalSignature, "digital signature"},
		{x509.KeyUsageContentCommitment, "non-repudiation"},
		{x509.KeyUsageKeyEncipherment, "key encipherment"},
		{x509.KeyUsageCertSign, "certificate signing"},
		{x509.KeyUsageCRLSign, "CRL signing"},
	} {
		if u&p.bit != 0 {
			out = append(out, p.name)
		}
	}
	return out
}

func extUsages(c *x509.Certificate) []string {
	var out []string
	for _, e := range c.ExtKeyUsage {
		switch e {
		case x509.ExtKeyUsage(36):
			out = append(out, "document signing")
		case x509.ExtKeyUsageEmailProtection:
			out = append(out, "e-mail protection")
		case x509.ExtKeyUsageClientAuth:
			out = append(out, "client authentication")
		default:
			out = append(out, fmt.Sprintf("usage %d", int(e)))
		}
	}
	for _, o := range c.UnknownExtKeyUsage {
		if o.String() == "1.3.6.1.5.5.7.3.36" {
			out = append(out, "document signing")
			continue
		}
		out = append(out, o.String())
	}
	return out
}
