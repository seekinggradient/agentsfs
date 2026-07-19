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
	Target string // the resolvable target: inner text with #anchor and |alias stripped
	Anchor string // the #anchor portion without its "#", "" when absent
	Alias  string // the |alias portion without its "|", "" when absent
}

var (
	linkRe       = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)
	inlineCodeRe = regexp.MustCompile("`[^`]*`")
)

// parseLinkInner splits the text inside [[ ]] into its target, anchor, and
// alias. The wiki-link grammar is [[target#anchor|alias]]: the alias is split
// first because it is always last and may itself contain a '#', then the
// anchor. Every consumer — scanning, resolution, rename's rewriting, and the
// hub reading git blobs — goes through this one function so link semantics
// cannot drift between them.
func parseLinkInner(inner string) (target, anchor, alias string) {
	if i := strings.Index(inner, "|"); i >= 0 {
		inner, alias = inner[:i], strings.TrimSpace(inner[i+1:])
	}
	if i := strings.Index(inner, "#"); i >= 0 {
		inner, anchor = inner[:i], strings.TrimSpace(inner[i+1:])
	}
	return strings.TrimSpace(inner), anchor, alias
}

// formatLink rebuilds the [[...]] text for a target, preserving any anchor and
// alias. It is the inverse of parseLinkInner, used when rewriting a link.
func formatLink(target, anchor, alias string) string {
	var b strings.Builder
	b.WriteString("[[")
	b.WriteString(target)
	if anchor != "" {
		b.WriteString("#")
		b.WriteString(anchor)
	}
	if alias != "" {
		b.WriteString("|")
		b.WriteString(alias)
	}
	b.WriteString("]]")
	return b.String()
}

// fenceDelim reports whether a line is a code-fence delimiter, and which one.
// CommonMark allows both ` and ~ fences of three or more characters.
//
// Closing is deliberately lenient: any run of the opening character at least as
// long closes the block, even with trailing text. Strict CommonMark would
// require a bare closing run, but a fence this scanner fails to close swallows
// every link below it — and links it cannot see are links `afs rename` will not
// rewrite, which leaves them silently pointing at the old name. Closing one
// line early only exposes a little extra text to the link scanner, which at
// worst produces a harmless dead-link warning. Fail toward seeing too much.
func fenceDelim(line string) (char byte, length int, ok bool) {
	t := strings.TrimSpace(line)
	if len(t) < 3 || (t[0] != '`' && t[0] != '~') {
		return 0, 0, false
	}
	c := t[0]
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	return c, n, true
}

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
			// A file the walk listed but cannot be read — most often a dangling
			// symlink — contributes no links. Skip it rather than failing the
			// whole scan: one broken pointer must not take down doctor, rename,
			// and backlinks for the entire instance. Doctor reports the broken
			// entry itself as its own finding.
			continue
		}
		links = append(links, ScanLinksIn(e.Rel, string(data))...)
	}
	return links, nil
}

// ScanLinksIn finds every wikilink in one markdown file's content, skipping
// fenced code blocks and inline code spans. source is the file's rel path.
// Callers without a working tree (e.g. the hub reading git blobs) use this to
// share the CLI's exact link semantics.
func ScanLinksIn(source, content string) []Link {
	var links []Link
	var fenceChar byte
	fenceLen := 0
	for i, line := range strings.Split(content, "\n") {
		if char, length, ok := fenceDelim(line); ok {
			switch {
			case fenceLen == 0:
				fenceChar, fenceLen = char, length
			case char == fenceChar && length >= fenceLen:
				// Only the opening character closes the block: an inner "~~~"
				// inside a ``` block is content, not a terminator.
				fenceChar, fenceLen = 0, 0
			}
			continue
		}
		if fenceLen > 0 {
			continue
		}
		line = inlineCodeRe.ReplaceAllString(line, "")
		for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
			target, anchor, alias := parseLinkInner(m[1])
			if target == "" {
				// [[#Section]] navigates within the current document. It names
				// no file, so it is neither a reference to resolve nor a dead
				// link to report.
				continue
			}
			links = append(links, Link{
				Source: source,
				Line:   i + 1,
				Target: target,
				Anchor: anchor,
				Alias:  alias,
			})
		}
	}
	return links
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

// NewNameIndex builds a NameIndex over an explicit list of rel paths, for
// callers (like the hub) that already have the file set and can't walk a
// working tree. Resolution semantics are identical to BuildNameIndex.
func NewNameIndex(files []string) *NameIndex {
	return &NameIndex{files: append([]string(nil), files...)}
}

// Written is the link target as the author typed it, minus any alias: the
// target plus its anchor. A '#' is legal in a POSIX filename, so a file may
// genuinely be named "Note#1.md" and be linked as [[Note#1]].
func (l Link) Written() string {
	if l.Anchor == "" {
		return l.Target
	}
	return l.Target + "#" + l.Anchor
}

// ResolveLink resolves a scanned link, preferring the text as written so a file
// actually named "Note#1.md" still wins, and falling back to the anchor-stripped
// target so [[Note#Section]] finds Note.md. Without the first attempt, adding
// anchor support would silently redirect an existing, correct link to a
// different file — worse than the dead link it replaced.
func (idx *NameIndex) ResolveLink(l Link) []string {
	if l.Anchor != "" {
		if m := idx.Resolve(l.Written()); len(m) > 0 {
			return m
		}
	}
	return idx.Resolve(l.Target)
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
		for _, m := range idx.ResolveLink(l) {
			if wanted[m] {
				out = append(out, l)
				break
			}
		}
	}
	return out, nil
}
