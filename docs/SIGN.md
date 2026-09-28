# filex-sign in depth

The long version of the [README](../README.md): what each piece does, why it
is shaped that way, and the caveats that are easier to read once than to
discover twice. It describes this tree; the [changelog](../CHANGELOG.md)
says which release each piece arrived in.

---

## 1. What a signature here is

A **PAdES-B** signature (`/SubFilter /ETSI.CAdES.detached`), ECDSA P-256 over
SHA-256, appended to the PDF as an **incremental update**: the original bytes
are copied unchanged, so every earlier signature keeps verifying over its own
byte range, and the pre-signature file is recoverable by truncating at its
first `%%EOF`.

Each signature carries a certificate issued for that one signing by this
installation's **signing authority** — the CA filex generated for the tenant,
or the organisation's own CA if an administrator imported one. The private key
never enters the sandbox: the plugin asks the host to issue a certificate, the
host signs the CMS digest with the matching key, and the plugin then asks the
host to **destroy the key**. What is left in the file proves who signed; it
cannot sign anything else.

**Optional:** an RFC 3161 **time stamp** (§12). Off by default.

### What it is not

- **Not a qualified electronic signature.** Where the law asks for one (in
  Turkey: e-imza / m-imza), use that. AATL and EUTL membership are not a goal.
- **Not LTV.** No DSS dictionary, no embedded revocation material. Validity
  rests on the authority you imported.
- **Not a lock against a determined editor.** A PDF can always be edited; what
  the certification and the seal (§7a) guarantee is that the edit SHOWS — a
  reader reports it as a change that is not permitted — and the SHA-256 every
  party holds no longer matches. Locking the FILE is the request's option.

### The sentence

> A signature made here comes from this installation's own signing authority.
> A reader who imports that authority's certificate once sees these signatures
> as valid; a reader who has not says the validity is unknown, which is not
> the same as invalid.

That wording is in the home screen, the verification report, the audit trail
and every receipt, in all five languages. A wrong expectation is worse than no
signature at all.

Its Spanish, German and French are AI-translated and await review by a native
speaker (corrections welcome); English and Turkish are the source languages,
written in the code. A sentence that decides what a reader may expect of a
signature is exactly the kind a native speaker should read.

---

## 2. Trust: the signing authorities

`host_sign_info` answers with **`ca_certs_pem`** — every CA certificate this
tenant has ever signed with, the live one first. Importing an organisation's
own CA, or rotating the generated one, **retires** the previous authority; it
is never deleted.

⚠ Build the trust pool from that whole bundle, never from the live
certificate alone: trusting only the current authority would make every
signature made before a rotation read as "untrusted". `internal/host` exposes
it as `SignInfo.Authorities`, and `internal/verify` puts all of them in the
`x509.CertPool` it verifies against. The Verify screen lists them with their
fingerprints and says which one is in use.

A reader outside filex needs the certificate once. filex serves it to an
administrator at `GET /api/admin/app-plugins/signing/ca.pem` (and every
authority, retired ones included, at `GET …/signing/cas`); there is no button
for it in the panel yet, so the README says how to fetch it and how to import
it into each reader.

---

## 3. The envelope (`internal/envelope`)

The signature request as data, kept in filex's per-file state under the
document (key `envelope`, ≤ 64 KiB), so it moves and vanishes with the file.

```go
Envelope{Schema, ID, Status, Document, Requester, Options, Signers, Fields,
         Events, CreatedAt, UpdatedAt, ClosedAt, SignCount, Locked, LockUntil,
         Form, DeliveryToken, DeliveryURL, DeliveryExpires}
```

**Schema 3** (the third milestone before the first release) added: named boxes, identities without an e-mail
address, delivery of the finished document, the receipt each signer is handed,
and `Form` — the flag that says this request's typed boxes exist in the PDF as
real AcroForm fields. A record written by an older build keeps `Form: false`
and is finished the old way, because its boxes were never created as fields.

`Decode` migrates what it reads without claiming the record is newer than it
is: a text box that carried the removed `date` **rule** becomes a `date`
**box**, and an old request's delivery is set to "none" — those signers were
never promised a copy.

### Identity

```go
Person{UserID, Email, Name}
```

**At least one of `Name` and `Email`**; neither is privileged. `Identity()`
prints the name with the address beside it, or whichever one there is — never
an empty half, never "e-mail: —". `CertName()` is what the certificate is
issued to: the name when there is one, the address otherwise, and the e-mail
SAN is written only when there **is** an address.

⚠ An inside signer with no display name is named in the certificate and the
signature line by their **e-mail**, not their filex username — deliberately
(owner's decision, 2026-09-22): a certificate has to identify the person
outside filex, where a username such as `admin2` means nothing. The app's
screens follow filex's own rule instead — the name the host hands over, else
the e-mail (`Display()`): the Signatures home's *Waiting for* column and the
status table print "Gülşen", not "Gülşen (gulsen@local)".

Dates a person reads — screens, mails, notices — are written the way filex
writes dates (*29 Eyl 2026*, *Sep 29, 2026*) through the SDK's
`pluginkit/humandate`: pass an `i18n.Day` / `When` / `Span` to `Sf`/`Tf`, or
use `DayText` in a table cell. The audit trail keeps ISO 8601 (it must be
exact), and so do stored stamps and form values.

`HasEmail()` is the single question that decides how anything reaches a
person: by mail when there is an address, through the requester when there is
not.

### The state machine (`machine.go`)

`Apply(env, event) → (env', effects, error)`, pure, never touching the host.
The caller performs the effects: revoke a share, lift the freeze, notify the
requester, invite whoever is next. Every path that closes an envelope goes
through one `finish()`, which is why no ending of the flow can leave the file
frozen or a link alive.

Events: `notified`, `viewed`, `signed`, `reminded`, `declined`, `cancelled`,
`expired`, `link_ended` (§12b).

---

## 4. The menu: state keys

The plugin keeps two value-free markers on the document:

- **`pending`** — a request is open. filex exposes it as `sign:pending`, which
  is what puts **Sign / Fill** in the right-click menu of exactly the
  documents that wait for one, and keeps **Sign…** / **Request signatures…**
  off them.
- **`signed`** — this app signed this document. ⚠ This is a **badge, not a
  gate**. **Verify** is offered on every PDF: the app only knows what it
  signed itself, and the document that most needs checking is the one it did
  not.

---

## 5. Boxes and their rules (`internal/fields`)

Five kinds: `signature`, `initials`, `date`, `text`, `checkbox`.

Text rules: **free**, **number**, **email**, plus length bounds. There is no
`date` text rule any more — `date` is a field type, and two ways to ask for
the same thing is how you get two answers. `fields.Migrate` turns an old
text+date box into a date box on read.

Every box has a **name** and an **identity**, and they are different things
(the owner, 2026-09-21: *"ASCII kimlik üretsin ama göstermesin hiç — adam
isterse Japonca yazsın alanı; isim ayrı, kimlik ayrı olsun"*).

- The **name** (`Label`) is what people read, in any script, **exactly as
  typed**: the editor, the signer's form, the field's `/TU` tooltip inside
  the PDF, the audit trail and every refusal ("Görev unvanı: bu alan
  zorunlu"). A box nobody named **stays unnamed** in the record, and every
  screen shows its kind's name in *that reader's* language, numbered when
  there are several ("İmza 1", "Signature 2" — `views.NameIn`). It used to be
  written into the record in the requester's language, so a Turkish signer of
  an English requester's document was asked for "Signature" and "Date".
- The **identity** (`Key`) is the PDF form field's own name (`/T`): the name
  folded to ASCII (`fields.Slug`: "Müşteri adı" → `musteri-adi`, ş→s, ğ→g,
  ı→i, İ→i …, spaces and anything else → `-`), the box's type when nothing
  folds (Japanese, emoji), made unique in the document (`imza`, `imza-2`) and
  clear of the form fields the PDF already carries. It is **never shown**.
  ⚠ It follows the name while the wizard is open and is **fixed when the
  request is sent** (`envelope.AssignKeys`, once, in the request job), so a
  field that exists in a sent document never changes its name under the
  signers. A record written before identities existed keeps naming its
  fields by id.

A signature or initials box is **drawn** (the default; the pad offers draw or
a picture of a signature) or **typed** (`Style: "typed"`: the signer's name set
in the face the requester chose, and only then is a face asked for). And it
chooses the **lines printed under it** (§9).

Every rule is checked **twice**: on the screen the signer fills, so a refusal
is a sentence they can act on, and again in the job that writes the document,
so a crafted submission cannot slip a value past the screen.

---

## 6. Fonts (`internal/fontkit`)

Five committed subsets: Caveat, Dancing Script, Homemade Apple (handwriting),
Inter, Source Serif 4 (official). Latin, Latin-1, Latin Extended-A — which is
where ı İ ş Ş ğ Ğ live — punctuation and currency.

Each is embedded as a Type0 / Identity-H font with a per-document subset and a
`/ToUnicode` map, so a stamped value is selectable, copyable text wherever the
file is opened. A letter a face does not have (Homemade Apple has no Ş, ğ or
İ) is borrowed from the nearest face that does, run by run, so one line can
read as one line.

### Every other script: Noto, fetched when it is used

⚠⚠ The owner (2026-09-21): a name in Japanese, a box filled in Arabic, an
audit line in Hindi must print — "yazı tipini online çekemez miyiz? online
çekebilirsek 0 MB". The five faces above stay the only ones in the module;
everything else is a **Noto** face, downloaded by filex the first time a
text needs it (host function `asset_fetch`, permission
`http:fonts.gstatic.com`), checked against a pinned sha256 before this app
sees a byte, and kept in the app's cache on the server. Latin and Turkish
never touch the network.

**The table.** `internal/fontkit/noto_table.go` is generated by
`go run scripts/notogen/main.go` and never edited by hand: 161 faces for 168
script keys — every script in Go's Unicode tables that Noto draws, plus
`Han/ja`, `Han/ko`, `Han/zh-Hant` (kanji beside kana are Japanese, beside
Hangul Korean) and `Common` (Noto Sans, Symbols, Symbols 2, Math). Nine
scripts have no Noto face at all (Beria Erfe, Garay, Gurung Khema, Kirat Rai,
Ol Onal, Sidetic, Tai Yo, Tolong Siki, Tulu Tigalari — all Unicode 16/17);
they are declared, and a character of theirs is said on screen as "no font
this app can use draws the … script".

**The source, and why.** `fonts.gstatic.com`, Google Fonts' static
**Regular (400)** TrueType instances, found through the CSS2 API and pinned
by URL, sha256 and size. Considered and not taken: the `notofonts` /
`noto-cjk` GitHub releases — the CJK faces there are variable fonts whose
DEFAULT instance is Thin (wght 100), so a name would print hairline, and
every face on one host means one permission on the install review instead of
several. The files are versioned in their path (`/s/notosansjp/v56/…`), so a
pin stays valid; if Google ever serves different bytes under one, the hash
refuses them (`integrity`) and the text is reported as unprintable — never
drawn from bytes nobody checked. `FILEX_SIGN_VERIFY_ALL_FONTS=1 go test
./internal/fontkit -run TestTable_EveryPin` downloads all 161 (≈ 62 MiB),
checks every hash and that every script draws a letter of its own through
the path a stamp takes; run it before a release and after regenerating.

**Only what is needed.** A face is fetched when a line needs a character the
bundled faces lack; the PDF embeds a SUBSET of it (the glyphs the document
uses — a glyf-table subsetter with composite closure, `pdfdoc/subset.go`), so
a signed contract with one Arabic name carries tens of kilobytes, not the
192 KB face. In filex every call is a fresh module, so a cached face is
re-read and parsed per call: ~0.3 s for the 5.3 MB Japanese face, measured.

### Laying a line out (`layout.go`, `bidi.go`)

1. **Direction** — the Unicode bidirectional algorithm, written here
   (`bidi.go`): rules W1–W7, N1–N2, I1–I2, L1–L2 over the UCD's classes.
   ⚠ Not from a library, on measurement: go-text's bidi put "42” — filled
   by" at level 2 inside a left-to-right paragraph, and x/text's `Ordering`
   printed "abc محمد 12 def" in logical order. **Declared, not done:**
   explicit embeddings and isolates (LRE…PDF, LRI…PDI — control characters
   nobody types into a signature box; ignored) and bracket pairing (N0 —
   only which side a bracket sits on when it encloses text of the other
   direction).
2. **Script** — per character; digits and punctuation (Common) and marks
   (Inherited) take the script around them.
3. **Face** — the chosen bundled face, its bundled fallbacks, then the Noto
   face for the resolved script, for the character's own script, and the
   Common chain.
4. **Shaping** — HarfBuzz (go-text/typesetting's port) for every Noto face
   except the very large CJK ones: Arabic, Syriac, N'Ko, Mongolian letters
   JOIN; Devanagari, Bengali, Tamil and the other Indic scripts form
   conjuncts and reorder their vowel signs; Thai, Khmer and Myanmar marks sit
   on their bases; Hebrew points too. Japanese, Chinese, Korean, Tangut and
   Khitan faces are mapped character by character — ideographs, kana and
   Hangul syllables need no shaping in a horizontal line, and HarfBuzz would
   parse the whole 10 MB face into memory (+38 MB for Noto Sans SC, measured).
   **Declared:** a Hangul syllable typed as separate jamo is not composed —
   type it as syllables, as every keyboard does.
5. **Order** — runs in visual order (L2); a right-to-left paragraph is set
   against the RIGHT edge of its box unless the box says otherwise.

In the PDF a shaped run is one `TJ` with a correction after every glyph whose
contextual advance differs from its recorded width. A cluster of several glyphs (a Devanagari
syllable whose vowel sign is drawn before its consonants) carries its
characters as marked-content `/ActualText`, because the per-glyph ToUnicode
map cannot say that [ि, त्र] reads "त्रि" — without it pdftotext gave back
"क्षत्रि य". Per cluster, not per line: a whole line as ActualText came back
reversed from poppler for Arabic and Hebrew. A mark the shaper offsets
stays in the same text flow (corrections around it, `Ts` for its height).
The raster stamp (the picture in a signature widget) goes through the same
layout, drawn with `x/image/vector`.

**Measured, script by script** (rendered with poppler, 2026-09-21): joined
and right to left — Arabic, Persian (with its digits), Urdu, Syriac, N'Ko,
Thaana, Hebrew with points; conjuncts, reordered vowel signs and stacked
marks — Devanagari, Bengali, Gujarati, Gurmukhi, Oriya, Tamil, Telugu,
Kannada, Malayalam, Sinhala, Myanmar, Tibetan, Thai, Lao, Khmer; plus
Ethiopic, Canadian Syllabics, polytonic Greek, Vietnamese, Chinese, Japanese,
Korean. **Declared, not done:**

- Mongolian is set horizontally, as a browser sets it in a horizontal line —
  not top to bottom.
- Urdu is drawn in Naskh (Noto Sans Arabic); there is no Nastaliq face.
- Copying text back is exact for the left-to-right scripts above and for
  Arabic, Persian, Urdu, Syriac and unpointed Hebrew. In Thaana, pointed
  Hebrew, N'Ko, Oriya, Tibetan and Lao, poppler's `pdftotext` puts spaces
  inside words (and, right to left, turns a letter's marks around): every
  character is in the file — ToUnicode and ActualText — the reader's word
  guessing splits them. What is PRINTED is exact in all of them.

### Saying it before the paper does

Nothing is dropped silently. `fontkit.Printed(face, s)` is a text as the
paper will carry it plus what was left out and why — `no_font` (no Noto face
for the script), `unavailable` (the face could not be fetched: no internet,
the grant withheld, the hash refused), `downloading` (filex is still fetching
it; the download outlives the screen's call), `no_glyph`. The screens use it
while the person types — the host asks the app again at every pause (the
`change` event) — and say it in a danger-toned line under what was typed:

- the **fill** step (inside signer, outside signer, signing yourself): every
  text box, in the face the box stamps with;
- the **signers** step of a request: every name, as printed under the
  signature and in the audit trail;
- the **boxes** step: every box name, which only the audit trail prints (and
  replaces by the box's kind when it cannot print it whole);
- the **approve** steps: the document shows every value AS PRINTED, the list
  says "… — printed as “…”", and the lines under a signature are the printed
  ones — what is approved is what is stamped.

Measured on a filex with no internet (a network namespace): the line appears
~1 s after the first keystroke, each answer ~0.1 s, nothing crashes; the app
log says each unreachable face once. With the network: the first Japanese
text costs 0.9–1.4 s (download and verification), every later one ~0.3 s.

---

## 7. Writing values into the document (`internal/pdfdoc`)

### The form path (schema 3)

`PrepareForm` creates every typed box as a real **AcroForm field**: a
`/Widget` annotation with `/FT /Tx` or `/FT /Btn`, the box's identity as `/T` (§5), its
name as `/TU`, listed in the page's `/Annots` and in the catalogue's
`/AcroForm /Fields`. It runs once, idempotently, before anything of ours is
signed.

`FillForm` then writes **only** the field object: `/V`, an `/AP /N` appearance
stream of its own, and the `/Ff` read-only bit so a later signer cannot
quietly change what an earlier one signed over. The page dictionary, its
content stream and its resources are never touched.

That is the whole reason this path exists. Before it, each signer's values
were drawn onto the page, so the second signer's fill rewrote page content
underneath the first signer's signature — and a strict reader was right to
call that *the document changed after it was signed*. digitorus spells the
rule out in `verify.checkIncrementalUpdateScope`; Adobe reaches the same
verdict its own way.

One deliberate omission: **no `/NeedAppearances`.** Handing the drawing back
to the reader means a Turkish name renders in whatever it guesses. The
appearance is written here, with the embedded face.

The SIGNATURE fields are created here too, in the same update, before
anything is signed: every signer's (their first signature box, or an invisible
field for a signer with none) and the seal's (invisible, locked). That is what
makes certification possible — §7a.

A rotated page is handled by the appearance's `/Matrix`: the content is drawn
in BBox space and the form's matrix turns it, so the reader fits it to `/Rect`
and the value reads upright.

## 7a. Certification, the seal and the hash

⚠⚠ The owner, 2026-09-22: "after a document is fully signed and someone
changes it, does the signature say so — or do we lock the file? **Both**, plus
a seal." In the order it happens:

1. **The first signature certifies** the document (`pdfdoc.PrepareSignature`
   with `Certify: 2`): its `/Reference` carries a DocMDP transform with
   `/P 2`, and the catalogue's `/Perms /DocMDP` points at it — without that
   entry a reader does not treat the document as certified at all. P=2 means
   *filling in the form and signing are permitted, nothing else*. A document
   that already carried somebody else's signature is not certified (a
   certification must be the first signature); neither is a request opened by
   an older build.
2. **Every later signer only fills and signs**: their values are field values
   (`FillForm`), and their signature goes into THEIR signature field, created
   in step 0 — the one shape every reader accepts under P=2. (digitorus/pdfsign
   could not do this: it writes the /Reference without /Perms, refuses a
   visible certification, and creates a new field and widget — and rewrites
   the form dictionary — for every signature. Signing is ours now,
   `pdfdoc/sign.go`; the CMS is still digitorus/pkcs7.) A box that belongs to
   *anyone* and was already signed gets a new field beside it, which P=2 also
   permits.
3. **filex seals.** The moment the last signature lands, in the same job,
   `sealDocument` signs the whole document with the installation's **seal**
   (host: `cert_issue {purpose: "platform"}` — a certificate *filex document
   seal* from the tenant's authority, whose key the host keeps) into the seal
   field, whose `/Lock` is `/Action /All /P 1`; the signature echoes it as a
   FieldMDP reference with `/P 1`, the way Acrobat's *lock document after
   signing* writes it. **The seal, not the last signer, carries the lock:** a
   lock on the last signer's signature would make the seal itself a forbidden
   change. After the seal, a reader reports ANY change as *not permitted*.
4. **The hash is of the bytes written.** `sealDocument` hashes what it
   returns — after the seal — and those exact bytes are the output, the
   delivered copy and the SHA-256 in every notice. The envelope keeps it
   (`Sealed{SHA256, SealFP, At, Output}`), and the request's document carries
   the state key `sealed` = `"<sha256> <signed file name>"`, which is how
   Verify recognises a copy of the signed file wherever it lives.
5. **Everybody is told**: the requester and the inside signers in filex, the
   outside signers by mail — with the delivery link when the request sends the
   document (otherwise the mail says the requester will hand it over) — the
   SHA-256, the seal's fingerprint, and how to check (Verify, or `sha256sum` /
   `Get-FileHash`). In the request's language.
6. **Lock the signed file** is the request's option (*While it is open*): the
   signed file stays under filex's lock with no end (`file_lock` with
   `LockUntilLifted`; for a file beside the original, a lock promised on the
   job's own output, taken when it is written). Only an administrator lifts it,
   and that is audited. Without it the file is an ordinary file — the
   certification, the seal and the hash still show any change.

Signing your own document ends the same way: your signature certifies (when
the document carried none), filex seals, and the job's answer carries the
SHA-256.

**Measured against Adobe's semantics.** pyHanko's difference analysis — the
one modelled on Acrobat's — on a two-signer request: the certification reads
`FILL_FORMS`, the second signer's changes `FORM_FILLING`, `docmdp_ok`; the
seal `NO_CHANGES`, covering the whole file. A form value changed after the
seal: the certification still `docmdp_ok` (it is form filling), the seal
**not** (its lock covers every field). A page redrawn after the seal: all
three **not** permitted. Our own verifier (`internal/verify/mdp.go`) gives
the same verdicts, and the tests hold it there.

**Measured in a browser** (filex `e2e/tests/101-app-plugin-sign-seal.spec.ts`,
2026-09-22): a request to an inside and an outside signer, *lock the signed
file* ticked, signed by both on their own screens. The outside signer's mail
(captured by an SMTP sink) carries the SHA-256, the seal's fingerprint and
how to check; `sha256sum` of the file the delivery link hands out is that
hash, and so is the file in filex; the requester's and the inside signer's
notices carry it too; a rename of the signed file is refused (423, held by
the app); Verify says every signature is valid, certified, sealed, and that
this is the file whose hash was sent. A copy with page 1's content stream
rewritten in a later update — the page dictionary untouched, the way PDF
tools usually do it — is reported as *not permitted by the certification:
page 1 draws something different* and the same for the seal, while every
signature is intact. pyHanko on those exact bytes: delivered —
`FILL_FORMS` / `FORM_FILLING` / seal `NO_CHANGES` over the entire file, all
`docmdp_ok`; edited — all three intact, `modification=OTHER`,
`docmdp_ok=False`.

⚠ That run found three things, all fixed and covered by
`TestVerify_APageRewrittenAfterTheSealIsNamedAndTheSignaturesStayIntact`:
a rewritten content STREAM was named "object 5 was changed" (the analysis
now knows the objects a page draws through — its content streams, resources,
fonts and XObjects); the certification was shown as "the signature does not
match the bytes it covers" with no certificate, because digitorus/pdfsign's
own DocMDP check returns before it checks the signature at all (such a
signature is now verified against its own revision — the file as it was when
it was made — and the permission verdict is ours); and the seal typed
`/Name (filex)` while its certificate says *filex document seal*, so Verify
warned about our own seal.

### The old path

`Stamp` still exists, and is used only for a request opened by an older build
— whose boxes were never created as fields, so there is nothing to fill. It
wraps the page's own content in `q … Q` (an unbalanced graphics state in the
document cannot leak into the stamp) and appends a new content stream.

---

## 8. Geometry (`internal/geometry`)

The editor speaks **fractions of the rendered viewport**, origin top-left,
CropBox and `/Rotate` already applied by pdf.js. The plugin converts to PDF
user space of the unrotated page, origin bottom-left, for `/Rotate`
0/90/180/270 and any CropBox offset. `Clamp` keeps a box inside the page and
above a minimum size, so nothing a screen sends can produce a zero-area
widget.

---

## 9. The visible signature (`internal/pdfsig/appearance.go`)

The signature widget's appearance is one composed PNG: the signer's drawing
(or their typed name in the face the requester chose) and, underneath, the
**lines this box chose** — by default the name and the signing date, which is
what every signature carried before the choice existed. It is
**rasterised**, so unlike a stamped field value it is not selectable text — a
caveat worth knowing, and the reason the same facts are repeated in the
verification report and the receipt.

**The lines** (`internal/stamp`) are chosen per signature box in the define
step, from what the signing record really holds: the signer's name, e-mail
address, date and time, IP address, the certificate's fingerprint (first four
groups) and serial, and the signing authority. ⚠ Every value comes from the
record at the instant of signing — nothing the requester typed reaches the
paper; the requester chooses *which* facts. A fact the record does not hold (a
signer with no address) is left out rather than printed as a dash. The
certificate is therefore issued **before** the picture is composed.

⚠⚠ **The signer sees exactly what will be printed before pressing Sign**
(the owner: an IP address is personal data). The approve step shows each of
their signature boxes with the very picture the job will print — drawn by
the same `pdfsig.Compose`, at half the resolution — and lists the lines in
words, in the request's language; the time and the certificate are marked as
the ones of the moment Sign is pressed. The IP printed is the address of the
screen they approved on: the visitor's on a signing link, and on a signed-in
screen the one filex hands the app as `context.actor.ip`.

A signer's **other** signature boxes carry their own drawing and their own
lines too, as the appearance of a form field (`pdfdoc.KindImage`) — never page
content, so an earlier signature stays clean. They used to show only the name
in a handwriting face, while the pad had asked for a drawing in each.

Each drawing is normalised before it rides in a job parameter or a surface's
state — to ≤ 40 KB, or a share of 48 KB when a signer has several, because
one job carries all of them and filex caps a job's parameters at 64 KiB (two
boxes at 40 KB apiece were refused with 413).

---

## 10. The flows (`internal/app`)

Both sides use the same shell: a step strip, **one question per step**, at
most one primary button plus Back. There is no dropdown anywhere (a `select`
is drawn as a row of choice buttons) and no folded "advanced" section — a
field is on the step or it is not in the manifest.

### `sign` — view `sign-self`, placement **page**

1. **Place the boxes** — the `pdf-fields` editor alone, filling the viewport.
2. **Sign** — a form of the boxes' names, the pad inline per signature box.
3. **Where it goes** — version / new file, and the file name **only** when it
   is a new file (`show_when` + `required_when`), plus an optional reason.

### `request` — view `request`, placement **page**

Eight steps: **Signers → Order → The boxes → Place them → Time → While it is
open → When it is done → Review**. The order step is skipped when there is one
signer. The identity box takes one signer per line: a name, an address, or
`Ali Yılmaz <ali@ornek.com>`. The boxes are NAMED on one step (what is wanted,
of whom — no document on screen) and PLACED on the next (the document and
nothing else).

⚠⚠ **Asking is a permission (0.2.0, filex ≥ 0.49.0).** The manifest's one
`user_permissions` entry, `request` (filex: `app.sign.request`, default
`user`), is required by the `request` action and the `request` view — and by
nothing else. filex drops the menu row for an account without it and answers
403 to the run, the view's open and every event of it; the app itself never
asks. See §19 for why `apply`, `status` and `envelopes` stay open.

The hints that POINT at it are the app's own, so they follow what filex tells
it: from filex 0.49.0 every job, view event and interface call carries the
ids of this app's permissions the reader holds (`actor.permissions`,
`wire.Actor.Can`). "…or Request signatures… to ask others" on the Signatures
panel (no request yet), on Verify (an unsigned document) and in the
Signatures screen's "How this works", and "Start one with Request
signatures…" in the answer to an `apply` on a document with no request, are
said only to a reader who holds `request`; everybody else reads the sentence
without it. No actor (an older filex does not say) reads as "does not hold":
a hint left out is a smaller wrong than a door offered and then shut
(`TestHints_RequestSignaturesIsOfferedOnlyToWhoMayAsk`).

⚠ A document on a storage that takes no writes (`FileRef.read_only`) never
reaches step one: the first screen says the signed document could never be
saved there, and the `request` job refuses the same way before it writes a
record, freezes the file, opens a link or tells anybody. The request itself
writes nothing — the write is the signed version when the last signer
answers — so filex's own read-only refusal, which guards writing jobs, could
not catch it. `sign-self` refuses the same way (its signed copy goes beside
the original).

⚠⚠ **The links' life comes from the host.** filex clamps every share link to
the lowest of what was asked, the page's own `max_ttl_days` (90 here) and the
installation's share ceiling (`share.max_ttl_days`, Admin → Protection, 7
days unless changed) — silently. So the Time step reads the ceiling from the
call (`CallContext.ShareMaxTTLDays`), offers no more than it, and says why
when the ceiling is the installation's; the review says the life the links
will really get; the request job reads the ceiling again from
`ActionRunInput.ShareMaxTTLDays` and asks for no more, so the record, the
freeze and the mails agree with the host. (Before, the step offered 14, the
review said "links valid 14 days", and the links lived 7.) A sign-by day
counts to its END: the link is cut to whole days rounded UP to that day's
end, and the request closes as the day ends (§12a).

**The yes/no defaults are what a person gets.** "Let a signer refuse" and
"Write an audit trail PDF" start ON: one constant (`views.DefaultAllowDecline`,
`views.DefaultAudit`) is both the field's declared default and the value a
new request starts from. (Before, the form declared them ON while the fresh
state held `false`, and a form's value wins over its default.)

**One request at a time.** A document carries one record, one `pending`
mark, one freeze. While a request is open the wizard answers on its FIRST
screen — who asked whom, how far it got — with one button, to the document's
`status` panel (`Surface.Open`), and it answers the same at Send for a request
somebody opened meanwhile. (Before, the host's `no_state: pending` gate was
the only refusal: eight steps, then "Invalid data".) Once a request has ended
a new one is legitimate; its first step says it replaces the old record and,
when the old one holds an audit trail never written as a file, to save it
first.

The job then opens one **signing share** per outside signer — including the
ones with no address, because they need a link and a PIN too — stores the
envelope, invites whoever is due, and tells the requester the PINs and the
hand-over links.

### `fill` — view `sign-fill`, placement **page**

Three steps: **What is asked of me → Fill it in → See and approve**. The first
step lists every box with its name, kind and page (a required one marked `*`);
the refusal button lives there. The second is a plain form with the pad in the
row of the box it belongs to — labelled with the box's name like any field,
with the same `*` when it is required, offering exactly what the requester
chose (a drawing, or a typed name in their face). The third shows the document
with the values seeded and each signature box as it will be printed (§9), and
signs.

A signer who is already done sees their **receipt** instead of a form.

### The signing share (`signer`)

The same three steps, drawn for somebody with no account, from the copy of the
document the share exposes. Its submission asks for the `apply` job, which
filex queues **as the share's creator** with `page_token_hash` added — that
hash is what binds the submission to one signer.

The page declares its `purpose` in the manifest, so the link is listed for
what it is: *Signing request* in the requester's My shares, opening the
Signatures page at *I asked for these*, its Revoke saying first that it
cancels the whole request (§12b is what makes that true). A receipt's link is
*Signed copy* and opens *I have signed these*. Opening the page counts as a
visit; only the Download button's fetch counts as a download.

### `apply` — hidden action, the second half of every flow

`op`: `sign` | `decline` | `remind` | `cancel` | `expire` | `link_ended` |
`audit` | `viewed`.

`scheduled: true` in the params marks a run the hourly wake-up started rather
than a person (§12a); it changes nothing about the work, only what the job
says when it finds the work already done.

`viewed` records that a signer opened the document and tells the requester,
once per signer. It is a JOB because the in-app filling screen is a **view**
and filex refuses `state_set` outside a job — a screen cannot record
anything, so it cannot safely say anything either.

Every op but `expire` and `link_ended` first SWEEPS: a request whose deadline
has passed is closed then and there, the file released, the links revoked and
both sides told, and so is a request one of whose links a person ended on the
Shares screen (§12b). The signer's page sweeps for the deadline too (not for
ended links: inside a page call filex answers `share_state` about the link
being visited, whatever token is named). The sweep is the BELT — the braces
are the hourly wake-up (§12a), which schedules the same ops for the minute
they are due, so nobody has to be there.

A signature runs: read the document → plan (rules checked again) →
`PrepareForm` if needed → `FillForm` → issue → sign → destroy the key → write
→ apply the event → run the effects → keep the certificate → hand over the
receipt → deliver, when this was the last one.

### `verify` — view `verify`, placement **page**

Reads the document and reports (§11). No state gate, `min_role: viewer`.

### `status` (inspector) and `envelopes` (home)

`status` follows one request. `envelopes` is the **Signatures page**: a page
of its own in the app (not a dialog), with a menu of sections, each one table —
*Waiting for my signature · I asked for these · I have signed these · Every
request* (administrators) · *How it works* — each entry counting its rows. The
page keeps the section in its address (`?section=`), so Back walks the
sections and a notification lands on one: the requester's progress notices
open *I asked for these*. Opened without a section it shows the first one
that has rows. *Due* is the last day an open request can be signed on: the
sign-by day when one was given, otherwise the day its last link runs out
(`lapseAt`, the one definition of "over").

filex opens a home view **with no document**, so the screen gets its rows from
`state_list`: this plugin's own state rows, newest first, deleted files never,
and **every row through the asker's own permissions** — the screen can draw the
right list without widening anybody's reach. One call for the `envelope` key
answers every section, because the record itself comes back with the row.

A document somebody signed **by themselves** carries no request at all, so it
is found through the `signed` badge instead, whose value is the user ids of the
signers who have an account here. That is the one fact the file cannot answer.

Two honest edges:

- ⚠ A state row written before the host kept the file's path (migration 00048)
  has no path and is not listed; the next `state_set` on that document fixes it.
- A row **is** a document, so clicking it goes there: the row action answers
  `&wire.Surface{Open: pluginkit.OpenFile(path, action, view)}` and filex takes
  the person to that file, on the screen the section is about — **Sign** opens
  the `fill` action, **Follow** the `status` view, **Verify** the `verify`
  action (`views.Target` is the one table that says which). Exactly one of the
  two is named; naming both is refused. The path only ever comes from a row
  this very call drew, and the host re-checks it: a screen the plugin does not
  own is an error, and a file the asker may not see has its link dropped, so
  offering to go somewhere can never become a way around permissions.

---

## 11. Verification (`internal/verify`)

Deliberately **offline**: no OCSP, no CRL, no clock but the document's own and
the timestamp's. A plugin runs in a sandbox with no network of its own, and a
verification that silently depends on reaching a responder is a verification
that fails on a train.

`digitorus/pdfsign/verify` does the cryptography, with:

| option | value | why |
|---|---|---|
| `TrustedRoots` | this tenant's whole CA bundle | a signature is trusted because this installation stands behind it |
| `AllowUntrustedRoots` | `false` | a certificate is not a root because it is in the file |
| `SkipRevocationCheck` | `true` | offline |
| `EnableExternalRevocationCheck` | `false` | offline |
| `RequiredEKUs` | document signing (1.3.6.1.5.5.7.3.36) | RFC 9336 |
| `TrustSignatureTime` | `true` | without a stamp it is the only time on offer — and the report says it is *declared*, not proven |

Three things are computed here rather than taken from the library, because
they are what a person actually asks:

1. **Does it cover the whole file?** From `/ByteRange`: `br[2]+br[3]` against
   the file's size. The library does not report it.
2. **What did the later updates do?** The revision a signature covered is a
   valid PDF of its own — truncate at the end of its byte range — so the page
   content of *then* and of *now* is simply hashed and compared. Identical
   means the updates since only added objects: filled fields, further
   signatures. Different means a page draws something else now.
3. **Who stands behind it, and what does the certificate attest?** Issuer,
   both fingerprints, validity windows, whether the signing moment fell inside
   the leaf's window, key usage and extended key usage, and whether the chain
   ends at one of this installation's own authorities.

Anything a certificate does not carry is reported as *not stated*. The
identity comes from the **certificate** when the signature dictionary's
`/Name` disagrees with it, and the report says so: `/Name` is typed by
whoever signed, the certificate is what an authority put its name behind.

---

## 12. Time stamping

Two settings, both read through the `settings` permission:

- `tsa_enabled` (bool, default **false**)
- `tsa_url` (string, default `https://freetsa.org/tsr`, shown only when the
  first is on)

The RFC 3161 request is made with Go's own `http.Client` (`pdfsig.timestampToken`),
and a wasm guest has no sockets: `internal/hostnet` swaps Go's
`http.DefaultTransport` for one that hands the request to filex's
`http_request` host function. ⚠ The host is still the boundary: the grant,
the private-address refusal, the size and time limits are all the host's.
Installing the transport widens nothing.

The RFC 3161 request carries the signature's **digest and a nonce** — the
document never leaves the installation. If the authority cannot be reached the
signature is made **without** a stamp and that is reported, in the job message
and in the verification report. Nobody loses a signature because a third party
was down.

**Leaf lifetime is ten years** (`certDays = 3650`). A verifier checks a
certificate against the clock it runs with, so a thirty-day leaf made every
signature read as "the certificate has expired" a month later. The long
horizon carries no key risk: the key is destroyed seconds after the signature
is written.

---

## 12a. The hourly wake-up (`internal/app/tick.go`)

A request that lapses at 03:00 has to close at 03:00. "The next time a human
opens the status screen" is not 03:00.

The `schedule` permission gives this app a sixth export, `tick`, which filex
calls **once an hour** with a window — `now` to the next hour boundary — and
one question: what do you want done, and when? The answer is a list of items,
and filex runs each one at the minute it names, as an ordinary job (ops row,
cancel, limits, audit — the usual machinery), with `actor_id` null: SYSTEM,
because nobody asked for it.

| | |
|---|---|
| What it reads | `state_list("envelope", 200)` — its own records, nothing else |
| What it asks | `share_state` for every link a waiting signer still holds (§12b) |
| What it schedules | the **`apply` action** with `op: expire`, `op: remind` or `op: link_ended` — the first two are the ops the panel's buttons queue |
| Key | `expire:<envelope id>` / `remind:<envelope id>:<signer id>` / `ended:<envelope id>` |
| When | `lapseAt` (the earlier of the deadline's end-of-day and the last open link's expiry) and `remindAt` (the interval after the signer's last movement) |
| Ordering | soonest first, so the far end is what a trim loses |

⚠⚠ **The wake-up decides; the action acts.** `tick` runs with a screen's
scope: it may read settings and state, look people up, notify and mail — it
may **not** write state, create files, take locks, open shares or sign. Those
refusals are answered *in band*, so a tick that writes does not fail loudly,
it quietly does nothing. `host.Fake.TickScope()` refuses every one of them and
**records the attempt**, and `TestTick_WritesNothing` asserts the list is
empty.

⚠ **The key is an idempotency key.** filex keeps at most one item per (app,
key), so naming it again MOVES that item — a deadline pushed back is the same
envelope closing later, not two closures. It is keyed by envelope, never by
path: a path has slashes (which the host refuses) and it changes when somebody
moves the file.

⚠ **Work beyond the window is not named**, and that is not an error: the
wake-up whose window contains it asks again. Work already past is named at its
true time and the host runs it at once — a wake-up that ran late should still
close what has lapsed.

⚠ **Three refusals keep an unattended reminder honest**, because a scheduled
job that cannot succeed is a failure every hour until the request closes: no
reminder to a signer who is finished, none out of turn in a sequential request
(the machine itself would refuse the event), and none to somebody filex has no
address for — `opRemind` answers "no address for filex to write to", which is
an answer to a person and a loop to a scheduler.

⚠ **A job that arrives after the work is done is not a fault.** Somebody can
sign or cancel in the hour between the wake-up and the minute it named. With
`scheduled: true` in its params, `expire` and `remind` answer OK and say
nothing happened, instead of painting a red ops row an administrator has to
investigate. Pressed by a person, the same case still says "the request is
already closed" — their screen was stale, and that is worth telling them.

**The belt stays.** `sweepExpired` still runs on every job and on the signer's
page: the wake-up only sees the first 200 records, is never called on an
installation that did not grant `schedule`, and does not run while the app is
disabled. Both paths perform the same `closeExpired`, so they cannot drift.

---

## 12b. A link ended on the Shares screen (`internal/app/links.go`)

A signing link is a real filex share, so a person can end it outside this
app: the requester under **My shares**, an administrator under **Shares**
(Revoke, or Delete). That stops the link — and, before this, nothing else:
the request stayed "Sent · 0/1 signed", the file frozen, waiting for a
signature that could never come.

⚠⚠ **filex does not tell an app that its link ended, and this app does not
need it to.** The link's facts are the app's to ask for — `share_state` read
answers `expires_at` and whether it still opens (`pluginkit.ShareInfo`) — and
the app already asks the host questions every hour, unattended. So the
wake-up asks after every link a waiting signer holds:

- a link that is **gone** (`not_found` — an administrator's Delete), or
- a link that **stopped earlier than the day filex gave it at share_create**
  (a revoke moves `expires_at` to the moment of the revoke; a minute's slack
  covers the database's precision)

is a link a person ended, and the wake-up names `apply` with
`op: link_ended`, due now, and nothing else for that request. The host only
brings the wake-up forward when a person ends one of an app's links
(`Registry.WakeSoon`), so the answer comes in seconds rather than within the
hour. One mechanism — the wake-up — and one way of learning — asking.

The job asks again (a stale answer must not close a live request) and applies
`link_ended`, which ends the request the way a refusal does: that signer can
no longer sign, so it cannot finish as asked. It closes as **cancelled**, the
event saying whose link and how; the other links are revoked, the file is
released, the requester is told, and so is every other signer still holding
a link. The signer whose link was ended is not written to: it was ended on
purpose, perhaps because it went to the wrong address.

Three things it deliberately does NOT treat as an ended link: a link that ran
out ON its day (that is the lapse, §12a, and closes as expired); the app's own
revoke of a link whose signer has signed; and a link whose facts could not be
read — a request is never closed on a guess. And a link that stops because
its creator's account was switched off is a PAUSE in filex (re-enabling the
account opens it again), so nothing here reads it as an ending.

---

## 13. The receipt (`internal/receipt`)

What a signer is handed **the moment their signature lands** — not when the
request finishes.

Three files:

| file | what |
|---|---|
| `.p7b` | certs-only PKCS#7: the leaf and the authority that issued it. No content, no signer — what Windows, Acrobat and openssl all import. |
| `.pem` | the same two certificates as text. |
| `.txt` | the facts in words: identity, document, when, serial, and both SHA-256 fingerprints, with the steps to check the signature by hand. |

⚠ Every one of them says, in the reader's language, that this is an **identity
receipt, not a signing capability**: it proves who signed and what was signed,
and it cannot sign anything, because the private key was destroyed when the
signature was written. A file called "certificate" invites exactly the wrong
assumption.

How it reaches the signer:

- **an account here** → a notification with both fingerprints and a click that
  opens the **Verify** screen on the document;
- **anybody else** → a share **of its own** (30 days, PIN when the request
  used one) that carries the three files.

⚠ It is never the signing link. The last signature revokes every open signing
link, which would take the receipt away from the person the moment somebody
else signed.

The leaf certificate is kept with the document (`cert:<signer id>`, DER as
base64, about 700 bytes), so the receipt can be built again later — for the
receipt share's screen, and for a signer who re-opens **Sign / Fill** after
they are done.

---

## 14. Delivering the finished document

When the last signature lands and the requester asked for it, the job opens
**one** filex share of the signed document and mails its link to every signer
who has an address. Whether that link carries a PIN is the requester's choice,
in the "When it is done" step; the PIN is shown to them, never mailed.

Anybody with no address is not mailed — the requester was given the link and
hands it over, which is the same rule as everywhere else in this app.

---

## 15. Office documents

An office document has no rendered pages, so nothing can be placed on it. Both
screens offer one thing: convert it to PDF beside the original, which is never
changed. "Sign…" and "Request signatures…" are offered on an office document
only while LibreOffice is installed (`applies.engine_ext` — filex folds the
office extensions into the action's rule only then). LibreOffice runs as a host **engine** with bare-token arguments
(`--convert-to pdf in.docx`) in its own run directory.

Without LibreOffice the screen says so plainly instead of failing a job.

"Convert to PDF" queues the hidden action `convert` (so the operations tray
names the job "Convert to PDF", not "Sign…"); the PDF lands beside the
original and the job's message says to open it and ask for signatures there.
If LibreOffice fails, runs out of time or went away between the screen and the
job, the job says which, in the reader's language; LibreOffice's own words go
to the app's log.

⚠⚠ The button is the screen's primary button, and filex posts a primary
button as `submit` (every other one as `action`), with its id in `action_id`
— in the full page, in the dialog and in an embedded explorer's popup alike.
Every handler here asks for the BUTTON (`pressed(in, id)`), never for the
event that carries it: until 0.1.1 "Convert to PDF", "Open its Signatures
panel" and "Close the expired request" listened for `action` only, and a
click redrew the same screen. `buttons_test.go` presses every footer button
the way filex does.

⚠ A converted document can never become a *new version* of its original — the
job refuses that combination rather than replacing a `.docx` with a PDF under
the same name.

---

## 16. Freezing the file

`files:lock` freezes the document read-only for **everyone**, administrators
included, until the request ends or the TTL passes. The locking plugin's own
jobs still write, which is what lets the signatures land.

Every ending of the envelope emits the unfreeze effect from the same
`finish()`, so no exit of the flow can leave a file locked. The freeze DURING
collection always carries a TTL (capped by the deadline), so nothing stays
frozen for ever even if everything else goes wrong. ⚠ One lock is
deliberately endless: the request's *Lock the signed file* option holds the
FINISHED document with `LockUntilLifted` until an administrator lifts it
(§7a).

---

## 17. Mail and notifications

Three ways to reach a signer, decided by one question — what is in their
identity:

| identity | how |
|---|---|
| an account here | filex's own notification, which opens the signing screen on the document |
| an e-mail address | their private link, by mail |
| a name and nothing | nothing is sent; the link and the PIN are shown to the requester |

⚠ **A PIN is never in a mail** — not a signing link's, not a receipt share's,
not the delivery link's. Every one of them goes to the requester, through the
job message and the bell.

The requester hears about: sending (with the PINs, the hand-over links and
the day the links stop), first view, each signature, completion, refusal,
cancellation, expiry, and a link ended on the Shares screen.

---

## 18. The audit trail

A separate PDF beside the document — never appended to the signed file, which
would break the signatures it is meant to explain. It carries: the request and
its options, every signer with their identity, status, timestamps, IP and
certificate, the values that were filled in, the whole event log, and the
paragraph about what a signature from this installation is worth.

It is written when the request completes **and** the signed file went
somewhere else; with a `version` output a job commits one kind of output, so
the panel offers it as a click instead.

It is written in the **requester's** language (`Options.Locale`, recorded by
their request job), whoever's job writes it — usually the last signer's, who
may be an outsider on an English screen. Every letter it prints is in the
embedded Inter subset; `TestAudit_EveryLetterIsInTheEmbeddedFace` renders the
Turkish trail and checks it rune by rune.

---

## 19. Decisions worth knowing

- **Asking is a permission; signing is not** (Burak, 2026-09-28). Only the
  two doors that START a request — the `request` menu row and its wizard —
  carry `requires: "request"`. `apply` does not: it is hidden and queued by
  both sides, the signer's Sign / Fill screen (a signature, a refusal) and
  the Signatures panel (Remind, Cancel request, Close the expired request,
  Save the audit trail), so gating it would stop a signer from signing and a
  requester who lost the permission from cancelling the request that keeps a
  file frozen. `status` and `envelopes` do not either: they are where a signer
  finds what waits for them. Taking the permission away stops the next
  request, not the open ones. `TestManifest_AskingIsAPermissionSigningIsNot`
  holds this, including "an action requires what its view requires".
- **The request lives with the document.** No cross-document index, because
  filex gives a plugin none. A deleted file takes its request with it.
- **`Apply` is pure.** Every transition is a table test; every effect is
  performed by the caller. That is what makes "the freeze is always lifted"
  provable rather than hoped for.
- **Rules are checked twice**, on the screen and in the job.
- **The certificate wins over `/Name`.** One is typed, the other is vouched
  for.
- **Reminders are sent only where the requester asked for them.** The default
  is none; "remind every N days" is a request for exactly that, so the hourly
  wake-up keeps it. Nothing is invented: no reminder to a signer who has
  acted, none to somebody filex has no address for, none out of turn in a
  sequential request, and none at all once the request is closed.
- **The screens are built in every declared language, always** — en, tr,
  es, de, fr. `wire.Text` carries all five; a form field's plain-string label
  is picked from the call's locale. English and Turkish are written in the
  code; the other three come from `internal/i18n/catalogue/*.json`, keyed by
  the English, and `internal/i18n/catalogue_test.go` scans the source so no
  text can go around the catalogue (see the README's *Languages*).
  Those three are AI-translated and awaiting a native speaker's review —
  and they carry legal wording, so a correction there is a correction to what
  the app says on paper.
- **Time stamping is off by default.** It is a network call in the middle of
  signing, and with a ten-year leaf the expiry problem it solves does not
  arise on a self-hosted installation. Whoever wants it switches it on.

---

## 20. What filex could add to make this app better

- ~~**A way to open a file from a surface.**~~ Shipped: `Surface.Open` takes a
  path and one of this plugin's screens, and the Signatures screen's rows use
  it (§10). Together with `state_list` that screen is now a list somebody can
  work from, not a signpost.
- ~~**A timer, or a "run me later" job.**~~ Shipped: the `schedule` permission
  and the `tick` export (§12a). Expiry and reminders are no longer something a
  person has to click.
- ~~**Which of its own permissions the reader holds.**~~ Shipped in filex
  0.49.0: a job, a view event and an interface call carry
  `actor.permissions`, the ids of this app's `user_permissions` the reader
  holds, decided by the same question filex asks at the door. The hints on
  the Signatures panel, the Verify screen and the Signatures screen ("…or
  Request signatures… to ask others") are now drawn only for a reader who
  holds `request` (§10).
- **An output mode on a scheduled item.** A surface's `job` may override the
  action's output mode; a schedule item may not, so unattended work has to
  ride an action whose manifest mode it can live with.
- **A DSS / LTV hook.** Embedding revocation material would let these
  signatures survive the authority's own lifetime.
- **An attachment on `mail_send`.** A receipt could then arrive as a file
  instead of a link.
- **A module that stays warm between calls.** Every call is a fresh
  instance, so a fetched font is read from the cache and parsed again in each
  one — about 0.3 s for the 5.3 MB Japanese face on a screen that asks at
  every pause in typing. An instance kept for a few seconds would keep the
  parsed face.
- **A screen that can start a quiet job.** The in-app signing screen is a
  view, and a view may not write state, so it cannot record that an INSIDE
  signer opened the document (the signing link's page can: a page call may
  write). The app no longer tries — the refused write logged a warning on
  every open. A surface `job` that runs in the background while the screen
  stays would let the `apply` job's `viewed` op record it.

---

## 21. Tests

Everything runs on the host — no wasm runtime needed — over the `host.Host`
seam, with `host.Fake` answering from memory with a throw-away CA.

```bash
go test ./...
bash scripts/build.sh     # gofmt + vet + tests, THEN the module
FILEX_SIGN_REQUIRE_FONTS=1 go test ./...                                     # the non-Latin tests may not skip
FILEX_SIGN_VERIFY_ALL_FONTS=1 go test ./internal/fontkit -run TestTable_EveryPin  # every pin, every script (≈ 62 MiB once)
```

The tests that draw real Arabic, Devanagari or Japanese take the pinned Noto
files through `internal/fontkit/notofixture` (a cache in the temporary
directory, filled once through the same pin) and SKIP without a network
unless `FILEX_SIGN_REQUIRE_FONTS` is set. The screens' "will not print" lines
are tested against `host.Fake`'s `Network`, `Offline` and `Pending`.

The **renderer rules and the language rules belong to the SDK**, not to
this repository: `pluginkit/plugintest` ships them so every plugin is
measured the same way, and a rule that lives in one plugin's test file is a
rule the next plugin gets wrong. This app calls them and adds only what is
particular to it:

| from the kit | what it refuses |
|---|---|
| `CheckSurface` | a node type filex has no component for, a `select` that is not a readable choice, more than one primary button, a condition pointing at a field that is not there, duplicate ids or field keys |
| `CheckLanguages` | a `Text` on any screen missing a declared language |
| `CheckLocaleParityOpts` (also over en/tr/es/de/fr) | the request wizard drawn in all five languages not being the same wizard |
| `CheckLocaleParityOpts` | the same screen drawn twice not being the same screen, and a string that did not change between them (strict here, with an allowlist for the data this app does not translate: date layouts, identities, file names, what a requester typed) |
| `CheckManifest`, `CheckManifestLanguages`, `CheckRegistered` | what the host itself refuses at install |

Beyond that, the suite enforces the rules that are this app's own:

- **language**: every `wire.Text` any screen can draw carries every declared
  language, in every locale; every form label, help and option **changes**
  with the language; no ASCII-ised Turkish anywhere; every text in the code
  is a literal English–Turkish pair the catalogues hold in es, de and fr, with
  the same format verbs (`internal/i18n/catalogue_test.go`); a whole request
  in each of es, de and fr — mails, notices, audit trail, Verify — speaks that
  language (`TestEveryLanguage_*`); and every letter those languages print,
  ß é ñ œ « » and the no-break spaces included, is in the face that draws it.
- **who may ask**: exactly one user permission (`request`, default `user`,
  labelled and described in every language); only the `request` action and
  view require it; an action requires what its view requires; the manifest
  says `filex: ">=0.49.0"` (`TestManifest_AskingIsAPermissionSigningIsNot`).
- **surfaces**: at most one primary button per step; a `select` stays small
  enough to draw as buttons; `show_when` / `required_when` name a field of the
  same form; the file name is asked only when the output is a new file.
- **the form path**: filling a field, twice, leaves every page's content
  digest untouched; a filled field is read-only; `/NeedAppearances` is not
  set; `PrepareForm` is idempotent.
- **end to end**: the eight-step wizard → shares and invitations → two
  signatures through two different doors → the receipt each signer gets → the
  delivery share → verification, where the **first** signature must read as
  "form fields were filled in and signatures added; no page draws anything
  different";
- a **job** speaks the caller's language too: the progress line in the tray,
  the subject of the page a stranger opens, the reason printed inside the
  signature, and the reason a refused rename gives.
