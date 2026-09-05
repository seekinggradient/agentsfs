package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Contract 0.11.0's backlog directory: a spine, ticket files beside it, an
// archive that task parsing never sees, and sub-backlogs reachable only by
// delegation from the spine. These pin the semantics the RFC decided, since
// nothing else in the parser can tell you whether work is released.

const spineWithDelegation = `---
description: The backlog.
agentsfs_role: backlog
---
# Backlog

## Now
- [/] Voice v3 → [[backlog/voice/INDEX]] ^voice-v3
- [ ] Ship the thing ^ship

## Later
- [ ] Parked idea
`

const voiceSubSpine = `---
description: Voice v3 workstream.
---
# Voice

## Now
- [ ] Lane manager ^lanes
- [ ] Turn queue — blocked by [[#^lanes]]

## Later
- [ ] Distillation
`

func newBacklogInstance(t *testing.T, files map[string]string) string {
	t.Helper()
	return newInstance(t, files)
}

// Delegation is nesting across a file boundary: the sub-spine's tasks are ready
// only while a non-terminal line links it, and they rank where that line ranks.
func TestDelegatedSubSpineIsReadyThroughItsDelegation(t *testing.T) {
	root := newBacklogInstance(t, map[string]string{
		"backlog/INDEX.md":       spineWithDelegation,
		"backlog/voice/INDEX.md": voiceSubSpine,
	})
	b, ok, err := LoadBacklog(root)
	if err != nil || !ok {
		t.Fatalf("LoadBacklog = (%v, %v, %v)", b, ok, err)
	}
	if !equalStrings(b.Pages, []string{"backlog/INDEX.md", "backlog/voice/INDEX.md"}) {
		t.Fatalf("pages = %v, want the spine then the sub-spine", b.Pages)
	}
	if len(b.Undelegated) != 0 {
		t.Errorf("delegated sub-spine reported as undelegated: %+v", b.Undelegated)
	}

	ready := b.ReadyTasks()
	var texts []string
	for _, r := range ready {
		texts = append(texts, r.Text)
	}
	// The delegating line is in Now and precedes "Ship the thing", so its WHOLE
	// subtree ranks ahead of it — Later work in the sub-backlog included, since
	// the root spine ranks the subtree and the sub-spine only orders within it.
	// The blocked turn queue is not ready at all.
	want := []string{"Lane manager", "Distillation", "Ship the thing", "Parked idea"}
	if !equalStrings(texts, want) {
		t.Errorf("ready = %v, want %v", texts, want)
	}

	delegating := b.Tasks[0]
	if len(delegating.Delegates) != 3 {
		t.Fatalf("delegating task has %d delegates, want 3", len(delegating.Delegates))
	}
	if delegating.OpenChildren != 3 {
		t.Errorf("OpenChildren = %d, want the sub-spine's 3 open tasks", delegating.OpenChildren)
	}
}

// The root spine is the sole priority authority: a terminal delegation releases
// nothing, and doctor says so rather than the parser silently reopening it.
func TestTerminalDelegationReleasesNothing(t *testing.T) {
	root := newBacklogInstance(t, map[string]string{
		"backlog/INDEX.md":       strings.Replace(spineWithDelegation, "- [/] Voice v3", "- [x] Voice v3", 1),
		"backlog/voice/INDEX.md": voiceSubSpine,
	})
	b, _, err := LoadBacklog(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range b.ReadyTasks() {
		if r.Page != "backlog/INDEX.md" {
			t.Errorf("task %q from %s is ready under a closed delegation", r.Text, r.Page)
		}
	}
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(findings, "delegation-terminal", "backlog/INDEX.md") {
		t.Errorf("no delegation-terminal finding: %+v", findings)
	}
}

// A sub-backlog nobody delegates to is a parked workstream: parsed and listed,
// never ready, and surfaced by doctor as info rather than an error.
func TestUndelegatedSubSpineIsParsedButNeverReady(t *testing.T) {
	root := newBacklogInstance(t, map[string]string{
		"backlog/INDEX.md":       "---\ndescription: The backlog.\nagentsfs_role: backlog\n---\n## Now\n- [ ] Ship the thing\n",
		"backlog/voice/INDEX.md": voiceSubSpine,
	})
	b, _, err := LoadBacklog(root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(b.UndelegatedPages, []string{"backlog/voice/INDEX.md"}) {
		t.Fatalf("UndelegatedPages = %v", b.UndelegatedPages)
	}
	found := false
	for _, task := range b.Flat() {
		if task.Page != "backlog/voice/INDEX.md" {
			continue
		}
		found = true
		if task.Ready {
			t.Errorf("undelegated task %q is ready", task.Text)
		}
		if !task.Undelegated {
			t.Errorf("undelegated task %q is not marked", task.Text)
		}
	}
	if !found {
		t.Error("undelegated sub-spine tasks are missing from Flat()")
	}
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(findings, "sub-backlog-undelegated", "backlog/voice/INDEX.md") {
		t.Errorf("no sub-backlog-undelegated finding: %+v", findings)
	}
}

// Slugs are a per-file namespace, like block anchors everywhere else: [[#^s]]
// means this page, and a cross-page reference names its page.
func TestCrossPageBlockerResolution(t *testing.T) {
	root := newBacklogInstance(t, map[string]string{
		"backlog/INDEX.md": "---\ndescription: The backlog.\nagentsfs_role: backlog\n---\n" +
			"## Now\n" +
			"- [/] Voice → [[backlog/voice/INDEX]] ^voice\n" +
			"- [ ] Announce it — blocked by [[voice/INDEX#^lanes]]\n" +
			"- [ ] Write the post — blocked by [[voice/INDEX#^shipped]]\n" +
			"- [ ] Same-page one — blocked by [[#^voice]]\n",
		"backlog/voice/INDEX.md": "---\ndescription: Voice.\n---\n## Now\n- [x] Lane manager ^lanes\n- [ ] Turn queue ^voice\n",
	})
	b, _, err := LoadBacklog(root)
	if err != nil {
		t.Fatal(err)
	}
	byText := map[string]*Task{}
	for _, task := range b.Flat() {
		byText[task.Text] = task
	}
	// The cross-page target is done, so the blocker lifted itself.
	if got := byText["Announce it — blocked by [[voice/INDEX#^lanes]]"]; got == nil || got.BlockedActive {
		t.Errorf("cross-page blocker on a done task did not lift: %+v", got)
	}
	// ^shipped exists on no page: the reference dangles and the block holds.
	if got := byText["Write the post — blocked by [[voice/INDEX#^shipped]]"]; got == nil || !got.BlockedActive {
		t.Errorf("dangling cross-page blocker lifted: %+v", got)
	}
	// ^voice names a task on BOTH pages; the bare form means this one, which is
	// still in progress, so the block holds.
	if got := byText["Same-page one — blocked by [[#^voice]]"]; got == nil || !got.BlockedActive {
		t.Errorf("same-page [[#^voice]] resolved to the sub-spine's task: %+v", got)
	}
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(findings, "dangling-task-ref", "backlog/INDEX.md") {
		t.Errorf("no dangling-task-ref finding for ^shipped: %+v", findings)
	}
	// Two pages may each name a task ^voice without either reference becoming
	// ambiguous — the namespace is per file.
	for _, f := range findings {
		if f.Code == "duplicate-task-slug" {
			t.Errorf("per-file slug reported as a duplicate: %+v", f)
		}
	}
}

// The owner-blocked channel: an agent parks a question and pulls the next item.
func TestOwnerBlockedChannel(t *testing.T) {
	b := ParseBacklogContent("## Now\n"+
		"- [ ] Pick a pricing tier — blocked by Owner:  do we charge per seat?\n"+
		"- [ ] Wait for the adjuster — blocked by their response\n", "backlog.md")
	tasks := b.Flat()
	if !tasks[0].OwnerBlocked || tasks[0].OwnerQuestion != "do we charge per seat?" {
		t.Errorf("owner blocker = %+v", tasks[0])
	}
	if !tasks[0].BlockedActive || tasks[0].Ready {
		t.Errorf("owner-blocked task is not blocked: %+v", tasks[0])
	}
	if tasks[1].OwnerBlocked {
		t.Errorf("ordinary blocker read as owner-blocked: %+v", tasks[1])
	}
	waiting := b.OwnerBlocked()
	if len(waiting) != 1 || waiting[0] != tasks[0] {
		t.Errorf("OwnerBlocked() = %+v", waiting)
	}
}

// An owner blocker never lifts itself, whatever it references: only the owner's
// answer resolves it.
func TestOwnerBlockedNeverLifts(t *testing.T) {
	b := ParseBacklogContent("## Now\n- [x] Done thing ^done\n- [ ] Ask — blocked by owner: ok to ship [[#^done]]?\n", "backlog.md")
	ask := b.Flat()[1]
	if !ask.BlockedActive || ask.Ready {
		t.Errorf("owner blocker lifted on a finished reference: %+v", ask)
	}
}

// Prime gives the owner's questions their own subsection, ahead of ready work.
func TestPrimeSurfacesOwnerBlocked(t *testing.T) {
	root := newBacklogInstance(t, map[string]string{
		"backlog/INDEX.md": "---\ndescription: The backlog.\nagentsfs_role: backlog\n---\n## Now\n" +
			"- [ ] Pick a tier — blocked by owner: per seat or per repo?\n" +
			"- [ ] Ship the thing\n",
	})
	pack, err := Prime(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	section, ok := pack.Section("Tasks")
	if !ok {
		t.Fatalf("no Tasks section: %s", pack.Text)
	}
	waiting := strings.Index(section.Body, "Waiting on you")
	ready := strings.Index(section.Body, "Ready")
	if waiting < 0 || ready < 0 || waiting > ready {
		t.Errorf("owner-blocked section missing or after ready work:\n%s", section.Body)
	}
	if !strings.Contains(section.Body, "per seat or per repo?") {
		t.Errorf("the question itself is not in the pack:\n%s", section.Body)
	}
}

const archivedSpine = `---
description: The backlog.
agentsfs_role: backlog
---
## Now
- [ ] Ship the thing → [[backlog/ship]] ^ship
`

// The archive is history, not a second backlog: it is excluded from parsing
// entirely, and read back only through LoadBacklogArchive.
func TestArchiveIsExcludedFromTasksAndReadSeparately(t *testing.T) {
	root := newBacklogInstance(t, map[string]string{
		"backlog/INDEX.md": archivedSpine,
		"backlog/ship.md":  "---\ndescription: Ship the thing.\n---\n# Ship\n\nThe spec.\n\n## Log\n- 2026-08-09 — started\n",
		"backlog/archive/INDEX.md": "---\ndescription: Closed work.\nagentsfs_role: collection\n---\n" +
			"## Now\n- [ ] this must never be parsed as a task\n",
		"backlog/archive/2026.md": "---\ndescription: Closed in 2026.\n---\n" +
			"- 2026-07-01 — [x] Built the parser [[agent-journal/entry]] ^parser\n" +
			"- 2026-07-14 — [-] Dropped the kanban ^kanban\n" +
			"- not an archive line\n",
		"backlog/archive/voice-lanes.md": "---\ndescription: Voice lanes.\nclosed: 2026-08-01\n---\n# Lanes\n",
	})
	b, _, err := LoadBacklog(root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(b.Pages, []string{"backlog/INDEX.md"}) {
		t.Fatalf("pages = %v, want the spine alone — archive/INDEX.md is not a spine", b.Pages)
	}
	for _, task := range b.Flat() {
		if strings.Contains(task.Page, "archive") {
			t.Errorf("archive task parsed as pending work: %+v", task)
		}
	}

	archive, err := LoadBacklogArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(archive) != 3 {
		t.Fatalf("archive = %+v, want two rollup lines and one ticket", archive)
	}
	if archive[0].Date != "2026-07-01" || archive[0].Status != TaskDone ||
		archive[0].Slug != "parser" || archive[0].Ref != "agent-journal/entry" ||
		archive[0].Page != "backlog/archive/2026.md" {
		t.Errorf("rollup line = %+v", archive[0])
	}
	if archive[1].Status != TaskDropped || archive[1].Text != "Dropped the kanban" {
		t.Errorf("dropped rollup line = %+v", archive[1])
	}
	if archive[2].Ticket != "backlog/archive/voice-lanes.md" || archive[2].Date != "2026-08-01" {
		t.Errorf("archived ticket = %+v", archive[2])
	}
}

// Ticket files are earned by a task line, so doctor reports the states where
// that link and the ticket's location disagree.
func TestDoctorTicketAndArchiveFindings(t *testing.T) {
	root := newBacklogInstance(t, map[string]string{
		"backlog/INDEX.md": "---\ndescription: The backlog.\nagentsfs_role: backlog\n---\n## Now\n" +
			"- [ ] Ship the thing → [[backlog/ship]] ^ship\n" +
			"- [x] Old work → [[backlog/old]] ^old\n" +
			"- [ ] Still going → [[backlog/archive/live]] ^live\n",
		"backlog/ship.md":          "---\ndescription: Ship the thing, in detail, with a spec long enough not to read as a stub of a note.\n---\n# Ship\n",
		"backlog/old.md":           "---\ndescription: Old work, in detail, with a spec long enough not to read as a stub of a note.\n---\n# Old\n",
		"backlog/nobody.md":        "---\ndescription: Nobody links this ticket, and it is long enough not to read as a stub of a note.\n---\n# Nobody\n",
		"backlog/archive/INDEX.md": "---\ndescription: Closed work.\nagentsfs_role: collection\n---\n",
		"backlog/archive/live.md":  "---\ndescription: Archived early.\n---\n# Live\n",
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ code, path string }{
		{"ticket-orphaned", "backlog/nobody.md"},
		{"ticket-unarchived", "backlog/old.md"},
		{"archive-live-ticket", "backlog/archive/live.md"},
	} {
		if !hasFinding(findings, want.code, want.path) {
			t.Errorf("no %s finding for %s: %+v", want.code, want.path, findings)
		}
	}
	// A ticket an open task links is exactly where it belongs.
	for _, f := range findings {
		if f.Path == "backlog/ship.md" && strings.HasPrefix(f.Code, "ticket-") {
			t.Errorf("live ticket flagged: %+v", f)
		}
	}
}

// An archived ticket must carry the closed: stamp the sweep writes.
func TestDoctorArchivedTicketNeedsClosedDate(t *testing.T) {
	root := newBacklogInstance(t, map[string]string{
		"backlog/INDEX.md":          "---\ndescription: The backlog.\nagentsfs_role: backlog\n---\n## Now\n- [ ] Ship it\n",
		"backlog/archive/INDEX.md":  "---\ndescription: Closed work.\nagentsfs_role: collection\n---\n",
		"backlog/archive/2026.md":   "---\ndescription: Closed in 2026.\n---\n- 2026-07-01 — [x] Built it\n",
		"backlog/archive/naked.md":  "---\ndescription: Moved without a stamp.\n---\n# Naked\n",
		"backlog/archive/proper.md": "---\ndescription: Moved properly.\nclosed: 2026-08-01\n---\n# Proper\n",
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(findings, "archive-live-ticket", "backlog/archive/naked.md") {
		t.Errorf("no archive-live-ticket finding for the unstamped file: %+v", findings)
	}
	for _, rel := range []string{"backlog/archive/proper.md", "backlog/archive/2026.md"} {
		if hasFinding(findings, "archive-live-ticket", rel) {
			t.Errorf("%s flagged: %+v", rel, findings)
		}
	}
}

// Cross-instance triage groups by instance and invents no order across them.
func TestTasksAcrossInstances(t *testing.T) {
	fleet := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(fleet, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Discovery keys on AGENTS.md declaring the contract, exactly as `afs status`
	// finds a fleet.
	agents := "---\ndescription: An instance.\nagentsfs_contract: " + CurrentContractVersion() +
		"\n---\n# This folder is an agentsfs\n"
	write("alpha/AGENTS.md", agents)
	write("alpha/INDEX.md", "---\ndescription: The alpha workspace.\n---\n")
	write("alpha/backlog/INDEX.md", "---\ndescription: The backlog.\nagentsfs_role: backlog\n---\n## Now\n"+
		"- [/] Working on it\n- [ ] Pick a tier — blocked by owner: per seat?\n- [ ] Ship it\n")
	write("beta/AGENTS.md", agents)
	write("beta/INDEX.md", "---\ndescription: The beta workspace.\n---\n")

	report, err := TasksAcrossInstances([]string{fleet}, StatusOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Instances) != 2 {
		t.Fatalf("instances = %+v, want two", report.Instances)
	}
	for _, s := range report.Scopes {
		if !s.Complete {
			t.Errorf("incomplete scope: %+v", s)
		}
	}
	var alpha, beta InstanceTasks
	for _, inst := range report.Instances {
		if strings.HasSuffix(inst.Path, "alpha") {
			alpha = inst
		}
		if strings.HasSuffix(inst.Path, "beta") {
			beta = inst
		}
	}
	if alpha.Backlog != "backlog/INDEX.md" || alpha.Description != "The alpha workspace." {
		t.Errorf("alpha = %+v", alpha)
	}
	if len(alpha.InProgress) != 1 || len(alpha.OwnerBlocked) != 1 || len(alpha.Ready) != 1 {
		t.Errorf("alpha view = in-progress %d, owner-blocked %d, ready %d",
			len(alpha.InProgress), len(alpha.OwnerBlocked), len(alpha.Ready))
	}
	// An instance with no backlog is listed with an empty view, not dropped.
	if beta.Path == "" || beta.Backlog != "" || len(beta.Ready) != 0 || beta.Error != "" {
		t.Errorf("beta = %+v", beta)
	}
}
