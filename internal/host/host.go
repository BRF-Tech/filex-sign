// Package host is the thin seam between the plugin's logic and filex's
// host functions. The logic (internal/app) talks to the Host interface;
// Kit forwards to pluginkit inside the wasm module, Fake answers from
// memory so the whole flow — request, share, fill, apply, sign, lock,
// receipt, verify — runs as an ordinary `go test` on the developer's
// machine with a throw-away CA.
package host

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"io"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// SignInfo is what the host says about signing, with the one field the
// plugin cares about most: the WHOLE bundle of signing authorities.
//
// ⚠ Build the trust pool from Authorities, never from the live
// certificate alone. Importing an organisation's own CA, or rotating the
// generated one, RETIRES the previous authority instead of deleting it —
// every signature it ever made stays checkable. Trusting only the live
// one would make the whole past read as "untrusted" after one rotation.
type SignInfo struct {
	Available bool
	Reason    string
	// Authorities is every CA certificate of this tenant as one PEM
	// bundle, the live one first.
	Authorities string
	Algorithm   string
}

// Host is every host function the plugin uses, plus time and randomness
// so tests are deterministic.
type Host interface {
	ReadInput(ref string) ([]byte, error)
	// Asset downloads a pinned file ONCE into the app's cache on the host
	// (asset_fetch: the `http:` permission, the sha256, the size) and reads
	// it. cached says no network was used. The fonts for non-Latin text
	// arrive this way (fontkit/noto.go).
	Asset(url, sha256 string, size int64) (data []byte, cached bool, err error)
	WriteOutput(name string, b []byte) (wire.OutputRef, error)
	Progress(done, total int64, msg string)

	Setting(key string) (string, bool, error)

	StateGet(ref, key string) (string, bool, error)
	StateSet(ref, key, value string) error
	StateDelete(ref, key string) error
	// StateList finds the files this plugin keeps a key on, newest first.
	// It is what lets a home screen list its own work: every other call
	// can only see the file it was opened on. Deleted files are not in the
	// answer, and neither are files the ASKER may not see — so the list
	// can be drawn honestly without ever widening anybody's reach.
	StateList(key string, limit int) ([]pluginkit.StateItem, error)

	// FileLock freezes the document read-only for everyone (the plugin's
	// own jobs still write); FileUnlock lifts it.
	FileLock(ref string, ttlDays int, reason string) (time.Time, error)
	// FileLockMessage is FileLock with the reason named by one of the
	// manifest's `messages`, so filex says it in each reader's language.
	FileLockMessage(ref string, ttlDays int, key string, args map[string]string) (time.Time, error)
	FileUnlock(ref string) error

	// EngineAvailable / EngineRun reach the host's heavy binaries; the
	// plugin only ever asks for LibreOffice, to turn an office document
	// into the PDF a signature can live in.
	EngineAvailable(name string) bool
	EngineRun(req pluginkit.EngineRequest) (*pluginkit.EngineResult, error)

	NotifySend(n pluginkit.Notice) (int64, error)
	MailSend(to, subject, body string) error

	// A surface an outsider opens is a real filex share: one revoke
	// list, one expiry policy, one PIN implementation, one visit counter.
	ShareCreate(req pluginkit.PageCreate) (*pluginkit.PageCreated, error)
	ShareRevoke(token string) error
	ShareState(token string, out any) error
	ShareStateSet(token string, st any) error
	// ShareInfo is what the host knows about one of OUR links: when it
	// stops, and whether it still opens. It is how the app learns that a
	// signing link was revoked (or deleted) from the Shares screen — filex
	// sends no event for that; the hourly wake-up asks. A deleted link
	// answers an error pluginkit.IsNotFound recognises.
	//
	// ⚠ Ask it from a job, a view or the wake-up, never from a signer's
	// PAGE: inside a page call filex answers share_state about the link
	// being visited, whatever token is named.
	ShareInfo(token string) (*pluginkit.ShareFacts, error)
	// SharePIN is the PIN of one of OUR links, read for the PERSON making
	// the call. It is the platform's own copy — this app keeps no PIN of
	// its own anywhere — and the host allows it only to whoever created
	// the link (or an administrator) and writes an audit row for every
	// read, successful or not.
	//
	// An empty PIN comes back with a REASON ("no_pin", "no_secret_key",
	// "not_recoverable"), never as a blank: a blank where a secret should
	// be reads as a bug, and the person cannot tell "there is no PIN" from
	// "we lost it".
	//
	// ⚠ Only from a call a signed-in person made. A visitor's page and the
	// hourly wake-up have no actor, and the host refuses them.
	SharePIN(token string) (pin string, reason string, err error)

	SignInfo() (*SignInfo, error)
	CertIssue(commonName, email string, days int) (*pluginkit.IssuedCert, error)
	// PlatformSeal is the installation's seal for this app: the same key
	// on every call, kept by the host, never destroyed by the app — what
	// closes a completed request (actions.go sealDocument).
	PlatformSeal() (*pluginkit.IssuedCert, error)
	// Signer turns an issued certificate into the crypto.Signer the PDF
	// library drives; the private key stays wherever the host keeps it.
	Signer(issued *pluginkit.IssuedCert) (crypto.Signer, *x509.Certificate, error)
	KeyDestroy(keyRef string) error

	Log(level, msg string)
	Now() time.Time
	NewID() string
}

// Kit is the production Host: straight through to pluginkit.
type Kit struct{}

func (Kit) ReadInput(ref string) ([]byte, error) { return pluginkit.ReadInput(ref) }

// Asset fetches (or finds in the cache) and reads a pinned file. It reads
// into a buffer of exactly the pinned size: a CJK face is megabytes, and a
// growing buffer would briefly hold it twice inside the module's memory.
func (Kit) Asset(url, sha256 string, size int64) ([]byte, bool, error) {
	a, err := pluginkit.AssetFetch(url, sha256, size)
	if err != nil {
		return nil, false, err
	}
	in, err := pluginkit.OpenInput(a.Ref)
	if err != nil {
		return nil, false, err
	}
	defer in.Close()
	buf := make([]byte, a.Size)
	if _, err := io.ReadFull(in, buf); err != nil {
		return nil, false, err
	}
	return buf, a.Cached, nil
}
func (Kit) WriteOutput(name string, b []byte) (wire.OutputRef, error) {
	return pluginkit.WriteOutput(name, b)
}
func (Kit) Progress(done, total int64, msg string) { pluginkit.Progress(done, total, msg) }
func (Kit) Setting(key string) (string, bool, error) {
	return pluginkit.Setting(key)
}
func (Kit) StateGet(ref, key string) (string, bool, error) {
	return pluginkit.StateGet(ref, key)
}
func (Kit) StateSet(ref, key, value string) error { return pluginkit.StateSet(ref, key, value) }
func (Kit) StateDelete(ref, key string) error     { return pluginkit.StateDelete(ref, key) }
func (Kit) StateList(key string, limit int) ([]pluginkit.StateItem, error) {
	return pluginkit.StateList(key, limit)
}
func (Kit) FileLockMessage(ref string, ttlDays int, key string, args map[string]string) (time.Time, error) {
	return pluginkit.FileLockMessage(ref, ttlDays, key, args)
}
func (Kit) FileLock(ref string, ttlDays int, reason string) (time.Time, error) {
	return pluginkit.FileLock(ref, ttlDays, reason)
}
func (Kit) FileUnlock(ref string) error      { return pluginkit.FileUnlock(ref) }
func (Kit) EngineAvailable(name string) bool { return pluginkit.EngineAvailable(name) }
func (Kit) EngineRun(req pluginkit.EngineRequest) (*pluginkit.EngineResult, error) {
	return pluginkit.EngineRun(req)
}
func (Kit) NotifySend(n pluginkit.Notice) (int64, error) { return pluginkit.NotifySend(n) }
func (Kit) MailSend(to, subject, body string) error      { return pluginkit.MailSend(to, subject, body) }
func (Kit) ShareCreate(req pluginkit.PageCreate) (*pluginkit.PageCreated, error) {
	return pluginkit.ShareCreate(req)
}
func (Kit) ShareRevoke(token string) error         { return pluginkit.ShareRevoke(token) }
func (Kit) ShareState(token string, out any) error { return pluginkit.ShareState(token, out) }
func (Kit) ShareStateSet(token string, st any) error {
	return pluginkit.ShareStateSet(token, st)
}
func (Kit) ShareInfo(token string) (*pluginkit.ShareFacts, error) { return pluginkit.ShareInfo(token) }

func (Kit) SharePIN(token string) (string, string, error) { return pluginkit.SharePIN(token) }

func (Kit) SignInfo() (*SignInfo, error) {
	raw, err := pluginkit.HostSignInfo()
	if err != nil {
		return nil, err
	}
	return &SignInfo{Available: raw.Available, Reason: raw.Reason,
		Authorities: authorities(raw), Algorithm: raw.Algorithm}, nil
}

// authorities is the whole bundle, retired authorities included. The
// single live certificate is only a fallback for a host that answers an
// older shape — trusting it alone would call every signature made before
// the last rotation untrusted.
func authorities(raw *pluginkit.SignInfo) string {
	if raw.CACertsPEM != "" {
		return raw.CACertsPEM
	}
	return raw.CACertPEM
}

func (Kit) CertIssue(cn, email string, days int) (*pluginkit.IssuedCert, error) {
	return pluginkit.CertIssue(cn, email, days)
}
func (Kit) PlatformSeal() (*pluginkit.IssuedCert, error) { return pluginkit.PlatformSeal() }
func (Kit) Signer(issued *pluginkit.IssuedCert) (crypto.Signer, *x509.Certificate, error) {
	s, err := pluginkit.NewHostSigner(issued)
	if err != nil {
		return nil, nil, err
	}
	return s, s.Cert, nil
}
func (Kit) KeyDestroy(keyRef string) error { return pluginkit.KeyDestroy(keyRef) }
func (Kit) Log(level, msg string)          { pluginkit.Log(level, msg) }
func (Kit) Now() time.Time                 { return time.Now() }
func (Kit) NewID() string                  { return NewID() }

// NewID is 16 hex characters from the platform's randomness (wasip1 has
// random_get, so crypto/rand works inside the sandbox).
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000")))[:16]
	}
	return hex.EncodeToString(b[:])
}

// ParseCertPEM decodes one PEM certificate.
func ParseCertPEM(pemText string) (*x509.Certificate, error) {
	return parsePEM(pemText)
}

// ParseChainPEM decodes every certificate in a PEM bundle.
func ParseChainPEM(pemText string) []*x509.Certificate {
	return parseChain(pemText)
}
