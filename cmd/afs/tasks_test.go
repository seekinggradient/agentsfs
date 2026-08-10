package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentsfs.ai/afs/internal/core"
)

const testBacklogPage = `---
description: Project backlog.
agentsfs_role: backlog
---

## Now
- [/] Embedded hub sync status polish ^hub-sync-polish
  - [x] Fix PJAX test flake
  - [ ] Update shipped-docs page
- [ ] Draft tasks RFC — blocked by [[#^prime-design]]

## Next
- [ ] Prime adaptive tree rendering ^prime-design

## Someday
- [ ] Beads issues.jsonl importer

## Done
- [x] Beads research
`

// The 0.11.0 shape: a backlog DIRECTORY whose INDEX.md is the spine, one
// delegated sub-spine, a ticket file, and an archive.
const testSpine = `---
description: Project backlog.
agentsfs_role: backlog
---

## Now
- [/] Embedded hub sync status polish → [[backlog/hub-sync]] ^hub-sync-polish
  - [x] Fix PJAX test flake
  - [ ] Update shipped-docs page
- [ ] Pick a TTS vendor — blocked by owner: Play.ht or ElevenLabs? ^tts-vendor
- [ ] Voice v3 lanes → [[backlog/voice/INDEX]] ^voice-v3

## Next
- [ ] Prime adaptive tree rendering ^prime-design

## Someday
- [ ] Beads issues.jsonl importer
`

const testSubSpine = `---
description: Voice v3 workstream.
---

## Now
- [ ] Turn-queue distillation ^turn-queue

## Next
- [ ] Lane telemetry — blocked by owner: do we keep the old lane names? ^lane-telemetry
`

const testTicket = `---
description: The embedded hub sync polish ticket.
---

# Hub sync polish

What is true right now.

## Log

- 2026-08-01 — opened
`

const testArchiveYear = `---
description: Closed backlog items, 2026.
---

# 2026

- 2026-08-02 — [x] Ship the backlog parser ^backlog-parser
- 2026-08-07 — [-] Beads adoption ^beads
`

// dirBacklogFiles is the seeded instance every 0.11.0 test runs against.
func dirBacklogFiles() map[string]string {
	return map[string]string{
		"backlog/INDEX.md":              testSpine,
		"backlog/voice/INDEX.md":        testSubSpine,
		"backlog/hub-sync.md":           testTicket,
		"backlog/archive/INDEX.md":      "---\ndescription: Closed work.\nagentsfs_role: collection\n---\n",
		"backlog/archive/2026.md":       testArchiveYear,
		"backlog/archive/old-ticket.md": "---\ndescription: The retired importer spike.\nclosed: 2025-12-11\n---\n",
	}
}

// newCLIInstance writes a throwaway instance for the CLI to run against. The
// contract version is this binary's own so the stale-contract nudge (stderr,
// and runAFS combines the streams) never shows up in an assertion.
func newCLIInstance(t *testing.T, files map[string]string) string {
	t.Helper()
	return newCLIInstanceAt(t, t.TempDir(), files)
}

// newCLIInstanceAt is newCLIInstance at a chosen path, so a test can put two
// instances under one search root.
func newCLIInstanceAt(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := map[string]string{
		"AGENTS.md": "---\ndescription: The contract.\nagentsfs_contract: " + core.CurrentContractVersion() +
			"\n---\n# This folder is an agentsfs\n",
		"INDEX.md": "---\ndescription: Working memory for the AgentsFS project itself.\n---\n",
	}
	for rel, content := range files {
		base[rel] = content
	}
	for rel, content := range base {
		mustWriteFile(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	return root
}

func TestTasksShowsInProgressThenReadyThenCounts(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, map[string]string{"backlog.md": testBacklogPage})

	out, err := runAFS(t, root, home, "tasks")
	if err != nil {
		t.Fatalf("afs tasks failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"In progress",
		"[/] Embedded hub sync status polish [^hub-sync-polish]",
		"Now\n  [ ] Update shipped-docs page",
		"Next\n  [ ] Prime adaptive tree rendering [^prime-design]",
		"1 blocked · 1 parked (Someday) · 2 done",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("afs tasks output missing %q:\n%s", want, out)
		}
	}
	// The default view is for choosing work: what is blocked, parked, or done is
	// counted, not listed.
	for _, unwanted := range []string{"Draft tasks RFC", "Beads issues.jsonl importer", "[x] Beads research"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("default view listed %q, which the summary counts:\n%s", unwanted, out)
		}
	}
}

func TestTasksScopeFlags(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, map[string]string{"backlog.md": testBacklogPage})

	all, err := runAFS(t, root, home, "tasks", "--all")
	if err != nil {
		t.Fatalf("afs tasks --all failed: %v\n%s", err, all)
	}
	for _, want := range []string{
		"Someday",
		"[x] Beads research",
		"    [x] Fix PJAX test flake", // nesting preserved: decomposition is visible
		"[ ] Draft tasks RFC — blocked by [[#^prime-design]]",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("afs tasks --all missing %q:\n%s", want, all)
		}
	}

	ready, err := runAFS(t, root, home, "tasks", "--ready")
	if err != nil {
		t.Fatalf("afs tasks --ready failed: %v\n%s", err, ready)
	}
	if !strings.HasPrefix(ready, "Now   [ ] Update shipped-docs page") {
		t.Errorf("afs tasks --ready is not flat and band-ordered:\n%s", ready)
	}
	if strings.Contains(ready, "Beads") {
		t.Errorf("afs tasks --ready included parked or done work:\n%s", ready)
	}

	// Bands match case-insensitively: the page writes "## Next", a caller types
	// whatever they remember.
	band, err := runAFS(t, root, home, "tasks", "--band", "next")
	if err != nil {
		t.Fatalf("afs tasks --band failed: %v\n%s", err, band)
	}
	if !strings.Contains(band, "[ ] Prime adaptive tree rendering [^prime-design]") {
		t.Errorf("afs tasks --band next missing its task:\n%s", band)
	}
	if strings.Contains(band, "Update shipped-docs page") {
		t.Errorf("afs tasks --band next leaked another band:\n%s", band)
	}

	if out, err := runAFS(t, root, home, "tasks", "--all", "--ready"); err == nil {
		t.Errorf("conflicting scope flags were accepted:\n%s", out)
	}
}

// The JSON surface is core's Task shape verbatim — one contract for the CLI,
// the Hub, and anything else that reads a backlog.
func TestTasksJSONEmitsCoreTaskRecords(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, map[string]string{"backlog.md": testBacklogPage})

	out, err := runAFS(t, root, home, "tasks", "--json")
	if err != nil {
		t.Fatalf("afs tasks --json failed: %v\n%s", err, out)
	}
	var body struct {
		Backlog *string `json:"backlog"`
		Tasks   []struct {
			File       string `json:"file"`
			Line       int    `json:"line"`
			Text       string `json:"text"`
			Status     string `json:"status"`
			Band       string `json:"band"`
			Slug       string `json:"slug"`
			Depth      int    `json:"depth"`
			ParentSlug string `json:"parent_slug"`
			Blocked    struct {
				Active bool     `json:"active"`
				Reason string   `json:"reason"`
				Refs   []string `json:"refs"`
			} `json:"blocked"`
			OpenChildren int  `json:"open_children"`
			Ready        bool `json:"ready"`
		} `json:"tasks"`
	}
	// The helper process prints go test's own "PASS" after the command's
	// output, so decode the first JSON value and ignore what follows.
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&body); err != nil {
		t.Fatalf("afs tasks --json is not JSON: %v\n%s", err, out)
	}
	if body.Backlog == nil || *body.Backlog != "backlog.md" {
		t.Fatalf("backlog path = %v, want backlog.md", body.Backlog)
	}
	if len(body.Tasks) != 7 {
		t.Fatalf("got %d tasks, want every task on the page (7):\n%s", len(body.Tasks), out)
	}
	first := body.Tasks[0]
	if first.Status != "in_progress" || first.Slug != "hub-sync-polish" || first.OpenChildren != 1 {
		t.Errorf("first task record = %+v", first)
	}
	blockedFound := false
	for _, task := range body.Tasks {
		if strings.HasPrefix(task.Text, "Draft tasks RFC") {
			blockedFound = true
			if !task.Blocked.Active || len(task.Blocked.Refs) != 1 || task.Blocked.Refs[0] != "prime-design" {
				t.Errorf("blocker record = %+v", task.Blocked)
			}
			if task.Ready {
				t.Errorf("a blocked task was reported ready: %+v", task)
			}
		}
	}
	if !blockedFound {
		t.Errorf("the blocked task is missing from --json:\n%s", out)
	}
}

// No backlog is a normal state, not a failure: guidance on stdout, exit 0, and
// a null backlog in JSON so a program can tell "none" from "empty".
func TestTasksWithoutBacklogGuidesAndSucceeds(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, nil)

	out, err := runAFS(t, root, home, "tasks")
	if err != nil {
		t.Fatalf("afs tasks should succeed without a backlog: %v\n%s", err, out)
	}
	for _, want := range []string{"agentsfs_role: backlog", "backlog/INDEX.md", "afs contract upgrade"} {
		if !strings.Contains(out, want) {
			t.Errorf("no-backlog guidance missing %q:\n%s", want, out)
		}
	}

	out, err = runAFS(t, root, home, "tasks", "--json")
	if err != nil {
		t.Fatalf("afs tasks --json failed: %v\n%s", err, out)
	}
	var body struct {
		Backlog *string       `json:"backlog"`
		Tasks   []interface{} `json:"tasks"`
	}
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&body); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if body.Backlog != nil {
		t.Errorf("backlog = %q, want null", *body.Backlog)
	}
	if body.Tasks == nil || len(body.Tasks) != 0 {
		t.Errorf("tasks = %v, want an empty array (never null)", body.Tasks)
	}
}

func TestPrimePrintsTheOrientationPack(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, map[string]string{
		"backlog.md":             testBacklogPage,
		"agent-journal/INDEX.md": "---\ndescription: Session journal.\nagentsfs_role: journal\n---\n",
		"agent-journal/2026-08-06T090000Z-c3d4-parser.md": "---\ndescription: Landed the backlog parser.\n---\n",
		"projects/INDEX.md":     "---\ndescription: Active efforts.\n---\n",
		"projects/hub/INDEX.md": "---\ndescription: Embedded Hub sync.\n---\n",
	})

	out, err := runAFS(t, root, home, "prime")
	if err != nil {
		t.Fatalf("afs prime failed: %v\n%s", err, out)
	}
	// The CLI resolves its own cwd, which on macOS is the symlink-resolved form
	// of t.TempDir(), so the identity line is matched by shape, not by path.
	want := []string{
		"# AgentsFS: /",
		filepath.Base(root) + "\n",
		"Working memory for the AgentsFS project itself.",
		"Contract: agentsfs " + core.CurrentContractVersion() + " — read AGENTS.md before writing.",
		"## Tasks",
		"[/] Embedded hub sync status polish [^hub-sync-polish]",
		"Full backlog: afs tasks",
		"## Tree",
		"## Recent journal",
		"agent-journal/2026-08-06T090000Z-c3d4-parser.md — Landed the backlog parser.",
		"## Pointers",
		"afs docs agent-start",
	}
	at := -1
	for _, w := range want {
		i := strings.Index(out, w)
		if i < 0 {
			t.Fatalf("prime output missing %q:\n%s", w, out)
		}
		if i < at {
			t.Errorf("prime section %q is out of order:\n%s", w, out)
		}
		at = i
	}

	// A budget too small for the tree drops the tree, never the contract line or
	// the work in flight.
	tight, err := runAFS(t, root, home, "prime", "--budget", "70")
	if err != nil {
		t.Fatalf("afs prime --budget failed: %v\n%s", err, tight)
	}
	if strings.Contains(tight, "## Tree") {
		t.Errorf("a 70-token pack kept the tree:\n%s", tight)
	}
	for _, w := range []string{"Contract: agentsfs", "## Tasks", "## Pointers"} {
		if !strings.Contains(tight, w) {
			t.Errorf("a tight budget dropped %q instead of the tree:\n%s", w, tight)
		}
	}
	if out, err := runAFS(t, root, home, "prime", "--budget", "0"); err == nil {
		t.Errorf("--budget 0 was accepted:\n%s", out)
	}
}

func TestTreeBudgetDegradesAndReportsTheTier(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, map[string]string{
		"projects/INDEX.md":      "---\ndescription: Active efforts with an end state.\n---\n",
		"projects/hub/INDEX.md":  "---\ndescription: The embedded Hub sync work, end to end.\n---\n",
		"projects/hub/status.md": "---\ndescription: Where the Hub sync work stands right now.\n---\n",
		"reference/INDEX.md":     "---\ndescription: Stable facts and documents worth keeping.\n---\n",
		"reference/beads.md":     "---\ndescription: Comparative study of Beads against AgentsFS primitives.\n---\n",
	})

	full, err := runAFS(t, root, home, "tree")
	if err != nil {
		t.Fatalf("afs tree failed: %v\n%s", err, full)
	}
	if !strings.Contains(full, "status.md") {
		t.Fatalf("unbudgeted tree is not the full tree:\n%s", full)
	}

	capped, err := runAFS(t, root, home, "tree", "--budget", "60")
	if err != nil {
		t.Fatalf("afs tree --budget failed: %v\n%s", err, capped)
	}
	if len(capped) >= len(full) {
		t.Errorf("--budget 60 did not shrink the tree:\n%s", capped)
	}
	if !strings.Contains(capped, "afs tree:") || !strings.Contains(capped, "budget") {
		t.Errorf("a degraded tree did not say it was degraded:\n%s", capped)
	}

	// An explicit --depth is an instruction; --budget is a ceiling. The
	// instruction wins, and the CLI says the ceiling was ignored.
	both, err := runAFS(t, root, home, "tree", "--depth", "1", "--budget", "4000")
	if err != nil {
		t.Fatalf("afs tree --depth --budget failed: %v\n%s", err, both)
	}
	if !strings.Contains(both, "--budget is ignored") {
		t.Errorf("precedence between --depth and --budget was silent:\n%s", both)
	}
	if strings.Contains(both, "status.md") {
		t.Errorf("--depth 1 was not honored:\n%s", both)
	}
}

func TestRolesReportsTheBacklogDirectoryAndItsSpine(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, dirBacklogFiles())

	out, err := runAFS(t, root, home, "roles")
	if err != nil {
		t.Fatalf("afs roles failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "backlog      backlog/ (by marker)") {
		t.Errorf("afs roles did not report the backlog directory:\n%s", out)
	}
	if !strings.Contains(out, "spine        backlog/INDEX.md") {
		t.Errorf("afs roles did not report the spine:\n%s", out)
	}

	// The retired 0.10.0 page role still resolves, and says which shape it is.
	legacy := newCLIInstance(t, map[string]string{"backlog.md": testBacklogPage})
	out, err = runAFS(t, legacy, home, "roles")
	if err != nil {
		t.Fatalf("afs roles failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "backlog      backlog.md (by marker, page (legacy))") {
		t.Errorf("afs roles did not mark the legacy page role:\n%s", out)
	}

	bare := newCLIInstance(t, nil)
	out, err = runAFS(t, bare, home, "roles")
	if err != nil {
		t.Fatalf("afs roles failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "backlog      (none)") {
		t.Errorf("afs roles did not report an absent backlog:\n%s", out)
	}
}

// The default view over a 0.11.0 directory backlog: in progress, then the
// owner's questions, then ready work — with delegated sub-spine tasks labeled
// by the page they live on, since that is the file the reader has to edit.
func TestTasksDefaultViewShowsOwnerBlockedAndLabelsSubSpines(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, dirBacklogFiles())

	out, err := runAFS(t, root, home, "tasks")
	if err != nil {
		t.Fatalf("afs tasks failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"In progress",
		"[/] Embedded hub sync status polish → [[backlog/hub-sync]] [^hub-sync-polish]",
		"Blocked on owner",
		"[ ] Pick a TTS vendor — blocked by owner: Play.ht or ElevenLabs? [^tts-vendor]",
		"Now\n  [ ] Update shipped-docs page",
		"backlog/voice/INDEX.md:",
		"    [ ] Turn-queue distillation [^turn-queue]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("afs tasks output missing %q:\n%s", want, out)
		}
	}
	// The section order is fixed: work in flight, then questions, then what can
	// be pulled.
	inProgress := strings.Index(out, "In progress")
	owner := strings.Index(out, "Blocked on owner")
	now := strings.Index(out, "\nNow\n")
	if !(inProgress < owner && owner < now) {
		t.Errorf("default sections are out of order (%d, %d, %d):\n%s", inProgress, owner, now, out)
	}
	// An owner-blocked task is a question, never ready work.
	if strings.Count(out, "Pick a TTS vendor") != 1 {
		t.Errorf("the owner-blocked task appeared outside its own section:\n%s", out)
	}
}

// A backlog with no owner-blocked task has no owner-blocked section: the
// default view earns every line it prints.
func TestTasksDefaultViewOmitsEmptyOwnerSection(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, map[string]string{"backlog.md": testBacklogPage})

	out, err := runAFS(t, root, home, "tasks")
	if err != nil {
		t.Fatalf("afs tasks failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "Blocked on owner") {
		t.Errorf("an empty owner-blocked section was printed:\n%s", out)
	}
}

// --blocked-on-owner is the owner's inbox: every parked question, on every page,
// with the file and line to answer it at.
func TestTasksBlockedOnOwnerIsTheOwnerInbox(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, dirBacklogFiles())

	out, err := runAFS(t, root, home, "tasks", "--blocked-on-owner")
	if err != nil {
		t.Fatalf("afs tasks --blocked-on-owner failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"backlog/INDEX.md:",
		"? Play.ht or ElevenLabs?",
		"backlog/voice/INDEX.md:",
		"? do we keep the old lane names?",
		"2 question(s) waiting on you",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("owner inbox missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Update shipped-docs page") {
		t.Errorf("the owner inbox listed ordinary ready work:\n%s", out)
	}

	empty := newCLIInstance(t, map[string]string{"backlog.md": testBacklogPage})
	out, err = runAFS(t, empty, home, "tasks", "--blocked-on-owner")
	if err != nil {
		t.Fatalf("afs tasks --blocked-on-owner failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Nothing is blocked on you.") {
		t.Errorf("an empty owner inbox did not say so:\n%s", out)
	}
}

// --done reads the archive: rollup lines and archived ticket files, newest
// first, grouped by year.
func TestTasksDoneReadsTheArchive(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, dirBacklogFiles())

	out, err := runAFS(t, root, home, "tasks", "--done")
	if err != nil {
		t.Fatalf("afs tasks --done failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"2026\n",
		"2026-08-07 [-] Beads adoption [^beads]",
		"2026-08-02 [x] Ship the backlog parser [^backlog-parser]",
		"2025\n",
		"2025-12-11 [x] The retired importer spike.",
		"backlog/archive/old-ticket.md",
		"3 closed item(s) in backlog/archive",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("afs tasks --done missing %q:\n%s", want, out)
		}
	}
	// Newest first, across years as well as within one.
	if strings.Index(out, "2026-08-07") > strings.Index(out, "2026-08-02") ||
		strings.Index(out, "2026-08-02") > strings.Index(out, "2025-12-11") {
		t.Errorf("archive is not newest-first:\n%s", out)
	}

	jsonOut, err := runAFS(t, root, home, "tasks", "--done", "--json")
	if err != nil {
		t.Fatalf("afs tasks --done --json failed: %v\n%s", err, jsonOut)
	}
	var body struct {
		Backlog *string `json:"backlog"`
		Archive []struct {
			Date   string `json:"date"`
			Status string `json:"status"`
			Text   string `json:"text"`
			Page   string `json:"page"`
		} `json:"archive"`
	}
	if err := json.NewDecoder(strings.NewReader(jsonOut)).Decode(&body); err != nil {
		t.Fatalf("afs tasks --done --json is not JSON: %v\n%s", err, jsonOut)
	}
	if body.Backlog == nil || *body.Backlog != "backlog/INDEX.md" {
		t.Errorf("backlog = %v, want the spine path", body.Backlog)
	}
	if len(body.Archive) != 3 {
		t.Errorf("got %d archived entries, want 3:\n%s", len(body.Archive), jsonOut)
	}
}

// The JSON envelope: backlog is the SPINE, and pages appears once a delegated
// sub-spine is part of the backlog.
func TestTasksJSONReportsSpineAndPages(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, dirBacklogFiles())

	out, err := runAFS(t, root, home, "tasks", "--json")
	if err != nil {
		t.Fatalf("afs tasks --json failed: %v\n%s", err, out)
	}
	var body struct {
		Backlog *string  `json:"backlog"`
		Pages   []string `json:"pages"`
		Tasks   []struct {
			File          string `json:"file"`
			Slug          string `json:"slug"`
			OwnerBlocked  bool   `json:"owner_blocked"`
			OwnerQuestion string `json:"owner_question"`
		} `json:"tasks"`
	}
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&body); err != nil {
		t.Fatalf("afs tasks --json is not JSON: %v\n%s", err, out)
	}
	if body.Backlog == nil || *body.Backlog != "backlog/INDEX.md" {
		t.Fatalf("backlog = %v, want backlog/INDEX.md", body.Backlog)
	}
	if len(body.Pages) != 2 || body.Pages[0] != "backlog/INDEX.md" || body.Pages[1] != "backlog/voice/INDEX.md" {
		t.Fatalf("pages = %v, want the spine then the sub-spine", body.Pages)
	}
	found := false
	for _, task := range body.Tasks {
		if task.Slug == "tts-vendor" {
			found = true
			if !task.OwnerBlocked || task.OwnerQuestion != "Play.ht or ElevenLabs?" {
				t.Errorf("owner-blocked record = %+v", task)
			}
		}
	}
	if !found {
		t.Errorf("the owner-blocked task is missing from --json:\n%s", out)
	}

	// One page, no pages key: most backlogs are a single spine.
	single, err := runAFS(t, newCLIInstance(t, map[string]string{"backlog.md": testBacklogPage}), home, "tasks", "--json")
	if err != nil {
		t.Fatalf("afs tasks --json failed: %v\n%s", err, single)
	}
	if strings.Contains(single, "\"pages\"") {
		t.Errorf("a single-page backlog published a pages list:\n%s", single)
	}
}

// Cross-instance triage: one group per instance, no ordering invented between
// them.
func TestTasksAcrossInstancesGroupsByInstance(t *testing.T) {
	home := t.TempDir()
	parent := t.TempDir()
	first := newCLIInstanceAt(t, filepath.Join(parent, "alpha"), dirBacklogFiles())
	second := newCLIInstanceAt(t, filepath.Join(parent, "beta"), map[string]string{
		"INDEX.md":   "---\ndescription: The beta knowledge base.\n---\n",
		"backlog.md": testBacklogPage,
	})

	out, err := runAFS(t, parent, home, "tasks")
	if err != nil {
		t.Fatalf("afs tasks over a search root failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Tasks scope: AgentsFS instances discoverable within:",
		filepath.Base(first),
		filepath.Base(second),
		"The beta knowledge base.",
		"In progress",
		"Blocked on owner",
		"Ready",
		"ready ·",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fleet tasks output missing %q:\n%s", want, out)
		}
	}

	jsonOut, err := runAFS(t, parent, home, "tasks", "--json")
	if err != nil {
		t.Fatalf("afs tasks --json over a search root failed: %v\n%s", err, jsonOut)
	}
	var report struct {
		SchemaVersion int `json:"schema_version"`
		Scopes        []struct {
			Complete bool `json:"complete"`
		} `json:"scopes"`
		Instances []struct {
			Path       string `json:"path"`
			Backlog    string `json:"backlog"`
			ReadyTotal int    `json:"ready_total"`
		} `json:"instances"`
	}
	if err := json.NewDecoder(strings.NewReader(jsonOut)).Decode(&report); err != nil {
		t.Fatalf("fleet --json is not the fleet report: %v\n%s", err, jsonOut)
	}
	if report.SchemaVersion != 1 || len(report.Instances) != 2 || len(report.Scopes) == 0 {
		t.Fatalf("fleet report = %+v\n%s", report, jsonOut)
	}
	for _, inst := range report.Instances {
		if inst.Backlog == "" {
			t.Errorf("instance %s reported no backlog:\n%s", inst.Path, jsonOut)
		}
	}
}
