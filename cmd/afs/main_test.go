package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentsfs.ai/afs/internal/core"
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

func TestHelpDoesNotAdvertiseHostedCommands(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	out, err := runAFS(t, project, home, "help")
	if err != nil {
		t.Fatalf("afs help failed: %v\n%s", err, out)
	}
	for _, unwanted := range []string{"afs login", "afs hosted", "hosted sync", "backup/restore"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("help still advertised %q:\n%s", unwanted, out)
		}
	}
}

func TestHelpDocumentsUninstallCommand(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	out, err := runAFS(t, project, home, "help")
	if err != nil {
		t.Fatalf("afs help failed: %v\n%s", err, out)
	}
	for _, want := range []string{"afs uninstall", "Never deletes any agentsfs", "filesystem or git data"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help did not contain %q:\n%s", want, out)
		}
	}
}

func TestUninstallRemovesBinaryButKeepsData(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "afs")
	instance := filepath.Join(home, "agentsfs")
	mustWriteFile(t, binary, "#!/bin/sh\n")
	mustWriteFile(t, filepath.Join(instance, "AGENTS.md"), "# Memory\n")

	out, err := runAFS(t, project, home, "uninstall", "--yes", "--binary", binary)
	if err != nil {
		t.Fatalf("afs uninstall failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(binary); !os.IsNotExist(err) {
		t.Fatalf("uninstall did not remove binary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(instance, "AGENTS.md")); err != nil {
		t.Fatalf("uninstall removed agentsfs data: %v", err)
	}
	if !strings.Contains(out, "Did not delete any agentsfs filesystem") {
		t.Fatalf("uninstall did not explain data safety:\n%s", out)
	}
}

func TestUninstallDryRunDoesNotRemoveFiles(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "afs")
	mustWriteFile(t, binary, "#!/bin/sh\n")

	out, err := runAFS(t, project, home, "uninstall", "--dry-run", "--binary", binary)
	if err != nil {
		t.Fatalf("afs uninstall --dry-run failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("dry run removed binary: %v", err)
	}
	if !strings.Contains(out, "Dry run only") {
		t.Fatalf("dry run did not report no changes:\n%s", out)
	}
}

func TestUninstallCanRemoveGlobalConnectionBlocks(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "afs")
	instance := filepath.Join(home, "agentsfs")
	globalConfig := filepath.Join(home, ".codex", "AGENTS.md")
	mustWriteFile(t, binary, "#!/bin/sh\n")
	mustWriteFile(t, globalConfig, "before\n\n"+core.ConnectionBlock(instance)+"\n\nafter\n")

	out, err := runAFS(t, project, home, "uninstall", "--yes", "--remove-global-connections", "--binary", binary)
	if err != nil {
		t.Fatalf("afs uninstall failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(globalConfig)
	if err != nil {
		t.Fatalf("global config missing: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "agentsfs:begin") || strings.Contains(text, instance) {
		t.Fatalf("global agentsfs block was not removed:\n%s", text)
	}
	if !strings.Contains(text, "before") || !strings.Contains(text, "after") {
		t.Fatalf("global config content was not preserved:\n%s", text)
	}
}

func TestUninstallRefusesNonAfsBinaryOverride(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "not-afs")
	mustWriteFile(t, binary, "#!/bin/sh\n")

	out, err := runAFS(t, project, home, "uninstall", "--yes", "--binary", binary)
	if err == nil {
		t.Fatalf("afs uninstall unexpectedly removed non-afs binary:\n%s", out)
	}
	if !strings.Contains(out, "binary name must be afs") {
		t.Fatalf("failure did not explain binary validation:\n%s", out)
	}
}

func runAFS(t *testing.T, dir, home string, args ...string) (string, error) {
	return runAFSWithInputEnv(t, dir, home, "", nil, args...)
}

func runAFSWithInputEnv(t *testing.T, dir, home, stdin string, extraEnv []string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{"-test.run=TestHelperProcess", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Env = append(os.Environ(),
		"GO_WANT_AFS_HELPER=1",
		"HOME="+home,
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
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

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
