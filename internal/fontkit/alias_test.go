package fontkit

import "testing"

// filex's renderer calls the serif face `source-serif`; this package calls
// it `source-serif-4`. A box set in the serif face used to arrive as an id
// this build did not know and be stamped in Inter (2026-09-21).
func TestFontAlias(t *testing.T) {
	if !Valid("source-serif") || Canonical("source-serif") != SourceSerif {
		t.Fatalf("the renderer's spelling must name the serif face: valid=%v canonical=%q", Valid("source-serif"), Canonical("source-serif"))
	}
	if Get("source-serif").ID != SourceSerif {
		t.Errorf("Get(source-serif) = %s", Get("source-serif").ID)
	}
	if WireID(SourceSerif) != "source-serif" || WireID(Caveat) != Caveat {
		t.Errorf("the renderer is answered in its own spelling: %q %q", WireID(SourceSerif), WireID(Caveat))
	}
}
