# Rendering — and editing — Markdown To documents on Hub pages

Status: implemented, not yet deployed. Verified against `internal/hub/mdtoview.go`,
`internal/hub/sharelink.go`, `internal/hub/web.go`, `internal/hub/assets/mdto.html`,
and `internal/hub/assets/mdto/` on 2026-08-09, plus a headless-Chrome run on a
local Hub: a real pointer drag on a kanban board, a checkbox on a todo list, a
band move on a backlog, a 412 raced against the save API, and a share link of the
same file.

A file whose frontmatter carries `markdownto: <spec>@<version>` is an ordinary
markdown note to this Hub — it commits, diffs, clones, and reads as one. This is
the second way to look at it: the **real Markdown To renderers**, run in the
reader's browser over the file's exact bytes, on authenticated instance file
pages and on anonymous share links.

There are two of those views, and one question decides which you get:

| | may this viewer write? | what the frame is | what it can do |
| --- | --- | --- | --- |
| **read-only** | no | `sandbox="allow-downloads"` | nothing runs; the document is a picture of the file |
| **live** | yes | `sandbox="allow-scripts allow-downloads"` | the board runs, and every mutation commits |

Nothing else changes between them. Same page, same pinned engine, same bytes,
same escape hatches.

## The shape

The Hub stays renderer-ignorant, which is the whole design (the integration
contract's §3, in the markdownto repo at `agentsfs/product/hub-contract.md`).
It knows exactly one frontmatter key. Everything else is the engine's business —
including which specs are draggable, which is decided in the browser and is not
written down anywhere in Go.

```
GET /{user}/{repo}/mdto/{path}     authenticated, same read gate as /blob
PUT /{user}/{repo}/mdto/{path}     the board's edits, back; write access + If-Match
GET /s/{token}                     anonymous; the rendered view IS the share view
GET /s/{token}?view=markdown       the plain-markdown escape hatch
GET /s/{token}?download=1          the file, as an attachment
```

The GET serves one thin page (`assets/mdto.html`) that carries:

1. the file's bytes, base64-encoded in a `data-b64` attribute — the encoding is
   not obfuscation, it is the shortest path with no markup escaping in it, so
   what the parser sees is byte-identical to what a `git clone` gets;
2. two same-origin `<script>` tags with `integrity=`: the **pinned** bundle
   (`assets/mdto/mdto.js`) and the loader (`assets/mdto/view.js`);
3. an empty, sandboxed `<iframe>`;
4. **and, only for a viewer with write access**, a `#mdto-live` element carrying
   the widened sandbox literal, the save URL, and the file's hash.

`view.js` decodes the bytes, calls `MDTO.parse`, and puts the resulting
standalone document into the iframe through `srcdoc`. It picks the renderer the
playground picks:

| The file | Read-only view | Live view (write access) |
| --- | --- | --- |
| parses with any `error` diagnostic | `renderDiagnosticsHtml` — the validation report | the same report; a file that does not parse is not a board |
| `kanban@0.1` | `renderHtml` — a **static** board | `renderBoard` — the live board |
| `todo@0.1` | `renderHtml` — sections of checklists | `renderBoard` — live checkboxes |
| `backlog@0.1` | `renderHtml` — the ladder of bands | `renderBoard` — a board of bands |
| `audio@0.1` | `renderHtml` → the manuscript | the same manuscript; there is no live view of a script |
| a spec this bundle has no view for | `renderHtml` → its "valid, not drawn" page | the same page |

The validation report is not an error state to hide: an honest report **is** the
view of a file that does not parse, and it renders down the same sandboxed path
as everything else — with the *read-only* sandbox, because there is nothing to
run.

A file with no envelope is untouched everywhere — no offer on the file page, and
a share link renders it exactly as it always did.

### Which specs are live

`view.js` asks one question, and it is the playground's own: no error
diagnostics, and the result carries a `document` or a `backlog` IR. That covers
`kanban`, `todo` and `backlog` today; `audio` puts its IR in `result.audio` and
so stays a manuscript. **No spec name appears in the Hub**, in Go or in the
loader, so a spec the bundle grows a board for tomorrow gets one here on the next
re-vendor with nothing to edit.

The live board is rendered with `chrome: 'embedded'` — the option that drops the
masthead, the spec heading, the toolbar and the footer a host page already draws.
The Hub's own bar carries the file's name, the spec it declares, and the ways
out. The read-only rendering keeps `chrome: 'full'`, unchanged, so one document
looks the same to every reader of it.

## Sandboxing

Both literals are authored in the HTML, and `view.js` never composes one:

```html
<!-- every page starts here -->
<iframe class="mdto-stage" id="mdto-stage" sandbox="allow-downloads"></iframe>

<!-- and only a page whose viewer may write carries this -->
<div id="mdto-live" data-sandbox="allow-scripts allow-downloads" …>
```

The loader swaps the frame element for one wearing `data-sandbox` at the moment —
and only at the moment — it mounts a board that runs. A page without
`#mdto-live` has no widened literal to apply, no URL to save to and no hash to
hold, so no path through the loader can produce a writable board on it. The
element is replaced rather than edited because a `sandbox` attribute is read when
the document loads; that is the playground's reason too.

`allow-same-origin` appears on neither, and that is the load-bearing part. The
frame runs at an **opaque origin**: it cannot read this page's DOM, this Hub's
cookies, or its storage. Verified in a browser, inside a running board —
`origin` is `"null"`, and `parent.document`, `document.cookie` and
`localStorage` each throw `SecurityError`. The only channel out is one
`postMessage` shape, from one window, checked below.

This is the playground's production-proven posture, adopted on purpose rather
than inherited: `^markdownto-writeback` in the agentsfs backlog records the
decision, including that no separate content domain is needed for it.

### The two policies

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
4. `go test ./internal/hub/ -run Mdto`.
5. Load a conforming file in a browser, **and drag something**, before deploying.

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

## The escape hatches

The rendering never captures the file. Every rendering page carries, in its own
chrome, **View as Markdown**, **Download .md**, and **Open in playground**, and
the rendered document adds its own download and source disclosure. On the file
page the ordinary markdown rendering is still right there below the offer — the
Markdown To view is an offer, not a replacement.

"Open in playground" is a plain link to `https://markdownto.ai/app/`: the
playground reads no hash or query today, so a deep link would silently drop the
file. When it grows the contract's `#hub=owner/instance/path` form, this is the
one constant to change (`playgroundURL` in `mdtoview.go`).

## Where the pieces live

| File | What it holds |
| --- | --- |
| `internal/hub/mdtoview.go` | detection, the pinned assets, both CSPs, the authed handler, the save |
| `internal/hub/sharelink.go` | `serveSharedMdto`, `?view=markdown`, `?download=1` (read-only, always) |
| `internal/hub/web.go` | the `/mdto/` route, the file page's offer, immutable asset caching |
| `internal/hub/assets/mdto.html` | the thin page: both sandbox literals, the save chrome, the conflict panel |
| `internal/hub/assets/mdto/` | the vendored bundle, its manifest, and `view.js` |
| `internal/hub/mdtoview_test.go` | the pin, the sandbox assertions, who gets which view, the save |

Detection reuses `readFileMeta`/`envelopeKey` from the save API
([save-api.md](save-api.md)), so the Hub can never disagree with itself about
which files are conforming documents. One consequence worth knowing: a file with
a **byte-order mark** before its opening `---` has no frontmatter as far as
`core` is concerned, so it declares nothing and gets no Markdown To view —
`afs`, the save API, and this view all agree on that.
