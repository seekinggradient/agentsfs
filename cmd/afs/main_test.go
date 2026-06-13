package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_AFS_HELPER") != "1" {
		return
	}
	for i, a := range os.Args {
		if a == "--" {
			os.Args = append([]string{"afs"}, os.Args[i+1:]...)
			main()
			return
		}
	}
	os.Exit(2)
}

func TestSetupCreatesPersonalInstanceAndConnectsProject(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	runGit(t, project, "init", "-b", "main")

	out, err := runAFS(t, project, home, "setup", "--yes")
	if err != nil {
		t.Fatalf("afs setup failed: %v\n%s", err, out)
	}

	instance := filepath.Join(home, "agentsfs")
	if _, err := os.Stat(filepath.Join(instance, "AGENTS.md")); err != nil {
		t.Fatalf("personal agentsfs was not initialized: %v", err)
	}

	projectAgents, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatalf("project was not connected: %v", err)
	}
	if !strings.Contains(string(projectAgents), instance) {
		t.Fatalf("project AGENTS.md does not point at instance %q:\n%s", instance, projectAgents)
	}
}

func TestInitInsideGitRepoRequiresShared(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	runGit(t, project, "init", "-b", "main")

	out, err := runAFS(t, project, home, "init", "--yes")
	if err == nil {
		t.Fatalf("afs init inside repo unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, "afs setup ~/agentsfs") || !strings.Contains(out, "afs init ./agentsfs --shared") {
		t.Fatalf("failure did not explain the two safe choices:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(project, "agentsfs")); !os.IsNotExist(err) {
		t.Fatalf("init without --shared created project agentsfs unexpectedly: %v", err)
	}
}

func TestInitSharedAtRepoRootUsesAgentsfsSubdir(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	runGit(t, project, "init", "-b", "main")

	out, err := runAFS(t, project, home, "init", "--shared", "--yes")
	if err != nil {
		t.Fatalf("afs init --shared failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(project, "agentsfs", "AGENTS.md")); err != nil {
		t.Fatalf("shared agentsfs was not initialized in ./agentsfs: %v\n%s", err, out)
	}
}

func TestInitSharedOutsideGitRepoFails(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()

	out, err := runAFS(t, dir, home, "init", "notes", "--shared")
	if err == nil {
		t.Fatalf("afs init --shared outside repo unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, "--shared only makes sense inside a git repo") {
		t.Fatalf("failure did not explain that --shared needs a repo:\n%s", out)
	}
}

func runAFS(t *testing.T, dir, home string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{"-test.run=TestHelperProcess", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GO_WANT_AFS_HELPER=1",
		"HOME="+home,
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
