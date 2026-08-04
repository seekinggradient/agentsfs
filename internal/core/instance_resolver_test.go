package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInstanceFromHostRootAndUnrelatedSubdirectory(t *testing.T) {
	repo := t.TempDir()
	runStatusGit(t, repo, "init", "-b", "main")
	instance := filepath.Join(repo, "agentsfs")
	writeStatusTestFile(t, filepath.Join(instance, "AGENTS.md"), statusTestContract())
	if err := os.MkdirAll(filepath.Join(instance, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(repo, "src", "deep")
	if err := os.MkdirAll(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}

	canonicalRepo, _ := canonicalPath(repo)
	canonicalInstance, _ := canonicalPath(instance)
	for _, start := range []string{repo, unrelated} {
		got, err := ResolveInstance(start, ResolveInstanceOptions{AllowProjectScan: true})
		if err != nil {
			t.Fatal(err)
		}
		if got.InstanceRoot != canonicalInstance || got.RepoRoot != canonicalRepo || got.Prefix != "agentsfs" || got.Mode != "embedded" || got.DetectedBy != "project-scan" {
			t.Fatalf("resolution from %s = %+v", start, got)
		}
	}
}

func TestResolveInstanceAmbiguousRequiresExplicitPath(t *testing.T) {
	repo := t.TempDir()
	runStatusGit(t, repo, "init", "-b", "main")
	for _, rel := range []string{"agentsfs", "teams/research-memory"} {
		writeStatusTestFile(t, filepath.Join(repo, rel, "AGENTS.md"), statusTestContract())
	}
	_, err := ResolveInstance(repo, ResolveInstanceOptions{AllowProjectScan: true})
	if err == nil || !strings.Contains(err.Error(), "./agentsfs") || !strings.Contains(err.Error(), "./teams/research-memory") {
		t.Fatalf("ambiguity error = %v", err)
	}
	selected, err := ResolveInstance(repo, ResolveInstanceOptions{ExplicitPath: filepath.Join(repo, "teams", "research-memory"), AllowProjectScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Prefix != "teams/research-memory" || selected.DetectedBy != "explicit" {
		t.Fatalf("explicit resolution = %+v", selected)
	}
}

func TestResolveInstanceDoesNotEnterNestedRepositoryOrFollowSymlink(t *testing.T) {
	repo := t.TempDir()
	runStatusGit(t, repo, "init", "-b", "main")
	nested := filepath.Join(repo, "vendor", "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	runStatusGit(t, nested, "init", "-b", "main")
	writeStatusTestFile(t, filepath.Join(nested, "agentsfs", "AGENTS.md"), statusTestContract())
	external := t.TempDir()
	writeStatusTestFile(t, filepath.Join(external, "AGENTS.md"), statusTestContract())
	if err := os.Symlink(external, filepath.Join(repo, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := ResolveInstance(repo, ResolveInstanceOptions{AllowProjectScan: true})
	if err == nil || !strings.Contains(err.Error(), "no AgentsFS instance") {
		t.Fatalf("nested/symlink instance should not resolve: %v", err)
	}
}
