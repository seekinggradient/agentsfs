package hub

import (
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// Ticket pages — the detail files that sit beside a backlog spine.
//
// A ticket is an ordinary note and renders as one. The single addition is the
// contract's body-vs-log split (agentsfs/rfcs/backlog-directories.md): the top
// of the file is synthesized state, updated in place, and a `## Log` section at
// the bottom is an append-only dated record — `- 2026-08-09 — tried X, blocked
// on Y`. Reading a ticket means reading the body and then scanning the log for
// when things happened, so the log gets timeline chrome: the date leaves the
// prose and becomes a marker on a connector line.
//
// Like the spine rendering, this works from a line scan rather than from inline
// nodes, and everything emitted is composed from fixed strings plus a date the
// scanner matched against `\d{4}-\d{2}-\d{2}`. Document text keeps flowing
// through goldmark's escaping renderer.

var (
	// ticketLogEntryRe matches a log line: a list item whose text opens with an
	// ISO date. The date must be followed by whitespace or end-of-line, so a
	// version string or a range keeps its text intact.
	ticketLogEntryRe = regexp.MustCompile(`^(\s*[-*+]\s+)(\d{4}-\d{2}-\d{2})([ \t]|$)`)
	// ticketListItemRe matches any list item, so an undated line inside the log
	// still joins the timeline (without a date marker).
	ticketListItemRe = regexp.MustCompile(`^\s*[-*+]\s+`)
	// ticketLogHeading is the heading text that opens the log section.
	ticketLogHeading = "Log"
)

// ticketDoc is the ticket line scan: the source goldmark should parse plus the
// per-line facts the transformer decorates with, keyed by line number.
type ticketDoc struct {
	source     string
	lineStarts []int
	headings   map[int]bool   // lines carrying the `## Log` heading
	items      map[int]bool   // list-item lines inside the log section
	dates      map[int]string // those lines' dates, lifted out of the prose
}

// scanTicket finds the log section and lifts each entry's leading date out of
// the line, the way scanBacklog lifts a trailing `^slug`: the date survives as
// chrome the transformer re-inserts, so the prose reads as the note and the
// date reads as the timeline marker. Line numbering is preserved exactly.
func scanTicket(source string) *ticketDoc {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n"), "\n")
	doc := &ticketDoc{
		headings: make(map[int]bool),
		items:    make(map[int]bool),
		dates:    make(map[int]string),
	}
	fence := "" // the delimiter that opened the current code fence, "" when outside one
	inLog := false
	for i, line := range lines {
		if delim := backlogFenceRe.FindStringSubmatch(line); delim != nil {
			switch {
			case fence == "":
				fence = delim[1]
			case delim[1][0] == fence[0] && len(delim[1]) >= len(fence):
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}

		if head := backlogHeadingRe.FindStringSubmatch(line); head != nil {
			// Any heading at the log's level or above closes it; a deeper one is
			// a subsection of the log and keeps the timeline running.
			if len(head[1]) <= 2 {
				inLog = false
			}
			if len(head[1]) == 2 {
				text := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(head[2]), "#"))
				if strings.EqualFold(text, ticketLogHeading) {
					inLog = true
					doc.headings[i] = true
				}
			}
			continue
		}
		if !inLog {
			continue
		}
		m := ticketLogEntryRe.FindStringSubmatchIndex(line)
		if m == nil {
			if ticketListItemRe.MatchString(line) {
				doc.items[i] = true
			}
			continue
		}
		doc.items[i] = true
		// A date with nothing after it is the whole entry: leave it in the prose
		// rather than emptying the list item of everything it says.
		rest := strings.TrimLeft(line[m[5]:], " \t")
		if rest == "" {
			continue
		}
		doc.dates[i] = line[m[4]:m[5]]
		lines[i] = line[:m[4]] + rest
	}

	doc.source = strings.Join(lines, "\n")
	doc.lineStarts = lineOffsets(lines)
	return doc
}

// lineAt maps a byte offset in the scanned source back to its 0-based line.
func (d *ticketDoc) lineAt(offset int) int { return lineAtOffset(d.lineStarts, offset) }

// ticketTransformer hangs the timeline classes on the parsed log section and
// puts each entry's date back as a marker.
type ticketTransformer struct{ doc *ticketDoc }

func (t *ticketTransformer) Transform(node *ast.Document, reader text.Reader, pc parser.Context) {
	// Collect first, mutate second — the same rule backlogTransformer follows,
	// for the same reason.
	var headings []*ast.Heading
	var items []*ast.ListItem
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch typed := n.(type) {
		case *ast.Heading:
			headings = append(headings, typed)
		case *ast.ListItem:
			items = append(items, typed)
		}
		return ast.WalkContinue, nil
	})
	for _, heading := range headings {
		if heading.Level == 2 && heading.Lines().Len() > 0 && t.doc.headings[t.doc.lineAt(heading.Lines().At(0).Start)] {
			heading.SetAttributeString("class", []byte("ticket-log"))
		}
	}
	for _, item := range items {
		t.decorateEntry(item)
	}
}

// decorateEntry marks one log line and, when it opened with a date, re-inserts
// that date ahead of the prose as the timeline's marker.
func (t *ticketTransformer) decorateEntry(item *ast.ListItem) {
	block := firstLinedBlock(item)
	if block == nil {
		return
	}
	line := t.doc.lineAt(block.Lines().At(0).Start)
	if !t.doc.items[line] {
		return
	}
	item.SetAttributeString("class", []byte("log-entry"))
	if list, ok := item.Parent().(*ast.List); ok {
		list.SetAttributeString("class", []byte("ticket-log-list"))
	}
	date, ok := t.doc.dates[line]
	if !ok {
		return
	}
	// The date matched an ISO pattern, so it holds nothing to escape; it is
	// escaped anyway, on the same principle as every other token this package
	// interpolates.
	marker := &backlogChromeNode{markup: `<span class="log-date">` + html.EscapeString(date) + `</span>`}
	if first := block.FirstChild(); first != nil {
		block.InsertBefore(block, first, marker)
	} else {
		block.AppendChild(block, marker)
	}
}

// firstLinedBlock returns the node whose source lines locate a list item: the
// item's own when it has them, otherwise its first block child's (a paragraph
// or text block). Returns nil when neither does — an empty item.
func firstLinedBlock(item *ast.ListItem) ast.Node {
	if item.Lines().Len() > 0 {
		return item
	}
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		if child.Type() == ast.TypeBlock && child.Lines().Len() > 0 {
			return child
		}
	}
	return nil
}
