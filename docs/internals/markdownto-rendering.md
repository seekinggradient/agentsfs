# Rendering Markdown To documents on Hub pages (read-only)

Status: implemented, not yet deployed. Verified against `internal/hub/mdtoview.go`,
`internal/hub/sharelink.go`, `internal/hub/web.go`, `internal/hub/assets/mdto.html`,
and `internal/hub/assets/mdto/` on 2026-08-09, plus a headless-Chrome load of a
share link on a local Hub.

A file whose frontmatter carries `markdownto: <spec>@<version>` is an ordinary
markdown note to this Hub — it commits, diffs, clones, and reads as one. This is
the second way to look at it: the **real Markdown To renderers**, run in the
reader's browser over the file's exact bytes, on authenticated instance file
pages and on anonymous share links.

Read-only only. The live, draggable kanban board runs script and is deliberately
not here; see [What stays blocked](#what-stays-blocked).

## The shape

The Hub stays renderer-ignorant, which is the whole design (the integration
contract's §3, in the markdownto repo at `agentsfs/product/hub-contract.md`).
It knows exactly one frontmatter key. Everything else is the engine's business.

```
GET /{user}/{repo}/mdto/{path}     authenticated, same read gate as /blob
GET /s/{token}                     anonymous; the rendered view IS the share view
GET /s/{token}?view=markdown       the plain-markdown escape hatch
GET /s/{token}?download=1          the file, as an attachment
```

Each serves one thin page (`assets/mdto.html`) that carries:

1. the file's bytes, base64-encoded in a `data-b64` attribute — the encoding is
   not obfuscation, it is the shortest path with no markup escaping in it, so
   what the parser sees is byte-identical to what a `git clone` gets;
2. two same-origin `<script>` tags with `integrity=`: the **pinned** bundle
   (`assets/mdto/mdto.js`) and a ~100-line loader (`assets/mdto/view.js`);
3. an empty, sandboxed `<iframe>`.

`view.js` decodes the bytes, calls `MDTO.parse`, and puts the resulting
standalone document into the iframe through `srcdoc`. It picks the renderer the
playground picks:

| The file | What renders | Mode chip |
| --- | --- | --- |
| parses with any `error` diagnostic | `renderDiagnosticsHtml` — the validation report | `validation report` |
| `kanban@0.1` | `renderHtml` — a **static** board of columns | `board` |
| `todo@0.1` | `renderHtml` — sections of checklists | `checklist` |
| `audio@0.1` | `renderHtml` → the manuscript view | `manuscript` |
| `backlog@0.1` | `renderHtml` → the ladder of bands | `backlog` |
| a spec this bundle has no view for | `renderHtml` → its "valid, not drawn" page | `document` |

The validation report is not an error state to hide: an honest report **is** the
read-only view of a file that does not parse, and it renders down the same
sandboxed path as everything else.

A file with no envelope is untouched everywhere — no offer on the file page, and
a share link renders it exactly as it always did.

## Sandboxing

```html
<iframe class="mdto-stage" id="mdto-stage" sandbox="allow-downloads"></iframe>
```

That attribute is the security boundary, and it is authored in the HTML: no
`allow-scripts`, no `allow-same-origin`. The frame runs at an **opaque origin
with script disabled entirely**, so nothing a hostile `.md` can say reaches this
page, the Hub's cookies, or the network. `allow-downloads` is only what lets the
rendered document's own "Download the Markdown" link (a `data:` URI) work.

The page carries a CSP (`mdtoCSP` in `mdtoview.go`) — the Hub's first browser
page to carry one at all, and strictly tighter than the unset default:

```
default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline';
img-src 'self' data:; font-src 'self' data:; connect-src 'none';
frame-src 'self'; child-src 'self'; worker-src 'none'; object-src 'none';
base-uri 'none'; form-action 'none'; frame-ancestors 'self'
```

A `srcdoc` frame **inherits** the embedding page's policy, which is why these
details matter more than they look:

- `connect-src 'none'` makes "the rendered document cannot phone home" a
  browser-enforced fact rather than a property of the renderer. (The documents
  are self-contained by construction — inline `<style>`, inline SVG mark, `data:`
  favicon, system fonts — and a dump of a rendered board contains zero `<script>`
  and zero `http(s)://`. This is the belt to that suspenders.)
- `style-src 'unsafe-inline'` is **required** by the inheritance: every rendered
  document carries its stylesheet inline. The thin page's own `<style>` rides on
  it.
- `img-src 'self' data:` covers the `data:` favicon while keeping a remote
  tracking pixel in a hostile file from loading.

### What stays blocked

The live kanban board (`MDTO.renderBoard`) needs `allow-scripts`, which the
playground grants on its own origin. On the Hub that decision is **blocked on the
open `^render-content-domain` backlog item** — the same question `/render` has
been waiting on. Nothing in this slice needs it, and nothing here should be
widened to reach it: the writeback slice (`^markdownto-writeback`) is where the
board belongs, behind that decision.

## The vendored bundle

`internal/hub/assets/mdto/mdto.js` is a **verbatim copy** of `site/app/mdto.js`
from the markdownto repository — the same artifact markdownto.ai's playground
serves. It is never edited here and never fetched at runtime.

`assets/mdto/VERSION` records where it came from: source repo, path, the commit,
the last commit that changed the bundle bytes, and its sha256.

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
   `internal/hub/assets/mdto/VERSION`
   (`git -C <markdownto> rev-parse HEAD`, `shasum -a 256 …/mdto.js`).
4. `go test ./internal/hub/ -run Mdto` — the manifest is asserted against the
   served bytes, and `window.MDTO` is asserted to still be the bundle's surface.
5. Load a conforming file in a browser before deploying. The API `view.js`
   depends on is `MDTO.parse`, `MDTO.renderHtml`, `MDTO.renderDiagnosticsHtml`
   and the `severity` field on diagnostics; a bundle that changed those would
   still pass the Go tests and fail in the page (`view.js` degrades to a message
   plus the plain-markdown links, never a blank frame).

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
| `internal/hub/mdtoview.go` | detection, the pinned assets, the CSP, the authed handler |
| `internal/hub/sharelink.go` | `serveSharedMdto`, `?view=markdown`, `?download=1` |
| `internal/hub/web.go` | the `/mdto/` route, the file page's offer, immutable asset caching |
| `internal/hub/assets/mdto.html` | the thin page (standalone document, like `share.html`) |
| `internal/hub/assets/mdto/` | the vendored bundle, its manifest, and `view.js` |
| `internal/hub/mdtoview_test.go` | the pin, the sandbox assertions, byte fidelity |

Detection reuses `readFileMeta`/`envelopeKey` from the save API
([save-api.md](save-api.md)), so the Hub can never disagree with itself about
which files are conforming documents. One consequence worth knowing: a file with
a **byte-order mark** before its opening `---` has no frontmatter as far as
`core` is concerned, so it declares nothing and gets no Markdown To view —
`afs`, the save API, and this view all agree on that.
