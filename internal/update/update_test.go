package update

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLatestReleaseVersion(t *testing.T) {
	cases := []struct {
		name, out, want string
	}{
		{"none", "abc\trefs/heads/main\n", ""},
		{"single", "abc\trefs/tags/v0.1.0\n", "0.1.0"},
		{"peeled entries fold in", "abc\trefs/tags/v0.1.0\ndef\trefs/tags/v0.1.0^{}\n", "0.1.0"},
		{"numeric ordering", "a\trefs/tags/v0.9.0\nb\trefs/tags/v0.10.0\nc\trefs/tags/v0.2.0\n", "0.10.0"},
		{"non-release tags ignored", "a\trefs/tags/latest\nb\trefs/tags/vNext\nc\trefs/tags/v1.2.3\n", "1.2.3"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := latestReleaseVersion(c.out); got != c.want {
			t.Errorf("%s: latestReleaseVersion = %q, want %q", c.name, got, c.want)
		}
	}
}

// tagRepo builds a local git repo Check can ls-remote, with the given tags.
func tagRepo(t *testing.T, tags ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--initial-branch=main")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "seed")
	for _, tag := range tags {
		run("tag", tag)
	}
	return dir
}

// With release tags present, Check compares versions against the newest tag —
// a binary at the latest release is up to date even when the branch head has
// moved past it (the release-loop bug), and an older one is not.
func TestCheckPrefersReleaseTags(t *testing.T) {
	repo := tagRepo(t, "v0.1.0", "v0.2.0")
	t.Setenv("AFS_UPDATE_REPO", repo)
	t.Setenv("AFS_UPDATE_REPO_SSH", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, err := Check(ctx, "0.2.0", "notheadrevision")
	if err != nil {
		t.Fatal(err)
	}
	if !status.UpToDate || status.LatestVersion != "0.2.0" {
		t.Errorf("binary at latest release should be up to date: %+v", status)
	}

	status, err = Check(ctx, "0.1.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if status.UpToDate || status.LatestVersion != "0.2.0" {
		t.Errorf("binary behind latest release should not be up to date: %+v", status)
	}
}

// With no release tags, Check falls back to comparing the build revision
// against the branch head, matching the installer's source-build fallback.
func TestCheckFallsBackToRevisionWithoutTags(t *testing.T) {
	repo := tagRepo(t)
	t.Setenv("AFS_UPDATE_REPO", repo)
	t.Setenv("AFS_UPDATE_REPO_SSH", "")
	head, err := exec.Command("git", "-C", filepath.Clean(repo), "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	rev := string(head[:12])
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, err := Check(ctx, "0.1.0", rev)
	if err != nil {
		t.Fatal(err)
	}
	if !status.UpToDate || status.LatestVersion != "" {
		t.Errorf("build at head should be up to date via revision fallback: %+v", status)
	}

	status, err = Check(ctx, "0.1.0", "0000000000ab")
	if err != nil {
		t.Fatal(err)
	}
	if status.UpToDate {
		t.Errorf("stale revision should not be up to date: %+v", status)
	}
}
