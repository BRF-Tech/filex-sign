package envelope

import "testing"

// Unique in the document, stable in order, clear of the document's own
// form fields, and never empty — the four promises AssignKeys makes.
func TestAssignKeys(t *testing.T) {
	fs := []Field{
		{ID: "sig-1", Type: "signature", Label: "İmza"},
		{ID: "sig-2", Type: "signature", Label: "imza"}, // folds to the same identity
		{ID: "text-1", Type: "text", Label: "顧客名"},      // nothing ASCII: the type stands in
		{ID: "text-2", Type: "text"},                    // unnamed: the type too
		{ID: "date-1", Type: "date", Label: "Tarih"},    // the PDF already has a "tarih"
		{ID: "chk-1", Type: "checkbox", Label: "Onay kutusu"},
	}
	AssignKeys(fs, map[string]bool{"tarih": true})
	want := []string{"imza", "imza-2", "text", "text-2", "tarih-2", "onay-kutusu"}
	seen := map[string]bool{}
	for i, f := range fs {
		if f.Key != want[i] {
			t.Errorf("box %s: identity %q, want %q", f.ID, f.Key, want[i])
		}
		if seen[f.Key] {
			t.Errorf("identity %q given twice", f.Key)
		}
		seen[f.Key] = true
		if f.FormName() != f.Key {
			t.Errorf("the PDF must name box %s by its identity", f.ID)
		}
	}
	// A record written before identities keeps naming its fields by id, so
	// a request already in flight fills the fields it created.
	if (Field{ID: "sig-9"}).FormName() != "sig-9" {
		t.Error("an old record's field must keep its id as its PDF name")
	}
}
