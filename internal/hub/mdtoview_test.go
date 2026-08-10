package hub

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The rendering fixture. mdtoKanban is a conforming kanban board deliberately
// carrying the things an escaping bug eats: CRLF line endings, an emoji, a
// quoted envelope, and an HTML-looking card title. mdtoPlain declares no
// envelope and must be untouched by everything here.
const (
	mdtoKanban = "---\r\n" +
		"markdownto: \"kanban@0.1\"\r\n" +
		"title: Launch board 🚀\r\n" +
		"---\r\n" +
		"\r\n" +
		"## Backlog\r\n" +
		"\r\n" +
		"- [ ] Write the <script>alert(1)</script> card\r\n" +
		"\r\n" +
		"## Done\r\n" +
		"\r\n" +
		"- [x] Choose the envelope\r\n"

	mdtoTodo = "---\nmarkdownto: todo@0.1\n---\n\n- [ ] Renew the registration\n- [x] Pay the bill\n"

	// Conforming envelope, broken body: the todo spec has no `### ` sections, so
	// this parses with errors and its read-only view is the validation report.
	mdtoBroken = "---\nmarkdownto: todo@0.1\n---\n\n#### Not a section\n\n- [ ] orphan\n"

	mdtoPlain = "---\ndescription: An ordinary note with no envelope\n---\n\n# Notes\n\nnothing special\n"
)

var mdtoRepoFiles = map[string]string{
	"apps/board.md":  mdtoKanban,
	"apps/list.md":   mdtoTodo,
	"apps/broken.md": mdtoBroken,
	"NOTE.md":        mdtoPlain,
}

// ---- the vendored bundle -------------------------------------------------

// mdtoManifest reads assets/mdto/VERSION into its key/value pairs.
func mdtoManifest(t *testing.T) map[string]string {
	t.Helper()
	body, err := assetsFS.ReadFile("assets/mdto/VERSION")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// TestMdtoVendoredBundleMatchesManifest is the pin. The integrity attribute is
// derived from the served bytes, so it can never break a page — which means a
// silent swap of the bundle would otherwise go unnoticed. The manifest is the
// declared intent, and this is what makes re-vendoring a deliberate act: change
// the file without changing VERSION and the build fails here, not in a browser.
func TestMdtoVendoredBundleMatchesManifest(t *testing.T) {
	manifest := mdtoManifest(t)
	body, err := assetsFS.ReadFile(mdtoBundlePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if got, want := hex.EncodeToString(sum[:]), manifest["sha256"]; got != want {
		t.Fatalf("vendored mdto.js sha256 = %s, manifest says %s — re-vendor deliberately (see assets/mdto/VERSION)", got, want)
	}
	if manifest["commit"] == "" || manifest["source"] == "" {
		t.Error("assets/mdto/VERSION must record the source repo and the commit the bundle came from")
	}
	if want := "sha256-" + base64.StdEncoding.EncodeToString(sum[:]); mdtoBundle.integrity != want {
		t.Errorf("bundle integrity = %q, want %q", mdtoBundle.integrity, want)
	}
	// The engine's whole surface is one global; a bundle that lost it would
	// render nothing and say nothing.
	if !strings.Contains(string(body), "window.MDTO is its whole surface") {
		t.Error("vendored bundle does not look like the Markdown To playground bundle")
	}

	// The four things the LIVE view depends on, asserted on the BYTES rather
	// than left to a browser. Each of them can disappear in a re-vendor without
	// breaking a single Go test or throwing a single error in the page: a board
	// that stopped posting its source would keep drawing, keep dragging, and
	// quietly stop saving — the exact failure the markdownto repo's own build
	// script warns about at the seam it warns about it.
	for _, want := range []struct{ needle, why string }{
		{`renderBoard(`, "the live board entry point"},
		{`chrome`, "the 'embedded' chrome option this page renders with"},
		{`mdto:"source"`, "the editor bridge — the wire every save rides on"},
		{`mdto:"key"`, "the board's Escape forwarding, which this page reads and deliberately drops"},
	} {
		if !strings.Contains(string(body), want.needle) {
			t.Errorf("vendored bundle no longer carries %s (%q): the live view would fail silently", want.why, want.needle)
		}
	}
	// The board's frame is allowed to run script because there is nothing in it
	// to phone home with. That is a property of these bytes, so it is checked
	// here rather than assumed.
	for _, forbidden := range []string{"XMLHttpRequest", "WebSocket(", "sendBeacon"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("vendored bundle grew %s; the board's frame is supposed to carry no network code at all", forbidden)
		}
	}
}

// TestMdtoAssetsServedImmutably: the scripts are content-addressed, so their
// URLs pin a reader to one engine and a year of caching is honest.
func TestMdtoAssetsServedImmutably(t *testing.T) {
	ts, _, _ := newShareTestHub(t)

	for _, asset := range []struct {
		name  string
		asset mdtoAsset
		path  string
	}{
		{"bundle", mdtoBundle, mdtoBundlePath},
		{"view", mdtoView, mdtoViewPath},
	} {
		res, err := http.Get(ts.URL + asset.asset.href)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", asset.name, res.StatusCode)
		}
		if got, want := res.Header.Get("Cache-Control"), "public, max-age=31536000, immutable"; got != want {
			t.Errorf("%s Cache-Control = %q, want %q", asset.name, got, want)
		}
		if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
			t.Errorf("%s Content-Type = %q, want text/javascript", asset.name, got)
		}
		if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s X-Content-Type-Options = %q, want nosniff", asset.name, got)
		}
		embedded, err := assetsFS.ReadFile(asset.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != string(embedded) {
			t.Errorf("%s served bytes differ from the embedded asset", asset.name)
		}
		sum := sha256.Sum256(body)
		if want := "sha256-" + base64.StdEncoding.EncodeToString(sum[:]); asset.asset.integrity != want {
			t.Errorf("%s integrity %q does not match the bytes served (%q)", asset.name, asset.asset.integrity, want)
		}
	}
}

// ---- detection ------------------------------------------------------------

func TestMdtoEnvelopeDetection(t *testing.T) {
	long := strings.Repeat("k", 80)
	for _, tc := range []struct {
		name, path, content, want string
	}{
		{"kanban", "apps/board.md", mdtoKanban, "kanban@0.1"},
		{"todo", "apps/list.md", mdtoTodo, "todo@0.1"},
		{"no envelope", "NOTE.md", mdtoPlain, ""},
		{"not markdown", "apps/board.txt", mdtoKanban, ""},
		{"html masquerading", "apps/board.html", mdtoKanban, ""},
		{"empty file", "apps/empty.md", "", ""},
		// A byte-order mark before the opening fence means core sees no
		// frontmatter at all, so `afs`, the save API and this view agree the
		// file declares nothing. Detection deliberately does not get cleverer
		// than the parser the rest of the product uses.
		{"byte-order mark", "apps/bom.md", "\ufeff" + mdtoTodo, ""},
		{"binary", "apps/bin.md", "---\nmarkdownto: todo@0.1\n---\n\x00\xff\xfe", ""},
		{"control chars in value", "apps/x.md", "---\nmarkdownto: \"kan\x07ban\"\n---\n", ""},
		{"absurdly long value", "apps/y.md", "---\nmarkdownto: " + long + "\n---\n", ""},
		{"too large", "apps/big.md", "---\nmarkdownto: todo@0.1\n---\n" + strings.Repeat("x", maxMdtoBytes), ""},
	} {
		if got := mdtoEnvelope(tc.path, tc.content); got != tc.want {
			t.Errorf("%s: mdtoEnvelope = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ---- the rendering page ---------------------------------------------------

var mdtoSourceRe = regexp.MustCompile(`id="mdto-source"[^>]*data-b64="([^"]*)"`)

// mdtoEmbeddedSource decodes the bytes a rendering page handed the engine. The
// entity decode mirrors what the browser's tokenizer does before getAttribute
// ever returns the value: html/template writes `+` as `&#43;` in an attribute.
func mdtoEmbeddedSource(t *testing.T, body string) string {
	t.Helper()
	m := mdtoSourceRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("page carries no base64 source payload:\n%s", body)
	}
	raw, err := base64.StdEncoding.DecodeString(html.UnescapeString(m[1]))
	if err != nil {
		t.Fatalf("source payload is not valid base64: %v", err)
	}
	return string(raw)
}

// assertMdtoPageCommon checks what must be true of EVERY rendering page,
// read-only or live: the frame it starts with, the pinned scripts, the escape
// hatches, and the bytes.
func assertMdtoPageCommon(t *testing.T, body, wantSource string) {
	t.Helper()

	// The frame every page is served with. `allow-downloads` is only what lets
	// the rendered document's own "Download the Markdown" link work.
	if !strings.Contains(body, `sandbox="allow-downloads"`) {
		t.Errorf("rendering page is missing the literal sandbox attribute:\n%s", body)
	}
	// The one flag that appears on NEITHER variant, at any time. Without it the
	// frame is an opaque origin and cannot reach this page or this Hub's
	// cookies, which is what makes running the board's script survivable at all.
	if strings.Contains(body, "allow-same-origin") {
		t.Error("rendering page granted the frame this origin")
	}
	if !strings.Contains(body, "srcdoc") && !strings.Contains(body, "mdto-stage") {
		t.Error("rendering page has no srcdoc stage")
	}

	// The pinned engine, integrity-checked, first-party. The attribute is
	// compared after entity decoding because html/template escapes `+` in an
	// attribute value as `&#43;` — the HTML tokenizer decodes it back before SRI
	// ever sees it, so the two forms are the same attribute.
	for _, want := range []mdtoAsset{mdtoBundle, mdtoView} {
		m := regexp.MustCompile(`<script src="` + regexp.QuoteMeta(want.href) + `" integrity="([^"]*)"`).FindStringSubmatch(body)
		if m == nil {
			t.Errorf("rendering page does not load %s with an integrity attribute:\n%s", want.href, body)
			continue
		}
		if got := html.UnescapeString(m[1]); got != want.integrity {
			t.Errorf("integrity for %s = %q, want %q", want.href, got, want.integrity)
		}
	}
	if strings.Contains(body, "https://markdownto.ai/app/mdto.js") || strings.Contains(body, "cdn.") {
		t.Error("rendering page loads the engine from somewhere other than this origin")
	}

	// The escape hatches: the rendering never captures the file.
	//
	// One variant of this page does not draw them itself — the chrome-less embed
	// the note page frames, whose host carries them one document up. It is
	// recognised here by having NO chrome at all rather than by being short a
	// link, so a full page that quietly lost its bar still fails.
	if embedded := !strings.Contains(body, `class="mdto-bar"`); embedded {
		for _, leftover := range []string{"mdto-crumbs", "mdto-acts", "mdto-foot"} {
			if strings.Contains(body, leftover) {
				t.Errorf("a page with no bar still carries %q — half a chrome is not a variant:\n%s", leftover, body)
			}
		}
	} else {
		if !strings.Contains(body, "View as Markdown") || !strings.Contains(body, "Download .md") {
			t.Errorf("rendering page is missing the plain-view escape hatches:\n%s", body)
		}
		if !strings.Contains(body, playgroundURL) {
			t.Error("rendering page is missing the playground link")
		}
	}

	// Byte fidelity: what the engine parses is what was committed.
	if got := mdtoEmbeddedSource(t, body); got != wantSource {
		t.Errorf("embedded source is not byte-identical to the file:\ngot  %q\nwant %q", got, wantSource)
	}
}

// assertMdtoPage is the READ-ONLY page: a share link, a reader, an anonymous
// visitor to a public instance. Nothing on it may run script, and nothing on it
// may name a way to save.
func assertMdtoPage(t *testing.T, res *http.Response, body, wantSource string) {
	t.Helper()
	assertMdtoPageCommon(t, body, wantSource)

	if strings.Contains(body, "allow-scripts") {
		t.Error("read-only rendering page widened the iframe sandbox")
	}
	// The live half is an ELEMENT the Hub either emits or does not. Its absence
	// is the whole gate: view.js cannot widen a frame whose widened literal was
	// never served, and cannot save to a URL it was never given.
	for _, marker := range []string{"mdto-live", "data-sandbox", "data-save", "data-hash", "mdto-conflict", "mdto-save"} {
		if strings.Contains(body, marker) {
			t.Errorf("read-only rendering page carries the live marker %q:\n%s", marker, body)
		}
	}

	// The CSP, which the srcdoc frame inherits: no script, no network.
	if got := res.Header.Get("Content-Security-Policy"); got != mdtoCSP {
		t.Errorf("Content-Security-Policy = %q, want %q", got, mdtoCSP)
	}
	if !strings.Contains(mdtoCSP, "connect-src 'none'") || !strings.Contains(mdtoCSP, "script-src 'self';") {
		t.Error("the read-only CSP must forbid inline script and network requests from the frame")
	}
}

// assertMdtoLivePage is the writable page. Everything the read-only page
// promises still holds except the two things this one deliberately adds, and
// both are asserted literally here so widening either becomes a diff someone
// has to defend.
func assertMdtoLivePage(t *testing.T, res *http.Response, body, wantSource, saveHref, wantHash string) {
	t.Helper()
	assertMdtoPageCommon(t, body, wantSource)

	if !strings.Contains(body, `data-sandbox="allow-scripts allow-downloads"`) {
		t.Errorf("live page does not author the widened sandbox literal:\n%s", body)
	}
	if !strings.Contains(body, `data-save="`+saveHref+`"`) {
		t.Errorf("live page does not name its save URL %q:\n%s", saveHref, body)
	}
	if !strings.Contains(body, `data-hash="`+wantHash+`"`) {
		t.Errorf("live page does not carry the source hash %q (the first If-Match)", wantHash)
	}
	// The conflict surface has to exist before it is needed: a 412 arrives after
	// the network is already unhappy, which is a bad moment to fetch a page.
	for _, marker := range []string{`id="mdto-save"`, `id="mdto-conflict"`, `id="mdto-reload"`, `id="mdto-take"`} {
		if !strings.Contains(body, marker) {
			t.Errorf("live page is missing the save chrome %s:\n%s", marker, body)
		}
	}
	if got := res.Header.Get("Content-Security-Policy"); got != mdtoLiveCSP {
		t.Errorf("Content-Security-Policy = %q, want the live policy %q", got, mdtoLiveCSP)
	}
	// The live policy differs from the read-only one in exactly two directives.
	if !strings.Contains(mdtoLiveCSP, "script-src 'self' 'unsafe-inline'") {
		t.Error("the live CSP must admit the frame's inlined patch engine")
	}
	if !strings.Contains(mdtoLiveCSP, "connect-src 'self'") {
		t.Error("the live CSP must admit this page's own save")
	}
	for _, directive := range []string{"default-src 'none'", "object-src 'none'", "base-uri 'none'", "form-action 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(mdtoLiveCSP, directive) {
			t.Errorf("the live CSP dropped %q, which the read-only policy carries", directive)
		}
	}
}

// TestFilePageOffersMdtoView: an envelope earns a second view, and only an
// envelope does. The markdown rendering stays either way.
func TestFilePageOffersMdtoView(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", mdtoRepoFiles)
	cookie := sessionCookieFor(srv, "alice")

	get := func(path string) string {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookie)
		res, err := noRedirectClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, res.StatusCode)
		}
		return string(body)
	}

	board := get("/alice/brain/blob/apps/board.md")
	if !strings.Contains(board, `href="/alice/brain/mdto/apps/board.md"`) {
		t.Errorf("conforming file page does not offer the Markdown To view:\n%s", board)
	}
	if !strings.Contains(board, "kanban@0.1") {
		t.Error("conforming file page does not name the spec it declares")
	}
	if !strings.Contains(board, `<article class="prose">`) {
		t.Error("conforming file page lost its ordinary markdown rendering")
	}

	plain := get("/alice/brain/blob/NOTE.md")
	if strings.Contains(plain, "/alice/brain/mdto/") {
		t.Errorf("a file with no envelope was offered the Markdown To view:\n%s", plain)
	}
}

// ---- the note page's inline rendering --------------------------------------

// TestFilePageRendersMdtoInline is the default this whole slice exists for: a
// conforming file's note page shows the document as what it declares, inside
// the Hub's own chrome — and gives the markdown back with one link.
func TestFilePageRendersMdtoInline(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	const blob = "/alice/brain/blob/apps/board.md"

	res, body := mdtoGet(t, ts, srv, "alice", blob)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.StatusCode, body)
	}

	// The view is framed from the file's OWN rendering page with its chrome off.
	// That nesting is the architecture: a `srcdoc` frame inherits its embedder's
	// CSP, and this page — pjax, an agent dock, repo images — cannot be held to
	// the rendered document's policy. One document, one policy each.
	if !strings.Contains(body, `src="/alice/brain/mdto/apps/board.md?embed=1"`) {
		t.Errorf("note page does not frame the Markdown To view:\n%s", body)
	}
	// The consequence worth keeping: the ~750 KB engine is the FRAME's, so it is
	// fetched only by pages that render one of these files, and never by an
	// ordinary note.
	if strings.Contains(body, mdtoBundle.href) || strings.Contains(body, mdtoView.href) {
		t.Error("the note page itself loaded the Markdown To bundle")
	}
	// The frame is deliberately not sandboxed: it is a first-party page on this
	// origin that sandboxes the document inside itself, and an opaque origin here
	// would break the board's own cookie-credentialed save.
	if strings.Contains(body, "sandbox=") {
		t.Errorf("the inline frame grew a sandbox attribute — it would break the save it exists for:\n%s", body)
	}

	// The toggle out, and the way to the full view. Both are plain links.
	if !strings.Contains(body, `href="`+blob+`?view=markdown"`) {
		t.Errorf("note page has no toggle to the markdown rendering:\n%s", body)
	}
	if !strings.Contains(body, `href="/alice/brain/mdto/apps/board.md"`) {
		t.Error("note page does not link to the full-page view")
	}

	// Degrading without script. The markdown rendering is still BUILT and still
	// on the page; a <noscript> stylesheet is what brings it back, so a reader
	// with no JavaScript sees exactly what this page used to show.
	if !strings.Contains(body, `<article class="prose">`) {
		t.Error("note page stopped rendering the markdown it falls back to")
	}
	hidden := strings.Index(body, ".note-mdto-raw { display: none; }")
	shown := strings.Index(body, ".note-mdto-raw { display: block; }")
	if hidden < 0 || shown < 0 {
		t.Fatalf("the no-script fallback is not a stylesheet pair:\n%s", body)
	}
	if shown < hidden {
		t.Error("the <noscript> sheet comes before the rule it must override")
	}
	if noscript := strings.Index(body, "<noscript>"); noscript < 0 || noscript > shown {
		t.Error("the fallback rule is not inside a <noscript>, so it would apply to everybody")
	}

	// A note with no envelope keeps the page it has always had — no frame, no
	// stylesheet, no mode strip, and `?view=markdown` is inert on it.
	for _, path := range []string{"/alice/brain/blob/NOTE.md", "/alice/brain/blob/NOTE.md?view=markdown"} {
		_, plain := mdtoGet(t, ts, srv, "alice", path)
		for _, marker := range []string{"note-mdto", "embed=1", "mdto/NOTE.md"} {
			if strings.Contains(plain, marker) {
				t.Errorf("%s carries the Markdown To marker %q:\n%s", path, marker, plain)
			}
		}
	}
}

// TestFilePageMarkdownToggle: the escape hatch is a URL and nothing else. It
// remembers nothing between requests, and it carries the rest of the page's
// state — a selected commit above all — across with it.
func TestFilePageMarkdownToggle(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	const blob = "/alice/brain/blob/apps/board.md"

	res, body := mdtoGet(t, ts, srv, "alice", blob+"?view=markdown")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.StatusCode, body)
	}
	for _, gone := range []string{"embed=1", "note-mdto-stage", "note-mdto-raw"} {
		if strings.Contains(body, gone) {
			t.Errorf("?view=markdown still carries %q:\n%s", gone, body)
		}
	}
	if !strings.Contains(body, `<article class="prose">`) {
		t.Error("?view=markdown did not serve the markdown rendering")
	}
	if !strings.Contains(body, `href="`+blob+`"`) {
		t.Errorf("the markdown view offers no way back to the rendered one:\n%s", body)
	}

	// The rest of the URL survives the toggle in both directions: flipping the
	// view must not throw away the commit whose diff is open beside it.
	_, withCommit := mdtoGet(t, ts, srv, "alice", blob+"?commit=deadbeef")
	if got := html.UnescapeString(withCommit); !strings.Contains(got, blob+"?commit=deadbeef&view=markdown") {
		t.Errorf("the toggle dropped the selected commit:\n%s", withCommit)
	}
	_, both := mdtoGet(t, ts, srv, "alice", blob+"?commit=deadbeef&view=markdown")
	if !strings.Contains(both, `href="`+blob+`?commit=deadbeef"`) {
		t.Errorf("the way back dropped the selected commit:\n%s", both)
	}
}

// TestMdtoInlineFramePerViewer is the same gate as the full page, asserted on
// the page the note page actually frames — because that is where it now
// matters. The note page's own markup is identical for every viewer: it frames
// one URL, and the Hub decides inside it what that URL renders.
func TestMdtoInlineFramePerViewer(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	const embed = "/alice/brain/mdto/apps/board.md?embed=1"
	const save = "/alice/brain/mdto/apps/board.md"

	for _, viewer := range []string{"alice", "carol"} {
		res, body := mdtoGet(t, ts, srv, viewer, embed)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", viewer, res.StatusCode)
		}
		assertMdtoLivePage(t, res, body, mdtoKanban, save, sourceHash([]byte(mdtoKanban)))
		// The save URL is the bare route: the embed flag is this page's chrome,
		// never part of what it writes to.
		if strings.Contains(body, `data-save="`+embed+`"`) {
			t.Error("the embedded board saves to the embed URL rather than the route")
		}
	}

	// A read collaborator frames the same URL and gets the script-less document.
	res, body := mdtoGet(t, ts, srv, "bob", embed)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reader: status = %d, want 200", res.StatusCode)
	}
	assertMdtoPage(t, res, body, mdtoKanban)

	// And so does an anonymous reader of a public instance, on their note page.
	if err := srv.setVisibility("alice", "brain", visPublic); err != nil {
		t.Fatal(err)
	}
	res, body = mdtoGet(t, ts, srv, "", embed)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("anonymous on a public instance: status = %d, want 200", res.StatusCode)
	}
	assertMdtoPage(t, res, body, mdtoKanban)

	_, page := mdtoGet(t, ts, srv, "", "/alice/brain/blob/apps/board.md")
	if !strings.Contains(page, `src="`+embed+`"`) {
		t.Errorf("an anonymous reader's note page does not frame the view:\n%s", page)
	}
}

// TestMdtoEmbedDropsOnlyItsChrome: the embed is the same document with its bar
// and footer off. Everything that makes it safe is unchanged, and the flag is
// read as a flag — an unrecognised value is simply the full page.
func TestMdtoEmbedDropsOnlyItsChrome(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	const route = "/alice/brain/mdto/apps/board.md"

	_, full := mdtoGet(t, ts, srv, "alice", route)
	_, embed := mdtoGet(t, ts, srv, "alice", route+"?embed=1")

	for _, gone := range []string{`class="mdto-bar"`, "mdto-crumbs", "mdto-foot", "AgentsFS Hub", "Open in playground"} {
		if !strings.Contains(full, gone) {
			t.Fatalf("the full page does not carry %q, so its absence proves nothing", gone)
		}
		if strings.Contains(embed, gone) {
			t.Errorf("the embedded page still draws %q, which its host already carries:\n%s", gone, embed)
		}
	}
	// What must NOT change with the chrome: the conflict surface, the save state,
	// and the <noscript> that names the file's plain form.
	for _, kept := range []string{`id="mdto-conflict"`, `id="mdto-save"`, `id="mdto-stage"`, "<noscript>"} {
		if !strings.Contains(embed, kept) {
			t.Errorf("the embedded page dropped %q with its chrome:\n%s", kept, embed)
		}
	}
	if _, other := mdtoGet(t, ts, srv, "alice", route+"?embed=yes"); !strings.Contains(other, `class="mdto-bar"`) {
		t.Error("embed is a flag, not a truthy string: an unrecognised value must serve the full page")
	}
}

// TestMdtoPageCarriesTheWayBack: the full-page view owns its whole document, so
// the ladder back into the Hub has to be drawn on it. A share link gets none of
// it — its reader has no session and no instance to return to.
func TestMdtoPageCarriesTheWayBack(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)

	_, body := mdtoGet(t, ts, srv, "alice", "/alice/brain/mdto/apps/board.md")
	for _, want := range []string{
		`href="/"`,                                    // the Hub itself
		`href="/alice"`,                               // the owner
		`href="/alice/brain"`,                         // the instance
		`href="/alice/brain/blob/apps/board.md"`,      // the note this view came from
		`href="/alice/brain/blob/apps/board.md?view=`, // and its markdown
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the full rendering page has no %s:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "AgentsFS Hub") || !strings.Contains(body, "Back to the note") {
		t.Error("the way back is not labelled")
	}

	token := mintShareLink(t, ts, srv, "alice", "brain", "apps/board.md", false)
	_, shared := getShared(t, ts, "/s/"+token)
	// The bar's stylesheet still mentions the crumbs — it is one bar — so what is
	// asserted is that no crumb, no hub label and no instance URL is ever DRAWN.
	for _, leak := range []string{`class="mdto-crumbs"`, "AgentsFS Hub", "Back to the note", "/alice/brain"} {
		if strings.Contains(shared, leak) {
			t.Errorf("the anonymous share page leaked hub navigation (%q):\n%s", leak, shared)
		}
	}
}

// TestMdtoViewPage is the authenticated rendering page, end to end. The viewer
// here is the owner, so what they get is the LIVE board.
func TestMdtoViewPage(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", mdtoRepoFiles)
	cookie := sessionCookieFor(srv, "alice")

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/alice/brain/mdto/apps/board.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	res, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.StatusCode, body)
	}
	assertMdtoLivePage(t, res, string(body), mdtoKanban,
		"/alice/brain/mdto/apps/board.md", sourceHash([]byte(mdtoKanban)))

	if !strings.Contains(string(body), `href="/alice/brain/blob/apps/board.md"`) {
		t.Error("rendering page does not link back to the plain markdown view")
	}
	if !strings.Contains(string(body), `href="/alice/brain/download/apps/board.md?format=original"`) {
		t.Error("rendering page does not offer the original download")
	}
	for header, want := range map[string]string{
		"X-Robots-Tag":           "noindex, nofollow",
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
	} {
		if got := res.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// TestMdtoViewRejectsNonConforming: a note that never claimed a spec is not
// rendered as one — it goes back to being a note.
func TestMdtoViewRejectsNonConforming(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", mdtoRepoFiles)
	cookie := sessionCookieFor(srv, "alice")

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/alice/brain/mdto/NOTE.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	res, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 back to the note", res.StatusCode)
	}
	if got, want := res.Header.Get("Location"), "/alice/brain/blob/NOTE.md"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// TestMdtoViewNeedsAccess: the rendering route is an ordinary read route on a
// private repo, and inherits exactly the same gate as /blob and /raw.
func TestMdtoViewNeedsAccess(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", mdtoRepoFiles)

	res, err := noRedirectClient().Get(ts.URL + "/alice/brain/mdto/apps/board.md")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(res.Header.Get("Location"), "/login") {
		t.Fatalf("anonymous read of a private repo: status %d, Location %q — want the login redirect",
			res.StatusCode, res.Header.Get("Location"))
	}
}

// ---- who gets the live board ----------------------------------------------

// mdtoLiveHub seeds one instance with the fixture files, a signed-in owner, a
// read-only collaborator, and a write collaborator — the three answers the Hub
// can give to "may this viewer write?".
func mdtoLiveHub(t *testing.T) (*httptest.Server, *Server, *AccountStore) {
	t.Helper()
	ts, srv, acc := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", mdtoRepoFiles)
	for _, name := range []string{"bob", "carol"} {
		if _, err := acc.CreateUser(name, name+"@example.com", "pw12345678"); err != nil {
			t.Fatal(err)
		}
	}
	if err := acc.AddCollaborator("alice", "brain", "bob", "read"); err != nil {
		t.Fatal(err)
	}
	if err := acc.AddCollaborator("alice", "brain", "carol", "write"); err != nil {
		t.Fatal(err)
	}
	return ts, srv, acc
}

// mdtoGet fetches a page as some viewer ("" = anonymous).
func mdtoGet(t *testing.T, ts *httptest.Server, srv *Server, viewer, path string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if viewer != "" {
		req.AddCookie(sessionCookieFor(srv, viewer))
	}
	res, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return res, string(body)
}

// TestMdtoLiveOnlyForWriters is the gate, stated once for every kind of viewer
// this Hub has. Write access earns the board; nothing else does, and a reader's
// page is the script-less one it has always been.
func TestMdtoLiveOnlyForWriters(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	const page = "/alice/brain/mdto/apps/board.md"
	const save = "/alice/brain/mdto/apps/board.md"

	for _, viewer := range []string{"alice", "carol"} {
		res, body := mdtoGet(t, ts, srv, viewer, page)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", viewer, res.StatusCode)
		}
		assertMdtoLivePage(t, res, body, mdtoKanban, save, sourceHash([]byte(mdtoKanban)))
	}

	// A read collaborator on the same private instance.
	res, body := mdtoGet(t, ts, srv, "bob", page)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reader: status = %d, want 200", res.StatusCode)
	}
	assertMdtoPage(t, res, body, mdtoKanban)

	// An anonymous visitor to a PUBLIC instance reads the same route, and read
	// is all it is: the route gate lets them in, and the page they get names no
	// way to save.
	if err := srv.setVisibility("alice", "brain", visPublic); err != nil {
		t.Fatal(err)
	}
	res, body = mdtoGet(t, ts, srv, "", page)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("anonymous on a public instance: status = %d, want 200", res.StatusCode)
	}
	assertMdtoPage(t, res, body, mdtoKanban)
}

// ---- writeback -------------------------------------------------------------

// mdtoSave PUTs bytes back the way the page does: same origin, session cookie,
// If-Match. ifMatch is sent verbatim when non-empty, so a test can send a stale
// hash, a bare one, or none at all.
func mdtoSave(t *testing.T, ts *httptest.Server, srv *Server, viewer, path, ifMatch, content string, tweak func(*http.Request)) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, ts.URL+path, strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/markdown; charset=utf-8")
	req.Header.Set("Origin", ts.URL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	if viewer != "" {
		req.AddCookie(sessionCookieFor(srv, viewer))
	}
	if tweak != nil {
		tweak(req)
	}
	res, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	json.Unmarshal(raw, &out)
	return res, out
}

// mdtoGitOut runs one read-only git command in a bare repo and returns stdout,
// so the assertions below are about what git holds rather than what an API says
// about it.
func mdtoGitOut(t *testing.T, bare string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", bare}, args...)...)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// mdtoFileAt reads a file's committed bytes straight out of the bare repo, so
// the assertions are about what git holds and not about what an API says.
func mdtoFileAt(t *testing.T, srv *Server, user, repo, filePath string) string {
	t.Helper()
	content, ok := BlobContent("git", srv.Storage.RepoDir(user, repo), defaultRef, filePath)
	if !ok {
		t.Fatalf("%s/%s: %s is gone", user, repo, filePath)
	}
	return content
}

// mdtoMoved is what the patch engine produces from mdtoKanban when the one open
// card is dragged into Done: the card's line moves, its state flips, and NOTHING
// else in the file is touched — CRLF endings, the emoji, the quoted envelope and
// the HTML-looking title all survive. Writeback has to carry bytes, not a
// re-serialization, and this fixture is what proves it.
const mdtoMoved = "---\r\n" +
	"markdownto: \"kanban@0.1\"\r\n" +
	"title: Launch board 🚀\r\n" +
	"---\r\n" +
	"\r\n" +
	"## Backlog\r\n" +
	"\r\n" +
	"## Done\r\n" +
	"\r\n" +
	"- [x] Choose the envelope\r\n" +
	"- [x] Write the <script>alert(1)</script> card\r\n"

// TestMdtoSaveCommits is the loop, end to end: the bytes a board would post
// become a real commit, byte for byte, and the response hands back the If-Match
// for the next mutation.
func TestMdtoSaveCommits(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	const path = "/alice/brain/mdto/apps/board.md"

	res, body := mdtoSave(t, ts, srv, "alice", path, `"`+sourceHash([]byte(mdtoKanban))+`"`, mdtoMoved, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", res.StatusCode, body)
	}
	if got := mdtoFileAt(t, srv, "alice", "brain", "apps/board.md"); got != mdtoMoved {
		t.Errorf("committed bytes are not the board's bytes:\ngot  %q\nwant %q", got, mdtoMoved)
	}
	if got, want := body["hash"], sourceHash([]byte(mdtoMoved)); got != want {
		t.Errorf("response hash = %v, want %v", got, want)
	}
	if body["committed"] != true {
		t.Errorf("response says committed = %v, want true", body["committed"])
	}
	if got, want := res.Header.Get("ETag"), `"`+sourceHash([]byte(mdtoMoved))+`"`; got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}

	// git log says a person did it, through a named front door.
	log := mdtoGitOut(t, srv.Storage.RepoDir("alice", "brain"), "log", "-1", "--format=%an%n%s%n%b")
	if !strings.Contains(log, "alice") {
		t.Errorf("the commit is not authored by the person who dragged the card:\n%s", log)
	}
	if !strings.Contains(log, "Update board.md") {
		t.Errorf("commit subject = %q", log)
	}
	if !strings.Contains(log, "Via: Markdown To board (agentsfs hub)") {
		t.Errorf("commit does not record the front door it came through:\n%s", log)
	}

	// The hash the save handed back is the one the next save must present, with
	// no round trip through GET.
	next := strings.Replace(mdtoMoved, "## Backlog\r\n\r\n", "## Backlog\r\n\r\n- [ ] Another card\r\n\r\n", 1)
	res2, body2 := mdtoSave(t, ts, srv, "alice", path, `"`+body["hash"].(string)+`"`, next, nil)
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("second save status = %d, want 200: %v", res2.StatusCode, body2)
	}
	if got := mdtoFileAt(t, srv, "alice", "brain", "apps/board.md"); got != next {
		t.Error("the second mutation did not land")
	}
}

// TestMdtoSaveWriteCollaborator: the gate is write access, not ownership.
func TestMdtoSaveWriteCollaborator(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	res, body := mdtoSave(t, ts, srv, "carol", "/alice/brain/mdto/apps/board.md",
		`"`+sourceHash([]byte(mdtoKanban))+`"`, mdtoMoved, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", res.StatusCode, body)
	}
	log := mdtoGitOut(t, srv.Storage.RepoDir("alice", "brain"), "log", "-1", "--format=%an")
	if !strings.Contains(log, "carol") {
		t.Errorf("the commit is attributed to %q, want carol", strings.TrimSpace(log))
	}
}

// TestMdtoSaveConflictNeverOverwrites is the whole point of the hash. A board
// held open while the file moved underneath it may not win.
func TestMdtoSaveConflictNeverOverwrites(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	const path = "/alice/brain/mdto/apps/board.md"
	stale := `"` + sourceHash([]byte(mdtoKanban)) + `"`

	// Somebody else commits first — here through the /api/v1 save API with a
	// PAT, which is exactly the concurrency the two surfaces have to survive.
	elsewhere := strings.Replace(mdtoKanban, "## Done", "## Doing\r\n\r\n## Done", 1)
	if _, err := srv.RepoCommit("alice", apiCommitRequest{
		Repo: "alice/brain", BaseRev: srv.RepoResolve("alice", "brain"),
		Message: "Add a column from somewhere else",
		Changes: []apiChange{{Path: "apps/board.md", Content: elsewhere}},
	}); err != nil {
		t.Fatal(err)
	}

	res, body := mdtoSave(t, ts, srv, "alice", path, stale, mdtoMoved, nil)
	if res.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412: %v", res.StatusCode, body)
	}
	if got, want := body["hash"], sourceHash([]byte(elsewhere)); got != want {
		t.Errorf("412 hash = %v, want the CURRENT hash %v — the page cannot recover without it", got, want)
	}
	if got := mdtoFileAt(t, srv, "alice", "brain", "apps/board.md"); got != elsewhere {
		t.Error("a stale save overwrote the file that moved underneath it")
	}

	// And the recovery really is one step: re-read, rebase, retry.
	rebased := strings.Replace(elsewhere, "- [ ] Write the <script>alert(1)</script> card\r\n\r\n", "", 1)
	rebased = strings.Replace(rebased, "- [x] Choose the envelope\r\n",
		"- [x] Choose the envelope\r\n- [x] Write the <script>alert(1)</script> card\r\n", 1)
	res2, body2 := mdtoSave(t, ts, srv, "alice", path, `"`+body["hash"].(string)+`"`, rebased, nil)
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d, want 200: %v", res2.StatusCode, body2)
	}
}

// TestMdtoSaveRequiresIfMatch: an unconditional PUT is the silent overwrite the
// hash model exists to prevent, and the file here always exists.
func TestMdtoSaveRequiresIfMatch(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	res, body := mdtoSave(t, ts, srv, "alice", "/alice/brain/mdto/apps/board.md", "", mdtoMoved, nil)
	if res.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("status = %d, want 428: %v", res.StatusCode, body)
	}
	if got, want := body["hash"], sourceHash([]byte(mdtoKanban)); got != want {
		t.Errorf("428 hash = %v, want %v", got, want)
	}
	if got := mdtoFileAt(t, srv, "alice", "brain", "apps/board.md"); got != mdtoKanban {
		t.Error("an unconditional save landed")
	}
}

// TestMdtoSaveIsSameOriginOnly: the credential is an ambient session cookie, so
// the request itself has to prove where it came from. Nothing here depends on
// the browser's SameSite behaviour, which is the layer underneath.
func TestMdtoSaveIsSameOriginOnly(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	match := `"` + sourceHash([]byte(mdtoKanban)) + `"`

	for _, tc := range []struct {
		name  string
		tweak func(*http.Request)
	}{
		{"another site", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }},
		{"no origin at all", func(r *http.Request) { r.Header.Del("Origin") }},
		{"a cross-site fetch that kept the origin", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
	} {
		res, _ := mdtoSave(t, ts, srv, "alice", "/alice/brain/mdto/apps/board.md", match, mdtoMoved, tc.tweak)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", tc.name, res.StatusCode)
		}
	}
	if got := mdtoFileAt(t, srv, "alice", "brain", "apps/board.md"); got != mdtoKanban {
		t.Error("a cross-origin save landed")
	}
}

// TestMdtoSaveNeedsWriteAccess: the page a reader gets never offers to save, and
// the route behind it refuses anyway.
func TestMdtoSaveNeedsWriteAccess(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	const path = "/alice/brain/mdto/apps/board.md"
	match := `"` + sourceHash([]byte(mdtoKanban)) + `"`

	res, _ := mdtoSave(t, ts, srv, "bob", path, match, mdtoMoved, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("read collaborator: status = %d, want 403", res.StatusCode)
	}

	// Anonymous, on a public instance — the read route admits them and the save
	// route does not.
	if err := srv.setVisibility("alice", "brain", visPublic); err != nil {
		t.Fatal(err)
	}
	res, _ = mdtoSave(t, ts, srv, "", path, match, mdtoMoved, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous on a public instance: status = %d, want 401", res.StatusCode)
	}
	if got := mdtoFileAt(t, srv, "alice", "brain", "apps/board.md"); got != mdtoKanban {
		t.Error("a save without write access landed")
	}
}

// TestMdtoSaveStaysTheSameDocument keeps this route from being a general
// file-write API wearing a board's clothes. A board patches the document it was
// drawn from; it never converts one spec into another and never turns a
// conforming file into a plain note.
func TestMdtoSaveStaysTheSameDocument(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	const path = "/alice/brain/mdto/apps/board.md"
	match := `"` + sourceHash([]byte(mdtoKanban)) + `"`

	for _, tc := range []struct{ name, content string }{
		{"a different spec", strings.Replace(mdtoKanban, `"kanban@0.1"`, `"todo@0.1"`, 1)},
		{"no envelope at all", mdtoPlain},
		{"not markdown any more", "<html><script>alert(1)</script></html>"},
	} {
		res, _ := mdtoSave(t, ts, srv, "alice", path, match, tc.content, nil)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.name, res.StatusCode)
		}
	}
	if got := mdtoFileAt(t, srv, "alice", "brain", "apps/board.md"); got != mdtoKanban {
		t.Error("a save that changed what the document is landed")
	}
}

// TestMdtoSaveNoOpMakesNoCommit: a card dropped back where it came from is a
// mutation the engine can emit, and an empty commit is not what `git log` is
// for. The caller is still told the hash it holds is current.
func TestMdtoSaveNoOpMakesNoCommit(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	before := strings.TrimSpace(mdtoGitOut(t, srv.Storage.RepoDir("alice", "brain"), "rev-parse", "HEAD"))

	res, body := mdtoSave(t, ts, srv, "alice", "/alice/brain/mdto/apps/board.md",
		`"`+sourceHash([]byte(mdtoKanban))+`"`, mdtoKanban, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", res.StatusCode, body)
	}
	if body["committed"] != false {
		t.Errorf("committed = %v, want false", body["committed"])
	}
	if got, want := body["hash"], sourceHash([]byte(mdtoKanban)); got != want {
		t.Errorf("hash = %v, want the unchanged %v", got, want)
	}
	if after := strings.TrimSpace(mdtoGitOut(t, srv.Storage.RepoDir("alice", "brain"), "rev-parse", "HEAD")); after != before {
		t.Error("an identical-bytes save made a commit")
	}
}

// TestMdtoSaveRejectsOtherMethods keeps the route's surface exactly two verbs
// wide.
func TestMdtoSaveRejectsOtherMethods(t *testing.T) {
	ts, srv, _ := mdtoLiveHub(t)
	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/alice/brain/mdto/apps/board.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(sessionCookieFor(srv, "alice"))
	res, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("DELETE status = %d, want 405", res.StatusCode)
	}
	if got := res.Header.Get("Allow"); got != "GET, HEAD, PUT" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD, PUT")
	}
}

// ---- share links ----------------------------------------------------------

// TestSharedConformingFileRenders is the distribution story: an anonymous
// reader follows a share link to a kanban file and meets the board, not the
// source — with the file itself still one click away.
func TestSharedConformingFileRenders(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", mdtoRepoFiles)
	token := mintShareLink(t, ts, srv, "alice", "brain", "apps/board.md", false)

	res, body := getShared(t, ts, "/s/"+token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.StatusCode, body)
	}
	assertMdtoPage(t, res, body, mdtoKanban)
	if !strings.Contains(body, "/s/"+token+"?view=markdown") {
		t.Errorf("shared rendering has no plain-markdown escape hatch:\n%s", body)
	}
	if !strings.Contains(body, "/s/"+token+"?download=1") {
		t.Error("shared rendering has no download")
	}
	if strings.Contains(body, `class="masthead"`) || strings.Contains(body, "/logout") {
		t.Error("shared rendering leaked the signed-in hub chrome")
	}

	// The escape hatch really is one: it serves the ordinary shared markdown,
	// with a way back.
	plainRes, plain := getShared(t, ts, "/s/"+token+"?view=markdown")
	if plainRes.StatusCode != http.StatusOK {
		t.Fatalf("plain view status = %d, want 200", plainRes.StatusCode)
	}
	if strings.Contains(plain, "mdto-stage") {
		t.Error("?view=markdown still served the rendering page")
	}
	if !strings.Contains(plain, "Shared via AgentsFS Hub") {
		t.Error("?view=markdown did not serve the public markdown chrome")
	}
	if !strings.Contains(plain, `href="/s/`+token+`"`) {
		t.Error("plain view offers no way back to the rendered view")
	}

	// Download .md: the exact bytes, as an attachment.
	dlRes, dl := getShared(t, ts, "/s/"+token+"?download=1")
	if dlRes.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want 200", dlRes.StatusCode)
	}
	if dl != mdtoKanban {
		t.Error("shared download is not byte-identical to the committed file")
	}
	if got := dlRes.Header.Get("Content-Disposition"); !strings.Contains(got, `attachment; filename="board.md"`) {
		t.Errorf("Content-Disposition = %q, want an attachment", got)
	}
	if got := dlRes.Header.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("download Content-Type = %q, want application/octet-stream", got)
	}
}

// TestSharedBrokenFileRendersTheReport: an honest validation report is the
// read-only view of a file that does not parse, and it travels the same
// script-less path. The Hub cannot know it is broken — it only knows the
// envelope — so what this asserts is that the same page is served and the
// engine is left to say so.
func TestSharedBrokenFileRendersTheReport(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", mdtoRepoFiles)
	token := mintShareLink(t, ts, srv, "alice", "brain", "apps/broken.md", false)

	res, body := getShared(t, ts, "/s/"+token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.StatusCode, body)
	}
	assertMdtoPage(t, res, body, mdtoBroken)
}

// TestSharedPlainNoteUnchanged: everything without an envelope keeps the share
// view it has always had, headers and all.
func TestSharedPlainNoteUnchanged(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", mdtoRepoFiles)
	token := mintShareLink(t, ts, srv, "alice", "brain", "NOTE.md", false)

	res, body := getShared(t, ts, "/s/"+token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.StatusCode, body)
	}
	if strings.Contains(body, "mdto-stage") || strings.Contains(body, mdtoBundle.href) {
		t.Errorf("a note with no envelope was served the Markdown To page:\n%s", body)
	}
	if res.Header.Get("Content-Security-Policy") != "" {
		t.Error("the plain share view unexpectedly grew a CSP")
	}
	if !strings.Contains(body, "Shared via AgentsFS Hub") {
		t.Error("plain share view lost its chrome")
	}
}
