package app

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/geometry"
	"github.com/brf-tech/filex-sign/internal/host"
	"github.com/brf-tech/filex-sign/internal/pdfdoc"
	"github.com/brf-tech/filex-sign/internal/verify"
	"github.com/brf-tech/filex-sign/internal/views"
)

// completeTwoSigners runs a request with an inside signer (Gökçe) and an
// outside one (ali@ornek.com) to its end and returns the final bytes.
func completeTwoSigners(t *testing.T, a *App, f *host.Fake, result, options map[string]any) []byte {
	t.Helper()
	s := walkRequestFull(t, a, "ali@ornek.com", []map[string]any{
		sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5),
		textBox("text-1", "s1", .1), textBox("text-2", "s2", .5),
	}, result, nil, options)
	if _, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil {
		t.Fatal(err)
	}
	signAs(t, a, f, gokce, "sig-1", map[string]any{"text-1": "Müdür"})
	env := loadEnv(t, a)
	ali := env.Signers[1]
	f.BindShare(ali.PageToken)
	page, err := a.pageSigner(pageEvent("open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4 && page.Job == nil; i++ {
		vals := map[string]any{}
		if page.State["step"] == views.FillStepFill {
			vals = merge(padValue(t, "sig-2"), map[string]any{"text-2": "Tedarikçi"})
		}
		if page, err = a.pageSigner(pageEvent("submit", "", page.State, vals)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := a.actionApply(jobFrom(t, fromShare(t, page, ali.PageToken), burak, docName))
	if err != nil || !out.OK {
		t.Fatalf("the last signature failed: %v %v", err, out)
	}
	return commit(t, f, out)
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ⚠⚠ The owner, 2026-09-22: "after a document is fully signed and someone
// changes it, does the signature say so — or do we lock the file? Both,
// plus a seal." The first signature certifies (P=2), the later one only
// fills and signs (permitted), filex seals the whole document with its own
// key into a LOCKED field, and every party is sent the SHA-256 of exactly
// the bytes that were written — the requester and the inside signer in
// filex, the outside signer by mail with the delivery link.
func TestCompletion_CertifiedSealedHashedToEveryone(t *testing.T) {
	a, f := newApp(t)
	mails, notices := len(f.Mails), len(f.Notices)
	final := completeTwoSigners(t, a, f, nil, map[string]any{"lock_signed": true})

	env := loadEnv(t, a)
	if env.Status != envelope.StatusCompleted || env.Sealed == nil {
		t.Fatalf("completed and sealed expected: %s %+v", env.Status, env.Sealed)
	}
	// The hash is of the bytes WRITTEN — after the seal.
	if env.Sealed.SHA256 != hashOf(final) {
		t.Fatalf("the recorded hash %s is not the written file's %s", env.Sealed.SHA256, hashOf(final))
	}
	if env.Certified != CertifyPermission {
		t.Errorf("the first signature certifies with P=%d, got %d", CertifyPermission, env.Certified)
	}

	rep, err := verify.Inspect(final, bundle(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Signatures) != 3 {
		t.Fatalf("two signatures and the seal expected, got %d", len(rep.Signatures))
	}
	cert, second, seal := rep.Signatures[0], rep.Signatures[1], rep.Signatures[2]
	if cert.Certification != 2 || rep.Certified != 2 {
		t.Errorf("the first signature is the certification (P=2): %d / %d", cert.Certification, rep.Certified)
	}
	if second.Certification != 0 || second.Lock != 0 {
		t.Errorf("the second is a plain approval signature: %+v", second)
	}
	if !seal.Seal || seal.Lock != 1 || !seal.CoversWholeFile || !rep.Sealed {
		t.Errorf("the last is filex's locked seal over the whole file: seal=%v lock=%d whole=%v sealed=%v", seal.Seal, seal.Lock, seal.CoversWholeFile, rep.Sealed)
	}
	for _, s := range rep.Signatures {
		if !s.Valid || !s.Trusted {
			t.Errorf("signature %d (%s) does not check out: %+v", s.Index, s.Name, s.Errors)
		}
		if !s.After.Permitted {
			t.Errorf("signature %d: what followed it was not permitted: %+v", s.Index, s.After.Violations)
		}
	}
	if rep.NotPermitted {
		t.Error("nothing forbidden happened in a normal request")
	}
	if rep.SHA256 != env.Sealed.SHA256 {
		t.Error("Verify's hash of the file is the one recorded")
	}

	// Everybody is told, with the hash and the seal.
	want := []string{env.Sealed.SHA256, env.Sealed.SealFP}
	requester := noticesTitled(f.Notices[notices:], "Tüm imzalar toplandı")
	if len(requester) != 1 {
		t.Fatalf("the requester's completion notice: %d", len(requester))
	}
	insider := noticesTitled(f.Notices[notices:], "İmzalı belge hazır")
	if len(insider) != 1 || insider[0].ToUserID != gokce.ID {
		t.Fatalf("the inside signer's completion notice: %+v", insider)
	}
	for _, n := range append(requester, insider...) {
		for _, w := range want {
			if !strings.Contains(n.Body["en"], w) || !strings.Contains(n.Body["tr"], w) {
				t.Errorf("notice %q lacks %s", n.Title["en"], w)
			}
		}
	}
	var mail *host.FakeMail
	for i := range f.Mails[mails:] {
		m := f.Mails[mails+i]
		if m.To == "ali@ornek.com" && strings.Contains(m.Subject, "İmzalı belge hazır") {
			mail = &m
		}
	}
	if mail == nil {
		t.Fatal("the outside signer was not sent the completion mail")
	}
	for _, w := range append(want, env.DeliveryURL, "Get-FileHash", "sha256sum") {
		if !strings.Contains(mail.Body, w) {
			t.Errorf("the outside signer's mail lacks %q:\n%s", w, mail.Body)
		}
	}

	// The option: the signed file stays locked for good (a new version is
	// the request's own document).
	if !env.Sealed.LockedForGood || len(f.LockedForGood) != 1 || f.LockedForGood[0] != "in:0" {
		t.Errorf("the signed file must be locked for good: %v %v", env.Sealed.LockedForGood, f.LockedForGood)
	}
	// What Verify finds a copy by.
	if v, ok, _ := f.StateGet("in:0", envelope.SealedKey); !ok || !strings.HasPrefix(v, env.Sealed.SHA256+" ") {
		t.Errorf("the sealed hash is not on the document: %q", v)
	}
}

// Without the option the signed file is an ordinary file afterwards — the
// seal and the hash still hold. A file BESIDE the original is locked
// through the job's own output when the option is on.
func TestCompletion_TheLockIsAChoice(t *testing.T) {
	a, f := newApp(t)
	completeTwoSigners(t, a, f, nil, nil)
	if env := loadEnv(t, a); env.Sealed == nil || env.Sealed.LockedForGood || len(f.LockedForGood) != 0 {
		t.Errorf("no lock was asked for: %+v %v", env.Sealed, f.LockedForGood)
	}

	b, g := newApp(t)
	completeTwoSigners(t, b, g, map[string]any{"output_mode": envelope.OutputSibling, "output_name": "{stem}-signed{ext}"}, map[string]any{"lock_signed": true})
	if len(g.LockedForGood) != 1 || !strings.HasPrefix(g.LockedForGood[0], "out:") {
		t.Errorf("a signed file beside the original is locked through the output: %v", g.LockedForGood)
	}
}

// ⚠⚠ What the certification and the seal are FOR: a change after the
// completion is reported as NOT PERMITTED. A redrawn page breaks both the
// certification (P=2) and the seal's lock (P=1); a mere form value changed
// afterwards is still form filling to the certification — but not to the
// seal, which closed the document.
func TestCompletion_ChangesAfterTheSealAreNotPermitted(t *testing.T) {
	a, f := newApp(t)
	final := completeTwoSigners(t, a, f, nil, nil)

	redrawn, err := pdfdoc.Stamp(final, []pdfdoc.Item{{Page: 1, Kind: pdfdoc.KindText, Text: "PAID IN FULL",
		Font: fontkit.Inter, Rect: geometry.Rect{X: 72, Y: 300, W: 300, H: 40}}})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := verify.Inspect(redrawn, bundle(t, f))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.NotPermitted {
		t.Fatal("a page redrawn after the seal must be reported as not permitted")
	}
	cert, seal := rep.Signatures[0], rep.Signatures[len(rep.Signatures)-1]
	if cert.After.Permitted || !hasViolation(cert.After.Violations, "page_content") {
		t.Errorf("the certification must name the redrawn page: %+v", cert.After)
	}
	if seal.After.Permitted {
		t.Error("the seal's lock is broken by any change")
	}
	if rep.Sealed {
		t.Error("a file changed after the seal is no longer the sealed file")
	}

	refilled, err := pdfdoc.FillForm(final, []pdfdoc.FieldValue{{Name: loadEnv(t, a).Field("text-1").FormName(), Kind: pdfdoc.KindText, Text: "Somebody else", Font: fontkit.Inter}})
	if err != nil {
		t.Fatal(err)
	}
	rep, err = verify.Inspect(refilled, bundle(t, f))
	if err != nil {
		t.Fatal(err)
	}
	cert, seal = rep.Signatures[0], rep.Signatures[len(rep.Signatures)-1]
	if !cert.After.Permitted {
		t.Errorf("to the certification alone a changed form value is form filling: %+v", cert.After)
	}
	if seal.After.Permitted || !rep.NotPermitted {
		t.Error("the seal closed the document: a form value changed after it is not permitted")
	}
}

func hasViolation(vs []verify.Violation, kind string) bool {
	for _, v := range vs {
		if v.Kind == kind {
			return true
		}
	}
	return false
}

// The Verify screen answers the owner's four questions on the finished
// file — every signature valid, certified, sealed, and the hash the one
// every party was sent — and, after a forbidden change, says so first.
func TestVerify_TheSealedFileAndAChangedOne(t *testing.T) {
	a, f := newApp(t)
	final := completeTwoSigners(t, a, f, nil, nil)
	s, err := a.viewVerify(viewInput(ViewVerify, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	words := surfaceWords(s)
	for _, want := range []string{"Every signature is valid", "Certified by", "Sealed by filex", "SHA-256 of this file: " + hashOf(final),
		"This is exactly the file whose SHA-256 every party was sent", "Her imza geçerli", "filex tarafından mühürlendi"} {
		if !strings.Contains(words, want) {
			t.Errorf("the report does not say %q", want)
		}
	}
	if strings.Contains(words, "NOT permit") {
		t.Error("nothing forbidden happened")
	}

	redrawn, err := pdfdoc.Stamp(final, []pdfdoc.Item{{Page: 1, Kind: pdfdoc.KindText, Text: "PAID",
		Font: fontkit.Inter, Rect: geometry.Rect{X: 72, Y: 300, W: 200, H: 40}}})
	if err != nil {
		t.Fatal(err)
	}
	f.Inputs["in:0"] = redrawn
	s, err = a.viewVerify(viewInput(ViewVerify, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	words = surfaceWords(s)
	for _, want := range []string{"do NOT permit", "changes not permitted", "Not permitted by the certification: page 1 draws something different",
		"Not permitted by filex's seal", "is NOT the one every party was sent"} {
		if !strings.Contains(words, want) {
			t.Errorf("after a change the report does not say %q", want)
		}
	}
}

// The trail records the certification, the seal and the hash.
func TestAudit_RecordsTheSealAndTheHash(t *testing.T) {
	a, f := newApp(t)
	final := completeTwoSigners(t, a, f, nil, map[string]any{"lock_signed": true})
	env := loadEnv(t, a)
	lines, _ := a.auditLines(env, views.EN)
	var all strings.Builder
	for _, l := range lines {
		all.WriteString(l.Text + "\n")
	}
	for _, want := range []string{"Certified by the first signature", "Sealed by filex", env.Sealed.SealFP, hashOf(final), "locked for good"} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("the audit trail does not record %q", want)
		}
	}
}

// Every language's completion mail carries the hash, the seal and how to
// check — not only the one the other tests happen to run in.
func TestDeliveryMail_CarriesTheHashInEveryLanguage(t *testing.T) {
	env := &envelope.Envelope{Document: "contract.pdf", Requester: envelope.Person{Name: "Burak"},
		Options:     envelope.Options{Deliver: envelope.DeliverShare, DeliveryPIN: "none"},
		DeliveryURL: "https://files.example/s/abc",
		Sealed:      &envelope.Sealed{SHA256: strings.Repeat("ab", 32), SealFP: "AAAA BBBB", Output: "contract.pdf"}}
	sg := &envelope.Signer{Person: envelope.Person{Email: "ali@ornek.com"}}
	for _, lang := range views.Languages() {
		_, body := deliveryMail(lang, env, sg)
		for _, want := range []string{env.Sealed.SHA256, env.Sealed.SealFP, env.DeliveryURL, "Get-FileHash", "sha256sum"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: the completion mail lacks %q", lang, want)
			}
		}
	}
}
