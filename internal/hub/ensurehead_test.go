package hub

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestEnsureHEADMasterPush reproduces the bug where a client pushes a `master`
// branch into a repo the Hub initialized on `main`: HEAD stays dangling, so the
// web view (which reads HEAD) sees an empty repo. EnsureHEAD must repair it.
func TestEnsureHEADMasterPush(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	store, err := NewLocalStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	// Bare repo initialized on main, exactly like EnsureRepo does.
	if err := store.EnsureRepo("alice", "brain"); err != nil {
		t.Fatal(err)
	}
	bare := store.RepoDir("alice", "brain")

	// A client whose local branch is `master` pushes it.
	work := filepath.Join(root, "work")
	runGit(t, "", "init", "-b", "master", work)
	writeRepoFile(t, work, "NOTE.md", "---\ndescription: hi.\n---\n# Note\n")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "first")
	runGit(t, work, "push", bare, "master")

	// Before repair the web view (HEAD -> unborn main) sees nothing.
	if files, _ := RepoSnapshot("git", bare, defaultRef); len(files) != 0 {
		t.Fatalf("expected empty snapshot before repair, got %d files", len(files))
	}

	// Repair: HEAD now points at master and the note is visible.
	if err := store.EnsureHEAD("alice", "brain"); err != nil {
		t.Fatal(err)
	}
	files, err := RepoSnapshot("git", bare, defaultRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected the note visible after repair, got %d files", len(files))
	}

	// Idempotent: a second call is a no-op and leaves HEAD on master.
	if err := store.EnsureHEAD("alice", "brain"); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", bare, "symbolic-ref", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "refs/heads/master\n" {
		t.Fatalf("HEAD = %q, want refs/heads/master", got)
	}
}

// TestEnsureHEADEmptyRepo verifies a genuinely empty repo (no commits) is left
// alone — EnsureHEAD must not error or invent a branch.
func TestEnsureHEADEmptyRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	store, err := NewLocalStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRepo("alice", "brain"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureHEAD("alice", "brain"); err != nil {
		t.Fatalf("EnsureHEAD on empty repo: %v", err)
	}
}
