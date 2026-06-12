package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Link is one [[wikilink]] occurrence in a markdown file.
type Link struct {
	Source string // rel path of the file containing the link
	Line   int    // 1-based
	Target string // raw text inside [[ ]], alias after | stripped
}

var (
	linkRe       = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)
	inlineCodeRe = regexp.MustCompile("`[^`]*`")
)

// ScanLinks finds every wikilink in every markdown file under root.
// Links inside fenced code blocks and inline code spans are skipped:
// backticked text is quotation (examples, instructions), not a reference.
func ScanLinks(root string) ([]Link, error) {
	entries, err := ListEntries(root)
	if err != nil {
		return nil, err
	}
	var links []Link
	for _, e := range entries {
		if e.IsDir || !isMarkdown(e.Rel) {
			continue
		}
		data, err := os.ReadFile(joinRel(root, e.Rel))
		if err != nil {
			return nil, err
		}
		inFence := false
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			line = inlineCodeRe.ReplaceAllString(line, "")
			for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
				target := m[1]
				if j := strings.Index(target, "|"); j >= 0 {
					target = target[:j]
				}
				links = append(links, Link{Source: e.Rel, Line: i + 1, Target: strings.TrimSpace(target)})
			}
		}
	}
	return links, nil
}

// NameIndex resolves link targets to files. Names are the identifiers:
// a link matches a file when it equals the file's name (markdown loses
// its .md extension; other types keep theirs) or a trailing path suffix
// of it, so [[work/Apple]] disambiguates between multiple Apples.
// Matching is case-insensitive, mirroring how agents actually write.
type NameIndex struct {
	files []string // all rel paths (non-dirs)
}

func BuildNameIndex(root string) (*NameIndex, error) {
	entries, err := ListEntries(root)
	if err != nil {
		return nil, err
	}
	idx := &NameIndex{}
	for _, e := range entries {
		if !e.IsDir {
			idx.files = append(idx.files, e.Rel)
		}
	}
	return idx, nil
}

// Resolve returns every file the raw link target could refer to.
func (idx *NameIndex) Resolve(target string) []string {
	t := strings.ToLower(strings.TrimSpace(target))
	if t == "" {
		return nil
	}
	var out []string
	for _, f := range idx.files {
		if linkable := linkName(f); matchSuffix(linkable, t) {
			out = append(out, f)
		}
	}
	return out
}

// linkName is the path a file is addressable by: rel path with .md
// stripped for markdown ("reference/Granite Mutual.md" → "reference/granite
// mutual"), lowercased.
func linkName(rel string) string {
	if isMarkdown(rel) {
		rel = rel[:len(rel)-len(filepath.Ext(rel))]
	}
	return strings.ToLower(rel)
}

// matchSuffix reports whether target equals linkable or a trailing
// path-segment suffix of it.
func matchSuffix(linkable, target string) bool {
	if linkable == target {
		return true
	}
	return strings.HasSuffix(linkable, "/"+target)
}

// Backlinks returns all links across the instance that resolve to the file
// or name given. The root contract files are skipped — their example links
// are teaching material, not references.
func Backlinks(root, name string) ([]Link, error) {
	links, err := ScanLinks(root)
	if err != nil {
		return nil, err
	}
	idx, err := BuildNameIndex(root)
	if err != nil {
		return nil, err
	}

	// The query may be a rel path or a bare name; normalize to the set of
	// files it denotes, then collect links resolving into that set.
	wanted := map[string]bool{}
	if matches := idx.Resolve(strings.TrimSuffix(name, ".md")); len(matches) > 0 {
		for _, m := range matches {
			wanted[m] = true
		}
	} else {
		for _, m := range idx.Resolve(name) {
			wanted[m] = true
		}
	}

	var out []Link
	for _, l := range links {
		if isRootContract(l.Source) {
			continue
		}
		for _, m := range idx.Resolve(l.Target) {
			if wanted[m] {
				out = append(out, l)
				break
			}
		}
	}
	return out, nil
}
