package core

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
	abs, err := filepath.Abs(start)
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
	abs, err := filepath.Abs(start)
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

// ListEntries walks the instance, skipping .git and .agentsfs (machine
// territory). scratch/ is included — callers that exempt it (doctor)
// filter explicitly, so the leniency is visible at the rule, not hidden
// in the walk.
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
		if d.IsDir() && (base == ".git" || base == ".agentsfs") {
			return filepath.SkipDir
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

func inScratch(rel string) bool {
	return rel == "scratch" || strings.HasPrefix(rel, "scratch/")
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
