package app

// The audit trail speaks the REQUESTER's language, in letters the PDF has.
//
// It was English only while every screen, notice and mail of the same flow
// followed the person's language (found by an audit of milestone 3, before the first release). The PDF
// embeds a subset of Inter precisely so a Turkish name renders the same in
// every reader; a letter outside that subset is borrowed from another face
// mid-word, so the Turkish trail is checked rune by rune against it.

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/pdfdoc"
	"github.com/brf-tech/filex-sign/internal/views"
)

// turkishHeavy is a finished request with every kind of line the trail can
// print, and Turkish letters in all the places a person can put them.
func turkishHeavy(locale string) *envelope.Envelope {
	at := func(d int) string { return envelope.Stamp(time.Date(2026, 9, d, 9, 30, 0, 0, time.UTC)) }
	return &envelope.Envelope{
		Schema: envelope.SchemaVersion, ID: "e0a1", Status: envelope.StatusCancelled,
		Document:  "Hizmet sözleşmesi — ıİşŞğĞüÜöÖçÇ.pdf",
		Requester: envelope.Person{UserID: burak.ID, Name: "Burak Faruk Şahin", Email: burak.Email},
		CreatedAt: at(20), UpdatedAt: at(22), ClosedAt: at(22),
		Options: envelope.Options{Locale: locale, ExpiryDays: 7, Deadline: "2026-09-30", Lock: true, PIN: "auto",
			Order: envelope.OrderSequential, Message: "Lütfen cuma gününe kadar imzalayın; teşekkürler.", Audit: true},
		Signers: []envelope.Signer{
			{ID: "s1", Kind: envelope.KindInternal, Status: envelope.SignerSigned,
				Person:   envelope.Person{UserID: gokce.ID, Name: "Gökçe Işıktaş", Email: gokce.Email},
				SignedAt: at(21), NotifiedAt: at(20), ViewedAt: at(21), SignedIP: "203.0.113.9",
				CertSerial: "4f2a", CertExpires: "2036-09-21", CertFP: "AB:CD"},
			{ID: "s2", Kind: envelope.KindExternal, Status: envelope.SignerVoid,
				Person:     envelope.Person{Name: "Çağrı Öztürk", Email: "cagri@ornek.com"},
				NotifiedAt: at(21), RemindedAt: at(22)},
			{ID: "s3", Kind: envelope.KindExternal, Status: envelope.SignerDeclined,
				Person:     envelope.Person{Name: "Şule Güneş"},
				DeclinedAt: at(22), DeclineReason: "Ödeme koşulları değişmeli."},
		},
		Fields: []envelope.Field{{ID: "t1", Type: "text", Label: "Görev unvanı", Page: 1, Value: "Genel Müdür", SignedBy: "s1"}},
		Events: []envelope.Event{
			{At: at(20), Type: "created", Note: "3 signer(s)"},
			{At: at(20), Type: envelope.EvNotified, Signer: "s1"},
			{At: at(21), Type: envelope.EvViewed, Signer: "s1", IP: "203.0.113.9"},
			{At: at(21), Type: envelope.EvSigned, Signer: "s1", IP: "203.0.113.9"},
			{At: at(22), Type: envelope.EvReminded, Signer: "s2"},
			{At: at(22), Type: envelope.EvDeclined, Signer: "s3", Note: "Ödeme koşulları değişmeli."},
			{At: at(22), Type: envelope.EvLinkEnded, Signer: "s2", Note: "revoked"},
			{At: at(22), Type: envelope.EvCancelled},
			{At: at(22), Type: envelope.EvExpired},
			{At: at(22), Type: "completed"},
		},
	}
}

func trailText(lines []pdfdoc.AuditLine, title string) string {
	var b strings.Builder
	b.WriteString(title + "\n")
	for _, l := range lines {
		b.WriteString(l.Text + "\n")
	}
	return b.String()
}

func TestAudit_TheTrailSpeaksTheRequestersLanguage(t *testing.T) {
	a, _ := newApp(t)
	for _, tc := range []struct {
		requester, job string
		want, never    []string
	}{
		// ⭐ The case that matters: a Turkish requester, and the trail written
		// by the job of an outside signer whose screen was English.
		{"tr", "en",
			[]string{"İmza denetim izi — ", "Katılımcılar (kimlik:", "Olay günlüğü", "İmza makamı", "Nasıl doğrulanır",
				"dosya donduruldu: evet", "birer birer", "Paylaşımlar ekranında iptal edildi", "3 imzacı",
				"Buradaki imza, bu kurulumun kendi imza makamından gelir", "İptal edildi", "Reddetti", "Görev unvanı (sayfa 1)"},
			[]string{"Signature audit trail", "Event log", "How to verify", "3 signer(s)", "revoked", "outside signer"}},
		{"en", "tr",
			[]string{"Signature audit trail — ", "Participants (identity:", "Event log", "How to verify", "file frozen: yes",
				"revoked on the Shares screen", "3 signer(s)", "A signature made here comes from"},
			[]string{"İmza denetim izi", "Olay günlüğü", "imzacı", "donduruldu"}},
		// A request recorded before its locale was: the job's own language.
		{"", "tr", []string{"İmza denetim izi — "}, []string{"Signature audit trail"}},
	} {
		env := turkishHeavy(tc.requester)
		lines, title := a.auditLines(env, auditLang(env, tc.job))
		text := trailText(lines, title)
		for _, w := range tc.want {
			if !strings.Contains(text, w) {
				t.Errorf("requester %q, job %q: the trail does not say %q:\n%s", tc.requester, tc.job, w, text)
			}
		}
		for _, w := range tc.never {
			if strings.Contains(text, w) {
				t.Errorf("requester %q, job %q: the trail says %q in the wrong language", tc.requester, tc.job, w)
			}
		}
	}
}

// ⚠ Every letter of the Turkish trail is in the embedded Inter subset: none
// of it is borrowed from another face, and none of it is dropped.
func TestAudit_EveryLetterIsInTheEmbeddedFace(t *testing.T) {
	a, _ := newApp(t)
	inter := fontkit.Get(fontkit.Inter)
	for _, lang := range []views.Lang{views.TR, views.EN} {
		env := turkishHeavy(string(lang))
		lines, title := a.auditLines(env, lang)
		seen := map[rune]bool{}
		all := title
		for _, l := range lines {
			all += l.Text
		}
		for _, r := range all {
			if seen[r] || unicode.IsSpace(r) {
				continue
			}
			seen[r] = true
			g, err := inter.Shape(string(r))
			if err != nil {
				t.Fatal(err)
			}
			if len(g) != 1 {
				t.Errorf("%s trail: %q (U+%04X) is not in the Inter subset and would be borrowed or dropped", lang, r, r)
			}
		}
		for _, must := range "ıİşŞğĞüÜöÖçÇ—·“”" {
			if !seen[must] && lang == views.TR {
				t.Errorf("the Turkish fixture no longer exercises %q; the check above proves less than it says", must)
			}
		}
	}
}

// ⭐ Through the flow: the request's LAST signature is an outside signer's,
// made on an English screen, and the trail that lands beside the document
// is the Turkish requester's — byte for byte the one written in Turkish.
func TestAudit_TheLastSignersJobWritesTheRequestersTrail(t *testing.T) {
	a, f := newApp(t)
	s := walkRequest(t, a, "ali@ornek.com", []map[string]any{sigBox("sig-1", "s1", .1), sigBox("sig-2", "s2", .5)},
		map[string]any{"output_mode": envelope.OutputSibling, "output_name": "{stem}-imzali{ext}"})
	// The requester's own job speaks Turkish (jobFrom sets "tr").
	if out, err := a.actionRequest(jobFrom(t, s, burak, docName)); err != nil || !out.OK {
		t.Fatalf("the request failed: %v %v", err, out)
	}
	if env := loadEnv(t, a); env.Options.Locale != "tr" {
		t.Fatalf("the request did not record its requester's language: %q", env.Options.Locale)
	}
	signAs(t, a, f, gokce, "sig-1", nil)

	ali := loadEnv(t, a).Signers[1]
	f.BindShare(ali.PageToken)
	page, err := a.pageSigner(pageEvent("open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4 && page.Job == nil; i++ {
		vals := map[string]any{}
		if page.State["step"] == views.FillStepFill {
			vals = padValue(t, "sig-2")
		}
		if page, err = a.pageSigner(pageEvent("submit", "", page.State, vals)); err != nil {
			t.Fatal(err)
		}
	}
	job := jobFrom(t, fromShare(t, page, ali.PageToken), burak, docName)
	job.Locale = "en" // the outsider's screen was English
	out, err := a.actionApply(job)
	if err != nil || !out.OK {
		t.Fatalf("the last signature failed: %v %v", err, out)
	}
	final := loadEnv(t, a)
	if final.Status != envelope.StatusCompleted {
		t.Fatalf("the request should have completed: %s", final.Status)
	}
	var trail []byte
	for _, o := range f.Outputs {
		if strings.HasSuffix(o.Name, "-audit.pdf") {
			trail = o.Data
		}
	}
	if trail == nil {
		t.Fatal("no audit trail was written beside the document")
	}
	tr, err := a.auditPDF(final, views.TR)
	if err != nil {
		t.Fatal(err)
	}
	en, _ := a.auditPDF(final, views.EN)
	if !bytes.Equal(trail, tr) {
		if bytes.Equal(trail, en) {
			t.Fatal("the trail was written in the LAST SIGNER's language, not the requester's")
		}
		t.Fatal("the trail matches neither language's rendering")
	}
}

// A box named in a script the trail's faces do not carry keeps its line:
// the kind's name stands in rather than an empty "(page 1): …".
func TestAudit_ANameTheFaceCannotDrawStillNamesTheBox(t *testing.T) {
	a, _ := newApp(t)
	env := &envelope.Envelope{ID: "e1", Status: envelope.StatusCompleted, Document: docName,
		Fields: []envelope.Field{
			{ID: "text-1", Type: "text", Page: 1, Label: "会社名", Value: "Acme"},
			{ID: "text-2", Type: "text", Page: 1, Label: "Müşteri adı", Value: "Ayşe"},
		}}
	lines, _ := a.auditLines(env, views.TR)
	var got []string
	for _, l := range lines {
		got = append(got, l.Text)
	}
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "Metin (sayfa 1): Acme") {
		t.Errorf("a name the face cannot draw must fall back to the kind's name:\n%s", joined)
	}
	if !strings.Contains(joined, "Müşteri adı (sayfa 1): Ayşe") {
		t.Errorf("a name the face CAN draw is printed as typed:\n%s", joined)
	}
}
