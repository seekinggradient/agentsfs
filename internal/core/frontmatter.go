package core

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// Description extracts the one-line description: from a markdown file's
// YAML frontmatter. Empty string means the file has none (a doctor finding).
func Description(path string) string {
	return FrontmatterValue(path, "description")
}

// FrontmatterValue extracts a one-line scalar from YAML frontmatter. It is
// intentionally tiny: the contract keeps required metadata single-line, and
// avoiding a YAML dependency keeps the toolkit boring.
func FrontmatterValue(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	return FrontmatterValueFromReader(f, key)
}

// FrontmatterValueFromReader is FrontmatterValue over an already-open source
// rather than a path, so callers without a working tree — e.g. the hub
// rendering a git blob streamed straight from a bare repo — parse the exact
// same frontmatter the CLI does, with no second implementation to drift.
func FrontmatterValueFromReader(r io.Reader, key string) string {
	sc := bufio.NewScanner(r)
	// Start small and let the scanner grow toward the 1 MiB line cap on demand.
	// Pre-allocating the cap here made every call cost a megabyte, which turned
	// hub pages that parse thousands of notes into allocation storms.
	sc.Buffer(make([]byte, 4*1024), 1024*1024)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return ""
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			return ""
		}
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// FrontmatterUnclosed reports whether a file opens a YAML frontmatter block
// with `---` and never closes it.
//
// This scanner is lenient enough to keep reading past the missing fence, so it
// usually still finds the description — which is exactly why the mistake goes
// unnoticed. Every stricter consumer disagrees: a real YAML parser, Obsidian,
// and the Hub all see a file with no frontmatter at all. And because the scan
// runs off the end of the intended block, a `key:` sitting in ordinary prose
// can be picked up as a field. Doctor reports it on its own so the fix is the
// right one — close the fence, rather than write another description.
//
// It deliberately checks nothing else about the YAML. The contract asks for a
// one-line description: but explicitly allows richer frontmatter (a sources:
// list, verified: dates), so a stricter parser here would flag valid files.
func FrontmatterUnclosed(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4*1024), 1024*1024)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return false // no frontmatter block at all — a different finding
	}
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "---" {
			return false
		}
	}
	return true
}

// DirDescription is the directory's own description. Every directory's comes
// from its INDEX.md, the root included: the root's per-instance description
// lives in its own INDEX.md, kept out of the contract-managed AGENTS.md so
// upgrades never rewrite it. Older instances predate the root INDEX.md, so the
// root falls back to AGENTS.md's description when no root INDEX.md exists.
func DirDescription(root, rel string) string {
	if rel == "." || rel == "" {
		if d := Description(joinRel(root, "INDEX.md")); d != "" {
			return d
		}
		return Description(joinRel(root, "AGENTS.md"))
	}
	return Description(joinRel(root, rel+"/INDEX.md"))
}

func joinRel(root, rel string) string {
	return root + string(os.PathSeparator) + strings.ReplaceAll(rel, "/", string(os.PathSeparator))
}
