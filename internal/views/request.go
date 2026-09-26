package views

import (
	"fmt"
	"strings"

	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
	"github.com/brf-tech/filex-sign/internal/fields"
	"github.com/brf-tech/filex-sign/internal/fontkit"
)

// ── The requester's wizard ─────────────────────────────────────────────
//
// Eight steps (seven when there is one signer and so no order to ask
// about), each asking ONE thing, each with at most one primary
// button plus Back. The boxes are placed in a step of their own where
// the document has the whole screen — a signature page that needs
// scrolling in two directions is the complaint that started this round.
//
// The signer's screens (signer.go) are built from the same shell: same
// step strip, same single primary, same wording. Asking for a signature
// and giving one must not look like two products.

// Steps of the wizard.
const (
	StepSigners = 1
	StepOrder   = 2
	// ⚠⚠ TWO steps, not one. Naming a box and finding a place for it are
	// different jobs, and asking them together is what the owner saw as
	// "you are still doing both at once": a palette of types, a rectangle
	// under the pointer, and a strip of properties for whichever box
	// happened to be selected. StepBoxes asks WHAT is wanted of whom;
	// StepPlace asks only WHERE, with the document to itself.
	StepBoxes   = 3
	StepPlace   = 4
	StepTiming  = 5
	StepOptions = 6
	StepResult  = 7
	StepReview  = 8
)

// RequestState is what the wizard carries between steps (Surface.State).
type RequestState struct {
	Step       int               `json:"step"`
	Signers    []envelope.Person `json:"signers"`
	Identities string            `json:"identities"`
	Fields     []envelope.Field  `json:"fields"`
	Opts       envelope.Options  `json:"opts"`

	// Life is how long this installation lets the links live. It is a fact
	// about the HOST, read fresh on every call (never carried in the
	// surface state, hence `json:"-"`), so an administrator who changes the
	// ceiling mid-wizard is obeyed on the very next screen.
	Life LinkLife `json:"-"`
	// LinkDays is the life the links will really get, deadline included —
	// what the review says. The app works it out (it has the clock); 0
	// leaves the review with the asked-for number.
	LinkDays int `json:"-"`
	// Previous is the document's last request when it has ENDED: a new one
	// replaces its record, and the first step says so. nil when there is
	// none. (An OPEN one never gets this far — see AlreadyRequested.)
	Previous *envelope.Envelope `json:"-"`
}

// Defaults of the questions whose answer is a yes or a no.
//
// ⚠⚠ ONE constant for the field's declared default AND the state a new
// request starts from. They used to be two facts: the form declared "Let a
// signer refuse" and "Write an audit trail PDF" as ON while the fresh state
// held the zero value, and a form's value always wins over its default — so
// both started OFF, and somebody who clicked through got neither (found by
// an audit of milestone 3; the screenshot script had to tick the audit box by
// hand).
const (
	DefaultAllowDecline = true
	DefaultAudit        = true
)

// NewRequestState is the wizard as a person first sees it.
func NewRequestState() RequestState {
	st := RequestState{Step: StepSigners}
	st.Opts.AllowDecline = DefaultAllowDecline
	st.Opts.Audit = DefaultAudit
	return st.WithDefaults()
}

// LinkLife is what the wizard may offer for how long the links live.
//
// ⚠⚠ The signing page's own numbers (the manifest's default_ttl_days /
// max_ttl_days) are NOT the whole story: filex clamps every link to the
// installation's share ceiling as well (7 days unless an administrator
// changed it), silently. The wizard used to offer 14 and say "links valid
// 14 days" in its review while the links it opened lived 7 — measured on
// a live instance, 2026-09-21. So the ceiling is read from the call
// (CallContext.ShareMaxTTLDays) and the offer is pulled down to it.
type LinkLife struct {
	Default int // what a new request starts with
	Max     int // the most the Time step accepts
	// Capped says Max is the INSTALLATION's ceiling, not the app's own:
	// the Time step then says why the number is what it is.
	Capped bool
}

// OrDefaults fills a zero LinkLife (a hand-made state, a test) with the
// signing page's manifest numbers.
func (l LinkLife) OrDefaults() LinkLife {
	if l.Max <= 0 {
		l.Max = 90
	}
	if l.Default <= 0 {
		l.Default = 14
	}
	if l.Default > l.Max {
		l.Default = l.Max
	}
	return l
}

// Clamp keeps a number of days inside what the links can have; 0 or less
// is "not answered" and takes the default.
func (l LinkLife) Clamp(days int) int {
	l = l.OrDefaults()
	switch {
	case days < 1:
		return l.Default
	case days > l.Max:
		return l.Max
	}
	return days
}

// People is every signer in the order they will be asked: the ones
// picked from the directory first, then the identities typed by hand.
// The app builds the envelope from this very list, so the box
// assignments (s1, s2, …) mean the same thing on both sides.
func (st RequestState) People() []envelope.Person {
	out := append([]envelope.Person(nil), st.Signers...)
	seen := map[string]bool{}
	for _, p := range out {
		if p.HasEmail() {
			seen[strings.ToLower(p.Email)] = true
		}
	}
	for _, p := range ParseIdentities(st.Identities) {
		if p.HasEmail() && seen[strings.ToLower(p.Email)] {
			continue
		}
		if p.HasEmail() {
			seen[strings.ToLower(p.Email)] = true
		}
		out = append(out, p)
	}
	if len(out) > envelope.MaxSigners {
		out = out[:envelope.MaxSigners]
	}
	return out
}

// ParseIdentities reads the identity box: one signer per line, written
// as a name, an e-mail address, or both ("Ali Yılmaz <ali@ornek.com>").
// All three are identities; none of them is more real than the others.
func ParseIdentities(s string) []envelope.Person {
	var out []envelope.Person
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if p, ok := ParseIdentity(line); ok {
			out = append(out, p)
		}
	}
	return out
}

// ParseIdentity reads one line of the identity box.
func ParseIdentity(line string) (envelope.Person, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return envelope.Person{}, false
	}
	if i := strings.LastIndex(line, "<"); i >= 0 && strings.HasSuffix(line, ">") {
		name := strings.TrimSpace(line[:i])
		mail := strings.TrimSpace(line[i+1 : len(line)-1])
		return envelope.Person{Name: name, Email: strings.ToLower(mail)}, name != "" || mail != ""
	}
	if strings.Contains(line, "@") && !strings.Contains(line, " ") {
		return envelope.Person{Email: strings.ToLower(line)}, true
	}
	return envelope.Person{Name: line}, true
}

// Outside reports whether anybody signs from outside this filex, which
// is the only reason the PIN question is worth asking.
func (st RequestState) Outside() bool {
	for _, p := range st.People() {
		if p.UserID == 0 {
			return true
		}
	}
	return false
}

// Order is asked only when there is somebody to order.
func (st RequestState) needsOrder() bool { return len(st.People()) > 1 }

// visible is the run of steps this request actually shows.
func (st RequestState) visible() []int {
	all := []int{StepSigners, StepOrder, StepBoxes, StepPlace, StepTiming, StepOptions, StepResult, StepReview}
	var out []int
	for _, s := range all {
		if s == StepOrder && !st.needsOrder() {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Next / Prev walk the visible steps, so a request with one signer never
// lands on a question about the order of one person.
func (st RequestState) Next() int {
	v := st.visible()
	for i, s := range v {
		if s == st.Step && i+1 < len(v) {
			return v[i+1]
		}
	}
	return StepReview
}

// Prev is the step Back goes to.
func (st RequestState) Prev() int {
	v := st.visible()
	for i, s := range v {
		if s == st.Step && i > 0 {
			return v[i-1]
		}
	}
	return StepSigners
}

func stepTitle(l Lang, step int) wire.Text {
	switch step {
	case StepSigners:
		return T("Signers", "İmzacılar")
	case StepOrder:
		return T("Order", "Sıra")
	case StepBoxes:
		return T("The boxes", "Kutular")
	case StepPlace:
		return T("Place them", "Yerleştir")
	case StepTiming:
		return T("Time", "Süre")
	case StepOptions:
		return T("While it is open", "Açık kaldığı sürece")
	case StepResult:
		return T("When it is done", "Bittiğinde")
	}
	return T("Review", "Gözden geçir")
}

func requestStrip(l Lang, st RequestState) wire.Node {
	v := st.visible()
	labels := make([]wire.Text, 0, len(v))
	current := 1
	for i, s := range v {
		labels = append(labels, stepTitle(l, s))
		if s == st.Step {
			current = i + 1
		}
	}
	return steps(stepStates(current, labels...)...)
}

// Request draws the wizard at st.Step.
func Request(l Lang, doc Doc, st RequestState, labels []SignerLabel, errs map[string]wire.Text) *wire.Surface {
	st = st.WithDefaults()
	s := &wire.Surface{
		Title:  Tf("Request signatures — %s", "İmza iste — %s", doc.Name),
		Size:   "xl",
		State:  StateOf(st),
		Nodes:  []wire.Node{requestStrip(l, st)},
		Errors: errs,
	}
	switch st.Step {
	case StepSigners:
		s.Nodes = append(s.Nodes,
			heading(T("Who has to sign?", "Kim imzalayacak?")),
			text(T("Pick people from this filex — they sign inside it — or write the identity of somebody outside, who gets a private link. An identity is a name, an e-mail address, or both.",
				"Bu filex kurulumundaki kişileri seçin — onlar buranın içinde imzalar — ya da dışarıdan birinin kimliğini yazın; ona özel bir bağlantı gider. Kimlik: ad, e-posta ya da ikisi.")),
			wire.Node{ID: IDSigners, Type: "people-picker", Props: map[string]any{
				"value": peopleProp(st.Signers), "multi": true, "allow_external": true}},
			form([]wire.Field{
				withHelp(withHintIn(longField(l, "identities", "Identity", "Kimlik"),
					l, "Ali Yılmaz <ali@example.com>", "Ali Yılmaz <ali@ornek.com>"),
					l, "One signer per line: a name, an e-mail address, or both. Somebody with no address still gets a link and a PIN — shown to YOU, to pass on by hand.",
					"Her satıra bir imzacı: ad, e-posta ya da ikisi. Adresi olmayana da bağlantı ve PIN üretilir — SİZE gösterilir, elden iletirsiniz."),
			}, map[string]any{"identities": st.Identities}),
		)
		s.Nodes = append(s.Nodes, nameNotes(st.People())...)
		s.Nodes = append(s.Nodes, previousNote(st.Previous)...)
		s.Actions = []wire.SurfaceAction{nextButton()}

	case StepOrder:
		s.Nodes = append(s.Nodes,
			heading(T("In what order?", "Hangi sırayla?")),
			text(T("Everybody at once, or one after another in the order you listed them — the next person only hears from us when the one before has signed.",
				"Hepsi aynı anda ya da listelediğiniz sırayla birer birer — sıradaki kişiye ancak bir öncekinin imzası gelince haber verilir.")),
			form([]wire.Field{
				choice(l, "order", "Signing order", "İmza sırası", envelope.OrderParallel,
					opt(l, envelope.OrderParallel, "Everybody at once", "Hepsi aynı anda"),
					opt(l, envelope.OrderSequential, "One after another", "Birer birer")),
			}, map[string]any{"order": st.Opts.Order}),
		)
		s.Actions = []wire.SurfaceAction{backButton(), nextButton()}

	case StepBoxes:
		// What is wanted, of whom. No document on this screen at all: the
		// question here is the NAME the signer will be asked for.
		s.Nodes = append(s.Nodes,
			heading(T("What has to be filled in?", "Neler doldurulacak?")),
			text(T("One box for every signature you need, and one for anything that has to be written. Give each box a name and say whose it is — that name is what the signer is asked for. Where they go is the next step.",
				"Gereken her imza için bir kutu, yazılacak her şey için birer kutu. Her kutuya bir ad verin ve kimin olduğunu söyleyin — imzacıya sorulacak olan o addır. Yerlerini bir sonraki adımda seçeceksiniz.")),
			wire.Node{ID: IDFields, Type: "pdf-fields", Props: editorProps(l, doc, "define", st.Fields, labels)},
		)
		s.Nodes = append(s.Nodes, boxNameNotes(st.Fields)...)
		s.Actions = []wire.SurfaceAction{backButton(), nextButton()}

	case StepPlace:
		// Only WHERE. The document gets the screen; the boxes named a step
		// ago are handed out one at a time.
		s.Nodes = append(s.Nodes,
			text(T("Choose a box above, then tap the document where it goes. Drag instead of tapping to size it as you place it; a box already down can be moved, resized or taken off again.",
				"Yukarıdan bir kutu seçin, sonra belgede gideceği yere dokunun. Dokunmak yerine sürüklerseniz yerleştirirken boyutlandırırsınız; yerleşmiş bir kutuyu taşıyabilir, boyutlandırabilir ya da sayfadan kaldırabilirsiniz.")),
			wire.Node{ID: IDFields, Type: "pdf-fields", Props: editorProps(l, doc, "place", st.Fields, labels)},
		)
		s.Actions = []wire.SurfaceAction{backButton(), nextButton()}

	case StepTiming:
		o := st.Opts
		life := st.Life.OrDefaults()
		expiry := intField(l, "expiry", "Links are valid for (days)", "Bağlantılar kaç gün geçerli", life.Default, 1, life.Max)
		if life.Capped {
			// The number looks arbitrary without its reason, and the reason
			// is not the app's: say whose it is and who can change it.
			expiry = withHelpf(expiry, l,
				"At most %d: this installation keeps a shared link for no longer than that. An administrator can change it under Protection.",
				"En fazla %d: bu kurulum paylaşılan bir bağlantıyı bundan uzun tutmuyor. Yönetici bunu Koruma bölümünden değiştirebilir.", life.Max)
		}
		s.Nodes = append(s.Nodes,
			heading(T("How long do they have?", "Ne kadar süreleri var?")),
			form([]wire.Field{
				expiry,
				withHelp(withHint(strField(l, "deadline", "Sign by (optional)", "Son imza tarihi (isteğe bağlı)"), "2026-12-31"),
					l, "A day, as YYYY-MM-DD. The request closes when that day ends, and the links — and the freeze, if you set one — end with it.",
					"Gün, YYYY-AA-GG biçiminde. İstek o gün bitince kapanır; bağlantılar — ve koyduysanız dondurma — onunla birlikte sona erer."),
				withHelp(intField(l, "remind_every", "Remind the signers every (days)", "İmzacılara kaç günde bir hatırlatılsın", 0, 0, 60),
					l, "0 means never. Otherwise a signer who has not signed hears from us again after that many quiet days, and again after as many more — sent by filex's hourly wake-up while the request is open, to everybody filex can write to. “Remind” in the Signatures panel still sends one whenever you like.",
					"0 = hiç. Aksi hâlde imzalamamış bir imzacıya, o kadar gün ses çıkmayınca yeniden, sonra yine aynı aralıkla haber verilir — istek açık kaldıkça filex'in saatlik uyandırmasıyla, filex'in yazabildiği herkese. İmzalar panelindeki “Hatırlat” istediğiniz an bir tane daha gönderir."),
			}, map[string]any{"expiry": o.ExpiryDays, "deadline": o.Deadline, "remind_every": o.RemindEveryDays}),
		)
		s.Actions = []wire.SurfaceAction{backButton(), nextButton()}

	case StepOptions:
		o := st.Opts
		var fs []wire.Field
		vals := map[string]any{"allow_decline": o.AllowDecline, "lock": o.Lock, "lock_signed": o.LockSigned, "message": o.Message}
		if st.Outside() {
			fs = append(fs, withHelp(choice(l, "pin", "Protect the links with a PIN", "Bağlantıları PIN ile koru", "auto",
				opt(l, "auto", "Yes, a PIN per signer", "Evet, imzacı başına bir PIN"),
				opt(l, "none", "No PIN", "PIN yok")),
				l, "The PIN is shown to you and never mailed, so the link alone is not enough. People who sign inside filex are already signed in.",
				"PIN size gösterilir, e-postayla gönderilmez; böylece bağlantı tek başına yetmez. filex içinde imzalayanlar zaten oturum açmış olur."))
			vals["pin"] = o.PIN
		}
		fs = append(fs,
			boolField(l, "allow_decline", "Let a signer refuse", "İmzacı reddedebilsin", DefaultAllowDecline),
			withHelp(boolField(l, "lock", "Freeze the file while signatures are collected", "İmzalar toplanırken dosyayı dondur", false),
				l, "Nobody — not even an administrator — can change the file until the request ends. Only this app's own signing writes into it.",
				"İstek bitene kadar kimse — yönetici bile — dosyayı değiştiremez. Yalnız bu uygulamanın imzalaması yazabilir."),
			// ⚠⚠ The owner, 2026-09-22: after the last signature, "does the
			// signature say so — or do we lock the file? Both." The PDF
			// always says so (certified, sealed, its hash sent to every
			// party); locking the FILE is this choice, and the help says
			// what each answer means, because "lock" alone reads like the
			// freeze above.
			withHelp(boolField(l, "lock_signed", "Lock the signed file when every signature is in", "Her imza gelince imzalı dosyayı kilitle", false),
				l, "Yes: once the last signature and filex's seal are in, the signed file is locked for good — nobody, not even an administrator, can change, move or delete it until an administrator lifts the lock on this app's page under Apps (lifting it is recorded). No: afterwards the signed file is an ordinary file. Either way the document is certified and sealed and every party is sent its SHA-256, so any later change shows.",
				"Evet: son imza ve filex'in mührü eklenince imzalı dosya kalıcı olarak kilitlenir — bir yönetici kilidi Uygulamalar altındaki bu uygulamanın sayfasından kaldırana kadar (kaldırma kayda geçer) kimse, yönetici bile, onu değiştiremez, taşıyamaz ya da silemez. Hayır: bundan sonra imzalı dosya sıradan bir dosyadır. İki durumda da belge onaylanmış ve mühürlenmiştir ve her tarafa SHA-256 özeti gönderilir; sonradan yapılan her değişiklik görünür."),
			withHintIn(longField(l, "message", "A message to the signers (optional)", "İmzacılara mesaj (isteğe bağlı)"),
				l, "Please sign by Friday.", "Lütfen cumaya kadar imzalayın."),
		)
		s.Nodes = append(s.Nodes, heading(T("How should the request behave?", "İstek nasıl davransın?")), form(fs, vals))
		s.Actions = []wire.SurfaceAction{backButton(), nextButton()}

	case StepResult:
		o := st.Opts
		out := o.Output.Normalized()
		s.Nodes = append(s.Nodes,
			heading(T("What happens when everybody has signed?", "Herkes imzalayınca ne olsun?")),
			form([]wire.Field{
				choice(l, "output_mode", "The signed document is", "İmzalı belge", envelope.OutputVersion,
					opt(l, envelope.OutputVersion, "A new version of this file", "Bu dosyanın yeni sürümü"),
					opt(l, envelope.OutputSibling, "A new file beside it", "Yanına yeni bir dosya")),
				withHelp(withHint(onlyWhen(strField(l, "output_name", "Name of the new file", "Yeni dosyanın adı"),
					"output_mode", envelope.OutputSibling), envelope.DefaultSiblingName),
					l, "{stem} is the name without its extension, {ext} the extension.",
					"{stem} uzantısız ad, {ext} uzantıdır."),
				choice(l, "deliver", "Send it to the signers", "İmzacılara gönder", envelope.DeliverShare,
					opt(l, envelope.DeliverShare, "Yes, as a filex link by e-mail", "Evet, e-postayla filex bağlantısı olarak"),
					opt(l, envelope.DeliverNone, "No", "Hayır")),
				withHelp(shownWhen(choice(l, "delivery_pin", "Protect that link with a PIN", "O bağlantıyı PIN ile koru", "auto",
					opt(l, "auto", "Yes", "Evet"), opt(l, "none", "No", "Hayır")),
					"deliver", envelope.DeliverShare),
					l, "As with the signing links, the PIN is shown to you and never mailed.",
					"İmza bağlantılarında olduğu gibi PIN size gösterilir, e-postayla gitmez."),
				withHelp(boolField(l, "audit", "Write an audit trail PDF", "Denetim izi PDF'i yaz", DefaultAudit),
					l, "Who was asked, what each of them did, when, and with which certificate.",
					"Kimden istendi, her biri ne yaptı, ne zaman ve hangi sertifikayla."),
			}, map[string]any{
				"output_mode": out.Mode, "output_name": out.Name,
				"deliver": o.Deliver, "delivery_pin": o.DeliveryPIN, "audit": o.Audit,
			}),
		)
		s.Actions = []wire.SurfaceAction{backButton(), nextButton()}

	default:
		s.Nodes = append(s.Nodes, reviewNodes(l, st, labels)...)
		s.Actions = []wire.SurfaceAction{backButton(), primary("send", T("Send", "Gönder"))}
	}
	return s
}

func reviewNodes(l Lang, st RequestState, labels []SignerLabel) []wire.Node {
	var rows []row
	for i, lab := range labels {
		n, typed := 0, 0
		for _, f := range st.Fields {
			if f.Assignee != lab.ID {
				continue
			}
			if fields.Drawn(f.Type) {
				n++
			} else {
				typed++
			}
		}
		turn := Plain("—")
		if st.Opts.Order == envelope.OrderSequential {
			turn = Plain(fmt.Sprintf("%d.", i+1))
		}
		how := T("a link by e-mail", "e-postayla bağlantı")
		who := lab.Label
		people := st.People()
		if i < len(people) {
			who = people[i].Identity()
			switch {
			case people[i].UserID != 0:
				how = T("in filex", "filex içinde")
			case !people[i].HasEmail():
				how = T("a link you hand over", "elden vereceğiniz bağlantı")
			}
		}
		rows = append(rows, row{ID: lab.ID, Cells: map[string]wire.Text{
			"who": Plain(who), "turn": turn, "how": how,
			"boxes": BoxCount(n, typed),
		}})
	}
	// ⚠⚠ The boxes that belong to ANYONE are a row of their own. They were
	// counted for nobody — every signer's row said "0 to fill" while two
	// boxes were waiting to be filled, and a muted line under the table said
	// "Unassigned boxes: 2" in words nobody connected to the table (the
	// owner, 2026-09-21: "herkes işaretli doldurulacakları iki kişiye de
	// yazmıyoruz … kimlik kısmında 'Herkes' diye bir kimlik daha göster").
	// Now every box is in exactly one row, so the rows add up to the boxes
	// on the document. The name is the one the define step's button uses.
	anyDrawn, anyTyped := 0, 0
	for _, f := range st.Fields {
		if f.Assignee != "" {
			continue
		}
		if fields.Drawn(f.Type) {
			anyDrawn++
		} else {
			anyTyped++
		}
	}
	if anyDrawn+anyTyped > 0 {
		rows = append(rows, row{ID: "anyone", Cells: map[string]wire.Text{
			"who":   AnyoneWords(),
			"turn":  Plain("—"),
			"how":   T("whoever of them signs first", "içlerinden ilk imzalayan"),
			"boxes": BoxCount(anyDrawn, anyTyped),
		}})
	}
	out := []wire.Node{
		heading(T("Is this right?", "Doğru mu?")),
		list([]column{
			{Key: "who", Label: T("Identity", "Kimlik")},
			{Key: "turn", Label: T("Turn", "Sıra")},
			{Key: "how", Label: T("Reached", "Nasıl ulaşılır")},
			{Key: "boxes", Label: T("Boxes", "Kutular")},
		}, rows, T("No signers", "İmzacı yok")),
		muted(joinWords(requestFacts(st.Opts, st.LinkDays))),
	}
	out = append(out, nameNotes(st.People())...)
	if st.Opts.Audit {
		out = append(out, boxNameNotes(st.Fields)...)
	}
	return out
}

// nameNotes: the signers whose names cannot be printed whole under their
// signature (the stamp's face) — said on the step where they are typed.
func nameNotes(people []envelope.Person) []wire.Node {
	var out []wire.Node
	for i, p := range people {
		name := p.CertName()
		out = append(out, PrintNotes(fmt.Sprintf("signer-%d", i+1), name, name, fontkit.StampFace(), UnderTheSignature)...)
	}
	return out
}

// boxNameNotes: the box names the audit trail cannot print whole (its face
// is Inter) — said while they are typed on the define step.
func boxNameNotes(fs []envelope.Field) []wire.Node {
	var out []wire.Node
	for _, f := range fs {
		if f.Label != "" {
			out = append(out, PrintNotes("box-"+f.ID, f.Label, f.Label, fontkit.Get(fontkit.Inter), BoxNameInAudit)...)
		}
	}
	return out
}

// AnyoneWords is the identity a box that belongs to no one signer is
// listed under — the same word the define step's "whose is it" button says.
func AnyoneWords() wire.Text { return T("Anyone", "Herkes") }

// BoxCount is "1 signature · 2 to fill" in both languages, singular and
// plural in English ("2 signature" was what it said).
// ⚠ It says what the person is ASKED FOR, in those words. "0 signatures ·
// 3 to fill" is arithmetic about a person; "fills in 3 boxes" is what they
// were invited to do — and since a participant may now be asked for no
// signature at all (the owner, 2026-09-23), the arithmetic form would be
// the commonest row on the table and the least readable.
func BoxCount(drawn, typed int) wire.Text {
	switch {
	case drawn == 0 && typed == 0:
		return T("nothing yet", "henüz bir şey yok")
	case drawn == 0 && typed == 1:
		return T("fills in 1 box", "1 kutu doldurur")
	case drawn == 0:
		return Tf("fills in %d boxes", "%d kutu doldurur", typed)
	case typed == 0 && drawn == 1:
		return T("signs", "imzalar")
	case typed == 0:
		return Tf("signs %d times", "%d kez imzalar", drawn)
	case drawn == 1:
		return Tf("signs · %d to fill", "imzalar · %d doldurulacak", typed)
	}
	return Tf("signs %d times · %d to fill", "%d kez imzalar · %d doldurulacak", drawn, typed)
}

// requestFacts is the review in one line. linkDays is the life the links
// will REALLY get — the asked-for days, pulled down to the installation's
// ceiling and to the sign-by day — worked out by the app, which has the
// clock; 0 falls back to the asked-for number.
//
// ⚠ Every number here is a promise the person reads just before pressing
// Send. It has to be the one the links keep (see LinkLife).
func requestFacts(o envelope.Options, linkDays int) []wire.Text {
	out := []wire.Text{}
	if o.Order == envelope.OrderSequential {
		out = append(out, T("one after another", "birer birer"))
	} else {
		out = append(out, T("everybody at once", "hepsi aynı anda"))
	}
	switch {
	case o.Deadline != "" && linkDays > 0 && linkDays < o.ExpiryDays:
		out = append(out, Tf("links valid until the end of %s", "bağlantılar %s günü bitene kadar geçerli", Day(o.Deadline)))
	case linkDays > 0:
		out = append(out, Tf("links valid %d days", "bağlantılar %d gün geçerli", linkDays))
	default:
		out = append(out, Tf("links valid %d days", "bağlantılar %d gün geçerli", o.ExpiryDays))
	}
	if o.Deadline != "" {
		out = append(out, Tf("sign by %s", "son tarih %s", Day(o.Deadline)))
	}
	if o.PIN == "none" {
		out = append(out, T("no PIN", "PIN yok"))
	} else {
		out = append(out, T("a PIN per outside signer", "dış imzacı başına PIN"))
	}
	if o.Lock {
		out = append(out, T("the file is frozen meanwhile", "bu sürede dosya dondurulur"))
	}
	if o.LockSigned {
		out = append(out, T("the signed file is locked for good when every signature is in", "her imza gelince imzalı dosya kalıcı olarak kilitlenir"))
	} else {
		out = append(out, T("the signed file stays an ordinary file (its seal and hash still show any change)", "imzalı dosya sıradan bir dosya olarak kalır (mühür ve özet yine de her değişikliği gösterir)"))
	}
	if o.AllowDecline {
		out = append(out, T("refusing allowed", "reddetmeye izin var"))
	}
	if o.RemindEveryDays > 0 {
		out = append(out, Tf("a reminder every %d quiet days", "sessiz geçen her %d günde bir hatırlatma", o.RemindEveryDays))
	}
	if o.Audit {
		out = append(out, T("an audit trail PDF at the end", "sonunda denetim izi PDF'i"))
	}
	out = append(out, OutputWords(o.Output.Normalized()))
	if o.Sends() {
		if o.DeliveryPIN == "none" {
			out = append(out, T("the signed copy is mailed to everybody as a link", "imzalı kopya herkese bağlantı olarak postalanır"))
		} else {
			out = append(out, T("the signed copy is mailed to everybody as a PIN-protected link", "imzalı kopya herkese PIN korumalı bağlantı olarak postalanır"))
		}
	}
	return out
}

// OutputWords says where the result lands, in a phrase that fits a list.
func OutputWords(o envelope.Output) wire.Text {
	switch o.Mode {
	case envelope.OutputSibling:
		return Tf("the result is saved as %s", "sonuç %s olarak kaydedilir", o.Name)
	case envelope.OutputNone:
		return T("the document is left as it is", "belgeye dokunulmaz")
	}
	return T("the result is a new version of this file", "sonuç bu dosyanın yeni sürümü olur")
}

// WithDefaults fills what an empty or hand-made state misses.
func (st RequestState) WithDefaults() RequestState {
	if st.Step < StepSigners {
		st.Step = StepSigners
	}
	if st.Step > StepReview {
		st.Step = StepReview
	}
	// The asked-for days inside what the links can have: a state carried
	// from a screen drawn under a higher ceiling is pulled down here, before
	// the review can repeat a number the host would cut.
	st.Opts.ExpiryDays = st.Life.Clamp(st.Opts.ExpiryDays)
	if st.Opts.PIN == "" {
		st.Opts.PIN = "auto"
	}
	if st.Opts.Order == "" {
		st.Opts.Order = envelope.OrderParallel
	}
	if st.Opts.Output.Mode == "" {
		st.Opts.Output = envelope.Output{Mode: envelope.OutputVersion}
	}
	if st.Opts.Deliver == "" {
		st.Opts.Deliver = envelope.DeliverShare
	}
	if st.Opts.DeliveryPIN == "" {
		st.Opts.DeliveryPIN = "auto"
	}
	return st
}

func peopleProp(people []envelope.Person) []map[string]any {
	out := make([]map[string]any, 0, len(people))
	for _, p := range people {
		m := map[string]any{"email": p.Email}
		if p.UserID != 0 {
			m["user_id"] = p.UserID
		}
		if p.Name != "" {
			m["name"] = p.Name
		}
		out = append(out, m)
	}
	return out
}

// StateOf is the wizard state as the surface carries it.
//
// ⚠⚠ The boxes travel here in the RECORD's shape, never FieldProp's. The
// state is the plugin's own memory between two events — the browser echoes
// it back untouched and never reads it — so dressing it in the wire's
// shapes only means reading it back through a translation it does not
// need. It also meant losing: a text box's rule went out as an object,
// came back into a `string` field and was dropped without a word.
func StateOf(st RequestState) map[string]any {
	st = st.WithDefaults()
	return map[string]any{
		"view": "request", "step": st.Step, "signers": peopleProp(st.Signers),
		"identities": st.Identities, "fields": st.Fields, "opts": st.Opts,
	}
}
