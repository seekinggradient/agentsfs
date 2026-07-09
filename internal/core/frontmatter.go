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

// DirDescription is the directory's own description: the root's comes from
// AGENTS.md, every other directory's from its INDEX.md.
func DirDescription(root, rel string) string {
	if rel == "." || rel == "" {
		return Description(joinRel(root, "AGENTS.md"))
	}
	return Description(joinRel(root, rel+"/INDEX.md"))
}

func joinRel(root, rel string) string {
	return root + string(os.PathSeparator) + strings.ReplaceAll(rel, "/", string(os.PathSeparator))
}
