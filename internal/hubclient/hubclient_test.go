package hubclient

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRevisionForPushLeavesHostRepositoryOutOfSharedInstance(t *testing.T) {
	repo := t.TempDir()
	instance := filepath.Join(repo, "agentsfs")
	if err := os.MkdirAll(instance, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "AgentsFS Test"},
		{"config", "user.email", "agentsfs@example.test"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instance, "AGENTS.md"), []byte("# AgentsFS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "commit", "-qm", "Add app and shared memory").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}

	revision, err := revisionForPush(instance)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := exec.Command("git", "-C", repo, "ls-tree", "--name-only", revision).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(tree)); got != "AGENTS.md" {
		t.Fatalf("shared push tree = %q, want only AgentsFS contents", got)
	}

	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte("package app\n\nconst Version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instance, "README.md"), []byte("# Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add update: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo, "commit", "-qm", "Update app and shared memory").CombinedOutput(); err != nil {
		t.Fatalf("git commit update: %v: %s", err, out)
	}
	updated, err := revisionForPush(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", repo, "merge-base", "--is-ancestor", revision, updated).Run(); err != nil {
		t.Fatalf("subsequent shared revision is not a fast-forward descendant: %v", err)
	}
	updatedTree, err := exec.Command("git", "-C", repo, "ls-tree", "--name-only", updated).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(updatedTree)); got != "AGENTS.md\nREADME.md" {
		t.Fatalf("updated shared push tree = %q, want only AgentsFS contents", got)
	}
}

func TestRevisionForPushUsesHeadForStandaloneInstance(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if got, err := revisionForPush(repo); err != nil || got != "HEAD" {
		t.Fatalf("revisionForPush standalone = %q, %v; want HEAD", got, err)
	}
}

func TestParseRef(t *testing.T) {
	cases := []struct {
		name, deflt         string
		wantOwner, wantSlug string
		wantErr             bool
	}{
		{"kauai-2026", "seekinggradient", "seekinggradient", "kauai-2026", false},
		{"someone/their-notes", "seekinggradient", "someone", "their-notes", false},
		{"My Trip Notes", "seekinggradient", "seekinggradient", "my-trip-notes", false},
		{"alice/My Notes", "seekinggradient", "alice", "my-notes", false},
		{"", "seekinggradient", "", "", true},
		{"alice/", "seekinggradient", "", "", true},
	}
	for _, c := range cases {
		owner, slug, err := ParseRef(c.name, c.deflt)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseRef(%q,%q) err=%v, wantErr=%v", c.name, c.deflt, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if owner != c.wantOwner || slug != c.wantSlug {
			t.Errorf("ParseRef(%q,%q) = %q/%q, want %q/%q", c.name, c.deflt, owner, slug, c.wantOwner, c.wantSlug)
		}
	}
}

func TestListPreservesSharedRepositoryMetadata(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bob" || r.URL.Query().Get("format") != "json" {
			t.Fatalf("listing request = %s, want /bob?format=json", r.URL.RequestURI())
		}
		user, token, ok := r.BasicAuth()
		if !ok || user != "bob" || token != "secret" {
			t.Fatalf("basic auth = %q/%q/%t, want bob/secret/true", user, token, ok)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"user":"bob","repos":[
{"owner":"bob","name":"own-notes","url":"https://hub/bob/own-notes"},
{"owner":"alice","name":"shared-notes","role":"write","shared":true,"url":"https://hub/alice/shared-notes"}
]}`)
	}))
	defer server.Close()
	if err := Save(Config{URL: server.URL, User: "bob", Token: "secret"}); err != nil {
		t.Fatal(err)
	}

	repos, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("List returned %d repos, want 2: %+v", len(repos), repos)
	}
	if repos[1].Owner != "alice" || repos[1].Name != "shared-notes" || !repos[1].Shared || repos[1].Role != "write" {
		t.Fatalf("shared repo = %+v", repos[1])
	}
}

func TestSharedCheckoutHubRemoteIsPushableAndVisibleToStatus(t *testing.T) {
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	if err := setRemote(dir, "hub", "https://hub.example/alice/shared-notes.git"); err != nil {
		t.Fatal(err)
	}
	if got := hubRemoteURL(dir); got != "https://hub.example/alice/shared-notes.git" {
		t.Fatalf("hub remote = %q", got)
	}

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	if err := Save(Config{URL: "https://hub.example", User: "bob", Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	status := GetStatus(dir)
	if !status.SignedIn || !status.Linked || status.LinkedURL != "https://hub.example/alice/shared-notes.git" {
		t.Fatalf("shared checkout status = %+v", status)
	}
}

func TestHandleCredentialOnlyAnswersForConfiguredHub(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(Config{URL: "https://hub.example", User: "bob", Token: "secret"}); err != nil {
		t.Fatal(err)
	}

	var got bytes.Buffer
	if err := HandleCredential("get", strings.NewReader("protocol=https\nhost=hub.example\npath=alice/shared-notes.git\n\n"), &got); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"username=bob", "password=secret"} {
		if !strings.Contains(got.String(), want) {
			t.Errorf("credential response missing %q: %q", want, got.String())
		}
	}

	got.Reset()
	if err := HandleCredential("get", strings.NewReader("protocol=https\nhost=other.example\n\n"), &got); err != nil {
		t.Fatal(err)
	}
	if got.Len() != 0 {
		t.Fatalf("credential helper answered for another host: %q", got.String())
	}
}

// Regression for the reported bug: `afs hub pull <name> --merge` run from
// inside an instance root folded the repo into a nested ./<name>/ subdirectory
// (a full duplicate) instead of folding its files into the current instance.
// With --merge and no explicit dir, Clone must fold into the enclosing
// instance: remote-only files are added, identical files skipped, and files
// that differ from the local copy are quarantined (never silently overwritten),
// while the remote's .git/.agentsfs and the local .agentsfs are left alone.
func TestCloneMergeFoldsIntoCurrentInstanceWithoutNesting(t *testing.T) {
	remotes := t.TempDir()
	seedBareRemote(t, remotes, "alice", "research", map[string]string{
		"added.md":             "remote-only note\n",
		"same.md":              "shared identical\n",
		"diff.md":              "remote version of diff\n",
		".agentsfs/.gitignore": "*\n!.gitignore\n",
	})

	instance := t.TempDir()
	seedInstance(t, instance, map[string]string{
		"same.md": "shared identical\n",      // identical → skipped
		"diff.md": "LOCAL version of diff\n", // differs → quarantined, kept
	})

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(Config{URL: remotes, User: "alice", Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(instance)

	res, err := Clone("research", "", true)
	if err != nil {
		t.Fatalf("merge pull failed: %v", err)
	}
	if !res.Merged {
		t.Fatalf("result not marked merged: %+v", res)
	}

	// The bug: a nested ./research/ checkout must NOT be created.
	if _, err := os.Stat(filepath.Join(instance, "research")); !os.IsNotExist(err) {
		t.Fatalf("merge created a nested ./research/ instead of folding in (stat err=%v)", err)
	}

	// Remote-only file added at the instance root.
	assertFile(t, filepath.Join(instance, "added.md"), "remote-only note\n")
	assertContains(t, "added", res.Added, "added.md")

	// Identical file skipped, left untouched.
	assertFile(t, filepath.Join(instance, "same.md"), "shared identical\n")
	assertContains(t, "skipped", res.Skipped, "same.md")

	// Differing file: local copy untouched, remote copy quarantined and reported.
	assertFile(t, filepath.Join(instance, "diff.md"), "LOCAL version of diff\n")
	assertContains(t, "conflicts", res.Conflicts, "diff.md")
	if res.QuarantinePath != "scratch/hub-merge-research" {
		t.Fatalf("QuarantinePath = %q, want scratch/hub-merge-research", res.QuarantinePath)
	}
	assertFile(t, filepath.Join(instance, "scratch", "hub-merge-research", "diff.md"), "remote version of diff\n")

	// Machine-territory guards: the remote's .git and .agentsfs are never folded
	// in, and the instance's own .agentsfs is untouched.
	if _, err := os.Stat(filepath.Join(instance, ".git")); !os.IsNotExist(err) {
		t.Fatalf("merge brought in a .git (stat err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(instance, ".agentsfs", ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("merge folded the remote .agentsfs into the instance (stat err=%v)", err)
	}
}

// With an explicit [dir], --merge folds into that instance rather than the
// current directory.
func TestCloneMergeFoldsIntoExplicitDir(t *testing.T) {
	remotes := t.TempDir()
	seedBareRemote(t, remotes, "alice", "notes", map[string]string{"a.md": "remote a\n"})

	target := filepath.Join(t.TempDir(), "kb")
	seedInstance(t, target, nil)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(Config{URL: remotes, User: "alice", Token: "secret"}); err != nil {
		t.Fatal(err)
	}

	res, err := Clone("notes", target, true)
	if err != nil {
		t.Fatalf("merge into explicit dir failed: %v", err)
	}
	if !res.Merged {
		t.Fatalf("result not marked merged: %+v", res)
	}
	assertFile(t, filepath.Join(target, "a.md"), "remote a\n")
	if _, err := os.Stat(filepath.Join(target, "notes")); !os.IsNotExist(err) {
		t.Fatalf("merge into explicit dir still nested a ./notes/ (stat err=%v)", err)
	}
}

// A plain pull (no --merge) is unchanged: it clones into ./<dir> as its own git
// checkout, keeping .git and a clean hub remote.
func TestClonePlainPullStaysAnIndependentCheckout(t *testing.T) {
	remotes := t.TempDir()
	seedBareRemote(t, remotes, "alice", "notes", map[string]string{"a.md": "remote a\n"})

	dest := filepath.Join(t.TempDir(), "dest")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(Config{URL: remotes, User: "alice", Token: "secret"}); err != nil {
		t.Fatal(err)
	}

	res, err := Clone("notes", dest, false)
	if err != nil {
		t.Fatalf("plain pull failed: %v", err)
	}
	if res.Merged || res.Updated {
		t.Fatalf("plain pull should be a fresh clone: %+v", res)
	}
	assertFile(t, filepath.Join(dest, "a.md"), "remote a\n")
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatalf("plain pull dropped .git: %v", err)
	}
	if got := hubRemoteURL(dest); got != remotes+"/alice/notes.git" {
		t.Fatalf("plain pull hub remote = %q", got)
	}
}

// seedBareRemote builds a fake hub remote: a bare git repo at
// <base>/<owner>/<slug>.git holding files, cloneable offline over a local path.
func seedBareRemote(t *testing.T, base, owner, slug string, files map[string]string) {
	t.Helper()
	work := t.TempDir()
	mustGit(t, work, "init", "-q", "-b", "main")
	mustGit(t, work, "config", "user.name", "Remote")
	mustGit(t, work, "config", "user.email", "remote@example.test")
	for rel, content := range files {
		p := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, work, "add", "-A")
	mustGit(t, work, "commit", "-q", "-m", "seed")
	bare := filepath.Join(base, owner, slug+".git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, base, "clone", "--bare", "-q", work, bare)
}

// seedInstance makes root a minimal agentsfs instance (a .agentsfs/ marker plus
// a contract-declaring AGENTS.md) holding files.
func seedInstance(t *testing.T, root string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeUnder(t, root, "AGENTS.md", "# This folder is an agentsfs\n")
	for rel, content := range files {
		writeUnder(t, root, rel, content)
	}
}

func writeUnder(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func assertContains(t *testing.T, label string, got []string, want string) {
	t.Helper()
	for _, g := range got {
		if g == want {
			return
		}
	}
	t.Fatalf("%s = %v, want to contain %q", label, got, want)
}

func TestEnsureCredentialHelperIsIdempotent(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", configHome)
	if err := Save(Config{URL: "https://hub.example", User: "bob", Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCredentialHelper(); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCredentialHelper(); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("git", "config", "--global", "--get-all", "credential.helper").Output()
	if err != nil {
		t.Fatalf("read global credential helpers: %v", err)
	}
	if strings.Count(string(out), gitCredentialHelper) != 1 {
		t.Fatalf("global credential helpers = %q", out)
	}
}

// The quarantine location honors the target instance's OWN scratch role (the
// `afs roles` contract: consumers read the resolved path, never hardcode a
// name) rather than assuming "scratch/".
func TestCloneMergeQuarantinesUnderResolvedScratchRole(t *testing.T) {
	remotes := t.TempDir()
	seedBareRemote(t, remotes, "alice", "research", map[string]string{
		"diff.md": "remote version\n",
	})
	instance := t.TempDir()
	seedInstance(t, instance, map[string]string{
		"diff.md":                "LOCAL version\n",
		"agent-scratch/INDEX.md": "---\nagentsfs_role: scratch\n---\n",
	})
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(Config{URL: remotes, User: "alice", Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(instance)

	res, err := Clone("research", "", true)
	if err != nil {
		t.Fatalf("merge pull failed: %v", err)
	}
	if res.QuarantinePath != "agent-scratch/hub-merge-research" {
		t.Fatalf("QuarantinePath = %q, want agent-scratch/hub-merge-research", res.QuarantinePath)
	}
	assertFile(t, filepath.Join(instance, "agent-scratch", "hub-merge-research", "diff.md"), "remote version\n")
	assertFile(t, filepath.Join(instance, "diff.md"), "LOCAL version\n")
}

// A symlink in the remote is never folded: copying one would materialize the
// link's LOCAL target as file content (a hostile KB could plant a link to a
// local secret and have the fold copy it into the instance, and a later push
// publish it). It is reported and skipped instead.
func TestCloneMergeSkipsSymlinks(t *testing.T) {
	remotes := t.TempDir()
	work := t.TempDir()
	mustGit(t, work, "init", "-q", "-b", "main")
	mustGit(t, work, "config", "user.name", "Remote")
	mustGit(t, work, "config", "user.email", "remote@example.test")
	if err := os.WriteFile(filepath.Join(work, "note.md"), []byte("plain note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(work, "sneaky.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	mustGit(t, work, "add", "-A")
	mustGit(t, work, "commit", "-q", "-m", "seed")
	bare := filepath.Join(remotes, "alice", "research.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, remotes, "clone", "--bare", "-q", work, bare)

	instance := t.TempDir()
	seedInstance(t, instance, nil)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(Config{URL: remotes, User: "alice", Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(instance)

	res, err := Clone("research", "", true)
	if err != nil {
		t.Fatalf("merge pull failed: %v", err)
	}
	assertContains(t, "symlinks", res.Symlinks, "sneaky.md")
	if _, err := os.Lstat(filepath.Join(instance, "sneaky.md")); !os.IsNotExist(err) {
		t.Fatalf("symlink was folded into the instance (lstat err=%v)", err)
	}
	assertFile(t, filepath.Join(instance, "note.md"), "plain note\n")
}
