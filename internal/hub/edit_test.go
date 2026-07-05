package hub

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommitFile verifies the no-checkout write path: it lands a real commit
// that changes content, and enforces optimistic concurrency.
func TestCommitFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	runGitT(t, "", "init", "-b", "main", work)
	writeRepoFile(t, work, "NOTE.md", "---\ndescription: first.\n---\n# One\noriginal\n")
	runGitT(t, work, "add", "-A")
	runGitT(t, work, "commit", "-m", "seed")
	bare := filepath.Join(tmp, "brain.git")
	runGitT(t, "", "clone", "--bare", work, bare)

	head0 := mustGitHead(bare)

	// Edit the note.
	newHead, err := CommitFile("git", bare, "NOTE.md", "---\ndescription: first.\n---\n# One\nedited body\n", "akshay", "Tweak note", head0)
	if err != nil {
		t.Fatalf("CommitFile: %v", err)
	}
	if newHead == head0 {
		t.Fatal("expected a new commit")
	}
	got, _ := BlobContent("git", bare, "HEAD", "NOTE.md")
	if !strings.Contains(got, "edited body") {
		t.Fatalf("content not updated: %q", got)
	}
	// author is the human, committer is the hub
	an, _ := gitCmd("git", bare, nil, nil, "log", "-1", "--format=%an|%cn")
	if an != "akshay|agentsfs hub\n" {
		t.Fatalf("author/committer = %q, want akshay|agentsfs hub", an)
	}

	// Create a new file that didn't exist before.
	if _, err := CommitFile("git", bare, "areas/new.md", "hi\n", "akshay", "add", ""); err != nil {
		t.Fatalf("CommitFile new path: %v", err)
	}
	if _, ok := BlobContent("git", bare, "HEAD", "areas/new.md"); !ok {
		t.Fatal("new file not created")
	}

	// Stale base is rejected.
	if _, err := CommitFile("git", bare, "NOTE.md", "x\n", "akshay", "m", head0); err != ErrStale {
		t.Fatalf("expected ErrStale for stale base, got %v", err)
	}
}

func runGitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
