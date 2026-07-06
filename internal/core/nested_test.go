package core

import (
	"os"
	"path/filepath"
	"testing"
)

// TestListEntriesSkipsNested verifies a nested git repo or agentsfs instance is
// treated as a separate knowledgebase and excluded from the parent's walk (so
// tree/search/reindex/doctor don't fold in notes that aren't part of this repo
// and wouldn't push).
func TestListEntriesSkipsNested(t *testing.T) {
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(root, "top.md"), []byte("x"), 0o644))

	// A nested git repository (e.g. a repo pulled in but not yet vendored).
	sub := filepath.Join(root, "vendored")
	must(os.MkdirAll(filepath.Join(sub, ".git"), 0o755))
	must(os.WriteFile(filepath.Join(sub, "secret.md"), []byte("y"), 0o644))

	// A nested agentsfs instance without git (its own .agentsfs).
	inst := filepath.Join(root, "other-kb")
	must(os.MkdirAll(filepath.Join(inst, ".agentsfs"), 0o755))
	must(os.WriteFile(filepath.Join(inst, "note.md"), []byte("z"), 0o644))

	entries, err := ListEntries(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Rel] = true
	}
	if !got["top.md"] {
		t.Error("expected top.md to be listed")
	}
	for _, leaked := range []string{"vendored", "vendored/secret.md", "other-kb", "other-kb/note.md"} {
		if got[leaked] {
			t.Errorf("nested instance leaked into the parent walk: %s", leaked)
		}
	}
}
