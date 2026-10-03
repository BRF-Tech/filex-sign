package views

import (
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/verify"
)

// VerifyInput is everything the report screen draws.
type VerifyInput struct {
	Doc    Doc
	Report *verify.Report
	// Err is a document that could not be read at all.
	Err string
	// Timestamps says whether this instance stamps its signatures with a
	// time-stamping authority, which decides how the "when" line reads.
	Timestamps bool
	// Sent is the SHA-256 this installation sent to every party when a
	// request completed with THIS file — nil when no record names it.
	Sent *SentHash
	// CanRequest: the reader holds the app's `request` permission, so an
	// unsigned document may point them at "Request signatures…".
	CanRequest bool
}

// SentHash is the completion record a file's hash was compared with.
type SentHash struct {
	SHA256   string
	Document string // the request's document
	Output   string // the signed file's name
	// Matches: this file's own hash is the one that was sent.
	Matches bool
}

// Verify draws the verification report: one card per signature, and
// above them the plain sentence about what a signature from this
// instance is worth. Nothing is invented — a field the certificate does
// not carry is reported as unstated, never guessed.
func Verify(l Lang, in VerifyInput) *wire.Surface {
	s := &wire.Surface{Title: Tf("Signatures in %s", "%s içindeki imzalar", in.Doc.Name),
		Size: "lg", State: map[string]any{"view": "verify"}}
	if in.Err != "" {
		s.Nodes = []wire.Node{danger(Tf("This document could not be read: %s", "Bu belge okunamadı: %s", in.Err))}
		return s
	}
	rep := in.Report
	if rep == nil || !rep.Signed() {
		hint := T("Use “Sign…” to sign it yourself.", "Kendiniz imzalamak için “İmzala…” kullanın.")
		if in.CanRequest {
			hint = T("Use “Sign…” to sign it yourself, or “Request signatures…” to ask others.",
				"Kendiniz imzalamak için “İmzala…”, başkalarından istemek için “İmza iste…” kullanın.")
		}
		s.Nodes = []wire.Node{
			info(T("This document carries no electronic signature.", "Bu belgede elektronik imza yok.")),
			muted(hint),
		}
		if rep != nil {
			s.Nodes = append(s.Nodes, authorityNodes(l, rep)...)
		}
		return s
	}

	s.Nodes = []wire.Node{
		heading(Tf("%d signature(s)", "%d imza", len(rep.Signatures))),
	}
	s.Nodes = append(s.Nodes, summaryNodes(rep, in.Sent)...)
	s.Nodes = append(s.Nodes, text(Expectation(l)))
	if rep.Err != "" {
		s.Nodes = append(s.Nodes, muted(Tf("The verifier also said: %s", "Doğrulayıcı ayrıca şunu söyledi: %s", rep.Err)))
	}
	s.Nodes = append(s.Nodes, authorityNodes(l, rep)...)
	for _, sig := range rep.Signatures {
		s.Nodes = append(s.Nodes, divider())
		s.Nodes = append(s.Nodes, signatureCard(l, sig, in.Timestamps)...)
	}
	return s
}

// summaryNodes is the answer before the details — the owner's four
// questions, 2026-09-22: is every signature valid, is the document
// certified, did filex seal it, and is this the file whose hash every
// party was sent? And, above all of them, a change nobody was allowed to
// make.
func summaryNodes(rep *verify.Report, sent *SentHash) []wire.Node {
	var out []wire.Node
	if rep.NotPermitted {
		out = append(out, danger(T("This document was changed after it was signed in a way its signatures do NOT permit. A PDF reader reports the same: changes not permitted.",
			"Bu belge, imzalandıktan sonra imzalarının İZİN VERMEDİĞİ bir biçimde değiştirildi. Bir PDF okuyucu da aynısını söyler: izin verilmeyen değişiklik.")))
		for _, s := range rep.Signatures {
			if !s.After.Permitted && (s.Certification > 0 || s.Lock > 0) {
				for _, v := range s.After.Violations {
					out = append(out, danger(violationWords(s, v)))
				}
			}
		}
	}
	valid := true
	for _, s := range rep.Signatures {
		valid = valid && s.Valid && s.Trusted
	}
	switch {
	case valid && rep.NotPermitted:
		// ⚠ Not "every signature is valid": a PDF reader does not accept a
		// certified document changed in a way it forbids, and neither may
		// this line. The signatures themselves are intact — say that, and
		// that what is wrong is the change, named above.
		out = append(out, text(T("Every signature itself is intact and comes from an authority this filex trusts - what is wrong is the change made after them, above.",
			"Her imzanın kendisi bozulmamış ve bu filex'in güvendiği bir makamdan geliyor - yanlış olan, yukarıda belirtilen, onlardan sonra yapılan değişiklik.")))
	case valid:
		out = append(out, text(T("Every signature is valid and comes from an authority this filex trusts.",
			"Her imza geçerli ve bu filex'in güvendiği bir makamdan geliyor.")))
	default:
		out = append(out, danger(T("Not every signature checks out - see the signatures below.",
			"Her imza doğrulanmıyor - aşağıdaki imzalara bakın.")))
	}
	for _, s := range rep.Signatures {
		if s.Certification > 0 {
			out = append(out, text(Each(func(l Lang) string {
				return l.Sf("Certified by %s: after the first signature, %s.", "%s tarafından onaylandı: ilk imzadan sonra %s.",
					s.Name, permissionWords(l, s.Certification))
			})))
			break
		}
	}
	if rep.Certified == 0 {
		out = append(out, muted(T("Not certified: the first signature does not say what may still change.",
			"Onaylanmamış: ilk imza neyin değişebileceğini söylemiyor.")))
	}
	if rep.Sealed {
		seal := rep.Signatures[len(rep.Signatures)-1]
		out = append(out, text(Tf("Sealed by filex (seal %s): the document is closed - any change after the seal is not permitted.",
			"filex tarafından mühürlendi (mühür %s): belge kapatıldı - mühürden sonraki her değişiklik izin verilmeyen değişikliktir.", seal.Cert.FP)))
	}
	out = append(out, text(Tf("SHA-256 of this file: %s", "Bu dosyanın SHA-256 özeti: %s", rep.SHA256)))
	switch {
	case sent != nil && sent.Matches:
		out = append(out, text(Tf("This is exactly the file whose SHA-256 every party was sent when “%s” was completed.",
			"Bu, “%s” tamamlandığında SHA-256 özeti her tarafa gönderilen dosyanın ta kendisi.", sent.Document)))
	case sent != nil:
		out = append(out, danger(Tf("This file's SHA-256 is NOT the one every party was sent when “%s” was completed: %s. This is not that file.",
			"Bu dosyanın SHA-256 özeti, “%s” tamamlandığında her tarafa gönderilen özet DEĞİL: %s. Bu, o dosya değil.", sent.Document, sent.SHA256)))
	case rep.Sealed:
		out = append(out, muted(T("Compare it with the SHA-256 in the completion notice you were sent: if they are the same, this is the file everybody signed.",
			"Size gönderilen tamamlanma bildirimindeki SHA-256 özetiyle karşılaştırın: aynıysa, bu herkesin imzaladığı dosyadır.")))
	}
	return out
}

// permissionWords is a DocMDP permission in plain words.
func permissionWords(l Lang, p int) string {
	switch p {
	case 1:
		return l.S("no change at all is permitted", "hiçbir değişikliğe izin verilmiyor")
	case 2:
		return l.S("filling in the form and signing are permitted", "formu doldurmaya ve imzalamaya izin veriliyor")
	case 3:
		return l.S("filling in the form, signing and annotating are permitted", "formu doldurmaya, imzalamaya ve not eklemeye izin veriliyor")
	}
	return l.S("anything is permitted", "her şeye izin veriliyor")
}

// violationWords says one forbidden change, and whose promise it broke.
func violationWords(s verify.Signature, v verify.Violation) wire.Text {
	var what wire.Text
	switch v.Kind {
	case "page_content":
		what = Tf("page %d draws something different", "%d. sayfa başka bir şey çiziyor", v.Page)
	case "page":
		what = Tf("page %d was changed", "%d. sayfa değiştirildi", v.Page)
	case "annotation":
		what = Tf("an annotation was added to or removed from page %d", "%d. sayfaya not eklendi ya da sayfadan not kaldırıldı", v.Page)
	case "field":
		what = Tf("the form field “%s” was changed", "“%s” form alanı değiştirildi", v.Field)
	case "catalog":
		what = T("the document's catalogue was changed", "belgenin kataloğu değiştirildi")
	case "form":
		what = T("the form was changed", "form değiştirildi")
	default:
		what = Tf("object %d was changed", "%d numaralı nesne değiştirildi", v.Object)
	}
	// ⚠ Whose promise was broken is in the sentence, not a word slotted
	// into it: "by the certification" is "laut der Zertifizierung", "by
	// filex's seal" "laut dem Siegel von filex" — one sentence per case
	// reads right in every language.
	return Each(func(l Lang) string {
		w := In(what, l)
		switch {
		case s.Seal:
			return l.Sf("Not permitted by filex's seal: %s.", "filex'in mührü buna izin vermiyor: %s.", w)
		case s.Certification > 0:
			return l.Sf("Not permitted by the certification: %s.", "Onay imzası buna izin vermiyor: %s.", w)
		}
		return l.Sf("Not permitted by signature %d: %s.", "%d. imza buna izin vermiyor: %s.", s.Index, w)
	})
}

// Expectation is the one paragraph every surface that talks about trust
// repeats, word for word, because a wrong expectation is worse than no
// signature at all.
func Expectation(l Lang) wire.Text {
	return T("A signature made here comes from this installation's own signing authority - the one filex generated, or the organisation's own certificate authority if the administrator imported one. A reader who imports that authority's certificate once sees these signatures as valid; a reader who has not says the validity is unknown, which is not the same as invalid. Where the law asks for a qualified electronic signature, use e-imza or m-imza instead.",
		"Buradaki imza, bu kurulumun kendi imza makamından gelir - filex'in ürettiği makam ya da yönetici içe aktardıysa kurumun kendi sertifika makamı. O makamın sertifikasını bir kez içe aktaran okuyucu bu imzaları geçerli görür; aktarmayan “geçerlilik bilinmiyor” der, ki bu “geçersiz” demek değildir. Kanunun nitelikli elektronik imza istediği işlerde e-imza ya da m-imza kullanın.")
}

// authorityNodes names the signing authorities this instance trusts,
// with the fingerprint a person can compare against a receipt.
func authorityNodes(l Lang, rep *verify.Report) []wire.Node {
	if len(rep.Authorities) == 0 {
		return []wire.Node{muted(T("This filex has no signing authority configured, so nothing can be checked against it.",
			"Bu filex kurulumunda imza makamı tanımlı değil; hiçbir şey ona karşı denetlenemiyor."))}
	}
	var rows []row
	for i, ca := range rep.Authorities {
		state := T("retired - signatures it made stay checkable", "emekli - verdiği imzalar denetlenebilir kalır")
		if i == 0 {
			state = T("in use", "kullanımda")
		}
		rows = append(rows, row{ID: fmt.Sprintf("ca%d", i), Cells: map[string]wire.Text{
			"who":   Plain(ca.Subject),
			"fp":    Plain(ca.FP),
			"valid": SpanText(ca.NotBefore, ca.NotAfter),
			"state": state,
		}})
	}
	return []wire.Node{list([]column{
		{Key: "who", Label: T("Signing authority", "İmza makamı")},
		{Key: "fp", Label: T("SHA-256 fingerprint", "SHA-256 parmak izi")},
		{Key: "valid", Label: T("Valid", "Geçerlilik")},
		{Key: "state", Label: T("State", "Durum")},
	}, rows, T("None", "Yok"))}
}

func signatureCard(l Lang, sig verify.Signature, stamps bool) []wire.Node {
	// ⚠ The verdict is filled in PER LANGUAGE. `Tf` builds both variants and
	// would otherwise drop the same, already-resolved string into both — so
	// the heading came out as "1. imza — valid, from an authority this filex
	// trusts": Turkish sentence, English verdict, in the one place a reader
	// is looking for a straight answer.
	head := Each(func(l Lang) string {
		return l.Sf("Signature %d - %s", "%d. imza - %s", sig.Index, verdict(l, sig))
	})
	rows := []row{
		{ID: "who", Cells: map[string]wire.Text{"k": T("Identity", "Kimlik"), "v": Plain(identityOf(l, sig))}},
	}
	add := func(id string, k wire.Text, v wire.Text) {
		rows = append(rows, row{ID: id, Cells: map[string]wire.Text{"k": k, "v": v}})
	}
	if sig.NameConflict {
		add("claim", T("Typed into the signature", "İmzaya yazılan ad"),
			Tf("%s - the certificate says otherwise, and the certificate is what an authority put its name behind.",
				"%s - sertifika başka söylüyor; makamın adını arkasına koyduğu şey sertifikadır.", sig.DeclaredName))
	}
	if sig.Reason != "" {
		add("reason", T("Reason", "Gerekçe"), Plain(sig.Reason))
	}
	if sig.Location != "" {
		add("where", T("Place", "Yer"), Plain(sig.Location))
	}
	add("when", Tc("time", "Signed", "İmza zamanı"), whenWords(l, sig, stamps))
	add("kind", T("Kind", "Tür"), signatureKindWords(sig))
	add("covers", T("Covers", "Kapsam"), coverWords(l, sig))
	add("after", T("Changes after it", "Sonrasındaki değişiklikler"), changeWords(l, sig))
	if sig.After.Policy > 0 {
		add("permitted", T("Permitted since", "O zamandan beri izin verilen"), permittedWords(sig))
	}
	add("crypto", T("Cryptographically", "Kriptografik olarak"), cryptoWords(l, sig))
	if sig.Algorithm != "" {
		add("alg", T("Algorithm", "Algoritma"), Plain(sig.Algorithm))
	}

	// What the certificate itself attests.
	add("cert-serial", T("Certificate serial", "Sertifika seri numarası"), orUnstated(l, sig.Cert.Serial))
	add("cert-fp", T("Certificate fingerprint (SHA-256)", "Sertifika parmak izi (SHA-256)"), orUnstated(l, sig.Cert.FP))
	add("cert-valid", T("Certificate valid", "Sertifika geçerliliği"), certValidWords(l, sig))
	add("cert-usage", T("The certificate is for", "Sertifikanın kullanım amacı"), usageWords(l, sig))

	// Who stands behind it.
	if sig.HasIssuer {
		add("ca", T("Signing authority", "İmza makamı"), Plain(sig.Issuer.Subject))
		add("ca-fp", T("Authority fingerprint (SHA-256)", "Makam parmak izi (SHA-256)"), orUnstated(l, sig.Issuer.FP))
		add("ca-valid", T("Authority valid", "Makam geçerliliği"),
			SpanText(sig.Issuer.NotBefore, sig.Issuer.NotAfter))
	} else {
		add("ca", T("Signing authority", "İmza makamı"),
			T("not stated - the document carries no issuer certificate", "belirtilmemiş - belgede makamın sertifikası yok"))
	}
	add("root", T("Chain ends at", "Zincirin ucu"), rootWords(l, sig))

	nodes := []wire.Node{heading(head)}
	for _, w := range sig.Warnings {
		t, show := warningWords(w)
		if !show {
			continue
		}
		nodes = append(nodes, muted(Each(func(l Lang) string {
			return l.Sf("Warning: %s", "Uyarı: %s", In(t, l))
		})))
	}
	for _, e := range sig.Errors {
		nodes = append(nodes, danger(Tf("Problem: %s", "Sorun: %s", e)))
	}
	nodes = append(nodes, list([]column{
		{Key: "k", Label: T("What", "Ne")},
		{Key: "v", Label: T("It says", "Ne diyor")},
	}, rows, T("Nothing", "Yok")))
	return nodes
}

func identityOf(l Lang, sig verify.Signature) string {
	name, mail := strings.TrimSpace(sig.Name), strings.TrimSpace(sig.Email)
	switch {
	case name != "" && mail != "":
		return name + " (" + mail + ")"
	case name != "":
		return name
	case mail != "":
		return mail
	}
	return l.S("not stated", "belirtilmemiş")
}

// warningWords turns one warning from the verification library into
// something a reader can act on, in both languages, and drops the ones this
// card already says better.
//
// ⚠⚠ The library speaks English and only English. Passed through raw, its
// sentences were the only English on an otherwise Turkish screen — and the
// most common one ("using signature time as fallback") repeats, in worse
// words, the line the card already prints under "Signature time". A warning
// nobody can read is not a warning.
//
// An unknown warning is still shown, in English, under a translated prefix:
// hiding what we have no translation for would be worse than showing it.
func warningWords(w string) (wire.Text, bool) {
	switch key := strings.ToLower(strings.TrimSpace(w)); {
	case strings.Contains(key, "using signature time as fallback"):
		// The "Signature time" row says this, in the reader's language and
		// with what to do about it.
		return wire.Text{}, false
	case strings.Contains(key, "no timestamp to validate"),
		strings.Contains(key, "no timestamp signing certificate"):
		return T("This signature carries no time stamp, so the time on it is the signer's own claim.",
			"Bu imzada zaman damgası yok; üstündeki saat imzacının kendi beyanıdır."), true
	case strings.Contains(key, "not system trusted"):
		return T("The time stamp was checked against the certificates inside the document, not against an authority this server trusts.",
			"Zaman damgası, bu sunucunun güvendiği bir makama değil, belgenin içindeki sertifikalara karşı denetlendi."), true
	case strings.Contains(key, "timestamp hash does not match"):
		return T("The time stamp does not match the signature it is attached to.",
			"Zaman damgası, bağlı olduğu imzayla uyuşmuyor."), true
	case strings.Contains(key, "revocation") || strings.Contains(key, "revoked"):
		return T("The certificate was revoked, and without a trusted time stamp there is no telling whether that happened before or after the signing.",
			"Sertifika iptal edilmiş; güvenilir bir zaman damgası olmadan bunun imzadan önce mi sonra mı olduğu anlaşılamıyor."), true
	}
	return Plain(w), true
}

// signatureKindWords says what the signature is: the certification, filex's seal,
// or an approval signature.
func signatureKindWords(sig verify.Signature) wire.Text {
	switch {
	case sig.Seal:
		return T("filex's seal - it closed the document (locked: no change permitted after it)",
			"filex'in mührü - belgeyi kapattı (kilitli: sonrasında hiçbir değişikliğe izin yok)")
	case sig.Certification > 0:
		return Each(func(l Lang) string {
			return l.Sf("certification - after it, %s", "onay imzası - sonrasında %s", permissionWords(l, sig.Certification))
		})
	case sig.Lock > 0:
		return Each(func(l Lang) string {
			return l.Sf("signature that locks the document - after it, %s", "belgeyi kilitleyen imza - sonrasında %s", permissionWords(l, sig.Lock))
		})
	}
	return T("signature", "imza")
}

// permittedWords says whether what came after held to the permission.
func permittedWords(sig verify.Signature) wire.Text {
	switch {
	case !sig.After.Analysed:
		return T("could not be worked out", "belirlenemedi")
	case sig.After.Permitted:
		return T("everything that happened since was permitted", "o zamandan beri olan her şeye izin veriliyordu")
	}
	return T("NOT PERMITTED changes were made after it", "sonrasında İZİN VERİLMEYEN değişiklikler yapıldı")
}

func verdict(l Lang, sig verify.Signature) string {
	switch {
	case !sig.Valid:
		return l.S("does not check out", "doğrulanmıyor")
	case sig.Revoked:
		return l.S("the certificate was revoked", "sertifika iptal edilmiş")
	case sig.After.Policy > 0 && !sig.After.Permitted:
		return l.S("intact, but changes it does not permit were made afterwards", "bozulmamış, ama sonrasında izin vermediği değişiklikler yapıldı")
	case sig.LaterChanges == verify.ChangedContent:
		return l.S("intact, but a page changed afterwards", "bozulmamış, ama sonrasında bir sayfa değişti")
	case sig.Trusted:
		return l.S("valid, from an authority this filex trusts", "geçerli, bu filex'in güvendiği bir makamdan")
	}
	return l.S("intact - the authority behind it is not one this filex knows", "bozulmamış - arkasındaki makamı bu filex tanımıyor")
}

func whenWords(l Lang, sig verify.Signature, stamps bool) wire.Text {
	when := When(sig.When)
	if sig.When.IsZero() {
		return T("not stated", "belirtilmemiş")
	}
	if sig.TimeProven {
		return Tf("%s UTC - proven by a time-stamping authority", "%s UTC - bir zaman damgası makamınca kanıtlanmış", when)
	}
	if !stamps {
		return Tf("%s UTC - declared by the signer's own clock, not proven. This installation does not add time stamps; an administrator can switch them on.",
			"%s UTC - imzacının kendi saatinin beyanı, kanıtlanmış değil. Bu kurulum zaman damgası eklemiyor; yönetici açabilir.", when)
	}
	return Tf("%s UTC - declared by the signer's own clock, not proven",
		"%s UTC - imzacının kendi saatinin beyanı, kanıtlanmış değil", when)
}

func coverWords(l Lang, sig verify.Signature) wire.Text {
	if sig.CoversWholeFile {
		return T("the whole file", "dosyanın tamamı")
	}
	return Tf("the first %d of %d bytes - the rest was appended afterwards",
		"%[2]d baytın ilk %[1]d baytı - geri kalanı sonradan eklendi", sig.SignedBytes, sig.FileBytes)
}

func changeWords(l Lang, sig verify.Signature) wire.Text {
	switch sig.LaterChanges {
	case verify.ChangedNothing:
		return T("none - nothing was added after it", "yok - sonrasına hiçbir şey eklenmedi")
	case verify.ChangedFields:
		return T("form fields were filled in and signatures added; no page draws anything different",
			"form alanları dolduruldu ve imza eklendi; hiçbir sayfa başka bir şey çizmiyor")
	case verify.ChangedContent:
		return T("a page draws something different from what was signed", "bir sayfa imzalanandan başka bir şey çiziyor")
	}
	return T("could not be worked out", "belirlenemedi")
}

func cryptoWords(l Lang, sig verify.Signature) wire.Text {
	if !sig.Valid {
		return T("the signature does not match the bytes it covers", "imza, kapsadığı baytlarla eşleşmiyor")
	}
	if sig.Trusted {
		return T("valid, and the chain reaches an authority this filex trusts",
			"geçerli ve zincir bu filex'in güvendiği bir makama çıkıyor")
	}
	return T("valid - but the chain does not reach an authority this filex trusts",
		"geçerli - ama zincir bu filex'in güvendiği bir makama çıkmıyor")
}

func certValidWords(l Lang, sig verify.Signature) wire.Text {
	if sig.Cert.NotAfter.IsZero() {
		return T("not stated", "belirtilmemiş")
	}
	window := Span{From: sig.Cert.NotBefore, To: sig.Cert.NotAfter}
	if sig.When.IsZero() {
		return SpanText(window.From, window.To)
	}
	if sig.WithinValidity {
		return Tf("%s - the signing moment falls inside it", "%s - imza anı bu aralığın içinde", window)
	}
	return Tf("%s - the signing moment falls OUTSIDE it", "%s - imza anı bu aralığın DIŞINDA", window)
}

// usageWords names what the certificate says it may be used for. verify
// reports the uses as English identifiers; they become words here, in each
// language — "document signing, e-mail protection · digital signature,
// non-repudiation" was printed on every Turkish signature card (2026-09-26).
func usageWords(l Lang, sig verify.Signature) wire.Text {
	if len(sig.Cert.EKU) == 0 && len(sig.Cert.KeyUsage) == 0 {
		return T("not stated", "belirtilmemiş")
	}
	return Each(func(l Lang) string {
		var parts []string
		for _, group := range [][]string{sig.Cert.EKU, sig.Cert.KeyUsage} {
			if len(group) == 0 {
				continue
			}
			names := make([]string, 0, len(group))
			for _, id := range group {
				names = append(names, In(usageName(id), l))
			}
			parts = append(parts, strings.Join(names, ", "))
		}
		return strings.Join(parts, " · ")
	})
}

// usageName is one certificate use (verify's identifier) in words; an
// identifier verify did not name (an OID) stays as it is.
func usageName(id string) wire.Text {
	switch id {
	case "document signing":
		return T("document signing", "belge imzalama")
	case "e-mail protection":
		return T("e-mail protection", "e-posta koruması")
	case "client authentication":
		return T("client authentication", "istemci kimlik doğrulaması")
	case "digital signature":
		return T("digital signature", "dijital imza")
	case "non-repudiation":
		return T("non-repudiation", "inkâr edilemezlik")
	case "key encipherment":
		return T("key encipherment", "anahtar şifreleme")
	case "certificate signing":
		return T("certificate signing", "sertifika imzalama")
	case "CRL signing":
		return T("CRL signing", "CRL imzalama")
	}
	if n, ok := strings.CutPrefix(id, "usage "); ok {
		return Tf("usage %s", "kullanım %s", n)
	}
	return Plain(id)
}

func rootWords(l Lang, sig verify.Signature) wire.Text {
	if sig.ChainRoot == "" {
		return T("not stated", "belirtilmemiş")
	}
	if sig.RootTrusted {
		return Tf("%s - one of this filex's own signing authorities", "%s - bu filex'in kendi imza makamlarından biri", sig.ChainRoot)
	}
	return Tf("%s - not one of this filex's signing authorities", "%s - bu filex'in imza makamlarından biri değil", sig.ChainRoot)
}

func orUnstated(l Lang, s string) wire.Text {
	if strings.TrimSpace(s) == "" {
		return T("not stated", "belirtilmemiş")
	}
	return Plain(s)
}
