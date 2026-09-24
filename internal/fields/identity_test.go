package fields

import "testing"

// A box's identity is its name folded to ASCII — never shown, only the
// PDF's own name for the field (the owner, 2026-09-21: "isim ayrı, kimlik
// ayrı").
func TestSlug(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Müşteri adı", "musteri-adi"},
		{"İmza", "imza"},
		{"ŞİRKET ÜNVANI", "sirket-unvani"},
		{"Kayıt  tarihi", "kayit-tarihi"}, // ı is its own letter, not i with a mark
		{"Çağrı / Göğüs", "cagri-gogus"},
		{"  Café crème — prix  ", "cafe-creme-prix"},
		{"Straße", "strasse"},
		{"Øresund Łódź", "oresund-lodz"},
		{"a..b", "a-b"},
		{"---", ""},
		{"顧客名", ""},        // nothing folds: the caller falls back to the type
		{"👍 onay", "onay"}, // the emoji goes, the word stays
		{"x2 Box 3", "x2-box-3"},
	} {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := Slug("çok uzun bir kutu adı ki kırk karakteri rahatça aşıyor ve kesilmesi gerek")
	if len(long) > MaxSlug || long[len(long)-1] == '-' {
		t.Errorf("a long name must be cut to %d without a trailing dash: %q", MaxSlug, long)
	}
}
