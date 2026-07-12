package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStatusInstancesDiscoversRootsAndEnclosingInstance(t *testing.T) {
	workspace := t.TempDir()
	one := filepath.Join(workspace, "AgentsFS-personal")
	two := filepath.Join(workspace, "projects", "AgentsFS-stocks")
	ignored := filepath.Join(workspace, "node_modules", "AgentsFS-vendor")
	decoy := filepath.Join(workspace, "ordinary-project")
	external := t.TempDir()
	for _, dir := range []string{one, two, ignored, decoy} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(one, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeStatusTestFile(t, filepath.Join(one, "AGENTS.md"), statusTestContract())
	// A cloned instance may not have its gitignored .agentsfs/ sidecar yet, so
	// the contract-declaring AGENTS.md fallback must remain discoverable.
	writeStatusTestFile(t, filepath.Join(two, "AGENTS.md"), statusTestContract())
	writeStatusTestFile(t, filepath.Join(ignored, "AGENTS.md"), statusTestContract())
	writeStatusTestFile(t, filepath.Join(decoy, "AGENTS.md"), "# Ordinary agent instructions\n")
	if err := os.Mkdir(filepath.Join(external, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeStatusTestFile(t, filepath.Join(external, "AGENTS.md"), statusTestContract())
	if err := os.Symlink(external, filepath.Join(workspace, "linked-instance")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	report := StatusInstances([]string{workspace}, StatusOptions{})
	if len(report.Instances) != 2 {
		t.Fatalf("discovered %d instances, want 2: %+v", len(report.Instances), report.Instances)
	}
	one, _ = filepath.EvalSymlinks(one)
	two, _ = filepath.EvalSymlinks(two)
	if report.Instances[0].Path != one || report.Instances[0].DetectedBy != ".agentsfs" {
		t.Fatalf("first instance = %+v, want marker-discovered %s", report.Instances[0], one)
	}
	if report.Instances[1].Path != two || report.Instances[1].DetectedBy != "AGENTS.md" {
		t.Fatalf("second instance = %+v, want AGENTS.md-discovered %s", report.Instances[1], two)
	}
	report = StatusInstances([]string{workspace, one}, StatusOptions{})
	if len(report.Instances) != 2 {
		t.Fatalf("overlapping roots were not deduplicated: %+v", report.Instances)
	}
	report = StatusInstances([]string{filepath.Join(workspace, "linked-instance")}, StatusOptions{})
	external, _ = filepath.EvalSymlinks(external)
	if len(report.Instances) != 1 || report.Instances[0].Path != external {
		t.Fatalf("explicit symlink root did not resolve to instance %s: %+v", external, report.Instances)
	}
	report = StatusInstances([]string{ignored}, StatusOptions{})
	if len(report.Instances) != 1 {
		t.Fatalf("explicitly supplied pruned directory was not scanned: %+v", report.Instances)
	}

	inside := filepath.Join(one, "reference", "deep")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	report = StatusInstances([]string{inside}, StatusOptions{})
	if len(report.Instances) != 1 || report.Instances[0].Path != one {
		t.Fatalf("status from inside did not resolve enclosing instance: %+v", report.Instances)
	}
}

func TestStatusInstancesMarksDuplicateRemoteCheckouts(t *testing.T) {
	workspace := t.TempDir()
	bare := filepath.Join(workspace, "knowledge.git")
	runStatusGit(t, workspace, "init", "--bare", bare)

	for _, name := range []string{"checkout-a", "checkout-b"} {
		dir := filepath.Join(workspace, name)
		runStatusGit(t, workspace, "clone", bare, dir)
		if err := os.Mkdir(filepath.Join(dir, ".agentsfs"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeStatusTestFile(t, filepath.Join(dir, "AGENTS.md"), statusTestContract())
	}

	report := StatusInstances([]string{workspace}, StatusOptions{})
	if len(report.Instances) != 2 {
		t.Fatalf("instances = %+v, want two checkouts", report.Instances)
	}
	if report.Instances[0].DuplicateOf != "" {
		t.Fatalf("first checkout unexpectedly marked duplicate: %+v", report.Instances[0])
	}
	if report.Instances[1].DuplicateOf != report.Instances[0].Path {
		t.Fatalf("second checkout duplicate_of = %q, want %q", report.Instances[1].DuplicateOf, report.Instances[0].Path)
	}
}

func TestStatusDiscoveryReportsEntryBudgetAndPartialResults(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeStatusTestFile(t, filepath.Join(root, "AGENTS.md"), statusTestContract())
	writeStatusTestFile(t, filepath.Join(root, "notes", "one.md"), "one\n")

	scope := StatusScope{SearchRoot: root, maxEntries: 2, timeoutSeconds: 15, Complete: true}
	found := map[string]string{}
	var issues []StatusIssue
	discoverStatusRoots(&scope, found, &issues)
	if scope.Complete || scope.EntriesVisited != 2 || scope.timeoutSeconds != 15 || scope.IncompleteReason != "entry limit 2 reached" {
		t.Fatalf("bounded scan did not report deterministic partial state: %+v", scope)
	}
}

func TestStatusInstancesReportsGitSyncAndExplicitFetch(t *testing.T) {
	workspace := t.TempDir()
	bare := filepath.Join(workspace, "remote.git")
	primary := filepath.Join(workspace, "primary")
	secondary := filepath.Join(workspace, "secondary")
	runStatusGit(t, workspace, "init", "--bare", bare)
	if err := os.Mkdir(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	runStatusGit(t, primary, "init", "-b", "main")
	configureStatusGit(t, primary)
	if err := os.Mkdir(filepath.Join(primary, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeStatusTestFile(t, filepath.Join(primary, "AGENTS.md"), statusTestContract())
	runStatusGit(t, primary, "add", "AGENTS.md")
	runStatusGit(t, primary, "commit", "-m", "Initialize knowledge")
	runStatusGit(t, primary, "remote", "add", "origin", bare)
	runStatusGit(t, primary, "push", "-u", "origin", "main")

	st := singleStatus(t, primary, StatusOptions{})
	if st.Git.SyncState != "synced" || st.Git.Dirty {
		t.Fatalf("initial git status = %+v, want clean and synced", st.Git)
	}

	writeStatusTestFile(t, filepath.Join(primary, "local.md"), "local\n")
	runStatusGit(t, primary, "add", "local.md")
	runStatusGit(t, primary, "commit", "-m", "Local change")
	st = singleStatus(t, primary, StatusOptions{})
	if st.Git.SyncState != "ahead" || st.Git.Ahead != 1 {
		t.Fatalf("local-ahead git status = %+v, want ahead 1", st.Git)
	}

	runStatusGit(t, workspace, "clone", "--branch", "main", bare, secondary)
	configureStatusGit(t, secondary)
	writeStatusTestFile(t, filepath.Join(secondary, "remote.md"), "remote\n")
	runStatusGit(t, secondary, "add", "remote.md")
	runStatusGit(t, secondary, "commit", "-m", "Remote change")
	runStatusGit(t, secondary, "push")

	st = singleStatus(t, primary, StatusOptions{Fetch: true})
	if st.Git.SyncState != "diverged" || st.Git.Ahead != 1 || st.Git.Behind != 1 {
		t.Fatalf("post-fetch git status = %+v, want diverged 1/1", st.Git)
	}
}

func TestStatusSharedWorktreeDirtyStateIsInstanceScoped(t *testing.T) {
	repo := t.TempDir()
	runStatusGit(t, repo, "init", "-b", "main")
	configureStatusGit(t, repo)
	instance := filepath.Join(repo, "AgentsFS-team")
	if err := os.MkdirAll(filepath.Join(instance, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeStatusTestFile(t, filepath.Join(instance, "AGENTS.md"), statusTestContract())
	writeStatusTestFile(t, filepath.Join(repo, "app.txt"), "clean\n")
	runStatusGit(t, repo, "add", "AgentsFS-team/AGENTS.md", "app.txt")
	runStatusGit(t, repo, "commit", "-m", "Initialize shared knowledge")

	writeStatusTestFile(t, filepath.Join(repo, "app.txt"), "unrelated host edit\n")
	st := singleStatus(t, instance, StatusOptions{})
	if st.Mode != "shared" || st.Git.Dirty {
		t.Fatalf("shared instance inherited unrelated host dirtiness: %+v", st)
	}
	writeStatusTestFile(t, filepath.Join(instance, "AGENTS.md"), statusTestContract()+"local edit\n")
	st = singleStatus(t, instance, StatusOptions{})
	if !st.Git.Dirty {
		t.Fatalf("shared instance edit was not reported dirty: %+v", st.Git)
	}
}

func TestNormalizeRemoteIdentityMatchesHTTPSAndSSHWithoutCredentials(t *testing.T) {
	https := normalizeRemoteIdentity("https://token@example.com/owner/knowledge.git", "/tmp")
	ssh := normalizeRemoteIdentity("git@example.com:owner/knowledge.git", "/tmp")
	if https != ssh {
		t.Fatalf("same remote normalized differently: https=%q ssh=%q", https, ssh)
	}
	if https != "network:example.com/owner/knowledge" {
		t.Fatalf("normalized remote = %q", https)
	}
	localPath := filepath.Join(t.TempDir(), "knowledge.git")
	fromPath := normalizeRemoteIdentity(localPath, "/tmp")
	fromURL := normalizeRemoteIdentity("file://"+localPath, "/tmp")
	if fromPath != fromURL {
		t.Fatalf("same local remote normalized differently: path=%q URL=%q", fromPath, fromURL)
	}
}

func singleStatus(t *testing.T, root string, opts StatusOptions) InstanceStatus {
	t.Helper()
	report := StatusInstances([]string{root}, opts)
	if len(report.Instances) != 1 {
		t.Fatalf("got %d instances: %+v", len(report.Instances), report.Instances)
	}
	return report.Instances[0]
}

func statusTestContract() string {
	return "---\ndescription: Test AgentsFS root.\nagentsfs_contract: " + CurrentContractVersion() + "\n---\n# This folder is an agentsfs\n"
}

func writeStatusTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func configureStatusGit(t *testing.T, dir string) {
	t.Helper()
	runStatusGit(t, dir, "config", "user.name", "test")
	runStatusGit(t, dir, "config", "user.email", "test@example.com")
}

func runStatusGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}
