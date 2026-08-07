package hub

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The share fixture: a PRIVATE repo whose root note embeds an image, links to
// another note two ways (markdown link + wikilink), embeds a markdown file (the
// type gate must refuse to publish it), and leaves SECRET.md unreferenced.
var shareRepoFiles = map[string]string{
	"NOTE.md": "# Root note\n\n![diagram](img/diagram.png)\n\n![sneaky](SECRET.md)\n\n" +
		"See [linked](LINKED.md) and [[WIKI]].\n",
	"LINKED.md":       "# Linked\n\n![shot](img/shot.png)\n\nAlso [secret](SECRET.md).\n",
	"WIKI.md":         "# Wiki\n",
	"SECRET.md":       "# Secret\n\nnot shared\n",
	"page.html":       testRenderHTML,
	"img/diagram.png": "PNGDIAGRAM",
	"img/shot.png":    "PNGSHOT",
	"img/unused.png":  "PNGUNUSED",
}

// seedShareRepo pushes files into a bare repo under the hub's storage and
// returns the work tree, so a test can land a second commit on the same repo.
func seedShareRepo(t *testing.T, srv *Server, user, repo string, files map[string]string) string {
	t.Helper()
	work := t.TempDir()
	runGit(t, work, "init", "-q", "-b", "main")
	writeShareFiles(t, work, files)
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "seed")
	runGit(t, "", "clone", "--bare", work, srv.Storage.RepoDir(user, repo))
	return work
}

func writeShareFiles(t *testing.T, work string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(work, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// pushShareCommit lands another commit on the seeded repo, so tests can prove
// share links follow HEAD instead of the mint-time snapshot.
func pushShareCommit(t *testing.T, srv *Server, work, user, repo string, files map[string]string) {
	t.Helper()
	writeShareFiles(t, work, files)
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "update")
	runGit(t, work, "push", srv.Storage.RepoDir(user, repo), "main")
}

func newShareTestHub(t *testing.T) (*httptest.Server, *Server, *AccountStore) {
	t.Helper()
	ts, srv, acc := newDeleteTestServer(t)
	if _, err := acc.CreateUser("alice", "alice@example.com", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	return ts, srv, acc
}

func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// postShare submits the share management form. auth applies whatever
// credentials the caller wants to test with (nil = anonymous).
func postShare(t *testing.T, ts *httptest.Server, user, repo, filePath string, form url.Values, auth func(*http.Request)) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/"+user+"/"+repo+"/share/"+filePath, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if auth != nil {
		auth(req)
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

var shareTokenRe = regexp.MustCompile(`/s/(shr_[A-Za-z0-9_-]+)`)

// mintShareLink creates a link the way the owner does — a browser form POST
// with a session cookie — and returns the raw token from the one-time URL.
func mintShareLink(t *testing.T, ts *httptest.Server, srv *Server, user, repo, filePath string, includeLinked bool) string {
	t.Helper()
	form := url.Values{"action": {"create"}}
	if includeLinked {
		form.Set("includeLinked", "1")
	}
	cookie := sessionCookieFor(srv, user)
	res, body := postShare(t, ts, user, repo, filePath, form, func(r *http.Request) { r.AddCookie(cookie) })
	if res.StatusCode != http.StatusOK {
		t.Fatalf("mint share link status = %d, want 200: %s", res.StatusCode, body)
	}
	m := shareTokenRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("share page did not show a share URL: %s", body)
	}
	return m[1]
}

func getShared(t *testing.T, ts *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
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

// TestShareLinkServesPrivateFileAnonymously is the feature in one test: an
// owner mints a link from the browser and a signed-out stranger reads exactly
// that file out of a private repo, in public chrome, with hub URLs rewritten.
func TestShareLinkServesPrivateFileAnonymously(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", shareRepoFiles)

	token := mintShareLink(t, ts, srv, "alice", "brain", "NOTE.md", false)

	res, body := getShared(t, ts, "/s/"+token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("shared note status = %d, want 200: %s", res.StatusCode, body)
	}
	for header, want := range map[string]string{
		"X-Robots-Tag":           "noindex, nofollow",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
		"Cache-Control":          "no-store",
	} {
		if got := res.Header.Get(header); got != want {
			t.Errorf("shared note %s = %q, want %q", header, got, want)
		}
	}
	if !strings.Contains(body, `src="/s/`+token+`/a/img/diagram.png"`) {
		t.Errorf("shared note did not rewrite its image to the token asset route: %s", body)
	}
	if !strings.Contains(body, "Shared via AgentsFS Hub") {
		t.Error("shared note is missing the public chrome footer")
	}
	if strings.Contains(body, `class="masthead"`) || strings.Contains(body, "/logout") {
		t.Error("shared note leaked the signed-in hub chrome")
	}
	// The type gate reaches the renderer too: a markdown file embedded as an
	// image must never be handed a working URL.
	if strings.Contains(body, "/a/SECRET.md") {
		t.Error("shared note linked a markdown file as an image asset")
	}
	// includeLinked was off, so links point back at the (still private) hub.
	if !strings.Contains(body, `href="/alice/brain/blob/LINKED.md"`) {
		t.Errorf("uncovered link should point at the hub URL: %s", body)
	}
	if strings.Contains(body, "/p/LINKED.md") {
		t.Error("uncovered link points into the share space")
	}
}

// TestShareLinkHTMLRootUsesRenderPath: an .html root is served through the same
// opaque-origin sandbox as /render, not as escaped source or a bare document.
func TestShareLinkHTMLRootUsesRenderPath(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", shareRepoFiles)

	token := mintShareLink(t, ts, srv, "alice", "brain", "page.html", false)
	res, body := getShared(t, ts, "/s/"+token)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("shared html status = %d, want 200", res.StatusCode)
	}
	if body != testRenderHTML {
		t.Errorf("shared html body = %q, want the stored HTML", body)
	}
	if got := res.Header.Get("Content-Security-Policy"); got != htmlRenderCSP {
		t.Errorf("shared html CSP = %q, want htmlRenderCSP", got)
	}
	if got := res.Header.Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Errorf("shared html X-Robots-Tag = %q", got)
	}
	if got := res.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("shared html Content-Type = %q", got)
	}
}

// TestShareLinkMintRequiresOwnerSession pins the authorization: a PAT (which
// lives on agent VMs) cannot publish, a collaborator cannot publish, and an
// anonymous caller gets the usual login treatment.
func TestShareLinkMintRequiresOwnerSession(t *testing.T) {
	ts, srv, acc := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", shareRepoFiles)
	if _, err := acc.CreateUser("bob", "bob@example.com", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	if err := acc.AddCollaborator("alice", "brain", "bob", "write"); err != nil {
		t.Fatal(err)
	}
	pat, err := acc.CreatePAT("alice", "agent")
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"action": {"create"}}

	res, _ := postShare(t, ts, "alice", "brain", "NOTE.md", form, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+pat)
	})
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("PAT mint status = %d, want 403", res.StatusCode)
	}

	bob := sessionCookieFor(srv, "bob")
	res, _ = postShare(t, ts, "alice", "brain", "NOTE.md", form, func(r *http.Request) { r.AddCookie(bob) })
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("collaborator mint status = %d, want 403", res.StatusCode)
	}

	res, _ = postShare(t, ts, "alice", "brain", "NOTE.md", form, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("anonymous mint status = %d, want 401", res.StatusCode)
	}

	// The share page itself is owner-only too.
	anonRes, _ := getShared(t, ts, "/alice/brain/share/NOTE.md")
	if anonRes.StatusCode != http.StatusFound {
		t.Errorf("anonymous share page status = %d, want a 302 login redirect", anonRes.StatusCode)
	}
	if len(acc.ListShareLinks("alice", "brain")) != 0 {
		t.Fatal("a rejected mint still created a share link")
	}

	// The file page offers the entry point to the owner only — a write
	// collaborator can edit the note but not decide to publish it.
	const shareHref = `href="/alice/brain/share/NOTE.md"`
	if _, page := getPageAs(t, ts, "/alice/brain/blob/NOTE.md", sessionCookieFor(srv, "alice")); !strings.Contains(page, shareHref) {
		t.Error("owner's file page is missing the Share link")
	}
	if _, page := getPageAs(t, ts, "/alice/brain/blob/NOTE.md", bob); strings.Contains(page, shareHref) {
		t.Error("collaborator's file page offers a Share link")
	}
}

func getPageAs(t *testing.T, ts *httptest.Server, path string, cookie *http.Cookie) (*http.Response, string) {
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
	return res, string(body)
}

// TestShareLinkIncludeLinked covers the one-hop opt-in: off, /p/ is closed; on,
// both link syntaxes open exactly their targets and nothing else.
func TestShareLinkIncludeLinked(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", shareRepoFiles)

	closed := mintShareLink(t, ts, srv, "alice", "brain", "NOTE.md", false)
	if res, _ := getShared(t, ts, "/s/"+closed+"/p/LINKED.md"); res.StatusCode != http.StatusNotFound {
		t.Errorf("linked page without includeLinked = %d, want 404", res.StatusCode)
	}

	open := mintShareLink(t, ts, srv, "alice", "brain", "NOTE.md", true)
	for _, p := range []string{"LINKED.md", "WIKI.md"} {
		res, body := getShared(t, ts, "/s/"+open+"/p/"+p)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("linked page %s = %d, want 200", p, res.StatusCode)
		}
		if !strings.Contains(body, "Shared via AgentsFS Hub") {
			t.Errorf("linked page %s is not in the public chrome", p)
		}
	}
	if res, _ := getShared(t, ts, "/s/"+open+"/p/SECRET.md"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unlinked page = %d, want 404", res.StatusCode)
	}

	root, body := getShared(t, ts, "/s/"+open)
	if root.StatusCode != http.StatusOK {
		t.Fatalf("shared root = %d, want 200", root.StatusCode)
	}
	for _, want := range []string{`href="/s/` + open + `/p/LINKED.md"`, `href="/s/` + open + `/p/WIKI.md"`} {
		if !strings.Contains(body, want) {
			t.Errorf("covered link missing %q in: %s", want, body)
		}
	}

	// A linked page renders under the same rules: its own image goes through
	// /a/, and its link to an uncovered file leaves the share space.
	_, linked := getShared(t, ts, "/s/"+open+"/p/LINKED.md")
	if !strings.Contains(linked, `src="/s/`+open+`/a/img/shot.png"`) {
		t.Errorf("linked page did not rewrite its image: %s", linked)
	}
	if !strings.Contains(linked, `href="/alice/brain/blob/SECRET.md"`) {
		t.Errorf("linked page's uncovered link should point at the hub: %s", linked)
	}
}

// TestShareLinkAssetRouteDoubleGate: /a/ serves only what the covered pages
// actually embed, and only types the hub is willing to inline.
func TestShareLinkAssetRouteDoubleGate(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", shareRepoFiles)

	closed := mintShareLink(t, ts, srv, "alice", "brain", "NOTE.md", false)

	res, body := getShared(t, ts, "/s/"+closed+"/a/img/diagram.png")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("embedded image = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("embedded image Content-Type = %q, want image/png", got)
	}
	if body != shareRepoFiles["img/diagram.png"] {
		t.Errorf("embedded image body = %q", body)
	}
	if got := res.Header.Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Errorf("embedded image X-Robots-Tag = %q", got)
	}

	// In the repo, but not embedded by a covered page.
	if res, _ := getShared(t, ts, "/s/"+closed+"/a/img/unused.png"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unembedded image = %d, want 404", res.StatusCode)
	}
	// Embedded, but markdown: the type gate must refuse regardless.
	if res, _ := getShared(t, ts, "/s/"+closed+"/a/SECRET.md"); res.StatusCode != http.StatusNotFound {
		t.Errorf("markdown claimed as an embed = %d, want 404", res.StatusCode)
	}
	// A linked page's embed only opens when the linked pages are shared.
	if res, _ := getShared(t, ts, "/s/"+closed+"/a/img/shot.png"); res.StatusCode != http.StatusNotFound {
		t.Errorf("linked page's embed without includeLinked = %d, want 404", res.StatusCode)
	}
	open := mintShareLink(t, ts, srv, "alice", "brain", "NOTE.md", true)
	if res, _ := getShared(t, ts, "/s/"+open+"/a/img/shot.png"); res.StatusCode != http.StatusOK {
		t.Errorf("linked page's embed with includeLinked = %d, want 200", res.StatusCode)
	}
}

// TestShareLinkRevokeAndUnknownToken: revocation is immediate, and a revoked
// token is indistinguishable from one that never existed.
func TestShareLinkRevokeAndUnknownToken(t *testing.T) {
	ts, srv, acc := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", shareRepoFiles)

	token := mintShareLink(t, ts, srv, "alice", "brain", "NOTE.md", false)
	if res, _ := getShared(t, ts, "/s/"+token); res.StatusCode != http.StatusOK {
		t.Fatalf("fresh share link = %d, want 200", res.StatusCode)
	}

	links := acc.ListShareLinksForPath("alice", "brain", "NOTE.md")
	if len(links) != 1 {
		t.Fatalf("ListShareLinksForPath = %d links, want 1", len(links))
	}
	cookie := sessionCookieFor(srv, "alice")
	res, body := postShare(t, ts, "alice", "brain", "NOTE.md", url.Values{
		"action": {"revoke"}, "id": {itoa(links[0].ID)},
	}, func(r *http.Request) { r.AddCookie(cookie) })
	if res.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200: %s", res.StatusCode, body)
	}
	if res, _ := getShared(t, ts, "/s/"+token); res.StatusCode != http.StatusNotFound {
		t.Errorf("revoked share link = %d, want 404", res.StatusCode)
	}
	if res, _ := getShared(t, ts, "/s/shr_nope"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown token = %d, want 404", res.StatusCode)
	}
}

// TestShareLinkTracksHead: the link publishes the file, not a snapshot — a new
// commit moves what /a/ will serve.
func TestShareLinkTracksHead(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	work := seedShareRepo(t, srv, "alice", "brain", shareRepoFiles)

	token := mintShareLink(t, ts, srv, "alice", "brain", "NOTE.md", false)
	if res, _ := getShared(t, ts, "/s/"+token+"/a/img/diagram.png"); res.StatusCode != http.StatusOK {
		t.Fatalf("embedded image before the update = %d, want 200", res.StatusCode)
	}

	pushShareCommit(t, srv, work, "alice", "brain", map[string]string{
		"NOTE.md":       "# Root note\n\n![diagram](img/fresh.png)\n",
		"img/fresh.png": "PNGFRESH",
	})

	if res, _ := getShared(t, ts, "/s/"+token+"/a/img/fresh.png"); res.StatusCode != http.StatusOK {
		t.Errorf("newly embedded image = %d, want 200", res.StatusCode)
	}
	if res, _ := getShared(t, ts, "/s/"+token+"/a/img/diagram.png"); res.StatusCode != http.StatusNotFound {
		t.Errorf("no-longer-embedded image = %d, want 404", res.StatusCode)
	}
}

// TestShareLinkLeavesRepoPrivacyAlone: a live share link opens exactly one URL
// space, never the repo's own routes.
func TestShareLinkLeavesRepoPrivacyAlone(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", shareRepoFiles)

	token := mintShareLink(t, ts, srv, "alice", "brain", "NOTE.md", true)
	if res, _ := getShared(t, ts, "/s/"+token); res.StatusCode != http.StatusOK {
		t.Fatalf("share link = %d, want 200", res.StatusCode)
	}
	for _, p := range []string{"/alice/brain/blob/NOTE.md", "/alice/brain/raw/img/diagram.png", "/alice/brain"} {
		res, _ := getShared(t, ts, p)
		if res.StatusCode != http.StatusFound || !strings.HasPrefix(res.Header.Get("Location"), "/login?next=") {
			t.Errorf("anonymous %s = %d (Location %q), want a login redirect", p, res.StatusCode, res.Header.Get("Location"))
		}
	}
}

// TestShareLinkNonMarkdownRoots covers the remaining per-type branches: plain
// text reads in the public chrome, and anything the hub won't inline is handed
// over as an opaque download rather than rendered on this origin.
func TestShareLinkNonMarkdownRoots(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "notes", map[string]string{
		"notes.txt":  "plain <b>text</b> here\n",
		"archive.7z": "\x00\x01binary payload",
	})

	textToken := mintShareLink(t, ts, srv, "alice", "notes", "notes.txt", false)
	res, body := getShared(t, ts, "/s/"+textToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("shared text = %d, want 200", res.StatusCode)
	}
	if !strings.Contains(body, "Shared via AgentsFS Hub") || !strings.Contains(body, "plain &lt;b&gt;text&lt;/b&gt; here") {
		t.Errorf("shared text is not escaped source in the public chrome: %s", body)
	}

	binToken := mintShareLink(t, ts, srv, "alice", "notes", "archive.7z", false)
	res, _ = getShared(t, ts, "/s/"+binToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("shared binary = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Errorf("shared binary Content-Disposition = %q, want attachment", got)
	}
	if got := res.Header.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("shared binary Content-Type = %q, want application/octet-stream", got)
	}

	// The share space is read-only.
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/s/"+textToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	post, err := noRedirectClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	post.Body.Close()
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST to a share link = %d, want 405", post.StatusCode)
	}
}

// TestRobotsDisallowsShareSpace keeps published links out of search indexes at
// the crawler's first stop, backing up the per-response X-Robots-Tag.
func TestRobotsDisallowsShareSpace(t *testing.T) {
	ts, _, _ := newShareTestHub(t)
	res, body := getShared(t, ts, "/robots.txt")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("robots.txt = %d, want 200", res.StatusCode)
	}
	if !strings.Contains(body, "Disallow: /s/") {
		t.Errorf("robots.txt is missing Disallow: /s/\n%s", body)
	}
}

// TestShareReservedName: "s" cannot be claimed as a username, or a signup would
// shadow the whole share space.
func TestShareReservedName(t *testing.T) {
	if !isReserved("s") {
		t.Error(`"s" must be reserved for the /s/ share routes`)
	}
}

// TestSettingsShareLinkAudit: the repo's settings page is the one place an
// owner can see everything this knowledge base publishes to the open web — the
// per-file share page only ever knows about one file. Revoking from that list
// is the same session-only action as minting, and takes the URL down at once.
func TestSettingsShareLinkAudit(t *testing.T) {
	ts, srv, acc := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", shareRepoFiles)
	cookie := sessionCookieFor(srv, "alice")

	token := mintShareLink(t, ts, srv, "alice", "brain", "NOTE.md", true)
	links := acc.ListShareLinks("alice", "brain")
	if len(links) != 1 {
		t.Fatalf("ListShareLinks = %d links, want 1", len(links))
	}

	_, page := getPageAs(t, ts, "/alice/brain/settings", cookie)
	for _, want := range []string{
		`href="/alice/brain/blob/NOTE.md"`,  // the row points at the published file
		"includes linked pages",             // the one-hop opt-in is visible here
		`value="revoke-share-link"`,         // ...and revocable without leaving
		`value="` + itoa(links[0].ID) + `"`, // by id, so the row is unambiguous
	} {
		if !strings.Contains(page, want) {
			t.Errorf("settings page share links section is missing %q: %s", want, page)
		}
	}
	if strings.Contains(page, "No active share links.") {
		t.Error("settings page shows the empty state while a link is live")
	}

	// A PAT drives most of this page, but not this: publishing is human-only
	// (PATs live on agent VMs), so unpublishing must not be weaker than that.
	pat, err := acc.CreatePAT("alice", "agent")
	if err != nil {
		t.Fatal(err)
	}
	res := postSettings(t, ts, "alice", "brain", url.Values{
		"action": {"revoke-share-link"}, "id": {itoa(links[0].ID)},
	}, nil, "alice", pat)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PAT revoke status = %d, want 200 (a rendered refusal)", res.StatusCode)
	}
	if r, _ := getShared(t, ts, "/s/"+token); r.StatusCode != http.StatusOK {
		t.Fatalf("share link after a PAT revoke = %d, want 200 (still live)", r.StatusCode)
	}

	res = postSettings(t, ts, "alice", "brain", url.Values{
		"action": {"revoke-share-link"}, "id": {itoa(links[0].ID)},
	}, cookie, "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d, want 200", res.StatusCode)
	}
	if r, _ := getShared(t, ts, "/s/"+token); r.StatusCode != http.StatusNotFound {
		t.Errorf("revoked share link = %d, want 404", r.StatusCode)
	}
	if n := len(acc.ListShareLinks("alice", "brain")); n != 0 {
		t.Errorf("ListShareLinks after revoke = %d, want 0", n)
	}
	if _, after := getPageAs(t, ts, "/alice/brain/settings", cookie); !strings.Contains(after, "No active share links.") {
		t.Errorf("settings page did not fall back to the empty state after revoking: %s", after)
	}
}

// TestSettingsShareLinksEmptyState: a knowledge base that publishes nothing
// says so plainly — an empty section would read as a missing feature.
func TestSettingsShareLinksEmptyState(t *testing.T) {
	ts, srv, _ := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "quiet", shareRepoFiles)

	_, page := getPageAs(t, ts, "/alice/quiet/settings", sessionCookieFor(srv, "alice"))
	if !strings.Contains(page, "No active share links.") {
		t.Errorf("settings page is missing the share links empty state: %s", page)
	}
	if strings.Contains(page, `value="revoke-share-link"`) {
		t.Error("settings page rendered a revoke form with no share links")
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func TestShareLinkURLStaysCopyable(t *testing.T) {
	ts, srv, acc := newShareTestHub(t)
	seedShareRepo(t, srv, "alice", "brain", shareRepoFiles)
	cookie := sessionCookieFor(srv, "alice")

	token := mintShareLink(t, ts, srv, "alice", "brain", "NOTE.md", false)

	// The raw token is stored by deliberate owner decision (see ShareLink), so
	// both owner-side lists keep showing the full copyable URL after mint.
	links := acc.ListShareLinks("alice", "brain")
	if len(links) != 1 || links[0].Token != token {
		t.Fatalf("stored token mismatch: got %d links", len(links))
	}
	_, share := getPageAs(t, ts, "/alice/brain/share/NOTE.md", cookie)
	if !strings.Contains(share, "/s/"+token) {
		t.Errorf("share page existing-links row is missing the stored URL")
	}
	_, settings := getPageAs(t, ts, "/alice/brain/settings", cookie)
	if !strings.Contains(settings, "/s/"+token) {
		t.Errorf("settings share-links row is missing the stored URL")
	}
}
