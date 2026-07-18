package hub

import (
	"io"
	"net/http"
	"os/exec"
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

// TestAgentDockHiddenForPublicRepoStrangersAndAnon covers the boundary this
// change must NOT move: the agent API deliberately excludes other users'
// public repos from an agent's scope, so a signed-in non-collaborator (and an
// anonymous visitor) must still see no dock on a public repo, even though they
// can read the page itself.
func TestAgentDockHiddenForPublicRepoStrangersAndAnon(t *testing.T) {
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

	// (c) signed-in stranger (no collaborator grant) on a public repo.
	status, body := getPage(t, client, pageURL, sessionCookieFor(srv, "carol"))
	if status != http.StatusOK {
		t.Fatalf("stranger page status = %d, want 200 (repo is public)", status)
	}
	if strings.Contains(body, `id="agent-dock"`) || strings.Contains(body, `class="agent-trigger"`) {
		t.Error("signed-in non-collaborator on a public repo should not see the agent dock/trigger")
	}

	// (d) signed-out visitor on the same public repo.
	status, body = getPage(t, client, pageURL, nil)
	if status != http.StatusOK {
		t.Fatalf("anon page status = %d, want 200 (repo is public)", status)
	}
	if strings.Contains(body, `id="agent-dock"`) || strings.Contains(body, `class="agent-trigger"`) {
		t.Error("signed-out visitor should not see the agent dock/trigger")
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
