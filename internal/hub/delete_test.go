package hub

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLocalStorageDeleteRepo: a deleted repo disappears from Exists/ListRepos
// but its bare dir survives, moved under .trash — the soft-delete contract.
func TestLocalStorageDeleteRepo(t *testing.T) {
	store := newRedirTestStore(t)
	if err := store.EnsureRepo("alice", "kauai"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRepo("alice", "kauai"); err != nil {
		t.Fatal(err)
	}
	if store.Exists("alice", "kauai") {
		t.Fatal("repo should no longer exist after delete")
	}
	repos, err := store.ListRepos("alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range repos {
		if r == "kauai" {
			t.Fatal("deleted repo should not appear in ListRepos")
		}
	}
	ents, err := os.ReadDir(filepath.Join(store.Dir, ".trash"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("expected 1 entry in .trash, got %d", len(ents))
	}
	if got := ents[0].Name(); !ents[0].IsDir() || filepath.Ext(got) != ".git" {
		t.Fatalf("trashed entry = %q, want a .git dir", got)
	}

	// Deleting again (already gone) must fail rather than silently no-op.
	if err := store.DeleteRepo("alice", "kauai"); err == nil {
		t.Fatal("expected error deleting a repo that doesn't exist")
	}
}

// newDeleteTestServer wires a full Server (storage + accounts) the way
// newTestHubServer does, but also exposes the AccountStore so tests can mint
// PATs and session cookies directly.
func newDeleteTestServer(t *testing.T) (*httptest.Server, *Server, *AccountStore) {
	t.Helper()
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
		t.Skipf("git-http-backend unavailable: %v", err)
	}
	srv.Accounts = acc
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, srv, acc
}

// sessionCookieFor mints a valid session cookie value the same way setSession
// does, without going through the /login HTTP flow.
func sessionCookieFor(srv *Server, user string) *http.Cookie {
	exp := time.Now().Add(time.Hour).Unix()
	return &http.Cookie{Name: sessionCookie, Value: makeSession(srv.sessionSecret(), user, exp)}
}

func postSettings(t *testing.T, ts *httptest.Server, user, repo string, form url.Values, cookie *http.Cookie, basicUser, basicPass string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", ts.URL+"/"+user+"/"+repo+"/settings", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if basicUser != "" {
		req.SetBasicAuth(basicUser, basicPass)
	}
	client := ts.Client()
	// This handler's success path is a redirect; the failure path re-renders
	// the settings page (200). Don't follow, so the caller can tell them apart.
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestDeleteRepoHTTP exercises the delete-repo settings action end to end:
// session-cookie owner with the right confirm succeeds; a wrong confirm or a
// PAT-only request must not delete anything.
func TestDeleteRepoHTTP(t *testing.T) {
	ts, srv, acc := newDeleteTestServer(t)
	if _, err := acc.CreateUser("alice", "", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	aliceTok, err := acc.CreatePAT("alice", "t")
	if err != nil {
		t.Fatal(err)
	}
	cookie := sessionCookieFor(srv, "alice")

	// (c) PAT-only auth must be refused, repo must survive.
	if err := srv.Storage.EnsureRepo("alice", "kauai"); err != nil {
		t.Fatal(err)
	}
	res := postSettings(t, ts, "alice", "kauai", url.Values{"action": {"delete-repo"}, "confirm": {"kauai"}}, nil, "alice", aliceTok)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("PAT-only delete: status = %d, want 200 (rendered error, not a redirect)", res.StatusCode)
	}
	if !srv.Storage.Exists("alice", "kauai") {
		t.Fatal("PAT-only delete must not have deleted the repo")
	}

	// (b) Wrong confirm text, with a valid session cookie, must not delete.
	res = postSettings(t, ts, "alice", "kauai", url.Values{"action": {"delete-repo"}, "confirm": {"not-the-slug"}}, cookie, "", "")
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("wrong confirm: status = %d, want 200", res.StatusCode)
	}
	if !srv.Storage.Exists("alice", "kauai") {
		t.Fatal("wrong confirm must not have deleted the repo")
	}

	// Set up a collaborator grant and a rename-redirect pointing at "kauai" so we
	// can verify both are cleaned up by a real delete.
	acc.CreateUser("bob", "", "pw12345678")
	if err := acc.AddCollaborator("alice", "kauai", "bob", "read"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Storage.RenameRepo("alice", "kauai", "kauai-tmp"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Storage.RenameRepo("alice", "kauai-tmp", "kauai"); err != nil {
		t.Fatal(err)
	}
	// "kauai-tmp" now redirects to "kauai".
	if dest, ok := srv.Storage.LookupRedirect("alice", "kauai-tmp"); !ok || dest != "kauai" {
		t.Fatalf("setup: kauai-tmp -> %q (%v), want kauai", dest, ok)
	}

	// (a) Owner with session cookie + correct confirm: repo deleted, redirect to /alice.
	res = postSettings(t, ts, "alice", "kauai", url.Values{"action": {"delete-repo"}, "confirm": {"kauai"}}, cookie, "", "")
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("delete: status = %d, want %d", res.StatusCode, http.StatusFound)
	}
	if loc := res.Header.Get("Location"); loc != "/alice" {
		t.Fatalf("Location = %q, want /alice", loc)
	}
	if srv.Storage.Exists("alice", "kauai") {
		t.Fatal("repo should be gone after delete")
	}

	// Collaborator grant dropped.
	if role := acc.CollaboratorRole("alice", "kauai", "bob"); role != "" {
		t.Fatalf("collaborator role after delete = %q, want none", role)
	}

	// (d) The redirect that pointed at the deleted slug is gone too — otherwise
	// an old clone URL would 301 into a dead repo, and an owner push would
	// silently recreate "kauai" from an empty state.
	if _, ok := srv.Storage.LookupRedirect("alice", "kauai-tmp"); ok {
		t.Fatal("redirect pointing at the deleted slug should have been dropped")
	}
}
