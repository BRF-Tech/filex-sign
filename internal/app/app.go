// Package app wires the plugin together: the action jobs (sign,
// request, fill, apply, verify), the screens, the signing share and the
// receipt share, all over the host.Host seam so the same code runs
// inside filex and under `go test` with host.Fake.
package app

import (
	"crypto/x509"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fontkit"
	"github.com/brf-tech/filex-sign/internal/host"
	"github.com/brf-tech/filex-sign/internal/verify"
	"github.com/brf-tech/filex-sign/internal/views"
)

// Names the manifest and the code must agree on (checked by a test).
const (
	ActionSign    = "sign"
	ActionRequest = "request"
	ActionFill    = "fill"
	ActionApply   = "apply"
	ActionVerify  = "verify"
	// ActionConvert is the hidden second half of "Convert to PDF": its own
	// action so the operations tray names what is happening ("PDF'e
	// dönüştür"), not "Sign…".
	ActionConvert = "convert"
	ViewSignSelf  = "sign-self"
	ViewRequest   = "request"
	ViewFill      = "sign-fill"
	ViewStatus    = "status"
	ViewVerify    = "verify"
	ViewHome      = "envelopes"
	PageSigner    = "signer"
	PageReceipt   = "receipt"
	// Lock reasons: manifest `messages`, said by filex in each reader's
	// language (pluginkit.FileLockMessage).
	LockCollecting = "lock.collecting"
	LockSealed     = "lock.sealed"
)

// Settings the administrator may set.
const (
	SettingTSAEnabled = "tsa_enabled"
	SettingTSAURL     = "tsa_url"
	// DefaultTSA is the endpoint offered when time stamping is switched
	// on without naming one.
	DefaultTSA = "https://freetsa.org/tsr"
	// TSAHost is the manifest's `http:` grant that matches DefaultTSA.
	TSAHost = "freetsa.org"
)

// Limits.
const (
	// certDays is the leaf's lifetime. It is LONG on purpose: a verifier
	// checks a certificate against the clock it is run with, so a
	// thirty-day leaf makes every signature read as "the certificate has
	// expired" a month later. There is no key risk in the long horizon —
	// the private key is destroyed seconds after the signature is
	// written, so the certificate outliving it proves identity and can
	// sign nothing.
	certDays        = 3650
	pngBudget       = 40 << 10 // the composed pad image handed through job params
	drawingsBudget  = 48 << 10 // every drawing of one signer together, inside a job's 64 KiB
	maxInspectBytes = 24 << 20 // the details panel skips counting signatures above this
	receiptTTLDays  = 30
	docRef          = "in:0"
	pubRef          = "pub:0"
	officeEngine    = "libreoffice"
	convertTimeoutS = 180
)

// App is the plugin.
type App struct {
	H host.Host
	M wire.Manifest
}

// New binds the host — and the fonts: a face for a script the bundled ones
// do not draw is fetched through the host on demand (fontkit/noto.go).
func New(h host.Host, m wire.Manifest) *App {
	a := &App{H: h, M: m}
	fontkit.SetFetcher(a.fetchFont)
	return a
}

// fetchFont hands fontkit a pinned Noto face: from the host's cache, or
// downloaded once through asset_fetch (the manifest's http:fonts.gstatic.com
// permission; the host checks the sha256 before the app sees a byte).
//
// ⚠ A face that cannot be had (an installation with no internet, a server
// that refuses) is not an error of the call: the text that needed it is
// reported as unprintable and the screens say so. It is NOT logged here:
// every call is a fresh module, so "once" cannot be remembered on this
// side — measured 2026-09-21 on an installation with no internet, three
// pauses in typing wrote fifteen identical lines. The host says it, once
// per outage (asset_fetch).
//
// A face the host is still downloading (`timeout`: the download outlives
// the call, see asset_fetch) is ErrStillDownloading: "a moment", not "the
// network is down".
func (a *App) fetchFont(f fontkit.NotoFont) ([]byte, error) {
	data, cached, err := a.H.Asset(f.URL, f.SHA256, f.Size)
	if err != nil {
		var he *pluginkit.HostError
		if errors.As(err, &he) && he.Code == wire.ErrTimeout {
			return nil, fmt.Errorf("%w: %v", fontkit.ErrStillDownloading, err)
		}
		return nil, err
	}
	if !cached {
		a.logf("info", "font %s fetched once and kept (%d bytes, sha256 verified)", f.Family, len(data))
	}
	return data, nil
}

// call runs one exported handler with a clean slate for the fonts: a face
// that could not be fetched is not asked for again within ONE call, and is
// asked for again in the next (the network may be back).
func call[I, O any](fn func(I) (O, error)) func(I) (O, error) {
	return func(in I) (O, error) {
		fontkit.BeginCall()
		return fn(in)
	}
}

// Plugin is what cmd/plugin registers with pluginkit.Run.
func (a *App) Plugin() *pluginkit.Plugin {
	return &pluginkit.Plugin{
		Manifest: a.M,
		Actions: map[string]pluginkit.ActionFunc{
			ActionSign:    call(a.actionSign),
			ActionRequest: call(a.actionRequest),
			// "Sign / Fill" is the menu row a signer uses while a request is
			// open; its screen queues `apply`, and a direct run means the
			// same thing, so it lands in the same handler.
			ActionFill:    call(a.actionApply),
			ActionApply:   call(a.actionApply),
			ActionVerify:  call(a.actionVerify),
			ActionConvert: call(a.actionConvert),
		},
		Views: map[string]pluginkit.ViewFunc{
			ViewSignSelf: call(a.viewSignSelf),
			ViewRequest:  call(a.viewRequest),
			ViewFill:     call(a.viewFill),
			ViewStatus:   call(a.viewStatus),
			ViewVerify:   call(a.viewVerify),
			ViewHome:     call(a.viewHome),
		},
		Pages: map[string]pluginkit.PageFunc{
			PageSigner:  call(a.pageSigner),
			PageReceipt: call(a.pageReceipt),
		},
		// The hourly wake-up (manifest permission `schedule`, see tick.go).
		// filex refuses at install an app that asks for the permission and
		// exports no tick, so these two go together or not at all.
		Tick: call(a.Tick),
	}
}

// fail is a job answer in every language.
func fail(en, tr string) (*wire.ActionRunOutput, error) { return failText(views.T(en, tr)) }

func failf(en, tr string, args ...any) (*wire.ActionRunOutput, error) {
	return failText(views.Tf(en, tr, args...))
}

// failText is a job answer already in words.
func failText(t wire.Text) (*wire.ActionRunOutput, error) {
	return &wire.ActionRunOutput{OK: false, Message: t}, nil
}

// failPlain is a job answer that is somebody else's words — an engine's
// error — the same in every language.
func failPlain(msg string) (*wire.ActionRunOutput, error) { return failText(views.Plain(msg)) }

// stem strips the extension.
func stem(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i]
	}
	return name
}

// expandName renders an output name pattern the way filex does:
// {stem}, {ext} (with its dot) and {name}.
func expandName(pattern, inputName string) string {
	base := path.Base(inputName)
	ext := path.Ext(base)
	st := strings.TrimSuffix(base, ext)
	if strings.TrimSpace(pattern) == "" {
		pattern = envelope.DefaultSiblingName
	}
	out := strings.NewReplacer("{stem}", st, "{ext}", ext, "{name}", base).Replace(pattern)
	if path.Ext(out) == "" {
		out += ".pdf"
	}
	return out
}

func (a *App) logf(level, format string, args ...any) {
	a.H.Log(level, fmt.Sprintf(format, args...))
}

// ── the signing authority ──────────────────────────────────────────────

// authority is what the screens say about who stands behind a signature.
type authority struct {
	OK      bool
	Reason  string
	Bundle  string // every CA of this tenant, retired ones included
	Live    *x509.Certificate
	Name    string
	FP      string
	Retired int
}

// ca asks the host once and keeps nothing: an authority can be imported
// or rotated between two calls, and a stale copy would make the report
// lie about who signed.
func (a *App) ca() authority {
	info, err := a.H.SignInfo()
	if err != nil {
		return authority{Reason: err.Error()}
	}
	out := authority{OK: info.Available, Reason: info.Reason, Bundle: info.Authorities}
	if !out.OK && out.Reason == "" {
		out.Reason = "the administrator has not enabled signing"
	}
	certs := verify.ParsePEM(info.Authorities)
	if len(certs) > 0 {
		out.Live = certs[0]
		out.Name = certName(certs[0])
		out.FP = verify.Fingerprint(certs[0])
		out.Retired = len(certs) - 1
	}
	return out
}

func certName(c *x509.Certificate) string {
	if c == nil {
		return ""
	}
	if cn := strings.TrimSpace(c.Subject.CommonName); cn != "" {
		return cn
	}
	if len(c.Subject.Organization) > 0 {
		return c.Subject.Organization[0]
	}
	return c.Subject.String()
}

// signingReady checks the host CA before any work is done.
func (a *App) signingReady() (string, bool) {
	c := a.ca()
	if !c.OK {
		// The host's own words stay here; a person reads
		// views.SigningUnavailable.
		a.logf("warn", "signing is not available: %s", c.Reason)
	}
	return c.Reason, c.OK
}

// tsa is the time-stamping authority the administrator switched on, or
// "" for none. It is OFF by default: a stamp is a network call in the
// middle of signing, and with a ten-year leaf the expiry problem it
// solves does not arise on an installation like this one.
func (a *App) tsa() string {
	on, _, err := a.H.Setting(SettingTSAEnabled)
	if err != nil || !truthy(on) {
		return ""
	}
	url, _, err := a.H.Setting(SettingTSAURL)
	if err != nil || strings.TrimSpace(url) == "" {
		return DefaultTSA
	}
	return strings.TrimSpace(url)
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "evet":
		return true
	}
	return false
}

// lang is the language one call is answered in.
func langOfView(in *wire.ViewEventInput) views.Lang { return views.Of(in.Context.Locale) }

// ── what a JOB shows a person ──────────────────────────────────────────
//
// ⚠ A screen is not the only place words reach somebody. A job's
// progress line sits in the tray, a share's subject heads the page a
// stranger opens, the reason is printed inside the signature by every
// PDF reader, and the freeze reason is what a refused rename says. Every
// one of them follows the call's language, for the same reason a label
// does: half a language is a bug, wherever it shows up.

// say picks the call's words (formatting them when given arguments).
func (a *App) say(locale, en, tr string, args ...any) string {
	if len(args) == 0 {
		return views.Of(locale).S(en, tr)
	}
	return views.Of(locale).Sf(en, tr, args...)
}

// step reports progress in the caller's language.
func (a *App) step(locale string, done, total int64, en, tr string, args ...any) {
	a.H.Progress(done, total, a.say(locale, en, tr, args...))
}
