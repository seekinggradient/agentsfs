package hub

import (
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// enableTestAgent turns on the agent feature without touching Sprites/OpenAI:
// DevURL alone satisfies AgentManager.Enabled().
func enableTestAgent(srv *Server) {
	m := NewAgentManager("", "", "", "", nil, nil)
	m.DevURL = "http://127.0.0.1:0"
	srv.Agent = m
}

// seedAgentAccessRepo creates alice/kauai with a single markdown note committed,
// via a working clone and a bare push-equivalent (mirrors fileview_media_test.go),
// so the blob page has real content to render.
func seedAgentAccessRepo(t *testing.T, srv *Server) {
	t.Helper()
	work := t.TempDir()
	runGit(t, work, "init", "-q", "-b", "main")
	writeRepoFile(t, work, "NOTE.md", "---\ndescription: hi\n---\n# Note\nbody\n")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "seed")
	bare := srv.Storage.RepoDir("alice", "kauai")
	runGit(t, "", "clone", "--bare", work, bare)
}

// getPage GETs path with an optional session cookie and returns the response
// status and body. cookie == nil means signed-out.
func getPage(t *testing.T, ts *http.Client, url string, cookie *http.Cookie) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	res, err := ts.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(body)
}

// TestAgentDockVisibilityForCollaborators covers the gate relaxation on a
// private repo: any collaborator (read or write), not just the owner, gets the
// agent dock. Only a write collaborator additionally gets the comment button,
// and a collaborator's dock points at the owner-qualified repo param since
// their agent spans repos across multiple owners.
func TestAgentDockVisibilityForCollaborators(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ts, srv, acc := newDeleteTestServer(t)
	if _, err := acc.CreateUser("alice", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.CreateUser("bob", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.CreateUser("dana", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	enableTestAgent(srv)
	seedAgentAccessRepo(t, srv)
	if err := acc.AddCollaborator("alice", "kauai", "bob", "write"); err != nil {
		t.Fatal(err)
	}
	if err := acc.AddCollaborator("alice", "kauai", "dana", "read"); err != nil {
		t.Fatal(err)
	}

	client := ts.Client()
	pageURL := ts.URL + "/alice/kauai/blob/NOTE.md"

	// (owner) the dock is present and points at the bare, unqualified repo slug.
	status, body := getPage(t, client, pageURL, sessionCookieFor(srv, "alice"))
	if status != http.StatusOK {
		t.Fatalf("owner page status = %d", status)
	}
	if !strings.Contains(body, `id="agent-dock"`) {
		t.Error("owner: expected agent dock in page")
	}
	if !strings.Contains(body, `data-agent-url="/agent/?repo=kauai"`) {
		t.Error("owner: expected bare (unqualified) repo param in AgentURL")
	}
	if !strings.Contains(body, "data-comment-toggle") {
		t.Error("owner: expected comment-for-agent button (owner can write)")
	}

	// (a) write collaborator: dock present AND comment button present, and the
	// dock's AgentURL is owner-qualified.
	status, body = getPage(t, client, pageURL, sessionCookieFor(srv, "bob"))
	if status != http.StatusOK {
		t.Fatalf("write collaborator page status = %d", status)
	}
	if !strings.Contains(body, `id="agent-dock"`) {
		t.Error("write collaborator: expected agent dock in page")
	}
	if !strings.Contains(body, `data-agent-url="/agent/?repo=alice%2Fkauai"`) {
		t.Error("write collaborator: expected owner-qualified repo param in AgentURL")
	}
	if !strings.Contains(body, "data-comment-toggle") {
		t.Error("write collaborator: expected comment-for-agent button")
	}

	// (b) read collaborator: dock present, comment button absent.
	status, body = getPage(t, client, pageURL, sessionCookieFor(srv, "dana"))
	if status != http.StatusOK {
		t.Fatalf("read collaborator page status = %d", status)
	}
	if !strings.Contains(body, `id="agent-dock"`) {
		t.Error("read collaborator: expected agent dock in page")
	}
	if !strings.Contains(body, `data-agent-url="/agent/?repo=alice%2Fkauai"`) {
		t.Error("read collaborator: expected owner-qualified repo param in AgentURL")
	}
	if strings.Contains(body, "data-comment-toggle") {
		t.Error("read collaborator: comment-for-agent button should be absent (no write access)")
	}
}

// TestAgentDockPublicRepoStrangerVsAnon covers the read-parity boundary: an
// agent's permissions are exactly its user's permissions, so a signed-in
// non-collaborator sees the dock on a public repo (owner-qualified, since it
// isn't their own namespace) — the same read reach they already have in the
// browser. An anonymous visitor still never sees a dock: there is no agent to
// open for a signed-out viewer, regardless of the repo's visibility.
func TestAgentDockPublicRepoStrangerVsAnon(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ts, srv, acc := newDeleteTestServer(t)
	if _, err := acc.CreateUser("alice", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.CreateUser("carol", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	enableTestAgent(srv)
	seedAgentAccessRepo(t, srv)
	if err := srv.setVisibility("alice", "kauai", visPublic); err != nil {
		t.Fatal(err)
	}

	client := ts.Client()
	pageURL := ts.URL + "/alice/kauai/blob/NOTE.md"

	// (c) signed-in stranger (no collaborator grant) on a public repo: the dock
	// IS present, owner-qualified.
	status, body := getPage(t, client, pageURL, sessionCookieFor(srv, "carol"))
	if status != http.StatusOK {
		t.Fatalf("stranger page status = %d, want 200 (repo is public)", status)
	}
	if !strings.Contains(body, `id="agent-dock"`) {
		t.Error("signed-in non-collaborator on a public repo should see the agent dock (read parity with the browser)")
	}
	if !strings.Contains(body, `data-agent-url="/agent/?repo=alice%2Fkauai"`) {
		t.Error("signed-in stranger on a public repo: expected owner-qualified repo param in AgentURL")
	}
	if strings.Contains(body, "data-comment-toggle") {
		t.Error("signed-in stranger on a public repo should NOT get the comment-for-agent button (no write access)")
	}

	// (d) signed-out visitor on the same public repo: still no dock at all.
	status, body = getPage(t, client, pageURL, nil)
	if status != http.StatusOK {
		t.Fatalf("anon page status = %d, want 200 (repo is public)", status)
	}
	if strings.Contains(body, `id="agent-dock"`) || strings.Contains(body, `class="agent-trigger"`) {
		t.Error("signed-out visitor should not see the agent dock/trigger")
	}
}

// TestAgentButtonOnNonRepoPages covers the "agent button on every page"
// requirement: a signed-in viewer gets a direct agent link even on pages with
// no repo context of their own (account, repo settings), while a page
// reachable while signed out (the marketing home page) never gets one.
func TestAgentButtonOnNonRepoPages(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ts, srv, acc := newDeleteTestServer(t)
	if _, err := acc.CreateUser("alice", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	enableTestAgent(srv)
	seedAgentAccessRepo(t, srv)

	client := ts.Client()

	// /account has no repo context: a signed-in viewer still gets the agent
	// trigger, pointed at the top-level (non-repo-scoped) agent.
	status, body := getPage(t, client, ts.URL+"/account", sessionCookieFor(srv, "alice"))
	if status != http.StatusOK {
		t.Fatalf("account page status = %d", status)
	}
	if !strings.Contains(body, `class="agent-trigger"`) {
		t.Error("signed-in viewer on /account should see the agent trigger")
	}
	if !strings.Contains(body, `data-agent-url="/agent/"`) {
		t.Error("account page AgentURL should be the top-level (non-repo-scoped) agent")
	}

	// The repo settings page has repo context: the owner gets a repo-scoped
	// (bare-slug) agent link, matching the repo/file/history pages' style.
	status, body = getPage(t, client, ts.URL+"/alice/kauai/settings", sessionCookieFor(srv, "alice"))
	if status != http.StatusOK {
		t.Fatalf("settings page status = %d", status)
	}
	if !strings.Contains(body, `class="agent-trigger"`) {
		t.Error("signed-in owner on repo settings should see the agent trigger")
	}
	if !strings.Contains(body, `data-agent-url="/agent/?repo=kauai"`) {
		t.Error("settings page AgentURL should be repo-scoped (bare slug for the owner)")
	}

	// The marketing home page is reachable while signed out and must never
	// carry an agent button.
	status, body = getPage(t, client, ts.URL+"/", nil)
	if status != http.StatusOK {
		t.Fatalf("home page status = %d", status)
	}
	if strings.Contains(body, `id="agent-dock"`) || strings.Contains(body, `class="agent-trigger"`) {
		t.Error("signed-out visitor on the marketing home page should not see an agent button")
	}
}

// TestAdminPagesShowAgentLink covers the admin console's small hand-rolled
// Agent nav link (adminAgentLinkHTML): the raw (non-templated) admin pages
// don't render base.html's trigger+dock, but the signed-in operator still
// gets a direct link to their own agent on both /admin/metrics and
// /admin/access.
func TestAdminPagesShowAgentLink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ts, srv, acc := newDeleteTestServer(t)
	if _, err := acc.CreateUser("alice", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	enableTestAgent(srv)
	srv.AdminUser = "alice"
	mets, err := OpenMetrics(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv.Metrics = mets

	client := ts.Client()

	status, body := getPage(t, client, ts.URL+"/admin/metrics", sessionCookieFor(srv, "alice"))
	if status != http.StatusOK {
		t.Fatalf("admin metrics page status = %d", status)
	}
	if !strings.Contains(body, `href="/agent/">Agent</a>`) {
		t.Error("admin metrics page should carry a direct Agent link for the signed-in admin")
	}

	status, body = getPage(t, client, ts.URL+"/admin/access", sessionCookieFor(srv, "alice"))
	if status != http.StatusOK {
		t.Fatalf("admin access page status = %d", status)
	}
	if !strings.Contains(body, `href="/agent/">Agent</a>`) {
		t.Error("admin access page should carry a direct Agent link for the signed-in admin")
	}
}

// TestHandleAgentRedirectQualifiesByViewer exercises the per-repo /agent/
// redirect (handleAgent) directly: it now allows any collaborator (not just
// the owner) and qualifies the target repo param the same way agentPath does.
func TestHandleAgentRedirectQualifiesByViewer(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ts, srv, acc := newDeleteTestServer(t)
	if _, err := acc.CreateUser("alice", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.CreateUser("bob", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.CreateUser("carol", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	enableTestAgent(srv)
	seedAgentAccessRepo(t, srv)
	if err := acc.AddCollaborator("alice", "kauai", "bob", "read"); err != nil {
		t.Fatal(err)
	}

	client := ts.Client()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	agentRouteURL := ts.URL + "/alice/kauai/agent"

	get := func(cookie *http.Cookie) *http.Response {
		req, err := http.NewRequest(http.MethodGet, agentRouteURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookie)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}

	// Owner redirects to the bare repo slug.
	res := get(sessionCookieFor(srv, "alice"))
	if res.StatusCode != http.StatusFound {
		t.Fatalf("owner /agent status = %d, want 302", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "/agent/?repo=kauai" {
		t.Errorf("owner /agent redirect = %q, want /agent/?repo=kauai", got)
	}

	// A collaborator (any role) redirects to the owner-qualified repo param.
	res = get(sessionCookieFor(srv, "bob"))
	if res.StatusCode != http.StatusFound {
		t.Fatalf("collaborator /agent status = %d, want 302", res.StatusCode)
	}
	if got := res.Header.Get("Location"); got != "/agent/?repo=alice%2Fkauai" {
		t.Errorf("collaborator /agent redirect = %q, want /agent/?repo=alice%%2Fkauai", got)
	}

	// A non-collaborator is forbidden, even though they're signed in.
	res = get(sessionCookieFor(srv, "carol"))
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("stranger /agent status = %d, want 403", res.StatusCode)
	}
}
