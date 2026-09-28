# filex-sign — e-Signature for filex

**filex-sign** is an [app plugin](https://github.com/BRF-Tech/filex/blob/main/docs/APP-PLUGINS.md)
for [filex](https://github.com/BRF-Tech/filex) that signs documents and asks
other people to sign them — inside filex, with no external service. It is the
reference app for filex's plugin platform: a WebAssembly module plus a
manifest, installed from this repository.

Both sides of a signature are the **same screen**, step by step: asking for a
signature and giving one must not look like two products.

- **Sign…** a document yourself — place the boxes, fill them in, say where the
  result goes. Three steps, one question each.
- **Request signatures…** from one or more people — who signs, in what order,
  which boxes each of them fills, where those go, how long they have, how the
  request behaves, what happens when it is done, review, send.
- **Sign / Fill** — what is asked of me → a plain form of **named** boxes →
  the document as it will be → sign.
- **Verify** — on **any** PDF, signed here or anywhere else: who signed, which
  authority stands behind them, what the certificate attests, whether the
  signature covers the whole file, and what the later updates did to it.
- **Every box has a name.** Whoever places a box names it ("Görev unvanı",
  "Tarih"); the signer fills a form of those names instead of hunting across a
  page. The name is in the box, in the form, in the audit trail and in the
  PDF's own field.
- **A signer is an identity, not an address.** A name, an e-mail address, or
  both. Somebody with no address still gets a link and a PIN — shown to the
  requester, who hands them over.
- **A receipt for every signer**, the moment their signature lands: their
  certificate as a `.p7b` bundle, a `.pem` and a plain-text summary with both
  SHA-256 fingerprints. It is an *identity receipt, not a signing capability*:
  the private key was destroyed when the signature was written.
- **The finished document goes out** to every signer with an address as a
  filex share link, with or without a PIN — the requester chooses.
- **A Signatures screen that lists.** Apps → Signatures: what is waiting for
  your signature, what you asked others for, what you have signed, and — for
  an administrator — every request in the installation. Each row names its
  document and **takes you to it**, on the screen its section is about: Sign,
  Follow or Verify.
- **Five languages, everywhere.** The manifest declares `en`, `tr`, `es`,
  `de` and `fr`; every screen, mail, notice, receipt, audit trail and line
  printed under a signature speaks each of them, in the terms filex's own
  language packs use (usted / Sie / vous). The test suite refuses a build
  where any text the app shows is missing in one — see *Languages* below.
  The audit trail is written in the requester's language, whoever's
  signature happens to complete the request.
  Spanish, German and French are **AI-translated and awaiting review by a
  native speaker** — corrections welcome. What they carry is legal wording
  (the sentence printed under a signature, what a signer consents to, the
  audit trail), so a phrase that reads wrong in your language is worth an
  issue.
- **Office documents** (ODT, DOCX, XLSX, PPTX…) are converted to PDF by the
  installation's LibreOffice; without it the app says so instead of failing.
- **The file can be frozen** while signatures are collected: read-only for
  everyone, administrators included, until the request ends.
- **Who may ask is the administrator's call.** Asking others to sign is an
  app permission, **Request signatures** (`app.sign.request`), that the
  administrator grants or refuses per role and per person. Signing, filling
  in, Verify and following a request already sent need none — see
  [Who may request signatures](#who-may-request-signatures).
- Signatures are **PAdES-B** (`ETSI.CAdES.detached`, ECDSA P-256 / SHA-256),
  each with a fresh certificate from **this installation's own signing
  authority**; the private key never leaves the server and is destroyed after
  one use. Time stamping (RFC 3161) is available and **off by default**.

> Status: **v0.2.0**. Step-by-step screens on both sides, named boxes,
> identities without an e-mail address, receipts, delivery of the finished
> document, verification on any PDF, real AcroForm fields so a second
> signature does not disturb the first, optional time stamping, and asking
> for signatures behind a permission the administrator hands out. See
> [Limits](#limits).

## Install

**Needs filex v0.49.0 or later** (the first filex that knows an app's own
permissions; an older one refuses the manifest). On filex v0.43.0–v0.48.x,
install `v0.1.1`.

Admin → Plugins → Apps → Install → **GitHub repository** →
`BRF-Tech/filex-sign` at a release tag (for example `v0.2.0`). filex fetches
`filex-app.json` from the tag, downloads `plugin.wasm` from the release and
checks its sha256. Review the permissions and approve:

| Permission | Why |
|---|---|
| `files:read`, `files:write` | read the document; write the signed one, the audit trail and each signer's receipt |
| `files:lock` | freeze the file while its signatures are collected (only if you switch it on) |
| `sign` | have the signing authority issue a certificate per signer and sign the digest |
| `state` | keep the request with the document, keep each signer's certificate so the receipt can be given again, mark the documents that wait, and list those marks on the Signatures screen |
| `settings` | read the two settings you control: time stamping on/off, and by which authority |
| `public_pages` | open a filex share per outside signer (link, then receipt) and one for the finished document, and notice when one of those links is revoked or deleted on the Shares screen |
| `mail:send` | e-mail a link, a receipt and the finished document (never a PIN) |
| `notify:send` | ask a colleague to sign, hand them their receipt, tell the requester what happens |
| `users:lookup` | pick signers from the installation's users |
| `engines:libreoffice` | turn an office document into the PDF a signature can live in |
| `http:freetsa.org` | only when you switch time stamping on: ask a time-stamping authority to date a signature |
| `http:fonts.gstatic.com` | download one Noto face the first time a name or a filled box needs a script this app does not carry (Arabic, Hebrew, Devanagari, CJK…). Every file is pinned by its sha256; Latin and Turkish never touch the network |
| `schedule` | wake the app once an hour (and at once when one of its links is ended) so a request closes the minute it runs out or its link is ended, and the reminders you asked for go out on time, with nobody present |

Signing needs `FILEX_SECRET_KEY` on the server (the authority and every key
are sealed with it). The home screen (**Apps → Signatures**) says when signing
is not available and why, names the signing authority with its SHA-256
fingerprint, and says whether this installation can convert office documents.

### Who may request signatures

The table above is what the **app** may do. What each **person** may do with
it adds one permission of the app's own, which filex lists under
*e-Signature* in its role editor and in a person's exceptions:

| Permission | Key | Until the administrator decides |
|---|---|---|
| **Request signatures** | `app.sign.request` | accounts that can change files (`user`); not viewers |

It covers the two doors that **start** a request: the **Request signatures…**
menu row and its eight-step screen. Without it the row is not in the menu, and
running it anyway — the action, the screen, any of the screen's steps — is
refused with 403. Administrators always hold it; for everybody else the
person's own exception decides first, then their custom role, then the
built-in role's decision, and only then the default above.

The app's own screens follow the same answer: filex tells it which of its
permissions the reader holds, so the document's **Signatures** panel, the
**Verify** screen and the **Signatures** screen suggest **Request
signatures…** only to somebody who holds it. Everybody else is pointed at
**Sign…** alone.

Nothing else is behind it, on purpose:

- **Signing** — **Sign…** on your own document, **Sign / Fill** when somebody
  asked you, an outside signer's link. Being asked to sign is not something
  the person asked can be refused.
- **Verify**, on any PDF.
- **Following a request already sent** — the document's **Signatures** panel
  (Remind, Show link, Cancel request, Close the expired request, Save the audit
  trail) and the **Signatures** screen. Taking the permission away stops the
  next request, not the open ones: the requester can still cancel the request
  that keeps a file frozen.

## What a signature from here is worth

The signature comes from **this installation's own signing authority** — the
one filex generated, or your organisation's own certificate authority if an
administrator imported it. A reader who imports that authority's certificate
once sees these signatures as **valid**; a reader who has not says the
validity is **unknown**, which is not the same as invalid. Where the law asks
for a qualified electronic signature (in Turkey: e-imza or m-imza), use that
instead. AATL and EUTL membership are not a goal of this app.

That paragraph is not only in this README: it is on the home screen, in the
verification report, in the audit trail and in every receipt, in all five
languages, because a wrong expectation is worse than no signature at all.

Importing the authority certificate:

1. **Get the certificate.** filex serves the live authority certificate to
   an administrator at `GET /api/admin/app-plugins/signing/ca.pem` — there is
   no button for it in the panel yet. Open
   `https://<your filex>/api/admin/app-plugins/signing/ca.pem` in a browser tab
   where you are signed in to filex as an administrator and it downloads as
   `filex-signing-ca.pem`; or fetch it with an administrator's token:
   `curl -H "Authorization: Bearer <token>" -o filex-signing-ca.pem https://<your filex>/api/admin/app-plugins/signing/ca.pem`.
   Compare its SHA-256 fingerprint with the one **Apps → Signatures** shows.
   (`GET …/signing/cas` lists every authority, retired ones included.)
2. **Adobe Acrobat / Reader**: Preferences → Signatures → Identities &
   Trusted Certificates → Trusted Certificates → Import → pick
   `filex-signing-ca.pem`, tick *Use this certificate as a trusted root*.
3. **Foxit**: File → Preferences → Trust Manager → Trusted Certificates →
   Add → `filex-signing-ca.pem`.
4. Other readers: import as a trusted root for document signing.

Rotating or replacing the authority **retires** the old one, it never deletes
it: every signature it ever made stays checkable, and the Verify screen lists
the retired authorities beside the live one.

## The flows

### Sign yourself — three steps

Right-click a document → **Sign…**. It opens as a full page in a new tab.

1. **Place the boxes** — the document fills the screen; nothing else is asked.
   Name each box.
2. **Sign** — a plain form of those names, with the signature pad in the row
   of the box it belongs to.
3. **Where it goes** — a new version of this file, or a new file beside it.
   The file name is asked **only** for the second, and is then required.

An office document has no rendered pages to place a box on, so the screen
offers one thing: convert it to PDF beside the original. Then sign the PDF.

### Request signatures — eight steps

Right-click a PDF → **Request signatures…**:

1. **Signers** — pick people from this filex, and/or write identities by hand:
   one per line, as a name, an e-mail address, or `Ali Yılmaz <ali@…>`.
2. **Order** — everybody at once, or one after another. *Skipped when there is
   only one signer: there is no order to ask about.*
3. **The boxes** — what has to be filled in, and by whom: one box per
   signature, plus anything to be written, each with a name. No document on
   this step; the question is what the signer will be asked for.
4. **Place them** — the document fills the screen, and the boxes named a step
   ago are handed out one at a time to be put down.
5. **Time** — how many days the links are valid, an optional sign-by day, and
   how often a signer who has not signed is reminded (never, unless you say).
   The links can live no longer than this installation keeps any shared link
   (an administrator's setting under **Protection**, 7 days unless changed) —
   the step offers no more than that and says so. A sign-by day counts to its
   end: the request closes when that day is over.
6. **While it is open** — a PIN per outside signer or none, whether a signer
   may refuse (on unless you switch it off), whether to freeze the file while
   signatures are collected, whether to **lock the signed file** for good once
   every signature is in (until an administrator lifts it), a message.
7. **When it is done** — where the signed document goes (and its name, asked
   only when it is a new file), whether the signed copy is mailed to the
   signers as a link, whether that link carries a PIN, whether to write an
   audit trail (on unless you switch it off).
8. **Review → Send.** The review says how long the links will really live.

Colleagues get a notification that opens the signing screen. Outside signers
with an address get a mailed link. An identity with **no** address gets a link
and a PIN too — shown to *you*, in the job message and the bell, to hand over.
**A PIN is never in a mail**, for any of the three kinds of share this app
opens.

**One request at a time.** A document carries one signature request: one
record, one freeze, one set of boxes in the file. While a request is open,
**Request signatures…** says so on its first screen — who asked whom, how far
it got — and takes you to the document's **Signatures** panel to follow or
cancel it. Once it has ended (completed, cancelled, expired, refused), a new
request is the next round on the same document; its first step says that it
replaces the old record, and warns you first when that record holds an audit
trail that was never written as a file.

### Signing — three steps

A colleague opens **Sign / Fill** from the document's menu or the
notification; an outside signer opens their link and enters the PIN. Both see
the same screen:

1. **What is asked of me** — who is asking, their message, the deadline, and a
   list of every box with its name, its kind and its page. (If the request
   allows it, "I will not sign" lives here.)
2. **Fill it in** — one row per named box: text, a date, a tick, and the
   signature pad inline for a signature box. Every rule is checked here, so a
   wrong e-mail address is a sentence on the screen, not a failed job.
3. **See and approve** — the document with everything in place, plus a list of
   what was entered. Then **Sign**.

The plugin fills the document's **real form fields** (see below), issues a
certificate in the signer's name (for a filex account with no display name,
their e-mail — never a username, which means nothing outside filex), applies
the signature as an incremental
update, writes the result where the request said, revokes that signer's link
and hands them their **receipt** — a notification with the fingerprints and a
link to Verify for a colleague, a share of its own for an outside signer.

When the last signature lands the request completes, the freeze is lifted, the
audit trail is written (if asked for) and the signed document goes out to
every signer with an address as one filex share.

### Verify — on any PDF

Right-click → **Verify**. There is no state gate on this action on purpose:
the app only knows what it signed itself, and the document that most needs
checking is the one it did not. A PDF with no signatures says exactly that.

For each signature the report gives:

- the **identity** — from the certificate, and a note when the signature
  dictionary's own `/Name` disagrees with it (the certificate wins);
- the reason and the place, when they were given;
- **when** — and whether that is *proven* by a time-stamping authority or
  merely *declared* by the signer's own clock;
- whether the signature covers the **whole file**, and what the updates after
  it did: nothing, filled form fields and further signatures, or a page that
  now draws something else;
- the cryptographic verdict, warnings and errors;
- the certificate's **serial** and **SHA-256 fingerprint** (the line to
  compare against a receipt), its validity window, and whether the signing
  moment fell inside it;
- the **signing authority**: its name, fingerprint, validity, and whether it
  is one of this installation's own;
- what the certificate is **for**: extended key usage (document signing, RFC
  9336) and key usage.

Anything the certificate does not carry is reported as *not stated*, never
guessed.

### Following a request

The document's details panel → **Signatures**: state, order, deadline, freeze,
who signed when, **Remind**, **Show link**, **Cancel request**, **Close the
expired request**, the delivery link, the signing authority's fingerprint, and
— when the signed file went in as a new version — **Save the audit trail**.
None of it needs the *Request signatures* permission: a requester whose
permission was taken away still follows, reminds and cancels what they sent.

A signing link is an ordinary filex share, so it also appears under **My
shares** and, for an administrator, under **Shares**. **Revoking or deleting
it there closes the request**: that signer can no longer sign, so it cannot
finish as asked. filex wakes the app within seconds, the app asks after its
links, and the request is closed as *cancelled* — the panel says which link
was ended and when, the other links stop, the file is released, you are told,
and so is every other signer who was still waiting. (filex does not tell an
app that one of its links ended; the app asks, on its hourly wake-up — the
revoke only brings that wake-up forward.)

## Several signatures on one document

A document signed by two people used to have a problem: each signer's values
were drawn onto the **page**, so the second signer's fill rewrote page content
underneath the first signer's signature, and a strict reader was right to call
that "the document changed after it was signed".

The typed boxes of a request are **real AcroForm fields**. They
are created once, in one incremental update, before anything of ours is
signed; every signature after that only writes its own field values and their
appearance streams. Page content and page resources are never touched. A
filled field is then locked read-only, so a later signer cannot quietly change
what an earlier one signed over.

**No `/NeedAppearances`:** the appearance of every value is written here,
with the embedded face, so a Turkish name renders the same in every reader
instead of however the reader guesses.

## Certified, sealed, and the hash to everyone

The signature fields — every signer's and filex's seal's — are created with
the form, before anything is signed. So:

- **The first signature certifies the document** (DocMDP P=2): from then on
  only filling in the form and signing are permitted, which is all a later
  signer does.
- **When the last signature lands, filex seals the document** with the
  installation's own seal (*filex document seal*, from the same authority),
  into a field locked with P=1. After that, a PDF reader reports **any**
  change as *changes not permitted* — not merely "modified after signing".
- **Every party is sent the SHA-256 of the sealed file** — the requester and
  inside signers in filex, outside signers by mail — with the seal's
  fingerprint and how to check it (Verify, or `sha256sum` / `Get-FileHash`).
- **Lock the signed file** is an option of the request: the signed file stays
  locked in filex for good, until an administrator lifts it (audited).

Verify says, before any detail: every signature valid, certified, sealed by
filex, and whether this is the file whose hash everybody was sent — and, in
red, any change that was not permitted. See [docs/SIGN.md](docs/SIGN.md) §7a.

A request opened by an older build keeps working the old way, because its
boxes were never created as fields.

## Time stamping

Off by default. Admin → Plugins → **Apps** → e-Signature → **Settings**:

- **Add a time stamp to every signature** — when off, the signing time is the
  signer's own claim and the verification report says so.
- **Time-stamping authority** — the RFC 3161 endpoint; empty uses
  `https://freetsa.org/tsr`, which is the host the manifest asks permission
  for. Another host needs its own `http:` grant.

The request carries the signature's **digest and a nonce** — the document
never leaves the installation. If the authority cannot be reached the
signature is still made, without a stamp, and the report and the job message
say so.

Leaf certificates are issued for ten years. That is deliberate: a verifier
checks a certificate against the clock it runs with, so a short-lived leaf
makes perfectly good signatures read as "expired" a month later. There is no
key risk in the long horizon — the private key is destroyed seconds after the
signature is written, so the certificate proves identity and can sign nothing.

## Limits

| | v0.2.0 |
|---|---|
| Input | PDF, and any office document this installation's LibreOffice can open (ODT, DOCX, XLSX, PPTX, RTF, TXT…). Without LibreOffice, PDFs only. |
| Refused | Encrypted PDFs (remove the password), XFA forms (flatten first). |
| Requests on office documents | A request needs rendered pages to place boxes on, so the screen converts the file and asks you to work on the PDF. |
| Boxes | Signature, initials, date, text, tick box. **A date is a field type, not a text rule** — text rules are free text, numbers, an e-mail address and length bounds. Each signature box is drawn or typed (chosen when it is defined) and carries its own drawing, with the lines chosen for it printed under it. |
| Signature level | PAdES-B, with an optional RFC 3161 time stamp. The first signature certifies (DocMDP P=2); filex's seal closes the document (P=1). **No LTV**: validity depends on the authority you imported. |
| Stamped values | Real embedded text (Type0 / Identity-H, subset per face), selectable and copyable. A letter a face does not have (Homemade Apple has no Ş, ğ or İ) is borrowed from the nearest face that does. |
| Other scripts | Every script Noto draws — Arabic, Hebrew, Devanagari and the Indic scripts, Thai, Khmer, Ethiopic, Chinese, Japanese, Korean and 150 more — **fetched the first time a text needs it** (0 MB in the app; `http:fonts.gstatic.com`, each file pinned by sha256), shaped with HarfBuzz and laid out right to left where it reads so. Without internet those characters are not printed, and the screen says so while you type. Nine very new scripts have no Noto font and are declared as such. Not done: explicit bidi embeddings/isolates, bracket pairing, jamo composition (docs/SIGN.md §6). |
| Appearance text | The signer's name and the date inside the signature widget are rasterised into the appearance image, so they are not selectable text. |
| Reminders | Only what you asked for: leave "remind every N days" at zero and nothing is ever sent. Set it, and filex's hourly wake-up sends that nudge on time — to signers it can actually write to, while the request is open — and the panel's "Remind" button still sends one whenever you want. |
| Expiry | A request that runs out at 03:00 **closes at 03:00**, with nobody present: filex wakes the app hourly and runs the closure at the minute it falls due — record closed, file released, links revoked, both sides told. The first job to touch a lapsed request still closes it too, so an instance whose wake-up is off (a demo) loses nothing. A freeze always carries a TTL, so nothing stays locked for ever. |
| Link life | No longer than the lowest of: what you asked for, the signing page's own 90 days, and what this installation allows ANY shared link (Admin → Protection, 7 days unless changed). The Time step offers no more than that and says why; the review, the record and the requester's notice all give the real day. |
| Ended links | A signing link revoked or deleted on the Shares screen closes its request as cancelled, within seconds; the panel says whose link it was. A link that simply ran out closes the request as expired, at its own minute. |
| Who may ask | Accounts that hold **Request signatures** (`app.sign.request`): by default those that can change files, never viewers; the administrator decides per role and per person. filex tells the app whether the reader holds it, so the hints on its screens ("…or Request signatures… to ask others") are drawn only for those who do. |
| Requests per document | One open at a time. After it has ended, a new request replaces its record in the Signatures panel; the signatures stay in the file and Verify still reports them. |
| Audit trail | Written beside the document when the request completes **and** the output is a new file; with a version output it is one click in the panel (a job commits one kind of output). In the requester's language, whoever's signature completed the request. |
| Receipts | A share that lives 30 days. Download the three files and keep them; the certificate itself is kept with the document, so the receipt can be given again from the Sign / Fill screen. |
| PINs | Shown to the requester, never mailed — for signing links, receipts and the delivery link alike. |
| Signatures screen | Lists this app's own work through `state_list`: only its own state rows, only files you may already see, deleted files never. It is a **list, not a search** — 100 documents, newest first, and it says so when there are more. A row takes you to its document, on the screen its section is about. |
| Size | Documents up to filex's plugin input limit (256 MB by default); Verify and the details panel skip documents above 24 MB. |

## Development

```
filex-app.json          the manifest (embedded into the module at build time)
manifest.go             parses the embedded manifest — `describe` cannot drift
cmd/plugin/main.go      wasm entry point: installs the host transport, registers in init()
internal/app            the flows: sign, request, fill, apply, verify; the screens; the shares
internal/envelope       the request as data + the pure state machine
internal/fields         box kinds, rules, date layouts — refusals in all five languages
internal/fontkit        the five embedded font subsets, glyphs and fallbacks
internal/geometry       fractions ↔ PDF user space (Rotate 0/90/180/270, CropBox)
internal/pdfdoc         incremental updates: the AcroForm path (form.go) and the old stamper
internal/pdfsig         intake checks, appearance, the CMS (digitorus/pkcs7) and the time stamp
internal/pdfdoc         form fields, values, stamps, the audit trail, and the signature itself (sign.go)
internal/verify         what a signature proves: coverage, later changes, the authority
internal/receipt        the .p7b / .pem / .txt a signer is handed
internal/views          surface builders (what each person sees, in each of the five languages)
internal/host           Host interface: Kit (pluginkit) and Fake (tests, in-memory CA)
internal/hostnet        Go's http.DefaultTransport routed through filex, for the TSA call
internal/testca         throw-away CA shaped like filex's
internal/testpdf        tiny PDF writer for tests (xref table or xref stream)
scripts/build.sh        gofmt + vet + tests, then wasm, sha256 and the size gate (≤ 22 MB)
scripts/fonts.py        regenerates the embedded font subsets from google/fonts
docs/SIGN.md            the long version of this page
```

Go 1.27 (the pinned `digitorus/pdfsign` needs it; `GOTOOLCHAIN=auto` fetches
it). No cgo, no TinyGo.

```bash
go test ./...                       # everything runs on the host, no wasm runtime needed
bash scripts/build.sh               # tests first, then dist/plugin.wasm + sha256
bash scripts/build.sh --stamp       # also writes the sha256 into filex-app.json (release)
```

`scripts/build.sh` runs `gofmt -l`, `go vet` and `go test ./...` **before** it
builds, and refuses to produce a module when any of them fails. A wasm that
was never checked is worse than no wasm: it installs, it looks right, and the
first person to use it finds out.

## Languages

> **Spanish, German and French are AI-translated, awaiting review by a native
> speaker — corrections welcome.** English and Turkish are the source
> languages: they are written in the code, by hand, beside each other. The
> three translated ones carry legal wording — the signature sentence, the
> consent text, the audit trail — so a correction to them is a correction to
> what the app says on paper, not a matter of style.

English and Turkish are written in the code, side by side, at every text:
`T("Back", "Geri")`, `l.Sf("%s signed", "%s imzaladı", who)`. Spanish, German
and French are data: `internal/i18n/catalogue/{es,de,fr}.json`, keyed by the
English exactly as written, format verbs included. A translator edits JSON and
never touches Go; the terms follow the glossaries of filex's language packs
(`filex-lang-es`, `-de`, `-fr`), and the French follows the pack's typography
(no-break spaces before `:` `;` `!` `?`, « guillemets », ’).

`internal/i18n/catalogue_test.go` keeps it complete. It reads the module's own
source, finds every function that takes an English–Turkish pair, and collects
the English written at each call. It refuses a text built at run time (a
concatenation no catalogue can hold), a hand-made `wire.Text`, and any `"tr"`
branch; and it holds each catalogue to exactly those keys — nothing missing,
nothing stale, no empty value, the same format verbs (reordered only with
explicit indexes, `%[2]s`), the same leading and trailing whitespace. A word
that means two things in another language says so with a context
(`Tc("time", "Signed", …)`), and the English on screen does not change. The
same test holds `filex-app.json`: every text in every declared language.

filex v0.43.0 hands an app the reader's own language, a language pack's
included, so `es`, `de` and `fr` are read as written. (Older hosts narrowed
every locale but Turkish to `en` — they cannot install this app, which
declares `filex: ">=0.49.0"`.) The app answers `es`, `de` and `fr` whenever it
is asked in them; the tests ask directly (`TestEveryLanguage_*`).

The test suite is also where the product rules are enforced, not just the
code:

- every `wire.Text` the app can show carries **every** declared language, in
  every screen and in every locale;
- a form field's label, help and options **change** with the call's language
  (a plain string is picked by the plugin, so an English label on a Turkish
  screen is a bug the scan catches);
- at most one primary button per step; a `select` never grows past what can be
  drawn as buttons;
- `show_when` / `required_when` name a field of the same form, and the file
  name is asked only when the output is a new file;
- two signatures on one document leave the **first** one reading as "form
  fields were filled in and signatures added; no page draws anything
  different".

The build is **reproducible**: the same tree always produces the same sha256.
That is not a nicety — `filex-app.json` carries the module's own hash and
filex checks it at install, so the hash committed before a tag has to survive
CI rebuilding the module at that tag. Two things make it hold: `-buildvcs=false`
(Go otherwise stamps the git commit into the module, and `-trimpath` does not
cover that), and blanking `wasm.sha256` before the build (the manifest is
embedded, so a stamped manifest would change the very hash it records).

**Releasing** (the maintainer's decision): `bash scripts/build.sh --stamp`,
commit the stamped `filex-app.json`, then push the tag. CI refuses a tree
whose stamped hash is not the one it builds.

### Fonts

`internal/fontkit/fonts/*.ttf` are committed subsets (Latin, Latin-1, Latin
Extended-A — which is where ı İ ş Ş ğ Ğ live — punctuation and currency),
regenerated by `scripts/fonts.py` from [google/fonts](https://github.com/google/fonts)
with `fonttools`. Their licences are beside them in `fonts/licenses/` (OFL for
Caveat, Dancing Script, Inter and Source Serif 4; Apache 2.0 for Homemade
Apple). Every subset is embedded in the wasm and, when used, in the signed
PDF — a reader needs nothing installed.

Every other script is a Noto face (OFL), NOT in the repository or the wasm:
`internal/fontkit/noto_table.go` pins each one by URL and sha256 and filex
downloads it the first time a text needs it. Regenerate the table with
`go run scripts/notogen/main.go` (it downloads every face to hash it), then
`FILEX_SIGN_VERIFY_ALL_FONTS=1 go test ./internal/fontkit -run TestTable_EveryPin`.
The tests that draw real Arabic, Devanagari or Japanese download the faces
they need once into the temp directory and skip without a network —
`FILEX_SIGN_REQUIRE_FONTS=1` makes that a failure (CI with a network).

### The SDK

The guest SDK is filex's own Go module, pinned in `go.mod` to the filex
release this app targets:

```
require github.com/brf-tech/filex/backend v0.49.0
```

A fresh clone needs nothing else checked out: `go build` fetches
`backend/pkg/pluginkit` from the Go module proxy. The module's sha256
depends on that exact SDK version (Go records it in the module), so
moving to a newer filex means `go get github.com/brf-tech/filex/backend@<tag>`,
`bash scripts/build.sh --stamp` and committing the new hash.

### Install a local build

Admin → Plugins → Apps → Install → **Files**: `dist/plugin.wasm` +
`filex-app.json`. Or push a tag: the release workflow builds, stamps the
sha256 into `filex-app.json` and attaches both to the GitHub release.

## License

MIT — see [LICENSE](LICENSE). © BRF Teknoloji.
