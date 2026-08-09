package hub

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"html"
	"io"
	"net/http"
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

// assertMdtoPage checks everything that must be true of a rendering page
// wherever it is served from — the sandbox, the pinned scripts, and the bytes.
func assertMdtoPage(t *testing.T, res *http.Response, body, wantSource string) {
	t.Helper()

	// The security boundary, asserted literally. `allow-downloads` is only what
	// lets the rendered document's own "Download the Markdown" link work; the
	// live board's `allow-scripts` waits on the content-domain decision.
	if !strings.Contains(body, `sandbox="allow-downloads"`) {
		t.Errorf("rendering page is missing the literal sandbox attribute:\n%s", body)
	}
	if strings.Contains(body, "allow-scripts") || strings.Contains(body, "allow-same-origin") {
		t.Error("rendering page widened the iframe sandbox")
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

	// The CSP, which the srcdoc frame inherits: no network from the frame.
	if got := res.Header.Get("Content-Security-Policy"); got != mdtoCSP {
		t.Errorf("Content-Security-Policy = %q, want %q", got, mdtoCSP)
	}
	if !strings.Contains(mdtoCSP, "connect-src 'none'") {
		t.Error("the rendering CSP must forbid network requests from the frame")
	}

	// The escape hatches: the rendering never captures the file.
	if !strings.Contains(body, "View as Markdown") || !strings.Contains(body, "Download .md") {
		t.Errorf("rendering page is missing the plain-view escape hatches:\n%s", body)
	}
	if !strings.Contains(body, playgroundURL) {
		t.Error("rendering page is missing the playground link")
	}

	// Byte fidelity: what the engine parses is what was committed.
	if got := mdtoEmbeddedSource(t, body); got != wantSource {
		t.Errorf("embedded source is not byte-identical to the file:\ngot  %q\nwant %q", got, wantSource)
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

// TestMdtoViewPage is the authenticated rendering page, end to end.
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
	assertMdtoPage(t, res, string(body), mdtoKanban)

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
