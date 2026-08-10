package hub

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testRenderHTML = "<!doctype html>\n<title>Chart</title>\n<h1>Q4 revenue</h1>\n<script>document.title = 'live'</script>\n"

// seedHTMLRepo pushes a repo containing page.html (plus a note) into the hub's
// storage and returns nothing but the server it seeded, mirroring the bare-clone
// pattern the other file-view tests use.
func seedHTMLRepo(t *testing.T, srv *Server, user, repo string, files map[string]string) {
	t.Helper()
	work := t.TempDir()
	runGit(t, work, "init", "-q", "-b", "main")
	for name, body := range files {
		// Seeded paths may be nested (a backlog directory, a collection), and
		// the map's iteration order gives no chance to create the parent first.
		if err := os.MkdirAll(filepath.Dir(filepath.Join(work, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, work, "add", name)
	}
	runGit(t, work, "commit", "-m", "seed")
	runGit(t, "", "clone", "--bare", work, srv.Storage.RepoDir(user, repo))
}

func getNoRedirect(t *testing.T, url string, auth bool) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth {
		req.SetBasicAuth("alice", "s3cret")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
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

// TestRenderHTMLRouteServesSandboxedDocument is the core of the feature: a
// stored .html file comes back as a real document, but with the opaque-origin
// sandbox CSP that keeps it from touching the hub origin.
func TestRenderHTMLRouteServesSandboxedDocument(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	seedHTMLRepo(t, srv, "alice", "brain", map[string]string{
		"page.html": testRenderHTML,
		"NOTE.md":   "# note\n",
	})
	if err := srv.setVisibility("alice", "brain", visPublic); err != nil {
		t.Fatal(err)
	}

	res, body := getNoRedirect(t, ts.URL+"/alice/brain/render/page.html", false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("render status = %d, want 200: %s", res.StatusCode, body)
	}
	if got := res.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("render Content-Type = %q, want text/html; charset=utf-8", got)
	}
	csp := res.Header.Get("Content-Security-Policy")
	for _, want := range []string{
		"sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox",
		"frame-ancestors 'self'",
		"connect-src 'none'",
		"default-src 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("render CSP %q missing %q", csp, want)
		}
	}
	if strings.Contains(csp, "allow-same-origin") {
		t.Errorf("render CSP grants allow-same-origin, which would break the origin boundary: %q", csp)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("render X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := res.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("render Referrer-Policy = %q, want no-referrer", got)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("render Cache-Control = %q, want no-store", got)
	}
	if body != testRenderHTML {
		t.Errorf("render body = %q, want the stored HTML %q", body, testRenderHTML)
	}
}

// TestRenderHTMLRoutePrivateRepoNeedsLogin proves the new route rides the same
// read-authorization block as blob/raw rather than bypassing it.
func TestRenderHTMLRoutePrivateRepoNeedsLogin(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	seedHTMLRepo(t, srv, "alice", "secret", map[string]string{"page.html": testRenderHTML})

	res, _ := getNoRedirect(t, ts.URL+"/alice/secret/render/page.html", false)
	if res.StatusCode != http.StatusFound {
		t.Fatalf("anonymous private render status = %d, want 302", res.StatusCode)
	}
	if got := res.Header.Get("Location"); !strings.HasPrefix(got, "/login?next=") {
		t.Fatalf("anonymous private render Location = %q, want a /login redirect", got)
	}

	// The owner still gets the document.
	ownerRes, ownerBody := getNoRedirect(t, ts.URL+"/alice/secret/render/page.html", true)
	if ownerRes.StatusCode != http.StatusOK || ownerBody != testRenderHTML {
		t.Fatalf("owner private render = status %d, body %q; want 200 and the stored HTML", ownerRes.StatusCode, ownerBody)
	}
}

// TestRenderHTMLRouteRejectsNonHTML keeps the live-document route from becoming
// a generic inline-anything hole.
func TestRenderHTMLRouteRejectsNonHTML(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	seedHTMLRepo(t, srv, "alice", "brain", map[string]string{
		"page.html": testRenderHTML,
		"NOTE.md":   "# note\n",
	})
	if err := srv.setVisibility("alice", "brain", visPublic); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"NOTE.md", "missing.html"} {
		res, _ := getNoRedirect(t, ts.URL+"/alice/brain/render/"+path, true)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("render %s status = %d, want 404", path, res.StatusCode)
		}
	}
	if htmlRenderable("NOTE.md") || !htmlRenderable("Page.HTM") || !htmlRenderable("a/b/page.html") {
		t.Error("htmlRenderable should accept .html/.htm case-insensitively and nothing else")
	}
	if got := filePreviewKind("page.html"); got != "html" {
		t.Errorf("filePreviewKind(page.html) = %q, want html", got)
	}
}

// TestFilePageEmbedsSandboxedHTMLPreview covers the file page swapping escaped
// source for the sandboxed iframe.
func TestFilePageEmbedsSandboxedHTMLPreview(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	seedHTMLRepo(t, srv, "alice", "brain", map[string]string{"page.html": testRenderHTML})
	if err := srv.setVisibility("alice", "brain", visPublic); err != nil {
		t.Fatal(err)
	}

	res, page := getNoRedirect(t, ts.URL+"/alice/brain/blob/page.html", false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("html file page status = %d, want 200", res.StatusCode)
	}
	for _, want := range []string{
		`class="html-preview"`,
		"/alice/brain/render/page.html",
		`sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"`,
		"Open full page",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("html file page missing %q", want)
		}
	}
	// The old path escaped the whole document into a <pre>; it must be gone, or
	// the page would ship the source and the render side by side.
	if strings.Contains(page, "raw-prose") || strings.Contains(page, "Q4 revenue") {
		t.Error("html file page still renders the escaped source")
	}
}

// TestRawHTMLStaysAnAttachment guards the boundary the render route relies on:
// /raw must never serve HTML inline on the hub origin.
func TestRawHTMLStaysAnAttachment(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	big := testRenderHTML + "<p>" + strings.Repeat("padding ", 200) + "</p>\n"
	if int64(len(big)) <= maxLFSPointerBytes {
		t.Fatalf("test fixture is %d bytes, needs to exceed %d", len(big), maxLFSPointerBytes)
	}
	seedHTMLRepo(t, srv, "alice", "brain", map[string]string{"page.html": big})
	if err := srv.setVisibility("alice", "brain", visPublic); err != nil {
		t.Fatal(err)
	}

	res, body := getNoRedirect(t, ts.URL+"/alice/brain/raw/page.html", false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("raw html status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Fatalf("raw html Content-Disposition = %q, want attachment", got)
	}
	if body != big {
		t.Error("raw html body did not round-trip")
	}
}
