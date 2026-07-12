package hub

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCollaboratorGitAccess exercises the serveGit authorization matrix end to
// end over HTTP: owner, anonymous, and read/write collaborators against a
// private then public repo. This is the security-critical surface.
func TestCollaboratorGitAccess(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := discoverGitHTTPBackend(); err != nil {
		t.Skipf("git-http-backend unavailable: %v", err)
	}
	dir := t.TempDir()
	store, err := NewLocalStorage(filepath.Join(dir, "repos"))
	if err != nil {
		t.Fatal(err)
	}
	acc, err := OpenAccounts(filepath.Join(dir, "acc.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(store, NewTokenStore(), "")
	if err != nil {
		t.Fatal(err)
	}
	srv.Accounts = acc
	acc.CreateUser("alice", "", "pw12345678")
	acc.CreateUser("bob", "", "pw12345678")
	aliceTok, _ := acc.CreatePAT("alice", "t")
	bobTok, _ := acc.CreatePAT("bob", "t")
	if err := store.EnsureRepo("alice", "kauai"); err != nil { // private by default
		t.Fatal(err)
	}

	ts := httptest.NewServer(srv)
	defer ts.Close()

	// status of an info/refs advertisement (401 = auth refused; 200 = allowed).
	refs := func(service, user, tok string) int {
		req, _ := http.NewRequest("GET", ts.URL+"/alice/kauai.git/info/refs?service="+service, nil)
		if tok != "" {
			req.SetBasicAuth(user, tok)
		}
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	const up, rp = "git-upload-pack", "git-receive-pack"
	want := func(label string, got, exp int) {
		if got != exp {
			t.Fatalf("%s = %d, want %d", label, got, exp)
		}
	}

	// Private, no grant: only the owner gets in.
	want("anon read private", refs(up, "", ""), 401)
	want("stranger read private", refs(up, "bob", bobTok), 401)
	want("owner read", refs(up, "alice", aliceTok), 200)
	want("owner push", refs(rp, "alice", aliceTok), 200)

	// Read grant: bob may clone but not push.
	if err := acc.AddCollaborator("alice", "kauai", "bob", "read"); err != nil {
		t.Fatal(err)
	}
	want("read-collab clone", refs(up, "bob", bobTok), 200)
	want("read-collab push", refs(rp, "bob", bobTok), 401)

	// Write grant: bob may clone and push.
	if err := acc.AddCollaborator("alice", "kauai", "bob", "write"); err != nil {
		t.Fatal(err)
	}
	want("write-collab clone", refs(up, "bob", bobTok), 200)
	want("write-collab push", refs(rp, "bob", bobTok), 200)

	// Revoke: back to no access.
	acc.RemoveCollaborator("alice", "kauai", "bob")
	want("revoked read", refs(up, "bob", bobTok), 401)

	// Public: anyone reads, nobody-but-owner pushes.
	srv.setVisibility("alice", "kauai", visPublic)
	want("anon read public", refs(up, "", ""), 200)
	want("anon push public", refs(rp, "", ""), 401)
	want("stranger push public", refs(rp, "bob", bobTok), 401)
}

func TestCollaboratorEmailInviteHTTP(t *testing.T) {
	ts, srv, acc := newDeleteTestServer(t)
	if _, err := acc.CreateUser("alice", "alice@example.com", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Storage.EnsureRepo("alice", "kauai"); err != nil {
		t.Fatal(err)
	}

	res := postSettings(t, ts, "alice", "kauai", url.Values{
		"action": {"add-collaborator"}, "email": {"new@example.com"}, "role": {"write"},
	}, sessionCookieFor(srv, "alice"), "", "")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	page := string(body)
	marker := "/invite/"
	start := strings.Index(page, marker)
	if res.StatusCode != http.StatusOK || start < 0 {
		t.Fatalf("invite settings response = %d, missing invite link", res.StatusCode)
	}
	for _, want := range []string{
		"Copy invite link",
		"Copy agent prompt",
		"afs hub pull alice/kauai ./kauai",
		"afs docs agent-start",
		"AGENTS.md",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("invite settings page missing %q", want)
		}
	}
	token := page[start+len(marker):]
	if end := strings.IndexAny(token, "\"< "); end >= 0 {
		token = token[:end]
	}
	if token == "" {
		t.Fatal("empty invite token")
	}

	client := ts.Client()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Get(ts.URL + "/invite/" + token)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "invite=") {
		t.Fatalf("GET invite status/location = %d / %q", res.StatusCode, res.Header.Get("Location"))
	}

	res, err = client.PostForm(ts.URL+"/signup", url.Values{
		"invite": {token}, "next": {"/alice/kauai"}, "user": {"new-user"},
		"email": {"new@example.com"}, "password": {"pw12345678"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/alice/kauai" {
		t.Fatalf("signup status/location = %d / %q", res.StatusCode, res.Header.Get("Location"))
	}
	if got := acc.CollaboratorRole("alice", "kauai", "new-user"); got != "write" {
		t.Fatalf("redeemed HTTP invite role = %q, want write", got)
	}
}
