package hub

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Rendering a Markdown To document, read-only.
//
// A file whose frontmatter carries `markdownto: <spec>@<version>` is still an
// ordinary markdown note to this Hub — it commits, diffs, clones, and reads as
// one. What this file adds is a second way to look at it: the real Markdown To
// renderers, run in the reader's browser over the file's exact bytes.
//
// The Hub stays renderer-ignorant, and that is the whole design (hub-contract
// §3). It knows one frontmatter key. It serves a thin page carrying the file's
// bytes and a PINNED, integrity-checked copy of the browser bundle the
// playground ships (assets/mdto/), and the bundle decides what a kanban board
// or a backlog looks like. Upgrading the engine is a deliberate re-vendor
// (assets/mdto/VERSION), never a Hub rebuild and never a runtime fetch from
// another origin.
//
// The output never gets this origin. renderHtml and renderDiagnosticsHtml
// return complete standalone documents, and they go into an iframe through
// `srcdoc` with a sandbox that has neither `allow-scripts` nor
// `allow-same-origin` — the playground's own read-only posture. The LIVE board
// (which does run script, to write edits back) is deliberately not here: it
// waits on the Hub's `/render` content-domain decision.

// mdtoBundlePath is the vendored bundle inside the embedded asset tree, and
// mdtoViewPath the small script that drives it. Both are served from
// /_assets/, first-party, with an integrity attribute computed from these
// exact bytes.
const (
	mdtoBundlePath = "assets/mdto/mdto.js"
	mdtoViewPath   = "assets/mdto/view.js"
	mdtoAssetDir   = "mdto/"
)

// maxMdtoBytes bounds what will be embedded into a rendering page. It matches
// the file page's own render limit: above it the note is not rendered as
// markdown either, and the download is the honest answer.
const maxMdtoBytes = 1 << 20

// playgroundURL is where "Open in playground" goes. The playground has no
// prefill API yet (no hash or query the app reads), so this is deliberately a
// plain link to the app rather than a deep link that would silently drop the
// file — see docs/internals/markdownto-rendering.md.
const playgroundURL = "https://markdownto.ai/app/"

// mdtoAsset is one served-with-integrity script: its bytes, the SRI value
// computed from them, and the cache-busting URL that pins a reader to this
// exact copy.
type mdtoAsset struct {
	href      string
	integrity string
}

var (
	mdtoBundle = newMdtoAsset(mdtoBundlePath)
	mdtoView   = newMdtoAsset(mdtoViewPath)
)

// newMdtoAsset derives the href/integrity/etag for an embedded script from its
// own bytes. Deriving rather than declaring is the point: the integrity
// attribute on the page and the bytes the Hub serves cannot drift apart, so a
// re-vendor can never ship a page whose script the browser refuses. That the
// bytes are the INTENDED ones is a separate question, and assets/mdto/VERSION
// plus TestMdtoVendoredBundleMatchesManifest answers it.
func newMdtoAsset(name string) mdtoAsset {
	body, err := assetsFS.ReadFile(name)
	if err != nil {
		// Unreachable: the file is embedded at build time. Fail loudly rather
		// than serving a page whose script silently never loads.
		panic("hub: missing embedded asset " + name + ": " + err.Error())
	}
	sum := sha256.Sum256(body)
	return mdtoAsset{
		href:      "/_assets/" + strings.TrimPrefix(name, "assets/") + "?v=" + hex.EncodeToString(sum[:])[:12],
		integrity: "sha256-" + base64.StdEncoding.EncodeToString(sum[:]),
	}
}

// mdtoCSP is the policy for the thin rendering page — the Hub's first browser
// page to carry one, and strictly tighter than the (unset) default.
//
// Two documents live under it. The page itself needs exactly two same-origin
// scripts (the pinned bundle and the loader) and one stylesheet, so everything
// else is off.
//
// The sandboxed `srcdoc` frame INHERITS this policy, which is why the details
// matter more than they look:
//
//   - `connect-src 'none'` is what makes "the rendered document cannot phone
//     home" a browser-enforced fact rather than a property of the renderer. The
//     documents are self-contained by construction (inline <style>, inline SVG,
//     data: favicon), and this is the belt to that suspenders.
//   - `script-src` names no origin the frame could satisfy, so even a rendered
//     document that somehow contained a <script> could not run it — on top of
//     the sandbox already forbidding script entirely.
//   - `style-src 'unsafe-inline'` is required, and only by the frame: every
//     rendered document carries its stylesheet inline. The page's own inline
//     <style> rides along on it.
//   - `img-src 'self' data:` covers the data: favicon and the Hub's own icon
//     while keeping remote images (a tracking pixel in a hostile file) off.
const mdtoCSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self' data:; connect-src 'none'; " +
	"frame-src 'self'; child-src 'self'; worker-src 'none'; object-src 'none'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

// setMdtoHeaders marks a rendering page uncrawlable, unsniffable, uncached, and
// bound by mdtoCSP.
func setMdtoHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", mdtoCSP)
}

// mdtoEnvelope reports the `markdownto:` value a markdown file declares, or ""
// for every other file. It reads the one key through the same readFileMeta the
// save API uses, so the Hub can never disagree with itself about which files
// are conforming documents.
//
// Non-markdown paths, invalid UTF-8, and anything past maxMdtoBytes report "":
// there is nothing to hand a parser in those cases, and the ordinary view is
// the right one.
func mdtoEnvelope(filePath, content string) string {
	if !markdownPath(filePath) || len(content) > maxMdtoBytes {
		return ""
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return ""
	}
	return mdtoDisplayEnvelope(readFileMeta([]byte(content)).Envelope)
}

// mdtoDisplayEnvelope normalizes an envelope for display. The value is
// file-authored, so it is trimmed of surrounding whitespace and quotes, bounded
// in length, and rejected outright if it carries control characters — the page
// escapes it either way, but a chip is not a place for a novel.
func mdtoDisplayEnvelope(raw string) string {
	v := strings.TrimSpace(raw)
	v = strings.Trim(v, `"'`)
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 64 {
		return ""
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return v
}

// mdtoPageData backs assets/mdto.html. Every link on the page is supplied by
// the caller, because the two views this page serves — an authenticated file
// page and an anonymous share link — reach the same file through completely
// different URL spaces.
type mdtoPageData struct {
	Title          string
	Name           string
	Envelope       string
	SourceB64      string
	BundleHref     string
	BundleSRI      string
	ViewHref       string
	ViewSRI        string
	MarkdownHref   string // the plain-markdown escape hatch: this rendering never captures the file
	DownloadHref   string
	PlaygroundHref string
	BackHref       string
	BackLabel      string
}

// newMdtoPageData packs one file's bytes and the pinned scripts into the page.
// The source travels base64-encoded in a data attribute: the encoding is not
// obfuscation but the shortest path with no escaping in it, so the bytes the
// browser parses are the bytes that were committed — CRLF, BOM, emoji and all.
func newMdtoPageData(name, envelope, content string) mdtoPageData {
	return mdtoPageData{
		Title:          name + " · " + envelope,
		Name:           name,
		Envelope:       envelope,
		SourceB64:      base64.StdEncoding.EncodeToString([]byte(content)),
		BundleHref:     mdtoBundle.href,
		BundleSRI:      mdtoBundle.integrity,
		ViewHref:       mdtoView.href,
		ViewSRI:        mdtoView.integrity,
		PlaygroundHref: playgroundURL,
	}
}

// handleMdtoView serves the rendering page for an authenticated file view
// (/{user}/{repo}/mdto/{path}). Read authorization already ran in serveWeb.
// A file with no envelope has no Markdown To view, so it goes back to the
// ordinary note page rather than being rendered as something it never claimed
// to be.
func (s *Server) handleMdtoView(w http.ResponseWriter, r *http.Request, user, repo, filePath, viewer string) {
	bare := s.Storage.RepoDir(user, repo)
	size, ok := BlobSize("git", bare, defaultRef, filePath)
	if !ok || size > maxMdtoBytes {
		s.renderNotFound(w, r, viewer, user, repo)
		return
	}
	content, ok := BlobContent("git", bare, defaultRef, filePath)
	if !ok {
		s.renderNotFound(w, r, viewer, user, repo)
		return
	}
	envelope := mdtoEnvelope(filePath, content)
	if envelope == "" {
		http.Redirect(w, r, "/"+user+"/"+repo+"/blob/"+filePath, http.StatusFound)
		return
	}

	data := newMdtoPageData(pathBase(filePath), envelope, content)
	data.MarkdownHref = "/" + user + "/" + repo + "/blob/" + filePath
	data.DownloadHref = "/" + user + "/" + repo + "/download/" + filePath + "?format=original"
	data.BackHref = "/" + user + "/" + repo
	data.BackLabel = user + " / " + repo

	setMdtoHeaders(w)
	s.renderPage(w, r, "mdto", data)
}
