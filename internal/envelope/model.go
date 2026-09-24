// Package envelope is the signature request as data: who has to sign
// what, where on the page, under which rules, and how far along it is.
// The whole record lives in filex's per-file state under the document
// (key "envelope", ≤ 64 KiB), so it moves and vanishes with the file;
// the pure state machine in machine.go is the only thing that changes
// it.
package envelope

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex-sign/internal/fields"
)

// SchemaVersion is bumped when the JSON shape changes incompatibly.
// 2 added: typed fields with rules and fonts, signing order, deadline,
// reminders, declining, the file lock and the output choice.
// 3 added: named fields, signers without an e-mail address, delivery of
// the finished document as a share, the receipt each signer gets, and
// the AcroForm path -- a request opened at schema 3 fills REAL form
// fields instead of redrawing the page, so a second signature does not
// make the first one read as "the document changed after it was
// signed". An older record keeps Form false and is finished the old
// way, because its boxes were never created as fields.
const SchemaVersion = 3

// State keys this plugin keeps on the document. The envelope itself
// lives under StateKey; PendingKey is a value-free marker filex's
// listings expose as `sign:pending`, which is what makes the right-click
// menu offer "Sign / Fill" on exactly the documents that wait for one.
const (
	StateKey   = "envelope"
	PendingKey = "pending"
	// TodoKey is the PERSONAL marker (wire.PersonalState: `todo@<user id>`)
	// on a document a signer with an account has to sign now; a rule names
	// it `todo@me` (filex-app.json → fill).
	TodoKey = "todo"
	// SignedKey is a BADGE only: it marks a document this plugin signed
	// so a listing can show it. It is never a gate -- "Verify" is offered
	// on every PDF, because the document that most needs checking is the
	// one this instance did not sign.
	//
	// Its value is the user ids of the signers who have an account here,
	// comma separated (see AddSigner). That is what lets the Signatures
	// screen say "I have signed these" about a document that carries no
	// request at all -- somebody signed it by themselves.
	SignedKey = "signed"
	// CertPrefix + signer id holds that signer's leaf certificate (DER,
	// base64) so the receipt can be handed out again later. About 700
	// bytes each, well inside the host's 64 KiB per key.
	CertPrefix = "cert:"
)

// Status of the envelope as a whole.
type Status string

const (
	StatusSent       Status = "sent"        // pages created, nobody signed yet
	StatusInProgress Status = "in_progress" // at least one signer signed
	StatusCompleted  Status = "completed"   // every signer signed
	StatusCancelled  Status = "cancelled"   // the requester cancelled
	StatusExpired    Status = "expired"     // the pages expired before completion
	StatusDeclined   Status = "declined"    // a signer refused
)

// Closed reports whether the envelope accepts no more signatures.
func (s Status) Closed() bool {
	return s == StatusCompleted || s == StatusCancelled || s == StatusExpired || s == StatusDeclined
}

// SignerStatus is one participant's progress.
type SignerStatus string

const (
	SignerPending  SignerStatus = "pending"  // page created, nothing sent yet
	SignerNotified SignerStatus = "notified" // mail / notification went out
	SignerViewed   SignerStatus = "viewed"   // opened the page
	SignerSigned   SignerStatus = "signed"
	SignerDeclined SignerStatus = "declined"
	SignerVoid     SignerStatus = "void" // the envelope closed before they signed
)

// Kind says how the signer signs: a user of this filex instance signs
// inside the app, anybody else through their own private page.
const (
	KindInternal = "internal"
	KindExternal = "external"
)

// Signing order.
const (
	OrderParallel   = "parallel"   // everybody may sign at once
	OrderSequential = "sequential" // one after another, in the listed order
)

// Output modes a request may ask for — the same words filex's job output
// takes.
const (
	OutputVersion = "version" // a new version of the same file
	OutputSibling = "sibling" // a new file beside it
	OutputNone    = "none"
)

// DefaultSiblingName is the name a new file beside the original takes.
const DefaultSiblingName = "{stem}-signed{ext}"

// Person is who somebody IS, as far as this request is concerned: a
// name, an e-mail address, or both. At least one of the two is required
// and neither is privileged -- "e-mail is mandatory" was never true of
// the people who sign on paper either, and it is not true here.
type Person struct {
	UserID int64  `json:"user_id,omitempty"`
	Email  string `json:"email,omitempty"`
	Name   string `json:"name,omitempty"`
}

// HasEmail reports whether filex can write to this person. It is the one
// question that decides how a link reaches them: by mail when there is
// an address, through the requester when there is not.
func (p Person) HasEmail() bool { return strings.Contains(p.Email, "@") }

// Identity is the person as a line of text: the name, with the address
// beside it when there is one, or the address alone. Never an empty
// half -- a screen does not print "e-mail: -".
func (p Person) Identity() string {
	name, mail := strings.TrimSpace(p.Name), strings.TrimSpace(p.Email)
	switch {
	case name != "" && mail != "":
		return name + " (" + mail + ")"
	case name != "":
		return name
	case mail != "":
		return mail
	}
	return "-"
}

// CertName is what a certificate is issued to: the name when there is
// one, the address otherwise.
func (p Person) CertName() string {
	if n := strings.TrimSpace(p.Name); n != "" {
		return n
	}
	return strings.TrimSpace(p.Email)
}

// Display is the person's name, or their e-mail when there is none. A
// signer may have neither an account nor an address -- the requester
// hands them the link themselves -- so a name alone is enough.
func (p Person) Display() string {
	if strings.TrimSpace(p.Name) != "" {
		return strings.TrimSpace(p.Name)
	}
	if strings.TrimSpace(p.Email) != "" {
		return strings.TrimSpace(p.Email)
	}
	return "-"
}

// Reachable reports whether filex can reach this person by itself: an
// account to notify, or an address to write to.
func (p Person) Reachable() bool { return p.UserID != 0 || p.HasEmail() }

// Signer is one participant.
type Signer struct {
	ID     string       `json:"id"`
	Kind   string       `json:"kind"`
	Person Person       `json:"person"`
	Status SignerStatus `json:"status"`

	// The public page opened for this signer (external signers only; a
	// user of this instance signs in the app). The token is a bearer
	// secret: it only ever lives in this server-side record and is never
	// shown to anyone but the requester (Show link).
	PageToken     string `json:"page_token,omitempty"`
	PageTokenHash string `json:"page_token_hash,omitempty"`
	PageURL       string `json:"page_url,omitempty"`
	PageExpires   string `json:"page_expires,omitempty"`
	MailSent      bool   `json:"mail_sent,omitempty"`
	MailError     string `json:"mail_error,omitempty"`

	NotifiedAt    string `json:"notified_at,omitempty"`
	ViewedAt      string `json:"viewed_at,omitempty"`
	SignedAt      string `json:"signed_at,omitempty"`
	RemindedAt    string `json:"reminded_at,omitempty"`
	DeclinedAt    string `json:"declined_at,omitempty"`
	DeclineReason string `json:"decline_reason,omitempty"`
	SignedIP      string `json:"signed_ip,omitempty"`
	CertSerial    string `json:"cert_serial,omitempty"`
	CertExpires   string `json:"cert_expires,omitempty"`
	// CertFP is the SHA-256 fingerprint of the leaf certificate: the one
	// line a person can compare between their receipt and a verification
	// report without understanding anything else about certificates.
	CertFP string `json:"cert_fp,omitempty"`

	// The receipt an OUTSIDE signer is handed the moment they finish. It
	// is a share of its OWN, never the signing link: the last signature
	// revokes every open signing link, which would take the receipt with
	// it.
	ReceiptToken   string `json:"receipt_token,omitempty"`
	ReceiptURL     string `json:"receipt_url,omitempty"`
	ReceiptExpires string `json:"receipt_expires,omitempty"`
}

// HandOver reports whether the requester passes this signer their link
// by hand: somebody with no account and no e-mail address.
func (s Signer) HandOver() bool { return !s.Person.Reachable() }

// Internal reports whether the signer is a user of this instance, who
// signs in the app instead of through a public page.
func (s Signer) Internal() bool { return s.Kind == KindInternal && s.Person.UserID != 0 }

// Done reports whether the signer will not act any more.
func (s Signer) Done() bool {
	return s.Status == SignerSigned || s.Status == SignerVoid || s.Status == SignerDeclined
}

// Field is a box the `pdf-fields` editor placed: fractions of the
// rendered page (origin top-left), 1-based page, optional assignee (a
// signer id; empty = anybody's), plus the rules a typed value has to
// meet and the face it is stamped in.
type Field struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"` // signature | initials | date | text | checkbox
	Page     int     `json:"page"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	W        float64 `json:"w"`
	H        float64 `json:"h"`
	Assignee string  `json:"assignee,omitempty"`
	Required bool    `json:"required,omitempty"`
	Label    string  `json:"label,omitempty"`

	// Placed is nil once the box is on a page, and points at false while it
	// has been DEFINED and not yet placed. Naming a box and finding a place
	// for it are two different jobs and the wizard now asks them one at a
	// time; a request may not be sent while anything is still waiting.
	//
	// ⚠ A POINTER, so a field written before this reads as placed rather
	// than as "nobody has put it anywhere" — which would refuse to send
	// every request already in flight.
	Placed *bool `json:"placed,omitempty"`

	// Rule, Format, MinLen and MaxLen are a typed field's constraints;
	// Font is one of the faces the plugin embeds.
	Rule   string `json:"rule,omitempty"`
	Format string `json:"format,omitempty"`
	MinLen int    `json:"min_len,omitempty"`
	MaxLen int    `json:"max_len,omitempty"`
	Font   string `json:"font,omitempty"`

	// Style is how a signature or initials box is given: "" (drawn by
	// hand, or a picture of a hand) or "typed" (the signer's name set in
	// Font). The requester decides in the define step; the signer's pad
	// then offers exactly that. Meaningless on any other type.
	Style string `json:"style,omitempty"`
	// Lines are the facts printed under a signature, by id (views.StampLine*:
	// name, email, date, ip, cert, serial, authority), chosen per box by
	// the requester. ⚠ A POINTER on purpose: nil is "the default lines"
	// (what every box written before this carries), and an empty list is
	// "nothing under the signature" — `omitempty` on a plain slice would
	// fold the second into the first and put back the lines somebody took
	// off.
	Lines *[]string `json:"lines,omitempty"`
	// Key is the box's IDENTITY: an ASCII slug of its name ("Müşteri
	// adı" → "musteri-adi"), unique in the document, that becomes the PDF
	// form field's name (/T). Never shown to anybody — the NAME is what
	// people read, in any script, exactly as typed (the owner, 2026-09-21:
	// "ASCII kimlik üretsin ama göstermesin hiç … isim ayrı, kimlik ayrı").
	// It follows the name until the request is sent and is fixed from then
	// on (fields.AssignKeys); empty on a record written before it existed,
	// whose boxes are named by ID in the PDF.
	Key string `json:"key,omitempty"`

	// Value is what a signer filled in for text / date / checkbox fields
	// (signature images are never stored here — far too large).
	Value string `json:"value,omitempty"`
	// SignedBy is the signer who consumed the field.
	SignedBy string `json:"signed_by,omitempty"`
}

// Typed reports whether a signature box asks for a typed name rather than
// a drawing.
func (f Field) Typed() bool { return f.Style == "typed" }

// FormName is the box's name inside the PDF (/T): its identity when it has
// one, its internal id on a record written before identities existed.
func (f Field) FormName() string {
	if f.Key != "" {
		return f.Key
	}
	return f.ID
}

// IsPlaced says whether the box has a place on the document yet.
func (f Field) IsPlaced() bool { return f.Placed == nil || *f.Placed }

// Unplaced is the boxes still waiting for a place.
func Unplaced(fs []Field) []Field {
	var out []Field
	for _, f := range fs {
		if !f.IsPlaced() {
			out = append(out, f)
		}
	}
	return out
}

// Spec is the field's rules, the shape internal/fields checks against.
func (f Field) Spec() fields.Spec {
	return fields.Spec{
		Type: fields.NormalizeType(f.Type), Rule: fields.NormalizeRule(f.Rule),
		Format: fields.NormalizeFormat(f.Format), MinLen: f.MinLen, MaxLen: f.MaxLen,
		Required: f.Required, Font: fields.NormalizeFont(f.Font),
	}
}

// Options chosen by the requester.
type Options struct {
	PIN        string `json:"pin"` // "auto" (a PIN per page) | "none"
	ExpiryDays int    `json:"expiry_days"`
	Message    string `json:"message,omitempty"`
	Locale     string `json:"locale,omitempty"`

	// Order is parallel (default) or sequential.
	Order string `json:"order,omitempty"`
	// Deadline is the day the request should be signed by (YYYY-MM-DD).
	// The request closes when that day ends, and the links and the freeze
	// are cut to reach no further than the day after.
	Deadline string `json:"deadline,omitempty"`
	// RemindEveryDays: a signer who has been silent that long is reminded,
	// and again after as many more quiet days — sent by the hourly wake-up
	// (tick.go), only to signers filex can write to, only while the request
	// is open. 0 = never. The details panel's "Remind" sends one any time.
	RemindEveryDays int `json:"remind_every_days,omitempty"`
	// AllowDecline gives the signer a "I will not sign" button.
	AllowDecline bool `json:"allow_decline,omitempty"`
	// Lock freezes the document read-only for everyone while the request
	// is open (filex's file lock; administrators included).
	Lock bool `json:"lock,omitempty"`
	// Audit writes a trail PDF beside the document when the last
	// signature lands.
	Audit bool `json:"audit,omitempty"`
	// LockSigned keeps the signed file under filex's lock for good once
	// every signature is in (until an administrator lifts it, audited).
	// Without it the finished file is an ordinary file — the certification,
	// the seal and the hash every party was sent still say whether it was
	// changed.
	LockSigned bool `json:"lock_signed,omitempty"`
	// Output says where the signed document goes.
	Output Output `json:"output,omitempty"`

	// Deliver says what happens once every signature is in: the signed
	// document is opened as a filex share and its link mailed to every
	// signer who has an address, or nothing is sent.
	Deliver string `json:"deliver,omitempty"` // share | none
	// DeliveryPIN protects that share with a PIN which -- like every
	// other PIN here -- is shown to the requester and never mailed.
	DeliveryPIN string `json:"delivery_pin,omitempty"` // auto | none
}

// Delivery choices.
const (
	DeliverShare = "share"
	DeliverNone  = "none"
)

// Sends reports whether the finished document goes out as a share.
func (o Options) Sends() bool { return o.Deliver == DeliverShare }

// Output is where a signature's result lands: a new version of the same
// file, a new file beside it, or a name the requester typed.
type Output struct {
	Mode string `json:"mode,omitempty"`
	Name string `json:"name,omitempty"`
}

// Normalized fills the defaults an old or hand-made record may miss.
func (o Output) Normalized() Output {
	switch o.Mode {
	case OutputNone:
		return Output{Mode: OutputNone}
	case OutputSibling:
		name := strings.TrimSpace(o.Name)
		if name == "" {
			name = DefaultSiblingName
		}
		return Output{Mode: OutputSibling, Name: name}
	}
	return Output{Mode: OutputVersion}
}

// Event is one line of the envelope's history.
type Event struct {
	At     string `json:"at"`
	Type   string `json:"type"`
	Signer string `json:"signer,omitempty"`
	Note   string `json:"note,omitempty"`
	IP     string `json:"ip,omitempty"`
}

// Envelope is the whole record.
type Envelope struct {
	Schema    int      `json:"schema"`
	ID        string   `json:"id"`
	Status    Status   `json:"status"`
	Document  string   `json:"document"` // the file's name at request time
	Requester Person   `json:"requester"`
	Title     string   `json:"title,omitempty"`
	Options   Options  `json:"options"`
	Signers   []Signer `json:"signers"`
	Fields    []Field  `json:"fields"`
	Events    []Event  `json:"events"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	ClosedAt  string   `json:"closed_at,omitempty"`
	// SignCount is how many signatures this plugin applied to the document
	// through this envelope.
	SignCount int `json:"sign_count"`
	// Locked records that this plugin holds filex's lock on the document,
	// and until when, so every exit of the flow knows to lift it.
	Locked    bool   `json:"locked,omitempty"`
	LockUntil string `json:"lock_until,omitempty"`
	// Form records that this request's typed boxes exist in the document
	// as real AcroForm fields, so a signature fills a field instead of
	// redrawing the page underneath an earlier signature.
	Form bool `json:"form,omitempty"`
	// Certified is the DocMDP permission the FIRST signature certified the
	// document with (2: filling in forms and signing only); 0 = not
	// certified (a request opened by an older build, or a document that
	// already carried somebody else's signature).
	Certified int `json:"certified,omitempty"`
	// Sealed is the completion: filex's own signature over the whole
	// document and the SHA-256 of exactly the bytes every party was sent.
	Sealed *Sealed `json:"sealed,omitempty"`
	// The share the finished document was delivered through.
	DeliveryToken   string `json:"delivery_token,omitempty"`
	DeliveryURL     string `json:"delivery_url,omitempty"`
	DeliveryExpires string `json:"delivery_expires,omitempty"`
}

// SealedKey is the state key the completion leaves on the request's
// document: "<sha256 hex> <signed file name>". Verify finds a copy of the
// signed file by its hash through it (state_list), wherever the copy is.
const SealedKey = "sealed"

// Sealed is what the completion left: the final bytes' hash, whose seal
// closed them, and what the file allows from then on.
type Sealed struct {
	// SHA256 is the lower-case hex SHA-256 of the signed file exactly as it
	// was written and delivered — computed AFTER the seal.
	SHA256 string `json:"sha256"`
	// SealFP is the seal certificate's SHA-256 fingerprint (grouped hex).
	SealFP string `json:"seal_fp"`
	At     string `json:"at"`
	// Output is the signed file's name.
	Output string `json:"output,omitempty"`
	// LockedForGood: the signed file stays under filex's lock (LockSigned).
	LockedForGood bool `json:"locked_for_good,omitempty"`
}

// Limits the record must respect so it fits filex's 64 KiB state slot.
const (
	MaxSigners = 20
	MaxFields  = 60
	MaxEvents  = 200
	MaxBytes   = 60 << 10
)

// ErrNotFound is returned by Decode when there is no envelope.
var ErrNotFound = errors.New("envelope: none for this document")

// Decode parses a stored envelope; "" means none.
func Decode(s string) (*Envelope, error) {
	if strings.TrimSpace(s) == "" {
		return nil, ErrNotFound
	}
	var e Envelope
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		return nil, fmt.Errorf("envelope: unreadable: %w", err)
	}
	if e.Schema > SchemaVersion {
		return nil, fmt.Errorf("envelope: schema %d is newer than this plugin (%d)", e.Schema, SchemaVersion)
	}
	migrate(&e)
	return &e, nil
}

// migrate brings an older record up to what this build understands
// WITHOUT claiming it is one: Schema stays where it was, because the
// document's boxes are still whatever the older build put there.
func migrate(e *Envelope) {
	for i := range e.Fields {
		e.Fields[i].Type, e.Fields[i].Rule = fields.Migrate(e.Fields[i].Type, e.Fields[i].Rule)
	}
	if e.Options.Deliver == "" {
		// A request opened before delivery existed sends nothing: those
		// signers were never promised a copy, so do not surprise them.
		e.Options.Deliver = DeliverNone
	}
	if e.Options.DeliveryPIN == "" {
		e.Options.DeliveryPIN = "auto"
	}
}

// Encode serialises the envelope, trimming the oldest events when it
// would not fit the state slot.
func Encode(e *Envelope) (string, error) {
	for {
		b, err := json.Marshal(e)
		if err != nil {
			return "", err
		}
		if len(b) <= MaxBytes || len(e.Events) <= 10 {
			return string(b), nil
		}
		e.Events = e.Events[len(e.Events)/4:]
	}
}

// Signer finds a signer by id.
func (e *Envelope) Signer(id string) *Signer {
	for i := range e.Signers {
		if e.Signers[i].ID == id {
			return &e.Signers[i]
		}
	}
	return nil
}

// SignerByTokenHash finds the signer whose page has that token hash.
func (e *Envelope) SignerByTokenHash(h string) *Signer {
	if h == "" {
		return nil
	}
	for i := range e.Signers {
		if e.Signers[i].PageTokenHash == h {
			return &e.Signers[i]
		}
	}
	return nil
}

// SignerForUser finds the signer a logged-in person is: by user id
// first, then by e-mail (a request may have named the address before the
// account existed).
func (e *Envelope) SignerForUser(userID int64, email string) *Signer {
	for i := range e.Signers {
		if userID != 0 && e.Signers[i].Person.UserID == userID {
			return &e.Signers[i]
		}
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}
	for i := range e.Signers {
		if p := strings.ToLower(strings.TrimSpace(e.Signers[i].Person.Email)); p != "" && p == email {
			return &e.Signers[i]
		}
	}
	return nil
}

// FieldsFor lists the fields a signer may act on: their own and the
// unassigned ones that nobody consumed yet.
func (e *Envelope) FieldsFor(signerID string) []Field {
	var out []Field
	for _, f := range e.Fields {
		if f.Assignee == signerID || (f.Assignee == "" && f.SignedBy == "") {
			out = append(out, f)
		}
	}
	return out
}

// Signs reports whether this participant is asked for a SIGNATURE at
// all — one of their own, or one of the boxes that belong to anybody and
// nobody has taken yet.
//
// ⚠⚠ Not every participant signs (the owner, 2026-09-23: "her kişi için
// imza yerleştirmek zorunlu olmasın bazı kişiler sadece metin
// doldurabilir"). Somebody who only fills in boxes still commits their
// work cryptographically — their submission is signed over their own
// revision, invisibly, so the chain of custody stays unbroken and
// attributable — but every word shown to them, and every word written
// about them afterwards, has to say FILLED rather than SIGNED.
func (e *Envelope) Signs(signerID string) bool {
	for _, f := range e.FieldsFor(signerID) {
		if fields.Drawn(f.Type) {
			return true
		}
	}
	return false
}

// Field finds a field by id.
func (e *Envelope) Field(id string) *Field {
	for i := range e.Fields {
		if e.Fields[i].ID == id {
			return &e.Fields[i]
		}
	}
	return nil
}

// Sequential reports whether the signers go one after another.
func (e *Envelope) Sequential() bool { return e.Options.Order == OrderSequential }

// Next is the signer whose turn it is now, or nil when nobody is left.
func (e *Envelope) Next() *Signer {
	for i := range e.Signers {
		if !e.Signers[i].Done() {
			return &e.Signers[i]
		}
	}
	return nil
}

// Turn reports whether it is this signer's turn. In a parallel request
// it always is; in a sequential one only the first unfinished signer may
// act.
func (e *Envelope) Turn(id string) bool {
	if !e.Sequential() {
		return true
	}
	n := e.Next()
	return n != nil && n.ID == id
}

// Progress counts signed vs. total signers.
func (e *Envelope) Progress() (signed, total int) {
	for _, s := range e.Signers {
		if s.Status == SignerSigned {
			signed++
		}
	}
	return signed, len(e.Signers)
}

// OpenPageTokens lists the tokens of pages that are still live.
func (e *Envelope) OpenPageTokens() []string {
	var out []string
	for _, s := range e.Signers {
		if s.PageToken != "" && s.Status != SignerSigned && s.Status != SignerVoid {
			out = append(out, s.PageToken)
		}
	}
	return out
}

// MaxBadgeSigners caps the `signed` badge so it stays a badge.
const MaxBadgeSigners = 20

// AddSigner adds a user id to a `signed` badge value, keeping it unique,
// ordered and short. An id of 0 (somebody with no account here) is not
// recorded: the badge answers "did I sign this", and they cannot ask.
func AddSigner(badge string, userID int64) string {
	if userID == 0 {
		return badge
	}
	want := strconv.FormatInt(userID, 10)
	out := make([]string, 0, MaxBadgeSigners)
	for _, part := range strings.Split(badge, ",") {
		part = strings.TrimSpace(part)
		if part == "" || part == want {
			continue
		}
		out = append(out, part)
	}
	out = append(out, want)
	if len(out) > MaxBadgeSigners {
		out = out[len(out)-MaxBadgeSigners:]
	}
	return strings.Join(out, ",")
}

// HasSigner reports whether that user is named on a `signed` badge.
func HasSigner(badge string, userID int64) bool {
	if userID == 0 {
		return false
	}
	want := strconv.FormatInt(userID, 10)
	for _, part := range strings.Split(badge, ",") {
		if strings.TrimSpace(part) == want {
			return true
		}
	}
	return false
}

// Stamp formats a time the way the record stores it.
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Day is the date half of a stored stamp.
func Day(stamp string) string {
	if len(stamp) >= 10 {
		return stamp[:10]
	}
	return stamp
}

// Validate checks the shape of a new envelope before it is sent.
func (e *Envelope) Validate() error {
	if len(e.Signers) == 0 {
		return errors.New("at least one signer is required")
	}
	if len(e.Signers) > MaxSigners {
		return fmt.Errorf("at most %d signers", MaxSigners)
	}
	if len(e.Fields) > MaxFields {
		return fmt.Errorf("at most %d fields", MaxFields)
	}
	seen := map[string]bool{}
	for _, s := range e.Signers {
		if s.ID == "" {
			return errors.New("a signer has no id")
		}
		if seen[s.ID] {
			return fmt.Errorf("duplicate signer id %q", s.ID)
		}
		seen[s.ID] = true
		if s.Person.Email != "" && !strings.Contains(s.Person.Email, "@") {
			return fmt.Errorf("%q is not an e-mail address", s.Person.Email)
		}
		if s.Person.Email == "" && strings.TrimSpace(s.Person.Name) == "" {
			return errors.New("a signer needs a name or an e-mail address")
		}
	}
	ids := map[string]bool{}
	for _, f := range e.Fields {
		if f.ID == "" {
			return errors.New("a field has no id")
		}
		if ids[f.ID] {
			return fmt.Errorf("duplicate field id %q", f.ID)
		}
		ids[f.ID] = true
		if f.Assignee != "" && !seen[f.Assignee] {
			return fmt.Errorf("field %s is assigned to unknown signer %q", f.ID, f.Assignee)
		}
		if f.Page < 1 {
			return fmt.Errorf("field %s has no page", f.ID)
		}
		if !fields.ValidType(f.Type) {
			return fmt.Errorf("field %s has an unknown type %q", f.ID, f.Type)
		}
	}
	return nil
}
