package hub

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
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
