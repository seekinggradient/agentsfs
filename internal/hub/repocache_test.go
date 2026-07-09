package hub

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRepoViewCache covers the view cache's whole life: a cache hit returns
// the same view, a new push is picked up (via the incremental history walk,
// since the prior view is passed in), and a force-push that drops the cached
// commit still yields correct times through the full-walk fallback.
func TestRepoViewCache(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	runGit(t, "", "init", "-b", "main", work)
	writeRepoFile(t, work, "a.md", "---\ndescription: First.\n---\nSee [[b]].\n")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "seed")

	store, err := NewLocalStorage(filepath.Join(tmp, "repos"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Storage: store}
	bare := store.RepoDir("alice", "brain")
	runGit(t, "", "clone", "--bare", work, bare)

	v1, err := s.repoView("alice", "brain")
	if err != nil {
		t.Fatal(err)
	}
	if len(v1.Files) != 1 || v1.Files[0].Description != "First." {
		t.Fatalf("unexpected initial view: %+v", v1.Files)
	}

	// Unchanged repo → the exact cached view comes back.
	v2, err := s.repoView("alice", "brain")
	if err != nil {
		t.Fatal(err)
	}
	if v2 != v1 {
		t.Fatal("expected a cache hit to return the cached view")
	}

	// A push moves HEAD → rebuilt view sees the new note, linked-up graph, and
	// a fresh last-commit time via the incremental walk.
	writeRepoFile(t, work, "b.md", "---\ndescription: Second.\n---\n")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "add b")
	runGit(t, work, "push", bare, "main")

	v3, err := s.repoView("alice", "brain")
	if err != nil {
		t.Fatal(err)
	}
	if v3 == v1 || len(v3.Files) != 2 {
		t.Fatalf("expected a rebuilt 2-file view, got %+v", v3.Files)
	}
	for _, f := range v3.Files {
		if f.LastCommit == 0 {
			t.Errorf("%s: expected a last-commit time after incremental update", f.Path)
		}
	}
	if len(v3.Graph.Links) != 1 {
		t.Fatalf("expected the a→b wikilink in the rebuilt graph, got %+v", v3.Graph.Links)
	}

	// A force-push that rewrites the cached commit away must fall back to the
	// full history walk and still produce times for every file.
	runGit(t, work, "commit", "--amend", "--all", "-m", "rewritten")
	runGit(t, work, "push", "--force", bare, "main")

	v4, err := s.repoView("alice", "brain")
	if err != nil {
		t.Fatal(err)
	}
	if v4 == v3 || len(v4.Files) != 2 {
		t.Fatalf("expected a rebuilt view after force-push, got %+v", v4.Files)
	}
	for _, f := range v4.Files {
		if f.LastCommit == 0 {
			t.Errorf("%s: expected a last-commit time after force-push fallback", f.Path)
		}
	}
}
