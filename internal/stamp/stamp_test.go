package stamp

import (
	"strings"
	"testing"
	"time"
)

var when = time.Date(2026, 9, 21, 14, 5, 0, 0, time.UTC)

// Nil is the defaults (what every signature carried before the choice),
// an empty choice is nothing, and a choice comes back in printing order
// with anything unknown dropped.
func TestChosen(t *testing.T) {
	if got := Chosen(nil); strings.Join(got, ",") != "name,date" {
		t.Errorf("defaults = %v", got)
	}
	empty := []string{}
	if got := Chosen(&empty); len(got) != 0 {
		t.Errorf("an empty choice must stay empty, got %v", got)
	}
	mixed := []string{Cert, "shoe-size", Name, IP}
	if got := Chosen(&mixed); strings.Join(got, ",") != "name,ip,cert" {
		t.Errorf("chosen = %v", got)
	}
}

// Every line is worded here and only here, from the record's facts; a
// fact the record does not hold is left out rather than printed as a dash.
func TestLines(t *testing.T) {
	f := Facts{Name: "Ayşe Yılmaz", Email: "ayse@example.com", When: when, IP: "203.0.113.7",
		CertFP: "3F2A 9C1B 7D4E 0A11 BEEF CAFE", Serial: "1A2B", Authority: "filex CA"}
	all := IDs()
	got := Lines("tr", all, f)
	want := []string{"Ayşe Yılmaz", "ayse@example.com", "İmza tarihi: 21.09.2026 14:05 UTC",
		"IP adresi: 203.0.113.7", "Sertifika SHA-256: 3F2A 9C1B 7D4E 0A11…", "Sertifika seri no: 1A2B",
		"İmza makamı: filex CA"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("tr lines:\n got %q\nwant %q", got, want)
	}
	if en := Lines("en", []string{Date, IP}, f); en[0] != "Signed 2026-09-21 14:05 UTC" || en[1] != "IP address: 203.0.113.7" {
		t.Errorf("en lines: %q", en)
	}
	// Somebody with no address and no IP on record: those lines vanish.
	bare := Lines("en", all, Facts{Name: "Elden Veren", When: when})
	if strings.Join(bare, "|") != "Elden Veren|Signed 2026-09-21 14:05 UTC" {
		t.Errorf("missing facts must be left out: %q", bare)
	}
}

// A preview says when a fact will be known — and only for the facts that
// really do not exist yet.
func TestLinesPending(t *testing.T) {
	signer := Lines("en", []string{IP, Cert, Serial}, Facts{IP: "203.0.113.7", PendingCert: true, PendingWho: You})
	if signer[0] != "IP address: 203.0.113.7" || !strings.Contains(signer[1], "when you sign") || !strings.Contains(signer[2], "when you sign") {
		t.Errorf("the signer's preview: %q", signer)
	}
	requester := Lines("tr", []string{IP}, Facts{PendingIP: true, PendingWho: They})
	if len(requester) != 1 || !strings.Contains(requester[0], "imzaladıklarında") {
		t.Errorf("the requester's preview: %q", requester)
	}
	// ⚠ The signer's own preview never promises an address it does not
	// have: the line is left out, as it will be on the paper.
	if got := Lines("en", []string{IP}, Facts{PendingCert: true}); len(got) != 0 {
		t.Errorf("an unknown IP on the signer's preview must be left out: %q", got)
	}
}
