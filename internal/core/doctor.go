package core

import (
	"fmt"
	"os"
	"strings"
)

// Finding is one health problem doctor identified. Severity is "error"
// (contract violation), "warn" (probably wrong), or "info" (worth a look).
// Doctor is deterministic — no LLM — and its output is the worklist the
// gardener consumes.
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

// Doctor checks instance health. scratch/ is exempt from everything (mess
// is legal there); the root contract files are exempt from link checks
// (their example links are teaching material).
func Doctor(root string) ([]Finding, error) {
	entries, err := ListEntries(root)
	if err != nil {
		return nil, err
	}
	links, err := ScanLinks(root)
	if err != nil {
		return nil, err
	}
	idx, err := BuildNameIndex(root)
	if err != nil {
		return nil, err
	}

	var findings []Finding
	add := func(sev, code, path, msg string) {
		findings = append(findings, Finding{sev, code, path, msg})
	}

	// Per-directory INDEX presence and per-file descriptions.
	indexBodies := map[string]string{} // dir → lowercased INDEX.md content
	for _, e := range entries {
		if !e.IsDir || inScratch(e.Rel) {
			continue
		}
		idxPath := joinRel(root, e.Rel+"/INDEX.md")
		if data, err := os.ReadFile(idxPath); err == nil {
			indexBodies[e.Rel] = strings.ToLower(string(data))
		} else {
			add("error", "missing-index", e.Rel, "directory has no INDEX.md describing it")
		}
	}
	if data, err := os.ReadFile(joinRel(root, "AGENTS.md")); err == nil {
		indexBodies["."] = strings.ToLower(string(data)) // root describes itself
	}

	for _, e := range entries {
		if e.IsDir || inScratch(e.Rel) {
			continue
		}
		base := baseName(e.Rel)
		if strings.HasPrefix(base, ".") {
			continue // machine files (.gitattributes etc.) describe nothing
		}
		if isMarkdown(e.Rel) {
			if Description(joinRel(root, e.Rel)) == "" {
				add("error", "missing-description", e.Rel, "markdown file has no description: frontmatter")
			}
		} else {
			// Non-markdown files must be described in their directory's INDEX.md.
			if body := indexBodies[parentOf(e.Rel)]; !strings.Contains(body, strings.ToLower(base)) {
				add("warn", "undescribed-file", e.Rel, "not mentioned in its directory's INDEX.md (binaries can't describe themselves)")
			}
		}
	}

	// Link health.
	linkedFiles := map[string]bool{}
	for _, l := range links {
		if isRootContract(l.Source) || inScratch(l.Source) {
			continue
		}
		matches := idx.Resolve(l.Target)
		for _, m := range matches {
			linkedFiles[m] = true
		}
		switch {
		case len(matches) == 0:
			add("error", "dead-link", l.Source, fmt.Sprintf("line %d: [[%s]] resolves to no file", l.Line, l.Target))
		case len(matches) > 1:
			add("warn", "ambiguous-link", l.Source, fmt.Sprintf("line %d: [[%s]] matches %s — disambiguate with a path suffix", l.Line, l.Target, strings.Join(matches, ", ")))
		}
	}

	// Orphans and stubs: fragmentation's early warning signs.
	for _, e := range entries {
		if e.IsDir || inScratch(e.Rel) || !isMarkdown(e.Rel) {
			continue
		}
		base := baseName(e.Rel)
		if isRootContract(e.Rel) || strings.EqualFold(base, "INDEX.md") {
			continue
		}
		body, err := os.ReadFile(joinRel(root, e.Rel))
		if err == nil && len(strings.TrimSpace(stripFrontmatter(string(body)))) < 120 {
			add("warn", "stub", e.Rel, "nearly empty — expand it or consolidate it into a denser note")
		}
		if !linkedFiles[e.Rel] && !mentionedInOwnIndex(indexBodies, e.Rel) {
			add("info", "orphan", e.Rel, "no wikilinks point here and its directory's INDEX.md doesn't mention it")
		}
	}
	return findings, nil
}

func mentionedInOwnIndex(indexBodies map[string]string, rel string) bool {
	body := indexBodies[parentOf(rel)]
	base := strings.ToLower(baseName(rel))
	return strings.Contains(body, base) ||
		strings.Contains(body, strings.TrimSuffix(base, ".md"))
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---") {
		return s
	}
	rest := s[3:]
	if i := strings.Index(rest, "\n---"); i >= 0 {
		after := rest[i+4:]
		if j := strings.Index(after, "\n"); j >= 0 {
			return after[j+1:]
		}
		return ""
	}
	return s
}
