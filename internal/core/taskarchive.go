package core

import (
	"os"
	"regexp"
	"strings"
)

// The archive is where a backlog's growth goes cold. `backlog/archive/` is an
// ordinary collection: closed one-liners roll up into per-year pages
// (archive/2026.md), and a closed ticket's detail file is MOVED in whole and
// stamped `closed: YYYY-MM-DD`. The gardener owns both writes; everything here
// is the read path, and it is deliberately the smallest one that answers "what
// closed, when" — the archive is history, not a second backlog.
//
// Nothing here participates in task parsing: LoadBacklog excludes archive/
// entirely, so a closed task can never come back as pending work.

// ArchivedTask is one closed item as the archive records it: a rollup line on a
// per-year page, or an archived ticket file's frontmatter.
type ArchivedTask struct {
	Date   string     `json:"date"`             // the closed date, YYYY-MM-DD as written
	Status TaskStatus `json:"status"`           // done or dropped
	Text   string     `json:"text"`             // the task text, or the ticket's description
	Slug   string     `json:"slug,omitempty"`   // the task's ^slug when the line kept one
	Ref    string     `json:"ref,omitempty"`    // the first [[link]] the line carries — a ticket or journal entry
	Page   string     `json:"page"`             // rel path of the rollup page, or of the ticket file
	Ticket string     `json:"ticket,omitempty"` // rel path when this entry IS a ticket file
}

// archiveRollupRe is the rollup-line grammar, kept tight so ordinary prose in an
// archive page is never mistaken for history:
//
//   - 2026-08-09 — [x] Ship the thing [[ticket]] ^slug
//
// A list bullet, an ISO date, an em dash (an ASCII "--" or "-" is accepted, since
// that is what a keyboard produces), and a TERMINAL checkbox — an archive line
// records something that closed, so [ ] and [/] are not archive entries.
var archiveRollupRe = regexp.MustCompile(`^[ \t]*[-*+][ \t]+(\d{4}-\d{2}-\d{2})[ \t]+(?:—|–|--|-)[ \t]+\[([xX\-])\](?:[ \t]+(.*))?$`)

// archiveYearPageRe matches a per-year rollup page name (2026.md). Any other
// markdown file in the archive is an archived ticket, which must carry `closed:`.
var archiveYearPageRe = regexp.MustCompile(`^\d{4}\.md$`)

// LoadBacklogArchive reads the instance's backlog archive: every rollup line on
// the per-year pages, then every archived ticket file's `closed:` stamp. Order
// is page order then document order, which is the order the sweep appended
// them; callers that want another order sort by Date themselves.
//
// An instance with no backlog directory (or no archive yet) has an empty
// archive, not an error — a legacy page backlog has nowhere to put one.
func LoadBacklogArchive(root string) ([]ArchivedTask, error) {
	entries, err := ListEntries(root)
	if err != nil {
		return nil, err
	}
	roles := resolveReservedFromEntries(root, entries)
	if roles.Backlog == "" || roles.BacklogLegacy {
		return nil, nil
	}
	var out []ArchivedTask
	for _, e := range entries {
		if e.IsDir || !isMarkdown(e.Rel) || !inBacklogArchive(e.Rel, roles.Backlog) {
			continue
		}
		base := baseName(e.Rel)
		if strings.EqualFold(base, "INDEX.md") {
			continue // the collection's own descriptor, not an entry
		}
		if archiveYearPageRe.MatchString(strings.ToLower(base)) {
			data, err := os.ReadFile(joinRel(root, e.Rel))
			if err != nil {
				// A page the walk listed but cannot be read contributes no history;
				// doctor reports the unreadable file itself.
				continue
			}
			out = append(out, parseArchiveRollup(string(data), e.Rel)...)
			continue
		}
		out = append(out, ArchivedTask{
			Date:   ClosedDate(joinRel(root, e.Rel)),
			Status: TaskDone,
			Text:   Description(joinRel(root, e.Rel)),
			Page:   e.Rel,
			Ticket: e.Rel,
		})
	}
	return out, nil
}

// ClosedDate reads an archived ticket's `closed:` stamp, "" when it carries
// none. It is FrontmatterValue with one addition: an unquoted YYYY-MM-DD is a
// YAML timestamp rather than a string, which the shared scalar reader
// deliberately declines to represent, so the raw line is read for it. The stamp
// is a date by definition, and it is the whole point of the field.
func ClosedDate(path string) string {
	if v := strings.TrimSpace(FrontmatterValue(path, "closed")); v != "" {
		return v
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	block, ok := frontmatterBlock(f)
	if !ok {
		return ""
	}
	return strings.TrimSpace(lenientValue(block, "closed"))
}

// parseArchiveRollup reads one per-year page. It is pure, like the task parser,
// and skips fenced code for the same reason: a page that documents the grammar
// must not sprout phantom history from its own examples.
func parseArchiveRollup(content, relPath string) []ArchivedTask {
	var (
		out       []ArchivedTask
		fenceChar byte
		fenceLen  int
	)
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if char, length, ok := fenceDelim(line); ok {
			switch {
			case fenceLen == 0:
				fenceChar, fenceLen = char, length
			case char == fenceChar && length >= fenceLen:
				fenceChar, fenceLen = 0, 0
			}
			continue
		}
		if fenceLen > 0 {
			continue
		}
		m := archiveRollupRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text, slug := splitSlug(strings.TrimSpace(m[3]))
		entry := ArchivedTask{
			Date:   m[1],
			Status: statusForMarker(m[2]),
			Text:   text,
			Slug:   slug,
			Page:   relPath,
		}
		if link := linkRe.FindStringSubmatch(text); link != nil {
			entry.Ref, _, _ = parseLinkInner(link[1])
		}
		out = append(out, entry)
	}
	return out
}
