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
