package core

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	afs "agentsfs.ai/afs"
)

// InitResult reports what Init actually did, so the CLI can narrate it.
type InitResult struct {
	Dir          string
	GitInited    bool // false if dir was already inside a git repo
	LFSAvailable bool // .gitattributes written and lfs hooks installed
	Committed    bool // false when git identity is missing; files left staged
}

// Init lays down the instance template in dir, initializes git, and makes
// the first commit. It refuses to touch a directory that already has an
// AGENTS.md so an existing instance (or project) is never overwritten.
func Init(dir string) (*InitResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(abs, "AGENTS.md")); err == nil {
		return nil, fmt.Errorf("%s already contains an AGENTS.md — refusing to overwrite", abs)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}

	res := &InitResult{Dir: abs, LFSAvailable: lfsAvailable()}

	tmpl, err := fs.Sub(afs.TemplateFS, "template")
	if err != nil {
		return nil, err
	}
	err = fs.WalkDir(tmpl, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// LFS attributes without git-lfs installed make `git add` of media
		// fail outright, so the file is only laid down when lfs exists.
		if path == ".gitattributes" && !res.LFSAvailable {
			return nil
		}
		dest := filepath.Join(abs, filepath.FromSlash(path))
		if d.IsDir() {
			if path == "." {
				return nil
			}
			return os.MkdirAll(dest, 0o755)
		}
		data, err := fs.ReadFile(tmpl, path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		return nil, err
	}

	if !insideGitRepo(abs) {
		if _, err := git(abs, "init"); err != nil {
			return nil, fmt.Errorf("git init failed: %w", err)
		}
		res.GitInited = true
	}
	if res.LFSAvailable {
		if _, err := git(abs, "lfs", "install", "--local"); err != nil {
			return nil, fmt.Errorf("git lfs install failed: %w", err)
		}
	}
	if _, err := git(abs, "add", "-A"); err != nil {
		return nil, fmt.Errorf("git add failed: %w", err)
	}
	if _, err := git(abs, "commit", "-m", "Initialize agentsfs"); err == nil {
		res.Committed = true
	}
	return res, nil
}

func insideGitRepo(dir string) bool {
	_, err := git(dir, "rev-parse", "--git-dir")
	return err == nil
}

func lfsAvailable() bool {
	return exec.Command("git", "lfs", "version").Run() == nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
