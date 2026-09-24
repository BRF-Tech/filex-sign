package envelope

import (
	"strconv"
	"strings"

	"github.com/brf-tech/filex-sign/internal/fields"
)

// AssignKeys gives every box an identity (Field.Key — the PDF form
// field's name) derived from its name, unique within the document.
//
// ⚠⚠ WHEN it runs is the stability rule, and it is deliberate: the
// identity FOLLOWS THE NAME while the request is being put together (the
// wizard never stores one — the boxes are renamed freely), and is FIXED
// the moment the request is sent (the request job calls this once, before
// the record is written, and nothing ever calls it on a sent record
// again). A field that already exists in a document therefore never
// changes its name under the signers, and an exported form keeps the name
// it was given. Signing your own document derives it at signing, once.
//
// `taken` holds names the document already uses for its OWN form fields
// (a PDF that arrived as a form). ⚠ They must be avoided, not merely
// counted: pdfdoc.PrepareForm leaves an existing field alone, so a box
// whose identity collided with one would silently fill somebody else's
// field.
//
// A name with nothing ASCII in it (Japanese, emoji) falls back to the
// box's type — `signature`, `date` — and a second box wanting the same
// identity gets `-2`, `-3` …: `imza`, `imza-2`.
func AssignKeys(fs []Field, taken map[string]bool) {
	used := map[string]bool{}
	for k := range taken {
		used[k] = true
	}
	for i := range fs {
		base := fields.Slug(fs[i].Label)
		if base == "" {
			base = fields.NormalizeType(fs[i].Type)
		}
		key := base
		for n := 2; used[key]; n++ {
			suffix := "-" + strconv.Itoa(n)
			trimmed := base
			if len(trimmed)+len(suffix) > fields.MaxSlug {
				trimmed = strings.TrimRight(trimmed[:fields.MaxSlug-len(suffix)], "-")
			}
			key = trimmed + suffix
		}
		used[key] = true
		fs[i].Key = key
	}
}
