package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func hostRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "test"},
		{"config", "user.email", "test@example.com"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// Finding 1 regression: init inside a dirty enclosing repo must commit only
// files under the instance directory — never the user's unrelated work.
func TestInitInsideDirtyRepoCommitsOnlyInstanceFiles(t *testing.T) {
	host := hostRepo(t)
	if err := os.WriteFile(filepath.Join(host, "wip.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(host, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(host, "commit", "-m", "project work"); err != nil {
		t.Fatal(err)
	}
	// The user's in-progress, uncommitted change.
	if err := os.WriteFile(filepath.Join(host, "wip.txt"), []byte("v2 unrelated"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Init(filepath.Join(host, "memory"), ModeShared)
	if err != nil {
		t.Fatal(err)
	}
	if res.GitInited {
		t.Error("GitInited = true in shared mode (should join the host repo)")
	}
	if res.LFSConfigured {
		t.Error("LFSConfigured = true in shared mode (host repo's call)")
	}
	if !res.Committed {
		t.Fatal("init commit failed")
	}

	committed, err := git(host, "show", "--name-only", "--format=")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range strings.Fields(committed) {
		if !strings.HasPrefix(f, "memory/") {
			t.Errorf("init commit swept in unrelated file %q", f)
		}
	}
	status, err := git(host, "status", "--porcelain", "wip.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "wip.txt") {
		t.Error("user's uncommitted wip.txt change is gone — it was committed or lost")
	}
}

func TestEnclosingRepoRoot(t *testing.T) {
	host := hostRepo(t)
	// A not-yet-existing nested path still resolves to the host root.
	got, ok := EnclosingRepoRoot(filepath.Join(host, "does", "not", "exist", "yet"))
	if !ok || mustEval(t, got) != mustEval(t, host) {
		t.Errorf("EnclosingRepoRoot = %q, %v; want %q", got, ok, host)
	}
	if _, ok := EnclosingRepoRoot(t.TempDir()); ok {
		t.Error("EnclosingRepoRoot found a repo where there is none")
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Finding 2 regression: an ordinary project with a generic AGENTS.md is not
// an instance; one whose AGENTS.md declares the contract is.
func TestFindRootRejectsGenericAgentsMD(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Project instructions\n\nRun the linter before committing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "src")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if root, err := FindRoot(sub); err == nil {
		t.Errorf("FindRoot accepted a generic AGENTS.md repo as instance root %q", root)
	}
}

func TestFindRootAcceptsContractDeclaration(t *testing.T) {
	dir := t.TempDir() // hand-made instance: marker AGENTS.md, no .agentsfs/
	content := "---\ndescription: Root.\n---\n# This folder is an agentsfs\n\nRules...\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := FindRoot(dir)
	if err != nil {
		t.Fatalf("FindRoot rejected a hand-made instance: %v", err)
	}
	wantR, _ := filepath.EvalSymlinks(dir)
	gotR, _ := filepath.EvalSymlinks(root)
	if gotR != wantR {
		t.Errorf("FindRoot = %q, want %q", gotR, wantR)
	}
}
