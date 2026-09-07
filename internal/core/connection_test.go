package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectTargetsStayInsideRepository(t *testing.T) {
	parent := t.TempDir()
	mustWrite(t, filepath.Join(parent, "AGENTS.md"), "parent instructions")
	root := filepath.Join(parent, "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "CLAUDE.md"), "custom Claude instructions")
	sub := filepath.Join(root, "src")
	os.MkdirAll(sub, 0755)
	targets := ProjectTargets(sub)
	if len(targets) != 2 || targets[0].Path != filepath.Join(root, "AGENTS.md") || targets[1].Path != filepath.Join(root, "CLAUDE.md") {
		t.Fatalf("wrong targets: %+v", targets)
	}
	// A Git worktree uses a file marker, and must also stop the ancestor walk.
	os.Remove(filepath.Join(root, ".git"))
	mustWrite(t, filepath.Join(root, ".git"), "gitdir: /unused")
	if got := ProjectTargets(sub); got[0].Path != targets[0].Path {
		t.Fatalf("worktree targets: %+v", got)
	}
}

func TestProjectTargetsOutsideGitUseCurrentDirectory(t *testing.T) {
	parent := t.TempDir()
	mustWrite(t, filepath.Join(parent, "AGENTS.md"), "unrelated parent")
	child := filepath.Join(parent, "child")
	os.MkdirAll(child, 0755)
	got := ProjectTargets(child)
	if len(got) != 1 || got[0].Path != filepath.Join(child, "AGENTS.md") {
		t.Fatalf("wrong targets: %+v", got)
	}
}

func TestEmbeddedConnectionMigrationAndRelocation(t *testing.T) {
	project := t.TempDir()
	instance := filepath.Join(project, "memory")
	os.MkdirAll(instance, 0755)
	target := filepath.Join(project, "AGENTS.md")
	mustWrite(t, target, "Keep my instructions\n\n"+ConnectionBlock(instance)+"\n")
	for i := 0; i < 2; i++ {
		if err := Connect(target, instance); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(target)
	s := string(raw)
	if strings.Count(s, "<!-- agentsfs:begin") != 1 || !strings.Contains(s, "read `./memory/AGENTS.md`") || !strings.Contains(s, "Keep my instructions") || strings.Contains(s, project) {
		t.Fatalf("bad migration: %s", s)
	}
	moved := filepath.Join(t.TempDir(), "copy")
	os.MkdirAll(filepath.Join(moved, "memory"), 0755)
	copied := filepath.Join(moved, "AGENTS.md")
	os.WriteFile(copied, raw, 0644)
	if err := Connect(copied, filepath.Join(moved, "memory")); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(copied)
	if string(again) != s {
		t.Fatalf("relocation changed portable connection")
	}
	removed, err := Disconnect(copied, filepath.Join(moved, "memory"))
	if err != nil || !removed {
		t.Fatalf("disconnect: %v %v", removed, err)
	}
	rest, _ := os.ReadFile(copied)
	if strings.Contains(string(rest), "agentsfs:begin") || !strings.Contains(string(rest), "Keep my instructions") {
		t.Fatal(string(rest))
	}
}
