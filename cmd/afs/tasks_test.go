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

// newCLIInstance writes a throwaway instance for the CLI to run against. The
// contract version is this binary's own so the stale-contract nudge (stderr,
// and runAFS combines the streams) never shows up in an assertion.
func newCLIInstance(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
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
	for _, want := range []string{"agentsfs_role: backlog", "backlog.md", "afs contract upgrade"} {
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

func TestRolesReportsTheBacklogPage(t *testing.T) {
	home := t.TempDir()
	root := newCLIInstance(t, map[string]string{"backlog.md": testBacklogPage})

	out, err := runAFS(t, root, home, "roles")
	if err != nil {
		t.Fatalf("afs roles failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "backlog      backlog.md (by marker)") {
		t.Errorf("afs roles did not report the backlog page:\n%s", out)
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
