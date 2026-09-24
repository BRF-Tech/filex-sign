package host

import (
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/testca"
)

// Fake is an in-memory Host for tests. It mirrors the rules the real host
// enforces that matter to the plugin's logic: state is per input ref, a
// destroyed key no longer signs, a revoked page is gone, mail can be
// switched off to exercise the "no mail" path.
type Fake struct {
	mu sync.Mutex

	Inputs   map[string][]byte   // ref → bytes ("in:0", "pub:0", …)
	Files    map[string]FakeFile // ref → where the file is and what it is called
	Outputs  []FakeOutput
	State    map[string]string // ref + "\x00" + key → value
	Notices  []pluginkit.Notice
	Mails    []FakeMail
	Shares   map[string]*FakeShare // token → share
	Settings map[string]string
	Logs     []string
	Clock    time.Time
	ids      int

	// Locks mirrors the host's one-lock-per-file rule; Unlocks records
	// every lift so a test can prove no exit of the flow forgets one.
	Locks   map[string]time.Time
	Unlocks []string
	// LockedForGood lists every ref locked until lifted (no end).
	LockedForGood []string
	// LockReasons: the manifest message each lock was taken with (FileLockMessage).
	LockReasons map[string]string

	// Engines says which heavy binaries this "host" has, and EngineFn
	// stands in for running them.
	Engines    map[string]bool
	EngineFn   func(req pluginkit.EngineRequest) (*pluginkit.EngineResult, error)
	EngineRuns []pluginkit.EngineRequest

	// Network is what Asset can download: URL → bytes. Offline makes every
	// download fail the way an installation with no internet does; the
	// cache (by sha256) still answers. Downloads lists every URL actually
	// downloaded — a cached asset adds nothing.
	Network   map[string][]byte
	Offline   bool
	Downloads []string
	assets    map[string][]byte
	// Pending: the host is still downloading whatever is not cached, and
	// every call answers `timeout` (asset_fetch on a slow line).
	Pending bool

	// SealsHandedOut counts PlatformSeal calls.
	SealsHandedOut int

	// Knobs
	MailErr   error // returned by MailSend when set
	NotifyErr error
	LockErr   error
	UnlockErr error
	// StateErr is returned by every state write; ReadOnlyState() sets it
	// to what filex answers a call that may not make one.
	StateErr error
	// ShareMaxTTLDays is the installation's share ceiling
	// (share.max_ttl_days). ShareCreate clamps a link's life to it exactly
	// as filex does, silently — which is the whole reason the app has to
	// read it first (CallContext.ShareMaxTTLDays). 0 = no ceiling, the
	// Fake's default so older tests keep the lives they ask for.
	ShareMaxTTLDays int
	// ShareInfoErr makes ShareInfo fail with something that is NOT "no
	// such link" — a host that cannot answer. The app must then leave the
	// request alone: it never closes one on a guess.
	ShareInfoErr error
	// SharePINErr makes SharePIN fail outright (a refused permission).
	SharePINErr error
	// PINUnrecoverable marks tokens whose PIN the "host" can no longer
	// show — a link minted before the instance kept a recoverable copy, or
	// one sealed under a key that has been rotated away. The screen has to
	// say so honestly instead of showing a blank.
	PINUnrecoverable map[string]bool
	// PINReads records every token whose PIN was really handed over, so a
	// test can prove a screen did not read one it had no business reading.
	PINReads []string
	// ShareErr, OutputErr and SignErr are the rest of what a call without
	// a writable scope is refused. TickScope() sets the whole set at once.
	ShareErr  error
	OutputErr error
	SignErr   error
	// Refused names every write this Fake turned away, in order.
	//
	// ⚠⚠ It exists because a refusal is answered IN BAND: a plugin that
	// writes where it may not does not trap, it gets an error it is free
	// to log and carry on from — so a green test proves nothing about a
	// call that should never have written. This list is the proof. A
	// wake-up that named exactly the right work AND appears here is still
	// wrong, and will be wrong on a real instance in a way no screen shows.
	Refused         []string
	SignUnavailable bool
	CA              *testca.CA
	// RetiredCAs stands in for authorities that were rotated or replaced:
	// they keep verifying old signatures and are never deleted.
	RetiredCAs []*x509.Certificate
	keys       map[string]*testca.Leaf
	Destroyed  map[string]bool
	Progressed []string
}

// FakeFile is what the host knows about a stored file beside its bytes.
// ⚠ A ref with no Path stands in for a state row written before the host
// kept the path (migration 00048): StateList leaves it out, exactly as
// the real one does, until the next state_set fills it in.
type FakeFile struct {
	Path string // adapter-qualified, "docs://a/b.pdf"
	Name string
}

// FakeOutput is a file the plugin wrote.
type FakeOutput struct {
	Ref, Name string
	Data      []byte
}

// FakeMail is a sent mail.
type FakeMail struct{ To, Subject, Body string }

// FakeShare is a share the plugin opened.
type FakeShare struct {
	Token   string
	Req     pluginkit.PageCreate
	PIN     string
	State   json.RawMessage
	Revoked bool
	Expires time.Time
}

// NewFake makes a Fake with a fresh CA and a fixed clock.
func NewFake() *Fake {
	clock := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	ca, err := testca.NewAt("sign", clock)
	if err != nil {
		panic(err)
	}
	return &Fake{
		Inputs: map[string][]byte{}, Files: map[string]FakeFile{}, State: map[string]string{}, Shares: map[string]*FakeShare{},
		Settings: map[string]string{},
		Clock:    clock, CA: ca,
		keys: map[string]*testca.Leaf{}, Destroyed: map[string]bool{},
		Locks: map[string]time.Time{}, Engines: map[string]bool{}, LockReasons: map[string]string{},
		PINUnrecoverable: map[string]bool{},
	}
}

// ── file locks ─────────────────────────────────────────────────────────

func (f *Fake) FileLock(ref string, ttlDays int, reason string) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LockErr != nil {
		return time.Time{}, f.refused("file_lock", f.LockErr)
	}
	_, input := f.Inputs[ref]
	output := false
	for _, o := range f.Outputs {
		output = output || o.Ref == ref
	}
	if !input && !output {
		return time.Time{}, &pluginkit.HostError{Code: wire.ErrNotFound, Message: "no such input or output " + ref}
	}
	if ttlDays == pluginkit.LockUntilLifted {
		// Until lifted: the zero time, as the real host answers no end.
		f.Locks[ref] = time.Time{}
		f.LockedForGood = append(f.LockedForGood, ref)
		return time.Time{}, nil
	}
	if until, ok := f.Locks[ref]; ok && until.After(f.Clock) {
		return until, nil
	}
	if ttlDays <= 0 {
		ttlDays = 30
	}
	if ttlDays > 365 {
		ttlDays = 365
	}
	until := f.Clock.AddDate(0, 0, ttlDays)
	f.Locks[ref] = until
	return until, nil
}

// FileLockMessage is FileLock with a manifest message as its reason; the key
// is recorded (LockReasons) so a test can see which one was used.
func (f *Fake) FileLockMessage(ref string, ttlDays int, key string, args map[string]string) (time.Time, error) {
	until, err := f.FileLock(ref, ttlDays, "")
	if err != nil {
		return until, err
	}
	f.mu.Lock()
	if f.LockReasons == nil {
		f.LockReasons = map[string]string{}
	}
	f.LockReasons[ref] = key
	f.mu.Unlock()
	return until, nil
}

// UnlockErr is what FileUnlock answers when set. ⚠ filex lifts a lock
// "from action jobs only", so a SCREEN that ends a request meets exactly
// this refusal.
func (f *Fake) FileUnlock(ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.UnlockErr != nil {
		return f.refused("file_unlock", f.UnlockErr)
	}
	f.Unlocks = append(f.Unlocks, ref)
	delete(f.Locks, ref)
	return nil
}

// LockedRefs lists the refs still frozen.
func (f *Fake) LockedRefs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for ref := range f.Locks {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

// ── engines ────────────────────────────────────────────────────────────

func (f *Fake) EngineAvailable(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Engines[name]
}

func (f *Fake) EngineRun(req pluginkit.EngineRequest) (*pluginkit.EngineResult, error) {
	f.mu.Lock()
	if !f.Engines[req.Engine] {
		f.mu.Unlock()
		return nil, &pluginkit.HostError{Code: wire.ErrUnavailable, Message: "engine " + req.Engine + " is not installed on this host"}
	}
	f.EngineRuns = append(f.EngineRuns, req)
	fn := f.EngineFn
	f.mu.Unlock()
	if fn == nil {
		return nil, &pluginkit.HostError{Code: wire.ErrInternal, Message: "no engine behaviour configured in this test"}
	}
	return fn(req)
}

// AddArtefact registers a file an engine "produced" so the plugin can
// read it back through ReadInput.
func (f *Fake) AddArtefact(name string, data []byte) wire.OutputRef {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids++
	ref := fmt.Sprintf("eng:%d", f.ids)
	f.Inputs[ref] = append([]byte(nil), data...)
	return wire.OutputRef{Ref: ref, Name: name}
}

func (f *Fake) ReadInput(ref string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.Inputs[ref]
	if !ok {
		return nil, &pluginkit.HostError{Code: wire.ErrNotFound, Message: "no such input " + ref}
	}
	return append([]byte(nil), b...), nil
}

// Asset mirrors asset_fetch: the cache first, then the network, and the
// bytes only when they hash to the pin.
func (f *Fake) Asset(url, sum string, size int64) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.assets == nil {
		f.assets = map[string][]byte{}
	}
	if b, ok := f.assets[sum]; ok {
		return append([]byte(nil), b...), true, nil
	}
	if f.Offline {
		return nil, false, &pluginkit.HostError{Code: wire.ErrUnavailable, Message: "the network is not reachable"}
	}
	if f.Pending {
		return nil, false, &pluginkit.HostError{Code: wire.ErrTimeout, Message: "still downloading; ask again in a moment"}
	}
	b, ok := f.Network[url]
	if !ok {
		return nil, false, &pluginkit.HostError{Code: wire.ErrUnavailable, Message: "the server answered 404"}
	}
	f.Downloads = append(f.Downloads, url)
	if int64(len(b)) > size {
		return nil, false, &pluginkit.HostError{Code: wire.ErrTooLarge, Message: "the asset is larger than max_bytes"}
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != sum {
		return nil, false, &pluginkit.HostError{Code: wire.ErrIntegrity, Message: "the downloaded file does not match its pinned sha256"}
	}
	f.assets[sum] = append([]byte(nil), b...)
	return append([]byte(nil), b...), false, nil
}

func (f *Fake) WriteOutput(name string, b []byte) (wire.OutputRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.OutputErr != nil {
		return wire.OutputRef{}, f.refused("file_create", f.OutputErr)
	}
	ref := fmt.Sprintf("out:%d", len(f.Outputs))
	f.Outputs = append(f.Outputs, FakeOutput{Ref: ref, Name: name, Data: append([]byte(nil), b...)})
	return wire.OutputRef{Ref: ref, Name: name}, nil
}

func (f *Fake) Progress(done, total int64, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Progressed = append(f.Progressed, fmt.Sprintf("%d/%d %s", done, total, msg))
}

func (f *Fake) StateGet(ref, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Inputs[ref]; !ok {
		return "", false, &pluginkit.HostError{Code: wire.ErrNotFound, Message: "state is kept per storage file; ref is not one"}
	}
	v, ok := f.State[ref+"\x00"+key]
	return v, ok, nil
}

func (f *Fake) StateSet(ref, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StateErr != nil {
		return f.refused("state_set", f.StateErr)
	}
	if _, ok := f.Inputs[ref]; !ok {
		return &pluginkit.HostError{Code: wire.ErrNotFound, Message: "state is kept per storage file; ref is not one"}
	}
	if len(value) > 64<<10 {
		return &pluginkit.HostError{Code: wire.ErrTooLarge, Message: "state over 64 KiB"}
	}
	f.State[ref+"\x00"+key] = value
	return nil
}

// ReadOnlyState makes every write refuse the way filex refuses one from a
// call that may not make it.
//
// ⚠⚠ This is not a hypothetical: a plain `view_event` is EXACTLY such a
// call ("this call may not write state"). A job may write, a public
// page's event may write, a screen inside the app may NOT — so any
// behaviour a screen tries to record has to survive being refused
// without telling anybody the same news twice.
func (f *Fake) ReadOnlyState() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.StateErr = &pluginkit.HostError{Code: wire.ErrPermissionDenied, Message: "this call may not write state"}
}

// TickScope makes the Fake answer exactly what filex answers the hourly
// wake-up: reads pass, every WRITE is refused.
//
// ⚠ A tick may read settings and its own state, look people up, notify
// and mail; it may not write state, create files, take locks, open or
// revoke shares, or sign. The work it schedules is an ordinary job and
// gets the writable scope — decide in the wake-up, act in the action.
func (f *Fake) TickScope() {
	f.mu.Lock()
	defer f.mu.Unlock()
	refuse := func(what string) error {
		return &pluginkit.HostError{Code: wire.ErrPermissionDenied, Message: what + " from action jobs only"}
	}
	f.StateErr = &pluginkit.HostError{Code: wire.ErrPermissionDenied, Message: "this call may not write state"}
	f.LockErr, f.UnlockErr = refuse("locks"), refuse("locks")
	f.ShareErr, f.OutputErr, f.SignErr = refuse("shares"), refuse("outputs"), refuse("signing")
}

// refused records a write this scope turned away and answers it.
// ⚠ Caller holds the lock.
func (f *Fake) refused(what string, err error) error {
	f.Refused = append(f.Refused, what)
	return err
}

// Register says where a ref lives, so StateList can name it.
func (f *Fake) Register(ref, path, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Files[ref] = FakeFile{Path: path, Name: name}
}

// StateList mirrors the host: this plugin's rows only, newest first,
// never a file with no path (see FakeFile).
func (f *Fake) StateList(key string, limit int) ([]pluginkit.StateItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	var out []pluginkit.StateItem
	for k, v := range f.State {
		ref, got, ok := strings.Cut(k, "\x00")
		if !ok || (key != "" && got != key) {
			continue
		}
		file, known := f.Files[ref]
		if !known || file.Path == "" {
			continue
		}
		out = append(out, pluginkit.StateItem{Path: file.Path, Name: file.Name, Key: got, Value: v})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *Fake) StateDelete(ref, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StateErr != nil {
		return f.refused("state_set", f.StateErr)
	}
	delete(f.State, ref+"\x00"+key)
	return nil
}

func (f *Fake) NotifySend(n pluginkit.Notice) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.NotifyErr != nil {
		return 0, f.NotifyErr
	}
	f.Notices = append(f.Notices, n)
	return int64(len(f.Notices)), nil
}

func (f *Fake) MailSend(to, subject, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.MailErr != nil {
		return f.MailErr
	}
	f.Mails = append(f.Mails, FakeMail{To: to, Subject: subject, Body: body})
	return nil
}

// Setting answers an admin-configured setting.
func (f *Fake) Setting(key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.Settings[key]
	return v, ok, nil
}

func (f *Fake) ShareCreate(req pluginkit.PageCreate) (*pluginkit.PageCreated, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ShareErr != nil {
		return nil, f.refused("share_create", f.ShareErr)
	}
	f.ids++
	tok := fmt.Sprintf("tok%08d", f.ids)
	pin := ""
	if req.PIN == "auto" {
		pin = fmt.Sprintf("%06d", 100000+f.ids)
	} else if req.PIN != "" {
		pin = req.PIN
	}
	ttl := req.TTLDays
	if ttl <= 0 {
		ttl = 14
	}
	if f.ShareMaxTTLDays > 0 && ttl > f.ShareMaxTTLDays {
		ttl = f.ShareMaxTTLDays
	}
	st, _ := json.Marshal(req.State)
	exp := f.Clock.AddDate(0, 0, ttl)
	f.Shares[tok] = &FakeShare{Token: tok, Req: req, PIN: pin, State: st, Expires: exp}
	return &pluginkit.PageCreated{Token: tok, URL: "https://filex.example/s/" + tok, PIN: pin, ExpiresAt: exp.Format(time.RFC3339)}, nil
}

func (f *Fake) ShareRevoke(token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ShareErr != nil {
		return f.refused("share_revoke", f.ShareErr)
	}
	p, ok := f.Shares[token]
	if !ok {
		return &pluginkit.HostError{Code: wire.ErrNotFound, Message: "no such share"}
	}
	p.revoke(f.Clock)
	return nil
}

// revoke is filex's revoke: the link stops NOW, which the host records by
// moving expires_at to the moment of the revoke. An earlier expiry than
// the one handed out at share_create is how a revoke is recognised later.
func (p *FakeShare) revoke(now time.Time) {
	if !p.Revoked {
		p.Revoked = true
		if now.Before(p.Expires) {
			p.Expires = now
		}
	}
}

// EndFromShares is a PERSON ending one of the app's links on the Shares
// screen — My shares' Revoke, or an administrator's Revoke (deleted =
// false) or Delete (deleted = true). The app is not told; it has to ask.
func (f *Fake) EndFromShares(token string, deleted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if deleted {
		delete(f.Shares, token)
		return
	}
	if p, ok := f.Shares[token]; ok {
		p.revoke(f.Clock)
	}
}

// ShareInfo answers like share_state's read: the link's facts, or
// not_found for a link that no longer exists. A read, so it is allowed in
// every scope — the wake-up's included.
func (f *Fake) ShareInfo(token string) (*pluginkit.ShareFacts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ShareInfoErr != nil {
		return nil, f.ShareInfoErr
	}
	p, ok := f.Shares[token]
	if !ok {
		return nil, &pluginkit.HostError{Code: wire.ErrNotFound, Message: "no such link"}
	}
	exp := p.Expires
	return &pluginkit.ShareFacts{Page: p.Req.PageID, Subject: p.Req.Subject, ExpiresAt: &exp,
		Revoked: p.Revoked || !f.Clock.Before(exp)}, nil
}

// SharePIN answers the PIN this fake minted, or the reason it cannot —
// the three the real host gives, so a screen's honest message can be
// measured without a database.
func (f *Fake) SharePIN(token string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SharePINErr != nil {
		return "", "", f.SharePINErr
	}
	p, ok := f.Shares[token]
	if !ok {
		return "", "", &pluginkit.HostError{Code: wire.ErrNotFound, Message: "no such link"}
	}
	if p.PIN == "" {
		return "", "no_pin", nil
	}
	if f.PINUnrecoverable[token] {
		return "", "not_recoverable", nil
	}
	f.PINReads = append(f.PINReads, token)
	return p.PIN, "", nil
}

// currentShareKey names the share a page_event call is bound to.
var currentShareKey = "\x00current"

// BindShare makes token the "current share" for ShareState("") calls.
func (f *Fake) BindShare(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.State[currentShareKey] = token
}

func (f *Fake) share(token string) (*FakeShare, error) {
	if token == "" {
		token = f.State[currentShareKey]
	}
	p, ok := f.Shares[token]
	if !ok {
		return nil, &pluginkit.HostError{Code: wire.ErrNotFound, Message: "no such share"}
	}
	return p, nil
}

func (f *Fake) ShareState(token string, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.share(token)
	if err != nil {
		return err
	}
	if out == nil || len(p.State) == 0 {
		return nil
	}
	return json.Unmarshal(p.State, out)
}

func (f *Fake) ShareStateSet(token string, st any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ShareErr != nil {
		return f.refused("share_state_set", f.ShareErr)
	}
	p, err := f.share(token)
	if err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	p.State = b
	return nil
}

func (f *Fake) SignInfo() (*SignInfo, error) {
	if f.SignUnavailable {
		return &SignInfo{Available: false, Reason: "FILEX_SECRET_KEY is not set"}, nil
	}
	bundle := testca.PEM(f.CA.Cert)
	for _, old := range f.RetiredCAs {
		bundle += testca.PEM(old)
	}
	return &SignInfo{Available: true, Authorities: bundle, Algorithm: "ecdsa-p256-sha256"}, nil
}

func (f *Fake) CertIssue(cn, email string, days int) (*pluginkit.IssuedCert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SignErr != nil {
		return nil, f.refused("cert_issue", f.SignErr)
	}
	if f.SignUnavailable {
		return nil, &pluginkit.HostError{Code: wire.ErrUnavailable, Message: "signing unavailable"}
	}
	// Issued on the HOST's clock, which is the clock it signs with.
	leaf, err := f.CA.IssueAt(cn, email, days, f.Clock)
	if err != nil {
		return nil, err
	}
	f.ids++
	ref := fmt.Sprintf("key%d", f.ids)
	f.keys[ref] = leaf
	return &pluginkit.IssuedCert{KeyRef: ref, CertPEM: testca.PEM(leaf.Cert), ChainPEM: testca.PEM(f.CA.Cert), NotAfter: leaf.Cert.NotAfter.Format(time.RFC3339)}, nil
}

// PlatformSeal is the fake installation's seal: one key, issued once by
// the same authority, that KeyDestroy refuses.
func (f *Fake) PlatformSeal() (*pluginkit.IssuedCert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SignErr != nil {
		return nil, f.refused("cert_issue", f.SignErr)
	}
	if f.SignUnavailable {
		return nil, &pluginkit.HostError{Code: wire.ErrUnavailable, Message: "signing unavailable"}
	}
	if _, ok := f.keys[FakeSealRef]; !ok {
		leaf, err := f.CA.IssueAt("filex document seal", "", 3650, f.Clock)
		if err != nil {
			return nil, err
		}
		f.keys[FakeSealRef] = leaf
	}
	leaf := f.keys[FakeSealRef]
	f.SealsHandedOut++
	return &pluginkit.IssuedCert{KeyRef: FakeSealRef, CertPEM: testca.PEM(leaf.Cert), ChainPEM: testca.PEM(f.CA.Cert), NotAfter: leaf.Cert.NotAfter.Format(time.RFC3339)}, nil
}

// FakeSealRef is the fake host's one seal key.
const FakeSealRef = "platform-seal"

type fakeSigner struct {
	f   *Fake
	ref string
	pub crypto.PublicKey
}

func (s *fakeSigner) Public() crypto.PublicKey { return s.pub }
func (s *fakeSigner) Sign(r io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	s.f.mu.Lock()
	leaf, ok := s.f.keys[s.ref]
	destroyed := s.f.Destroyed[s.ref]
	s.f.mu.Unlock()
	if !ok || destroyed {
		return nil, &pluginkit.HostError{Code: wire.ErrNotFound, Message: "no such key"}
	}
	if opts == nil || opts.HashFunc() != crypto.SHA256 {
		return nil, errors.New("host signs sha256 digests only")
	}
	return leaf.Key.Sign(r, digest, opts)
}

func (f *Fake) Signer(issued *pluginkit.IssuedCert) (crypto.Signer, *x509.Certificate, error) {
	cert, err := parsePEM(issued.CertPEM)
	if err != nil {
		return nil, nil, err
	}
	return &fakeSigner{f: f, ref: issued.KeyRef, pub: cert.PublicKey}, cert, nil
}

func (f *Fake) KeyDestroy(keyRef string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if keyRef == FakeSealRef {
		return &pluginkit.HostError{Code: wire.ErrInvalid, Message: "the platform seal key is kept by the host"}
	}
	if _, ok := f.keys[keyRef]; !ok {
		return &pluginkit.HostError{Code: wire.ErrNotFound, Message: "no such key"}
	}
	f.Destroyed[keyRef] = true
	return nil
}

func (f *Fake) Log(level, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Logs = append(f.Logs, level+": "+msg)
}

func (f *Fake) Now() time.Time { return f.Clock }

func (f *Fake) NewID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids++
	return fmt.Sprintf("id%04d", f.ids)
}

// LiveKeys lists the key refs that were issued and not destroyed.
func (f *Fake) LiveKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.keys {
		// The seal is the installation's, not a signer's: it is never
		// destroyed, and it is not what "no key survives a signature" means.
		if !f.Destroyed[k] && k != FakeSealRef {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
