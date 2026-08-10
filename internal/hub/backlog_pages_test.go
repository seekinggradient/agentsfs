package hub

import (
	"net/http"
	"strings"
	"testing"
)

// Contract 0.11.0 (agentsfs/rfcs/backlog-directories.md) grew the backlog from
// one page into a directory: a spine (the directory's INDEX.md), ticket detail
// files beside it, delegated sub-backlogs, and an archive. These tests cover
// what the Hub renders for each of those, and pin the 0.10.0 legacy page —
// still the shape of every un-upgraded instance — as a regression.

// backlogSpinePage is a directory spine: the marker is in the INDEX.md's own
// frontmatter, the body is the same task grammar, and its lines exercise the
// three new decorations (owner-blocked, delegation, ticket link).
const backlogSpinePage = `---
description: Project backlog — the spine
agentsfs_role: backlog
---

# Backlog

## Now
- [/] Voice v3 lanes → [[backlog/voice/INDEX]] ^voice-v3
- [ ] Ship the render pass → [[voice-lanes]] ^render-pass
- [ ] Pick a palette — blocked by owner: warm or cool? ^palette
- [ ] Wait on the adjuster — blocked by [[#^render-pass]]

## Next
- [ ] Write the archive sweep ^sweep
`

const backlogSubSpine = `---
description: Voice workstream — delegated sub-backlog
---

# Voice

## Now
- [x] Lane scaffolding
- [x] Turn queue
- [/] Distillation
- [ ] Voice v3 cutover
`

const backlogTicketPage = `---
description: Voice v3 lanes — the spec and its decision trail
---

# Voice v3 lanes

The lane model splits the turn queue from distillation.

## Log
- 2026-08-09 — tried the two-lane split, blocked on the queue
- 2026-08-08 — started; wrote the spec above
- an undated note that still belongs to the timeline
`

func renderSpine(t *testing.T, content string, links *backlogLinks) string {
	t.Helper()
	rendered, err := renderMarkdownWith(content, markdownOptions{
		backlog:      true,
		backlogLinks: links,
		resolveWiki:  func(target string) (string, bool) { return "/alice/brain/blob/" + target + ".md", true },
	})
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}

// TestResolveBacklogPlacement covers the ancestor lookup: which of the four
// renderings a page inside (or outside) a backlog directory earns.
func TestResolveBacklogPlacement(t *testing.T) {
	const marked = "---\nagentsfs_role: backlog\n---\n"
	tree := map[string]string{
		"backlog/INDEX.md":            marked,
		"backlog/voice-lanes.md":      "---\ndescription: a ticket\n---\n",
		"backlog/voice/INDEX.md":      "---\ndescription: a sub-spine\n---\n",
		"backlog/voice/lane-a.md":     "---\ndescription: a sub-ticket\n---\n",
		"backlog/archive/INDEX.md":    "---\nagentsfs_role: collection\n---\n",
		"backlog/archive/2026.md":     "---\ndescription: closed in 2026\n---\n",
		"backlog/archive/old-work.md": "---\nclosed: 2026-08-01\n---\n",
		"notes/plain.md":              "---\ndescription: unrelated\n---\n",
		"legacy-backlog.md":           marked,
		"backlog/diagram.png":         "",
	}
	blob := func(rel string) (string, bool) { c, ok := tree[rel]; return c, ok }

	for _, tc := range []struct {
		path string
		want backlogPlacement
	}{
		{"backlog/INDEX.md", backlogPlacement{spine: true, dir: "backlog", root: "backlog"}},
		{"legacy-backlog.md", backlogPlacement{spine: true}},
		{"backlog/voice-lanes.md", backlogPlacement{ticket: true, dir: "backlog", root: "backlog"}},
		{"backlog/voice/INDEX.md", backlogPlacement{spine: true, dir: "backlog/voice", root: "backlog"}},
		{"backlog/voice/lane-a.md", backlogPlacement{ticket: true, dir: "backlog/voice", root: "backlog"}},
		{"backlog/archive/old-work.md", backlogPlacement{ticket: true, archived: true, dir: "backlog/archive", root: "backlog"}},
		{"backlog/archive/2026.md", backlogPlacement{rollup: true, archived: true, dir: "backlog/archive", root: "backlog"}},
		{"backlog/archive/INDEX.md", backlogPlacement{archived: true, dir: "backlog/archive", root: "backlog"}},
		{"notes/plain.md", backlogPlacement{}},
		{"backlog/diagram.png", backlogPlacement{}},
	} {
		if got := resolveBacklogPlacement(tc.path, tree[tc.path], blob); got != tc.want {
			t.Errorf("resolveBacklogPlacement(%q) = %+v, want %+v", tc.path, got, tc.want)
		}
	}

	// Without a tree to read (a view whose repository listing failed), a page
	// that is not itself marked stays an ordinary note rather than erroring.
	if got := resolveBacklogPlacement("backlog/voice-lanes.md", tree["backlog/voice-lanes.md"], nil); got.inBacklog() {
		t.Errorf("a page resolved into a backlog with no tree to read: %+v", got)
	}
}

// TestBacklogLinkSegment pins the conservative matching rule: a wikilink
// resolves by name across the whole instance, so only a target that really
// names something in this backlog directory may earn a decoration.
func TestBacklogLinkSegment(t *testing.T) {
	for _, tc := range []struct {
		target    string
		wantIndex bool
		name      string
		ok        bool
	}{
		{"backlog/voice/INDEX", true, "voice", true},
		{"voice/INDEX", true, "voice", true},
		{"voice/index.md", true, "voice", true},
		{"voice/INDEX#^slug", true, "voice", true},
		{"voice", true, "", false},
		{"other/voice/INDEX", true, "", false},
		{"backlog/archive/INDEX", true, "", false},
		{"voice-lanes", false, "voice-lanes", true},
		{"backlog/voice-lanes.md", false, "voice-lanes", true},
		{"voice/lane-a", false, "", false},
		{"../escape", false, "", false},
		{"INDEX", false, "", false},
	} {
		name, ok := backlogLinkSegment("backlog", tc.target, tc.wantIndex)
		if ok != tc.ok || name != tc.name {
			t.Errorf("backlogLinkSegment(%q, wantIndex=%v) = (%q, %v), want (%q, %v)",
				tc.target, tc.wantIndex, name, ok, tc.name, tc.ok)
		}
	}
}

// TestBacklogOwnerBlockedBadge covers the owner-blocked channel: a question
// parked for the owner reads as an ask, not as work held up by other work.
func TestBacklogOwnerBlockedBadge(t *testing.T) {
	rendered := renderSpine(t, backlogSpinePage, nil)

	wantAll(t, rendered,
		`<span class="task-blocked task-blocked-owner" title="warm or cool?" aria-label="Waiting on you: warm or cool?">waiting on you</span>`,
		// The ordinary blocker on the next line keeps the generic badge.
		`<span class="task-blocked">blocked</span>`,
	)
	if strings.Count(rendered, "task-blocked-owner") != 1 {
		t.Errorf("expected exactly one owner-blocked badge:\n%s", rendered)
	}

	// Case-insensitive on the word, tolerant of the spacing an author types,
	// and an empty question still parks the line.
	loose := renderSpine(t, "## Now\n- [ ] Decide — Blocked By Owner :  ship it?\n- [ ] Ask — blocked by owner:\n", nil)
	wantAll(t, loose, `title="ship it?"`, `title="Waiting on an answer from the owner"`)

	// The question is document text: it reaches the tooltip escaped, never as
	// markup or as a way out of the attribute.
	hostile := renderSpine(t, `## Now`+"\n"+`- [ ] X — blocked by owner: " onmouseover="alert(1)`+"\n", nil)
	if strings.Contains(hostile, `" onmouseover="alert(1)"`) {
		t.Errorf("an owner question broke out of the title attribute:\n%s", hostile)
	}
	wantAll(t, hostile, `&#34; onmouseover=&#34;alert(1)`)
}

// TestBacklogDelegationChipAndTicketDots covers the two link decorations. Both
// are repository lookups the page cannot answer for itself, so both degrade to
// nothing when the view has no tree to consult.
func TestBacklogDelegationChipAndTicketDots(t *testing.T) {
	links := &backlogLinks{
		delegate: func(target string) (backlogDelegation, bool) {
			if target == "backlog/voice/INDEX" {
				return backlogDelegation{name: "voice", done: 3, total: 7}, true
			}
			return backlogDelegation{}, false
		},
		ticket: func(target string) bool { return target == "voice-lanes" },
	}
	rendered := renderSpine(t, backlogSpinePage, links)

	wantAll(t, rendered,
		`<a class="wl wl-delegate" href="/alice/brain/blob/backlog/voice/INDEX.md">`,
		`<span class="task-delegate" style="--band-fill:42%" role="img" aria-label="voice: 3 of 7 done" title="voice: 3 of 7 done"><b>voice</b> 3/7</span>`,
		// The dot is a class only; the item's own task-<status> class colours it.
		`<a class="wl wl-ticket" href="/alice/brain/blob/voice-lanes.md">`,
	)

	// No links resolver — a legacy page, or a view that could not read the tree.
	bare := renderSpine(t, backlogSpinePage, nil)
	for _, unwanted := range []string{"task-delegate", "wl-ticket", "wl-delegate"} {
		if strings.Contains(bare, unwanted) {
			t.Errorf("decoration %q appeared without a links resolver:\n%s", unwanted, bare)
		}
	}
}

// TestTicketLogRendersAsTimeline covers the body-vs-log split: the log's dates
// leave the prose and become the timeline's markers.
func TestTicketLogRendersAsTimeline(t *testing.T) {
	rendered, err := renderMarkdownWith(backlogTicketPage, markdownOptions{
		ticket:      true,
		resolveWiki: func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, rendered,
		`<h2 id="log" class="ticket-log">Log</h2>`,
		`<ul class="ticket-log-list">`,
		`<li class="log-entry"><span class="log-date">2026-08-09</span>— tried the two-lane split`,
		`<span class="log-date">2026-08-08</span>`,
		// An undated line still joins the timeline, without a marker.
		`<li class="log-entry">an undated note`,
	)
	// The body above the log is ordinary prose, and a ticket is not a task page.
	if strings.Contains(rendered, "task-status") || strings.Contains(rendered, "band-progress") {
		t.Errorf("a ticket page picked up the spine's task rendering:\n%s", rendered)
	}

	// A `## Log` heading is only a log inside a ticket: an ordinary note keeps
	// GFM's rendering, and a section after the log ends the timeline.
	plain, err := renderMarkdown(backlogTicketPage, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "log-entry") || strings.Contains(plain, "log-date") {
		t.Errorf("an ordinary note picked up the log timeline:\n%s", plain)
	}
	after, err := renderMarkdownWith("## Log\n- 2026-08-09 — one\n\n## Notes\n- 2026-08-09 — two\n", markdownOptions{ticket: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(after, "log-entry") != 1 {
		t.Errorf("the timeline ran past the end of the log section:\n%s", after)
	}
}

// TestBacklogDirectoryFileView is the wiring test: a whole backlog directory
// served by the Hub, with each of its page kinds asserted through the real file
// view — the spine found by its directory's marker, a sub-spine found by being
// beneath it, a ticket, an archived ticket, and a rollup.
func TestBacklogDirectoryFileView(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	seedHTMLRepo(t, srv, "alice", "brain", map[string]string{
		"backlog/INDEX.md":         backlogSpinePage,
		"backlog/voice/INDEX.md":   backlogSubSpine,
		"backlog/voice-lanes.md":   backlogTicketPage,
		"backlog/archive/INDEX.md": "---\ndescription: Closed work\nagentsfs_role: collection\n---\n\n# Archive\n",
		"backlog/archive/2026.md":  "---\ndescription: Closed in 2026\n---\n\n# 2026\n\n- [x] 2026-07-02 — Beads research ^beads\n- [-] 2026-07-11 — Kanban prototype ^kanban\n",
		"backlog/archive/old-spec.md": "---\ndescription: An old spec\nclosed: 2026-08-01\n---\n\n# Old spec\n\n## Log\n" +
			"- 2026-08-01 — closed, superseded\n",
		"notes/plain.md": "---\ndescription: unrelated\n---\n\n## Log\n- 2026-08-09 — not a ticket\n",
	})
	if err := srv.setVisibility("alice", "brain", visPublic); err != nil {
		t.Fatal(err)
	}
	get := func(path string) string {
		t.Helper()
		res, body := getNoRedirect(t, ts.URL+"/alice/brain/blob/"+path, false)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, res.StatusCode)
		}
		return body
	}

	// The spine: found by its own directory's INDEX.md marker, with the bands,
	// the owner-blocked badge, a live delegation chip counted from the sub-spine
	// (2 of 4 done), and a state dot on the ticket link.
	spine := get("backlog/INDEX.md")
	wantAll(t, spine,
		`class="band band-now"`,
		`class="task task-inprogress" id="task-voice-v3"`,
		`class="task-blocked task-blocked-owner"`,
		`<b>voice</b> 2/4`,
		`class="wl wl-ticket"`,
		`class="wl wl-delegate"`,
	)

	// A sub-spine carries no marker of its own — it is a spine by being a
	// subdirectory of the backlog directory.
	sub := get("backlog/voice/INDEX.md")
	wantAll(t, sub, `class="band band-now"`, `class="task task-done"`)

	// A ticket: ordinary markdown, the log as a timeline, and a chip saying
	// which backlog it belongs to.
	ticket := get("backlog/voice-lanes.md")
	wantAll(t, ticket,
		`<li class="log-entry"><span class="log-date">2026-08-09</span>`,
		`<a class="backlog-chip" href="/alice/brain/blob/backlog/INDEX.md">part of <b>backlog</b> backlog</a>`,
	)
	if strings.Contains(ticket, "band-progress") {
		t.Errorf("a ticket page picked up the spine rendering:\n%s", ticket)
	}

	// An archived ticket reads exactly like a live one, so the chip is what
	// says otherwise — with the sweep's `closed:` date.
	archived := get("backlog/archive/old-spec.md")
	wantAll(t, archived,
		`<span class="backlog-chip backlog-chip-archived">archived · closed 2026-08-01</span>`,
		`class="log-entry"`,
	)

	// A rollup page is read-only history: the terminal markers its lines carry
	// get the task styling, with no bands to chip.
	rollup := get("backlog/archive/2026.md")
	wantAll(t, rollup,
		`class="task task-done" id="task-beads"`,
		`class="task task-dropped" id="task-kanban"`,
		`backlog-chip-archived`,
	)
	if strings.Contains(rollup, "band-progress") {
		t.Errorf("a rollup page invented a band chip:\n%s", rollup)
	}

	// Nothing outside the backlog directory changes.
	plain := get("notes/plain.md")
	for _, unwanted := range []string{"log-entry", "backlog-chip", "task-status"} {
		if strings.Contains(plain, unwanted) {
			t.Errorf("an unrelated note picked up backlog markup %q:\n%s", unwanted, plain)
		}
	}
}

// TestBacklogLegacyPageStillRenders is the regression: 0.10.0's page-level
// marker is retired but still resolves, and an instance that has not upgraded
// must keep the rendering it had — including inside a directory that has
// nothing to do with the role.
func TestBacklogLegacyPageStillRenders(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	seedHTMLRepo(t, srv, "alice", "brain", map[string]string{
		"backlog.md":            backlogPage,
		"projects/backlog.md":   backlogPage,
		"projects/INDEX.md":     "---\ndescription: Projects\n---\n",
		"projects/unrelated.md": "---\ndescription: not a ticket\n---\n\n## Log\n- 2026-08-09 — nothing\n",
	})
	if err := srv.setVisibility("alice", "brain", visPublic); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"backlog.md", "projects/backlog.md"} {
		res, body := getNoRedirect(t, ts.URL+"/alice/brain/blob/"+path, false)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, res.StatusCode)
		}
		wantAll(t, body, `class="task task-inprogress"`, `class="band band-now"`, `class="task-anchor"`)
		if strings.Contains(body, "backlog-chip") {
			t.Errorf("a legacy page claimed membership in a backlog directory:\n%s", body)
		}
	}
	// A directory with an ordinary INDEX.md is not a backlog, so its notes are
	// not tickets however they are laid out.
	_, plain := getNoRedirect(t, ts.URL+"/alice/brain/blob/projects/unrelated.md", false)
	if strings.Contains(plain, "log-entry") {
		t.Errorf("a note beside a legacy backlog page rendered as a ticket:\n%s", plain)
	}
}
