package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/geometry"
	"github.com/brf-tech/filex-sign/internal/stamp"
	"github.com/brf-tech/filex-sign/internal/views"
)

// ── reading what the screens post ──────────────────────────────────────

func valuesOf(in *wire.ViewEventInput) map[string]any {
	if in == nil || in.Data == nil {
		return map[string]any{}
	}
	if v, ok := in.Data["values"].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	switch v := m[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case json.Number:
		return v.String()
	case float64:
		return strings.TrimRight(strings.TrimRight(json.Number(formatFloat(v)).String(), "0"), ".")
	}
	return ""
}

func formatFloat(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func num(m map[string]any, k string) int {
	if m == nil {
		return 0
	}
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	case string:
		neg := false
		s := strings.TrimSpace(v)
		if strings.HasPrefix(s, "-") {
			neg, s = true, s[1:]
		}
		var n int
		for _, c := range s {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		if neg {
			return -n
		}
		return n
	}
	return 0
}

// boolOf reads a checkbox field, which arrives as a bool from a form and
// as a string from an echoed state.
func boolOf(m map[string]any, k string) bool {
	if m == nil {
		return false
	}
	switch v := m[k].(type) {
	case bool:
		return v
	case string:
		return fields.IsTicked(v)
	case float64:
		return v != 0
	}
	return false
}

func has(m map[string]any, k string) bool {
	if m == nil {
		return false
	}
	_, ok := m[k]
	return ok
}

func roundTrip(v any, out any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// wireField is one box as a SCREEN speaks it: the record's field with the
// one key whose shape differs shadowed. On the wire a text box's rule is an
// object (`{kind: "any"|"number"|"email", min?, max?}` —
// docs/APP-PLUGINS-API.md → "Faces and rules"); in the record it is a bare
// word beside the field's own `min_len` / `max_len`.
//
// ⚠⚠ The translation belongs HERE and not in envelope.Field: every
// envelope already on disk holds the bare word, and a model that stopped
// understanding it would take those requests down with it. An anonymous
// struct's promoted key loses to a shallower one of the same name
// (encoding/json), so `rule` lands in Rule below instead of failing the
// unmarshal — which is exactly what used to throw the whole list away.
type wireField struct {
	envelope.Field
	Rule json.RawMessage `json:"rule"`
}

// wireRule is the contract's rule object.
type wireRule struct {
	Kind string `json:"kind"`
	Min  int    `json:"min"`
	Max  int    `json:"max"`
}

// ruleOf reads a posted `rule` in EITHER shape: the contract's object, and
// the bare word an older screen (or a hand-made job) sends. It answers the
// word the record stores, the bounds the object carried, and whether the
// object carried bounds at all — a bare word leaves the box's own
// `min_len` / `max_len` where they were.
//
// Anything else is read as "no rule" rather than as a refusal: one box
// worded oddly must not cost the person every box on the screen.
func ruleOf(raw json.RawMessage) (rule string, minLen, maxLen int, bounded bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return "", 0, 0, false
	}
	if s[0] == '"' {
		var word string
		if err := json.Unmarshal(raw, &word); err != nil {
			return "", 0, 0, false
		}
		return strings.TrimSpace(word), 0, 0, false
	}
	var r wireRule
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", 0, 0, false
	}
	return ruleWord(r.Kind), r.Min, r.Max, true
}

// ruleWord maps the contract's kind to the record's word. `any` — and any
// kind this build does not know, the removed `date` included (v3 §3.3) —
// is free text.
func ruleWord(kind string) string {
	switch strings.TrimSpace(kind) {
	case "number":
		return fields.RuleNumber
	case "email":
		return fields.RuleEmail
	}
	return fields.RuleFree
}

// parseFields reads a `pdf-fields` edit value (the whole fields array)
// and normalises every rule, so nothing a screen sends can put a shape
// the stamper does not understand into the record.
//
// Two things happen here that matter beyond normalising:
//
//	a box placed by an older build with the removed `date` TEXT RULE
//	becomes a date box, which is the one way to ask for a date now;
//	a box that arrives without a name stays unnamed, and every screen
//	shows its kind's name in the reader's own language (views.NameIn), so
//	the signer is never asked to fill in "text-3" — nor "Text" on a
//	Turkish screen.
func parseFields(v any, defaultFont string, l views.Lang) ([]envelope.Field, error) {
	if v == nil {
		return nil, nil
	}
	var posted []wireField
	if err := roundTrip(v, &posted); err != nil {
		return nil, errors.New("the field list is unreadable")
	}
	if len(posted) > envelope.MaxFields {
		return nil, errors.New("too many fields on this document")
	}
	var out []envelope.Field
	for _, w := range posted {
		f := w.Field
		rule, minLen, maxLen, bounded := ruleOf(w.Rule)
		f.Rule = rule
		if bounded {
			// The object carries its own bounds, so it is the whole answer:
			// a bound cleared in the editor has to come back as cleared.
			f.MinLen, f.MaxLen = minLen, maxLen
		}
		out = append(out, f)
	}
	for i := range out {
		f := geometry.Clamp(geometry.Frac{X: out[i].X, Y: out[i].Y, W: out[i].W, H: out[i].H})
		out[i].X, out[i].Y, out[i].W, out[i].H = f.X, f.Y, f.W, f.H
		if out[i].Page < 1 {
			out[i].Page = 1
		}
		out[i].Type, out[i].Rule = fields.Migrate(fields.NormalizeType(out[i].Type), out[i].Rule)
		if fields.Drawn(out[i].Type) {
			out[i].Rule, out[i].Format, out[i].MinLen, out[i].MaxLen = "", "", 0, 0
			// Drawn by hand, or a name typed in a face — nothing else.
			if out[i].Style != "typed" {
				out[i].Style = ""
			}
			// Only lines this build can print, in the order it prints them;
			// an empty choice stays an empty choice (stamp.Chosen).
			if out[i].Lines != nil {
				chosen := stamp.Chosen(out[i].Lines)
				out[i].Lines = &chosen
			}
		} else {
			out[i].Style, out[i].Lines = "", nil
			if out[i].Type == fields.TypeDate {
				// A date box has no text rule: its shape IS the rule.
				out[i].Rule = ""
				out[i].Format = fields.NormalizeFormat(out[i].Format)
			} else {
				out[i].Rule = fields.NormalizeRule(out[i].Rule)
				out[i].Format = ""
			}
			if out[i].MinLen < 0 {
				out[i].MinLen = 0
			}
			if out[i].MaxLen < 0 {
				out[i].MaxLen = 0
			}
		}
		if out[i].Font == "" && defaultFont != "" {
			out[i].Font = defaultFont
		}
		if out[i].Font != "" {
			out[i].Font = fields.NormalizeFont(out[i].Font)
		}
		// ⚠⚠ The NAME is kept exactly as typed (any script), and a box
		// nobody named STAYS unnamed: its kind's name is supplied on each
		// screen in that reader's language (views.NameIn). Writing the
		// default in here baked the requester's language into the record,
		// and a Turkish signer was asked for "Signature" and "Date".
		out[i].Label = clipName(out[i].Label, 60)
		// The identity is derived when the request is sent, never posted.
		out[i].Key = ""
		out[i].Value = clip(out[i].Value, 400)
		out[i].SignedBy = ""
	}
	return out, nil
}

// clipName caps a box's NAME without touching its spaces. ⚠⚠ It is not
// `clip`: this list round-trips through the screen on every `change` while
// the name is being typed, and trimming here hands the browser back a name
// with the space it just typed removed — a two-word name cannot be typed at
// all (the owner, 2026-09-23: "isimde boşluk bırakamıyorum hiç izin
// vermiyor"). A name that is NOTHING BUT spaces is carried as it is too, so
// the step can refuse it and say why (views.go, StepBoxes); the record is
// trimmed once, when the request is sent (request.go).
func clipName(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// blankName reports a name that is there but says nothing — spaces, tabs, an
// invisible separator. It is not the same as no name at all: an unnamed box
// wears its kind's name in the reader's own language, which is a choice,
// while a box named "   " is somebody meaning to name it and not having.
func blankName(name string) bool { return name != "" && strings.TrimSpace(name) == "" }

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n])
}

// filled is one value a signer put into a box: the text (or the PNG of a
// drawing) plus the face they picked.
type filled struct {
	Value string
	Font  string
}

// fill is what a signer filled in: field id → value.
type fill map[string]filled

// parseFill reads the job's `values` parameter, which the screen built
// from its own state: `{"<field id>": {"value": "…", "font": "…"}}`. A
// bare string is accepted too, so a hand-made job still works.
func parseFill(v any) fill {
	out := fill{}
	m, ok := v.(map[string]any)
	if !ok {
		return out
	}
	for id, raw := range m {
		if id == "" {
			continue
		}
		switch e := raw.(type) {
		case string:
			if e != "" {
				out[id] = filled{Value: e}
			}
		case bool:
			if e {
				out[id] = filled{Value: "true"}
			}
		case float64:
			out[id] = filled{Value: formatFloat(e)}
		case map[string]any:
			f := filled{Value: str(e, "value"), Font: str(e, "font")}
			if f.Value == "" {
				if b, ok := e["value"].(bool); ok && b {
					f.Value = "true"
				}
			}
			if f.Value != "" || f.Font != "" {
				out[id] = f
			}
		}
	}
	return out
}

// parsePad reads a `signature-pad` value: {png_b64, mode, font}.
func parsePad(v any) (png, font string) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", ""
	}
	return str(m, "png_b64"), str(m, "font")
}

// parsePeople reads a `people-picker` value, or the list the request job
// is handed. A person with a name and no address is a person.
func parsePeople(v any) []envelope.Person {
	var raw []map[string]any
	if err := roundTrip(v, &raw); err != nil {
		return nil
	}
	var out []envelope.Person
	seen := map[string]bool{}
	for _, m := range raw {
		p := envelope.Person{Email: strings.ToLower(str(m, "email")), Name: clip(str(m, "name"), 120),
			UserID: int64(num(m, "user_id"))}
		key := p.Email
		if key == "" {
			key = "name:" + strings.ToLower(p.Name)
		}
		if (p.Email == "" && p.Name == "") || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

// decodeImage accepts raw base64 or a data: URL.
func decodeImage(b64 string) []byte {
	b64 = strings.TrimSpace(b64)
	if i := strings.Index(b64, ","); i >= 0 && strings.HasPrefix(b64, "data:") {
		b64 = b64[i+1:]
	}
	if b64 == "" {
		return nil
	}
	if b, err := base64.StdEncoding.DecodeString(b64); err == nil {
		return b
	}
	if b, err := base64.RawStdEncoding.DecodeString(b64); err == nil {
		return b
	}
	if b, err := base64.URLEncoding.DecodeString(b64); err == nil {
		return b
	}
	return nil
}

// docOf describes the call's document for the screens. The adapter-
// qualified path is taken from the host's own input when it knows the
// storage, and from `data.path` / the echoed state otherwise.
func docOf(in *wire.ViewEventInput, ref string) views.Doc {
	d := views.Doc{Ref: ref}
	if len(in.Context.Inputs) > 0 {
		d.Name = in.Context.Inputs[0].Name
		d.ReadOnly = in.Context.Inputs[0].ReadOnly
		if p := in.Context.Inputs[0].Path; strings.Contains(p, "://") {
			d.Path = p
		}
	}
	if in.Data != nil {
		if p, ok := in.Data["path"].(string); ok && strings.Contains(p, "://") {
			d.Path = p
		}
	}
	if in.State != nil && d.Path == "" {
		if p, ok := in.State["doc_path"].(string); ok && strings.Contains(p, "://") {
			d.Path = p
		}
	}
	return d
}

// actorPerson is the caller as an identity: a name, an address, or both.
func actorPerson(a *wire.Actor) envelope.Person {
	if a == nil {
		return envelope.Person{Name: "Signer"}
	}
	p := envelope.Person{UserID: a.ID, Name: strings.TrimSpace(a.Name), Email: strings.TrimSpace(a.Email)}
	if p.Name == "" && p.Email == "" {
		p.Name = "Signer"
	}
	return p
}

// firstDrawnField picks where the visible signature goes: the signer's
// own signature box first, an unassigned one second.
func firstDrawnField(fs []envelope.Field, signerID string) *envelope.Field {
	for i := range fs {
		if fields.Drawn(fs[i].Type) && fs[i].Assignee == signerID && signerID != "" {
			return &fs[i]
		}
	}
	for i := range fs {
		if fields.Drawn(fs[i].Type) {
			return &fs[i]
		}
	}
	return nil
}

// pageInfo is the visitor context a share call carries.
func pageInfo(in *wire.ViewEventInput) (visitorIP string, state map[string]any) {
	if in == nil || in.Data == nil {
		return "", nil
	}
	m, ok := in.Data["page"].(map[string]any)
	if !ok {
		return "", nil
	}
	st, _ := m["state"].(map[string]any)
	return str(m, "visitor_ip"), st
}

func parseStamp(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

// outputFromValues reads the "where does it go" part of a form. There is
// no third "custom" choice any more: the NAME is a field of its own,
// shown — and required — only when the answer is a new file.
func outputFromValues(vals map[string]any) envelope.Output {
	mode := str(vals, "output_mode")
	name := str(vals, "output_name")
	switch mode {
	case envelope.OutputSibling:
		if name == "" {
			name = envelope.DefaultSiblingName
		}
		return envelope.Output{Mode: envelope.OutputSibling, Name: name}
	case envelope.OutputNone:
		return envelope.Output{Mode: envelope.OutputNone}
	}
	return envelope.Output{Mode: envelope.OutputVersion}
}
