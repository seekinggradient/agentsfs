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

// InitMode decides how a new instance relates to an enclosing git repo.
// The choice is consequential — git history is forever — so the CLI refuses
// to create inside a repo unless shared mode is explicit.
type InitMode int

const (
	// ModeStandalone gives the instance its own git repo. The right choice
	// when not inside any repo (a personal agentsfs), and the only mode that
	// configures Git LFS, since it owns the repo.
	ModeStandalone InitMode = iota
	// ModeShared joins the enclosing repo: knowledge shares the codebase's
	// history and ships with it (team-shared memory). No git init, no LFS
	// setup — both belong to the host repo.
	ModeShared
)

// InitResult reports what Init actually did, so the CLI can narrate it.
type InitResult struct {
	Dir           string
	Mode          InitMode
	GitInited     bool // we ran `git init` for this instance
	LFSAvailable  bool // git-lfs binary present on this machine
	LFSConfigured bool // .gitattributes written and lfs hooks installed
	Committed     bool // false when git identity is missing; files left staged
}

// Init lays down the instance template in dir under the given mode and makes
// the first commit. It refuses to touch a directory that already has an
// AGENTS.md so an existing instance (or project) is never overwritten.
func Init(dir string, mode InitMode) (*InitResult, error) {
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

	// We create (and therefore own) the repo in every mode except shared,
	// where the instance joins the host repo. Only a repo we own gets LFS.
	ownsRepo := mode != ModeShared
	res := &InitResult{Dir: abs, Mode: mode, LFSAvailable: lfsAvailable()}
	res.LFSConfigured = res.LFSAvailable && ownsRepo

	tmpl, err := fs.Sub(afs.TemplateFS, "template")
	if err != nil {
		return nil, err
	}
	err = fs.WalkDir(tmpl, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == ".gitattributes" && !res.LFSConfigured {
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

	if ownsRepo && !isRepoRoot(abs) {
		if _, err := git(abs, "init"); err != nil {
			return nil, fmt.Errorf("git init failed: %w", err)
		}
		res.GitInited = true
	}
	if res.LFSConfigured {
		if _, err := git(abs, "lfs", "install", "--local"); err != nil {
			return nil, fmt.Errorf("git lfs install failed: %w", err)
		}
	}
	// The pathspec is load-bearing: since git 2.0, `add -A` without one
	// stages the entire working tree — inside a host repo that would sweep
	// the user's unrelated work into our commit.
	if _, err := git(abs, "add", "-A", "--", "."); err != nil {
		return nil, fmt.Errorf("git add failed: %w", err)
	}
	if _, err := git(abs, "commit", "-m", "Initialize agentsfs"); err == nil {
		res.Committed = true
	}
	return res, nil
}

// EnclosingRepoRoot returns the work-tree root of the git repo containing
// dir (or its nearest existing ancestor, since dir may not exist yet), and
// whether one was found.
func EnclosingRepoRoot(dir string) (string, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for abs != filepath.Dir(abs) {
		if _, err := os.Stat(abs); err == nil {
			break
		}
		abs = filepath.Dir(abs)
	}
	root, err := git(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	return root, true
}

// isRepoRoot reports whether dir is itself the root of a git work tree —
// used to avoid re-initializing a repo we're already at the top of.
func isRepoRoot(dir string) bool {
	root, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	d, _ := filepath.Abs(dir)
	rr, _ := filepath.Abs(root)
	return d == rr
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
