package core

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
)

// The backlog is the instance's prospective memory: what is pending, in what
// order, and what is blocking it. The write path is markdown editing — an agent
// or a human edits one role-marked page — and everything here is the read path,
// deriving structure from that page on demand. There is no database, no index
// table, and no identifier an author has to allocate: the page is small by
// design, so it is parsed whole each time it is asked for.
//
// The grammar is standard GFM plus two conventions already in the training
// distribution, so a model writes them correctly without being taught: Obsidian
// task-status variants ([/] and [-] alongside [ ] and [x]) and block anchors
// (^slug) for references. Contract 0.10.0.

// TaskStatus is a task's checkbox state.
type TaskStatus string

const (
	TaskOpen       TaskStatus = "open"
	TaskInProgress TaskStatus = "in_progress"
	TaskDone       TaskStatus = "done"
	TaskDropped    TaskStatus = "dropped"
)

// Priority bands, named by `##` headings and recognized case-insensitively.
// The first three are the working bands, in priority order. Someday is the
// parking lot and Done the archive: both are parsed and listed, neither is ever
// ready work. A heading the page invents is kept as the band verbatim — the
// tasks under it are still parsed and reportable, just never ready.
const (
	BandNow     = "Now"
	BandNext    = "Next"
	BandLater   = "Later"
	BandSomeday = "Someday"
	BandDone    = "Done"
)

// Task is one checkbox line, with the structure the parser derived around it.
type Task struct {
	File string // rel path of the backlog page
	Line int    // 1-based line of the task line

	// Text is the task as written with the marker, the trailing ^slug, and the
	// list syntax stripped. Inline markdown ([[links]], emphasis, arrows) is
	// deliberately kept: it is the author's prose, not decoration to normalize.
	Text   string
	Status TaskStatus
	Band   string // heading text as written; "" before any heading
	Slug   string // "" when the task declares no anchor

	Depth      int    // 0 for top-level tasks
	ParentSlug string // nearest ANCESTOR carrying a slug; "" when none does

	// A blocker holds unless it names tasks and all of them are finished. See
	// resolveBlockers: BlockedActive false with a non-empty BlockedReason is the
	// legitimate "this blocker lifted itself" state.
	BlockedActive bool
	BlockedReason string   // the annotation text after "blocked by"
	BlockedRefs   []string // slugs referenced by that annotation, in order

	OpenChildren int // descendants (direct and transitive) still open or in progress
	Ready        bool
	Children     []*Task
}

// Backlog is one parsed backlog page. Tasks holds the top-level tasks in
// document order; nested tasks hang off their parent's Children.
type Backlog struct {
	Path  string // rel path of the page this was parsed from
	Tasks []*Task
}

var (
	// A task line is a list item whose content begins with a checkbox. The
	// bullet must be followed by whitespace and the checkbox by whitespace or
	// end-of-line, so "- [x]done" and "-[x] done" are prose, exactly as every
	// markdown renderer reads them. A list item without a checkbox is not a task
	// at all: ordinary prose and plain lists are legal on the page and ignored.
	taskLineRe = regexp.MustCompile(`^([ \t]*)[-*+][ \t]+\[([ xX/\-])\](?:[ \t]+(.*))?$`)

	// A trailing block anchor names the task. Kebab-case only: a malformed
	// anchor (uppercase, leading dash) is not a slug and stays in the text,
	// where its author can see it did not take.
	taskSlugRe = regexp.MustCompile(`(?:^|[ \t])\^([a-z0-9][a-z0-9-]*)$`)

	blockedByRe = regexp.MustCompile(`(?i)blocked by`)
)

// ParseBacklogContent parses one backlog page and computes ready work. It is
// pure — content in, structure out — so the hub can parse a git blob with the
// exact semantics the CLI parses a working file, and so tests need no disk.
//
// Fenced code blocks are skipped, on the same reasoning links.go skips them:
// backticked text is quotation, not the real thing. The backlog page carries
// its own conventions (the journal-INDEX pattern), so a page that documents the
// grammar in a fence must not sprout phantom tasks from its own examples.
func ParseBacklogContent(content, relPath string) *Backlog {
	b := &Backlog{Path: relPath}

	var (
		all       []*Task // every task, document order
		stack     []*Task // open ancestors, strictly increasing indentation
		indents   []int   // stack[i]'s indentation width
		band      string
		fenceChar byte
		fenceLen  int
	)

	lines := strings.Split(content, "\n")
	inFrontmatter := len(lines) > 0 && strings.TrimSpace(lines[0]) == "---"
	for i, raw := range lines {
		// Line numbers stay absolute — they are what a reader jumps to — so
		// skipped regions are stepped over, never removed.
		line := strings.TrimSuffix(raw, "\r")
		if inFrontmatter {
			if i > 0 && strings.TrimSpace(line) == "---" {
				inFrontmatter = false
			}
			continue
		}
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

		if level, text := atxHeading(line); level > 0 {
			// Only `##` names a band; deeper or shallower headings are ordinary
			// structure. Any heading ends the surrounding list, so nesting starts
			// fresh under it — a top-level task cannot become the child of one
			// declared two sections earlier.
			if level == 2 {
				band = text
			}
			stack, indents = stack[:0], indents[:0]
			continue
		}

		m := taskLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		t := &Task{
			File:   relPath,
			Line:   i + 1,
			Status: statusForMarker(m[2]),
			Band:   band,
		}
		t.Text, t.Slug = splitSlug(strings.TrimSpace(m[3]))
		t.BlockedActive, t.BlockedReason, t.BlockedRefs = parseBlocker(t.Text)

		// Nesting is decomposition, and a task belongs to the nearest preceding
		// task indented less than it — the same reading every markdown renderer
		// gives the list. A tab counts as four spaces so mixed indentation nests
		// the way it looks.
		indent := indentWidth(m[1])
		for len(stack) > 0 && indents[len(indents)-1] >= indent {
			stack, indents = stack[:len(stack)-1], indents[:len(indents)-1]
		}
		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			t.Depth = parent.Depth + 1
			t.ParentSlug = nearestAncestorSlug(stack)
			parent.Children = append(parent.Children, t)
		} else {
			b.Tasks = append(b.Tasks, t)
		}
		stack, indents = append(stack, t), append(indents, indent)
		all = append(all, t)
	}

	resolveBlockers(all)
	for _, t := range all {
		t.OpenChildren = openDescendants(t)
	}
	markReady(b.Tasks, false, false)
	return b
}

// LoadBacklog parses the instance's backlog page. The second return is false
// when no page claims the role — an instance without a backlog is normal, not
// an error; only a page that claims the role and then cannot be read is.
func LoadBacklog(root string) (*Backlog, bool, error) {
	roles, err := ResolveReservedDirs(root)
	if err != nil {
		return nil, false, err
	}
	if roles.Backlog == "" {
		return nil, false, nil
	}
	data, err := os.ReadFile(joinRel(root, roles.Backlog))
	if err != nil {
		return nil, false, err
	}
	return ParseBacklogContent(string(data), roles.Backlog), true, nil
}

// Flat returns every task, parents before their children, in document order.
func (b *Backlog) Flat() []*Task {
	if b == nil {
		return nil
	}
	var out []*Task
	var walk func([]*Task)
	walk = func(ts []*Task) {
		for _, t := range ts {
			out = append(out, t)
			walk(t.Children)
		}
	}
	walk(b.Tasks)
	return out
}

// ReadyTasks returns the work that can be picked up right now, most urgent
// first: band (Now → Next → Later), then document order. Order within a band is
// the author's priority — reordering lines is the reprioritization gesture — so
// the sort is stable and never rearranges within a band.
func (b *Backlog) ReadyTasks() []*Task {
	var out []*Task
	for _, t := range b.Flat() {
		if t.Ready {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return bandRank(out[i].Band) < bandRank(out[j].Band) })
	return out
}

// InProgress returns the [/] tasks in document order. They are surfaced first
// and separately from ready work: they are resumed, not re-claimed.
func (b *Backlog) InProgress() []*Task {
	var out []*Task
	for _, t := range b.Flat() {
		if t.Status == TaskInProgress {
			out = append(out, t)
		}
	}
	return out
}

// MarshalJSON emits the per-task record the RFC specifies: flat scalars with
// the blocker as a nested object, and no children — Flat() already yields every
// task, so nesting them here would publish each task twice. Owning the shape in
// core keeps every consumer (CLI, hub, MCP) on one contract.
func (t Task) MarshalJSON() ([]byte, error) {
	refs := t.BlockedRefs
	if refs == nil {
		refs = []string{}
	}
	return json.Marshal(taskJSON{
		File:       t.File,
		Line:       t.Line,
		Text:       t.Text,
		Status:     t.Status,
		Band:       t.Band,
		Slug:       t.Slug,
		Depth:      t.Depth,
		ParentSlug: t.ParentSlug,
		Blocked: taskBlockedJSON{
			Active: t.BlockedActive,
			Reason: t.BlockedReason,
			Refs:   refs,
		},
		OpenChildren: t.OpenChildren,
		Ready:        t.Ready,
	})
}

type taskJSON struct {
	File         string          `json:"file"`
	Line         int             `json:"line"`
	Text         string          `json:"text"`
	Status       TaskStatus      `json:"status"`
	Band         string          `json:"band"`
	Slug         string          `json:"slug"`
	Depth        int             `json:"depth"`
	ParentSlug   string          `json:"parent_slug"`
	Blocked      taskBlockedJSON `json:"blocked"`
	OpenChildren int             `json:"open_children"`
	Ready        bool            `json:"ready"`
}

type taskBlockedJSON struct {
	Active bool     `json:"active"`
	Reason string   `json:"reason"`
	Refs   []string `json:"refs"`
}

func statusForMarker(marker string) TaskStatus {
	switch marker {
	case "/":
		return TaskInProgress
	case "x", "X":
		return TaskDone
	case "-":
		return TaskDropped
	default:
		return TaskOpen
	}
}

// splitSlug peels a trailing block anchor off the task text.
func splitSlug(text string) (string, string) {
	m := taskSlugRe.FindStringSubmatchIndex(text)
	if m == nil {
		return text, ""
	}
	return strings.TrimSpace(text[:m[0]]), text[m[2]:m[3]]
}

// parseBlocker reads the `blocked by` annotation. The phrase alone marks the
// task blocked; only references can lift it later (resolveBlockers), so a
// plain-text reason — or none at all — holds until a human edits it away.
// References are read with the instance's own wikilink grammar, so
// [[#^slug]], [[backlog#^slug]], and [[#^slug|alias]] all name the same task.
func parseBlocker(text string) (active bool, reason string, refs []string) {
	loc := blockedByRe.FindStringIndex(text)
	if loc == nil {
		return false, "", nil
	}
	reason = strings.TrimSpace(text[loc[1]:])
	for _, m := range linkRe.FindAllStringSubmatch(reason, -1) {
		_, anchor, _ := parseLinkInner(m[1])
		if slug, ok := strings.CutPrefix(anchor, "^"); ok && slug != "" {
			refs = append(refs, slug)
		}
	}
	return true, reason, refs
}

// resolveBlockers lifts every self-resolving blocker. A referenced slug that
// this page does not define keeps the block on — the reference cannot be
// satisfied, so acting as though it were would let a task go ready on the
// strength of a typo. Doctor reports it as dangling-task-ref.
func resolveBlockers(all []*Task) {
	bySlug := map[string]*Task{}
	for _, t := range all {
		// First occurrence wins, matching how every other duplicate resolves here;
		// doctor flags the duplicate slug itself.
		if t.Slug != "" {
			if _, seen := bySlug[t.Slug]; !seen {
				bySlug[t.Slug] = t
			}
		}
	}
	for _, t := range all {
		if !t.BlockedActive || len(t.BlockedRefs) == 0 {
			continue
		}
		lifted := true
		for _, ref := range t.BlockedRefs {
			target, ok := bySlug[ref]
			if !ok || (target.Status != TaskDone && target.Status != TaskDropped) {
				lifted = false
				break
			}
		}
		t.BlockedActive = !lifted
	}
}

// openDescendants counts the work still outstanding beneath a task — direct and
// transitive, since a finished subtask with unfinished children of its own is
// not finished work. The parser never flips a parent's own checkbox from this:
// the file is the source of truth and doctor flags the inconsistency instead.
func openDescendants(t *Task) int {
	n := 0
	for _, c := range t.Children {
		if c.Status == TaskOpen || c.Status == TaskInProgress {
			n++
		}
		n += openDescendants(c)
	}
	return n
}

// markReady applies the ready rule down the tree, carrying the two ancestor
// conditions that disqualify a descendant: an ancestor whose blocker is active
// (the work genuinely cannot start) and a dropped ancestor (the whole branch was
// abandoned). A done ancestor is deliberately NOT disqualifying — that is the
// inconsistency doctor reports, and its open children are still real work.
func markReady(tasks []*Task, blockedAncestor, droppedAncestor bool) {
	for _, t := range tasks {
		t.Ready = t.Status == TaskOpen &&
			!blockedAncestor && !droppedAncestor &&
			!t.BlockedActive &&
			t.OpenChildren == 0 &&
			bandRank(t.Band) >= 0
		markReady(t.Children, blockedAncestor || t.BlockedActive, droppedAncestor || t.Status == TaskDropped)
	}
}

// bandRank is a band's priority, or -1 when the band yields no ready work:
// Someday (the parking lot), Done (the archive), a heading the page invented,
// or no heading at all.
func bandRank(band string) int {
	switch strings.ToLower(strings.TrimSpace(band)) {
	case strings.ToLower(BandNow):
		return 0
	case strings.ToLower(BandNext):
		return 1
	case strings.ToLower(BandLater):
		return 2
	default:
		return -1
	}
}

// nearestAncestorSlug is the slug of the closest ancestor that has one. A task
// whose immediate parent is anonymous is still addressable as work under the
// nearest named ancestor, which is what a reference wants to say.
func nearestAncestorSlug(stack []*Task) string {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].Slug != "" {
			return stack[i].Slug
		}
	}
	return ""
}

// indentWidth measures indentation in spaces, counting a tab as four — the
// width common renderers give it, so what the parser calls a child is what the
// reader sees indented under its parent.
func indentWidth(s string) int {
	n := 0
	for _, r := range s {
		if r == '\t' {
			n += 4
		} else {
			n++
		}
	}
	return n
}

// atxHeading returns a line's ATX heading level and its text, or level 0 when
// the line is not a heading. "#tag" is not a heading: the hashes must be
// followed by whitespace or nothing.
func atxHeading(line string) (int, string) {
	t := strings.TrimLeft(line, " ")
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return 0, ""
	}
	rest := t[n:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return 0, ""
	}
	return n, strings.TrimSpace(rest)
}
