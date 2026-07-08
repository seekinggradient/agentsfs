package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentsfs.ai/afs/internal/buildinfo"
)

type Status struct {
	LocalVersion   string
	LatestVersion  string // newest release tag (without the v); empty when the repo has no v* tags
	LocalRevision  string
	RemoteRevision string // head of Ref; only consulted when no release tags exist
	Repo           string
	Ref            string
	UpToDate       bool
}

// Check reports whether a newer afs is installable. Release tags are the
// primary signal: `afs update` installs the latest release asset, so the
// check compares the local version against the newest v* tag — comparing
// against the branch head would report "update available" forever on a
// release binary that is already the newest installable one. Repos with no
// releases yet (self-hosted forks, pre-release) fall back to comparing the
// local build revision against the head of Ref, which matches the
// installer's source-build fallback.
func Check(ctx context.Context, localVersion, localRevision string) (Status, error) {
	repo := getenv("AFS_UPDATE_REPO", buildinfo.GitRepoURL)
	ref := getenv("AFS_UPDATE_REF", buildinfo.Ref)
	out, repoUsed, err := lsRemoteWithFallback(ctx, repo, "--tags", "refs/tags/v*")
	if err != nil {
		return Status{}, err
	}
	status := Status{
		LocalVersion:  localVersion,
		LocalRevision: localRevision,
		Repo:          repoUsed,
		Ref:           ref,
	}
	if latest := latestReleaseVersion(out); latest != "" {
		status.LatestVersion = latest
		status.UpToDate = buildinfo.CompareVersions(localVersion, latest) >= 0
		return status, nil
	}
	remote, err := RemoteRevision(ctx, repoUsed, ref)
	if err != nil {
		return Status{}, err
	}
	status.RemoteRevision = remote
	status.UpToDate = localRevision != "" && strings.HasPrefix(remote, localRevision)
	return status, nil
}

// latestReleaseVersion picks the highest v* tag out of `git ls-remote --tags`
// output and returns it without the leading v ("0.2.0"), or "" when none
// exist. Peeled entries (refs/tags/v0.1.0^{}) name the same tag and are
// folded in by the trim.
func latestReleaseVersion(lsRemoteOut string) string {
	latest := ""
	for _, line := range strings.Split(lsRemoteOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(fields[1], "refs/tags/"), "^{}")
		if len(name) < 2 || name[0] != 'v' || name[1] < '0' || name[1] > '9' {
			continue
		}
		version := name[1:]
		if latest == "" || buildinfo.CompareVersions(version, latest) > 0 {
			latest = version
		}
	}
	return latest
}

// lsRemoteWithFallback runs git ls-remote against repo, retrying once over
// the SSH URL when the HTTPS one is unreachable (private repos before the
// public release). It returns the output and the repo URL that worked.
func lsRemoteWithFallback(ctx context.Context, repo string, args ...string) (string, string, error) {
	out, err := lsRemote(ctx, repo, args...)
	if err == nil {
		return out, repo, nil
	}
	sshRepo := getenv("AFS_UPDATE_REPO_SSH", buildinfo.GitRepoSSHURL)
	if sshRepo == "" || sshRepo == repo {
		return "", "", err
	}
	out, sshErr := lsRemote(ctx, sshRepo, args...)
	if sshErr != nil {
		return "", "", err
	}
	return out, sshRepo, nil
}

func lsRemote(ctx context.Context, repo string, args ...string) (string, error) {
	// git ls-remote takes flags before the repository and ref patterns after.
	cmdArgs := []string{"ls-remote"}
	var refs []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			cmdArgs = append(cmdArgs, a)
		} else {
			refs = append(refs, a)
		}
	}
	cmdArgs = append(cmdArgs, repo)
	cmdArgs = append(cmdArgs, refs...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("update check timed out")
		}
		return "", fmt.Errorf("git ls-remote failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func RemoteRevision(ctx context.Context, repo, ref string) (string, error) {
	out, err := lsRemote(ctx, repo, ref)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", fmt.Errorf("no revision found for %s %s", repo, ref)
	}
	return fields[0], nil
}

func NotificationDue(now time.Time) bool {
	if os.Getenv("AFS_NO_UPDATE_CHECK") != "" || os.Getenv("CI") != "" {
		return false
	}
	path, err := notificationStampPath()
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err == nil && now.Sub(info.ModTime()) < 24*time.Hour {
		return false
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(now.Format(time.RFC3339)+"\n"), 0o644)
	return true
}

func notificationStampPath() (string, error) {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return dir + "/agentsfs/update/last-check", nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("home not found")
	}
	return home + "/.cache/agentsfs/update/last-check", nil
}

func getenv(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
