package core

import (
	"encoding/json"
	"strings"
	"testing"
)

// only returns the single task the fixture is about, failing the test when the
// parse produced anything else — a wrong count would otherwise surface as a
// confusing index panic.
func only(t *testing.T, content string) *Task {
	t.Helper()
	tasks := ParseBacklogContent(content, "backlog.md").Flat()
	if len(tasks) != 1 {
		t.Fatalf("want exactly 1 task from:\n%s\ngot %d", content, len(tasks))
	}
	return tasks[0]
}

func texts(tasks []*Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.Text
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The checkbox markers are the whole write path: [ ] [/] [x] [-], any bullet,
// x either case. A list item without a checkbox is prose, not a task — plain
// lists are legal on the page and must be ignored rather than half-parsed.
func TestParseTaskMarkers(t *testing.T) {
	cases := []struct {
		name   string
		line   string
		isTask bool
		want   TaskStatus
	}{
		{"open", "- [ ] a", true, TaskOpen},
		{"in progress", "- [/] a", true, TaskInProgress},
		{"done", "- [x] a", true, TaskDone},
		{"done uppercase", "- [X] a", true, TaskDone},
		{"dropped", "- [-] a", true, TaskDropped},
		{"star bullet", "* [ ] a", true, TaskOpen},
		{"plus bullet", "+ [x] a", true, TaskDone},
		{"empty task text", "- [ ]", true, TaskOpen},
		{"tab after bullet", "-\t[/] a", true, TaskInProgress},
		{"plain list item", "- just a note", false, ""},
		{"unknown marker", "- [?] a", false, ""},
		{"no space after bullet", "-[x] a", false, ""},
		{"no space after checkbox", "- [x]a", false, ""},
		{"prose mentioning a box", "The [x] marker means done.", false, ""},
		{"heading", "## [x] not a task", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tasks := ParseBacklogContent("## Now\n"+c.line+"\n", "backlog.md").Flat()
			if !c.isTask {
				if len(tasks) != 0 {
					t.Fatalf("%q should not parse as a task, got %+v", c.line, tasks[0])
				}
				return
			}
			if len(tasks) != 1 {
				t.Fatalf("%q should parse as one task, got %d", c.line, len(tasks))
			}
			if tasks[0].Status != c.want {
				t.Errorf("status = %q, want %q", tasks[0].Status, c.want)
			}
			if tasks[0].Line != 2 {
				t.Errorf("Line = %d, want 2 (1-based, counting the heading)", tasks[0].Line)
			}
			if tasks[0].File != "backlog.md" {
				t.Errorf("File = %q, want backlog.md", tasks[0].File)
			}
		})
	}
}

// Bands come from `##` headings only, matched case-insensitively for the ready
// rule but kept verbatim as written. Other heading levels are ordinary
// structure, and a heading the page invented still yields listed (never ready)
// tasks.
func TestParseBands(t *testing.T) {
	content := `- [ ] before any heading
## Now
- [ ] now work
## NEXT
- [ ] next work
### A subsection
- [ ] still next
## Icebox
- [ ] invented band
## someday
- [ ] parked
## Done
- [x] archived
`
	tasks := ParseBacklogContent(content, "backlog.md").Flat()
	want := []struct {
		text  string
		band  string
		ready bool
	}{
		{"before any heading", "", false},
		{"now work", "Now", true},
		{"next work", "NEXT", true},
		{"still next", "NEXT", true},
		{"invented band", "Icebox", false},
		{"parked", "someday", false},
		{"archived", "Done", false},
	}
	if len(tasks) != len(want) {
		t.Fatalf("parsed %d tasks, want %d", len(tasks), len(want))
	}
	for i, w := range want {
		got := tasks[i]
		if got.Text != w.text || got.Band != w.band || got.Ready != w.ready {
			t.Errorf("task %d = {text:%q band:%q ready:%v}, want {%q %q %v}",
				i, got.Text, got.Band, got.Ready, w.text, w.band, w.ready)
		}
	}
}

// A `###` heading is not a band, but it does end the list above it: a task
// under it must not become a child of the last task before it.
func TestParseHeadingResetsNesting(t *testing.T) {
	content := `## Now
- [ ] parent
### Aside
- [ ] after the aside
`
	b := ParseBacklogContent(content, "backlog.md")
	if len(b.Tasks) != 2 {
		t.Fatalf("want 2 top-level tasks, got %d", len(b.Tasks))
	}
	if d := b.Tasks[1].Depth; d != 0 {
		t.Errorf("task after a heading has depth %d, want 0", d)
	}
}

// Nesting is decomposition: a task belongs to the nearest preceding task
// indented less than it, spaces and tabs alike. ParentSlug names the nearest
// ancestor that HAS a slug, which is what a reference wants to say.
func TestParseNesting(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"spaces", "## Now\n- [ ] top ^top\n  - [ ] mid\n    - [ ] deep\n- [ ] sibling\n"},
		{"tabs", "## Now\n- [ ] top ^top\n\t- [ ] mid\n\t\t- [ ] deep\n- [ ] sibling\n"},
		{"mixed", "## Now\n- [ ] top ^top\n    - [ ] mid\n\t\t- [ ] deep\n- [ ] sibling\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := ParseBacklogContent(c.content, "backlog.md")
			if len(b.Tasks) != 2 {
				t.Fatalf("want 2 top-level tasks, got %d", len(b.Tasks))
			}
			flat := b.Flat()
			want := []struct {
				text       string
				depth      int
				parentSlug string
			}{
				{"top", 0, ""},
				{"mid", 1, "top"},
				{"deep", 2, "top"}, // nearest ancestor WITH a slug, not the immediate parent
				{"sibling", 0, ""},
			}
			if len(flat) != len(want) {
				t.Fatalf("flat = %v, want %d tasks", texts(flat), len(want))
			}
			for i, w := range want {
				if flat[i].Text != w.text || flat[i].Depth != w.depth || flat[i].ParentSlug != w.parentSlug {
					t.Errorf("task %d = {%q depth:%d parent:%q}, want {%q %d %q}",
						i, flat[i].Text, flat[i].Depth, flat[i].ParentSlug, w.text, w.depth, w.parentSlug)
				}
			}
			// Flat() is document order, parents before children.
			if !equalStrings(texts(flat), []string{"top", "mid", "deep", "sibling"}) {
				t.Errorf("Flat() order = %v", texts(flat))
			}
		})
	}
}

// OpenChildren counts outstanding work anywhere beneath a task, so a done
// subtask hiding an open one of its own still holds the parent open.
func TestParseOpenChildren(t *testing.T) {
	b := ParseBacklogContent(`## Now
- [ ] parent
  - [x] done child
    - [ ] forgotten grandchild
  - [-] dropped child
  - [/] working child
`, "backlog.md")
	parent := b.Tasks[0]
	if parent.OpenChildren != 2 {
		t.Errorf("OpenChildren = %d, want 2 (the grandchild and the in-progress child)", parent.OpenChildren)
	}
	if parent.Ready {
		t.Error("a task with open descendants must not be ready")
	}
	if got := parent.Children[0].OpenChildren; got != 1 {
		t.Errorf("done child's OpenChildren = %d, want 1", got)
	}
}

// The trailing block anchor is what makes a task referenceable. It is stripped
// from the text; a malformed one is left in place, where its author can see it
// did not take.
func TestParseSlugs(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		wantText string
		wantSlug string
	}{
		{"kebab case", "- [ ] Ship hub sync polish ^hub-sync-polish", "Ship hub sync polish", "hub-sync-polish"},
		{"digits", "- [ ] Task ^v2-rollout-3", "Task", "v2-rollout-3"},
		{"slug only", "- [ ] ^bare", "", "bare"},
		{"uppercase is malformed", "- [ ] Task ^Hub-Sync", "Task ^Hub-Sync", ""},
		{"leading dash is malformed", "- [ ] Task ^-nope", "Task ^-nope", ""},
		{"underscore is malformed", "- [ ] Task ^no_underscores", "Task ^no_underscores", ""},
		{"not trailing", "- [ ] Task ^slug and more words", "Task ^slug and more words", ""},
		{"block reference is not a slug", "- [ ] Task — blocked by [[#^other]]", "Task — blocked by [[#^other]]", ""},
		{"inline markdown kept", "- [/] Hub sync → [[hub-sync-status]] ^hub-sync", "Hub sync → [[hub-sync-status]]", "hub-sync"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := only(t, "## Now\n"+c.line+"\n")
			if got.Text != c.wantText || got.Slug != c.wantSlug {
				t.Errorf("= {text:%q slug:%q}, want {%q %q}", got.Text, got.Slug, c.wantText, c.wantSlug)
			}
		})
	}
}

// A blocker with references lifts itself when they are all finished; anything
// else it says — plain text, or a reference to a slug this page never defines —
// holds until a human edits it away.
func TestParseBlockers(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		wantActive bool
		wantReason string
		wantRefs   []string
	}{
		{
			name:       "no blocker",
			content:    "## Now\n- [ ] plain task\n",
			wantActive: false, wantReason: "", wantRefs: nil,
		},
		{
			name:       "plain text reason holds",
			content:    "## Now\n- [ ] Call back — blocked by adjuster response\n",
			wantActive: true, wantReason: "adjuster response", wantRefs: nil,
		},
		{
			name:       "case insensitive phrase",
			content:    "## Now\n- [ ] Task — BLOCKED BY something\n",
			wantActive: true, wantReason: "something", wantRefs: nil,
		},
		{
			name:       "reference to an open task holds",
			content:    "## Now\n- [ ] Task — blocked by [[#^other]]\n- [ ] Other ^other\n",
			wantActive: true, wantReason: "[[#^other]]", wantRefs: []string{"other"},
		},
		{
			name:       "reference to a done task lifts",
			content:    "## Now\n- [ ] Task — blocked by [[#^other]]\n- [x] Other ^other\n",
			wantActive: false, wantReason: "[[#^other]]", wantRefs: []string{"other"},
		},
		{
			name:       "reference to a dropped task lifts",
			content:    "## Now\n- [ ] Task — blocked by [[#^other]]\n- [-] Other ^other\n",
			wantActive: false, wantReason: "[[#^other]]", wantRefs: []string{"other"},
		},
		{
			name:       "in-progress reference holds",
			content:    "## Now\n- [ ] Task — blocked by [[#^other]]\n- [/] Other ^other\n",
			wantActive: true, wantReason: "[[#^other]]", wantRefs: []string{"other"},
		},
		{
			name:       "named-page reference form",
			content:    "## Now\n- [ ] Task — blocked by [[backlog#^other]]\n- [x] Other ^other\n",
			wantActive: false, wantReason: "[[backlog#^other]]", wantRefs: []string{"other"},
		},
		{
			name:       "aliased reference form",
			content:    "## Now\n- [ ] Task — blocked by [[#^other|the other one]]\n- [x] Other ^other\n",
			wantActive: false, wantReason: "[[#^other|the other one]]", wantRefs: []string{"other"},
		},
		{
			name:       "all references must be finished",
			content:    "## Now\n- [ ] Task — blocked by [[#^a]] and [[#^b]]\n- [x] A ^a\n- [ ] B ^b\n",
			wantActive: true, wantReason: "[[#^a]] and [[#^b]]", wantRefs: []string{"a", "b"},
		},
		{
			name:       "unresolvable reference holds",
			content:    "## Now\n- [ ] Task — blocked by [[#^ghost]]\n",
			wantActive: true, wantReason: "[[#^ghost]]", wantRefs: []string{"ghost"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseBacklogContent(c.content, "backlog.md").Flat()[0]
			if got.BlockedActive != c.wantActive {
				t.Errorf("BlockedActive = %v, want %v", got.BlockedActive, c.wantActive)
			}
			if got.BlockedReason != c.wantReason {
				t.Errorf("BlockedReason = %q, want %q", got.BlockedReason, c.wantReason)
			}
			if !equalStrings(got.BlockedRefs, c.wantRefs) {
				t.Errorf("BlockedRefs = %v, want %v", got.BlockedRefs, c.wantRefs)
			}
			if c.wantActive && got.Ready {
				t.Error("a blocked task must not be ready")
			}
		})
	}
}

// The ready rule is the query the whole design exists to answer, so every leg
// of it gets a fixture: status, band, children, blockers, and the two ancestor
// conditions.
func TestReadySemantics(t *testing.T) {
	cases := []struct {
		name    string
		content string
		ready   []string
	}{
		{
			name:    "open leaves in working bands",
			content: "## Now\n- [ ] a\n## Next\n- [ ] b\n## Later\n- [ ] c\n",
			ready:   []string{"a", "b", "c"},
		},
		{
			name:    "non-open statuses are never ready",
			content: "## Now\n- [/] a\n- [x] b\n- [-] c\n- [ ] d\n",
			ready:   []string{"d"},
		},
		{
			name:    "parked and archived bands are never ready",
			content: "## Someday\n- [ ] a\n## Done\n- [ ] b\n## Now\n- [ ] c\n",
			ready:   []string{"c"},
		},
		{
			name:    "a parent with open children is not ready, its leaves are",
			content: "## Now\n- [ ] parent\n  - [ ] child\n",
			ready:   []string{"child"},
		},
		{
			name:    "a parent whose children are all finished is ready",
			content: "## Now\n- [ ] parent\n  - [x] child\n",
			ready:   []string{"parent"},
		},
		{
			name:    "an active blocker on an ancestor disqualifies the branch",
			content: "## Now\n- [ ] parent — blocked by paperwork\n  - [ ] child\n    - [ ] grandchild\n",
			ready:   nil,
		},
		{
			name:    "a lifted ancestor blocker does not disqualify",
			content: "## Now\n- [ ] parent — blocked by [[#^done-thing]]\n  - [ ] child\n- [x] Done thing ^done-thing\n",
			ready:   []string{"child"},
		},
		{
			name:    "a dropped ancestor disqualifies the branch",
			content: "## Now\n- [-] abandoned\n  - [ ] child\n",
			ready:   nil,
		},
		{
			name:    "a done ancestor does not disqualify its open children",
			content: "## Now\n- [x] checked off early\n  - [ ] leftover\n",
			ready:   []string{"leftover"},
		},
		{
			name:    "tasks before any heading are not ready",
			content: "- [ ] homeless\n## Now\n- [ ] placed\n",
			ready:   []string{"placed"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := texts(ParseBacklogContent(c.content, "backlog.md").ReadyTasks())
			if !equalStrings(got, c.ready) {
				t.Errorf("ready = %v, want %v", got, c.ready)
			}
		})
	}
}

// Ready ordering is band first, then document order — and document order within
// a band is the author's priority, so the sort must never rearrange it.
func TestReadyTasksOrdering(t *testing.T) {
	content := `## Later
- [ ] later first
- [ ] later second
## Now
- [ ] now first
- [ ] now second
## Next
- [ ] next only
`
	got := texts(ParseBacklogContent(content, "backlog.md").ReadyTasks())
	want := []string{"now first", "now second", "next only", "later first", "later second"}
	if !equalStrings(got, want) {
		t.Errorf("ReadyTasks = %v, want %v", got, want)
	}
}

// In-progress work is surfaced first and separately: it is resumed, not
// re-claimed, so it is never mixed into the ready queue.
func TestInProgress(t *testing.T) {
	b := ParseBacklogContent("## Now\n- [/] a\n- [ ] b\n## Next\n- [/] c\n", "backlog.md")
	if got := texts(b.InProgress()); !equalStrings(got, []string{"a", "c"}) {
		t.Errorf("InProgress = %v, want [a c]", got)
	}
	for _, t2 := range b.ReadyTasks() {
		if t2.Status == TaskInProgress {
			t.Errorf("in-progress task %q leaked into ReadyTasks", t2.Text)
		}
	}
}

// Frontmatter is not content, and a fenced block is quotation — the backlog
// page carries its own conventions, so a page documenting the grammar must not
// sprout phantom tasks from its own examples.
func TestParseSkipsFrontmatterAndFences(t *testing.T) {
	content := "---\ndescription: Backlog.\nagentsfs_role: backlog\n---\n" +
		"## Now\n" +
		"Write tasks like this:\n" +
		"```markdown\n" +
		"## Now\n" +
		"- [ ] an example, not a task\n" +
		"```\n" +
		"- [ ] a real task\n"
	got := texts(ParseBacklogContent(content, "backlog.md").Flat())
	if !equalStrings(got, []string{"a real task"}) {
		t.Errorf("parsed %v, want only the unfenced task", got)
	}
}

// The worked example the RFC specifies (and the template ships), asserted whole:
// the fixture is the contract between the grammar as documented and the grammar
// as implemented.
const rfcWorkedExample = `---
description: Project backlog — prioritized ideas and work items
agentsfs_role: backlog
---

## Now
- [/] Embedded hub sync status polish ^hub-sync-polish
  - [x] Fix PJAX test flake
  - [ ] Update shipped-docs page
- [ ] Draft tasks RFC — blocked by [[#^prime-design]]

## Next
- [ ] Prime adaptive tree rendering ^prime-design

## Later
- [ ] Kanban view of backlog pages on Hub

## Someday
- [ ] Beads issues.jsonl importer

## Done
- [x] Beads research → [[beads-research-report]]
`

func TestParseRFCWorkedExample(t *testing.T) {
	b := ParseBacklogContent(rfcWorkedExample, "backlog.md")
	want := []struct {
		line          int
		text          string
		status        TaskStatus
		band          string
		slug          string
		depth         int
		parentSlug    string
		blockedActive bool
		openChildren  int
		ready         bool
	}{
		{7, "Embedded hub sync status polish", TaskInProgress, "Now", "hub-sync-polish", 0, "", false, 1, false},
		{8, "Fix PJAX test flake", TaskDone, "Now", "", 1, "hub-sync-polish", false, 0, false},
		{9, "Update shipped-docs page", TaskOpen, "Now", "", 1, "hub-sync-polish", false, 0, true},
		{10, "Draft tasks RFC — blocked by [[#^prime-design]]", TaskOpen, "Now", "", 0, "", true, 0, false},
		{13, "Prime adaptive tree rendering", TaskOpen, "Next", "prime-design", 0, "", false, 0, true},
		{16, "Kanban view of backlog pages on Hub", TaskOpen, "Later", "", 0, "", false, 0, true},
		{19, "Beads issues.jsonl importer", TaskOpen, "Someday", "", 0, "", false, 0, false},
		{22, "Beads research → [[beads-research-report]]", TaskDone, "Done", "", 0, "", false, 0, false},
	}
	flat := b.Flat()
	if len(flat) != len(want) {
		t.Fatalf("parsed %d tasks, want %d: %v", len(flat), len(want), texts(flat))
	}
	for i, w := range want {
		got := flat[i]
		if got.Line != w.line || got.Text != w.text || got.Status != w.status || got.Band != w.band ||
			got.Slug != w.slug || got.Depth != w.depth || got.ParentSlug != w.parentSlug ||
			got.BlockedActive != w.blockedActive || got.OpenChildren != w.openChildren || got.Ready != w.ready {
			t.Errorf("task %d:\n got {line:%d text:%q status:%q band:%q slug:%q depth:%d parent:%q blocked:%v open:%d ready:%v}\nwant {line:%d text:%q status:%q band:%q slug:%q depth:%d parent:%q blocked:%v open:%d ready:%v}",
				i, got.Line, got.Text, got.Status, got.Band, got.Slug, got.Depth, got.ParentSlug, got.BlockedActive, got.OpenChildren, got.Ready,
				w.line, w.text, w.status, w.band, w.slug, w.depth, w.parentSlug, w.blockedActive, w.openChildren, w.ready)
		}
	}
	// The blocker names a task that exists but is not finished, so it holds.
	if refs := flat[3].BlockedRefs; !equalStrings(refs, []string{"prime-design"}) {
		t.Errorf("blocker refs = %v, want [prime-design]", refs)
	}
	if got := texts(b.ReadyTasks()); !equalStrings(got, []string{"Update shipped-docs page", "Prime adaptive tree rendering", "Kanban view of backlog pages on Hub"}) {
		t.Errorf("ReadyTasks = %v", got)
	}
	if got := texts(b.InProgress()); !equalStrings(got, []string{"Embedded hub sync status polish"}) {
		t.Errorf("InProgress = %v", got)
	}
	// Two top-level tasks under Now, one each under the other bands.
	if len(b.Tasks) != 6 {
		t.Errorf("top-level tasks = %d, want 6", len(b.Tasks))
	}
}

// core owns the per-task JSON shape so the CLI, the hub, and MCP cannot drift
// from the RFC or from each other: flat scalars, the blocker nested, refs never
// null, and no children (Flat() already yields every task).
func TestTaskJSONShape(t *testing.T) {
	b := ParseBacklogContent("## Now\n- [ ] parent — blocked by [[#^ghost]] ^p\n  - [/] child\n", "backlog.md")
	data, err := json.Marshal(b.Flat())
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("marshaled %d tasks, want 2", len(got))
	}
	for _, key := range []string{"file", "line", "text", "status", "band", "slug", "depth", "parent_slug", "blocked", "open_children", "ready"} {
		if _, ok := got[0][key]; !ok {
			t.Errorf("task JSON missing %q: %s", key, data)
		}
	}
	if _, ok := got[0]["children"]; ok {
		t.Errorf("task JSON should not nest children: %s", data)
	}
	blocked, ok := got[0]["blocked"].(map[string]any)
	if !ok {
		t.Fatalf("blocked is not an object: %s", data)
	}
	if blocked["active"] != true || blocked["reason"] != "[[#^ghost]]" {
		t.Errorf("blocked = %v", blocked)
	}
	if refs, ok := blocked["refs"].([]any); !ok || len(refs) != 1 || refs[0] != "ghost" {
		t.Errorf("blocked refs = %v", blocked["refs"])
	}
	// A task with no blocker still publishes an empty list, never null.
	child, _ := got[1]["blocked"].(map[string]any)
	if refs, ok := child["refs"].([]any); !ok || len(refs) != 0 {
		t.Errorf("absent refs marshaled as %v, want []", child["refs"])
	}
	if got[1]["status"] != string(TaskInProgress) || got[1]["parent_slug"] != "p" {
		t.Errorf("child task = %v", got[1])
	}
}

// The backlog resolves by marker wherever it lives and whatever it is called,
// and no name resolves it on its own — the marker is the only truth. Since
// contract 0.11.0 a marked directory INDEX.md confers the role on the DIRECTORY
// (the spine is that INDEX.md); a marked ordinary page is the retired 0.10.0
// shape and still resolves, as legacy.
func TestBacklogRoleResolution(t *testing.T) {
	t.Run("marker", func(t *testing.T) {
		root := newInstance(t, map[string]string{
			"plans/INDEX.md": "---\ndescription: Plans.\n---\n",
			"plans/work.md":  "---\ndescription: The backlog.\nagentsfs_role: backlog\n---\n## Now\n- [ ] a\n",
		})
		roles, err := ResolveReservedDirs(root)
		if err != nil {
			t.Fatal(err)
		}
		if roles.Backlog != "plans/work.md" || roles.BacklogSource != RoleSourceMarker {
			t.Fatalf("backlog = %q by %q, want plans/work.md by marker", roles.Backlog, roles.BacklogSource)
		}
		b, ok, err := LoadBacklog(root)
		if err != nil || !ok {
			t.Fatalf("LoadBacklog = (%v, %v, %v)", b, ok, err)
		}
		if b.Path != "plans/work.md" || len(b.Tasks) != 1 {
			t.Errorf("loaded backlog = %+v", b)
		}
		if b.Tasks[0].File != "plans/work.md" {
			t.Errorf("task File = %q, want the backlog's rel path", b.Tasks[0].File)
		}
	})

	// No classic-name fallback: a file called backlog.md without the marker is an
	// ordinary note.
	t.Run("none", func(t *testing.T) {
		root := newInstance(t, map[string]string{
			"backlog.md": "---\ndescription: Looks like one.\n---\n## Now\n- [ ] a\n",
		})
		roles, err := ResolveReservedDirs(root)
		if err != nil {
			t.Fatal(err)
		}
		if roles.Backlog != "" || roles.BacklogSource != RoleSourceNone {
			t.Fatalf("backlog = %q by %q, want empty by none", roles.Backlog, roles.BacklogSource)
		}
		b, ok, err := LoadBacklog(root)
		if err != nil {
			t.Fatal(err)
		}
		if ok || b != nil {
			t.Errorf("LoadBacklog found a backlog where none is declared: %+v", b)
		}
	})

	// Several marked pages resolve to the first in sorted path order, with every
	// one reported so doctor can name them.
	t.Run("duplicates", func(t *testing.T) {
		root := newInstance(t, map[string]string{
			"a-work.md": "---\ndescription: One.\nagentsfs_role: backlog\n---\n## Now\n- [ ] from a\n",
			"z-work.md": "---\ndescription: Another.\nagentsfs_role: backlog\n---\n## Now\n- [ ] from z\n",
		})
		roles, err := ResolveReservedDirs(root)
		if err != nil {
			t.Fatal(err)
		}
		if roles.Backlog != "a-work.md" {
			t.Errorf("backlog = %q, want the first in sorted order", roles.Backlog)
		}
		if !equalStrings(roles.DuplicateBacklog, []string{"a-work.md", "z-work.md"}) {
			t.Errorf("DuplicateBacklog = %v, want both pages", roles.DuplicateBacklog)
		}
	})

	// Contract 0.11.0 inverted the 0.10.0 rule this subtest used to pin: a marked
	// INDEX.md now confers the role on its DIRECTORY, whose INDEX.md is the spine.
	t.Run("marker on a directory INDEX", func(t *testing.T) {
		root := newInstance(t, map[string]string{
			"plans/INDEX.md": "---\ndescription: Plans, and the backlog.\nagentsfs_role: backlog\n---\n## Now\n- [ ] a\n",
		})
		roles, err := ResolveReservedDirs(root)
		if err != nil {
			t.Fatal(err)
		}
		if roles.Backlog != "plans" || roles.BacklogSpine != "plans/INDEX.md" || roles.BacklogLegacy {
			t.Errorf("backlog = %q spine %q legacy %v, want the plans DIRECTORY", roles.Backlog, roles.BacklogSpine, roles.BacklogLegacy)
		}
		if roles.Journal == "plans" || roles.Scratch == "plans" || len(roles.Collections) != 0 {
			t.Errorf("a backlog marker conferred another directory's role: %+v", roles)
		}
		b, ok, err := LoadBacklog(root)
		if err != nil || !ok {
			t.Fatalf("LoadBacklog = (%v, %v, %v)", b, ok, err)
		}
		if b.Dir != "plans" || b.Spine != "plans/INDEX.md" || len(b.Tasks) != 1 {
			t.Errorf("loaded backlog = %+v", b)
		}
	})

	// The directory role wins over a page still carrying the 0.10.0 marker: an
	// instance mid-upgrade has both, and the directory is where the work moved.
	t.Run("directory beats legacy page", func(t *testing.T) {
		root := newInstance(t, map[string]string{
			"backlog/INDEX.md": "---\ndescription: The backlog.\nagentsfs_role: backlog\n---\n## Now\n- [ ] from the spine\n",
			"old-work.md":      "---\ndescription: The old one.\nagentsfs_role: backlog\n---\n## Now\n- [ ] from the page\n",
		})
		roles, err := ResolveReservedDirs(root)
		if err != nil {
			t.Fatal(err)
		}
		if roles.Backlog != "backlog" || roles.BacklogSpine != "backlog/INDEX.md" || roles.BacklogLegacy {
			t.Fatalf("backlog = %q spine %q legacy %v, want the directory", roles.Backlog, roles.BacklogSpine, roles.BacklogLegacy)
		}
		// The straggler is still reported, so doctor can name it.
		if !equalStrings(roles.DuplicateBacklog, []string{"backlog", "old-work.md"}) {
			t.Errorf("DuplicateBacklog = %v, want both homes", roles.DuplicateBacklog)
		}
	})
}

// The 0.10.0 page role still resolves — read-only compatibility — and says so,
// so doctor can suggest the upgrade rather than an agent silently losing its
// backlog to a contract change.
func TestLegacyPageBacklogStillResolves(t *testing.T) {
	root := newInstance(t, map[string]string{
		"plans/INDEX.md": "---\ndescription: Plans.\n---\n",
		"plans/work.md":  "---\ndescription: The backlog.\nagentsfs_role: backlog\n---\n## Now\n- [ ] a\n",
	})
	roles, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if roles.Backlog != "plans/work.md" || roles.BacklogSpine != "plans/work.md" || !roles.BacklogLegacy {
		t.Fatalf("roles = %+v, want the legacy page", roles)
	}
	b, ok, err := LoadBacklog(root)
	if err != nil || !ok {
		t.Fatalf("LoadBacklog = (%v, %v, %v)", b, ok, err)
	}
	if b.Dir != "" || b.Spine != "plans/work.md" || len(b.Pages) != 1 {
		t.Errorf("legacy backlog = %+v, want one page and no directory", b)
	}
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(findings, "backlog-page-role-legacy", "plans/work.md") {
		t.Errorf("no backlog-page-role-legacy finding: %+v", findings)
	}
}

// Two pages claiming the role is genuine ambiguity — nothing downstream can say
// which page is the backlog until a human picks one — so it is an error, like
// the directory roles.
func TestDoctorDuplicateBacklog(t *testing.T) {
	root := newInstance(t, map[string]string{
		"a-work.md": "---\ndescription: One.\nagentsfs_role: backlog\n---\n## Now\n- [ ] a\n",
		"z-work.md": "---\ndescription: Another.\nagentsfs_role: backlog\n---\n## Now\n- [ ] z\n",
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a-work.md", "z-work.md"} {
		if !hasFinding(findings, "duplicate-backlog", rel) {
			t.Errorf("no duplicate-backlog finding for %s: %+v", rel, findings)
		}
	}
	sawError := false
	for _, f := range findings {
		if f.Code == "duplicate-backlog" {
			if f.Severity == "error" {
				sawError = true
			}
			if !strings.Contains(f.Message, "a-work.md") || !strings.Contains(f.Message, "z-work.md") {
				t.Errorf("duplicate-backlog message should name both pages: %q", f.Message)
			}
		}
	}
	if !sawError {
		t.Errorf("duplicate-backlog must be an error: %+v", findings)
	}
}

// The three content findings, each with a fixture that trips it and one that
// does not — a check that fires on healthy input is worse than no check.
func TestDoctorBacklogFindings(t *testing.T) {
	const header = "---\ndescription: Backlog.\nagentsfs_role: backlog\n---\n"
	cases := []struct {
		name  string
		body  string
		code  string
		fires bool
	}{
		{
			name:  "duplicate slug",
			body:  "## Now\n- [ ] one ^dup\n- [ ] two ^dup\n",
			code:  "duplicate-task-slug",
			fires: true,
		},
		{
			name:  "distinct slugs",
			body:  "## Now\n- [ ] one ^a\n- [ ] two ^b\n",
			code:  "duplicate-task-slug",
			fires: false,
		},
		{
			name:  "reference to a slug nothing defines",
			body:  "## Now\n- [ ] one — blocked by [[#^ghost]]\n",
			code:  "dangling-task-ref",
			fires: true,
		},
		{
			name:  "reference that resolves",
			body:  "## Now\n- [ ] one — blocked by [[#^real]]\n- [ ] two ^real\n",
			code:  "dangling-task-ref",
			fires: false,
		},
		{
			name:  "plain-text blocker is not a dangling reference",
			body:  "## Now\n- [ ] one — blocked by the adjuster\n",
			code:  "dangling-task-ref",
			fires: false,
		},
		{
			name:  "checked parent with open child",
			body:  "## Now\n- [x] parent\n  - [ ] child\n",
			code:  "task-parent-inconsistent",
			fires: true,
		},
		{
			name:  "checked parent with in-progress child",
			body:  "## Now\n- [x] parent\n  - [/] child\n",
			code:  "task-parent-inconsistent",
			fires: true,
		},
		{
			name:  "checked parent with finished children",
			body:  "## Now\n- [x] parent\n  - [x] child\n  - [-] other\n",
			code:  "task-parent-inconsistent",
			fires: false,
		},
		{
			name:  "open parent with open child is normal",
			body:  "## Now\n- [ ] parent\n  - [ ] child\n",
			code:  "task-parent-inconsistent",
			fires: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := newInstance(t, map[string]string{"backlog.md": header + c.body})
			findings, err := Doctor(root)
			if err != nil {
				t.Fatal(err)
			}
			if got := hasFinding(findings, c.code, "backlog.md"); got != c.fires {
				t.Fatalf("%s finding = %v, want %v:\n%+v", c.code, got, c.fires, findings)
			}
			// None of the three is structural ambiguity: a backlog with a typo in
			// it must never fail a command.
			for _, f := range findings {
				if f.Code == c.code && f.Severity != "warn" {
					t.Errorf("%s severity = %q, want warn", c.code, f.Severity)
				}
			}
		})
	}
}

// An instance with no backlog at all is normal — doctor must stay silent rather
// than nag about a role nothing has adopted yet.
func TestDoctorNoBacklogIsSilent(t *testing.T) {
	findings, err := Doctor(newInstance(t, map[string]string{
		"notes/INDEX.md": "---\ndescription: Notes.\n---\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		switch f.Code {
		case "duplicate-backlog", "duplicate-task-slug", "dangling-task-ref", "task-parent-inconsistent":
			t.Errorf("backlog finding on an instance with no backlog: %+v", f)
		}
	}
}
