// Package stamp is what is printed under a signature: the catalogue of
// facts a requester may choose from, and the ONE function that turns the
// chosen facts into lines of text.
//
// ⚠⚠ One function, three readers. The define step previews the lines
// (views: the catalogue's examples), the signer previews them before
// pressing Sign (views: the approve step), and the job prints them
// (pdfsig.Compose). If any of the three worded a line itself, the preview
// and the paper would drift — the owner's requirement, 2026-09-21, is that
// "the signer sees a preview of exactly what will be stamped under their
// signature before they sign", and an IP address is personal data that
// must not reach the paper unseen.
//
// Every VALUE comes from the signing record (Facts), never from anything
// the requester typed: the requester chooses WHICH facts, nothing else.
package stamp

import (
	"strings"
	"time"

	"github.com/brf-tech/filex-sign/internal/i18n"
)

// The facts a signature can carry under it, in the order they are
// offered and printed.
const (
	Name      = "name"
	Email     = "email"
	Date      = "date"
	IP        = "ip"
	Cert      = "cert"
	Serial    = "serial"
	Authority = "authority"
)

// IDs lists every line, in catalogue (and printing) order.
func IDs() []string { return []string{Name, Email, Date, IP, Cert, Serial, Authority} }

// Defaults are the lines a box prints when nobody chose: exactly what
// every signature carried before the choice existed — the name, and the
// date.
func Defaults() []string { return []string{Name, Date} }

// Known reports whether id is a line this build can print.
func Known(id string) bool {
	for _, k := range IDs() {
		if k == id {
			return true
		}
	}
	return false
}

// Chosen is the lines a box prints: its own choice filtered to known ids
// in catalogue order, or the defaults when it made none (nil). An empty,
// non-nil choice is "nothing under the signature" and stays empty.
func Chosen(choice *[]string) []string {
	if choice == nil {
		return Defaults()
	}
	want := map[string]bool{}
	for _, id := range *choice {
		want[id] = true
	}
	out := []string{}
	for _, id := range IDs() {
		if want[id] {
			out = append(out, id)
		}
	}
	return out
}

// Facts is what the signing record holds at the moment the signature is
// written. A preview fills what it already knows and marks the rest
// Pending: the certificate does not exist until the signer presses Sign.
type Facts struct {
	Name      string
	Email     string
	When      time.Time
	IP        string
	CertFP    string // grouped hex, "3F2A 9C1B …"
	Serial    string
	Authority string
	// PendingCert: the certificate does not exist yet (a preview), so its
	// lines say when it will.
	PendingCert bool
	// PendingIP: the address is not known yet — the REQUESTER's preview of
	// somebody else's signature. ⚠ Never set on the signer's own preview:
	// their address is known there, and a line promised as "given when you
	// sign" that then turned out empty would be a preview that lied.
	PendingIP bool
	// PendingWho words the pending lines for the person reading them:
	// "you" on the signer's own preview, "they" on the requester's.
	PendingWho Who
}

// Who a pending line speaks to.
type Who int

const (
	// You: the signer's own preview.
	You Who = iota
	// They: the requester's preview of somebody else's signature.
	They
)

// DateLine is the date line exactly as it is printed.
//
// ⚠ The date's LAYOUT is words too: a German reader writes 22.09.2026, a
// French or Spanish one 22/09/2026. The layout is a catalogue entry (the
// English key is Go's reference date), so each language prints the date
// the way its readers write it.
func DateLine(when time.Time, lang string) string {
	l := i18n.Of(lang)
	return l.Sf("Signed %s UTC", "İmza tarihi: %s UTC", when.UTC().Format(l.S("2006-01-02 15:04", "02.01.2006 15:04")))
}

// Lines turns the chosen facts into what is printed, in lang (the
// request's language — the paper speaks one language, whoever reads the
// screen). A fact the record does not hold — no e-mail address, no IP —
// is LEFT OUT rather than printed as a dash: a line under a signature that
// says nothing is noise on a legal document.
func Lines(lang string, ids []string, f Facts) []string {
	var out []string
	for _, id := range ids {
		if s := line(lang, id, f); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func line(lang, id string, f Facts) string {
	switch id {
	case Name:
		return strings.TrimSpace(f.Name)
	case Email:
		if e := strings.TrimSpace(f.Email); strings.Contains(e, "@") {
			return e
		}
	case Date:
		if !f.When.IsZero() {
			return DateLine(f.When, lang)
		}
	case IP:
		if ip := strings.TrimSpace(f.IP); ip != "" {
			return say(lang, "IP address: ", "IP adresi: ") + ip
		}
		if f.PendingIP {
			return say(lang, "IP address: ", "IP adresi: ") + pending(lang, f.PendingWho)
		}
	case Cert:
		if fp := strings.TrimSpace(f.CertFP); fp != "" {
			return say(lang, "Certificate SHA-256: ", "Sertifika SHA-256: ") + shortFP(fp)
		}
		if f.PendingCert {
			return say(lang, "Certificate SHA-256: ", "Sertifika SHA-256: ") + pending(lang, f.PendingWho)
		}
	case Serial:
		if s := strings.TrimSpace(f.Serial); s != "" {
			return say(lang, "Certificate serial: ", "Sertifika seri no: ") + s
		}
		if f.PendingCert {
			return say(lang, "Certificate serial: ", "Sertifika seri no: ") + pending(lang, f.PendingWho)
		}
	case Authority:
		if a := strings.TrimSpace(f.Authority); a != "" {
			return say(lang, "Signing authority: ", "İmza makamı: ") + a
		}
	}
	return ""
}

// shortFP is the first four groups of a grouped fingerprint and an
// ellipsis. ⚠ A whole SHA-256 is 79 characters and would be shrunk to
// nothing in a signature box; four groups are enough to match against the
// receipt and the verification report, which carry the whole thing.
func shortFP(fp string) string {
	parts := strings.Fields(fp)
	if len(parts) <= 4 {
		return strings.Join(parts, " ")
	}
	return strings.Join(parts[:4], " ") + "…"
}

func pending(lang string, who Who) string {
	if who == They {
		return say(lang, "given when they sign", "imzaladıklarında verilir")
	}
	return say(lang, "given when you sign", "imzaladığınızda verilir")
}

func say(lang, en, tr string) string { return i18n.Of(lang).S(en, tr) }
