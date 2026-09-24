package app

import (
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/verify"
	"github.com/brf-tech/filex-sign/internal/views"
)

// report reads a document and says what its signatures prove. The trust
// pool is the WHOLE bundle of this tenant's signing authorities, retired
// ones included — trusting only the live one would make every signature
// made before a rotation read as untrusted.
func (a *App) report(ref string) (*verify.Report, error) {
	raw, err := a.H.ReadInput(ref)
	if err != nil {
		return nil, err
	}
	return verify.Inspect(raw, a.ca().Bundle)
}

// viewVerify draws the report. It is offered on EVERY PDF — a document
// this installation did not sign is the one that most needs checking, so
// the state keys are never used as a gate here.
func (a *App) viewVerify(in *wire.ViewEventInput) (*wire.Surface, error) {
	l := langOfView(in)
	doc := docOf(in, docRef)
	out := views.VerifyInput{Doc: doc, Timestamps: a.tsa() != ""}
	if len(in.Context.Inputs) > 0 && in.Context.Inputs[0].Size > maxInspectBytes {
		out.Err = l.S("the document is too large to check here", "belge burada denetlenemeyecek kadar büyük")
		return views.Verify(l, out), nil
	}
	rep, err := a.report(docRef)
	if err != nil {
		out.Err = views.In(intakeWords(err, doc.Name), l)
		return views.Verify(l, out), nil
	}
	out.Report = rep
	out.Sent = a.sentHash(rep.SHA256)
	return views.Verify(l, out), nil
}

// sentHash finds the completion record for a file by its hash: the
// request that sealed exactly these bytes (state key `sealed` on the
// request's document, "<sha256> <signed file name>"), wherever this copy
// of the file lives. The record ON this file comes first — a request whose
// signed file went in as a new version sealed this very file, and a hash
// that differs from its record is the one answer that matters most.
func (a *App) sentHash(sum string) *views.SentHash {
	if env, err := a.load(docRef); err == nil && env.Sealed != nil && env.Options.Output.Normalized().Mode == envelope.OutputVersion {
		return &views.SentHash{SHA256: env.Sealed.SHA256, Document: env.Document, Output: env.Sealed.Output,
			Matches: env.Sealed.SHA256 == sum}
	}
	items, err := a.H.StateList(envelope.SealedKey, homeListLimit)
	if err != nil {
		return nil
	}
	for _, it := range items {
		if h, name, _ := strings.Cut(it.Value, " "); h == sum {
			return &views.SentHash{SHA256: h, Document: it.Name, Output: name, Matches: true}
		}
	}
	return nil
}

// countSignatures is what the details panel shows beside the request.
func (a *App) countSignatures(in *wire.ViewEventInput) int {
	if len(in.Context.Inputs) == 0 || in.Context.Inputs[0].Size > maxInspectBytes {
		return 0
	}
	if ext := extOf(in.Context.Inputs[0].Name); ext != "" && ext != "pdf" {
		return 0
	}
	rep, err := a.report(docRef)
	if err != nil || rep == nil {
		return 0
	}
	return len(rep.Signatures)
}
