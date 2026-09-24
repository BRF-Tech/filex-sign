package views

import (
	"sort"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/fontkit"
)

// ── What will not be printed, said while the person types ──────────────
//
// ⚠⚠ The owner (2026-09-21): text in a script the app cannot draw — a name
// in Japanese on an installation with no internet, a letter of a script no
// font exists for — must never vanish from the paper unannounced. So every
// screen where somebody TYPES text that will be printed says, under what
// they typed, which characters will be left out and why; the screen asks
// again at every pause in typing (the host's debounced `change` event), so
// the line appears while they are still writing. And every preview shows
// the text AS IT WILL BE PRINTED (AsPrinted), so what is approved is what
// is stamped.
//
// Where a text is printed decides what the note says will happen to it:
// the consequence sentences below.

// InTheDocument: a box's value, stamped on the page (and quoted by the
// audit trail).
var InTheDocument = T("They will be left out wherever this box's text is printed.",
	"Bu kutunun yazısı nereye basılırsa basılsın bunlar dışarıda kalır.")

// UnderTheSignature: a signer's name, printed under their signature and in
// the audit trail.
var UnderTheSignature = T("The lines under the signature and the audit trail will carry the name without them.",
	"İmzanın altındaki satırlar ve denetim izi adı bunlar olmadan taşır.")

// BoxNameInAudit: a box's name, which only the audit trail prints — and
// which it replaces by the box's kind when it cannot print it whole
// (app/audit.go).
var BoxNameInAudit = T("The audit trail will call the box by its kind instead.",
	"Denetim izi kutuyu bunun yerine türünün adıyla anar.")

// AsPrinted is s as the paper will carry it in face: without what no face
// can draw. Every preview of typed text goes through it.
func AsPrinted(face *fontkit.Face, s string) string {
	out, _ := fontkit.Printed(face, s)
	return out
}

// PrintedLines are the lines printed under a signature, as the stamp's own
// face will draw them.
func PrintedLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, s := range lines {
		out[i] = AsPrinted(fontkit.StampFace(), s)
	}
	return out
}

// PrintNotes says what of value will not be printed in face, one line per
// reason, labelled with the box or person it belongs to. Nothing when all
// of it prints.
func PrintNotes(id, label, value string, face *fontkit.Face, consequence wire.Text) []wire.Node {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	_, missing := fontkit.Printed(face, value)
	if len(missing) == 0 {
		return nil
	}
	byReason := map[string][]fontkit.Missing{}
	var order []string
	for _, m := range missing {
		if _, ok := byReason[m.Reason]; !ok {
			order = append(order, m.Reason)
		}
		byReason[m.Reason] = append(byReason[m.Reason], m)
	}
	var out []wire.Node
	for _, reason := range order {
		ms := byReason[reason]
		chars := missingText(value, ms)
		var msg wire.Text
		switch reason {
		case fontkit.NoFont:
			script := strings.ReplaceAll(ms[0].Script, "_", " ")
			msg = Each(func(l Lang) string {
				return l.Sf("“%s”: %s cannot be printed — no font this app can use draws the %s script. %s",
					"“%s”: %s basılamaz — bu uygulamanın kullanabildiği hiçbir yazı tipi %s yazısını çizmiyor. %s", label, chars, script, In(consequence, l))
			})
		case fontkit.Unavailable:
			msg = Each(func(l Lang) string {
				return l.Sf("“%s”: %s cannot be printed right now — the font for these characters could not be downloaded (this installation may have no internet access, or fonts.gstatic.com is not allowed for this app). %s",
					"“%s”: %s şu anda basılamıyor — bu karakterlerin yazı tipi indirilemedi (bu kurulumun internet erişimi olmayabilir ya da bu uygulamaya fonts.gstatic.com izni verilmemiş olabilir). %s", label, chars, In(consequence, l))
			})
		case fontkit.Downloading:
			msg = Tf("“%s”: the font for %s is still being downloaded. If it has not arrived by the time this is printed, these characters will be left out.",
				"“%s”: %s için yazı tipi hâlâ indiriliyor. Basılacağı ana kadar gelmezse bu karakterler dışarıda kalır.", label, chars)
		default:
			msg = Each(func(l Lang) string {
				return l.Sf("“%s”: %s cannot be printed — no font this app can use has these characters. %s",
					"“%s”: %s basılamaz — bu uygulamanın kullanabildiği hiçbir yazı tipinde bu karakterler yok. %s", label, chars, In(consequence, l))
			})
		}
		out = append(out, wire.Node{ID: "print-note:" + id + ":" + reason, Type: "text",
			Props: map[string]any{"text": msg, "tone": "danger"}})
	}
	return out
}

// missingText shows the characters that will be left out as they stand in
// what was typed: runs of neighbours together ("山田 太郎", not "山 田 太 郎"),
// at most maxShown characters.
func missingText(value string, ms []fontkit.Missing) string {
	const maxShown = 24
	runes := []rune(value)
	at := make([]int, 0, len(ms))
	for _, m := range ms {
		if m.At >= 0 && m.At < len(runes) {
			at = append(at, m.At)
		}
	}
	sort.Ints(at)
	var b strings.Builder
	shown := 0
	for i, p := range at {
		if shown == maxShown {
			b.WriteString("…")
			break
		}
		if i > 0 && p != at[i-1]+1 {
			b.WriteString(" ")
		}
		b.WriteRune(runes[p])
		shown++
	}
	return b.String()
}
