package app

import (
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
)

// ⚠⚠ What a Turkish reader gets from a JOB, and from the one screen whose
// language the host got wrong (views/turkish_test.go is every screen).
// 2026-09-26, the maintainer: parts of the app were English while filex was
// Turkish.

// An embedded explorer draws Turkish over an account whose language is
// English, and the host told the app "en". The wizard's fields must still
// reach the Turkish screen in Turkish: every text a field shows travels in
// every language, and the screen picks its own.
func TestTurkish_TheWizardSpeaksTheScreensLanguageWhateverTheHostSaid(t *testing.T) {
	a, _ := newApp(t)
	in := requestIn("open", nil, nil, ptr(7))
	in.Context.Locale = "en"
	s, err := a.viewRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := formsOf(s)
	id, ok := fields["identities"]
	if !ok {
		t.Fatal("the first step has no identities field")
	}
	if id.Label != "Identity" {
		t.Fatalf("the plain label is the language the host said: %q", id.Label)
	}
	if id.I18n == nil || id.I18n.Label["tr"] != "Kimlik" ||
		!strings.HasPrefix(id.I18n.Help["tr"], "Her satıra bir imzacı") ||
		id.I18n.Placeholder["tr"] != "Ali Yılmaz <ali@ornek.com>" {
		t.Errorf("a Turkish screen has nothing Turkish to draw for the identities field: %+v", id.I18n)
	}
}

// turkishFailure runs a job and hands back its Turkish message, failing the
// test if it succeeded.
func turkishFailure(t *testing.T, run func() (*wire.ActionRunOutput, error)) string {
	t.Helper()
	out, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if out.OK {
		t.Fatalf("the job was expected to fail: %+v", out.Message)
	}
	return out.Message["tr"]
}

func noEnglish(t *testing.T, what, tr string, english ...string) {
	t.Helper()
	for _, w := range english {
		if strings.Contains(tr, w) {
			t.Errorf("%s: English %q inside the Turkish message: %q", what, w, tr)
		}
	}
}

func TestTurkish_JobFailuresAreSaidInTurkish(t *testing.T) {
	t.Run("signing is off", func(t *testing.T) {
		a, f := newApp(t)
		f.SignUnavailable = true
		tr := turkishFailure(t, func() (*wire.ActionRunOutput, error) {
			return a.actionSign(&wire.ActionRunInput{ActionID: ActionSign, Locale: "tr", Actor: burak,
				Inputs: []wire.FileRef{{Ref: "in:0", Name: docName}}})
		})
		if !strings.Contains(tr, "FILEX_SECRET_KEY ayarlı değil") {
			t.Errorf("the reason is not said in Turkish: %q", tr)
		}
		noEnglish(t, "signing is off", tr, "is not set")
	})

	t.Run("a document that is not a PDF", func(t *testing.T) {
		a, f := newApp(t)
		f.Inputs["in:0"] = []byte("PK\x03\x04 an office document")
		tr := turkishFailure(t, func() (*wire.ActionRunOutput, error) {
			return a.actionSign(&wire.ActionRunInput{ActionID: ActionSign, Locale: "tr", Actor: burak,
				Inputs: []wire.FileRef{{Ref: "in:0", Name: "teklif.docx", Size: 1000}}})
		})
		if !strings.Contains(tr, "“teklif.docx” bir PDF değil") {
			t.Errorf("the refusal is not said in Turkish: %q", tr)
		}
		noEnglish(t, "not a PDF", tr, "not a PDF", "Convert app", "office program", "LibreOffice")
	})

	t.Run("a reminder the host refused", func(t *testing.T) {
		a, f := newApp(t)
		env := twoSigners(t, a)
		var outside *envelope.Signer
		for i := range env.Signers {
			if !env.Signers[i].Internal() && env.Signers[i].Person.HasEmail() {
				outside = &env.Signers[i]
			}
		}
		if outside == nil {
			t.Fatal("no outside signer with an address")
		}
		f.MailErr = &pluginkit.HostError{Code: "rate_limited", Message: "60 mails per hour"}
		tr := turkishFailure(t, func() (*wire.ActionRunOutput, error) {
			return a.actionApply(&wire.ActionRunInput{ActionID: ActionApply, Locale: "tr", Actor: burak,
				Params: map[string]any{"op": "remind", "signer_id": outside.ID},
				Inputs: []wire.FileRef{{Ref: "in:0", Name: docName, Size: 1000}}})
		})
		if !strings.Contains(tr, "çok sık denendi") {
			t.Errorf("the host's refusal is not named in Turkish: %q", tr)
		}
		noEnglish(t, "reminder", tr, "rate_limited", "mails per hour")
	})
}

// A document that cannot be read says so ONCE, in Turkish — not "Bu belge
// okunamadı: Belge okunamadı: verify: malformed PDF…".
func TestTurkish_VerifyOnAnUnreadableFile(t *testing.T) {
	a, f := newApp(t)
	f.Inputs["in:0"] = []byte("%PDF-1.7 this is not really a PDF")
	s, err := a.viewVerify(viewInput(ViewVerify, "open", "", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	words := surfaceWords(s)
	if n := strings.Count(words, "okunamadı"); n != 1 {
		t.Errorf("“okunamadı” is said %d times:\n%s", n, words)
	}
	for _, line := range strings.Split(words, "\n") {
		if strings.HasPrefix(line, "Bu belge okunamadı") {
			noEnglish(t, "verify", line, "verify:", "malformed", "EOF", "failed", "invalid")
		}
	}
}
