package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListReturnsKnownSkills(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	want := map[string]bool{
		"agentsfs-setup":    false,
		"agentsfs-remember": false,
		"agentsfs-adopt":    false,
		"agentsfs-garden":   false,
	}
	if len(list) != len(want) {
		t.Fatalf("List() returned %d skills, want %d: %+v", len(list), len(want), list)
	}
	for _, s := range list {
		if _, ok := want[s.Dir]; !ok {
			t.Fatalf("List() returned unexpected skill dir %q", s.Dir)
		}
		want[s.Dir] = true
		if s.Name != s.Dir {
			t.Errorf("skill %q: Name %q does not match directory basename", s.Dir, s.Name)
		}
		if strings.TrimSpace(s.Description) == "" {
			t.Errorf("skill %q has an empty description", s.Dir)
		}
	}
	for dir, seen := range want {
		if !seen {
			t.Errorf("List() missing known skill %q", dir)
		}
	}
}

func TestMaterializeWritesAndOverwrites(t *testing.T) {
	base := t.TempDir()

	// A pre-existing stale SKILL.md that Materialize must overwrite so the
	// cache always matches the binary.
	staleDir := filepath.Join(base, "agentsfs-garden")
	if err := os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(staleDir, "SKILL.md")
	if err := os.WriteFile(stalePath, []byte("STALE"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Materialize(base); err != nil {
		t.Fatalf("Materialize() error: %v", err)
	}

	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		got, err := os.ReadFile(filepath.Join(base, s.Dir, "SKILL.md"))
		if err != nil {
			t.Fatalf("skill %q was not materialized: %v", s.Dir, err)
		}
		if len(got) == 0 {
			t.Errorf("skill %q materialized to an empty file", s.Dir)
		}
	}

	// The stale file was replaced with the real embedded content.
	got, err := os.ReadFile(stalePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "STALE" {
		t.Fatal("Materialize() did not overwrite the stale SKILL.md")
	}
	if !strings.Contains(string(got), "agentsfs-garden") {
		t.Errorf("overwritten garden SKILL.md lacks expected frontmatter:\n%s", got)
	}
}
