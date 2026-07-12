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
	Committed     bool // false when git identity is missing or commit safety blocks; files left staged
	// CommitSkipped explains why Init deliberately left the template staged
	// instead of committing it (for example, unrelated host-repo paths were
	// already staged in shared mode). Empty means the commit was attempted.
	CommitSkipped string
	// Collisions names reserved-default template dirs skipped because an
	// existing entry collided with them case-insensitively (init into a
	// non-empty folder — the vault-adoption path). One message each.
	Collisions []string
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
	// Init into a non-empty directory is the vault-adoption path. A reserved
	// default dir (agent-journal/, agent-scratch/) whose name collides
	// case-insensitively with an existing entry is skipped, not merged into
	// the user's dir — same guard the contract-upgrade lay-down applies. The
	// comparison is string-level so it behaves identically on case-sensitive
	// Linux and case-insensitive macOS.
	skipDirs := map[string]string{} // template dir → colliding existing name
	for _, def := range []string{defaultJournalDir, defaultScratchDir} {
		if existing, clash := collidingEntry(abs, def); clash {
			skipDirs[def] = existing
			res.Collisions = append(res.Collisions, collisionMessageForInit(existing, def))
		}
	}
	err = fs.WalkDir(tmpl, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == ".gitattributes" && !res.LFSConfigured {
			return nil
		}
		// Skip a colliding reserved-default dir and everything under it.
		if top := topSegment(path); top != "" {
			if _, skip := skipDirs[top]; skip {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
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
	// A path-scoped `git add` does not make a later plain `git commit` scoped:
	// anything the user had already staged elsewhere in the enclosing repo would
	// be swept into our initialization commit. In shared mode, leave everything
	// staged for review instead of claiming or committing unrelated work.
	if mode == ModeShared {
		outside, err := stagedOutsideInstance(abs)
		if err != nil {
			return nil, fmt.Errorf("inspect staged paths before commit: %w", err)
		}
		if len(outside) > 0 {
			res.CommitSkipped = "unrelated host-repository files are already staged outside this agentsfs: " + strings.Join(outside, ", ")
			return res, nil
		}
	}
	if _, err := git(abs, "commit", "-m", "Initialize agentsfs"); err == nil {
		res.Committed = true
	}
	return res, nil
}

// stagedOutsideInstance returns staged repo-relative paths that do not live
// under instance. It is used only for ModeShared, where the instance joins an
// enclosing repository and a plain git commit would otherwise include every
// path already present in that repository's index.
func stagedOutsideInstance(instance string) ([]string, error) {
	prefix, err := git(instance, "rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	rel := strings.Trim(filepath.ToSlash(prefix), "/")
	if rel == "" {
		return nil, nil
	}
	out, err := git(instance, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	var outside []string
	for _, path := range strings.Split(out, "\x00") {
		path = filepath.ToSlash(path)
		if path == "" || path == rel || strings.HasPrefix(path, rel+"/") {
			continue
		}
		outside = append(outside, path)
	}
	return outside, nil
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

// topSegment returns the first path segment of a slash path ("" for "." or
// empty), used to test whether a template entry lives under a skipped dir.
func topSegment(path string) string {
	if path == "." || path == "" {
		return ""
	}
	if i := strings.IndexByte(path, '/'); i >= 0 {
		return path[:i]
	}
	return path
}

// collisionMessageForInit describes a reserved-default dir skipped during init
// because an existing entry collides with its name.
func collisionMessageForInit(existing, def string) string {
	role := RoleJournal
	if def == defaultScratchDir {
		role = RoleScratch
	}
	return fmt.Sprintf("existing directory %q collides with reserved default %q — not created; mark a directory with 'agentsfs_role: %s' or rename the collision", existing, def, role)
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
