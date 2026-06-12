package core

import (
	"bufio"
	"os"
	"strings"
)

// Description extracts the one-line description: from a markdown file's
// YAML frontmatter. Empty string means the file has none (a doctor
// finding). Only the description line is parsed — the contract requires
// it to be a single line, and avoiding a YAML dependency keeps the
// toolkit boring.
func Description(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return ""
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			return ""
		}
		if v, ok := strings.CutPrefix(line, "description:"); ok {
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
