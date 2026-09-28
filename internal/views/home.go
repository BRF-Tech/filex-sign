package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
)

// Row actions the Signatures screen offers, one per section, and the
// screen each one takes the person to on that document.
//
// A row is a document, so clicking it goes THERE: `Surface.Open` names
// the file and the screen to start on it. The host checks the screen is
// this plugin's and drops the link when the asker may not see that file,
// so offering to go somewhere can never become a way around permissions.
const (
	OpenSign   = "sign"
	OpenFollow = "follow"
	OpenVerify = "verify"
	// ShowPIN is not a place to go: it reads one link's PIN, in place, for
	// the person who made that link (app/views.go → host.SharePIN).
	ShowPIN = "showpin"
)

// Target is the action and the view a row action opens on the document.
// Exactly one of the two is named — naming both is refused, naming
// neither would just open the file.
func Target(rowAction string) (action, view string) {
	switch rowAction {
	case OpenSign:
		return "fill", ""
	case OpenVerify:
		return "verify", ""
	case OpenFollow:
		return "", "status"
	}
	return "", ""
}

// Card is one document on the Signatures screen.
type Card struct {
	Document string
	Path     string // adapter-qualified, and the id the row is known by
	Status   envelope.Status
	Signed   int
	Total    int
	Waiting  string // who is being waited on, already joined
	Deadline string
	Locked   bool
	Updated  string
	// Requester is who asked, for the rows that are not the actor's own.
	Requester string
}

// PinLink is one link this app minted whose PIN the requester was promised
// and, until now, was never shown again.
//
// ⚠⚠ The owner, 2026-09-23: "imzalama pinlerini sadece siz görürsünüz dedin
// ama o pinleri göstermiyorsun bir yerde". We tell the requester that a PIN
// never travels in the same mail as its link BECAUSE they will hand it over
// — and then had nowhere for them to read it.
//
// ⚠ ID is NOT the token. A row id reaches the browser; a live link's token
// in the DOM is a live link for anybody looking over a shoulder. The app
// resolves the row back to its token from its own record.
type PinLink struct {
	ID       string
	Document string
	Path     string
	// Who the link belongs to, and what sort of link it is.
	Who  wire.Text
	Kind wire.Text
	// State is the link's own state in words: waiting, opened, done,
	// expired, revoked.
	State wire.Text
	// Live: the link still opens, so its PIN is still worth something. A
	// finished, expired or revoked link is listed — it is history the
	// requester may need — but offers no PIN, because it would not work.
	Live bool
	// HasPIN: it was minted with one at all.
	HasPIN bool
	// PIN is filled for the ONE row just revealed; Reason says why it could
	// not be ("no_pin", "no_secret_key", "not_recoverable").
	PIN    string
	Reason string
}

// HomeInput is the Signatures screen: every request this app can see,
// split by what the person has to do with it.
type HomeInput struct {
	// ToSign is waiting for the actor's signature, Requested is what they
	// asked others for, Signed is what they have already signed, and All
	// is every request in the installation — administrators only, and
	// still only the files the asker may see.
	ToSign    []Card
	Requested []Card
	Signed    []Card
	All       []Card
	Admin     bool

	// Pins are the links THIS person minted whose PIN was promised to them
	// — one row per signing link and per download share (PinLink).
	Pins []PinLink
	// Shown is the PIN just read, for the row that asked. It lives for this
	// one answer and is never put in the surface's state: a secret in the
	// state is a secret posted back on every keystroke afterwards.
	Shown *PinLink

	// Section is the section asked for (the page's `?section=`); "" or one
	// this person does not have opens the default one (HomeDefault).
	Section string

	// Truncated says the host had more rows than this screen asked for.
	Truncated bool

	CAOK     bool
	CAReason string
	CAName   string
	CAFP     string
	Office   bool
	// CanRequest: the reader holds the app's `request` permission, so
	// "How this works" may tell them to "Request signatures…".
	CanRequest bool
}

// The Signatures page's sections — the page's menu (wire.Surface.Sections),
// each its own table. ⚠ The ids are part of an ADDRESS (`?section=`) that
// notifications and bookmarks carry: rename one and every link to it lands
// on the default instead.
const (
	SectionToSign    = "to-sign"
	SectionRequested = "requested"
	SectionSigned    = "signed"
	SectionPins      = "pins"
	SectionAll       = "all"
	SectionAbout     = "about"
)

// HomeSections is the menu this person gets, in order: the three that are
// about them, "every request" for an administrator, and how it all works.
func HomeSections(in HomeInput) []string {
	out := []string{SectionToSign, SectionRequested, SectionSigned, SectionPins}
	if in.Admin {
		out = append(out, SectionAll)
	}
	return append(out, SectionAbout)
}

// HomeDefault is the section a page opened without one shows: the first
// that has something in it, so a person who has something to sign lands on
// it, and one who only asks lands on what they asked for.
func HomeDefault(in HomeInput) string {
	for _, s := range HomeSections(in) {
		if len(homeCards(in, s)) > 0 {
			return s
		}
	}
	return SectionToSign
}

func homeCards(in HomeInput, section string) []Card {
	switch section {
	case SectionToSign:
		return in.ToSign
	case SectionRequested:
		return in.Requested
	case SectionSigned:
		return in.Signed
	case SectionAll:
		if in.Admin {
			return in.All
		}
	}
	return nil
}

// homeCount is what the menu entry counts. Every section but the PINs one
// counts documents; that one counts LINKS, which is what its table holds.
func homeCount(in HomeInput, section string) int {
	if section == SectionPins {
		return len(in.Pins)
	}
	return len(homeCards(in, section))
}

func homeSectionTitle(section string) wire.Text {
	switch section {
	case SectionToSign:
		return T("Waiting for my signature", "İmzamı bekleyenler")
	case SectionRequested:
		return T("I asked for these", "Benim istediklerim")
	case SectionSigned:
		return T("I have signed these", "İmzaladıklarım")
	case SectionAll:
		return T("Every request", "Tüm istekler")
	case SectionPins:
		return T("PINs", "PIN'ler")
	}
	return T("How it works", "Nasıl çalışır")
}

// Home is the `envelopes` screen under Apps: a page with a MENU — what is
// waiting for me, what I asked for, what I have signed, every request (an
// administrator), and how it works — and one table per section.
//
// ⚠⚠ The owner, 2026-09-21: "İmzalar popup açıyor ama bence popup yerine
// kendi sayfasını açsın ve her biri ayrı bir menü içinde farklı tablolar
// göstersin". It used to be one dialog with four tables and a manual under
// them, scrolled through to find the one that mattered. Each section is now
// its own table behind its own menu entry, the entry counting its rows, and
// the page keeps the section in its address (the host's frame does that).
// The tables are the `list` node, drawn by the product's own table.
//
// A deleted document is not listed: a request lives with its file and goes
// when the file goes.
func Home(l Lang, in HomeInput) *wire.Surface {
	sec := in.Section
	known := false
	for _, s := range HomeSections(in) {
		if s == sec {
			known = true
		}
	}
	if !known {
		sec = HomeDefault(in)
	}
	s := &wire.Surface{Title: T("Signatures", "İmzalar"), Size: "xl", Section: sec,
		State: map[string]any{"view": "envelopes", "section": sec}}
	for _, id := range HomeSections(in) {
		entry := wire.Section{ID: id, Label: homeSectionTitle(id)}
		if id != SectionAbout {
			n := homeCount(in, id)
			entry.Count = &n
		}
		s.Sections = append(s.Sections, entry)
	}

	if !in.CAOK {
		s.Nodes = append(s.Nodes, danger(Tf("Signing is not available on this installation: %s",
			"Bu kurulumda imzalama kullanılamıyor: %s", SigningUnavailable(in.CAReason))))
	}
	switch sec {
	case SectionToSign:
		s.Nodes = append(s.Nodes, section(l, homeSectionTitle(sec), in.ToSign,
			T("Nothing is waiting for you.", "Sizi bekleyen bir şey yok."), true, OpenSign,
			T("Sign", "İmzala"))...)
	case SectionRequested:
		s.Nodes = append(s.Nodes, section(l, homeSectionTitle(sec), in.Requested,
			T("You have not asked anybody to sign yet.", "Henüz kimseden imza istemediniz."), true, OpenFollow,
			T("Follow", "Takip et"))...)
	case SectionSigned:
		s.Nodes = append(s.Nodes, section(l, homeSectionTitle(sec), in.Signed,
			T("You have not signed anything here yet.", "Burada henüz bir şey imzalamadınız."), false, OpenVerify,
			T("Verify", "Doğrula"))...)
	case SectionPins:
		s.Nodes = append(s.Nodes, pinSection(l, in)...)
	case SectionAll:
		s.Nodes = append(s.Nodes, section(l, T("Every request in this installation", "Bu kurulumdaki tüm istekler"),
			in.All, T("No requests.", "İstek yok."), true, OpenFollow, T("Follow", "Takip et"))...)
		s.Nodes = append(s.Nodes, muted(T("Only the documents you may see are listed — this screen never widens anybody's reach.",
			"Yalnız görebileceğiniz belgeler listelenir — bu ekran kimsenin erişimini genişletmez.")))
	default:
		s.Nodes = append(s.Nodes, homeAbout(l, in)...)
	}
	if in.Truncated && sec != SectionAbout {
		s.Nodes = append(s.Nodes, muted(T("There is more than this screen shows. The explorer filters and searches; this is a list, not a search.",
			"Bu ekranın gösterdiğinden fazlası var. Süzmek ve aramak için dosya gezginini kullanın; burası bir liste, arama değil.")))
	}
	return s
}

// homeAbout is the "How it works" section: how to start, what a box is,
// who can sign, and which authority signs.
func homeAbout(l Lang, in HomeInput) []wire.Node {
	start := T("Right-click a document → “Sign…” to sign it yourself. While a request is open the same menu offers “Sign / Fill” on that document. “Verify” is offered on every PDF, signed here or anywhere else.",
		"Bir belgeye sağ tıklayın → kendiniz imzalamak için “İmzala…”. Bir istek açıkken aynı menü o belgede “İmzala / Doldur” gösterir. “Doğrula” her PDF'te vardır — burada imzalanmış olsun olmasın.")
	if in.CanRequest {
		start = T("Right-click a document → “Sign…” to sign it yourself, or “Request signatures…” to ask others. While a request is open the same menu offers “Sign / Fill” on that document. “Verify” is offered on every PDF, signed here or anywhere else.",
			"Bir belgeye sağ tıklayın → kendiniz imzalamak için “İmzala…”, başkalarından istemek için “İmza iste…”. Bir istek açıkken aynı menü o belgede “İmzala / Doldur” gösterir. “Doğrula” her PDF'te vardır — burada imzalanmış olsun olmasın.")
	}
	n := []wire.Node{heading(T("How this works", "Bu nasıl çalışır")),
		text(start),
		text(T("Boxes have names. Whoever places them names each one, and the signer fills a plain form of those names before seeing the finished document and approving it.",
			"Kutuların adı vardır. Yerleştiren kişi her birine bir ad verir; imzacı da önce o adlardan oluşan sade bir formu doldurur, sonra belgenin son hâlini görüp onaylar.")),
		text(T("A signer does not need an e-mail address. An identity is a name, an address, or both; somebody with no address gets a link and a PIN that are shown to the requester, who hands them over.",
			"İmzacının e-posta adresi olmak zorunda değil. Kimlik: ad, adres ya da ikisi; adresi olmayana da bağlantı ve PIN üretilir, bunlar isteği gönderene gösterilir, o elden iletir.")),
		text(Expectation(l)),
	}
	if in.CAFP != "" {
		n = append(n, muted(Tf("Signing authority: %s · SHA-256 %s", "İmza makamı: %s · SHA-256 %s", in.CAName, in.CAFP)))
	}
	if in.Office {
		n = append(n, muted(T("Office documents (ODT, DOCX, XLSX, PPTX…) are converted to PDF here before signing; the signed PDF is saved beside the original, which is never changed.",
			"Ofis belgeleri (ODT, DOCX, XLSX, PPTX…) imzalanmadan önce burada PDF'e çevrilir; imzalı PDF aslının yanına kaydedilir, aslına dokunulmaz.")))
	} else {
		n = append(n, muted(T("This installation has no LibreOffice, so only PDFs can be signed. Convert an office document to PDF first.",
			"Bu kurulumda LibreOffice yok, bu yüzden yalnız PDF imzalanabilir. Ofis belgesini önce PDF'e dönüştürün.")))
	}
	return n
}

func section(l Lang, title wire.Text, cards []Card, empty wire.Text, live bool,
	action string, actionLabel wire.Text) []wire.Node {

	var rows []row
	for _, c := range cards {
		cells := map[string]wire.Text{
			"doc":      Plain(c.Document),
			"state":    StatusWords(c.Status),
			"progress": Plain(fmt.Sprintf("%d/%d", c.Signed, c.Total)),
			"waiting":  Plain(orDash(c.Waiting)),
			"due":      Plain(orDash(c.Deadline)),
		}
		if c.Locked {
			state := StatusWords(c.Status)
			cells["state"] = Each(func(l Lang) string { return l.Sf("%s · frozen", "%s · dondurulmuş", In(state, l)) })
		}
		if c.Requester != "" {
			cells["doc"] = Tf("%s — asked by %s", "%s — isteyen %s", c.Document, c.Requester)
		}
		rows = append(rows, row{ID: c.Path, Cells: cells,
			Actions: []rowAction{{ID: action, Label: actionLabel}}})
	}
	cols := []column{
		{Key: "doc", Label: T("Document", "Belge")},
		{Key: "state", Label: T("State", "Durum")},
		{Key: "progress", Label: Tc("column of counts", "Signed", "İmza")},
	}
	if live {
		cols = append(cols,
			column{Key: "waiting", Label: T("Waiting for", "Beklenen")},
			// ⚠ The day as a DATE, not as text: filex prints it the way
			// the explorer prints dates ("29 Eyl 2026"). An ISO day in the
			// cell read "2026-09-29" beside "22 Eyl 2026, 14:01" one column
			// over in the file list (v0.43.0 wave 2).
			column{Key: "due", Label: T("Due", "Son tarih"), Format: "date"})
	}
	// ⚠ No heading over the table: the page's menu already names the
	// section, and the same words twice — a tab, then a heading under it —
	// read as two things. `title` stays the section's name for callers that
	// draw a table without a menu.
	_ = title
	return []wire.Node{list(cols, rows, empty)}
}

// pinSection is the PINs table: every link this person minted that is
// protected by a PIN, and the one audited way back to it.
//
// ⚠⚠ The PIN is NOT read to draw the table. Reading one is an audited act
// (the host writes `share.pin_revealed` for every read, successful or not),
// so a table that read twenty of them to render itself would write twenty
// audit rows every time somebody glanced at the screen. The row says
// whether a PIN is worth asking for; asking is the row's own action.
func pinSection(l Lang, in HomeInput) []wire.Node {
	var rows []row
	for _, p := range in.Pins {
		cells := map[string]wire.Text{
			"doc": Plain(p.Document), "who": p.Who, "kind": p.Kind, "state": p.State,
			"pin": T("hidden", "gizli"),
		}
		var acts []rowAction
		switch {
		case !p.HasPIN:
			cells["pin"] = T("no PIN on this link", "bu bağlantıda PIN yok")
		case p.PIN != "":
			cells["pin"] = Plain(p.PIN)
		case p.Reason != "":
			cells["pin"] = pinReasonWords(p.Reason)
		case !p.Live:
			// ⚠ A finished, expired or revoked link's PIN opens nothing. The
			// verb is not offered, because offering it would be offering a
			// secret that cannot be used — and reading it would still be a
			// read in the audit trail.
			cells["pin"] = T("the link has ended", "bağlantı sona erdi")
		default:
			acts = append(acts, rowAction{ID: ShowPIN, Label: T("Show PIN", "PIN'i göster")})
		}
		rows = append(rows, row{ID: p.ID, Cells: cells, Actions: acts})
	}
	out := []wire.Node{
		text(T("The PIN of a link never travels in the same message as the link — you hand it over. This is where you read it back.",
			"Bir bağlantının PIN'i, bağlantıyla aynı mesajda gitmez — onu siz iletirsiniz. Buradan geri okuyabilirsiniz.")),
		list([]column{
			{Key: "doc", Label: T("Document", "Belge")},
			{Key: "who", Label: T("Whose link", "Kimin bağlantısı")},
			{Key: "kind", Label: T("Kind", "Tür")},
			{Key: "state", Label: T("State", "Durum")},
			{Key: "pin", Label: T("PIN", "PIN")},
		}, rows, T("None of your links is protected by a PIN.", "Bağlantılarınızın hiçbiri PIN ile korunmuyor.")),
	}
	if p := in.Shown; p != nil && p.PIN != "" {
		out = append(out, form([]wire.Field{
			withHelp(labelled("pin", "string", In(p.Who, l)), l,
				"Copy it and pass it on yourself — by a different route from the link. It is shown for this one answer; press “Show PIN” again whenever you need it, and every read is recorded.",
				"Kopyalayıp kendiniz iletin — bağlantıdan farklı bir yoldan. Yalnız bu cevapta görünür; gerektikçe yeniden “PIN'i göster” deyin, her okuma kayda geçer."),
		}, map[string]any{"pin": p.PIN}))
	}
	if p := in.Shown; p != nil && p.Reason != "" {
		out = append(out, danger(pinReasonWords(p.Reason)), muted(pinRemedy(p.Reason)))
	}
	out = append(out, muted(T("Only the links you asked for are listed, and only you can read their PINs. Every read is written to filex's audit trail.",
		"Yalnız sizin istediğiniz bağlantılar listelenir ve PIN'lerini yalnız siz okuyabilirsiniz. Her okuma filex'in denetim izine yazılır.")))
	return out
}

// PinLinksOf lists the PIN-protected links one request holds, in reading
// order: each participant's own signing link, the receipt each of them was
// handed, and the download share the finished file went out on. Beside the
// rows it answers the TOKEN of each, which is how the caller reads a PIN —
// and which is why the token is not in the row.
//
// ⚠ A link with no PIN is listed too, saying so. "No PIN on this link" is a
// fact the requester needs (nothing to hand over); leaving the row out
// would look like the link was missing.
func PinLinksOf(env *envelope.Envelope, path, document string, now time.Time) ([]PinLink, map[string]string) {
	var out []PinLink
	tokens := map[string]string{}
	add := func(kindID string, sgID string, p PinLink, token string) {
		p.ID = env.ID + "|" + kindID + "|" + sgID
		p.Document, p.Path = document, path
		out = append(out, p)
		tokens[p.ID] = token
	}
	closed := env.Status.Closed()
	for i := range env.Signers {
		sg := env.Signers[i]
		if sg.PageToken == "" {
			// Somebody who signs inside filex has no link and therefore no
			// PIN: they are already signed in.
			continue
		}
		live := !closed && !sg.Done() && !past(sg.PageExpires, now)
		add("signing", sg.ID, PinLink{
			Who:    Plain(sg.Person.Identity()),
			Kind:   T("signing link", "imza bağlantısı"),
			State:  linkState(ActWords(sg.Status, env.Signs(sg.ID)), live, past(sg.PageExpires, now)),
			Live:   live,
			HasPIN: env.Options.PIN != "none",
		}, sg.PageToken)
		if sg.ReceiptToken != "" {
			rlive := !past(sg.ReceiptExpires, now)
			add("receipt", sg.ID, PinLink{
				Who:    Plain(sg.Person.Identity()),
				Kind:   T("receipt", "makbuz"),
				State:  linkState(T("Handed over", "Verildi"), rlive, !rlive),
				Live:   rlive,
				HasPIN: env.Options.PIN != "none",
			}, sg.ReceiptToken)
		}
	}
	if env.DeliveryToken != "" {
		live := !past(env.DeliveryExpires, now)
		add("delivery", "", PinLink{
			Who:    T("everybody — the signed file", "herkes — imzalı dosya"),
			Kind:   T("download link", "indirme bağlantısı"),
			State:  linkState(T("Sent", "Gönderildi"), live, !live),
			Live:   live,
			HasPIN: env.Options.DeliveryPIN != "none",
		}, env.DeliveryToken)
	}
	return out, tokens
}

// linkState is one link's state in words: what it was doing, or that it has
// ended — and an ended link's PIN is not offered, because it opens nothing.
func linkState(doing wire.Text, live, expired bool) wire.Text {
	switch {
	case live:
		return doing
	case expired:
		return T("Expired", "Süresi doldu")
	}
	return doing
}

// past reports whether an RFC3339 stamp is behind us. An empty one is not:
// a link with no end does not expire.
func past(stamp string, now time.Time) bool {
	if stamp == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return false
	}
	return !now.Before(t)
}

// pinReasonWords says WHICH of the three it is. ⚠ Never a blank and never a
// dash: "there is no PIN", "this installation cannot show any PIN" and "this
// one is gone" are three different facts, and a person who is shown nothing
// cannot tell them apart from a bug.
func pinReasonWords(reason string) wire.Text {
	switch reason {
	case "no_pin":
		return T("no PIN on this link", "bu bağlantıda PIN yok")
	case "no_secret_key":
		return T("this installation cannot show PINs", "bu kurulum PIN gösteremiyor")
	}
	return T("this PIN cannot be shown any more", "bu PIN artık gösterilemiyor")
}

// pinRemedy is what DOES work, said next to what does not.
func pinRemedy(reason string) wire.Text {
	if reason == "no_secret_key" {
		return T("An administrator has not given this installation a secret key, so no PIN it mints can ever be read back. Until they do, note a PIN down when the request is sent — it is in the job's message and in your bell.",
			"Yönetici bu kuruluma bir gizli anahtar vermemiş; bu yüzden ürettiği hiçbir PIN geri okunamaz. Verilene kadar PIN'i istek gönderilirken not edin — iş mesajında ve bildiriminizde yazar.")
	}
	return T("The link still works; only its PIN is gone. Cancel the request and send it again to get a link with a PIN you can read, or reach the person another way.",
		"Bağlantı hâlâ çalışıyor; yalnız PIN'i kayıp. Okuyabileceğiniz bir PIN için isteği iptal edip yeniden gönderin ya da kişiye başka bir yoldan ulaşın.")
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// Dates for people are written through i18n's Day / When / Span (the SDK's
// humandate — filex's own format), never as an ISO day.

// SigningUnavailable is why this installation cannot sign, in words a
// reader can act on.
//
// ⚠ The host's reason is English and technical ("FILEX_SECRET_KEY is not
// set"), and it used to be spliced as it is into the reader's sentence:
// "Bu kurulumda imzalama kullanılamıyor: FILEX_SECRET_KEY is not set"
// (2026-09-26). The app keeps the host's own words in its log.
func SigningUnavailable(reason string) wire.Text {
	switch r := strings.ToLower(reason); {
	case strings.Contains(r, "filex_secret_key"):
		return T("the server has no FILEX_SECRET_KEY, which signing needs — an administrator sets it and restarts filex",
			"sunucuda imzalamanın gerektirdiği FILEX_SECRET_KEY ayarlı değil — bir yönetici ayarlayıp filex'i yeniden başlatmalı")
	case strings.Contains(r, "permission"):
		return T("this app was not granted signing — an administrator reviews its permissions",
			"bu uygulamaya imzalama izni verilmemiş — bir yönetici izinlerini gözden geçirmeli")
	case r == "" || strings.Contains(r, "not enabled"):
		return T("an administrator has not enabled signing", "bir yönetici imzalamayı henüz etkinleştirmedi")
	}
	return T("the server could not provide a signing authority — the reason is in the app's log",
		"sunucu bir imza makamı sağlayamadı — sebebi uygulamanın günlüğünde")
}

// ErrWords names a host error code (pluginkit.HostError.Code) for a person.
// The code alone — "rate_limited", "permission_denied" — was printed inside
// a Turkish sentence (2026-09-26).
func ErrWords(code string) wire.Text {
	switch code {
	case "permission_denied":
		return T("permission denied", "izin verilmedi")
	case "not_found":
		return T("not found", "bulunamadı")
	case "too_large":
		return T("too large", "çok büyük")
	case "timeout":
		return T("it took too long", "zaman aşımına uğradı")
	case "unavailable":
		return T("the service is not available here — mail may not be set up", "hizmet burada kullanılamıyor — e-posta ayarlanmamış olabilir")
	case "busy", "rate_limited":
		return T("too many attempts — try again later", "çok sık denendi — biraz sonra yeniden deneyin")
	case "invalid":
		return T("the request was refused as invalid", "istek geçersiz sayılıp reddedildi")
	}
	return T("an unexpected error — the details are in the app's log", "beklenmeyen bir hata — ayrıntı uygulamanın günlüğünde")
}
