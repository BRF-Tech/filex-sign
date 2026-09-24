package envelope

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func two() Envelope {
	return Envelope{
		Schema: SchemaVersion, ID: "env1", Status: StatusSent, Document: "nda.pdf",
		Requester: Person{UserID: 1, Email: "burak@example.com", Name: "Burak"},
		Options:   Options{PIN: "auto", ExpiryDays: 14},
		Signers: []Signer{
			{ID: "s1", Kind: KindExternal, Person: Person{Email: "ayse@example.com", Name: "Ayşe Yılmaz"}, Status: SignerPending, PageToken: "tok1", PageTokenHash: "h1"},
			{ID: "s2", Kind: KindInternal, Person: Person{UserID: 7, Email: "gok@example.com", Name: "Gökçe"}, Status: SignerPending, PageToken: "tok2", PageTokenHash: "h2"},
		},
		Fields: []Field{
			{ID: "sig-1", Type: "signature", Page: 1, X: 0.1, Y: 0.8, W: 0.3, H: 0.08, Assignee: "s1"},
			{ID: "sig-2", Type: "signature", Page: 1, X: 0.6, Y: 0.8, W: 0.3, H: 0.08, Assignee: "s2"},
			{ID: "date-1", Type: "date", Page: 1, X: 0.1, Y: 0.9, W: 0.2, H: 0.03},
		},
		CreatedAt: "2026-09-19T10:00:00Z",
	}
}

var t0 = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

func kinds(effs []Effect) string {
	var parts []string
	for _, e := range effs {
		parts = append(parts, e.Kind+":"+e.Notice+e.Token)
	}
	return strings.Join(parts, ",")
}

func TestApply_Table(t *testing.T) {
	type step struct {
		in      Input
		status  Status
		effects string
		wantErr string
	}
	cases := []struct {
		name  string
		steps []step
	}{
		{"notify then view then sign both", []step{
			{Input{Type: EvNotified, Signer: "s1", At: t0}, StatusSent, "", ""},
			{Input{Type: EvViewed, Signer: "s1", IP: "1.2.3.4", At: t0}, StatusSent, "notify_requester:viewed", ""},
			{Input{Type: EvViewed, Signer: "s1", At: t0}, StatusSent, "", ""}, // second view: no notice
			{Input{Type: EvSigned, Signer: "s1", At: t0, Fields: []Field{{ID: "sig-1"}, {ID: "date-1", Value: "2026-09-19"}}}, StatusInProgress, "revoke_page:tok1,notify_requester:signer_signed", ""},
			{Input{Type: EvSigned, Signer: "s2", At: t0}, StatusCompleted, "revoke_page:tok2,notify_requester:completed", ""},
			{Input{Type: EvSigned, Signer: "s2", At: t0}, StatusCompleted, "", "closed"},
		}},
		{"signing twice is refused", []step{
			{Input{Type: EvSigned, Signer: "s1", At: t0}, StatusInProgress, "revoke_page:tok1,notify_requester:signer_signed", ""},
			{Input{Type: EvSigned, Signer: "s1", At: t0}, StatusInProgress, "", "already signed"},
			{Input{Type: EvReminded, Signer: "s1", At: t0}, StatusInProgress, "", "already signed"},
		}},
		{"unknown signer", []step{
			{Input{Type: EvSigned, Signer: "nope", At: t0}, StatusSent, "", "unknown signer"},
		}},
		{"cancel revokes the open pages only", []step{
			{Input{Type: EvSigned, Signer: "s1", At: t0}, StatusInProgress, "revoke_page:tok1,notify_requester:signer_signed", ""},
			{Input{Type: EvCancelled, At: t0}, StatusCancelled, "revoke_page:tok2,notify_requester:cancelled", ""},
			{Input{Type: EvSigned, Signer: "s2", At: t0}, StatusCancelled, "", "closed"},
			{Input{Type: EvCancelled, At: t0}, StatusCancelled, "", "closed"},
		}},
		{"expiry voids the pending signers", []step{
			{Input{Type: EvExpired, At: t0}, StatusExpired, "revoke_page:tok1,revoke_page:tok2,notify_requester:expired", ""},
		}},
		{"remind moves pending to notified", []step{
			{Input{Type: EvReminded, Signer: "s2", At: t0}, StatusSent, "", ""},
		}},
		{"unknown event", []step{
			{Input{Type: "bogus", At: t0}, StatusSent, "", "unknown event"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := two()
			for i, st := range tc.steps {
				next, effs, err := Apply(env, st.in)
				if st.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), st.wantErr) {
						t.Fatalf("step %d: err = %v, want %q", i, err, st.wantErr)
					}
					// a refused event leaves the envelope untouched
					if next.Status != env.Status || len(next.Events) != len(env.Events) {
						t.Fatalf("step %d: refused event changed the envelope", i)
					}
					continue
				}
				if err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				if next.Status != st.status {
					t.Fatalf("step %d: status = %s, want %s", i, next.Status, st.status)
				}
				if got := kinds(effs); got != st.effects {
					t.Fatalf("step %d: effects = %q, want %q", i, got, st.effects)
				}
				env = next
			}
		})
	}
}

func TestApply_IsPure(t *testing.T) {
	env := two()
	before, _ := Encode(&env)
	next, _, err := Apply(env, Input{Type: EvSigned, Signer: "s1", At: t0})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := Encode(&env)
	if before != after {
		t.Fatal("Apply mutated its input")
	}
	if next.Signers[0].Status != SignerSigned || env.Signers[0].Status != SignerPending {
		t.Fatal("the copy did not diverge from the input")
	}
}

func TestSigned_RecordsFieldsAndCert(t *testing.T) {
	env := two()
	next, _, err := Apply(env, Input{Type: EvSigned, Signer: "s1", IP: "9.9.9.9", At: t0, CertSerial: "abc", CertExpires: "2027-01-01T00:00:00Z",
		Fields: []Field{{ID: "sig-1", Value: "IGNORED"}, {ID: "date-1", Value: "2026-09-19"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := next.Signer("s1")
	if s.SignedIP != "9.9.9.9" || s.CertSerial != "abc" || s.SignedAt != "2026-09-19T12:00:00Z" {
		t.Fatalf("signer facts: %+v", s)
	}
	if next.Fields[0].SignedBy != "s1" || next.Fields[0].Value != "" {
		t.Fatalf("signature field must record the signer and never a value: %+v", next.Fields[0])
	}
	if next.Fields[2].SignedBy != "s1" || next.Fields[2].Value != "2026-09-19" {
		t.Fatalf("date field: %+v", next.Fields[2])
	}
	if next.SignCount != 1 {
		t.Fatalf("sign_count = %d", next.SignCount)
	}
	// The consumed unassigned field is no longer offered to the next signer.
	if got := next.FieldsFor("s2"); len(got) != 1 || got[0].ID != "sig-2" {
		t.Fatalf("FieldsFor(s2) = %+v", got)
	}
}

func TestEncodeDecode_RoundTripAndBudget(t *testing.T) {
	env := two()
	for i := 0; i < 3000; i++ {
		env.Events = append(env.Events, Event{At: "2026-09-19T12:00:00Z", Type: "viewed", Signer: "s1", Note: strings.Repeat("x", 40)})
	}
	s, err := Encode(&env)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) > MaxBytes {
		t.Fatalf("encoded %d bytes > budget", len(s))
	}
	back, err := Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID != "env1" || len(back.Signers) != 2 || back.Signers[0].Person.Name != "Ayşe Yılmaz" {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if _, err := Decode(""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty → %v", err)
	}
	if _, err := Decode(`{"schema": 99}`); err == nil {
		t.Fatal("a newer schema must be refused")
	}
}

func TestValidate(t *testing.T) {
	env := two()
	if err := env.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := two()
	bad.Fields[0].Assignee = "ghost"
	if err := bad.Validate(); err == nil {
		t.Fatal("field assigned to an unknown signer must fail")
	}
	bad = two()
	bad.Signers[1].Person.Email = "nope"
	if err := bad.Validate(); err == nil {
		t.Fatal("a signer without an e-mail must fail")
	}
	bad = two()
	bad.Signers = nil
	if err := bad.Validate(); err == nil {
		t.Fatal("no signers must fail")
	}
}

func TestSignerByTokenHash(t *testing.T) {
	env := two()
	if s := env.SignerByTokenHash("h2"); s == nil || s.ID != "s2" {
		t.Fatal("lookup by token hash")
	}
	if env.SignerByTokenHash("") != nil {
		t.Fatal("empty hash must not match")
	}
}

// ── v2: declining, order, and the lock that must never be forgotten ────

func TestDecline_ClosesTheRequestAndReleasesEverything(t *testing.T) {
	env := two()
	env.Locked, env.LockUntil = true, "2026-10-03T10:00:00Z"
	next, effs, err := Apply(env, Input{Type: EvDeclined, Signer: "s1", At: t0, Note: "Tutar yanlış."})
	if err != nil {
		t.Fatal(err)
	}
	if next.Status != StatusDeclined || !next.Status.Closed() {
		t.Fatalf("status: %v", next.Status)
	}
	if s := next.Signer("s1"); s.Status != SignerDeclined || s.DeclineReason != "Tutar yanlış." || s.DeclinedAt == "" {
		t.Fatalf("signer: %+v", s)
	}
	if s := next.Signer("s2"); s.Status != SignerVoid {
		t.Fatalf("the other signer should be void: %v", s.Status)
	}
	got := kinds(effs)
	for _, want := range []string{"revoke_page:tok1", "revoke_page:tok2", "unlock_file:", "notify_requester:declined"} {
		if !strings.Contains(got, want) {
			t.Errorf("effects %q missing %q", got, want)
		}
	}
}

// Every ending of the flow lifts the lock: that is the one thing this
// plugin must not get wrong, because a forgotten lock freezes the file
// for everyone, administrators included.
func TestEveryClosingEventUnlocks(t *testing.T) {
	for _, in := range []Input{
		{Type: EvCancelled, At: t0},
		{Type: EvExpired, At: t0},
		{Type: EvDeclined, Signer: "s1", At: t0},
	} {
		env := two()
		env.Locked = true
		next, effs, err := Apply(env, in)
		if err != nil {
			t.Fatalf("%s: %v", in.Type, err)
		}
		if !next.Status.Closed() {
			t.Fatalf("%s: did not close", in.Type)
		}
		if !strings.Contains(kinds(effs), EffectUnlockFile) {
			t.Errorf("%s: the lock was not lifted (%s)", in.Type, kinds(effs))
		}
	}
	// And the happy ending too.
	env := two()
	env.Locked = true
	next, _, _ := Apply(env, Input{Type: EvSigned, Signer: "s1", At: t0})
	_, effs, err := Apply(next, Input{Type: EvSigned, Signer: "s2", At: t0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kinds(effs), EffectUnlockFile) {
		t.Errorf("completing the request did not lift the lock (%s)", kinds(effs))
	}
	// An envelope that holds no lock asks for no unlock.
	plain := two()
	_, effs2, _ := Apply(plain, Input{Type: EvCancelled, At: t0})
	if strings.Contains(kinds(effs2), EffectUnlockFile) {
		t.Error("an unlocked document should not be unlocked again")
	}
}

func TestSequential_OnlyOneTurnAtATime(t *testing.T) {
	env := two()
	env.Options.Order = OrderSequential
	if !env.Turn("s1") || env.Turn("s2") {
		t.Fatal("the first signer has the turn in a sequential request")
	}
	if _, _, err := Apply(env, Input{Type: EvSigned, Signer: "s2", At: t0}); !errors.Is(err, ErrNotYourTurn) {
		t.Fatalf("signing out of turn should be refused, got %v", err)
	}
	next, effs, err := Apply(env, Input{Type: EvSigned, Signer: "s1", At: t0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kinds(effs), EffectInvite) {
		t.Fatalf("the next signer must be invited: %s", kinds(effs))
	}
	if e := effs[len(effs)-1]; e.Kind != EffectInvite || e.Signer != "s2" {
		t.Fatalf("the invitation must name the next signer: %+v", e)
	}
	if !next.Turn("s2") {
		t.Fatal("after the first signature it is the second signer's turn")
	}
	// A parallel request never blocks anybody.
	par := two()
	if !par.Turn("s1") || !par.Turn("s2") {
		t.Fatal("a parallel request lets everybody sign")
	}
	if _, _, err := Apply(par, Input{Type: EvSigned, Signer: "s2", At: t0}); err != nil {
		t.Fatalf("a parallel request must not care about order: %v", err)
	}
}

func TestSchemaTwoDecodesAndRefusesTheFuture(t *testing.T) {
	env := two()
	env.Fields[2].Rule = "date"
	env.Fields[2].Format = "DD.MM.YYYY"
	env.Fields[2].Font = "caveat"
	env.Options.Output = Output{Mode: OutputSibling, Name: "{stem}-signed{ext}"}
	env.Options.Lock, env.Options.Audit, env.Options.AllowDecline = true, true, true
	s, err := Encode(&env)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	// ⚠ A v2 record is read, not preserved word for word: the `date` TEXT
	// RULE was removed, and a box that carried it becomes a date box —
	// which is what it always meant. Everything else survives untouched.
	if back.Fields[2].Type != "date" || back.Fields[2].Rule != "" {
		t.Fatalf("an old text+date box should be read as a date box: %+v", back.Fields[2])
	}
	if back.Fields[2].Font != "caveat" || !back.Options.Lock {
		t.Fatalf("v2 fields did not survive the round trip: %+v", back.Fields[2])
	}
	// Nothing was promised to those signers, so nothing is sent to them
	// now: a record written before delivery existed delivers nothing.
	if back.Options.Sends() {
		t.Error("an old request must not start mailing copies it never promised")
	}
	if back.Form {
		t.Error("an old request's boxes were never created as form fields")
	}
	if back.Options.Output.Normalized().Name != "{stem}-signed{ext}" {
		t.Fatalf("output: %+v", back.Options.Output)
	}
	if _, err := Decode(`{"schema":99}`); err == nil {
		t.Error("a record from a newer plugin must be refused, not guessed at")
	}
}

func TestOutputNormalized(t *testing.T) {
	cases := []struct{ in, wantMode, wantName string }{
		{"", OutputVersion, ""},
		{OutputVersion, OutputVersion, ""},
		{OutputNone, OutputNone, ""},
		{OutputSibling, OutputSibling, DefaultSiblingName},
		{"nonsense", OutputVersion, ""},
	}
	for _, c := range cases {
		got := Output{Mode: c.in}.Normalized()
		if got.Mode != c.wantMode || got.Name != c.wantName {
			t.Errorf("%q → %+v, want %s/%s", c.in, got, c.wantMode, c.wantName)
		}
	}
	if got := (Output{Mode: OutputSibling, Name: "x.pdf"}).Normalized(); got.Name != "x.pdf" {
		t.Errorf("a chosen name must survive: %+v", got)
	}
}

// The `signed` badge is a badge, and it names the signers with an
// account so the Signatures screen can say "I signed this" about a
// document that carries no request at all.
func TestSignedBadgeNamesItsSigners(t *testing.T) {
	badge := AddSigner("", 7)
	badge = AddSigner(badge, 1)
	badge = AddSigner(badge, 7) // again: still once, and now last
	if badge != "1,7" {
		t.Fatalf("badge is %q", badge)
	}
	if !HasSigner(badge, 1) || !HasSigner(badge, 7) {
		t.Error("both signers should be on it")
	}
	if HasSigner(badge, 9) {
		t.Error("somebody who did not sign is not on it")
	}
	// Nobody without an account is recorded: the badge answers "did I
	// sign this", and they cannot ask.
	if AddSigner(badge, 0) != badge {
		t.Error("an id of 0 must not be recorded")
	}
	if HasSigner(badge, 0) {
		t.Error("an id of 0 never matches")
	}
	// It stays a badge.
	long := ""
	for i := int64(1); i <= MaxBadgeSigners+5; i++ {
		long = AddSigner(long, i)
	}
	if n := len(strings.Split(long, ",")); n != MaxBadgeSigners {
		t.Errorf("the badge grew to %d entries", n)
	}
	if HasSigner(long, 1) {
		t.Error("the oldest entry should have been dropped")
	}
}
