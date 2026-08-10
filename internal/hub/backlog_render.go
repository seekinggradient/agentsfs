package hub

import (
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"agentsfs.ai/afs/internal/core"
)

// Backlog rendering — the Hub's read-only view of the one page per instance
// that declares `agentsfs_role: backlog` in its own frontmatter.
//
// The backlog is markdown the agent writes by hand (see
// agentsfs/rfcs/backlog-and-tasks.md): GFM checkbox lists, `##` priority bands,
// Obsidian-style `[/]`/`[-]` status variants, and `^slug` block anchors. GFM
// alone renders only half of that — it knows `[ ]` and `[x]` and nothing else —
// so this file supplies the rest at render time. Nothing here writes: the CLI
// owns task truth (readiness, whether a blocker has lifted); the Hub only makes
// the page legible.
//
// Everything emitted is composed here from a fixed vocabulary plus tokens the
// line scanner validated (statuses come from a closed set, slugs must match
// backlogSlugRe). Document text keeps flowing through goldmark's escaping
// renderer, and unsafe HTML rendering stays off — a backlog page is user data
// like any other note and can never inject markup.

// isBacklogRole reports whether a page's `agentsfs_role` frontmatter value marks
// it as the backlog. It compares exactly the way core.ResolveReserved does,
// against core's own constant, so the Hub can never style a page the CLI does
// not consider the backlog (or miss one it does).
func isBacklogRole(role string) bool {
	return role == core.RoleBacklog
}

// Task statuses, used both as the internal vocabulary and — because they are a
// closed set of safe tokens — as the CSS class suffix on the rendered control.
const (
	backlogOpen       = "open"
	backlogInProgress = "inprogress"
	backlogDone       = "done"
	backlogDropped    = "dropped"
)

// backlogStatusLabels are the accessible names for the status controls. They
// are what a screen reader announces in place of the visual mark, so they read
// as states rather than as glyph descriptions.
var backlogStatusLabels = map[string]string{
	backlogOpen:       "To do",
	backlogInProgress: "In progress",
	backlogDone:       "Done",
	backlogDropped:    "Dropped",
}

// backlogBands are the priority bands the RFC reserves, matched case
// insensitively on `##` heading text. The value is the CSS token; unrecognized
// `##` headings are left entirely alone.
var backlogBands = map[string]string{
	"now":     "now",
	"next":    "next",
	"later":   "later",
	"someday": "someday",
	"done":    "done",
}

var (
	// backlogTaskRe matches a task line's marker. The checkbox alternatives
	// mirror GFM's own tasklist regexp (`^\[([\sxX])\]\s*`, applied after the
	// bullet) so `[ ]`/`[x]` keep parsing exactly as they do today, extended
	// with the two Obsidian variants the grammar adds.
	backlogTaskRe = regexp.MustCompile(`^(\s*[-*+]\s+\[)([ xX/-])(\])`)
	// backlogSlugRe matches the trailing block anchor that makes a task
	// referenceable. Kebab-case per the RFC; anything else is left as prose so
	// a stray caret never silently disappears from the page.
	backlogSlugRe = regexp.MustCompile(`\s\^([a-z0-9][a-z0-9-]*)[ \t]*$`)
	// backlogSlugTokenRe is the same rule applied to a bare slug, used to vet
	// the anchor half of a `[[name#^slug]]` reference before it becomes a URL
	// fragment.
	backlogSlugTokenRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	// backlogHeadingRe matches an ATX heading line, capturing its level and
	// text (a closing run of `#`s is trimmed separately).
	backlogHeadingRe = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*))?$`)
	// backlogFenceRe matches a fenced-code-block delimiter. The scanner has to
	// track fences because it works line by line: without this, a ``` block
	// demonstrating the task grammar would have its markers rewritten and its
	// lines counted into a band's progress chip.
	backlogFenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	// backlogBlockedRe and backlogOwnerRe are this file's second implementation
	// of core's blocker grammar (core.parseBlockerInto): the phrase marks the
	// line blocked, and a reason beginning `owner:` is the owner-blocked
	// channel — a question parked for the owner rather than work held up by
	// other work. They are deliberately the same patterns core compiles, so the
	// Hub can never badge a line the CLI reads differently.
	backlogBlockedRe = regexp.MustCompile(`(?i)blocked by`)
	backlogOwnerRe   = regexp.MustCompile(`(?i)^owner[ \t]*:[ \t]*`)
	// backlogNameRe vets a path segment before it becomes chip text or part of
	// a blob path: sub-backlog and ticket names come from document text, and
	// only ordinary file-name characters may reach either.
	backlogNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// backlogTask is what the line scan learned about one task line.
type backlogTask struct {
	status  string // one of the backlog* status constants
	slug    string // validated kebab-case anchor, "" when the line has none
	blocked bool   // the line carries a `blocked by` annotation
	// owner marks the owner-blocked channel — `blocked by owner: <question>` —
	// which is not work waiting on work but a question waiting on the reader.
	owner    bool
	question string // the text after `owner:`, "" when the author left it empty
}

// backlogDelegation is a sub-backlog a task line delegates to, with the
// progress its spine adds up to. Counts come from scanning that spine's blob
// with this same scanner, so the chip and the sub-page agree by construction.
type backlogDelegation struct {
	name        string
	done, total int
}

// backlogLinks answers the two questions a spine's wikilinks raise that the
// page itself cannot: does this target name a delegated sub-backlog (and how
// far along is it), and does it name a ticket file beside the spine. Both are
// repository lookups, supplied by the view; a nil backlogLinks renders the page
// with no chips and no state dots rather than failing.
type backlogLinks struct {
	delegate func(target string) (backlogDelegation, bool)
	ticket   func(target string) bool
}

// backlogBand is a priority band and the progress its tasks add up to. Counts
// cover every task line under the band at any depth, which is what makes the
// chip a useful glance value on a decomposed backlog.
type backlogBand struct {
	class string
	done  int
	total int
}

// backlogDoc is the result of the line scan: a normalized source for goldmark
// plus everything the AST transformer needs, keyed by line number.
type backlogDoc struct {
	source     string
	lineStarts []int
	tasks      map[int]backlogTask
	bands      map[int]*backlogBand
}

// scanBacklog reads the page once, line by line, and returns both the source
// goldmark should parse and the per-line facts the transformer decorates with.
//
// Two rewrites happen here rather than in the AST, because both are far more
// reliable against raw lines than against inline nodes goldmark has already
// split and interpreted:
//
//   - `[/]` and `[-]` become `[ ]`, so GFM's tasklist parser produces a
//     checkbox node for every task line and the transformer has one uniform
//     hook to replace. Left alone they would arrive as literal text (goldmark's
//     link parser even splits `[` into its own node), which is exactly the
//     "raw marker leaked into the prose" the RFC is fixing.
//   - a trailing ` ^slug` is dropped, hiding the anchor from the prose. It
//     survives as the list item's id.
//
// Line numbering is preserved exactly, so byte offsets in the returned source
// still map back to the facts collected here.
func scanBacklog(source string) *backlogDoc {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n"), "\n")
	doc := &backlogDoc{
		tasks: make(map[int]backlogTask),
		bands: make(map[int]*backlogBand),
	}
	var band *backlogBand
	fence := "" // the delimiter that opened the current code fence, "" when outside one
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
			// Any heading at the band level or above closes the open band; a
			// deeper heading is a subsection of it and keeps counting into it.
			if len(head[1]) <= 2 {
				band = nil
			}
			if len(head[1]) == 2 {
				text := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(head[2]), "#"))
				if class, ok := backlogBands[strings.ToLower(text)]; ok {
					band = &backlogBand{class: class}
					doc.bands[i] = band
				}
			}
			continue
		}

		marker := backlogTaskRe.FindStringSubmatchIndex(line)
		if marker == nil {
			continue
		}
		task := backlogTask{status: backlogStatusFor(line[marker[4]])}
		rest := line[marker[7]:]
		if slug := backlogSlugRe.FindStringSubmatchIndex(rest); slug != nil {
			task.slug = rest[slug[2]:slug[3]]
			rest = rest[:slug[0]]
		}
		if loc := backlogBlockedRe.FindStringIndex(rest); loc != nil {
			task.blocked = true
			reason := strings.TrimSpace(rest[loc[1]:])
			if m := backlogOwnerRe.FindStringIndex(reason); m != nil {
				task.owner = true
				task.question = strings.TrimSpace(reason[m[1]:])
			}
		}
		doc.tasks[i] = task
		if band != nil {
			band.total++
			if task.status == backlogDone {
				band.done++
			}
		}

		// `[/]` and `[-]` are not GFM; normalize them to the open marker so the
		// tasklist parser fires. `[ ]`/`[x]` pass through untouched.
		checkbox := line[marker[4]]
		if checkbox == '/' || checkbox == '-' {
			checkbox = ' '
		}
		lines[i] = line[:marker[4]] + string(checkbox) + line[marker[5]:marker[7]] + rest
	}

	doc.source = strings.Join(lines, "\n")
	doc.lineStarts = lineOffsets(lines)
	return doc
}

// lineOffsets is the byte offset each line starts at once the lines are joined
// back together, which is what maps a node's source position to the facts a
// line scan collected.
func lineOffsets(lines []string) []int {
	starts := make([]int, len(lines))
	offset := 0
	for i, line := range lines {
		starts[i] = offset
		offset += len(line) + 1 // + the newline strings.Join puts back
	}
	return starts
}

// lineAtOffset maps a byte offset back to its 0-based line.
func lineAtOffset(starts []int, offset int) int {
	return sort.SearchInts(starts, offset+1) - 1
}

func backlogStatusFor(checkbox byte) string {
	switch checkbox {
	case 'x', 'X':
		return backlogDone
	case '/':
		return backlogInProgress
	case '-':
		return backlogDropped
	default:
		return backlogOpen
	}
}

// lineAt maps a byte offset in the scanned source back to its 0-based line.
func (d *backlogDoc) lineAt(offset int) int { return lineAtOffset(d.lineStarts, offset) }

// backlogTransformer decorates the parsed document with everything the line
// scan learned. It runs after parsing and before rendering, which keeps the
// whole feature inside goldmark's escaping pipeline.
type backlogTransformer struct {
	doc *backlogDoc
	// links is nil on a page whose repository context is unavailable (a legacy
	// page, a view that could not read the tree). Everything it drives is
	// additive, so the page renders without it.
	links *backlogLinks
}

func (t *backlogTransformer) Transform(node *ast.Document, reader text.Reader, pc parser.Context) {
	// Collect first, mutate second: replacing a node mid-walk severs the
	// sibling chain goldmark's walker is iterating, silently skipping whatever
	// followed it.
	var headings []*ast.Heading
	var checkboxes []*extast.TaskCheckBox
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch typed := n.(type) {
		case *ast.Heading:
			headings = append(headings, typed)
		case *extast.TaskCheckBox:
			checkboxes = append(checkboxes, typed)
		}
		return ast.WalkContinue, nil
	})
	for _, heading := range headings {
		t.decorateBand(heading)
	}
	for _, checkbox := range checkboxes {
		t.decorateTask(checkbox)
	}
}

// decorateBand turns a recognized `## Now`-style heading into a band header with
// a progress chip. Unrecognized headings, and bands holding no tasks, are left
// exactly as GFM rendered them.
func (t *backlogTransformer) decorateBand(heading *ast.Heading) {
	if heading.Level != 2 || heading.Lines().Len() == 0 {
		return
	}
	band, ok := t.doc.bands[t.doc.lineAt(heading.Lines().At(0).Start)]
	if !ok {
		return
	}
	heading.SetAttributeString("class", []byte("band band-"+band.class))
	if band.total > 0 {
		heading.AppendChild(heading, &backlogChromeNode{markup: backlogBandChipHTML(band)})
	}
}

// decorateTask replaces a checkbox with the styled status control, wraps the
// task's prose so done/dropped styling can reach the text without also striking
// through the control or the item's nested children, and appends the blocked
// badge and anchor affordance.
//
// A checkbox the scanner has no record of — one inside a blockquote, say, which
// the line scan deliberately does not claim — keeps GFM's default rendering.
func (t *backlogTransformer) decorateTask(checkbox *extast.TaskCheckBox) {
	block := checkbox.Parent()
	if block == nil || block.Lines().Len() == 0 {
		return
	}
	item, ok := block.Parent().(*ast.ListItem)
	if !ok {
		return
	}
	task, ok := t.doc.tasks[t.doc.lineAt(block.Lines().At(0).Start)]
	if !ok {
		return
	}

	item.SetAttributeString("class", []byte("task task-"+task.status))
	if task.slug != "" {
		item.SetAttributeString("id", []byte("task-"+task.slug))
	}
	delegations := t.decorateTaskLinks(block)
	block.ReplaceChild(block, checkbox, &backlogChromeNode{markup: backlogStatusHTML(task.status)})
	block.AppendChild(block, &backlogChromeNode{markup: backlogTaskTrailerHTML(task, delegations)})
}

// decorateTaskLinks classifies the wikilinks written on one task line: a link
// to a sub-backlog's spine is a delegation (the line owns that whole subtree,
// so its progress belongs on the line), and a link to a ticket file beside the
// spine gets a state dot. The dot is a class only — the CSS colours it from the
// item's own `task-<status>` class, so a link never needs to know the status of
// the line it sits on.
func (t *backlogTransformer) decorateTaskLinks(block ast.Node) []backlogDelegation {
	if t.links == nil {
		return nil
	}
	var delegations []backlogDelegation
	_ = ast.Walk(block, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		link, ok := n.(*wikiLinkNode)
		if !entering || !ok || !link.Resolved {
			return ast.WalkContinue, nil
		}
		if t.links.delegate != nil {
			if d, isDelegation := t.links.delegate(link.Target); isDelegation {
				link.Class = "wl-delegate"
				delegations = append(delegations, d)
				return ast.WalkContinue, nil
			}
		}
		if t.links.ticket != nil && t.links.ticket(link.Target) {
			link.Class = "wl-ticket"
		}
		return ast.WalkContinue, nil
	})
	return delegations
}

// backlogStatusHTML is the status control plus the opening tag of the prose
// wrapper; backlogTaskTrailerHTML closes it. They are a pair.
func backlogStatusHTML(status string) string {
	label := backlogStatusLabels[status]
	return `<span class="task-status task-` + status + `" role="img" aria-label="` + label + `" title="` + label + `"></span><span class="task-text">`
}

func backlogTaskTrailerHTML(task backlogTask, delegations []backlogDelegation) string {
	var b strings.Builder
	b.WriteString(`</span>`)
	switch {
	case task.owner:
		// The owner-blocked channel is a question for the reader, not work held
		// up by other work, so it reads as an ask rather than as a blocker. The
		// question itself is document text and lives only in the tooltip, where
		// it is escaped like any other note content.
		title := task.question
		if title == "" {
			title = "Waiting on an answer from the owner"
		}
		title = html.EscapeString(title)
		b.WriteString(`<span class="task-blocked task-blocked-owner" title="` + title +
			`" aria-label="Waiting on you: ` + title + `">waiting on you</span>`)
	case task.blocked:
		// Deliberately unresolved: knowing whether the block has lifted needs
		// the state of every referenced task, which is the CLI's job. The badge
		// annotates what the line says, and the referenced tasks stay clickable
		// as wikilinks.
		b.WriteString(`<span class="task-blocked">blocked</span>`)
	}
	for _, d := range delegations {
		b.WriteString(backlogDelegateChipHTML(d))
	}
	if task.slug != "" {
		slug := html.EscapeString(task.slug)
		b.WriteString(`<a class="task-anchor" href="#task-` + slug + `" title="^` + slug +
			`" aria-label="Copy link to task ^` + slug + `" data-task-anchor><span aria-hidden="true">#</span></a>`)
	}
	return b.String()
}

// backlogDelegateChipHTML is the band chip's per-line twin: the delegating line
// ranks a whole subtree, so its progress belongs beside it. Name and counts are
// validated (backlogNameRe) and escaped before they reach markup.
func backlogDelegateChipHTML(d backlogDelegation) string {
	done, total := strconv.Itoa(d.done), strconv.Itoa(d.total)
	name := html.EscapeString(d.name)
	label := d.name + ": " + done + " of " + total + " done"
	class := "task-delegate"
	if d.done == d.total {
		class += " task-delegate-complete"
	}
	return `<span class="` + class + `" style="--band-fill:` + strconv.Itoa(d.done*100/d.total) +
		`%" role="img" aria-label="` + html.EscapeString(label) + `" title="` + html.EscapeString(label) +
		`"><b>` + name + `</b> ` + done + `/` + total + `</span>`
}

// backlogProgress totals a spine's task lines at every depth — what a
// delegation chip reports for the sub-backlog it names.
func backlogProgress(doc *backlogDoc) (done, total int) {
	for _, task := range doc.tasks {
		total++
		if task.status == backlogDone {
			done++
		}
	}
	return done, total
}

func backlogBandChipHTML(band *backlogBand) string {
	done, total := strconv.Itoa(band.done), strconv.Itoa(band.total)
	label := done + " of " + total + " done"
	class := "band-progress"
	if band.done == band.total {
		class += " band-progress-complete"
	}
	return `<span class="` + class + `" style="--band-fill:` + strconv.Itoa(band.done*100/band.total) +
		`%" role="img" aria-label="` + label + `" title="` + label + `"><b>` + done + `</b>/` + total + `</span>`
}

// backlogWikiResolve extends wikilink resolution with the block-anchor form the
// task grammar uses for references: `[[#^slug]]` points at a task on this very
// page, `[[backlog#^slug]]` at one on another page (name-resolved by the view's
// own resolver, with the task's anchor appended). Targets that are not block
// anchors, and anchors whose slug is malformed, fall through untouched — a
// broken reference should render as the view's usual missing-link marker, not
// as a link to nowhere.
func backlogWikiResolve(inner func(target string) (string, bool)) func(target string) (string, bool) {
	fallback := func(target string) (string, bool) {
		if inner == nil {
			return "", false
		}
		return inner(target)
	}
	return func(target string) (string, bool) {
		name, slug, ok := splitTaskAnchor(target)
		if !ok {
			return fallback(target)
		}
		if name == "" {
			return "#task-" + slug, true
		}
		resolved, found := fallback(name)
		if !found {
			return "", false
		}
		return resolved + "#task-" + slug, true
	}
}

// splitTaskAnchor splits a wikilink target of the form `name#^slug` (name may be
// empty for a same-page reference), reporting whether it is one at all.
func splitTaskAnchor(target string) (name, slug string, ok bool) {
	target = strings.TrimSpace(target)
	i := strings.Index(target, "#^")
	if i < 0 {
		return "", "", false
	}
	slug = target[i+2:]
	if !validTaskSlug(slug) {
		return "", "", false
	}
	return target[:i], slug, true
}

// validTaskSlug applies the RFC's kebab-case rule. Every slug reaching markup —
// as an id, an href, or a title — passes through here first, so the tokens this
// file interpolates can never carry markup of their own.
func validTaskSlug(slug string) bool {
	return backlogSlugTokenRe.MatchString(slug)
}

// ---- the chrome node ----

var kindBacklogChrome = ast.NewNodeKind("BacklogChrome")

// backlogChromeNode carries a fragment of markup this package composed itself:
// status controls, the prose wrapper, blocked badges, anchor affordances, band
// chips. It is never built from document text — only from fixed strings, closed
// status/band vocabularies, validated slugs, and integers — which is why it can
// be written verbatim without opening the hole that goldmark's unsafe mode
// would.
type backlogChromeNode struct {
	ast.BaseInline
	markup string
}

func (n *backlogChromeNode) Kind() ast.NodeKind         { return kindBacklogChrome }
func (n *backlogChromeNode) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

type backlogChromeRenderer struct{}

func (r *backlogChromeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindBacklogChrome, r.render)
}

func (r *backlogChromeRenderer) render(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(node.(*backlogChromeNode).markup)
	return ast.WalkSkipChildren, nil
}
