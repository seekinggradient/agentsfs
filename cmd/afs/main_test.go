package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

func TestHelpDocumentsHostedCommands(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	out, err := runAFS(t, project, home, "help")
	if err != nil {
		t.Fatalf("afs help failed: %v\n%s", err, out)
	}
	for _, want := range []string{"afs login", "afs hosted <create|list|connect|status|push|pull|clone>", "real git", "backup/restore"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help did not contain %q:\n%s", want, out)
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
	for _, want := range []string{"afs uninstall", "Never deletes any agentsfs filesystem"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help did not contain %q:\n%s", want, out)
		}
	}
}

func TestUninstallRemovesBinaryAndHostedAuthButKeepsData(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	configHome := filepath.Join(home, "config")
	binary := filepath.Join(home, ".local", "bin", "afs")
	instance := filepath.Join(home, "agentsfs")
	mustWriteFile(t, binary, "#!/bin/sh\n")
	mustWriteFile(t, filepath.Join(configHome, "agentsfs", "hosted.json"), `{"token":"afs_testtoken"}`+"\n")
	mustWriteFile(t, filepath.Join(instance, "AGENTS.md"), "# Memory\n")

	out, err := runAFSWithInputEnv(t, project, home, "", []string{"AFS_CONFIG_HOME=" + configHome},
		"uninstall", "--yes", "--binary", binary)
	if err != nil {
		t.Fatalf("afs uninstall failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(binary); !os.IsNotExist(err) {
		t.Fatalf("uninstall did not remove binary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configHome, "agentsfs", "hosted.json")); !os.IsNotExist(err) {
		t.Fatalf("uninstall did not remove hosted auth: %v", err)
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
	configHome := filepath.Join(home, "config")
	binary := filepath.Join(home, ".local", "bin", "afs")
	mustWriteFile(t, binary, "#!/bin/sh\n")
	mustWriteFile(t, filepath.Join(configHome, "agentsfs", "hosted.json"), `{"token":"afs_testtoken"}`+"\n")

	out, err := runAFSWithInputEnv(t, project, home, "", []string{"AFS_CONFIG_HOME=" + configHome},
		"uninstall", "--dry-run", "--binary", binary)
	if err != nil {
		t.Fatalf("afs uninstall --dry-run failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("dry run removed binary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configHome, "agentsfs", "hosted.json")); err != nil {
		t.Fatalf("dry run removed hosted auth: %v", err)
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

	out, err := runAFS(t, project, home, "uninstall", "--yes", "--keep-auth", "--remove-global-connections", "--binary", binary)
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

func TestLoginStoresTokenOutsideRepo(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	configHome := filepath.Join(home, "config")
	token := "afs_testtoken"

	out, err := runAFSWithInputEnv(t, project, home, token+"\n", []string{"AFS_CONFIG_HOME=" + configHome},
		"login", "--endpoint", "http://127.0.0.1:4321", "--token-stdin")
	if err != nil {
		t.Fatalf("afs login failed: %v\n%s", err, out)
	}
	if strings.Contains(out, token) {
		t.Fatalf("login printed the token:\n%s", out)
	}

	configPath := filepath.Join(configHome, "agentsfs", "hosted.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("hosted config was not written outside repo: %v", err)
	}
	if !strings.Contains(string(data), token) || !strings.Contains(string(data), "http://127.0.0.1:4321") {
		t.Fatalf("hosted config missing endpoint/token fields:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(project, ".agentsfs", "hosted.json")); !os.IsNotExist(err) {
		t.Fatalf("login wrote hosted config inside the repo: %v", err)
	}
}

func TestHostedConnectWritesNonSecretConnection(t *testing.T) {
	home := t.TempDir()
	instance := filepath.Join(t.TempDir(), "memory")
	server := hostedFakeServer(t, nil)
	defer server.Close()

	loginForHostedTest(t, t.TempDir(), home, server.URL)
	if out, err := runAFS(t, t.TempDir(), home, "init", instance, "--yes"); err != nil {
		t.Fatalf("afs init failed: %v\n%s", err, out)
	}
	out, err := runAFS(t, instance, home, "hosted", "connect", "fs_123")
	if err != nil {
		t.Fatalf("afs hosted connect failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(instance, ".agentsfs", "hosted.json"))
	if err != nil {
		t.Fatalf("connection file not written: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "fs_123") || !strings.Contains(text, server.URL) {
		t.Fatalf("connection file missing remote metadata:\n%s", text)
	}
	if strings.Contains(text, "afs_testtoken") {
		t.Fatalf("connection file contains hosted token:\n%s", text)
	}
}

func TestHostedConnectConfiguresGitRemoteWithoutSecrets(t *testing.T) {
	home := t.TempDir()
	instance := filepath.Join(t.TempDir(), "memory")
	remoteURL := "https://github.com/agentsfs-test/fs-123.git"
	server := hostedFakeServerWithRemote(t, remoteURL, nil)
	defer server.Close()

	loginForHostedTest(t, t.TempDir(), home, server.URL)
	if out, err := runAFS(t, t.TempDir(), home, "init", instance, "--yes"); err != nil {
		t.Fatalf("afs init failed: %v\n%s", err, out)
	}
	out, err := runAFS(t, instance, home, "hosted", "connect", "fs_123")
	if err != nil {
		t.Fatalf("afs hosted connect failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Configured git remote") {
		t.Fatalf("connect did not report git remote configuration:\n%s", out)
	}

	remote, err := gitOutputForTest(instance, "remote", "get-url", "agentsfs")
	if err != nil {
		t.Fatalf("git remote missing: %v\n%s", err, remote)
	}
	authRemoteURL := "https://x-access-token@github.com/agentsfs-test/fs-123.git"
	if strings.TrimSpace(remote) != authRemoteURL {
		t.Fatalf("git remote = %q, want %q", strings.TrimSpace(remote), authRemoteURL)
	}
	helper, err := gitOutputForTest(instance, "config", "--get-all", "credential."+authRemoteURL+".helper")
	if err != nil {
		t.Fatalf("credential helper missing: %v\n%s", err, helper)
	}
	if !strings.Contains(helper, "hosted credential-helper") {
		t.Fatalf("credential helper does not call afs hosted credential-helper:\n%s", helper)
	}
	if strings.Contains(helper, "afs_testtoken") {
		t.Fatalf("credential helper contains hosted token:\n%s", helper)
	}
	repoWideHelper, _ := gitOutputForTest(instance, "config", "--local", "--get-all", "credential.helper")
	if strings.Contains(repoWideHelper, "hosted credential-helper") {
		t.Fatalf("hosted helper should be URL-scoped, not repo-wide:\n%s", repoWideHelper)
	}

	data, err := os.ReadFile(filepath.Join(instance, ".agentsfs", "hosted.json"))
	if err != nil {
		t.Fatalf("connection file not written: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, remoteURL) || strings.Contains(text, "afs_testtoken") {
		t.Fatalf("connection file has wrong remote/secret content:\n%s", text)
	}
}

func TestHostedCredentialHelperPrintsGitCredentialOnly(t *testing.T) {
	home := t.TempDir()
	server := hostedFakeServerWithRemote(t, "https://github.com/agentsfs-test/fs-123.git", func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "POST" && r.URL.Path == "/api/filesystems/fs_123/git-credentials" {
			writeJSON(t, w, map[string]any{
				"provider":       "github",
				"remote_url":     "https://github.com/agentsfs-test/fs-123.git",
				"username":       "x-access-token",
				"password":       "github-short-lived-test-token",
				"expires_at":     "2026-06-13T01:00:00Z",
				"default_branch": "main",
			})
			return true
		}
		return false
	})
	defer server.Close()

	loginForHostedTest(t, t.TempDir(), home, server.URL)
	out, err := runAFSWithInputEnv(t, t.TempDir(), home, "protocol=https\nhost=github.com\n\n", nil,
		"hosted", "credential-helper", "--filesystem", "fs_123", "--endpoint", server.URL, "get")
	if err != nil {
		t.Fatalf("credential helper failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "username=x-access-token") || !strings.Contains(out, "password=github-short-lived-test-token") {
		t.Fatalf("credential helper did not emit git credentials:\n%s", out)
	}
	if strings.Contains(out, "afs_testtoken") {
		t.Fatalf("credential helper printed hosted API token:\n%s", out)
	}
}

func TestHostedPushAndCloneUseRealGitRemote(t *testing.T) {
	home := t.TempDir()
	instance := filepath.Join(t.TempDir(), "memory")
	bare := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, t.TempDir(), "init", "--bare", bare)
	remoteURL := "file://" + bare
	server := hostedFakeServerWithRemote(t, remoteURL, nil)
	defer server.Close()

	loginForHostedTest(t, t.TempDir(), home, server.URL)
	if out, err := runAFS(t, t.TempDir(), home, "init", instance, "--yes"); err != nil {
		t.Fatalf("afs init failed: %v\n%s", err, out)
	}
	mustWriteFile(t, filepath.Join(instance, "projects", "status.md"), "# Local git remote\n")
	runGit(t, instance, "add", "-A", ".")
	runGit(t, instance, "commit", "-m", "Seed hosted git test")
	if out, err := runAFS(t, instance, home, "hosted", "connect", "fs_123"); err != nil {
		t.Fatalf("afs hosted connect failed: %v\n%s", err, out)
	}
	out, err := runAFS(t, instance, home, "hosted", "push")
	if err != nil {
		t.Fatalf("afs hosted push failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Pushed HEAD") {
		t.Fatalf("push did not report git push:\n%s", out)
	}
	showRef, err := gitOutputForTest("", "--git-dir", bare, "show-ref", "--verify", "refs/heads/main")
	if err != nil {
		t.Fatalf("bare remote did not receive main: %v\n%s", err, showRef)
	}

	cloneDir := filepath.Join(t.TempDir(), "cloned-memory")
	out, err = runAFS(t, t.TempDir(), home, "hosted", "clone", "fs_123", cloneDir)
	if err != nil {
		t.Fatalf("afs hosted clone failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(cloneDir, "projects", "status.md"))
	if err != nil {
		t.Fatalf("clone did not contain pushed file: %v", err)
	}
	if string(data) != "# Local git remote\n" {
		t.Fatalf("clone file content mismatch:\n%s", data)
	}
}

func TestHostedBackupSendsTextFilesAndSkipsInternals(t *testing.T) {
	home := t.TempDir()
	instance := filepath.Join(t.TempDir(), "memory")
	var mu sync.Mutex
	var pushed []string
	server := hostedFakeServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "PUT" && r.URL.Path == "/api/filesystems/fs_123/files" {
			var body struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("bad push body: %v", err)
			}
			mu.Lock()
			pushed = append(pushed, body.Path)
			mu.Unlock()
			writeJSON(t, w, map[string]any{"file": map[string]any{"path": body.Path, "size": len(body.Content)}})
			return true
		}
		return false
	})
	defer server.Close()

	loginForHostedTest(t, t.TempDir(), home, server.URL)
	if out, err := runAFS(t, t.TempDir(), home, "init", instance, "--yes"); err != nil {
		t.Fatalf("afs init failed: %v\n%s", err, out)
	}
	if out, err := runAFS(t, instance, home, "hosted", "connect", "fs_123"); err != nil {
		t.Fatalf("afs hosted connect failed: %v\n%s", err, out)
	}
	mustWriteFile(t, filepath.Join(instance, "projects", "status.md"), "# Status\n")
	mustWriteFile(t, filepath.Join(instance, ".gitattributes"), "*.png filter=lfs diff=lfs merge=lfs -text\n")

	out, err := runAFS(t, instance, home, "hosted", "backup")
	if err != nil {
		t.Fatalf("afs hosted backup failed: %v\n%s", err, out)
	}

	mu.Lock()
	got := strings.Join(pushed, "\n")
	mu.Unlock()
	for _, want := range []string{"AGENTS.md", "README.md", "scratch/INDEX.md", "projects/status.md", ".gitattributes"} {
		if !strings.Contains(got, want) {
			t.Fatalf("push did not send %s; sent:\n%s", want, got)
		}
	}
	if strings.Contains(got, ".agentsfs/hosted.json") || strings.Contains(got, ".git/") {
		t.Fatalf("push sent internal files:\n%s", got)
	}
}

func TestHostedRestoreRefusesOverwriteUnlessForced(t *testing.T) {
	home := t.TempDir()
	instance := filepath.Join(t.TempDir(), "memory")
	server := hostedFakeServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/filesystems/fs_123/tree":
			writeJSON(t, w, map[string]any{
				"tree": []map[string]any{{
					"path": "projects/status.md",
					"name": "status.md",
					"type": "file",
				}},
			})
			return true
		case r.Method == "GET" && r.URL.Path == "/api/filesystems/fs_123/files":
			writeJSON(t, w, map[string]any{
				"file":    map[string]any{"path": "projects/status.md", "size": 14},
				"content": "# Remote\n",
			})
			return true
		default:
			return false
		}
	})
	defer server.Close()

	loginForHostedTest(t, t.TempDir(), home, server.URL)
	if out, err := runAFS(t, t.TempDir(), home, "init", instance, "--yes"); err != nil {
		t.Fatalf("afs init failed: %v\n%s", err, out)
	}
	if out, err := runAFS(t, instance, home, "hosted", "connect", "fs_123"); err != nil {
		t.Fatalf("afs hosted connect failed: %v\n%s", err, out)
	}
	mustWriteFile(t, filepath.Join(instance, "projects", "status.md"), "# Local\n")

	out, err := runAFS(t, instance, home, "hosted", "restore")
	if err == nil {
		t.Fatalf("restore unexpectedly overwrote local file:\n%s", out)
	}
	if !strings.Contains(out, "rerun with --force") {
		t.Fatalf("pull failure did not explain force:\n%s", out)
	}

	out, err = runAFS(t, instance, home, "hosted", "restore", "--force")
	if err != nil {
		t.Fatalf("restore --force failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(instance, "projects", "status.md"))
	if err != nil {
		t.Fatalf("pulled file missing: %v", err)
	}
	if string(data) != "# Remote\n" {
		t.Fatalf("force pull did not write remote content:\n%s", data)
	}
}

func TestHostedPathValidationAllowsOrdinaryDotfiles(t *testing.T) {
	for _, rel := range []string{".gitattributes", ".gitignore", "notes/.keep"} {
		if err := validateHostedRelPath(rel); err != nil {
			t.Fatalf("%s should be allowed: %v", rel, err)
		}
	}
	for _, rel := range []string{".git/config", ".agentsfs/index.db", "notes/../secret.md", "/absolute.md"} {
		if err := validateHostedRelPath(rel); err == nil {
			t.Fatalf("%s should be rejected", rel)
		}
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

func gitOutputForTest(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func loginForHostedTest(t *testing.T, dir, home, endpoint string) {
	t.Helper()
	out, err := runAFSWithInputEnv(t, dir, home, "afs_testtoken\n", nil, "login", "--endpoint", endpoint, "--token-stdin")
	if err != nil {
		t.Fatalf("afs login failed: %v\n%s", err, out)
	}
}

func hostedFakeServer(t *testing.T, custom func(http.ResponseWriter, *http.Request) bool) *httptest.Server {
	return hostedFakeServerWithRemote(t, "", custom)
}

func hostedFakeServerWithRemote(t *testing.T, remoteURL string, custom func(http.ResponseWriter, *http.Request) bool) *httptest.Server {
	t.Helper()
	filesystem := map[string]any{
		"id":          "fs_123",
		"name":        "Hosted Test",
		"slug":        "hosted-test",
		"description": "Test filesystem",
		"file_count":  3,
		"bytes_used":  123,
		"updated_at":  "2026-06-13T00:00:00Z",
	}
	if remoteURL != "" {
		filesystem["remote"] = map[string]any{
			"provider":       "github",
			"remote_url":     remoteURL,
			"html_url":       "https://github.com/agentsfs-test/fs-123",
			"owner":          "agentsfs-test",
			"repo":           "fs-123",
			"default_branch": "main",
		}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "Bearer afs_testtoken" {
			http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
			return
		}
		if custom != nil && custom(w, r) {
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/filesystems":
			writeJSON(t, w, map[string]any{"filesystems": []map[string]any{filesystem}})
		case r.Method == "GET" && r.URL.Path == "/api/filesystems/fs_123":
			writeJSON(t, w, map[string]any{"filesystem": filesystem})
		case r.Method == "GET" && r.URL.Path == "/api/filesystems/fs_123/tree":
			writeJSON(t, w, map[string]any{"tree": []map[string]any{}})
		default:
			t.Fatalf("unexpected hosted request: %s %s", r.Method, r.URL.String())
		}
	}))
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write json: %v", err)
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
