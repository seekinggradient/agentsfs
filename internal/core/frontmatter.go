package core

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Description extracts the one-line description: from a markdown file's
// YAML frontmatter. Empty string means the file has none (a doctor finding).
func Description(path string) string {
	return FrontmatterValue(path, "description")
}

// FrontmatterValue extracts a scalar from a file's YAML frontmatter, or "".
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
//
// The block between the leading `---` fences is parsed with a real YAML parser
// (gopkg.in/yaml.v3), so what afs reads matches what Obsidian, the Hub, and any
// other YAML consumer read. When the block is NOT valid YAML, it falls back to
// a lenient line scan for the requested key. That fallback is deliberate: about
// 1% of existing notes carry frontmatter a strict parser rejects (a `: ` inside
// an unquoted description is the usual culprit), and silently dropping their
// descriptions from `afs tree`/`status` would be a regression. Extraction
// degrades gracefully; the malformed block is surfaced separately by
// FrontmatterProblem, which doctor turns into a finding.
func FrontmatterValueFromReader(r io.Reader, key string) string {
	block, ok := frontmatterBlock(r)
	if !ok {
		return ""
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(block), &m); err == nil {
		if v, present := m[key]; present {
			return scalarString(v)
		}
		return ""
	}
	return lenientValue(block, key)
}

// frontmatterBlock returns the text between the opening `---` and the next
// `---`, and whether an opening fence was present. A file with no leading fence
// has no frontmatter. An unclosed block returns everything to EOF with ok=true
// so the value extractors can still try — FrontmatterProblem reports the
// missing fence on its own.
func frontmatterBlock(r io.Reader) (string, bool) {
	sc := bufio.NewScanner(r)
	// Start small and grow toward a 1 MiB line cap on demand: pre-allocating
	// the cap made every call cost a megabyte, an allocation storm on hub pages
	// that parse thousands of notes.
	sc.Buffer(make([]byte, 4*1024), 1024*1024)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return "", false
	}
	var b strings.Builder
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "---" {
			return b.String(), true
		}
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	return b.String(), true // unclosed; still parseable, flagged elsewhere
}

// lenientValue is the pre-YAML behavior, used only when the block fails to
// parse as YAML: return everything after `key:` on its own line, trimmed and
// unquoted. It exists so a malformed block does not cost an existing note its
// description.
func lenientValue(block, key string) string {
	for _, line := range strings.Split(block, "\n") {
		if v, ok := strings.CutPrefix(line, key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// scalarString renders a decoded YAML scalar as the string afs works in. The
// governed keys (description, agentsfs_role, agentsfs_contract) are strings by
// contract; the other cases keep a non-string value (a bool, a date, a number
// someone wrote unquoted) legible rather than blank. A collection or mapping
// value for a scalar key renders empty — it is a user error the value
// extractors are not asked to represent.
func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool, int, int64, float64:
		return fmt.Sprintf("%v", t)
	default:
		return "" // list, map, or timestamp struct: not a scalar
	}
}

// FrontmatterProblem reports why a file's frontmatter is malformed, or "" when
// it is fine or absent. This is what makes the switch to a real parser useful
// rather than merely internal: afs can now flag the exact frontmatter Obsidian
// and the Hub reject — an unclosed fence, or a block that is not valid YAML
// (most often a `: ` inside an unquoted value) — instead of silently reading
// past it. Doctor turns a non-empty result into a malformed-frontmatter
// finding.
//
// Absent frontmatter is not a problem here; a missing description is a
// different, separately reported finding.
func FrontmatterProblem(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	return frontmatterProblem(f)
}

func frontmatterProblem(r io.Reader) string {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4*1024), 1024*1024)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return "" // no frontmatter block at all
	}
	var b strings.Builder
	closed := false
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "---" {
			closed = true
			break
		}
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	if !closed {
		return "frontmatter opens with --- but is never closed"
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(b.String()), &m); err != nil {
		return "frontmatter is not valid YAML: " + firstLine(err.Error())
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
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
