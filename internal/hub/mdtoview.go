package hub

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"agentsfs.ai/afs/internal/core"
)

// Rendering a Markdown To document — read-only for a reader, LIVE for someone
// who may write.
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
// another origin. Which dialects are draggable is likewise the bundle's call,
// decided in the browser by view.js: no spec name is written down in Go.
//
// Two views come out of that, and the difference is one question the Hub can
// answer on its own — MAY THIS VIEWER WRITE?
//
//   - No (a reader, a collaborator without write, an anonymous share link):
//     the document goes into an iframe through `srcdoc` with a sandbox that has
//     neither `allow-scripts` nor `allow-same-origin`. Script is off entirely.
//     This path is unchanged, byte for byte, by everything below.
//   - Yes: the page also carries the live board. Same `srcdoc`, same opaque
//     origin (still NO `allow-same-origin`, so the frame cannot read this page,
//     its DOM, or this Hub's cookies), plus `allow-scripts` so the patch engine
//     the bundle inlines can run. The board posts its whole file back on every
//     mutation and the page commits it — see handleMdtoSave.
//
// That is the playground's production-proven posture, adopted deliberately
// rather than inherited: `^markdownto-writeback` in the agentsfs backlog
// records the decision, including that no separate content domain is required
// for it.

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

// playgroundDeepLink is the playground's own #hub= grammar (its app.js
// appLinkFor): owner, instance, then each path segment, percent-encoded and
// joined with slashes. It names the file; the playground fetches it itself,
// running OAuth if the instance is private. Share views never get one — an
// anonymous viewer may hold no account, and the fragment would name a private
// path in their URL bar; the plain playground link stays their door.
func playgroundDeepLink(owner, repo, filePath string) string {
	segments := []string{url.PathEscape(owner), url.PathEscape(repo)}
	for _, part := range strings.Split(filePath, "/") {
		segments = append(segments, url.PathEscape(part))
	}
	return playgroundURL + "#hub=" + strings.Join(segments, "/")
}

// mdtoRawQuery asks a note's own page for the plain markdown rendering instead
// of the Markdown To view it now shows by default. It is deliberately spelled
// the way a share link already spells it (`?view=markdown`, sharelink.go): one
// word for one idea on every surface that renders one of these files.
const mdtoRawQuery = "view=markdown"

// mdtoWantsMarkdown reports whether this request asked for the raw rendering.
func mdtoWantsMarkdown(q url.Values) bool {
	return strings.EqualFold(strings.TrimSpace(q.Get("view")), "markdown")
}

// mdtoModeHref rewrites a file page's own URL with that escape hatch set or
// cleared, carrying everything ELSE the reader is looking at along with it — a
// selected commit in the history sidebar most of all.
//
// The toggle is a link and the URL is the whole of its memory. Nothing is
// stored, per-reader or per-file, so a note linked to from anywhere opens the
// same way for everybody, and a reader who wants the markdown says so in the
// address bar rather than in a preference they have to find again later.
func mdtoModeHref(blobPath string, q url.Values, markdown bool) string {
	next := url.Values{}
	for k, vals := range q {
		if k == "view" {
			continue
		}
		next[k] = vals
	}
	if markdown {
		next.Set("view", "markdown")
	}
	if len(next) == 0 {
		return blobPath
	}
	return blobPath + "?" + next.Encode()
}

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
	"img-src 'self' data:; media-src 'self'; font-src 'self' data:; connect-src 'none'; " +
	"frame-src 'self'; child-src 'self'; worker-src 'none'; object-src 'none'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

// mdtoLiveCSP is the policy for a page that may run the live board. It differs
// from mdtoCSP in exactly two directives, and both differences are the frame's
// or the save's, never a general loosening:
//
//   - `script-src` gains 'unsafe-inline' BECAUSE OF THE FRAME. The board's patch
//     engine is inlined into the rendered document by construction (the bundle
//     carries it as a string constant precisely so the frame fetches nothing),
//     and a `srcdoc` frame inherits this page's policy — so with 'self' alone
//     the board would draw and then sit there dead. What the page itself gives
//     up is small and stated here so it is not discovered later: this document
//     contains no inline <script> of its own, its two scripts are same-origin
//     and SRI-pinned, and every value it prints goes through html/template's
//     contextual escaper. 'unsafe-inline' buys an attacker nothing they could
//     not already do with markup injection into this page, which is the thing
//     actually being prevented.
//   - `connect-src` becomes 'self' because THIS page saves: view.js PUTs the
//     board's new bytes back to the Hub. The frame inherits that too, and it is
//     worth being precise about what it means there — the frame is an opaque
//     origin, so a request it made to this Hub would carry no cookies and its
//     response would be unreadable to it. "The rendered document cannot phone
//     home" stays browser-enforced for every origin but this one.
//
// Everything else is identical, including `frame-ancestors 'self'`, `object-src
// 'none'`, `base-uri 'none'` and `form-action 'none'`.
const mdtoLiveCSP = "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; media-src 'self'; font-src 'self' data:; connect-src 'self'; " +
	"frame-src 'self'; child-src 'self'; worker-src 'none'; object-src 'none'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

// mdtoGuidedCSP is the policy for a GUIDED page — a guided-narration@0.1
// manuscript, whose view is not a document at all but a player. It differs from
// mdtoCSP in exactly four directives. Three of them are the frame's:
//
//   - `script-src` gains 'unsafe-inline', for the same reason mdtoLiveCSP does
//     and by the same construction: the entire guided reader (~300 KB of it) is
//     inlined into the rendered document as one <script>, precisely so the frame
//     fetches nothing. A srcdoc frame inherits this page's policy, so with
//     'self' alone the reader would draw its toolbar and then sit there dead.
//     What the page itself gives up is the same small thing it gives up on the
//     live policy: it contains no inline <script> of its own, its two scripts
//     are same-origin and SRI-pinned, and every value it prints goes through
//     html/template's contextual escaper.
//   - `media-src` gains blob: and data:, because the player decodes speech into
//     an object URL and plays it (`URL.createObjectURL` -> `new Audio(url)`),
//     and an offline export carries its recordings as data: URIs.
//   - `img-src` gains blob:, because the source article the reader mounts is
//     rendered inside the same policy and may carry either kind of inline image.
//
// The fourth is the page's, and it is the same one the live policy moves for the
// same shape of reason. `connect-src` becomes 'self' because THIS page fetches:
// view.js reads the committed recording — the guided-narration-audio@0.1 index
// and one MP3 per beat — over /raw/ on this origin and hands the bytes to the
// reader base64'd inside the handshake it was already sending. A srcdoc frame
// runs under its embedder's policy, so 'none' here forbade the page's own fetch
// and left the feed inert (the 2026-09-21 journal episode has the violation
// transcript). What 'self' does NOT hand the frame is a channel: the frame is
// sandboxed without allow-same-origin, its origin is opaque, and an opaque
// origin is same-origin with nothing — so inside the frame 'self' matches no
// URL at all and is 'none' under another spelling, browser-enforced as before.
// Two rules keep that true and the tests pin both: the directive never names a
// scheme or a host (an opaque origin DOES match a host source), and the frame
// never gains allow-same-origin. The reader still contains no fetch() and no
// XMLHttpRequest — verified on the bundle's own bytes. Nothing on a guided page
// is saved, either: this policy is read-only in the only sense that matters.
//
// Everything else is identical to mdtoCSP, `object-src 'none'`, `base-uri
// 'none'`, `form-action 'none'` and `frame-ancestors 'self'` included.
const mdtoGuidedCSP = "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; media-src 'self' blob: data:; font-src 'self' data:; connect-src 'self'; " +
	"frame-src 'self'; child-src 'self'; worker-src 'none'; object-src 'none'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

// setMdtoHeaders marks a rendering page uncrawlable, unsniffable, uncached, and
// bound by mdtoCSP — the read-only policy, which is what a share link and every
// reader gets.
func setMdtoHeaders(w http.ResponseWriter) {
	setMdtoHeadersCSP(w, mdtoCSP)
}

// setMdtoLiveHeaders is setMdtoHeaders with the live board's policy. It is
// reached only from handleMdtoView, and only after the viewer has been found to
// have write access.
func setMdtoLiveHeaders(w http.ResponseWriter) {
	setMdtoHeadersCSP(w, mdtoLiveCSP)
}

// setMdtoGuidedHeaders is setMdtoHeaders with the guided reader's policy: the
// page a reader or an anonymous visitor gets for a guided-narration manuscript,
// where "read-only" has to mean "runs, saves nothing" rather than "runs
// nothing".
func setMdtoGuidedHeaders(w http.ResponseWriter) {
	setMdtoHeadersCSP(w, mdtoGuidedCSP)
}

func setMdtoHeadersCSP(w http.ResponseWriter, csp string) {
	h := w.Header()
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", csp)
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

// mdtoCrumb is one step of the way back out of a full-page rendering: the same
// owner/instance ladder the Hub's masthead draws, rebuilt here because this page
// owns its whole document and cannot carry the base chrome (its CSP admits
// exactly two scripts).
type mdtoCrumb struct {
	Name string
	Href string
}

// mdtoPageData backs assets/mdto.html. Every link on the page is supplied by
// the caller, because the three views this page serves — an authenticated file
// page, that page's chrome-less embed, and an anonymous share link — reach the
// same file through completely different URL spaces.
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

	// The way back into the Hub, and it exists only where there IS a Hub to go
	// back to. Crumbs ladder up to the instance; FileHref is the note's own page,
	// which is where a reader who opened the full view came from. A share link
	// sets neither: its reader has no session, no instance, and is not meant to
	// acquire one — the same reason assets/share.html carries no masthead.
	Crumbs   []mdtoCrumb
	FileHref string

	// Embed drops this page's own bar and footer for the file page's inline
	// frame, where the Hub's chrome is already on screen around it. Nothing else
	// changes: same bytes, same sandbox literals, same CSP, same save loop. It is
	// the page-level twin of the bundle's `chrome: 'embedded'`.
	Embed bool

	// Live turns the page from a rendering into an editor. It is set ONLY on the
	// authenticated file view, ONLY for a viewer with write access, and it is
	// what the template keys the widened sandbox literal and the save chrome
	// off. A share link never sets it, so the anonymous page cannot grow a
	// writable board by any route through this struct.
	Live bool
	// SaveHref is where the board's new bytes are PUT — this very URL. Empty
	// unless Live.
	SaveHref string
	// SourceHash is the sha256 of the bytes on the page: the first If-Match the
	// editor holds, so the round trip starts without a GET. Empty unless Live.
	SourceHash string

	// Guided marks a guided-narration@0.1 manuscript, whose view is a player
	// rather than a picture. It is what the template keys the reader's sandbox
	// literal off, and it is set for EVERY viewer — the reader is the read-only
	// view of this spec, not a privilege earned by write access.
	Guided bool
	// GuidedSourceB64 is the source article the manuscript names, base64-encoded
	// exactly as the manuscript's own bytes are, and GuidedSourceRef is the
	// `source:` string the reader will echo back in its handshake. Both are
	// empty when the manuscript names no resolvable sibling, and the reader then
	// falls back to its own paste/open form.
	GuidedSourceB64    string
	GuidedSourceRef    string
	GuidedSourceFormat string
	// GuidedAudioHref points at the guided-narration-audio@0.1 index for this exact
	// manuscript, and GuidedAudioVoice names the voice that read it. They are set only when
	// the recording beside the file is CURRENT for these bytes — the same currency rule the
	// strip above the page uses, because a reader speaking an older draft's sentences over
	// today's beats is worse than a reader speaking in the browser's robot voice.
	//
	// They are a pointer and a label, and deliberately nothing more. The Hub does not open
	// the index, does not read a single MP3, and does not learn what a beat is: view.js
	// fetches both through the ordinary /raw/ route, first-party and cookie-bearing, and
	// hands the bytes to the reader base64'd inside the handshake it was already sending.
	// That is why the guided page's `connect-src` is 'self' and names no host, and why the
	// frame, at its opaque origin, still cannot reach anything — see
	// docs/internals/markdownto-rendering.md.
	GuidedAudioHref  string
	GuidedAudioVoice string

	// Narrate is the optional hosted artifact strip for narrate@0.1. Hub resolves a tiny
	// manifest beside the manuscript, validates that it points only into the manuscript's
	// versioned artifact directory, and compares its source hash with these exact bytes.
	Narrate *mdtoNarrateArtifacts
}

// resolveGuidedSource reads the article a guided-narration manuscript names and
// returns it base64-encoded beside the exact `source:` string that named it, or
// two empty strings.
//
// The Hub does not FETCH anything here, and the spec is explicit that it must
// not: "the source is a reference, not an instruction to fetch". What it does is
// narrower and is the only thing the reader cannot do for itself — read a blob
// out of the repository the manuscript already lives in, at the commit the
// manuscript was read from, for a viewer who has already been let through the
// page's read gate. An https:// source is legal in the spec and there is nothing
// here to resolve it against, so it is treated exactly like an absent one.
//
// Every refusal below lands in the same place: no attributes, no bridge, and the
// reader's own intake form — a page that is missing a convenience, never a page
// that is broken.
func resolveGuidedSource(bare, filePath, content string) (b64, ref string) {
	ref = strings.TrimSpace(core.FrontmatterValueFromReader(strings.NewReader(content), guidedSourceKey))
	if !guidedRelativeSource(ref) {
		return "", ""
	}
	// Relative to the manuscript's own directory, which is what the spec says
	// and what the reader assumes when it echoes the string back.
	resolved, ok := safeRepoPath(path.Join(path.Dir(filePath), ref))
	if !ok || !markdownPath(resolved) {
		return "", ""
	}
	size, exists := BlobSize("git", bare, defaultRef, resolved)
	if !exists || size > maxMdtoBytes {
		return "", ""
	}
	article, exists := BlobContent("git", bare, defaultRef, resolved)
	if !exists || !utf8.ValidString(article) || strings.ContainsRune(article, 0) {
		return "", ""
	}
	return base64.StdEncoding.EncodeToString([]byte(article)), ref
}

// resolveGuidedCapture reads an existing capture beside an HTML source in this
// same repository. Absolute references must use the configured public origin
// and this repository's raw route. No URL is fetched and no other repo is read.
func resolveGuidedCapture(bare, filePath, content, origin, owner, repo string) (b64, ref string) {
	ref = strings.TrimSpace(core.FrontmatterValueFromReader(strings.NewReader(content), guidedSourceKey))
	u, err := url.Parse(ref)
	if err != nil || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", ""
	}
	var sourcePath string
	if u.IsAbs() {
		base, err := url.Parse(origin)
		prefix := "/" + owner + "/" + repo + "/raw/"
		if err != nil || base.Host == "" || u.Scheme != base.Scheme || u.Host != base.Host || !strings.HasPrefix(u.Path, prefix) {
			return "", ""
		}
		sourcePath = strings.TrimPrefix(u.Path, prefix)
	} else {
		if u.Host != "" || strings.HasPrefix(u.Path, "/") {
			return "", ""
		}
		sourcePath = u.Path
	}
	if strings.Contains(sourcePath, "\\") {
		return "", ""
	}
	for _, segment := range strings.Split(sourcePath, "/") {
		if segment == ".." {
			return "", ""
		}
	}
	if !u.IsAbs() {
		sourcePath = path.Join(path.Dir(filePath), sourcePath)
	}
	resolved, ok := safeRepoPath(sourcePath)
	ext := strings.ToLower(path.Ext(resolved))
	if !ok || (ext != ".html" && ext != ".htm") {
		return "", ""
	}
	capturePath := resolved + ".capture.json"
	size, exists := BlobSize("git", bare, defaultRef, capturePath)
	if !exists || size > maxMdtoBytes {
		return "", ""
	}
	capture, exists := BlobContent("git", bare, defaultRef, capturePath)
	if !exists || !utf8.ValidString(capture) {
		return "", ""
	}
	var data struct {
		Source string            `json:"source"`
		Blocks []json.RawMessage `json:"blocks"`
	}
	if json.Unmarshal([]byte(capture), &data) != nil || data.Source != ref || len(data.Blocks) == 0 {
		return "", ""
	}
	return base64.StdEncoding.EncodeToString([]byte(capture)), ref
}

// guidedSourceKey is the frontmatter key guided-narration@0.1 names its article
// with. It is read through the same core parser readFileMeta uses, so the value
// the Hub resolves and the value the engine parsed out of the same bytes are the
// same string — including when it was written quoted.
const guidedSourceKey = "source"

// guidedRelativeSource reports whether this `source:` value names a file in this
// repository. The spec's own validator (packages/core/src/guided-narration) is
// the shape being restated: a relative `.md` path, no scheme, no leading slash,
// no backslash, no control characters, no query and no fragment.
//
// Two of these refusals are the Hub's rather than the spec's, and both are worth
// naming. An `https://` source is VALID and simply names something this Hub has
// no business fetching. And a path that climbs out of the manuscript's own
// directory with `..` is refused rather than normalised: the spec says relative
// paths resolve against the manuscript's directory, and a manuscript reaching
// sideways through the tree is not something to resolve quietly on its behalf —
// it is the same rule safeRepoPath applies to every other path the Hub resolves,
// applied one step earlier so `path.Join` cannot erase the `..` first.
func guidedRelativeSource(ref string) bool {
	if ref == "" || len(ref) > 1024 || !markdownPath(ref) {
		return false
	}
	if strings.ContainsAny(ref, "\\?#") || strings.HasPrefix(ref, "/") {
		return false
	}
	for _, r := range ref {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	// A scheme, https:// included: nothing in this repository is being named.
	if i := strings.IndexByte(ref, ':'); i > 0 && isURLScheme(ref[:i]) {
		return false
	}
	for _, seg := range strings.Split(ref, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// isURLScheme reports whether this prefix looks like a URL scheme rather than
// part of a file name. A colon is legal in a path on the platforms the Hub
// serves, so the test is the scheme grammar and not the colon alone.
func isURLScheme(s string) bool {
	if s == "" || !isASCIILetter(rune(s[0])) {
		return false
	}
	for _, r := range s[1:] {
		if !isASCIILetter(r) && (r < '0' || r > '9') && r != '+' && r != '-' && r != '.' {
			return false
		}
	}
	return true
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
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

// handleMdtoView serves the Markdown To view of one file
// (/{user}/{repo}/mdto/{path}) and, on the same URL, accepts the board's edits
// back with PUT. One path for both halves is deliberate: the thing you may save
// is exactly the thing you are looking at, so there is no second URL space to
// keep in agreement with this one, and the route's read gate in serveWeb
// already ran before either half is reached.
//
// A file with no envelope has no Markdown To view, so a GET goes back to the
// ordinary note page rather than rendering it as something it never claimed to
// be.
//
// `?embed=1` serves the same page with its own bar and footer off, which is what
// the note page frames INSIDE the Hub's chrome (renderFile). The frame is a
// nested document rather than a second copy of this machinery on purpose: a
// `srcdoc` frame inherits its embedder's CSP, and the note page — an application
// page with pjax, an agent dock and repo images — can never be held to
// `default-src 'none'; connect-src 'none'`. Nesting is what lets the rendered
// document keep the policy it has here while the page around it keeps its own.
// The embed changes the chrome and nothing else: same bytes, same sandbox
// literals, same access gate, same CSP, same save URL.
func (s *Server) handleMdtoView(w http.ResponseWriter, r *http.Request, user, repo, filePath, viewer string) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodPut:
		s.handleMdtoSave(w, r, user, repo, filePath, viewer)
		return
	default:
		w.Header().Set("Allow", "GET, HEAD, PUT")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

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

	blob := "/" + user + "/" + repo + "/blob/" + filePath
	data := newMdtoPageData(pathBase(filePath), envelope, content)
	// The file page now renders this same view inline, so the two links are no
	// longer the same place: the note's page is where the board also lives, and
	// `?view=markdown` is the raw text. Naming them apart is what keeps "View as
	// Markdown" meaning what it says.
	data.MarkdownHref = blob + "?" + mdtoRawQuery
	data.DownloadHref = "/" + user + "/" + repo + "/download/" + filePath + "?format=original"
	data.FileHref = blob
	// The playground grew #hub= deep links (its Open-from-Hub work), so the
	// authenticated view can hand it the file by name instead of an empty tab.
	data.PlaygroundHref = playgroundDeepLink(user, repo, filePath)
	data.Crumbs = []mdtoCrumb{
		{Name: user, Href: "/" + user},
		{Name: repo, Href: "/" + user + "/" + repo},
	}
	// One query parameter, read as a flag and nothing else: the embed is the same
	// page with its own chrome off, never a different document or a different
	// gate. It is set by the file page's frame (web.go) and by nobody else.
	data.Embed = r.URL.Query().Get("embed") == "1"

	// The one question the Hub answers about this page. apiRepoAccess is the
	// same capability core the git remote, the agent API, the MCP server and the
	// /edit route ask, so "may edit the board" and "may edit the note" can never
	// disagree. A reader, a read-only collaborator and an anonymous viewer of a
	// public instance all come out false here and get the script-less page.
	_, canWrite := s.apiRepoAccess(user, repo, viewer)
	if spec, hasAudio := narrationArtifactSpecFor(envelope); hasAudio {
		data.Narrate = resolveNarrateArtifacts(
			spec, bare, user, repo, filePath, content, data.PlaygroundHref, canWrite,
		)
	}
	// A guided narration is a player, and a player that cannot run is not a
	// read-only view of it — it is a blank page with a paste box nobody can use.
	// So this page carries the reader's own sandbox literal for every viewer,
	// and, when the manuscript names an article that is committed beside it, the
	// article itself: the one half of the reader's handshake the browser cannot
	// perform for itself from inside an opaque origin that no 'self' matches.
	data.Guided = isGuidedNarration(envelope)
	if data.Guided {
		data.GuidedSourceB64, data.GuidedSourceRef = resolveGuidedSource(bare, filePath, content)
		data.GuidedSourceFormat = "markdown"
		if data.GuidedSourceB64 == "" {
			data.GuidedSourceB64, data.GuidedSourceRef = resolveGuidedCapture(bare, filePath, content, s.PublicBaseURL, user, repo)
			if data.GuidedSourceB64 != "" {
				data.GuidedSourceFormat = "json"
			}
		}
		// And the recording, on the same terms: a pointer at files this viewer may already
		// read, handed over only when it was made from the bytes on this page. `Current`
		// is the whole of the test — a stale recording still has its strip, and its strip
		// says out loud that it is older, but nothing hands it to the player.
		if data.Narrate != nil && data.Narrate.Current {
			data.GuidedAudioHref, data.GuidedAudioVoice = data.Narrate.BeatsHref, data.Narrate.Voice
		}
	}
	switch {
	case canWrite:
		// Unchanged for a writer, guided or not. The save loop is not suppressed
		// here because there is nothing here to suppress: view.js only enters it
		// for a result the engine gave a board IR, and a guided narration's IR is
		// never one. What this page hands a writer is a reader that runs, plus
		// the same live chrome every other conforming file hands them.
		data.Live = true
		data.SaveHref = "/" + user + "/" + repo + "/mdto/" + filePath
		data.SourceHash = sourceHash([]byte(content))
		setMdtoLiveHeaders(w)
	case data.Guided:
		setMdtoGuidedHeaders(w)
	default:
		setMdtoHeaders(w)
	}
	s.renderPage(w, r, "mdto", data)
}

// mdtoSaveResult is what a committed board edit answers with. hash is the saved
// bytes' hash — the client's If-Match for its next mutation, so a drag, a drop
// and another drag never need a round trip through GET.
type mdtoSaveResult struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Rev  string `json:"rev"`
	// Committed is false when the bytes were already what the board sent (a
	// no-op mutation, or a save retried after a response was lost): the hash is
	// still correct and the caller is still in sync, but git got nothing.
	Committed bool `json:"committed"`
}

// handleMdtoSave commits one board mutation. It is the writeback half of the
// live view and nothing else — not a general file-write API — and four gates
// say so:
//
//  1. **A session, and write access.** The credential is the Hub's own session
//     cookie, resolved into `viewer` by serveWeb before this is reached: the
//     same credential, resolved the same way, that the /edit route's form POST
//     has always used to commit a note. `/api/v1` stays bearer-only, and this
//     page never asks it for anything (see docs/internals/save-api.md).
//  2. **Same origin.** A cross-site page must not be able to steer a logged-in
//     browser into committing here, so mdtoSameOrigin demands an Origin header
//     naming this Hub. That is a check on the request itself rather than a
//     property of the cookie; SameSite=Lax (setSession) is the second layer
//     under it, and is what the /edit form relies on alone.
//  3. **Still the same document.** The bytes must declare the same
//     `markdownto:` envelope the file already carries. A board patches the
//     document it was drawn from; it never converts one spec into another, and
//     never turns a note into something else. This is what keeps the route from
//     being "PUT arbitrary bytes to any path you can read".
//  4. **If-Match, always.** The file exists by definition here, so an
//     unconditional save is refused with 428 exactly as the save API refuses
//     one. The hash is the Markdown To patch engine's own `sourceHash`, so the
//     value the board computed its mutation against is the value git is asked
//     to still be holding: one conflict model, from the drag to the commit.
func (s *Server) handleMdtoSave(w http.ResponseWriter, r *http.Request, user, repo, filePath, viewer string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Robots-Tag", "noindex, nofollow")

	if viewer == "" {
		apiError(w, http.StatusUnauthorized, "sign in to edit this document")
		return
	}
	if !mdtoSameOrigin(r) {
		apiError(w, http.StatusForbidden, "this endpoint accepts same-origin requests from a hub page only")
		return
	}
	if _, canWrite := s.apiRepoAccess(user, repo, viewer); !canWrite {
		apiError(w, http.StatusForbidden, "no write access")
		return
	}
	p, ok := safeRepoPath(filePath)
	if !ok {
		apiError(w, http.StatusBadRequest, "bad path")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMdtoBytes))
	if err != nil {
		apiError(w, http.StatusRequestEntityTooLarge, "this document is too large for the board view")
		return
	}

	bare := s.Storage.RepoDir(user, repo)
	head := s.RepoResolve(user, repo)
	current, exists := "", false
	if head != "" {
		current, exists = BlobContent("git", bare, head, p)
	}
	if !exists {
		// Gone, moved, or renamed under the open board. There is no current
		// representation to match, and creating one would resurrect a file
		// somebody deliberately removed.
		s.writeHashMismatch(w, "", head, "the file is no longer at this path")
		return
	}
	currentHash := sourceHash([]byte(current))

	was := mdtoEnvelope(p, current)
	if was == "" {
		s.writeHashMismatch(w, currentHash, head, "the file at this path is no longer a Markdown To document")
		return
	}
	if now := mdtoEnvelope(p, string(body)); now != was {
		apiError(w, http.StatusBadRequest, "a board saves the document it was drawn from; these bytes declare "+mdtoEnvelopeLabel(now))
		return
	}

	ifMatch := strings.TrimSpace(r.Header.Get("If-Match"))
	if ifMatch == "" {
		s.writePreconditionRequired(w, currentHash, head)
		return
	}
	if !etagMatches(ifMatch, currentHash) {
		s.writeHashMismatch(w, currentHash, head, "the file changed since this board was opened")
		return
	}

	// A mutation that produced the bytes already committed. The patch engine can
	// emit one (drop a card back where it came from), and a commit per mutation
	// is the contract — an EMPTY commit is not.
	if string(body) == current {
		s.writeMdtoSaved(w, mdtoSaveResult{Path: p, Hash: currentHash, Rev: head, Committed: false})
		return
	}

	res, err := s.RepoCommit(viewer, apiCommitRequest{
		Repo:    user + "/" + repo,
		BaseRev: head,
		Message: mdtoCommitMessage(p),
		Changes: []apiChange{{Path: p, Content: string(body)}},
	})
	if err != nil {
		if ce, ok := err.(*conflictError); ok {
			// HEAD moved onto this very path between the hash check and the ref
			// update — the same "someone got there first", a few milliseconds
			// later. Answer it in the 412 shape so the page's recovery path is
			// the one it already has.
			latest := ""
			if cur, ok := BlobContent("git", bare, ce.head, p); ok {
				latest = sourceHash([]byte(cur))
			}
			s.writeHashMismatch(w, latest, ce.head, "the file changed while this edit was being committed")
			return
		}
		writeAccessError(w, err)
		return
	}
	s.writeMdtoSaved(w, mdtoSaveResult{Path: p, Hash: sourceHash(body), Rev: res.NewRev, Committed: true})
}

// writeMdtoSaved answers a save with the hash the editor must now hold. The
// ETag is set for the same reason the save API sets it: the next If-Match is
// already in the reader's hands.
func (s *Server) writeMdtoSaved(w http.ResponseWriter, res mdtoSaveResult) {
	w.Header().Set("ETag", `"`+res.Hash+`"`)
	w.Header().Set("X-Afs-Source-Hash", res.Hash)
	writeJSON(w, http.StatusOK, res)
}

// mdtoCommitMessage is what a board mutation looks like in `git log`. The board
// posts its whole file and not the operation that produced it, so the subject
// says what is honestly known; the `Via:` trailer records the front door, as
// every other write surface does, and the author is the human who dragged the
// card.
func mdtoCommitMessage(p string) string {
	return "Update " + pathBase(p) + "\n\nVia: Markdown To board (agentsfs hub)\n"
}

// mdtoEnvelopeLabel names what a rejected body declared, for the error message.
func mdtoEnvelopeLabel(envelope string) string {
	if envelope == "" {
		return "no Markdown To envelope at all"
	}
	return envelope
}

// mdtoSameOrigin reports whether a write came from a page on this Hub.
//
// The Origin header is the check that matters: a browser sends it on every PUT,
// a page cannot forge it, and it names the site that initiated the request
// rather than the credential that rode along. Sec-Fetch-Site is consulted when
// present because it says the same thing more directly, and its absence proves
// nothing (older browsers, and non-browser callers, simply omit it).
//
// A missing Origin is refused rather than waved through. This route exists for
// one browser page; a caller with no origin is not that page, and the Hub has a
// bearer-authenticated API for everything else.
func mdtoSameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return false
	}
	origin := r.Header.Get("Origin")
	return origin != "" && strings.EqualFold(origin, hubBase(r))
}
