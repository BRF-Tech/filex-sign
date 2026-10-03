package views

import (
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"

	"github.com/brf-tech/filex-sign/internal/envelope"
)

// ReceiptFile is one file the receipt offers for download. On a share
// the public shell lists them itself from the share's own file list;
// the names are repeated here so the screen can say what each one is.
type ReceiptFile struct {
	Name string
	What wire.Text
}

// ReceiptInput is one signer's proof of what they signed.
type ReceiptInput struct {
	Document string
	Signer   envelope.Person
	SignedAt string
	Serial   string
	CertFP   string
	CAName   string
	CAFP     string
	Files    []ReceiptFile
	// InApp points somebody who has an account at the Verify screen
	// instead of explaining how to check a file by hand.
	InApp bool
}

// Receipt is what a signer is handed the moment their signature lands.
//
// ⚠ It is an IDENTITY RECEIPT, not a signing capability, and the screen
// says so in those words: the private key that made the signature is
// destroyed the moment the signature is written, so nobody — not the
// signer, not this instance — can sign anything new with it. What the
// receipt carries is the certificate that names who signed, and the
// fingerprints to compare it against.
func Receipt(l Lang, in ReceiptInput) *wire.Surface {
	s := &wire.Surface{Title: T("Your signature receipt", "İmza makbuzunuz"), Size: "lg",
		State: map[string]any{"view": "receipt"}}
	s.Nodes = []wire.Node{
		heading(Tf("You signed “%s”.", "“%s” belgesini imzaladınız.", in.Document)),
		text(ReceiptSentence(l)),
	}
	rows := []row{
		{ID: "who", Cells: map[string]wire.Text{"k": T("Identity", "Kimlik"), "v": Plain(in.Signer.Identity())}},
		{ID: "doc", Cells: map[string]wire.Text{"k": T("Document", "Belge"), "v": Plain(in.Document)}},
	}
	if in.SignedAt != "" {
		rows = append(rows, row{ID: "when", Cells: map[string]wire.Text{
			"k": Tc("time", "Signed", "İmza zamanı"), "v": DayText(in.SignedAt)}})
	}
	if in.Serial != "" {
		rows = append(rows, row{ID: "serial", Cells: map[string]wire.Text{
			"k": T("Certificate serial", "Sertifika seri numarası"), "v": Plain(in.Serial)}})
	}
	if in.CertFP != "" {
		rows = append(rows, row{ID: "fp", Cells: map[string]wire.Text{
			"k": T("Certificate fingerprint (SHA-256)", "Sertifika parmak izi (SHA-256)"), "v": Plain(in.CertFP)}})
	}
	if in.CAName != "" {
		rows = append(rows, row{ID: "ca", Cells: map[string]wire.Text{
			"k": T("Signing authority", "İmza makamı"), "v": Plain(in.CAName)}})
	}
	if in.CAFP != "" {
		rows = append(rows, row{ID: "cafp", Cells: map[string]wire.Text{
			"k": T("Authority fingerprint (SHA-256)", "Makam parmak izi (SHA-256)"), "v": Plain(in.CAFP)}})
	}
	s.Nodes = append(s.Nodes, list([]column{
		{Key: "k", Label: T("What", "Ne")},
		{Key: "v", Label: T("It says", "Ne diyor")},
	}, rows, T("Nothing", "Yok")))

	if len(in.Files) > 0 {
		var frows []row
		for _, f := range in.Files {
			frows = append(frows, row{ID: f.Name, Cells: map[string]wire.Text{
				"name": Plain(f.Name), "what": f.What}})
		}
		s.Nodes = append(s.Nodes,
			divider(),
			text(T("These files are yours to keep:", "Bu dosyalar sizde kalsın:")),
			list([]column{
				{Key: "name", Label: T("File", "Dosya")},
				{Key: "what", Label: T("What it is", "Ne olduğu")},
			}, frows, T("None", "Yok")))
	}

	s.Nodes = append(s.Nodes, divider(), heading(T("How to check it later", "Sonradan nasıl doğrularsınız")))
	if in.InApp {
		s.Nodes = append(s.Nodes, text(T("Right-click the document in filex and choose “Verify”: the report names every signature, the authority behind it, and the same fingerprints you see here - compare them line by line.",
			"filex'te belgeye sağ tıklayıp “Doğrula” deyin: rapor her imzayı, arkasındaki makamı ve burada gördüğünüz parmak izlerinin aynısını listeler - satır satır karşılaştırın.")))
	} else {
		s.Nodes = append(s.Nodes, text(T("Open the signed PDF in a reader that checks signatures (Adobe Acrobat Reader, Okular, Firefox's PDF viewer). Import the authority certificate above once, and the signature shows as valid. Compare the certificate fingerprint the reader shows with the one on this page: if they match, the signature in that file is the one you made.",
			"İmzalı PDF'i imza denetleyen bir okuyucuda açın (Adobe Acrobat Reader, Okular, Firefox'un PDF görüntüleyicisi). Yukarıdaki makam sertifikasını bir kez içe aktarın, imza geçerli görünsün. Okuyucunun gösterdiği sertifika parmak izini bu sayfadakiyle karşılaştırın: aynıysa o dosyadaki imza sizin attığınız imzadır.")))
	}
	s.Nodes = append(s.Nodes, muted(Expectation(l)))
	return s
}

// ReceiptSentence is the sentence that has to appear on every surface
// and in every file of the receipt, in the same words.
func ReceiptSentence(l Lang) wire.Text {
	return T("This is an identity receipt, not a signing capability: it proves who signed and what was signed, and it cannot sign anything. The private key that made your signature was destroyed the moment the signature was written, so nobody - not you, not this installation - can sign something new with it.",
		"Bu bir kimlik makbuzudur, imza yeteneği değildir: kimin neyi imzaladığını kanıtlar, kendisiyle hiçbir şey imzalanamaz. İmzanızı üreten özel anahtar imza yazılır yazılmaz yok edildi; onunla kimse - ne siz ne de bu kurulum - yeni bir şey imzalayamaz.")
}
