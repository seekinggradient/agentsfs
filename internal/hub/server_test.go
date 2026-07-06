package hub

import (
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
func newTestHub(t *testing.T) (*httptest.Server, func(user, token, repo string) string) {
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
