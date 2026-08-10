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
// or a human edits the spine — and everything here is the read path, deriving
// structure from those pages on demand. There is no database, no index table,
// and no identifier an author has to allocate: each page is small by design, so
// it is parsed whole each time it is asked for.
//
// The grammar is standard GFM plus two conventions already in the training
// distribution, so a model writes them correctly without being taught: Obsidian
// task-status variants ([/] and [-] alongside [ ] and [x]) and block anchors
// (^slug) for references.
//
// Contract 0.11.0 made the backlog a DIRECTORY: the spine is its INDEX.md, and a
// sub-directory's INDEX.md is a sub-backlog spine whose tasks are reachable only
// by DELEGATION — a spine line linking that sub-spine. The delegating line is
// the parent and the sub-spine's tasks are its children, so the root spine stays
// the sole priority authority. Ticket detail files (ordinary notes beside the
// spine) and archive/ are not task pages and are never parsed for tasks. A
// 0.10.0 page-level backlog still parses as the single page it always was.

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
	File string // rel path of the task page this line lives on
	// Page is File under the name the multi-file model uses; they are always the
	// same value. The JSON field stays `file`, which every consumer already reads.
	Page string
	Line int // 1-based line of the task line

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

	// OwnerBlocked marks the owner-blocked channel: `— blocked by owner: <the
	// question>`. It is an ordinary prose blocker to the parser — it never lifts
	// itself — and a question for the reader when the reader is the owner, so the
	// views surface it separately from work that is merely held up.
	OwnerBlocked  bool
	OwnerQuestion string // the text after `owner:`

	OpenChildren int // descendants (direct and transitive) still open or in progress
	Ready        bool
	Children     []*Task

	// Delegates holds the top-level tasks of the sub-spine this line delegates
	// to, when it links one. They are this task's children across a file
	// boundary: they inherit its readiness gate and count in its OpenChildren.
	Delegates []*Task
	// Undelegated marks a task on a sub-spine that no delegation reaches — a
	// parked workstream. It is parsed and listed (--all shows it), never ready.
	Undelegated bool

	// blockedRefTargets parallels BlockedRefs: the link target each ref was
	// written with, "" for a same-page [[#^slug]]. Slugs are a per-file
	// namespace, so resolving a reference needs both halves.
	blockedRefTargets []string
	// rank orders ready work across the delegation tree: (band, document
	// position) pairs, outermost delegating line first. See ReadyTasks.
	rank []int
}

// Backlog is one instance's parsed backlog: the spine plus every sub-spine a
// delegation reaches. Tasks holds the SPINE's top-level tasks in document
// order; nested tasks hang off Children and delegated sub-spine tasks off
// Delegates, so the whole tree is one walk from here.
type Backlog struct {
	// Dir is the backlog directory, "" for a legacy 0.10.0 page backlog.
	Dir string
	// Spine is the rel path of the task page at the center: <Dir>/INDEX.md, or
	// the legacy page.
	Spine string
	// Path is Spine under its 0.10.0 name.
	//
	// Deprecated: read Spine (the task page) or Dir (the directory).
	Path string
	// Pages lists every parsed task page, spine first, then sub-spines
	// depth-first in sorted order. Ticket files and archive/ are not here.
	Pages []string
	Tasks []*Task
	// Undelegated holds the top-level tasks of sub-spines nothing delegates to,
	// in page order. They are never ready; Flat lists them after the spine's.
	Undelegated []*Task
	// UndelegatedPages names those sub-spines, including any with no tasks at
	// all, so doctor can report a parked workstream that Undelegated cannot show.
	UndelegatedPages []string
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

	// The owner-blocked channel: `— blocked by owner: <question>`. Case-
	// insensitive on the word, tolerant of the spacing an author actually types.
	ownerBlockedRe = regexp.MustCompile(`(?i)^owner[ \t]*:[ \t]*`)
)

// backlogArchiveDir is the collection inside a backlog directory that holds what
// closed. It is a name, not a marker: the archive is created lazily by the
// gardener's sweep, and task parsing must exclude it before any INDEX.md in it
// can be read as a spine.
const backlogArchiveDir = "archive"

// ParseBacklogContent parses one backlog page and computes ready work. It is
// pure — content in, structure out — so the hub can parse a git blob with the
// exact semantics the CLI parses a working file, and so tests need no disk.
//
// Fenced code blocks are skipped, on the same reasoning links.go skips them:
// backticked text is quotation, not the real thing. The backlog page carries
// its own conventions (the journal-INDEX pattern), so a page that documents the
// grammar in a fence must not sprout phantom tasks from its own examples.
func ParseBacklogContent(content, relPath string) *Backlog {
	b := &Backlog{Path: relPath, Spine: relPath, Pages: []string{relPath}}

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
			Page:   relPath,
			Line:   i + 1,
			Status: statusForMarker(m[2]),
			Band:   band,
		}
		t.Text, t.Slug = splitSlug(strings.TrimSpace(m[3]))
		parseBlockerInto(t)

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

	// One page is still a page set of one: a reference may name it explicitly
	// ([[backlog#^slug]]) rather than leaving the target off, and both forms have
	// always meant the same task.
	resolveBlockers(map[string][]*Task{relPath: all}, NewNameIndex([]string{relPath}))
	for _, t := range all {
		t.OpenChildren = openDescendants(t)
	}
	markReady(b.Tasks, false, false)
	rankTasks(b.Tasks, nil, 0)
	return b
}

// BacklogPageContent is one task page's content, keyed by its rel path. It is
// what ParseBacklogPages consumes, so a caller without a working tree (the hub
// reading git blobs, a test) stitches a multi-file backlog with the exact
// semantics the CLI gives one on disk.
type BacklogPageContent struct {
	Path    string
	Content string
}

// ParseBacklogPages parses a directory-role backlog: pages[0] is the spine and
// the rest are sub-spines, in the order LoadBacklog discovers them (depth-first,
// sorted). dir is the backlog directory. It is pure, like ParseBacklogContent,
// and it is that function plus the three things only the whole set can decide:
// which sub-spines a delegation reaches, whether a cross-page blocker resolves,
// and where each task ranks in the one priority order the root spine authors.
func ParseBacklogPages(dir string, pages []BacklogPageContent) *Backlog {
	if len(pages) == 0 {
		return &Backlog{Dir: dir}
	}
	parsed := make([]*Backlog, len(pages))
	byPage := map[string][]*Task{}
	b := &Backlog{Dir: dir, Spine: pages[0].Path, Path: pages[0].Path}
	for i, p := range pages {
		parsed[i] = ParseBacklogContent(p.Content, p.Path)
		byPage[p.Path] = parsed[i].Flat()
		b.Pages = append(b.Pages, p.Path)
	}
	b.Tasks = parsed[0].Tasks

	// Delegation. Every task is a candidate, spine first, so the root spine
	// claims a sub-spine two lines want; a page is claimed once, which also makes
	// a delegation cycle impossible to walk twice.
	spineIdx := NewNameIndex(b.Pages)
	claimed := map[string]bool{pages[0].Path: true}
	for _, p := range pages {
		for _, t := range byPage[p.Path] {
			for _, target := range delegationTargets(t, p.Path, spineIdx) {
				if claimed[target] {
					continue
				}
				claimed[target] = true
				for i := range parsed {
					if pages[i].Path == target {
						t.Delegates = append(t.Delegates, parsed[i].Tasks...)
					}
				}
			}
		}
	}
	for i, p := range pages {
		if claimed[p.Path] {
			continue
		}
		b.Undelegated = append(b.Undelegated, parsed[i].Tasks...)
		b.UndelegatedPages = append(b.UndelegatedPages, p.Path)
	}

	// Blockers, open counts, readiness, and ranking are recomputed over the
	// stitched tree: each page's own pass saw only its own slugs and none of its
	// delegated children.
	resolveBlockers(byPage, spineIdx)
	for _, ts := range byPage {
		for _, t := range ts {
			t.OpenChildren = openDescendants(t)
		}
	}
	markReady(b.Tasks, false, false)
	rankTasks(b.Tasks, nil, 0)
	// A parked workstream is parsed and listed but never ready: the root spine is
	// the only thing that can release work.
	for _, t := range flatten(b.Undelegated) {
		t.Ready, t.Undelegated = false, true
	}
	return b
}

// delegationTargets returns the sub-spines a task line delegates to: the pages
// its wikilinks resolve to, by the same name resolution every other link uses,
// so [[backlog/voice/INDEX]] and [[voice/INDEX]] name the same spine. A link to
// the task's own page (or to a ticket file, which is not in the index at all) is
// not a delegation.
func delegationTargets(t *Task, page string, spines *NameIndex) []string {
	if !strings.Contains(t.Text, "[[") {
		return nil
	}
	var out []string
	for _, l := range ScanLinksIn(page, t.Text) {
		for _, m := range spines.ResolveLink(l) {
			if m != page {
				out = append(out, m)
			}
		}
	}
	return out
}

// LoadBacklog parses the instance's backlog: the spine and every sub-spine
// beneath it (a legacy page-role backlog is the one page it has always been).
// The second return is false when nothing claims the role — an instance without
// a backlog is normal, not an error; only a page that claims the role and then
// cannot be read is.
func LoadBacklog(root string) (*Backlog, bool, error) {
	entries, err := ListEntries(root)
	if err != nil {
		return nil, false, err
	}
	return loadBacklogFromEntries(root, entries, resolveReservedFromEntries(root, entries))
}

// loadBacklogFromEntries is the entry-list form, so callers that already walked
// the tree and resolved roles (prime, doctor) don't do either twice.
func loadBacklogFromEntries(root string, entries []Entry, roles RoleDirs) (*Backlog, bool, error) {
	if roles.BacklogSpine == "" {
		return nil, false, nil
	}
	pages := []BacklogPageContent{{Path: roles.BacklogSpine}}
	if !roles.BacklogLegacy {
		for _, rel := range subSpines(entries, roles.Backlog) {
			pages = append(pages, BacklogPageContent{Path: rel})
		}
	}
	for i := range pages {
		data, err := os.ReadFile(joinRel(root, pages[i].Path))
		if err != nil {
			return nil, false, err
		}
		pages[i].Content = string(data)
	}
	if roles.BacklogLegacy {
		b := ParseBacklogContent(pages[0].Content, pages[0].Path)
		return b, true, nil
	}
	return ParseBacklogPages(roles.Backlog, pages), true, nil
}

// subSpines lists the sub-backlog spines under a backlog directory: every
// <dir>/<sub…>/INDEX.md, depth-first in sorted order (ListEntries sorts by rel
// path, which is that order). A sub-backlog carries no role marker — it is
// defined by being a directory under the backlog with task grammar in its
// INDEX.md — so nothing here reads frontmatter.
//
// archive/ is excluded wholesale, at any depth: it holds what closed, and a
// closed task must never come back as pending work. Ticket detail files are
// excluded by construction — only INDEX.md is a task page.
func subSpines(entries []Entry, dir string) []string {
	var out []string
	for _, e := range entries {
		if e.IsDir || !strings.HasPrefix(e.Rel, dir+"/") || !strings.EqualFold(baseName(e.Rel), "INDEX.md") {
			continue
		}
		if parentOf(e.Rel) == dir {
			continue // the spine itself, which the caller already has
		}
		if inBacklogArchive(e.Rel, dir) {
			continue
		}
		out = append(out, e.Rel)
	}
	return out
}

// inBacklogArchive reports whether a path under the backlog directory lives in
// an archive collection — <dir>/archive/ or a sub-backlog's own.
func inBacklogArchive(rel, dir string) bool {
	if dir == "" || !strings.HasPrefix(rel, dir+"/") {
		return false
	}
	for _, seg := range strings.Split(strings.TrimPrefix(rel, dir+"/"), "/") {
		if strings.EqualFold(seg, backlogArchiveDir) {
			return true
		}
	}
	return false
}

// Flat returns every task, parents before their children, in document order:
// the spine, with each delegated sub-spine inline at the line that delegates to
// it, then the sub-spines nothing delegates to.
func (b *Backlog) Flat() []*Task {
	if b == nil {
		return nil
	}
	return append(flatten(b.Tasks), flatten(b.Undelegated)...)
}

func flatten(tasks []*Task) []*Task {
	var out []*Task
	var walk func([]*Task)
	walk = func(ts []*Task) {
		for _, t := range ts {
			out = append(out, t)
			walk(t.Children)
			walk(t.Delegates)
		}
	}
	walk(tasks)
	return out
}

// ReadyTasks returns the work that can be picked up right now, most urgent
// first: band (Now → Next → Later), then document order. Order within a band is
// the author's priority — reordering lines is the reprioritization gesture — so
// the sort never rearranges within a band. Delegated work ranks where its
// delegating line ranks: the root spine is the priority authority, and a
// sub-spine's own bands only order the subtree that line released.
func (b *Backlog) ReadyTasks() []*Task {
	var out []*Task
	for _, t := range b.Flat() {
		if t.Ready {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return lessRank(out[i].rank, out[j].rank) })
	return out
}

func lessRank(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// rankTasks assigns every task its ready-ordering key: (band, document
// position) for its own page, prefixed by the key of the line that delegated to
// its page. Ordering compares keys lexicographically, so a delegating line
// carries its whole subtree to its own position in the root's priority order.
func rankTasks(tasks []*Task, prefix []int, seq int) int {
	for _, t := range tasks {
		t.rank = append(append(append([]int{}, prefix...), bandSortRank(t.Band)), seq)
		seq++
		seq = rankTasks(t.Children, prefix, seq)
		rankTasks(t.Delegates, t.rank, 0)
	}
	return seq
}

// bandSortRank is bandRank for ordering rather than readiness: a band that
// yields no ready work sorts last instead of first, which only matters for the
// subtree under a delegating line parked in Someday.
func bandSortRank(band string) int {
	if r := bandRank(band); r >= 0 {
		return r
	}
	return 1 << 20
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
		OpenChildren:  t.OpenChildren,
		Ready:         t.Ready,
		Undelegated:   t.Undelegated,
		OwnerBlocked:  t.OwnerBlocked,
		OwnerQuestion: t.OwnerQuestion,
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
	// The 0.11.0 additions are omitempty: they are absent from the great
	// majority of tasks, and a consumer written against 0.10.0 reads the record
	// unchanged.
	Undelegated   bool   `json:"undelegated,omitempty"`
	OwnerBlocked  bool   `json:"owner_blocked,omitempty"`
	OwnerQuestion string `json:"owner_question,omitempty"`
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

// parseBlockerInto reads the `blocked by` annotation onto a task. The phrase
// alone marks the task blocked; only references can lift it later
// (resolveBlockers), so a plain-text reason — or none at all — holds until a
// human edits it away. References are read with the instance's own wikilink
// grammar, so [[#^slug]], [[voice/INDEX#^slug]], and [[#^slug|alias]] all name a
// task; the target half is kept because slugs are a per-page namespace.
//
// A reason beginning `owner:` is the owner-blocked channel: a question only the
// owner can answer, parked so the agent can pull the next item instead of
// stalling. It is an ordinary blocker otherwise — and deliberately one that
// never lifts itself, since only the owner's answer resolves it.
func parseBlockerInto(t *Task) {
	loc := blockedByRe.FindStringIndex(t.Text)
	if loc == nil {
		return
	}
	t.BlockedActive = true
	t.BlockedReason = strings.TrimSpace(t.Text[loc[1]:])
	if m := ownerBlockedRe.FindStringSubmatchIndex(t.BlockedReason); m != nil {
		t.OwnerBlocked = true
		t.OwnerQuestion = strings.TrimSpace(t.BlockedReason[m[1]:])
	}
	for _, m := range linkRe.FindAllStringSubmatch(t.BlockedReason, -1) {
		target, anchor, _ := parseLinkInner(m[1])
		if slug, ok := strings.CutPrefix(anchor, "^"); ok && slug != "" {
			t.BlockedRefs = append(t.BlockedRefs, slug)
			t.blockedRefTargets = append(t.blockedRefTargets, target)
		}
	}
}

// resolveBlockers lifts every self-resolving blocker across the backlog's pages.
// A slug is a PER-PAGE identifier, matching how markdown block anchors work
// everywhere else: [[#^slug]] resolves on the page the annotation is written on,
// and a cross-page reference names its page — [[voice/INDEX#^slug]] — resolving
// by the ordinary link-name rules. A reference that resolves to nothing keeps
// the block on: acting as though it were satisfied would let a task go ready on
// the strength of a typo. Doctor reports it as dangling-task-ref.
//
// An owner-blocked task never lifts, whatever it references: the annotation is a
// question, and only the owner's answer removes it.
func resolveBlockers(byPage map[string][]*Task, pageIdx *NameIndex) {
	bySlug := map[string]map[string]*Task{}
	for page, tasks := range byPage {
		slugs := map[string]*Task{}
		for _, t := range tasks {
			// First occurrence wins, matching how every other duplicate resolves
			// here; doctor flags the duplicate slug itself.
			if t.Slug != "" {
				if _, seen := slugs[t.Slug]; !seen {
					slugs[t.Slug] = t
				}
			}
		}
		bySlug[page] = slugs
	}
	for _, tasks := range byPage {
		for _, t := range tasks {
			if !t.BlockedActive || t.OwnerBlocked || len(t.BlockedRefs) == 0 {
				continue
			}
			lifted := true
			for i, ref := range t.BlockedRefs {
				target := resolveBlockerPage(t, t.blockedRefTargets[i], byPage, pageIdx)
				finished, ok := bySlug[target][ref]
				if !ok || (finished.Status != TaskDone && finished.Status != TaskDropped) {
					lifted = false
					break
				}
			}
			t.BlockedActive = !lifted
		}
	}
}

// resolveBlockerPage is the page a blocker reference points at: the task's own
// page for a bare [[#^slug]], otherwise the backlog page the target names. A
// target naming something outside the backlog (or nothing at all) resolves to
// "", which no page defines — the reference dangles and the block holds.
func resolveBlockerPage(t *Task, target string, byPage map[string][]*Task, pageIdx *NameIndex) string {
	if target == "" {
		return t.Page
	}
	if _, ok := byPage[target]; ok {
		return target
	}
	if pageIdx != nil {
		if m := pageIdx.Resolve(target); len(m) == 1 {
			return m[0]
		}
	}
	return ""
}

// openDescendants counts the work still outstanding beneath a task — direct and
// transitive, since a finished subtask with unfinished children of its own is
// not finished work. Delegated sub-spine tasks count too: a line that delegates
// a workstream is not done while that workstream has open tasks. The parser
// never flips a parent's own checkbox from this: the file is the source of truth
// and doctor flags the inconsistency instead.
func openDescendants(t *Task) int {
	n := 0
	for _, c := range append(append([]*Task{}, t.Children...), t.Delegates...) {
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
//
// Delegation is the same rule across a file boundary, with one addition: the
// sub-spine's tasks are ready only while the delegating line is NON-TERMINAL.
// The root spine is the sole priority authority, so a workstream nobody is
// currently pointing at releases nothing — a checked-off delegation is doctor's
// delegation-terminal finding, not a source of ready work.
func markReady(tasks []*Task, blockedAncestor, droppedAncestor bool) {
	for _, t := range tasks {
		t.Ready = t.Status == TaskOpen &&
			!blockedAncestor && !droppedAncestor &&
			!t.BlockedActive &&
			t.OpenChildren == 0 &&
			bandRank(t.Band) >= 0
		markReady(t.Children, blockedAncestor || t.BlockedActive, droppedAncestor || t.Status == TaskDropped)
		terminal := t.Status == TaskDone || t.Status == TaskDropped
		markReady(t.Delegates, blockedAncestor || t.BlockedActive, droppedAncestor || terminal)
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
