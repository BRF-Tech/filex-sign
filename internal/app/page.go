package app

import (
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/views"
)

// pageState is the durable record a share of ours carries.
type pageState struct {
	EnvelopeID  string `json:"envelope_id"`
	SignerID    string `json:"signer_id"`
	SubmittedAt string `json:"submitted_at,omitempty"`
}

// pageSigner answers the signing share an outside signer opens: THE SAME
// three steps a signer of this instance gets in the app, drawn from the
// copy of the document the share exposes. Its submission asks for the
// `apply` job, which filex queues as the requester on the original file.
func (a *App) pageSigner(in *wire.ViewEventInput) (*wire.Surface, error) {
	l := langOfView(in)
	var ps pageState
	if err := a.H.ShareState("", &ps); err != nil {
		a.logf("warn", "share state: %v", err)
	}
	visitorIP, _ := pageInfo(in)
	doc := docOf(in, pubRef)
	env, err := a.load(docRef)
	if err != nil {
		return views.Gone(views.T("This signature request no longer exists.", "Bu imza isteği artık yok.")), nil
	}
	// A page event is the one SCREEN of this app that may write, so it
	// carries the same sweep every job does: somebody who turns up after
	// the deadline closes the request — and everybody is told — rather
	// than being handed a form whose signature would be refused.
	env = a.sweepExpired(docRef, env)
	doc.Name = env.Document
	if ps.EnvelopeID != "" && ps.EnvelopeID != env.ID {
		return views.Gone(views.T("This link belongs to an older request for this document.",
			"Bu bağlantı bu belgenin daha eski bir isteğine ait.")), nil
	}
	sg := env.Signer(ps.SignerID)
	if sg == nil {
		return views.Gone(views.T("This link does not belong to a signer of this document.",
			"Bu bağlantı bu belgenin bir imzacısına ait değil.")), nil
	}
	if sg.Status == envelope.SignerSigned {
		return views.Receipt(l, a.receiptOf(docRef, env, sg, l, false)), nil
	}
	if ps.SubmittedAt != "" && !env.Status.Closed() {
		// Submitted, the job is still running (or failed): say so rather
		// than let the person sign twice.
		return views.Received(), nil
	}
	// A request that ended — including one this very visit just swept out
	// of time — is not a form. The visitor came to sign and is told why
	// they cannot, instead of filling in a page whose job would refuse it.
	if env.Status.Closed() {
		if env.Status == envelope.StatusExpired {
			return views.Gone(views.T("This signature request has expired.",
				"Bu imza isteğinin süresi doldu.")), nil
		}
		return views.Gone(views.T("This signature request is closed.",
			"Bu imza isteği kapandı.")), nil
	}

	if in.Event == "open" {
		if next := a.markViewed(docRef, env, sg, visitorIP); next != env {
			env = next
			sg = env.Signer(ps.SignerID)
		}
	}

	s, err := a.fillFlow(l, in, doc, env, sg, false, visitorIP)
	if err != nil || s == nil || s.Job == nil {
		return s, err
	}
	ps.SubmittedAt = envelope.Stamp(a.H.Now())
	if err := a.H.ShareStateSet("", ps); err != nil {
		a.logf("warn", "share state set: %v", err)
	}
	return s, nil
}
