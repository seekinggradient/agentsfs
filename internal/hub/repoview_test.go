package hub

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"agentsfs.ai/afs/internal/core"
)

func writeRepoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRepoSnapshot enumerates a bare repo at HEAD and checks paths,
// descriptions (parsed from frontmatter via core), and freshness.
func TestRepoSnapshot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	runGit(t, "", "init", "-b", "main", work)
	writeRepoFile(t, work, "NOTE.md", "---\ndescription: Top note.\n---\n# Note\n")
	writeRepoFile(t, work, "projects/INDEX.md", "---\ndescription: Active projects.\n---\n")
	writeRepoFile(t, work, "projects/plan.md", "---\ndescription: The plan.\n---\nbody\n")
	writeRepoFile(t, work, "image.png", "not really a png")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "seed")

	bare := filepath.Join(tmp, "brain.git")
	runGit(t, "", "clone", "--bare", work, bare)

	files, err := RepoSnapshot("git", bare, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"NOTE.md":           "Top note.",
		"projects/INDEX.md": "Active projects.",
		"projects/plan.md":  "The plan.",
		"image.png":         "", // non-markdown: no description
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %+v", len(files), len(want), files)
	}
	for _, f := range files {
		wantDesc, ok := want[f.Path]
		if !ok {
			t.Errorf("unexpected file %q", f.Path)
			continue
		}
		if f.Description != wantDesc {
			t.Errorf("%s: description = %q, want %q", f.Path, f.Description, wantDesc)
		}
		if f.LastCommit == 0 {
			t.Errorf("%s: expected a last-commit time", f.Path)
		}
	}
}

// TestRepoSnapshotEmpty returns no files (and no error) for a repo with no
// commits yet.
func TestRepoSnapshotEmpty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	bare := filepath.Join(t.TempDir(), "empty.git")
	runGit(t, "", "init", "--bare", "-b", "main", bare)
	files, err := RepoSnapshot("git", bare, "HEAD")
	if err != nil {
		t.Fatalf("empty repo should not error: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("empty repo should have no files, got %+v", files)
	}
}

func TestRepoBacklinksResolvesTargetPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	runGit(t, "", "init", "-b", "main", work)
	writeRepoFile(t, work, "companies/apple.md", "---\ndescription: Apple.\n---\n")
	writeRepoFile(t, work, "notes/mention.md", "See [[Apple]] and [[companies/Apple|AAPL]].\n")
	writeRepoFile(t, work, "notes/other.md", "See [[Banana]].\n")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "seed")

	bare := filepath.Join(tmp, "brain.git")
	runGit(t, "", "clone", "--bare", work, bare)

	files, err := RepoSnapshot("git", bare, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	links := RepoBacklinks("git", bare, "HEAD", "companies/apple.md", core.NewNameIndex(paths))
	if len(links) != 2 {
		t.Fatalf("got %d backlinks, want 2: %+v", len(links), links)
	}
	for _, l := range links {
		if l.Source != "notes/mention.md" {
			t.Fatalf("unexpected backlink source %q", l.Source)
		}
	}
}

func TestBuildRepoGraphResolvesWikilinks(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	work := filepath.Join(tmp, "work")
	runGit(t, "", "init", "-b", "main", work)
	writeRepoFile(t, work, "companies/apple.md", "---\ndescription: Apple.\n---\n")
	writeRepoFile(t, work, "notes/mention.md", "See [[Apple]] and [[companies/Apple|AAPL]].\n")
	writeRepoFile(t, work, "notes/other.md", "See [[Banana]].\n")
	writeRepoFile(t, work, "diagram.png", "not markdown")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "seed")

	bare := filepath.Join(tmp, "brain.git")
	runGit(t, "", "clone", "--bare", work, bare)

	files, err := RepoSnapshot("git", bare, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	graph := BuildRepoGraph("git", bare, "HEAD", "alice", "brain", files)
	if len(graph.Nodes) != 3 {
		t.Fatalf("got %d graph nodes, want 3: %+v", len(graph.Nodes), graph.Nodes)
	}
	if len(graph.Links) != 1 {
		t.Fatalf("got %d graph links, want 1: %+v", len(graph.Links), graph.Links)
	}
	link := graph.Links[0]
	if link.Count != 2 {
		t.Fatalf("link count = %d, want 2", link.Count)
	}
	if graph.Nodes[link.Source].Path != "notes/mention.md" || graph.Nodes[link.Target].Path != "companies/apple.md" {
		t.Fatalf("unexpected graph edge: %+v -> %+v", graph.Nodes[link.Source], graph.Nodes[link.Target])
	}
}
