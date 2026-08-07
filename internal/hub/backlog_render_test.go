package hub

import (
	"net/http"
	"strings"
	"testing"
)

// backlogPage is the RFC's worked example plus the cases the renderer has to
// get right: every status marker, a nested decomposition, a blocked line with a
// same-page task reference, an unrecognized heading, and a fenced code block
// that demonstrates the grammar and must survive untouched.
const backlogPage = `---
description: Project backlog — prioritized ideas and work items
agentsfs_role: backlog
---

# Backlog

## Now
- [/] Embedded hub sync status polish ^hub-sync-polish
  - [x] Fix PJAX test flake
  - [ ] Update shipped-docs page
- [ ] Draft tasks RFC — blocked by [[#^prime-design]]
- [-] Kanban prototype ^kanban-prototype

## Next
- [ ] Prime adaptive tree rendering ^prime-design

## Notes
- [ ] Filed under a heading that is not a band

` + "```markdown\n- [/] a grammar example inside a fence ^not-a-slug\n```" + `

## Done
- [x] Beads research → [[beads-research-report]]
`

func renderBacklog(t *testing.T, content string) string {
	t.Helper()
	rendered, err := renderMarkdownWith(content, markdownOptions{
		backlog:     true,
		resolveWiki: func(target string) (string, bool) { return "/alice/brain/blob/" + target + ".md", true },
	})
	if err != nil {
		t.Fatal(err)
	}
	return rendered
}

func wantAll(t *testing.T, rendered string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered backlog missing %q:\n%s", want, rendered)
		}
	}
}

// TestBacklogRendersEveryStatusMarker is the heart of the feature: GFM knows
// two of the four markers, and the other two must not leak into the prose.
func TestBacklogRendersEveryStatusMarker(t *testing.T) {
	rendered := renderBacklog(t, backlogPage)

	wantAll(t, rendered,
		`<li class="task task-inprogress" id="task-hub-sync-polish">`,
		`<span class="task-status task-inprogress" role="img" aria-label="In progress"`,
		`<li class="task task-done">`,
		`<span class="task-status task-done" role="img" aria-label="Done"`,
		`<li class="task task-open">`,
		`<span class="task-status task-open" role="img" aria-label="To do"`,
		`<li class="task task-dropped" id="task-kanban-prototype">`,
		`<span class="task-status task-dropped" role="img" aria-label="Dropped"`,
		// The prose wrapper is what lets done/dropped style the words without
		// striking through the control or the item's own children.
		`<span class="task-text">Embedded hub sync status polish</span>`,
	)
	if strings.Contains(rendered, `type="checkbox"`) {
		t.Errorf("backlog rendered a raw GFM checkbox instead of a status control:\n%s", rendered)
	}
	// Markers must not survive as prose. Checked on a fence-free page, since the
	// fenced grammar example in backlogPage is meant to keep its literal text.
	prose := renderBacklog(t, "## Now\n- [/] one\n- [-] two\n- [ ] three\n- [x] four\n")
	for _, leaked := range []string{"[/]", "[-]", "[ ]", "[x]"} {
		if strings.Contains(prose, leaked) {
			t.Errorf("rendered backlog still contains the literal marker %q:\n%s", leaked, prose)
		}
	}
}

// TestNonBacklogPageKeepsDefaultRendering pins the blast radius: without the
// role, `[/]` is literal text and `[ ]`/`[x]` stay GFM checkboxes.
func TestNonBacklogPageKeepsDefaultRendering(t *testing.T) {
	const page = "## Now\n- [/] In progress\n- [x] Done\n- [ ] Open ^some-slug\n"
	rendered, err := renderMarkdown(page, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	wantAll(t, rendered,
		"[/] In progress",
		`<input checked="" disabled="" type="checkbox">`,
		`<input disabled="" type="checkbox">`,
		"^some-slug", // a caret anchor is ordinary prose outside the backlog
	)
	for _, unwanted := range []string{"task-status", "band-progress", `class="task`} {
		if strings.Contains(rendered, unwanted) {
			t.Errorf("ordinary note picked up backlog markup %q:\n%s", unwanted, rendered)
		}
	}
}

// TestBacklogBandHeadingsCarryProgressChips covers band recognition and the
// counting rule: every task line under a band, at any depth.
func TestBacklogBandHeadingsCarryProgressChips(t *testing.T) {
	rendered := renderBacklog(t, backlogPage)

	wantAll(t, rendered,
		// Now holds 5 task lines (two of them nested); one is [x].
		`<h2 id="now" class="band band-now">Now<span class="band-progress" style="--band-fill:20%" role="img" aria-label="1 of 5 done" title="1 of 5 done"><b>1</b>/5</span></h2>`,
		`<h2 id="next" class="band band-next">Next<span class="band-progress" style="--band-fill:0%"`,
		`<h2 id="done" class="band band-done">Done<span class="band-progress band-progress-complete" style="--band-fill:100%"`,
	)
	// An unrecognized heading is not a band, and its tasks count toward nothing.
	if !strings.Contains(rendered, `<h2 id="notes">Notes</h2>`) {
		t.Errorf("unrecognized heading was given band treatment:\n%s", rendered)
	}
	// The fenced example is prose, not backlog data: it must neither be
	// rewritten nor counted into the Now band above it.
	wantAll(t, rendered, "[/] a grammar example inside a fence ^not-a-slug")
}

// TestBacklogBandCaseInsensitiveAndEmpty checks the two band edge cases the RFC
// names: case-insensitive matching, and a band with nothing under it.
func TestBacklogBandCaseInsensitiveAndEmpty(t *testing.T) {
	rendered := renderBacklog(t, "## LATER\n- [ ] one\n\n## Someday\n\n## Not a band\n- [ ] two\n")

	wantAll(t, rendered, `<h2 id="later" class="band band-later">LATER<span class="band-progress"`)
	if !strings.Contains(rendered, `<h2 id="someday" class="band band-someday">Someday</h2>`) {
		t.Errorf("an empty band should carry the band class but no chip:\n%s", rendered)
	}
}

// TestBacklogSlugBecomesAnchor covers the three halves of the `^slug` rule: the
// literal leaves the prose, the item becomes a link target, and a copy-link
// affordance appears.
func TestBacklogSlugBecomesAnchor(t *testing.T) {
	rendered := renderBacklog(t, backlogPage)

	wantAll(t, rendered,
		`id="task-hub-sync-polish"`,
		`<a class="task-anchor" href="#task-hub-sync-polish" title="^hub-sync-polish" aria-label="Copy link to task ^hub-sync-polish" data-task-anchor>`,
	)
	if strings.Contains(rendered, "^hub-sync-polish<") || strings.Contains(rendered, "polish ^hub-sync-polish") {
		t.Errorf("the literal ^slug is still in the prose:\n%s", rendered)
	}
	// A caret that is not a valid kebab-case slug stays prose rather than
	// silently disappearing from the page.
	loose := renderBacklog(t, "## Now\n- [ ] Ship it ^Not A Slug\n")
	wantAll(t, loose, "^Not A Slug")
	if strings.Contains(loose, "task-anchor") {
		t.Errorf("a malformed anchor produced a link target:\n%s", loose)
	}
}

// TestBacklogBlockedBadge covers the annotation, and the deliberate limit on
// it: the badge is a render-side note, not a computed verdict.
func TestBacklogBlockedBadge(t *testing.T) {
	rendered := renderBacklog(t, backlogPage)

	wantAll(t, rendered, `<span class="task-blocked">blocked</span>`)
	if strings.Count(rendered, `class="task-blocked"`) != 1 {
		t.Errorf("expected exactly one blocked badge:\n%s", rendered)
	}
	// Case-insensitive, and a plain-text blocker earns the badge too.
	plain := renderBacklog(t, "## Now\n- [ ] Await the adjuster — Blocked By adjuster response\n")
	wantAll(t, plain, `<span class="task-blocked">blocked</span>`)
}

// TestBacklogTaskAnchorWikilinks covers the reference form the grammar uses:
// same-page `[[#^slug]]`, and the cross-file `[[page#^slug]]` that rides the
// view's own name resolution with the task's fragment appended.
func TestBacklogTaskAnchorWikilinks(t *testing.T) {
	rendered := renderBacklog(t, backlogPage)
	wantAll(t, rendered, `<a class="wl" href="#task-prime-design">`)

	cross := renderBacklog(t, "## Now\n- [ ] See [[backlog#^prime-design]] and [[beads-research-report]]\n")
	wantAll(t, cross,
		`<a class="wl" href="/alice/brain/blob/backlog.md#task-prime-design">`,
		`<a class="wl" href="/alice/brain/blob/beads-research-report.md">`,
	)

	// A malformed anchor must not become a link to nowhere; it falls through to
	// ordinary name resolution.
	malformed, err := renderMarkdownWith("- [ ] See [[#^Not A Slug]]\n", markdownOptions{
		backlog:     true,
		resolveWiki: func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(malformed, "wl-missing") {
		t.Errorf("a malformed task reference should render as a missing link:\n%s", malformed)
	}
}

// TestBacklogEscapesUntrustedContent is the security case: a backlog page is
// user data, and everything the renderer emits is built from validated tokens
// or escaped text. Nothing in the page may become markup.
func TestBacklogEscapesUntrustedContent(t *testing.T) {
	const hostile = `---
agentsfs_role: backlog
---

## <img src=x onerror=alert(1)>

## Now
- [/] <script>alert('task')</script> ^ok-slug
- [ ] Quote break " onmouseover="alert(2) — blocked by nothing
- [x] Anchor that tries to escape ^a"><script>alert(3)</script>
- [ ] Reference [[#^b"><img src=x>]]
`
	rendered := renderBacklog(t, hostile)

	// Raw HTML never renders: goldmark stays out of unsafe mode, so tags are
	// dropped and every quote in the surviving text is an entity — an attribute
	// this file writes cannot be broken out of.
	for _, forbidden := range []string{"<script", "<img "} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("untrusted content survived as markup (%q):\n%s", forbidden, rendered)
		}
	}
	wantAll(t, rendered,
		"<!-- raw HTML omitted -->",
		`&quot; onmouseover=&quot;alert(2)`,
		`id="task-ok-slug"`,
	)
	// The hostile anchor is not a valid slug, so it is prose (escaped), not an id.
	if strings.Contains(rendered, `id="task-a`) {
		t.Errorf("a hostile ^anchor became an element id:\n%s", rendered)
	}
	// Attribute values the renderer does emit are escaped by goldmark's
	// attribute writer, so a stray quote cannot break out of class="…".
	if strings.Contains(rendered, `class="task task-open" onmouseover`) {
		t.Errorf("attribute injection through a task line:\n%s", rendered)
	}
}

// TestBacklogFileViewDetectsRoleFromFrontmatter is the wiring test: the file
// page opts into this rendering from the page's OWN frontmatter, and an
// otherwise identical page without the marker keeps today's rendering.
func TestBacklogFileViewDetectsRoleFromFrontmatter(t *testing.T) {
	ts, srv, _ := newTestHubServer(t)
	seedHTMLRepo(t, srv, "alice", "brain", map[string]string{
		"backlog.md": backlogPage,
		"plain.md":   strings.Replace(backlogPage, "agentsfs_role: backlog\n", "", 1),
	})
	if err := srv.setVisibility("alice", "brain", visPublic); err != nil {
		t.Fatal(err)
	}

	res, page := getNoRedirect(t, ts.URL+"/alice/brain/blob/backlog.md", false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("backlog file page status = %d, want 200", res.StatusCode)
	}
	wantAll(t, page,
		`class="task task-inprogress"`,
		`class="band band-now"`,
		`class="task-anchor"`,
	)

	_, plain := getNoRedirect(t, ts.URL+"/alice/brain/blob/plain.md", false)
	if strings.Contains(plain, "task-status") || strings.Contains(plain, "band-progress") {
		t.Errorf("a page without the backlog role picked up the task rendering:\n%s", plain)
	}
	if !strings.Contains(plain, "[/] Embedded hub sync status polish") {
		t.Errorf("a page without the backlog role should keep GFM's literal rendering:\n%s", plain)
	}
}
