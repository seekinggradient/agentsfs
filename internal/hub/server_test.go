package hub

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitEnv returns an environment that makes git non-interactive and gives it a
// commit identity, so tests never hang on a credential/identity prompt.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func tryGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	return cmd.Run()
}

// newTestHub starts an httptest server backed by a fresh local store, granting
// user "alice" the token "s3cret". It returns the server and a helper that
// builds an authenticated clone URL.
func newTestHubServer(t *testing.T) (*httptest.Server, *Server, func(user, token, repo string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := discoverGitHTTPBackend(); err != nil {
		t.Skipf("git-http-backend unavailable: %v", err)
	}

	store, err := NewLocalStorage(filepath.Join(t.TempDir(), "repos"))
	if err != nil {
		t.Fatal(err)
	}
	tokens := NewTokenStore()
	tokens.Add("alice", "s3cret")
	srv, err := New(store, tokens, "")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	base, _ := url.Parse(ts.URL)
	authURL := func(user, token, repo string) string {
		u := *base
		u.User = url.UserPassword(user, token)
		u.Path = "/" + user + "/" + repo + ".git"
		return u.String()
	}
	return ts, srv, authURL
}

func newTestHub(t *testing.T) (*httptest.Server, func(user, token, repo string) string) {
	t.Helper()
	ts, _, authURL := newTestHubServer(t)
	return ts, authURL
}

// TestCloneCommitPushClone is the Phase 0 gate demo as a test: push a note to a
// hosted repo, then prove a fresh clone from only the URL gets the content.
func TestCloneCommitPushClone(t *testing.T) {
	_, authURL := newTestHub(t)
	tmp := t.TempDir()

	// Clone the (auto-created, empty) repo.
	work1 := filepath.Join(tmp, "work1")
	runGit(t, "", "-c", "init.defaultBranch=main", "clone", authURL("alice", "s3cret", "brain"), work1)

	// Write a note, commit, and push.
	if err := os.WriteFile(filepath.Join(work1, "NOTE.md"), []byte("hello hub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work1, "add", "-A")
	runGit(t, work1, "commit", "-m", "first note")
	runGit(t, work1, "push", "origin", "main")

	// A brand-new clone with only the URL must see the pushed note.
	work2 := filepath.Join(tmp, "work2")
	runGit(t, "", "clone", authURL("alice", "s3cret", "brain"), work2)
	got, err := os.ReadFile(filepath.Join(work2, "NOTE.md"))
	if err != nil {
		t.Fatalf("expected NOTE.md in fresh clone: %v", err)
	}
	if string(got) != "hello hub\n" {
		t.Fatalf("content mismatch: got %q", got)
	}
}

// TestAuth verifies a bad token and cross-namespace access are both rejected.
func TestAuth(t *testing.T) {
	_, authURL := newTestHub(t)
	tmp := t.TempDir()

	if err := tryGit("", "clone", authURL("alice", "wrong", "brain"), filepath.Join(tmp, "bad")); err == nil {
		t.Fatal("expected clone with a bad token to fail")
	}
	// alice's token must not reach bob's namespace.
	if err := tryGit("", "clone", authURL("bob", "s3cret", "brain"), filepath.Join(tmp, "cross")); err == nil {
		t.Fatal("expected cross-namespace clone to fail")
	}
}

func TestAnonymousHomeAndPrivateDashboard(t *testing.T) {
	ts, _ := newTestHub(t)

	res, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("GET / status = %d, want 200", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	home := string(body)
	for _, want := range []string{
		"Your AgentsFS repositories, readable on the web.",
		"redesign-v2.css",
	} {
		if !strings.Contains(home, want) {
			t.Errorf("GET / missing %q", want)
		}
	}
	if strings.Contains(home, `name="robots" content="noindex`) {
		t.Error("canonical Hub homepage must not be noindexed")
	}

	headReq, err := http.NewRequest(http.MethodHead, ts.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	headRes, err := ts.Client().Do(headReq)
	if err != nil {
		t.Fatal(err)
	}
	headRes.Body.Close()
	if headRes.StatusCode != http.StatusOK {
		t.Fatalf("HEAD / status = %d, want 200", headRes.StatusCode)
	}

	res, err = ts.Client().Get(ts.URL + "/alice")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("GET /alice final status = %d, want login page after redirect", res.StatusCode)
	}
	if res.Request.URL.Path != "/login" {
		t.Fatalf("GET /alice final path = %q, want /login", res.Request.URL.Path)
	}
}

func TestAnonymousRedesignPreview(t *testing.T) {
	ts, _ := newTestHub(t)

	res, err := ts.Client().Get(ts.URL + "/redesign")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /redesign status = %d, want 200", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, want := range []string{
		"Your agents’ knowledge, hosted as real Git.",
		"Private by default</span><span>Web edits are commits</span><span>Pull anytime",
		"Pull the repository back at any time.",
		"afs hub push insurance-claim",
		"redesign.css",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("GET /redesign missing %q", want)
		}
	}
	if strings.Contains(page, "hardware-isolated agent") {
		t.Error("agent-disabled redesign advertises the hosted agent")
	}

	css, err := ts.Client().Get(ts.URL + "/_assets/redesign.css")
	if err != nil {
		t.Fatal(err)
	}
	defer css.Body.Close()
	if css.StatusCode != http.StatusOK {
		t.Fatalf("GET redesign stylesheet status = %d, want 200", css.StatusCode)
	}
	if got := css.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("redesign stylesheet Content-Type = %q, want text/css", got)
	}

	post, err := http.Post(ts.URL+"/redesign", "text/plain", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	defer post.Body.Close()
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /redesign status = %d, want 405", post.StatusCode)
	}
	if got := post.Header.Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("POST /redesign Allow = %q, want GET, HEAD", got)
	}
}

func TestAnonymousRedesignV2Preview(t *testing.T) {
	ts, _ := newTestHub(t)

	res, err := ts.Client().Get(ts.URL + "/redesign-v2")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /redesign-v2 status = %d, want 200", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, want := range []string{
		"Your AgentsFS repositories, readable on the web.",
		"The same repository, presented for people.",
		"afs hub push insurance-claim",
		"The Hub is a remote, not the database.",
		"redesign-v2.css",
		"redesign-v2.js",
		`name="robots" content="noindex,follow"`,
		`class="hv2-header-cta" href="/login">Sign in`,
		`role="tab" data-hv2-note="status" aria-selected="true"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("GET /redesign-v2 missing %q", want)
		}
	}
	if strings.Contains(page, "One private agent can work across") {
		t.Error("agent-disabled redesign V2 advertises the hosted agent")
	}

	for _, asset := range []struct {
		path        string
		contentType string
	}{
		{"/_assets/redesign-v2.css", "text/css"},
		{"/_assets/redesign-v2.js", "text/javascript"},
	} {
		assetRes, err := ts.Client().Get(ts.URL + asset.path)
		if err != nil {
			t.Fatal(err)
		}
		if assetRes.StatusCode != http.StatusOK {
			assetRes.Body.Close()
			t.Fatalf("GET %s status = %d, want 200", asset.path, assetRes.StatusCode)
		}
		if got := assetRes.Header.Get("Content-Type"); !strings.HasPrefix(got, asset.contentType) {
			assetRes.Body.Close()
			t.Fatalf("GET %s Content-Type = %q, want prefix %q", asset.path, got, asset.contentType)
		}
		assetRes.Body.Close()
	}

	post, err := http.Post(ts.URL+"/redesign-v2", "text/plain", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	defer post.Body.Close()
	if post.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /redesign-v2 status = %d, want 405", post.StatusCode)
	}
	if got := post.Header.Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("POST /redesign-v2 Allow = %q, want GET, HEAD", got)
	}
}

func TestPublicDiscoveryFiles(t *testing.T) {
	ts, _ := newTestHub(t)
	for _, tc := range []struct {
		path        string
		contentType string
		want        string
	}{
		{"/robots.txt", "text/plain", "Sitemap: https://hub.agentsfs.ai/sitemap.xml"},
		{"/sitemap.xml", "application/xml", "<loc>https://hub.agentsfs.ai/</loc>"},
	} {
		res, err := ts.Client().Get(ts.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", tc.path, res.StatusCode)
		}
		if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, tc.contentType) {
			t.Errorf("GET %s Content-Type = %q, want prefix %q", tc.path, got, tc.contentType)
		}
		if !strings.Contains(string(body), tc.want) {
			t.Errorf("GET %s missing %q", tc.path, tc.want)
		}
	}
}

func TestRedesignPreviewIsLoopbackOnly(t *testing.T) {
	for _, host := range []string{"localhost:8080", "127.0.0.1:8080", "[::1]:8080"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/redesign", nil)
		if !isLoopbackPreview(req) {
			t.Errorf("isLoopbackPreview(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"hub.agentsfs.ai", "example.com:443"} {
		req := httptest.NewRequest(http.MethodGet, "https://"+host+"/redesign", nil)
		if isLoopbackPreview(req) {
			t.Errorf("isLoopbackPreview(%q) = true, want false", host)
		}
	}
}
