# Rendering — and editing — Markdown To documents on Hub pages

Status: deployed production behavior in Fly release 112, verified 2026-08-13.
Implementation verified against `internal/hub/mdtoview.go`,
`internal/hub/sharelink.go`, `internal/hub/web.go`, `internal/hub/assets/mdto.html`,
`internal/hub/assets/file.html`, and `internal/hub/assets/mdto/` on 2026-08-09,
plus a headless-Chrome run on a local Hub: a real pointer drag on a kanban board,
a checkbox on a todo list, a band move on a backlog, a 412 raced against the save
API, and a share link of the same file — and, on the same day, the same drag and
the same checkbox performed on the board **embedded in an ordinary note page**,
each landing a commit.

The **guided** variant and the source handshake below were added on 2026-09-21
and verified the same day against a local Hub with headless Chrome, anonymously,
on a public instance: the reader mounted its article (98 blocks, 33,791
characters), listed 3 chapters and 6 passages, reported `location.origin` as
`"null"` with `parent.document` and `document.cookie` each throwing
`SecurityError`, logged zero CSP violations, advanced a beat through its own
Next control, and spoke all six beats in authored order through
`speechSynthesis`.

A file whose frontmatter carries `markdownto: <spec>@<version>` is an ordinary
markdown note to this Hub — it commits, diffs, clones, and reads as one. This is
the second way to look at it: the **real Markdown To renderers**, run in the
reader's browser over the file's exact bytes.

It is not a detour any more. **A conforming file's own note page renders it as
what it declares**, inside the Hub's normal chrome, by default; the markdown is
one link away and never leaves the page. The full-page view and the share link
are the same rendering somewhere else.

There are three variants of that rendering. Two questions decide which you get,
and the second one is asked only of one spec:

| | may this viewer write? | what the frame is | what it can do |
| --- | --- | --- | --- |
| **read-only** | no | `sandbox="allow-downloads"` | nothing runs; the document is a picture of the file |
| **guided** | no — *and it does not matter* | `sandbox="allow-scripts allow-downloads"` | the guided reader runs, and nothing is ever saved |
| **live** | yes | `sandbox="allow-scripts allow-downloads"` | the board runs, and every mutation commits |

Nothing else changes between them. Same page, same pinned engine, same bytes,
same escape hatches.

The **guided** variant exists because the read-only variant is right for a board
and wrong for a reader. A board's static render loses nothing — it is a picture
of the file, and a picture of a board is a board you cannot drag. A
`guided-narration@0.1` manuscript has no static render to fall back to: its view
IS a script, a ~300 KB player inlined into the rendered document, so the picture
of it is a dead toolbar above an empty paste box. The one spec whose entire
point is being read was the one spec nobody could read.

So a guided page runs its script for **every** viewer, including an anonymous
one on a public instance, and carries no save URL, no hash and no conflict panel
at all. "Read-only" means *saves nothing* there, not *runs nothing* — and the
policy below is what makes that a browser-enforced statement rather than a
promise about the renderer. `allow-same-origin` is absent from all three
literals, so every frame is an opaque origin in every variant.

A writer on a guided manuscript is unchanged: the live page, the live policy,
the live chrome, and now a frame that runs. No save loop appears for them
either, because `view.js` asks the engine whether the result is a board and a
guided narration never is.

## The three surfaces

One rendering page (`assets/mdto.html`) is served in three places. Only its
chrome differs; the variant above is decided the same way in all three.

| Surface | URL | Chrome | Who gets **live** |
| --- | --- | --- | --- |
| **The note page, inline** | `/{user}/{repo}/blob/{path}` | the Hub's own: masthead, file tree, note context — the rendering page is framed inside it with its own bar off (`?embed=1`) | write access |
| **The full page** | `/{user}/{repo}/mdto/{path}` | its own bar: crumbs back to the Hub, the file, the spec, the escape hatches | write access |
| **The share link** | `/s/{token}` | its own bar, with **no hub navigation at all** | nobody, ever |

Per viewer, on one conforming file:

| Viewer | Note page | Full page | Share link |
| --- | --- | --- | --- |
| owner / write collaborator | inline, **live** | **live** | read-only |
| read collaborator | inline, read-only | read-only | read-only |
| anonymous, public instance | inline, read-only | read-only | read-only |
| anonymous, private instance | login | login | read-only (the token is the authorization) |
| no JavaScript | the markdown | the markdown, named | the markdown, named |

On a `guided-narration@0.1` manuscript every **read-only** cell in that table
reads **guided** instead, on the note page and the full page alike — the frame
runs. The share-link column does not move: a share link has never been given a
frame that runs script, and giving one to an anonymous holder of a token is a
separate decision from giving one to a reader the access gate already let
through. A share link of a guided manuscript is still the picture, and is still
one click from the markdown.

A share link never sets `Live` and never carries a crumb: its reader has no
session and no instance to go back to, which is the same reason
`assets/share.html` draws no masthead.

## The note page's inline view

The note page **frames the file's own rendering page** rather than repeating its
machinery:

```html
<div class="note-mdto-stage">
  <iframe src="/{user}/{repo}/mdto/{path}?embed=1" loading="lazy"></iframe>
</div>
```

That nesting is not incidental — it is what makes the whole thing possible, for
one reason: **a `srcdoc` frame inherits its embedder's CSP.** The rendered
document must be held to `default-src 'none'; connect-src 'none'; img-src 'self'
data:`. The note page cannot be: it runs `app.js` (pjax fetches, the agent dock,
review comments), loads repo images, and frames the `/render/` HTML preview.
Embedding the document *directly* into the note page would silently hand it the
note page's policy — remote image loads out of a hostile file for a reader, and
network reach for a writer's frame, both of which the read-only posture exists to
prevent. One frame down, the document keeps the policy `mdtoview.go` sets for it
and the note page keeps its own. Two documents, two policies, no compromise.

Everything else falls out of that:

- **The CSP delta on the note page is zero.** It ships no CSP today and ships
  none now. The security properties of the rendering are unchanged because the
  rendering is unchanged — the same page, the same headers, the same sandbox
  literals, byte for byte.
- **The security boundary is where it always was.** Verified inside the nested
  embedding, from the board's own frame: `location.origin` is `"null"`, and
  `parent.document` and `document.cookie` each throw `SecurityError`. The board
  cannot read the rendering page, let alone the note page around it.
- **The outer frame is deliberately not sandboxed.** It is a first-party page on
  this origin that does its own sandboxing inside. Sandboxing it would give it an
  opaque origin and break the one thing it exists for: the board's same-origin,
  cookie-credentialed `PUT` (`mdtoSameOrigin` would see `Origin: null` and refuse
  it, correctly).
- **The engine is the frame's.** The ~750 KB bundle is fetched by the embedded
  document, so it is fetched only on pages that render one of these files and
  never on an ordinary note. It is content-addressed and served
  `immutable`+SRI-pinned, so the note page, the full page and the share link of
  the same file all hit one cache entry.

### The toggle, and no JavaScript

`?view=markdown` on the note page serves the markdown rendering instead, and the
markdown view links back. It is a **link and nothing else** — no stored
preference, per reader or per file, so a note linked from anywhere opens the same
way for everybody. `mdtoModeHref` carries the rest of the URL across the toggle
in both directions, so flipping the view does not close the commit diff open
beside it.

The markdown rendering is **built and printed on the page either way**, hidden by
a stylesheet while the board is up:

```html
<style>.note-mdto-raw { display: none; }</style>
<noscript><style>
  .note-mdto-stage, .note-mdto-modes { display: none; }
  .note-mdto-raw { display: block; }
</style></noscript>
```

A browser with scripting off never parses the `<noscript>` element's contents as
markup — so with JavaScript the rule does not exist, and without it the rule
wins, and the reader gets exactly the note page they always got plus a line
explaining why. Confirmed with `Emulation.setScriptExecutionDisabled`: the stage
and the mode strip compute to `display: none`, the article to `display: block`,
and the pinned bundle is never requested. (The chrome-less page itself still is —
one small HTML response, no engine, nothing drawn. `loading="lazy"` does not stop
a hidden iframe in Chrome; the cost is small enough to accept rather than solve
with script.)

## The shape

The Hub stays renderer-ignorant, which is the whole design (the integration
contract's §3, in the markdownto repo at `agentsfs/product/hub-contract.md`).
It knows exactly one frontmatter key. Everything else is the engine's business —
including which specs are draggable, which is decided in the browser and is not
written down anywhere in Go.

```
GET /{user}/{repo}/blob/{path}              the note page; renders the document inline
GET /{user}/{repo}/blob/{path}?view=markdown  … and the markdown instead
GET /{user}/{repo}/mdto/{path}              the full page, same read gate as /blob
GET /{user}/{repo}/mdto/{path}?embed=1      the same page, chrome off, for the frame above
PUT /{user}/{repo}/mdto/{path}              the board's edits, back; write access + If-Match
GET /s/{token}                              anonymous; the rendered view IS the share view
GET /s/{token}?view=markdown                the plain-markdown escape hatch
GET /s/{token}?download=1                   the file, as an attachment
```

`?embed=1` is read as a flag and affects one thing: the page's own bar and footer
are not drawn (nor is their stylesheet, so "the embed has no bar" cannot quietly
stop being true). It does not touch the access gate, the variant, the CSP, the
sandbox literals or the save URL — `data-save` stays the bare route, because the
embed flag is chrome and never part of what the page writes to. An unrecognised
value is simply the full page.

The GET serves one thin page (`assets/mdto.html`) that carries:

1. the file's bytes, base64-encoded in a `data-b64` attribute — the encoding is
   not obfuscation, it is the shortest path with no markup escaping in it, so
   what the parser sees is byte-identical to what a `git clone` gets;
2. two same-origin `<script>` tags with `integrity=`: the **pinned** bundle
   (`assets/mdto/mdto.js`) and the loader (`assets/mdto/view.js`);
3. an empty, sandboxed `<iframe>`;
4. **and, only for a viewer with write access**, a `#mdto-live` element carrying
   the widened sandbox literal, the save URL, and the file's hash;
5. **and, only for a guided narration and then for every viewer of it**, a
   `#mdto-guided` element carrying the reader's sandbox literal and its feature
   delegation — plus, on `#mdto-source`, the article the manuscript names.

`view.js` decodes the bytes, calls `MDTO.parse`, and puts the resulting
standalone document into the iframe through `srcdoc`. It picks the renderer the
playground picks:

| The file | Read-only view | Live view (write access) |
| --- | --- | --- |
| parses with any `error` diagnostic | `renderDiagnosticsHtml` — the validation report | the same report; a file that does not parse is not a board |
| `kanban@0.1` | `renderHtml` — a **static** board | `renderBoard` — the live board |
| `todo@0.1` | `renderHtml` — sections of checklists | `renderBoard` — live checkboxes |
| `backlog@0.1` | `renderHtml` — the ladder of bands | `renderBoard` — a board of bands |
| `narrate@0.1` | `renderHtml` → the manuscript | the same manuscript; there is no live view of a script |
| `guided-narration@0.1` | `renderHtml` → the guided reader, **running**, with the source article handed to it | the same reader, the same article; it is a script, not a board |
| a spec this bundle has no view for | `renderHtml` → its "valid, not drawn" page | the same page |

The validation report is not an error state to hide: an honest report **is** the
view of a file that does not parse, and it renders down the same sandboxed path
as everything else — with the *read-only* sandbox, because there is nothing to
run.

A file with no envelope is untouched everywhere — no frame, no toggle and no
stylesheet on its note page (`?view=markdown` is inert on it), and a share link
renders it exactly as it always did.

### Which specs are live, and which one runs without being live

`view.js` asks one question, and it is the playground's own: no error
diagnostics, and the result carries a `document` or a `backlog` IR. That covers
`kanban`, `todo` and `backlog` today; `audio` puts its IR in `result.audio` and
so stays a manuscript. **Almost no spec name appears in the Hub**, in Go or in
the loader, so a spec the bundle grows a board for tomorrow gets one here on the
next re-vendor with nothing to edit.

`isLive` now also says, out loud, that a `result.guidedNarration` is never live.
The engine gives a guided narration neither IR, so the line changes no behaviour
today — it exists because this is the one spec whose view *runs* without being
writable, and "it runs" must not be allowed to drift into "it saves" on a later
re-vendor. A guided narration is a script, not a board.

**Running** is the second question, and unlike the first it does name a spec.
`isGuidedNarration` (`narrate_artifacts.go`, beside the artifact roots, so every
spec name the Hub knows has one home) decides whether the page carries
`#mdto-guided`. That is a deliberate exception to renderer-ignorance and it is
worth stating why one was needed: the Hub cannot ask the bundle this question
before serving the page, because the bundle runs in the browser and the sandbox
attribute is decided in Go. The alternatives were to widen the sandbox for every
read-only document, which is the thing the read-only variant exists to prevent,
or to let `view.js` compose a literal, which is the thing the two-element design
exists to prevent. Naming one spec is the smallest of the three.

The live board is rendered with `chrome: 'embedded'` — the option that drops the
masthead, the spec heading, the toolbar and the footer a host page already draws.
The Hub's own bar carries the file's name, the spec it declares, and the ways
out. The read-only rendering keeps `chrome: 'full'`, unchanged, so one document
looks the same to every reader of it.

The `?embed=1` page is the same idea one level up: the note page draws the
chrome, so the rendering page does not draw it twice. Both decisions are the
same sentence — *do not print what the host already printed* — applied at the two
places a host exists.

## Sandboxing

All three literals are authored in the HTML, and `view.js` never composes one:

```html
<!-- every page starts here -->
<iframe class="mdto-stage" id="mdto-stage" sandbox="allow-downloads"></iframe>

<!-- only a page whose viewer may write carries this -->
<div id="mdto-live" data-sandbox="allow-scripts allow-downloads" …>

<!-- and only a guided narration carries this, for every viewer of it -->
<div id="mdto-guided" data-sandbox="allow-scripts allow-downloads" data-allow="autoplay">
```

The loader swaps the frame element for one wearing a `data-sandbox` at the
moment — and only at the moment — it mounts a document that runs, and it reads
the literal off `#mdto-live` for a board and off `#mdto-guided` for a reader. A
page with neither element has no widened literal to apply; a page with only
`#mdto-guided` additionally has no URL to save to and no hash to hold, so no
path through the loader can produce a writable board on it. The element is
replaced rather than edited because a `sandbox` attribute is read when the
document loads; that is the playground's reason too.

`data-allow` is the feature delegation, in markup for the same reason: what a
frame is permitted to do should be readable rather than assembled in a string.
`autoplay` is there because the guided reader puts the article in a frame of its
own and starts speaking in it; without the permission arriving from here it has
none to pass on. It is narrower than the playground's own `allow="autoplay *"`,
deliberately. One thing it does **not** delegate is `screen-wake-lock`, which
the player asks for and logs a permissions-policy message about: a sandboxed
reader on a Hub page keeping somebody's screen awake is not a capability worth
granting for a message in a console.

`allow-same-origin` appears on neither, and that is the load-bearing part. The
frame runs at an **opaque origin**: it cannot read this page's DOM, this Hub's
cookies, or its storage. Verified in a browser, inside a running board —
`origin` is `"null"`, and `parent.document`, `document.cookie` and
`localStorage` each throw `SecurityError`. The only channel out is one
`postMessage` shape, from one window, checked below.

This is the playground's production-proven posture, adopted on purpose rather
than inherited: `^markdownto-writeback` in the agentsfs backlog records the
decision, including that no separate content domain is needed for it.

### The three policies

A `srcdoc` frame **inherits** the embedding page's CSP, so the page's policy is
also the document's. The read-only page carries `mdtoCSP`, unchanged:

```
default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline';
img-src 'self' data:; font-src 'self' data:; connect-src 'none';
frame-src 'self'; child-src 'self'; worker-src 'none'; object-src 'none';
base-uri 'none'; form-action 'none'; frame-ancestors 'self'
```

The live page carries `mdtoLiveCSP`, which differs in exactly **two directives**:

- `script-src 'self' 'unsafe-inline'` — because of the frame. The board's patch
  engine is inlined into the rendered document by construction (the bundle
  carries it as a string constant precisely so the frame fetches nothing), and
  with `'self'` alone the board would draw and then sit there dead. What the page
  gives up is stated rather than discovered: it contains no inline `<script>` of
  its own, its two scripts are same-origin and SRI-pinned, and every value it
  prints goes through `html/template`'s contextual escaper — so `'unsafe-inline'`
  buys an attacker nothing they could not already do with markup injection into
  the page, which is the thing actually being prevented.
- `connect-src 'self'` — because this page saves. The frame inherits that too,
  and it is worth being exact about what it means there: an opaque origin's
  request to this Hub carries no cookies and its response is unreadable to it.
  "The rendered document cannot phone home" stays browser-enforced for every
  origin but this one, and the rendered documents contain no network code at all
  — a property `TestMdtoVendoredBundleMatchesManifest` checks on the bundle's
  bytes.

Everything else is identical, `object-src 'none'`, `base-uri 'none'`,
`form-action 'none'` and `frame-ancestors 'self'` included. A headless-Chrome
load of a live board reports zero CSP violations.

The guided page carries `mdtoGuidedCSP`, which differs from `mdtoCSP` in exactly
**three directives** and in nothing else:

- `script-src 'self' 'unsafe-inline'` — the same directive the live policy
  carries, for the same reason and by the same construction. The guided reader
  is inlined into the rendered document as one `<script>` precisely so the frame
  fetches nothing, and a `srcdoc` frame inherits this page's policy, so with
  `'self'` alone the reader would draw its toolbar and then sit there dead. What
  the page itself gives up is identical to what the live page gives up: it
  contains no inline `<script>` of its own, its two scripts are same-origin and
  SRI-pinned, and every value it prints goes through `html/template`'s
  contextual escaper.
- `media-src 'self' blob: data:` — the player decodes speech into an object URL
  and plays it (`URL.createObjectURL` → `new Audio(url)`), and its offline
  export carries recordings as `data:` URIs.
- `img-src 'self' data: blob:` — the source article is rendered inside this same
  policy and may carry either kind of inline image.

`connect-src 'none'` **does not move**, and that is the sentence the whole
variant rests on. Unlike the live page, a guided page has nothing to save and so
has no reason to loosen the one directive that would let its frame speak to this
Hub. The reader contains no `fetch` and no `XMLHttpRequest` — a property of the
vendored bytes, already asserted by
`TestMdtoVendoredBundleMatchesManifest` — so "the rendered document cannot phone
home" stays browser-enforced for every origin, with no exception, while ~300 KB
of somebody's player runs inside it. That is the trade this variant actually
makes: script, yes; network, no; origin, never.

`TestMdtoGuidedRunsForReaders` asserts the delta as a whole and not only
directive by directive — the guided policy must equal `mdtoCSP` with those three
substitutions applied — so a fourth one cannot be added without the diff saying
so. A headless-Chrome load of a guided reader on a local Hub, article mounted
and playing, reports zero CSP violations.

## The guided reader's source handshake

A `guided-narration@0.1` manuscript is a script *about* an article: it names one
in its `source:` frontmatter and its beats point at passages inside it. The spec
is explicit that nothing may go and get it — "the source is a reference, not an
instruction to fetch" — and the reader obeys that whether it wants to or not: it
runs at an opaque origin under `connect-src 'none'`, so it could not fetch one
if the spec allowed it.

That leaves exactly one way for a host to supply the article, and the reader
publishes it. `MDTO.renderHtml(result, { guidedSourceBridge: true })` makes the
rendered reader post `{mdto:'guided-ready', source:<guide.source>}` to `parent`
**as it loads**, and then wait — it draws nothing until it is answered. The host
replies with the article:

```
reader loads ─► {mdto:'guided-ready', source}          ─► view.js
view.js      ─► {mdto:'guided-restore', source, saved:{format:'markdown', text}}
             ─► the reader resolves every beat and mounts the article
```

The Hub's half is two attributes on `#mdto-source`, beside the manuscript's own
bytes and encoded the same way:

```html
<div id="mdto-source" data-b64="<the manuscript>"
     data-guided-source-ref="./article.md"
     data-guided-source-b64="<the article>"></div>
```

`handleMdtoView` resolves `source:` against the manuscript's directory, through
the same `core.FrontmatterValueFromReader` the save API's `readFileMeta` uses —
a real YAML parser, so `source: "./article.md"` resolves and echoes identically
to a bare one. A line scanner would have handed the reader a ref with the quotes
still on it, and the reader would have refused the reply and waited forever.

Everything else is refused, and before `path.Join` can normalise anything away:
a `..` segment, an absolute path, a scheme, a query, a fragment, a backslash, a
control character, a non-`.md` name, a sibling past `maxMdtoBytes`, a blob that
is not valid UTF-8. An `https://` source is **valid** per the spec and names
nothing in this repository, so it is treated exactly like an absent one — the
Hub reads a committed blob at `defaultRef` for a viewer the read gate already
let through, and it does not become a fetcher because a string looked like a
URL. `source:` is a value in a file anybody with write access can edit; it names
an article beside the manuscript or it names nothing.

### Two rules that are the whole of the design

**The listener goes in before the frame does.** The reader posts on load, so
installing the listener after mounting is a race the page loses silently — the
reader sits on an empty paste box and nothing says why. `view.js` installs it
first, for the same reason `#mdto-live`'s absence is the read-only gate: the
failure has to be structurally impossible, not merely unlikely.

**The bridge is asked for only when there is an article.** Every refusal above
lands in the same place: no attributes, no `guidedSourceBridge`, so the reader
never announces itself and never waits — it draws its own paste/open form the
moment it loads, exactly as it does in the playground. A host that sets the flag
and then cannot answer leaves the reader permanently empty, and that is the one
failure mode this arrangement is shaped to make unreachable rather than rare.

The reply is guarded by the writeback loop's first two checks, for its reasons:
`event.source` identity, because the frame is an opaque origin and
`event.origin` is the string `"null"` and worth nothing as a test; and the
source string, so a message about some other guide is not answered with this
one's bytes. The target origin is `"*"`, because there is no other value that
reaches an opaque origin — what travels is an article this Hub served to this
reader in the same response, into a frame this page created and holds the only
reference to.

The reader's other messages reach no listener at all: `guided-source` as it
loads an article, and `guided-auth`/`guided-audio` when it probes for lifelike
voices. That is the same deliberate silence `mdto:'key'` gets from the writeback
loop. There is **no Hub-authenticated speech on these pages and that is
deliberate**; the reader's health probe goes unanswered and it falls back to the
browser's own voice, which is the state it is in with no host at all.

### The recording, and why this page reads it instead of the reader

markdownto 0.3.1 grew the field that phase was waiting for. `guided-restore`'s
`saved` now takes an optional `recording`:

```
recording: { version: 1, voice, audio: [
  { text,               // the beat's narration, whitespace-collapsed: the match key
    audioBase64 | url,   // the bytes, or an address for the reader page to fetch
    mimeType, durationMs } ] }   // durationMs must be > 0
```

The reader answers `{mdto:'guided-recording-ready', source, beats}` or
`{mdto:'guided-recording-refused', source, reason}`, hides its sign-in, disables
**Prepare offline** — there is nothing left to sign in for — and speaks any beat
the recording misses in the computer voice, so coverage need not be complete.
`guided-narration-audio@0.1`, which `mdto guided-narration produce` already
writes beside every recording, is the natural source: it carries each beat's
collapsed narration verbatim beside that beat's file and measured duration.

**The bytes travel as `audioBase64`, read by this page.** The contract also
allows an entry naming a `url`, which the *reader page* retrieves for itself, and
that form is deliberately not used: the reader page is the sandboxed frame, and
what it may retrieve is governed by the frame half of the guided policy, which
this variant keeps as narrow as it has always been. `view.js` runs first-party on
the Hub's own origin with the viewer's session, so it does the reading and hands
over bytes. The recording is identical either way.

Go stays renderer-ignorant. It contributes a pointer and a label, on
`#mdto-guided`:

- `data-guided-audio` — the `/raw/` URL of the manifest's `beats.path`, the
  `guided-narration-audio@0.1` index.
- `data-guided-voice` — the manifest's `generation.voice`, so the reader's status
  line names the voice the strip is already naming.

Both appear **only when the recording is current for these exact bytes** — the
same `source.hash` rule the strip uses, and nothing else. The strip shows a stale
recording because a person can decide for themselves that an older reading is
worth hearing; a reader cannot, and would speak last week's sentences over
today's beats with nothing on screen to say so.

The spec says the `beats` path is "a path the Hub's validator ignores and the
player reads", and that is its exact standing in `narrate_artifacts.go`: it is
read *after* the manifest has been accepted on its other fields, so a missing or
malformed index still leaves a valid recording with a playable strip. Ignoring is
not trusting, though — the reader is handed this URL and everything beside it is
read — so `narrateBeatsHref` holds it to the rule the joined MP3 is held to: that
exact name, in that recording's own version directory, present and non-empty.
Anything else yields `""`, and the page loses its reader's voice and nothing
else.

`view.js` then reads the index, refuses anything not declaring
`guided-narration-audio@0.1`, turns each beat into a `/raw/` request, reads
**six at a time**, base64-encodes, and sends one `guided-restore` carrying the
result in index order. Every failure is local by construction, because the one
unacceptable outcome is a restore that never arrives — a reader told to wait by a
host that never answers sits empty forever:

| What fails | What it costs |
|---|---|
| One beat's file | That beat, read in the computer voice |
| A malformed or unreadable index | The recording; the article is still restored |
| The whole thing taking too long | Abandoned at 30 s; whatever arrived is sent |

The reader's own verdict comes back on `data-guided-recording`
(`ready` \| `refused`), with `data-guided-beats` or `data-guided-refused`, and in
the mode chip beside the file's name. It is the only way anything outside an
opaque origin learns what the player decided, and `refused` in particular — this
page built something the reader would not take, on a page that still reads
perfectly well — would otherwise be silent.

**Status: built and proven, not yet reachable on a Hub page.** A `srcdoc` frame
runs under its embedder's policy, which is the fact the three frame directives
above rest on, and it cuts the other way here: `view.js` reads the index under
the *page's* `connect-src`, which on a guided page is `'none'`. A headless-Chrome
load of a real guided page on a local Hub confirms it — the request goes to the
right URL and the browser refuses it, and the fallback then behaves exactly as
designed, with `guided-restore` still sent and the article still mounted. So the
page degrades rather than breaks, and the feed stays inert until that directive
is settled. It is a security decision about the guided policy and belongs to
whoever owns that policy; see the review note on the `guided-recording-feed`
branch.

### The one thing markdown lost — corrected upstream, 2026-09-21

The 0.3.1 re-vendor carries markdownto `d6558cc`, which reads a soft line break
as the space it is. The live example below now mounts whole: a headless-Chrome
load of `cloudwindow-architecture.guided-narration.md` against the markdown
import of its own article reports **54 passages · 12 chapters · Ready to play**,
where the previous bundle failed 19 of those 54. Nothing in this repository was
edited to achieve it, which is what the entry below predicted. The rest of this
section is kept as the record of the finding.

Verified in a browser, and worth knowing before authoring a manuscript for this
Hub: the reader's **markdown** importer dropped the whitespace at a soft line
break. A hard-wrapped article — most markdown in a repository — comes back with
its wrapped words joined: "at the\nmoment" becomes `at themoment`, "has
two\nhalves" becomes `has twohalves`. A `[target-quote::]` that spans a wrap
therefore matches nothing, and the reader's source load is all-or-nothing, so
one such beat leaves the whole article unmounted with *N* passages needing
attention.

`agentsfs/cloudwindow-architecture.guided-narration.md` in the CloudWindow repo
is the live example: **19 of its 54 beats** fail against the markdown import of
its own article, and **all 54 resolve** when the identical manuscript in the
identical reader is handed that article's committed `.capture.json` instead.
That isolates it exactly — the bytes the Hub delivers are correct and
byte-identical either way; what differs is how the reader turns them into
blocks.

The Hub does not work around it. Unwrapping the article would mean altering the
file on the way to the parser, which is the one thing this whole page refuses to
do, and deriving blocks in Go would mean the Hub owning a renderer. A capture
committed beside the article would fix it, but there is no convention for where
such a file lives — and a Hub-invented one would be a string no producer writes,
which is the mistake the narration artifact contract already learned once. The
fix belongs upstream in the importer, and a re-vendor after it lands makes every
manuscript work with nothing here to edit.

## The writeback loop

```
drag ─► patch engine (in the frame) ─► postMessage {mdto:'source', source}
     ─► view.js ─► PUT same-origin, If-Match: <held hash> ─► a real commit
     ─► {hash} ─► the next If-Match
```

The bridge is the pinned bundle's, not the Hub's: `site/tools/build-app.mjs` in
the markdownto repo wraps `renderSourceLines` so that every render posts the
exact bytes the board's session is holding. That function is the *wire* and not
only the source panel's renderer — an embedded board has no source panel and
calls it anyway — which is a coupling the markdownto repo states at every call
site because it has already cost one bug there.

### The guard discipline

Three checks stand between a message and a commit, and none is ceremonial:

1. **`event.source` identity, and only that.** The frame is an opaque origin, so
   `event.origin` is the string `"null"` for it and is worth nothing as a test.
2. **The shape** — `{mdto: 'source', source: <string>}` — which keeps every other
   message a page might receive out of the file.
3. **The echo drop.** A fresh frame's bridge has posted nothing yet, so its first
   render posts the bytes it was mounted from: a quotation, not an edit.
   Committing it would put an identical-bytes commit in the log for every board
   anybody ever opened.

The playground has a fourth check — the typist wins — for the race between its
textarea and the frame. There is no editor on this page, so there is no such race
and no such check: the board is the only writer here.

`{mdto: 'key', key: 'Escape'}` is the board's other message, forwarded so a host
can leave a presentation. This page has no present mode and no drawer, so the
message is recognised and **deliberately dropped**. Doing something with a key
nobody pressed here would be the surprise.

Saves are serialized, newest text wins: one PUT in flight, later mutations
coalesced. Sending them in parallel would race the `If-Match` against itself,
each naming a hash the one before had already replaced — and the file the person
is looking at is the last one anyway.

### What the PUT authenticates with

The credential is **the Hub's own session cookie**, resolved into `viewer` by
`serveWeb` before `handleMdtoSave` is reached — the same credential, resolved the
same way, that the note editor's form POST (`handleEdit`) has always used to
commit. That is why the writeback lives on the `/mdto/` route rather than on
`/api/v1`: `/api/v1` is bearer-only on purpose and never sends
`Access-Control-Allow-Credentials`, so an ambient session can never drive it from
another site (see [save-api.md](save-api.md)), and this slice had no business
loosening that.

Because an ambient cookie is the credential, the request has to prove where it
came from: `mdtoSameOrigin` requires an `Origin` header naming this Hub, and
refuses a `Sec-Fetch-Site` that says otherwise. A missing `Origin` is refused
rather than waved through — this route exists for one browser page, and the Hub
has a bearer-authenticated API for everything else. `SameSite=Lax` is the second
layer beneath that, and is what the `/edit` form relies on alone.

Four gates in total, and the last two are what keep this from being a general
file-write API wearing a board's clothes:

1. a session, and **write access** (`apiRepoAccess` — the same capability core
   the git remote, the agent API, the MCP server and `/edit` ask, so "may edit
   the board" and "may edit the note" can never disagree);
2. **same origin**;
3. **still the same document** — the saved bytes must declare the same
   `markdownto:` envelope the file already carries. A board patches the document
   it was drawn from; it never converts one spec into another and never turns a
   conforming file into a plain note;
4. **`If-Match`, always.**

### Conflicts

The hash is `sourceHash` from the save API — sha256 over the file's exact bytes,
byte-identical to the Markdown To patch engine's own `sourceHash`. So the value
the board computed its mutation against is the value git is asked to still be
holding: one conflict model, from the drag to the commit.

| Condition | Answer |
| --- | --- |
| `If-Match` matches | `200`, a real commit, `{hash}` for the next one |
| bytes already committed (a no-op mutation) | `200`, `committed: false`, no empty commit |
| `If-Match` stale, or the file moved/renamed/lost its envelope | `412` with the **current** hash |
| HEAD moved onto this path mid-commit | `412`, same shape |
| no `If-Match` | `428` with the current hash |

A `412` **halts the loop**. The page shows a conflict panel — the file changed
somewhere else, your last move was not saved, and nothing here has overwritten
what is on the Hub — offering three things: reload the board, download the file
as it now stands, and download your unsaved version (a `data:` URI built from the
bytes the board is holding, because this page is the only place they exist). The
board keeps working; the page simply stops saving. There is no retry with
`If-Match: *`, and there never should be.

`git log` records the human as the author and the front door as a trailer:

```
Update board.md

Via: Markdown To board (agentsfs hub)
```

### The diff a drag makes

A drag through the browser's real input pipeline (CDP drag interception, not a
synthesized event) on a local Hub produced this commit:

```diff
 ## Backlog
-- [ ] Draft the announcement
 - [ ] Book the venue
 ## Doing
+- [ ] Draft the announcement ^k1
 - [ ] Write the deck
```

byte-identical to `mdto kanban move "Draft the announcement" Doing --top --pin`
run over the same input. The board pins the card it moves, which is the only
difference from the CLI's bare default, and it is the engine's behaviour rather
than anything the Hub does.

The same drag, performed on the board **inline on the note page** — three
documents deep, through `Input.setInterceptDrags` and a real drop — produced the
same commit, author and trailer:

```
Update board.md

Via: Markdown To board (agentsfs hub)
```

so did a checkbox on an inline `todo@0.1` list. The writeback path is not a
second implementation for the inline case; it is the same `view.js` in the same
page, one frame further in.

## The vendored bundle

`internal/hub/assets/mdto/mdto.js` is a **verbatim copy** of `site/app/mdto.js`
from the markdownto repository — the same artifact markdownto.ai's playground
serves. It is never edited here and never fetched at runtime.

`assets/mdto/VERSION` records where it came from: source repo, path, the commit
it was vendored from, the last commit that changed the bundle bytes, and its
sha256. Those last two answer different questions ("which tree did you vendor?"
and "how old is the engine really?"); both are full object ids, and when the
bundle is taken at the commit that produced it they coincide, as they do today.

Serving:

- URL `/_assets/mdto/mdto.js?v=<first 12 hex of its own sha256>` — content
  addressed, so `Cache-Control: public, max-age=31536000, immutable` is honest
  (`serveAsset` special-cases the `mdto/` prefix; every other asset keeps the
  deploy-wide `assetVersion` and its one-hour window).
- `integrity="sha256-<base64>"`, **derived at init from the embedded bytes**
  (`newMdtoAsset`). Deriving rather than declaring means the attribute and the
  bytes can never drift apart, so a re-vendor cannot ship a page whose script the
  browser refuses.

That the bytes are the *intended* ones is a separate question, and
`assets/mdto/VERSION` + `TestMdtoVendoredBundleMatchesManifest` answers it: edit
the bundle without updating the manifest and the test fails.

### Upgrade procedure (deliberate, never automatic)

Engine upgrades are a version bump the Hub's owner makes on purpose. There is no
auto-update path and there must not be one — a share link minted today should
render the same way next year unless someone decided otherwise.

1. In the markdownto repo, rebuild the bundle: `node site/tools/build-app.mjs`.
2. `cp site/app/mdto.js internal/hub/assets/mdto/mdto.js`.
3. Update `commit`, `bundle-commit`, `sha256`, and `vendored` in
   `internal/hub/assets/mdto/VERSION`.
4. `node scripts/check-mdto-renderer.cjs` and `go test ./internal/hub/ -run Mdto`.
   The Node check executes the actual pinned browser engine against all bundled
   file examples, requires the supported format floor including `podcast@0.1`,
   and checks the native podcast dialogue view. CI and the Docker build both run it.
5. Load a conforming file in a browser, **and drag something**, before deploying.
   Load a guided narration too, **and press play**: the reader's half of its
   handshake is in the bundle, and a re-vendor that renamed `guided-ready` or
   stopped honouring `guidedSourceBridge` would leave every guided page on this
   Hub showing an empty paste box while every Go test stayed green. The needles
   for both are in `TestMdtoVendoredBundleMatchesManifest`; what they cannot
   check is that the reader still *answers*.

Step 5 matters more than it looks. The Go tests pin the manifest and assert, on
the bundle's bytes, that `renderBoard(`, `chrome`, `mdto:"source"` and
`mdto:"key"` are all still in there — because each of those can vanish in a
re-vendor without breaking a single test or throwing a single error in the page.
A board that stopped posting its source would keep drawing, keep dragging, and
quietly stop saving. The API `view.js` depends on is `MDTO.parse`,
`MDTO.renderHtml`, `MDTO.renderDiagnosticsHtml`, `MDTO.renderBoard`, the
`severity` field on diagnostics, and the bridge; a bundle that changed the
rendering half degrades to a message plus the plain-markdown links, never a blank
frame.

### Detecting upstream drift

The `MarkdownTo renderer drift` GitHub workflow compares the pinned bundle hash
with the published playground bundle daily and whenever the Hub bundle changes.
It can also be run manually. A mismatch fails visibly in GitHub Actions; it does
not replace production code. Operators must follow the upgrade procedure above
and deploy the tested Hub release. Run `node scripts/check-mdto-drift.mjs` when
releasing a new MarkdownTo spec or preparing a Hub deployment.

This check detects upstream releases within the scheduled check interval; it
does not make two independent deployments atomic. GitHub Actions notifications
must be enabled for maintainers to receive failures. A newly added format belongs
in the compatibility test floor before deploying its Hub support.

The September 7 podcast incident was an integration gap: the Hub still pinned
the August 12 bundle, which returned MDTO005 for `podcast@0.1`. Re-vendoring the
September 7 bundle restores native manuscript rendering. Generated audio remains
a separate repository artifact; renderer updates do not regenerate speech.

`guided-narration@0.1` repeated the shape exactly on September 20 and is the
reason the floor now names every format the bundle carries rather than a chosen
eleven. The spec landed in markdownto on September 8, one day after the bundle
this Hub had pinned, so a conforming manuscript rendered as a plain note and
nothing in Go was wrong: the Hub does not know spec names, so there was nothing
here to fix and nothing here to notice. Re-vendoring the September 20 bundle is
the entire rendering change. `scripts/check-mdto-renderer.cjs` now asserts the
guided reader, its source intake and its audio strip by class, so a re-vendor
that drops the view fails the check instead of quietly serving a generic report.

Re-vendoring turned out to be only the half of it that the Hub could not have
noticed. The reader rendered correctly and was still unusable, for two reasons
that had nothing to do with which bundle was pinned: its script could not run
under the read-only sandbox, and the article it is forbidden to fetch was not on
the page. Those are the **guided** variant and the source handshake above, and
neither is a rendering change at all.

## The narration artifact strip, for three specs

`internal/hub/narrate_artifacts.go` is the one place spec names appear in Go outside a test, and
it now names three. The strip is one validator for all of them, parameterised by a single thing:

| | `narrate@0.1` | `guided-narration@0.1` | `podcast@0.1` |
| --- | --- | --- | --- |
| artifact root beside the manuscript | `narrate/` | `guided-narration/` | `podcast/` |
| manifest contract | `narrate-artifacts@0.1` | the same | the same |
| layout inside it | `<root>/<basename>/<version>/<basename>.mp3` + `.receipt.json` | the same | the same |
| every other check | shared | shared | shared |

**One contract, not three.** The manifest's shape is identical for every narration kind, so the
contract string describes the shape and the root says which spec it belongs to. That is
markdownto's own convention, not an invention here: its hosted service has written podcast
artifacts under a `podcast/` root declaring `narrate-artifacts@0.1` since September
(`packages/narrate/src/hub-artifacts.ts`, whose `hubArtifactLayout` already takes a `kind`).
A per-spec contract would have been tidier to read and would have rejected every artifact the
producer actually emits.

What keeps two specs' artifacts from being confused for each other is therefore not the contract
string but the manifest's own `source.path`, which must name the manuscript on screen. A manifest
laid out perfectly under the right root and naming a different manuscript is refused —
`TestMdtoNarrationManifestMustNameItsOwnManuscript` fails when that check is removed. The distinct
roots remain worth having, because a manuscript and a guided narration of the same article can sit
in one directory under one basename.

Podcast came along for free and closes a real gap: the producer had been writing those artifacts
for weeks and the Hub read none of them.

One thing beyond the strip had to move with it. `isNarrateAudioUpload`
(`internal/hub/apiv1_files.go`) is the gate that gives a recording the 128 MiB
body limit and converts it to a Git LFS pointer on commit, and it matched a
literal `narrate/` path segment. A guided narration whose page drew a player
would therefore have had its MP3 capped at the ordinary 8 MiB limit and
committed **inline as a binary**, with nothing saying so — a silent ceiling and
a blob in git history. The gate now takes its roots from
`narrationArtifactRoots()`, derived from the specs themselves, so the upload
side and the view side cannot disagree about which paths hold a recording.
`TestNarrationAudioUploadGateCoversEveryArtifactRoot` fails if a root is ever
added to one and not the other.

What did **not** change is who makes the audio. The Hub still validates and
serves a committed artifact and generates nothing: `GenerateHref` is the
playground link, and "No recording yet" is an invitation rather than a job
queue. `mdto` has no `produce` verb for `guided-narration@0.1` — the spec owns
only `resolve`, and states that no CLI operation spends money or generates
audio — so a guided recording is produced by the reader's own Hub-authenticated
cloud narration, or by hand, and committed like any other file. The strip is
about **persisting and serving** it, which is the half the Hub owns.

## The escape hatches

The rendering never captures the file, and it never captures the reader either.

Every rendering page with a chrome of its own carries **View as Markdown**,
**Download .md** and **Open in playground**, and the rendered document adds its
own download and source disclosure. The full page adds the way back into the
Hub, because it owns its whole document and `renderPage` gives it no masthead to
inherit (its CSP admits exactly two scripts): a crumb ladder — **AgentsFS Hub /
owner / instance** — and **← Back to the note**, which is also repeated in the
footer. On that page "View as Markdown" now points at `?view=markdown` rather
than the note page, because the note page is where the board is.

The one page with no chrome of its own is the `?embed=1` frame, and its host
draws all of it: the note page's toolbar keeps **Open as `<spec>`**, the download
menu, **Edit** and **Share**, and the mode strip above the frame carries **View
the Markdown** and **Full view →**. Its `<noscript>` still names the markdown and
the download, for a reader who has no script at all.

On the note page the markdown rendering is not below the frame — it is behind the
link, and printed on the page regardless (see the no-JS stylesheet above). The
Markdown To view is the default, not a capture: one click and one URL take it
away.

"Open in playground" is a plain link to `https://markdownto.ai/app/`: the
playground reads no hash or query today, so a deep link would silently drop the
file. When it grows the contract's `#hub=owner/instance/path` form, this is the
one constant to change (`playgroundURL` in `mdtoview.go`).

## Where the pieces live

| File | What it holds |
| --- | --- |
| `internal/hub/mdtoview.go` | detection, the pinned assets, all three CSPs, the guided source resolver, the authed handler, `?embed=1`, `mdtoModeHref`, the save |
| `internal/hub/sharelink.go` | `serveSharedMdto`, `?view=markdown`, `?download=1` (read-only, always) |
| `internal/hub/web.go` | the `/mdto/` route, the note page's inline hrefs, immutable asset caching |
| `internal/hub/assets/mdto.html` | the thin page: the crumbs, all three sandbox literals, the source article, the save chrome, the conflict panel, the embed |
| `internal/hub/assets/file.html` | the note page's frame, the mode strip, and the `<noscript>` fallback |
| `internal/hub/assets/mdto/` | the vendored bundle, its manifest, and `view.js` |
| `internal/hub/narrate_artifacts.go` | every spec name the Hub knows: the artifact roots, the manifest validator, and `isGuidedNarration` |
| `internal/hub/mdtoview_test.go` | the pin, the sandbox assertions, who gets which view, the inline default, the toggle, the save, the artifact strips, and the guided reader's source and policy |

Detection reuses `readFileMeta`/`envelopeKey` from the save API
([save-api.md](save-api.md)), so the Hub can never disagree with itself about
which files are conforming documents. One consequence worth knowing: a file with
a **byte-order mark** before its opening `---` has no frontmatter as far as
`core` is concerned, so it declares nothing and gets no Markdown To view —
`afs`, the save API, and this view all agree on that.
