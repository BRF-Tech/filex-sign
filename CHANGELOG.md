# Changelog

All notable changes to filex-sign are listed here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[SemVer](https://semver.org/) and match `filex-app.json` → `version`.

## [Unreleased]

## [0.3.0] - 2026-10-02

A minor version: a capability goes away. Still **filex v0.49.0** or later,
and no new permission: the update asks for one fewer.

### Changed
- **e-Signature signs PDFs only.** To sign an office document (DOCX, XLSX,
  PPTX, ODT…), convert it to PDF first with the
  [Convert](https://github.com/BRF-Tech/filex-convert) app (or an office
  program), then sign the PDF. **Sign…**, **Request signatures…** and every
  other action of this app apply to PDFs alone, on every installation,
  whatever engines the server has: an office document is offered none of
  them. Until now a DOCX was offered both rows wherever LibreOffice was
  installed, and both screens offered **Convert to PDF**, which made signing
  depend on a converter.
- A **Sign…** or **Request signatures…** page reached on a document that is
  not a PDF some other way (an old bookmark, a screen left open across the
  update) says "This has to be a PDF first", tells the person to convert it
  with the Convert app and come back with the PDF, and offers no button. A
  click on the old screen's **Convert to PDF** lands there and queues nothing.
- A job that reaches the app on a document that is not a PDF (a direct run,
  or a `sign` job with `op=convert` queued before the update) is refused in
  the same words, in the reader's language, before anything is written,
  frozen, shared or sent. The app goes by the bytes, not the name: a PDF
  without the extension is still signed.
- The Signatures screen's "How this works" says the same on every
  installation: only PDFs can be signed; an office document is converted
  with the Convert app first. It used to depend on whether LibreOffice was
  installed.
- **What an older version left is kept.** No version could open a request on
  an office document (the job refused it from the first version that knew
  office documents), so there is no request, freeze or signing link on one
  to strand. 0.1.0 signed office documents directly and left the `signed`
  badge and the `sealed` record on the office document: both stay, the
  document is still listed under **Signed**, and Verify on the signed PDF
  still finds the record that sealed it.
- **No long dash in anything a person reads.** Every text of the app, in
  all five languages, writes a plain "-" where it had an em dash, an en
  dash, a Unicode hyphen, a figure dash, a horizontal bar or a minus sign:
  the screens (the titles "%s - imzala" and "Request signatures - %s"
  among them, shared with the read-only storage screen), the mails and
  notices, the audit trail and the receipt, a date range ("Sep 22, 2026 -
  Sep 22, 2027"), the "nothing here" placeholder, the es/de/fr catalogues
  (their keys follow the English) and every text of `filex-app.json`. The
  catalogue gate holds it: `TestCatalogue_NoLongDashAnywhereAPersonReads`
  reads every string literal of the code, every catalogue key and value
  and every manifest string, and fails on any of the seven characters.
- `pdfonly_test.go`: `TestPDFOnly_NothingIsOfferedOnAnOfficeDocument` (with
  every engine filex knows present), `TestPDFOnly_ManifestAsksForNoEngine`,
  `TestPDFOnly_ScreensOnAnOfficeDocumentSayConvertFirst`,
  `TestPDFOnly_JobsRefuseAnOfficeDocumentInWords`, and the guard
  `TestPDFOnly_WhatAnOlderVersionLeftOnAnOfficeDocumentIsKept`.

### Removed
- **Converting office documents.** The `engines:libreoffice` permission and
  its reason, `applies.engine_ext` on **Sign…** and **Request signatures…**,
  the hidden `convert` action, the **Convert to PDF** screens and button,
  LibreOffice's progress and error messages, and the engine calls in the
  `host.Host` seam. The app runs no server engine and depends on no other
  app. The texts that went with them are gone from the es, de and fr
  catalogues.

## [0.2.0] - 2026-09-29

A minor version: a new permission and a new floor. **Needs filex v0.49.0**,
the first filex that knows an app's own permissions - an older one refuses
the manifest (it does not know `user_permissions` or `requires`). The
manifest says so with `filex: ">=0.49.0"` in place of `min_filex: 0.43.0`.
On filex v0.43.0-v0.48.x, stay on 0.1.1.

### Added
- **Asking for signatures is a permission.** The manifest declares one user
  permission, **Request signatures** (`app.sign.request`, `user_permissions`
  → `request`), in all five languages, default `user`: accounts that can
  change files hold it until the administrator decides otherwise, per role
  or per person, in filex's role editor. The **Request signatures…** action
  and its wizard (`request` view) carry `requires: "request"`, so an account
  without it does not see the menu row, and a direct run, opening the
  wizard or any of its steps is refused with 403. Measured on filex built
  from `rel/0.49`: a user sees the row. A user whose exception says deny and
  a viewer do not, and get 403 on the action, the wizard and its events; a
  user whose built-in role says deny loses the row and gets 403 on the
  action, and has both back when the decision is cleared.
- **Signing needs no permission.** Nothing else carries `requires`: Sign…,
  Sign / Fill and the hidden `apply` a signer's screen queues, Verify, the
  hidden `convert`, the outside signer's page, the document's Signatures
  panel and the Signatures screen. `apply` is also what the panel's Remind,
  Cancel request and Close the expired request queue, so a requester whose
  permission is taken away still follows and cancels what they already sent -
  gating it would have left their files frozen and their signers unable to
  sign. Measured: the user refused the request permission still signs a
  document himself, and Verify reports his signature and filex's seal as
  valid.
- `TestManifest_AskingIsAPermissionSigningIsNot` holds the line: exactly one
  user permission, labelled and described in every language, only `request`
  and its view gated, and every action requiring what its view requires.
- **The hints follow the permission.** filex 0.49.0 tells the app which of
  its permissions the reader holds (`actor.permissions`). The document's
  Signatures panel, the Verify screen and the Signatures screen's "How this
  works" say "…or Request signatures… to ask others" only to a reader who
  holds it, and so does the answer to Remind or Cancel on a document with no
  request ("Start one with Request signatures…"); everybody else is pointed
  at Sign… alone, in all five languages.
  `TestHints_RequestSignaturesIsOfferedOnlyToWhoMayAsk`.

### Changed
- `scripts/build.sh`, while go.mod `replace`s the SDK with a local
  development copy, asks the copy that line names whether it is current. It
  used to ask one fixed directory and skip the check in silence when that
  directory, or node, was missing; now it refuses the build instead. With the
  published SDK (no `replace`) nothing changes.
- `TestManifestPassesTheSDKsOwnChecks` no longer adds `schedule` to the test
  kit's permission list: the kit has known it since filex v0.43.0.
- Built against filex v0.49.0's guest SDK
  (`github.com/brf-tech/filex/backend v0.49.0`, from the Go module proxy;
  0.1.1 was built against v0.43.0). It carries `user_permissions`,
  `requires` and `Actor.Can`, which this version needs.

## [0.1.1] - 2026-09-26

Proposed as **0.1.1** (a patch: fixes, one hidden action, no new permission,
still filex ≥ 0.43.0).

### Fixed
- **"Convert to PDF" did nothing.** Asking for a signature on a DOCX (or any
  office document) showed "Convert to PDF", and clicking it redrew the same
  screen: the button is the screen's primary one, filex posts a primary button
  as `submit`, and the app listened for `action` only. It now queues the
  conversion - in the full page, the dialog and an embedded explorer's popup -
  and the PDF lands beside the original. The same mistake had killed "Open its
  Signatures panel" (a second request on an open document) and "Close the
  expired request" (the Signatures panel); both work now. Every handler asks
  for the button, not the event (`pressed`), and a test presses every footer
  button the way filex's renderer does (the old test sent `action` for a
  primary button, which filex never does, and stayed green).
- **Parts of the app were English on a Turkish screen.**
  - A form field's label, help and placeholder, and a select's options, were
    one string in the language filex TOLD the app - the account's, which an
    embedded explorer drawing Turkish over an English account got wrong
    ("Identity", "One signer per line…"). Every field text now travels in
    every language and the screen picks its own, on any filex ≥ 0.43.0. (filex
    itself also starts telling apps the screen's language - see its
    changelog.)
  - English spliced into Turkish sentences: the certificate's uses on every
    Verify card ("document signing, e-mail protection · digital signature,
    non-repudiation"), the host's reason signing is off ("FILEX_SECRET_KEY is
    not set"), a host error code ("rate_limited", "unavailable"), and
    LibreOffice's or the PDF reader's own error text ("libreoffice produced no
    PDF (exit 1)", "verify: not a PDF file"). Each is now said in words, in
    every language; the raw text goes to the app's log. The Verify screen no
    longer says "okunamadı" twice.
  - The Turkish coverage line said the numbers the wrong way round ("5000
    baytın ilki 8000 bayt" → "8000 baytın ilk 5000 baytı").
  - Wording: "nonce", "istekçi", "ayrıntı paneli", "dosya yöneticisi",
    "kıpırdamadı", "hareket etmiş", "demeti", "Verilen:" and lowercase
    sentence starts replaced with filex's own words.
  - `TestTurkish_*` (views and app) fail when a word of a screen's English
    build appears in its Turkish build, when a field text is one string, and
    on each of the job failures above.

### Added
- The hidden action `convert` (the second half of "Convert to PDF"), so the
  operations tray says "Convert to PDF" / "PDF'e dönüştür" instead of "Sign…".
  A `sign` job with `op=convert` from an older screen still converts.

## [0.1.0] - 2026-09-24

⚠ The first version anybody can install. The app was built in three rounds
before it was published, and those rounds carried the numbers 0.1.0-0.3.0
while nothing was released; no installation ever ran them, so they are not
versions - they are folded in below as milestones, newest first, for the
reasons their entries give. The public history starts here.

Needs filex v0.43.0, whose call inputs carry the installation's share
ceiling (`share_max_ttl_days`) and whose SDK has `pluginkit.ShareInfo`,
`wire.Surface.Sections`, `wire.Actor.IP`, file-less notice targets,
`FileRef.read_only`, `applies.engine_ext`, per-language setting texts, a
public page's `purpose` and `asset_fetch`.

The module is 20.6 MB (the size gate is 22 MB): the text-layout engine -
HarfBuzz, the bidi rules, the script tables - is 2.8 MB of it; the fonts are
none of it.

### Added
- **Spanish, German and French.** Every text the app carries - screens, mails,
  notices, the receipt, the audit trail, the lines printed under a signature
  (their date written the way each language writes dates), and the manifest's
  labels, descriptions, settings and permission reasons - in `es`, `de` and
  `fr`, in the terms and register of filex's language packs (usted / Sie /
  vous). English and Turkish stay in the code; the other three are
  `internal/i18n/catalogue/*.json`, keyed by the English. A test reads the
  source and refuses a text no catalogue can reach, a missing or stale
  translation, a changed format verb and an empty value. filex 0.43.0 - which
  this app requires - hands an app the reader's own language, so these
  languages are read as written.
  ⚠ Those three were produced by an AI and have not been read by a native
  speaker yet. The app says so where anybody looks - the manifest's
  description in each of the five languages, the README's feature list and
  its *Languages* section, and `docs/SIGN.md` - because what they carry is
  legal wording: the sentence printed under a signature, what a signer
  consents to, the audit trail. Corrections are welcome and are corrections
  to what the app says on paper, not matters of style. English and Turkish
  are the source languages and carry no such label.
- **A space a face lacks is drawn with the face's own space.** The French
  audit trail's narrow no-break spaces (before `;` `!` `?`) were dropped from
  the PDF - the embedded Inter subset stops at U+2027 - and the words ran
  together; a Unicode space the face does not carry now uses its space glyph
  and copies out as a plain space. A face whose own cmap draws U+00A0 with its
  space glyph (Source Serif 4) no longer risks every space copying out as a
  no-break one.
- **Certified, sealed, and the hash to everyone** (the owner, 2026-09-22:
  "does the signature say so - or do we lock the file? Both, plus a seal").
  The first signature certifies the document (DocMDP P=2: filling in the form
  and signing only); every signature field - the seal's included - is made
  before anything is signed, so a later signer only fills and signs. When the
  last signature lands filex seals the whole document with the installation's
  own seal into a field locked with P=1, so a reader reports any later change
  as *not permitted*. The SHA-256 of exactly the sealed bytes, the seal's
  fingerprint and how to check them go to the requester, every inside signer
  and every outside signer (with the delivery link when the request sends the
  document), and into the audit trail. Verify says whether every signature is
  valid, the certification, the seal, whether this is the file whose hash was
  sent, and names any change that was not permitted. Signing is now written
  here (`pdfdoc/sign.go`) instead of by digitorus/pdfsign, which could not
  certify properly or sign an existing field.
- Measured end to end in a browser (filex e2e 101) and with pyHanko on the
  delivered bytes. The run found, and this fixes: a page's content stream
  rewritten after the seal was named "object N was changed" instead of "page
  N draws something different"; a certification followed by a forbidden
  change was shown as "the signature does not match the bytes it covers"
  with no certificate (digitorus/pdfsign's DocMDP check gives up before it
  checks the signature - it is now checked against its own revision); and
  the seal's typed name was "filex" rather than its certificate's, so Verify
  warned about our own seal. After a forbidden change the summary no longer
  says "every signature is valid": it says the signatures are intact and the
  change is what is wrong.
- **"Lock the signed file when every signature is in"** - an option beside the
  freeze: the signed file stays locked in filex until an administrator lifts
  it. Without it the file is an ordinary file afterwards; the seal and the hash
  still show any change.
- **Text in any script Noto draws - fetched when it is used, 0 MB in the
  app.** A name in Japanese, a box filled in Arabic, a signer in Hindi used to
  lose every letter; now each such script's Noto face is downloaded by filex
  the first time a text needs it (`asset_fetch`, permission
  `http:fonts.gstatic.com`, every file pinned by sha256 in a generated table -
  161 faces for 168 scripts), subset into the PDF, and the line is laid out
  properly: the Unicode bidi rules (written here, since the libraries got
  mixed Hebrew-and-number lines wrong), HarfBuzz shaping for joined Arabic and
  Indic conjuncts, the CJK faces by their character map. Complex clusters
  carry `/ActualText`, so copying "क्षत्रिय" gives "क्षत्रिय" back. Latin and
  Turkish never touch the network.
- **What will not print is said while you type.** Offline, or for one of the
  nine scripts no Noto face draws, the fill step, the signers step and the
  boxes step say - under what was typed, at the next pause - which
  characters will be left out and why ("could not be downloaded", "still
  being downloaded", "no font draws the Garay script"); the approve steps
  show every value and every line under a signature as it will be printed.
- **The signing screen tells a font still downloading from a network that is
  down**, and the app no longer logs a font it cannot fetch at every
  keystroke (filex says it once per outage).
- **The Signatures page is a page, with a menu.** It opened as a dialog with
  four tables and a manual under them; it is now a page of its own in the app
  (filex draws it in the same tab), its sections in a menu - *Waiting for my
  signature · I asked for these · I have signed these · Every request*
  (administrators) · *How it works* - each one table, each entry counting its
  rows. The section is part of the page's address, so Back walks them and a
  link lands on one: the requester's progress notices now open *I asked for
  these*.
- **Lines under a signature, chosen per box.** In the define step every
  signature box chooses what is printed under it - the signer's name, e-mail,
  date and time, IP address, certificate fingerprint, certificate serial,
  signing authority - defaulting to the name and the date, as before. Every
  value comes from the signing record; the requester chooses which facts,
  never their values. A card previews the lines in this app's own words.
- **The signer sees what will be printed before signing.** The approve step
  shows each signature box as the job will print it (the same drawing
  function, at half the resolution) and lists the lines in words, the time
  and certificate marked as the ones of the moment Sign is pressed. The IP
  printed is the address of the screen they approved on - a signed-in
  signer's too, which filex now hands the app.
- **Drawn or typed.** A signature box is drawn (the default) or typed; only a
  typed one asks for a face, and the signer's pad offers exactly what was
  chosen - draw or a picture for a drawn box, the name in the chosen face for
  a typed one.
- **A box's identity.** Beside its name - free text in any script, kept
  exactly as typed - every box gets an ASCII identity ("Müşteri adı" →
  `musteri-adi`) that becomes the PDF form field's name, unique in the
  document and clear of the form fields it already carries. It is never
  shown; it follows the name until the request is sent and is fixed from
  then on.
- **Every signature box carries its signer's drawing.** A second signature
  box for the same person showed only their name in a handwriting face while
  the pad had asked for a drawing in it; it now shows its own drawing and its
  own lines, as a form field's appearance.
- **The hourly wake-up** (`schedule` permission, `tick` export). A request
  that runs out at 03:00 closes at 03:00 - record closed, file released,
  links revoked, both sides told - and the reminders you asked for ("remind
  every N days") go out on the day they fall due, with nobody present. Every
  job still closes a lapsed request it touches.
- **A signing link revoked or deleted on the Shares screen closes its
  request.** It used to stop the link and nothing else: the request stayed
  "Sent · 0/1 signed", the file frozen, for a signature that could never
  come. filex sends an app no event for this; the wake-up asks after every
  open link (`share_state`) and filex brings the wake-up forward when a person
  ends one, so the request closes within seconds - as cancelled, the panel
  saying whose link was ended and when, the other links stopped, the file
  released, the requester and every other waiting signer told. A link that
  ran out on its own day is still the expiry; a link whose facts cannot be
  read is left alone.
- **One request at a time, said on the first screen.** On a document whose
  request is still open, *Request signatures…* says so at once - who asked
  whom, how far it got - and takes you to the document's Signatures panel.
  It used to let you through all eight steps and answer Send with "Invalid
  data". After a request has ended, a new one is the next round; its first
  step says it replaces the old record, and warns you when that record holds
  an audit trail never written as a file.

- **Every link the app opens says what it is.** In My shares a signer's link
  is marked *Signing request*, a receipt's *Signature receipt* (the
  manifest pages' `purpose`), and the finished document's delivery link -
  a page-less share, which listed as one somebody made by hand - *Signed
  copy* (a purpose given at `share_create`). The mark opens the Signatures
  page at the section of whoever the link belongs to (*I asked for these*
  for the requester, *I have signed these* for a signer whose own signature
  finished the request), and Revoke says first what it does: a signer's link
  cancels the whole request; a receipt's or the delivery's only takes that
  page away.

### Changed
- **"Sign…" and "Request signatures…" are offered on an office document only
  where LibreOffice is.** Without it they were offered on a .docx and opened
  a page saying it could not be done, while the Signatures page said only
  PDFs can be signed (`applies.engine_ext`).
- **The settings speak Turkish.** The time-stamp settings were English inside
  the Turkish admin panel.
- **The approve step is quick.** The signer's drawing was re-encoded at the
  best compression level on every event of their screen - up to eight times
  per drawing once two boxes shared the job's 64 KiB - and the stamped
  pictures were composed the same way: "Next" to the approve step took 2.6-4.6
  s and takes about 0.6 s now. A drawing that already fits is kept as it came;
  the pictures use the default level (4-8× faster, ~4 % larger).
- **"Anyone" is a row of the review.** Boxes that belong to anyone were
  counted for nobody - every signer's row said "0 to fill" and a line under
  the table said "Unassigned boxes: 2". They are a row of their own now, named
  as the define step names them (en *Anyone*, tr *Herkes*), so the rows add up
  to the boxes on the document. English plurals ("2 signatures").
- **A box nobody named is named in the reader's language.** The default name
  used to be written into the record in the requester's language; a Turkish
  signer of an English requester's document was asked for "Signature",
  "Date" and "Text". An unnamed box stays unnamed and every screen names it
  ("İmza 1", "Signature 2").
- **A required signature says so.** The pad is labelled like any field, with
  the same `*` a required field wears; the "what is asked of you" list marks
  required boxes too.
- **The signing and receipt links carry no subject.** It was a string frozen
  in the requester's language ("Please sign contract.pdf" on a Turkish page),
  printed under a title the app draws in the visitor's; the requester's
  message stays on the first step, in their own words.
- **The links live what the screens say.** filex cuts every shared link to
  the installation's ceiling (7 days unless an administrator changed it),
  silently; the Time step offered 14 and the review said "links valid 14
  days" while the links lived 7. The step now offers no more than the
  installation allows and says why, the review gives the life the links will
  really have, the request job asks for no more (so the record, the freeze
  and the mails agree), and the requester's notice names the day the links
  stop.
- **A sign-by day counts to its end.** Links were cut to whole days rounded
  down, so a request sent at 09:00 "to sign by the 22nd" lost its link at
  09:00 on the 22nd. They now last to the end of that day, when the request
  closes.
- **The audit trail is written in the requester's language** - whoever's
  signature completes the request - with every letter in the embedded face.
  It was English only.
- The Time step asks "Remind the signers every (days)" and says the wake-up
  sends it; it used to say filex gives an app no timer.

### Fixed
- **The signing screen no longer logs a refused write on every open.** The
  in-app screen is a view, filex never lets a view write, and the screen
  tried to record the signer's opening anyway: "recording the open:
  permission_denied" on every open. It no longer tries. An inside signer's
  opening is therefore not announced to the requester from the app (the
  signing link's page still announces an outside signer's); they hear when
  the signer signs or refuses.
- **A request on a read-only storage is refused before anything happens.**
  It used to be accepted and sent - the file frozen, the signer notified -
  and the signer's answer then failed for ever, because the signed version
  could not be saved. The wizards (request, and sign it yourself) say so at
  their first screen and the request job refuses too, before any record,
  freeze, link or notice.
- **The Signatures page's "Due" column shows a date.** It printed the
  optional sign-by day alone and read "-" on every request; it is the day
  the request runs out - the sign-by day, or the day its last link dies.
  The column is a date column (`format: "date"`, filex 0.43), so filex
  prints it the way it prints every date ("29 Eyl 2026", not
  "2026-09-29"). A request with only people inside filex has no such day
  and keeps its dash: it stays open until everyone has answered.
- **Every date a person reads is written the way filex writes dates.** The
  mails, notices, the status panel, the verification report and the
  receipt screen printed ISO days ("2026-09-29"); they now say "29 Eyl
  2026" / "Sep 29, 2026" / "29. Sept. 2026" in the reader's language, through
  the SDK's `pluginkit/humandate` (no month table of the app's own). The
  audit trail keeps ISO 8601 - it must be exact.
- **"Sign / Fill" is offered to the people who have something to sign.**
  It was offered on every pending document to everybody who could see it,
  and the page then told a non-signer they were not one. The app now keeps
  a personal marker (`todo@<user id>`, filex 0.43's personal state) for each
  signer with an account whose turn it is, and the action asks for
  `todo@me`: filex shows it to that person only. The page still checks.
- **"Request signatures…" is not offered on a read-only storage**
  (`applies.writable`): its flow ends in writing the signed document, so
  its first screen could only refuse. filex hides it there.
- **A frozen document's reason is said in the reader's language.** The
  reasons ("signatures are being collected", "signed by everybody and
  sealed by filex") are manifest `messages` in all five languages, and the
  lock names one by key (`FileLockMessage`): the admin's lock list, the
  details panel and a refused rename say it in THEIR language, not in the
  requester's.
- **The tables name a person the way filex does**: the Signatures home's
  *Waiting for* column and the status table print the name ("Gülşen"), and
  the e-mail only for somebody who has no name - not "Gülşen
  (gulsen@local)". The certificate and the signature line still name an
  account with no display name by its e-mail, deliberately.
- **Two signature boxes could not be signed.** One job carries every drawing
  and filex caps its parameters at 64 KiB; at 40 KB apiece, two boxes were
  refused with 413. The drawings share the budget now, and the first one no
  longer travels twice.
- **The serif face was silently replaced by Inter.** filex's renderer calls
  it `source-serif`, this app `source-serif-4`; nothing translated, so the
  face was offered, chosen and ignored. Both spellings mean the same face.
- A required signature box that was not the first one was never checked on
  the fill step.
- **"Let a signer refuse" and "Write an audit trail PDF" start ON**, as the
  form declared. A new request held `false` for both, and a form's value wins
  over its default, so a request sent without touching them had neither.
- README: the request has eight steps, not seven; the signing authority's
  certificate is fetched from `GET /api/admin/app-plugins/signing/ca.pem`
  (there is no "Signing" screen in the panel); time stamping lives under
  Admin → Plugins → Apps → e-Signature → Settings.

### Before the first release

#### Milestone 3 (built as "0.3.0", never published)

The round Burak asked for after trying v0.2.0 end to end: the two sides of a
signature became one step-by-step screen, boxes got names, a signer stopped
having to have an e-mail address, everybody gets a receipt, the finished
document goes out by itself, and a second signature no longer disturbs the
first.

##### Added
- **Verify, on any PDF.** A new action and a full-page report: who signed,
  which authority stands behind them, what the certificate attests (subject,
  e-mail SAN, key usage, document-signing EKU, validity, and whether the
  signing moment fell inside it), whether the signature covers the whole file,
  and what the updates after it did - nothing, filled form fields and further
  signatures, or a page that draws something else now. ⚠ Deliberately **not**
  gated on state: the app only knows what it signed itself, and the document
  that most needs checking is the one it did not.
- **A receipt for every signer**, handed over the moment their signature lands -
  a certs-only PKCS#7 `.p7b`, a `.pem`, and a plain-text summary with both
  SHA-256 fingerprints. Somebody with an account gets it as a notification
  that opens Verify; anybody else gets a share **of its own** (30 days), never
  the signing link, which the last signature revokes. Every artefact says, in
  the reader's language, that it is an *identity receipt, not a signing
  capability*.
- **Delivery of the finished document.** When the last signature lands, one
  filex share of the signed file is opened and mailed to every signer with an
  address; whether it carries a PIN is the requester's choice in the wizard.
- **Named boxes.** Every box carries a name - offered by kind, typed by
  whoever places it - shown in the editor, in the signer's form, in the PDF
  field's `/TU`, in the audit trail and in every refusal.
- **Identities without an e-mail address.** A signer is a name, an address, or
  both. Somebody with no address still gets a link and a PIN, shown to the
  requester to hand over. The certificate's subject is the name when there is
  one, the address otherwise, and the e-mail SAN is written only when there is
  an address.
- **Optional RFC 3161 time stamping**, off by default, with two admin
  settings. `internal/hostnet` routes Go's default HTTP transport through
  filex's `http_request`, so `digitorus/pdfsign` reaches the authority without
  a fork. The request carries only the signature's digest and a nonce. An
  unreachable authority costs nobody their signature: the signature is made
  without a stamp and the report says so.
- **The signing authorities are read as a bundle** (`ca_certs_pem`), retired
  ones included, so a rotation does not make the past read as untrusted. The
  Verify screen lists them with their fingerprints.
- **A Signatures screen that lists**, over the host's new `state_list`: what
  is waiting for my signature, what I asked for, what I have signed, and every
  request in the installation for an administrator. Only this plugin's own
  state rows, only files the asker may already see, deleted files never. A
  document signed without a request is found through the `signed` badge, whose
  value now names the signers with an account here. Each row carries the
  document's own name and the action its section is for, and clicking it
  **goes there** (`Surface.Open`): Sign opens the `fill` action, Follow the
  `status` view, Verify the `verify` action. The host checks the screen is
  ours and drops the link when the asker may not see the file.
- **`scripts/build.sh` runs `gofmt -l`, `go vet` and `go test ./...` before
  building**, and refuses to produce a module when any of them fails.

##### Changed
- **Both sides are the same shell, step by step.** *Sign…* is three steps
  (place the boxes → sign → where it goes), *Request signatures…* is eight
  (signers → order → the boxes → place them → time → while it is open → when
  it is done → review), and a signer's screen is three (what is asked of me → fill in the
  named boxes, with the pad in the row it belongs to → see the document as it
  will be and approve). One question per step, at most one primary button plus
  Back.
- **The boxes get a step of their own**, with the document and nothing else on
  it, so the editor fills the viewport.
- **No dropdowns and no folded sections anywhere.** A `select` is drawn as a
  row of choice buttons, and `Field.Advanced` is gone from the contract.
- **A form may not present a contradiction.** `show_when` / `required_when`
  everywhere they belong - the file name is shown, and required, only when the
  signed document is a new file. ⚠ This was Burak's own finding: v0.2.0 asked
  for a name above a choice that would never use it.
- **A date is a field type, not a text rule.** The `date` rule on text boxes
  is removed; a box placed by an older build becomes a date box on read.
- **Typed values are written as real AcroForm fields, not drawn on the page.**
  They are created once before anything of ours is signed, and every signature
  after that only writes its own field values and appearance streams. A filled
  field is locked read-only. No `/NeedAppearances` (the appearance is written
  here, with the embedded face) and no DocMDP (pdfsign rewrites `/Annots` for
  every signature, so certification would make our own second signature look
  like a violation of our own first one).
- **Leaf certificates last ten years** instead of thirty days. A verifier
  checks against the clock it runs with, so a short-lived leaf made good
  signatures read as expired a month later; the key is destroyed seconds after
  the signature, so the horizon carries no key risk.
- **The signer's certificate is kept with the document** (`cert:<signer id>`),
  so a receipt can be given again instead of existing for one instant.
- **The home screen lists requests** - waiting for me, asked by me, signed by
  me, and every request for an administrator - instead of explaining the flows
  and nothing else.
- **`SignInfo.CACertsPEM` is read as a field.** The reflection fallback that
  carried the older SDK generation is gone.
- **"Identity", not "e-mail", everywhere a person is named**: the signer list,
  the status panel, the audit trail, the receipt and the verification report.
  An empty half of an identity is never printed.
- **`public_page_*` became `share_*`**: a surface an outsider opens is a real
  filex share, with one revoke list, one expiry policy and one PIN
  implementation.
- **The manifest declares `languages: ["en", "tr"]`**, and the test suite
  refuses a build where any string a screen can show is missing one, or where
  a form label does not change with the call's language. ⚠ This was the
  "Çiz / Yaz / Yükle under an English heading" bug.

##### Fixed
- A second signature no longer makes the first one read as *the document
  changed after it was signed* - the criterion this round was measured by, and
  a test asserts it.
- The verification trust pool was built and then not handed to the verifier;
  every signature read as untrusted. Caught by the new end-to-end test.
- An empty table cell rendered as a `Text` with two empty strings, which the
  install-time language check would have refused.

#### Milestone 2 (built as "0.2.0", never published)

Everything the first round of use asked for: the screens got out of the
dialog, the document got fillable fields, and a request got the options a
real one needs.

##### Added
- **Full-page screens.** *Sign…*, *Request signatures…* and the new
  *Sign / Fill* open as pages in a new tab (`placement: "page"`) instead of
  a dialog, so the document is readable beside the form and there is no
  scroll inside a scroll. Only the short confirmations stay small.
- **Sign / Fill in the right-click menu**, beside the normal actions rather
  than instead of them: the request writes a value-free `pending` state key,
  so *Sign / Fill* is offered on exactly the documents that wait for a
  signature and *Request signatures…* only on the ones that do not
  (`applies.state` / `applies.no_state`).
- **Signers of this instance sign in the app.** They get no public link at
  all: a notification addressed to them (`to_user_id`) with a target that
  opens the signing screen on the document. No mail is sent to them; filex
  delivers to their own channels if they asked for it.
- **Fillable fields with rules**: `text`, `date` and `checkbox` beside
  signature and initials. A text box can ask for free text, numbers only, a
  date in one of three layouts (`DD.MM.YYYY`, `MM/DD/YYYY`, `YYYY-MM-DD`) or
  an e-mail address, and can set a minimum and maximum length. Every rule is
  checked on the screen (immediate, bilingual, by the box's label) and again
  in the job.
- **Five embedded fonts** - Caveat, Dancing Script, Homemade Apple
  (handwriting) and Inter, Source Serif 4 (official) - subset to Latin,
  Latin-1 and Latin Extended-A, so Turkish characters survive wherever the
  file is opened. A letter a face does not have (Homemade Apple has no Ş, ğ
  or İ) is borrowed from the nearest face that does, within the same line.
- **The plugin stamps the values into the PDF itself** (`internal/pdfdoc`):
  an incremental update that wraps the page's own content in `q … Q`,
  appends the stamp, merges the fonts into the page's effective resources
  and writes a cross reference in the file's own dialect (classic table or
  XRef stream). Text is real embedded text (Type0 / Identity-H with a
  ToUnicode map), clipped to its box, upright on `/Rotate 90 / 180 / 270`
  pages, auto-fitted and ellipsised when it cannot fit.
- **Office documents**: ODT, DOCX, XLSX, PPTX, RTF, TXT and friends are
  converted through `engines:libreoffice` and the PDF is signed. The screen
  also offers *Convert to PDF only*, which is what a request needs. Without
  LibreOffice the refusal says so and what to do instead.
- **Output choice per job** (`job.output`): a new version of the same file,
  a new file beside it, or a name you type (`{stem}`, `{ext}`, `{name}`).
  The default sibling name is now `{stem}-signed{ext}`.
- **Signing order**: everybody at once, or one after another - only the
  signer whose turn it is can act, and the next is invited the moment the
  previous one signs.
- **Deadline**, which caps the links' and the lock's lifetime.
- **Reminders**: the panel says when a signer has been silent longer than
  the requester was willing to wait. Offered, never sent behind their back -
  filex gives a plugin no timer.
- **Declining**: a signer may refuse with a reason; the request closes for
  everyone, every link stops working and the requester is told loudly.
- **File lock** (`files:lock`): while signatures are collected the document
  is read-only for everyone, administrators included, and only this app's
  own signing writes into it. The lock is lifted by the same code path every
  ending of the flow goes through, and carries the request's own TTL so an
  abandoned request thaws by itself.
- **Audit trail PDF**: who was asked, what each of them did, when, from
  where, with which certificate, and every value that was filled in. Written
  beside the document when the request completes with a new-file output, and
  one click in the panel otherwise.
- **Close the expired request** in the panel, which is also what releases
  the file.

##### Changed
- `apply` is `hidden`: it is the second half of a flow and never a menu row.
- The envelope is schema **2** (rules, fonts, order, deadline, reminders,
  declining, lock, output). Records from 0.1.0 still load.
- The wizard has four steps (signers · fields · options · review).
- `internal/pdfsig` keeps only the signature widget's appearance; everything
  else the plugin draws now goes through `internal/pdfdoc`.
- A signer with more than one signature box gets the widget in the first and
  their name in a handwriting face in the others.

##### Fixed
- The build is reproducible: `-buildvcs=false` (Go was stamping the git
  commit into the module, so the hash changed with every commit) and
  `wasm.sha256` blanked before the build (the manifest is embedded, so a
  stamped manifest changed the very hash it recorded). CI refuses a tree
  whose stamped hash is not the one it builds.

#### Milestone 1 (built as "0.1.0", never published)

The MVP (M1) of the e-signature app for filex's app-plugin platform.

##### Added
- **Sign…** - sign a PDF yourself: place a signature box, draw / type /
  upload your signature, get `<name>.signed.pdf` next to the original.
- **Request signatures…** - invite one or more people (users of the
  instance or outside e-mail addresses). Each signer gets a private page
  (`/p/<token>`, PIN per signer or none, 1-90 days) with only their boxes
  active; submissions are applied as incremental updates on the document
  (each one a new version), the requester is notified at every step and
  the page is revoked once used.
- **Signatures** section in the file's details panel: signers, states,
  Remind / Show link row actions, Cancel.
- **e-Signature** home screen explaining the flows and the trust model.
- PAdES-B signatures: `/ETSI.CAdES.detached`, ECDSA P-256 / SHA-256,
  signing-certificate-v2, one fresh certificate per signature from the
  instance's own CA (`cert_issue` → sign → `key_destroy`).
- Visible appearance: the drawing fitted above the signer's name and the
  date, rasterised with the Go Regular font (Turkish glyphs render
  correctly).
- Intake refusals with actionable words: not a PDF, encrypted, XFA.
- Geometry twin of filex's `fracToPdf` for /Rotate 0/90/180/270 and CropBox
  offsets, unit-tested against hand-computed values.
- Host-side test harness (`internal/host.Fake` + throw-away CA) covering
  the whole flow without a wasm runtime.

[Unreleased]: https://github.com/BRF-Tech/filex-sign/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/BRF-Tech/filex-sign/releases/tag/v0.3.0
[0.2.0]: https://github.com/BRF-Tech/filex-sign/releases/tag/v0.2.0
[0.1.1]: https://github.com/BRF-Tech/filex-sign/releases/tag/v0.1.1
[0.1.0]: https://github.com/BRF-Tech/filex-sign/releases/tag/v0.1.0
