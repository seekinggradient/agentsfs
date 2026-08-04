package core

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InstanceResolution identifies one AgentsFS instance and its relationship to
// the enclosing Git worktree. Prefix is slash-relative to RepoRoot and is "."
// for a standalone instance.
type InstanceResolution struct {
	InstanceRoot string `json:"instance_root"`
	RepoRoot     string `json:"repository_root,omitempty"`
	Prefix       string `json:"prefix"`
	Mode         string `json:"mode"`
	DetectedBy   string `json:"detected_by"`
}

type ResolveInstanceOptions struct {
	ExplicitPath     string
	AllowProjectScan bool
}

// ResolveInstance resolves an enclosing instance first, then (when enabled)
// searches only the enclosing Git worktree for a unique embedded instance.
// It stops after two matches because callers only need unique vs ambiguous.
func ResolveInstance(start string, opts ResolveInstanceOptions) (InstanceResolution, error) {
	if opts.ExplicitPath != "" {
		root, err := FindRoot(opts.ExplicitPath)
		if err != nil {
			return InstanceResolution{}, fmt.Errorf("invalid --instance %q: %w", opts.ExplicitPath, err)
		}
		return describeInstanceResolution(root, "explicit")
	}
	if root, err := FindRoot(start); err == nil {
		return describeInstanceResolution(root, "enclosing")
	}
	if !opts.AllowProjectScan {
		return InstanceResolution{}, fmt.Errorf("%s is not inside an agentsfs", start)
	}
	repoRoot, ok := EnclosingRepoRoot(start)
	if !ok {
		return InstanceResolution{}, fmt.Errorf("%s is not inside an agentsfs or Git worktree; run `afs setup`, move inside an instance, or pass --instance", start)
	}
	found, err := findProjectInstances(repoRoot, 2)
	if err != nil {
		return InstanceResolution{}, err
	}
	switch len(found) {
	case 0:
		return InstanceResolution{}, fmt.Errorf("no AgentsFS instance found in %s; run `afs setup` or pass --instance", repoRoot)
	case 1:
		return describeInstanceResolution(found[0], "project-scan")
	default:
		paths := make([]string, 0, len(found))
		for _, path := range found {
			rel, _ := filepath.Rel(repoRoot, path)
			paths = append(paths, "  ./"+filepath.ToSlash(rel))
		}
		return InstanceResolution{}, fmt.Errorf("multiple AgentsFS instances are embedded in %s:\n%s\nchoose one with --instance PATH", repoRoot, strings.Join(paths, "\n"))
	}
}

// RepositoryRemoteAppliesToInstance reports whether a repository-wide remote
// can be attributed to instance without guessing. It is always true for a
// standalone instance and true for an embedded instance only when that
// instance is the unique project-root resolution.
func RepositoryRemoteAppliesToInstance(instance, repoRoot string) bool {
	instance, instanceErr := canonicalPath(instance)
	repoRoot, repoErr := canonicalPath(repoRoot)
	if instanceErr != nil || repoErr != nil {
		return false
	}
	if instance == repoRoot {
		return true
	}
	resolved, err := ResolveInstance(repoRoot, ResolveInstanceOptions{AllowProjectScan: true})
	return err == nil && resolved.InstanceRoot == instance
}

func describeInstanceResolution(root, detectedBy string) (InstanceResolution, error) {
	root, err := canonicalPath(root)
	if err != nil {
		return InstanceResolution{}, err
	}
	res := InstanceResolution{InstanceRoot: root, Prefix: ".", Mode: "standalone", DetectedBy: detectedBy}
	if repo, ok := EnclosingRepoRoot(root); ok {
		repo, _ = canonicalPath(repo)
		res.RepoRoot = repo
		if root != repo {
			rel, relErr := filepath.Rel(repo, root)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return InstanceResolution{}, fmt.Errorf("AgentsFS root %s is not contained by Git worktree %s", root, repo)
			}
			res.Mode = "embedded"
			res.Prefix = filepath.ToSlash(rel)
		}
	}
	return res, nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(abs); resolveErr == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func findProjectInstances(repoRoot string, limit int) ([]string, error) {
	repoRoot, err := canonicalPath(repoRoot)
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	stack := []string{repoRoot}
	var found []string
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if dir != repoRoot {
			if _, statErr := os.Lstat(filepath.Join(dir, ".git")); statErr == nil {
				continue
			}
		}
		isInstance := false
		if info, statErr := os.Lstat(filepath.Join(dir, ".agentsfs")); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			isInstance = true
		} else if declaresContract(filepath.Join(dir, "AGENTS.md")) {
			isInstance = true
		}
		if isInstance {
			found = append(found, dir)
			if limit > 0 && len(found) >= limit {
				break
			}
			continue
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return nil, fmt.Errorf("inspecting %s: %w", dir, readErr)
		}
		for i := len(entries) - 1; i >= 0; i-- {
			entry := entries[i]
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if pruneStatusDirectory(path, home) {
				continue
			}
			stack = append(stack, path)
		}
	}
	sort.Strings(found)
	return found, nil
}

// agentsfsMarker is the template root's H1. AGENTS.md is a near-universal
// convention for agent instructions, so a bare AGENTS.md proves nothing —
// only one that actually declares the contract counts as an instance root.
const agentsfsMarker = "This folder is an agentsfs"

// FindRoot locates the instance root at or above start. The definitive
// marker is the .agentsfs directory (init always creates it); an AGENTS.md
// containing the contract declaration is accepted as a fallback so
// hand-made instances still work. Ordinary projects that merely have an
// AGENTS.md must never be detected — tools like search would create
// .agentsfs/ state inside them.
func FindRoot(start string) (string, error) {
	abs, err := canonicalPath(start)
	if err != nil {
		return "", err
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if info, err := os.Stat(filepath.Join(dir, ".agentsfs")); err == nil && info.IsDir() {
			return dir, nil
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		if p := filepath.Join(dir, "AGENTS.md"); fileExists(p) && declaresContract(p) {
			return dir, nil
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	return "", fmt.Errorf("%s is not inside an agentsfs (no .agentsfs/ directory, and no AGENTS.md declaring %q, in any parent)", abs, agentsfsMarker)
}

func declaresContract(agentsMD string) bool {
	data, err := os.ReadFile(agentsMD)
	return err == nil && strings.Contains(string(data), agentsfsMarker)
}

// ResolveScope turns a user-supplied path into the instance root that
// contains it plus the slash-relative subdirectory to scope a tree to. A
// path at (or equal to) the root scopes to "." — the whole instance. The
// path must be an existing directory; scoping to a file is rejected.
func ResolveScope(start string) (root, subdir string, err error) {
	abs, err := canonicalPath(start)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", fmt.Errorf("no such path: %s", start)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("tree scope must be a directory: %s", start)
	}
	root, err = FindRoot(abs)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == "" || rel == "." {
		rel = "."
	}
	return root, rel, nil
}

// Entry is one file or directory inside an instance, with paths always
// relative to the root and slash-separated.
type Entry struct {
	Rel   string
	IsDir bool
}

// ListEntries walks the instance, skipping every dot-directory (.git,
// .agentsfs, and machine/editor territory like .obsidian/) and any nested git
// repo or agentsfs instance (a separate knowledgebase — see the walk body).
// scratch/ is included — callers that exempt it (doctor) filter explicitly, so
// the leniency is visible at the rule, not hidden in the walk.
//
// TODO(v2): honor .gitignore (git ls-files --cached --others
// --exclude-standard when the instance is a repo) so build artifacts and
// node_modules inside an instance aren't treed, indexed, or doctored.
func ListEntries(root string) ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		base := filepath.Base(rel)
		// Dot-directories are machine territory (.git, .agentsfs, and editor
		// config like .obsidian/ or .trash/) — skip them entirely, the same way
		// per-file dot-prefix names are exempt from description rules. Knowledge
		// never lives in a hidden folder.
		if d.IsDir() && strings.HasPrefix(base, ".") {
			return filepath.SkipDir
		}
		// A subdirectory that is its own git repository or agentsfs instance is
		// a separate knowledgebase: don't fold its files into this one. Its
		// notes aren't part of this repo and wouldn't push — dropping the
		// nested .git (vendoring) is how you deliberately merge one in. Keyed on
		// the .git/.agentsfs markers, not a heuristic like "AGENTS.md declares a
		// contract", which any file could spoof.
		if d.IsDir() {
			if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
				return filepath.SkipDir
			}
			if info, err := os.Stat(filepath.Join(path, ".agentsfs")); err == nil && info.IsDir() {
				return filepath.SkipDir
			}
		}
		out = append(out, Entry{Rel: rel, IsDir: d.IsDir()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

// isRootContract reports whether rel is the root contract/bootstrap file.
// These are exempt from link checks: AGENTS.md contains example links like
// [[Name]] that must not be reported as dead.
func isRootContract(rel string) bool {
	return rel == "AGENTS.md" || rel == "README.md" || rel == "CLAUDE.md"
}

func isMarkdown(rel string) bool {
	return strings.EqualFold(filepath.Ext(rel), ".md")
}
