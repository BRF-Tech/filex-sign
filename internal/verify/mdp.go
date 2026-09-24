package verify

import (
	"bytes"
	"io"
	"regexp"
	"sort"
	"strconv"

	"github.com/digitorus/pdf"
)

// ── What changed after a signature, and whether it was allowed ─────────
//
// ⚠⚠ The owner, 2026-09-22: after completion, ANY change must make a reader
// report "changes not permitted" — not merely "modified after signing".
// That verdict is the reader's (Adobe's) difference analysis against the
// document's permissions; this is the same analysis, conservatively:
//
//   - the CERTIFICATION (the first signature, DocMDP P) sets what may happen
//     to the document after it: P=2 permits filling in the form and signing;
//   - a LOCKED signature (its field's /Lock P, the platform seal's P=1)
//     permits nothing after it at all;
//   - every incremental update after a signature is read object by object
//     and classified: a form field's value and appearance, a signature, a
//     new signature field are FORM FILLING; the document's own metadata and
//     long-term validation data change NOTHING; a page redrawn, an
//     annotation added, a font or a stream rewritten, the catalogue changed
//     are OTHER — permitted by no policy this app uses.
//
// The rules follow the ones readers apply (pyHanko's difference analysis
// models Acrobat's; the key sets below are its). What is not recognised is
// OTHER: a verifier that is unsure says "not permitted", never "fine".

// Change levels, lowest first.
const (
	levelNone  = 0 // metadata, DSS, unreferenced new objects
	levelForm  = 2 // filling in the form, signing
	levelOther = 4 // anything else
)

// Violation is one change a policy did not permit.
type Violation struct {
	// Kind: page_content | page | annotation | field | catalog | form | object.
	Kind string
	// Page is the 1-based page for page_content/page/annotation.
	Page int
	// Field is the field's name for field.
	Field string
	// Object is the object number.
	Object int
}

// Permissions is what the document allowed after one signature, and
// whether the changes made since held to it.
type Permissions struct {
	// Policy is the strictest permission in force after the signature: the
	// certification's P, lowered by any lock (1 = no change, 2 = form
	// filling and signing, 3 = … and annotations); 0 = no policy.
	Policy int
	// Permitted: the changes after this signature stay inside Policy (true
	// when there were none).
	Permitted bool
	// Violations name what was not permitted.
	Violations []Violation
	// Analysed is false when the revisions could not be read.
	Analysed bool
}

var objRe = regexp.MustCompile(`(?m)(?:^|[\r\n\s])(\d{1,10})\s+(\d{1,5})\s+obj\b`)

// valueUpdateKeys are the keys filling a field may change (a value, its
// appearance, its flags) — pyHanko's VALUE_UPDATE_KEYS.
var valueUpdateKeys = map[string]bool{"V": true, "AP": true, "AS": true, "Ff": true, "F": true, "DA": true, "Q": true, "Type": true}

// catalogueExempt are the catalogue keys that may change: the form (checked
// separately), long-term validation data and metadata.
var catalogueExempt = map[string]bool{"AcroForm": true, "DSS": true, "Extensions": true, "Metadata": true, "MarkInfo": true, "Version": true}

// formExempt are the form dictionary's keys that may change when fields
// are filled or a signature field is added.
var formExempt = map[string]bool{"Fields": true, "DR": true, "DA": true, "Q": true, "NeedAppearances": true, "SigFlags": true}

// segment is the changes between two revisions: the objects the later one
// (re)defines, classified.
type segment struct {
	level      int
	violations []Violation
	ok         bool
}

// analyse classifies the changes between the revision that ends at `from`
// and the one that ends at `to` (both byte offsets into doc).
func analyse(doc []byte, from, to int64) segment {
	if from >= to {
		return segment{ok: true}
	}
	oldR, err := pdf.NewReader(bytes.NewReader(doc[:from]), from)
	if err != nil {
		return segment{}
	}
	newR, err := pdf.NewReader(bytes.NewReader(doc[:to]), to)
	if err != nil {
		return segment{}
	}
	ids := changedIDs(newR, doc[from:to])
	c := newContext(oldR, newR)
	seg := segment{ok: true}
	for _, id := range ids {
		lvl, v := c.classify(id)
		if lvl > seg.level {
			seg.level = lvl
		}
		if v != nil && lvl > levelForm {
			seg.violations = append(seg.violations, *v)
		}
	}
	return seg
}

// changedIDs are the objects the appended bytes define: plain objects, and
// the members of any object stream among them.
func changedIDs(newR *pdf.Reader, appended []byte) []int {
	seen := map[int]bool{}
	for _, m := range objRe.FindAllSubmatch(appended, -1) {
		id, err := strconv.Atoi(string(m[1]))
		if err != nil || id <= 0 {
			continue
		}
		seen[id] = true
	}
	for id := range seen {
		v, err := newR.GetObject(uint32(id))
		if err != nil || v.Kind() != pdf.Stream {
			continue
		}
		switch v.Key("Type").Name() {
		case "XRef":
			delete(seen, id)
		case "ObjStm":
			delete(seen, id)
			for _, member := range objStmMembers(v) {
				seen[member] = true
			}
		}
	}
	out := make([]int, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// objStmMembers reads the object numbers an object stream holds.
func objStmMembers(v pdf.Value) []int {
	n := int(v.Key("N").Int64())
	first := int(v.Key("First").Int64())
	r := v.Reader()
	if r == nil || n <= 0 || first <= 0 {
		return nil
	}
	defer r.Close()
	head := make([]byte, first)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil
	}
	fields := bytes.Fields(head)
	var out []int
	for i := 0; i+1 < len(fields) && len(out) < n; i += 2 {
		if id, err := strconv.Atoi(string(fields[i])); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// context is what classifying needs to know about the new revision.
type context struct {
	oldR, newR *pdf.Reader
	root       int
	form       int
	info       int
	metadata   int
	pages      map[int]int // object → 1-based page number
	// parts are the objects a page draws THROUGH — its content streams,
	// its resources and the fonts and XObjects in them — each with the
	// (first) page that uses it, as the SIGNED revision had them.
	parts     map[int]int
	fields    map[int]string
	oldFields map[int]string
	oldPages  map[int]int
}

func newContext(oldR, newR *pdf.Reader) *context {
	c := &context{oldR: oldR, newR: newR, pages: map[int]int{}, fields: map[int]string{}, oldFields: map[int]string{}, oldPages: map[int]int{}, parts: map[int]int{}}
	root := newR.Trailer().Key("Root")
	c.root = int(root.GetPtr().GetID())
	if f := root.Key("AcroForm"); isOwn(f, c.root) {
		c.form = int(f.GetPtr().GetID())
	}
	if i := newR.Trailer().Key("Info"); !i.IsNull() {
		c.info = int(i.GetPtr().GetID())
	}
	if m := root.Key("Metadata"); !m.IsNull() {
		c.metadata = int(m.GetPtr().GetID())
	}
	for i := 1; i <= newR.NumPage(); i++ {
		c.pages[int(newR.Page(i).V.GetPtr().GetID())] = i
	}
	for i := 1; i <= oldR.NumPage(); i++ {
		p := oldR.Page(i).V
		c.oldPages[int(p.GetPtr().GetID())] = i
		c.pageParts(p, i)
	}
	walkFields(root.Key("AcroForm").Key("Fields"), c.fields, "", 0)
	walkFields(oldR.Trailer().Key("Root").Key("AcroForm").Key("Fields"), c.oldFields, "", 0)
	return c
}

// pageParts records what page n draws through.
//
// ⚠⚠ Measured in a browser, 2026-09-22: a later update that rewrote page
// 1's content STREAM — the page dictionary untouched, which is how a PDF
// tool usually changes what a page shows — was reported as "object 5 was
// changed". It is "page 1 draws something different", and that is what a
// reader has to be told. One level of resources is followed (the fonts and
// XObjects a page names): deep enough for every tool that redraws a page,
// shallow enough that a malformed file cannot walk us in circles.
func (c *context) pageParts(page pdf.Value, n int) {
	add := func(v pdf.Value) {
		if id := int(v.GetPtr().GetID()); id != 0 && id != int(page.GetPtr().GetID()) {
			if _, seen := c.parts[id]; !seen {
				c.parts[id] = n
			}
		}
	}
	contents := page.Key("Contents")
	add(contents)
	if contents.Kind() == pdf.Array {
		for i := 0; i < contents.Len(); i++ {
			add(contents.Index(i))
		}
	}
	res := page.Key("Resources")
	add(res)
	for _, kind := range []string{"Font", "XObject", "ExtGState", "ColorSpace", "Pattern", "Shading"} {
		dict := res.Key(kind)
		add(dict)
		for _, k := range dict.Keys() {
			add(dict.Key(k))
		}
	}
}

func walkFields(node pdf.Value, out map[int]string, prefix string, depth int) {
	if depth > 16 || node.Kind() != pdf.Array {
		return
	}
	for i := 0; i < node.Len(); i++ {
		f := node.Index(i)
		name := f.Key("T").Text()
		if prefix != "" {
			name = prefix + "." + name
		}
		out[int(f.GetPtr().GetID())] = name
		walkFields(f.Key("Kids"), out, name, depth+1)
	}
}

// isOwn reports whether v is an object of its own (reached through a
// reference) rather than a value inside its owner.
func isOwn(v pdf.Value, owner int) bool {
	id := int(v.GetPtr().GetID())
	return id != 0 && id != owner
}

// classify says how far one (re)defined object goes.
func (c *context) classify(id int) (int, *Violation) {
	newV, err := c.newR.GetObject(uint32(id))
	if err != nil {
		return levelNone, nil
	}
	oldV, err := c.oldR.GetObject(uint32(id))
	if err != nil || oldV.Kind() == pdf.Null {
		// New: harmless on its own. What makes it matter — a page listing
		// a new annotation, a field pointing at a new appearance — is the
		// change to an EXISTING object, classified there.
		return levelNone, nil
	}
	switch {
	case id == c.info, id == c.metadata:
		return levelNone, nil
	case id == c.root:
		return c.catalogue(oldV, newV)
	case id == c.form:
		return c.formDict(oldV, newV, id)
	}
	if n, ok := c.pages[id]; ok {
		return c.page(oldV, newV, id, n)
	}
	if name, ok := c.fields[id]; ok {
		changed := diffKeys(oldV, newV, id)
		for _, k := range changed {
			if !valueUpdateKeys[k] {
				return levelOther, &Violation{Kind: "field", Field: name, Object: id}
			}
		}
		if len(changed) == 0 {
			return levelNone, nil
		}
		return levelForm, nil
	}
	if same(oldV, newV, id, id) {
		return levelNone, nil
	}
	if n, ok := c.parts[id]; ok {
		return levelOther, &Violation{Kind: "page_content", Page: n, Object: id}
	}
	return levelOther, &Violation{Kind: "object", Object: id}
}

func (c *context) catalogue(oldV, newV pdf.Value) (int, *Violation) {
	level := levelNone
	for _, k := range diffKeys(oldV, newV, c.root) {
		if !catalogueExempt[k] {
			return levelOther, &Violation{Kind: "catalog", Object: c.root}
		}
		if k == "AcroForm" && !isOwn(newV.Key(k), c.root) {
			lvl, v := c.formDict(oldV.Key(k), newV.Key(k), c.root)
			if lvl > level {
				level = lvl
			}
			if v != nil {
				return lvl, v
			}
		}
	}
	return level, nil
}

// formDict: only the listed keys change, and the field list only grows by
// new SIGNATURE fields.
func (c *context) formDict(oldV, newV pdf.Value, owner int) (int, *Violation) {
	changed := diffKeys(oldV, newV, owner)
	if len(changed) == 0 {
		return levelNone, nil
	}
	for _, k := range changed {
		if !formExempt[k] {
			return levelOther, &Violation{Kind: "form", Object: owner}
		}
	}
	oldList := refIDs(oldV.Key("Fields"))
	for _, id := range oldList {
		if !contains(refIDs(newV.Key("Fields")), id) {
			return levelOther, &Violation{Kind: "form", Object: owner}
		}
	}
	for _, id := range refIDs(newV.Key("Fields")) {
		if contains(oldList, id) {
			continue
		}
		f, err := c.newR.GetObject(uint32(id))
		if err != nil || f.Key("FT").Name() != "Sig" {
			return levelOther, &Violation{Kind: "form", Object: owner}
		}
	}
	return levelForm, nil
}

// page: the one change a page may take is new signature widgets in its
// /Annots; a changed /Contents is a redrawn page.
func (c *context) page(oldV, newV pdf.Value, id, n int) (int, *Violation) {
	changed := diffKeys(oldV, newV, id)
	if len(changed) == 0 {
		return levelNone, nil
	}
	for _, k := range changed {
		switch k {
		case "Annots":
		case "Contents", "Resources":
			return levelOther, &Violation{Kind: "page_content", Page: n, Object: id}
		default:
			return levelOther, &Violation{Kind: "page", Page: n, Object: id}
		}
	}
	oldA := refIDs(oldV.Key("Annots"))
	newA := refIDs(newV.Key("Annots"))
	for _, a := range oldA {
		if !contains(newA, a) {
			return levelOther, &Violation{Kind: "annotation", Page: n, Object: id}
		}
	}
	for _, a := range newA {
		if contains(oldA, a) {
			continue
		}
		w, err := c.newR.GetObject(uint32(a))
		if err != nil || !(w.Key("FT").Name() == "Sig" || w.Key("Parent").Key("FT").Name() == "Sig") {
			return levelOther, &Violation{Kind: "annotation", Page: n, Object: id}
		}
	}
	return levelForm, nil
}

// diffKeys are the keys whose values differ between two versions of one
// dictionary (or a stream's dictionary and data, reported as "Contents").
func diffKeys(a, b pdf.Value, owner int) []string {
	if a.Kind() != b.Kind() {
		return []string{"*"}
	}
	if a.Kind() == pdf.Stream {
		if !sameStream(a, b) {
			return []string{"Contents"}
		}
		return nil
	}
	if a.Kind() != pdf.Dict {
		if !same(a, b, owner, owner) {
			return []string{"*"}
		}
		return nil
	}
	keys := map[string]bool{}
	for _, k := range a.Keys() {
		keys[k] = true
	}
	for _, k := range b.Keys() {
		keys[k] = true
	}
	var out []string
	for k := range keys {
		if !same(a.Key(k), b.Key(k), owner, owner) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// same compares two values without following references: an object of its
// own is the same when it is the same object number; a direct value is
// compared through.
func same(a, b pdf.Value, ownerA, ownerB int) bool {
	if isOwn(a, ownerA) || isOwn(b, ownerB) {
		return isOwn(a, ownerA) && isOwn(b, ownerB) && a.GetPtr().GetID() == b.GetPtr().GetID()
	}
	if numeric(a) && numeric(b) {
		// 306 and 306.000 are the same number: a rewritten object may spell
		// a number differently without changing it.
		d := a.Float64() - b.Float64()
		return d < 1e-6 && d > -1e-6
	}
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case pdf.Dict:
		if len(a.Keys()) != len(b.Keys()) {
			return false
		}
		for _, k := range a.Keys() {
			if !same(a.Key(k), b.Key(k), ownerA, ownerB) {
				return false
			}
		}
		return true
	case pdf.Array:
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !same(a.Index(i), b.Index(i), ownerA, ownerB) {
				return false
			}
		}
		return true
	case pdf.Stream:
		return sameStream(a, b)
	case pdf.String:
		return a.RawString() == b.RawString()
	}
	return a.String() == b.String()
}

func numeric(v pdf.Value) bool { return v.Kind() == pdf.Integer || v.Kind() == pdf.Real }

func sameStream(a, b pdf.Value) bool {
	ra, rb := a.Reader(), b.Reader()
	if ra == nil || rb == nil {
		return ra == nil && rb == nil
	}
	defer ra.Close()
	defer rb.Close()
	da, err1 := io.ReadAll(io.LimitReader(ra, 64<<20))
	db, err2 := io.ReadAll(io.LimitReader(rb, 64<<20))
	return err1 == nil && err2 == nil && bytes.Equal(da, db)
}

func refIDs(v pdf.Value) []int {
	if v.Kind() != pdf.Array {
		return nil
	}
	out := make([]int, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		out = append(out, int(v.Index(i).GetPtr().GetID()))
	}
	return out
}

func contains(list []int, id int) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}
