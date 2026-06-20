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
	LocalRevision  string
	RemoteRevision string
	Repo           string
	Ref            string
	UpToDate       bool
}

func Check(ctx context.Context, localRevision string) (Status, error) {
	repo := getenv("AFS_UPDATE_REPO", buildinfo.GitRepoURL)
	ref := getenv("AFS_UPDATE_REF", buildinfo.Ref)
	remote, err := RemoteRevision(ctx, repo, ref)
	if err != nil {
		sshRepo := getenv("AFS_UPDATE_REPO_SSH", buildinfo.GitRepoSSHURL)
		if sshRepo == "" || sshRepo == repo {
			return Status{}, err
		}
		remote, err = RemoteRevision(ctx, sshRepo, ref)
		if err != nil {
			return Status{}, err
		}
		repo = sshRepo
	}
	return Status{
		LocalVersion:   buildinfo.Version,
		LocalRevision:  localRevision,
		RemoteRevision: remote,
		Repo:           repo,
		Ref:            ref,
		UpToDate:       localRevision != "" && strings.HasPrefix(remote, localRevision),
	}, nil
}

func RemoteRevision(ctx context.Context, repo, ref string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", repo, ref)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("update check timed out")
		}
		return "", fmt.Errorf("git ls-remote failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	fields := strings.Fields(string(out))
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
