package hub

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func newRedirTestStore(t *testing.T) *LocalStorage {
	t.Helper()
	store, err := NewLocalStorage(filepath.Join(t.TempDir(), "repos"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// TestRedirectStorage: after a rename, the old slug resolves to the new one,
// following a chain of renames, but only to a repo that exists.
func TestRedirectStorage(t *testing.T) {
	store := newRedirTestStore(t)
	if err := store.EnsureRepo("alice", "old"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.LookupRedirect("alice", "old"); ok {
		t.Fatal("no redirect should exist before a rename")
	}
	if err := store.RenameRepo("alice", "old", "new"); err != nil {
		t.Fatal(err)
	}
	if dest, ok := store.LookupRedirect("alice", "old"); !ok || dest != "new" {
		t.Fatalf("old -> %q (%v), want new", dest, ok)
	}
	// Chained rename: old should follow through to the latest slug.
	if err := store.RenameRepo("alice", "new", "newer"); err != nil {
		t.Fatal(err)
	}
	if dest, ok := store.LookupRedirect("alice", "old"); !ok || dest != "newer" {
		t.Fatalf("old -> %q (%v), want newer", dest, ok)
	}
	// An unrelated slug has no redirect.
	if _, ok := store.LookupRedirect("alice", "nope"); ok {
		t.Fatal("unexpected redirect for an unknown slug")
	}
}

// TestRedirectGitURL: a git request to a renamed-away slug 301s to the new one,
// before any auth/auto-create, so old clone/push URLs keep working.
func TestRedirectGitURL(t *testing.T) {
	if _, err := discoverGitHTTPBackend(); err != nil {
		t.Skipf("git-http-backend unavailable: %v", err)
	}
	store := newRedirTestStore(t)
	if err := store.EnsureRepo("alice", "old"); err != nil {
		t.Fatal(err)
	}
	if err := store.RenameRepo("alice", "old", "new"); err != nil {
		t.Fatal(err)
	}
	tokens := NewTokenStore()
	srv, err := New(store, tokens, "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/alice/old.git/info/refs?service=git-upload-pack", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 301 {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/alice/new.git/info/refs?service=git-upload-pack" {
		t.Fatalf("Location = %q", loc)
	}
}
