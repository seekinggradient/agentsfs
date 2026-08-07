package core

import (
	"fmt"
	"strings"
	"testing"
)

const primeBacklog = `---
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

// primeInstance is a complete instance: root description, contract, backlog,
// journal with several entries, and a tree with somewhere to degrade to.
func primeInstance(t *testing.T, extra map[string]string) string {
	t.Helper()
	desc := func(s string) string { return "---\ndescription: " + s + "\n---\nbody\n" }
	files := map[string]string{
		"INDEX.md":   desc("Working memory for the AgentsFS project itself."),
		"backlog.md": primeBacklog,
		"top.md":     desc("A note at the root."),
		"agent-journal/INDEX.md": "---\ndescription: Session journal.\n" +
			"agentsfs_role: journal\n---\n",
		"agent-journal/2026-08-01T080000Z-old0-hub.md":    desc("The oldest session, which prime must not name."),
		"agent-journal/2026-08-05T101500Z-a1b2-beads.md":  desc("Compared Beads against a markdown-native backlog."),
		"agent-journal/2026-08-06T090000Z-c3d4-parser.md": desc("Landed the backlog parser and ready semantics."),
		"projects/INDEX.md":            desc("Active efforts with an end state."),
		"projects/hub/INDEX.md":        desc("The embedded Hub sync work."),
		"projects/hub/status.md":       desc("Where the Hub sync work stands right now."),
		"projects/hub/design/INDEX.md": desc("Design notes for the projection algorithm."),
		"projects/hub/design/cas.md":   desc("Compare-and-swap semantics for the projection commit."),
		"reference/INDEX.md":           desc("Stable facts and documents."),
		"reference/beads.md":           desc("Comparative study of Beads against AgentsFS primitives."),
	}
	for k, v := range extra {
		files[k] = v
	}
	return newInstance(t, files)
}

func sectionTitles(p PrimePack) []string {
	var out []string
	for _, s := range p.Sections {
		out = append(out, s.Title)
	}
	return out
}

// The whole pack, in the RFC's order: identity, tasks, tree, journal, pointers.
func TestPrimeAssemblesEverySection(t *testing.T) {
	root := primeInstance(t, nil)
	pack, err := Prime(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"", "Tasks", "Tree", "Recent journal", "Pointers"}
	if !equalStrings(sectionTitles(pack), want) {
		t.Fatalf("sections = %v, want %v", sectionTitles(pack), want)
	}
	if pack.Budget != defaultPrimeBudget {
		t.Errorf("budget = %d, want the default %d", pack.Budget, defaultPrimeBudget)
	}

	// Identity: the instance's own description and the contract version DECLARED
	// BY THE INSTANCE — never the one this binary happens to bundle.
	for _, want := range []string{
		root,
		"Working memory for the AgentsFS project itself.",
		"Contract: agentsfs " + ContractVersion(root) + " — read AGENTS.md before writing.",
	} {
		if !strings.Contains(pack.Text, want) {
			t.Errorf("pack is missing identity line %q:\n%s", want, pack.Text)
		}
	}

	// Tasks: in-progress first, then ready by band, then the way to the rest.
	tasks, _ := pack.Section("Tasks")
	for _, want := range []string{
		"[/] Embedded hub sync status polish [^hub-sync-polish]",
		"Now   [ ] Update shipped-docs page",
		"Next  [ ] Prime adaptive tree rendering [^prime-design]",
		"Full backlog: afs tasks",
	} {
		if !strings.Contains(tasks.Body, want) {
			t.Errorf("task section is missing %q:\n%s", want, tasks.Body)
		}
	}
	// Blocked, parked, and done work is not orientation — it is the backlog.
	for _, unwanted := range []string{"Draft tasks RFC", "Beads issues.jsonl importer", "Beads research"} {
		if strings.Contains(tasks.Body, unwanted) {
			t.Errorf("task section listed non-actionable work %q:\n%s", unwanted, tasks.Body)
		}
	}

	// Journal: the two newest by filename, newest first, with descriptions.
	journal, _ := pack.Section("Recent journal")
	lines := strings.Split(strings.TrimRight(journal.Body, "\n"), "\n")
	if len(lines) != primeJournalEntries {
		t.Fatalf("journal section has %d entries, want %d:\n%s", len(lines), primeJournalEntries, journal.Body)
	}
	if !strings.HasPrefix(lines[0], "agent-journal/2026-08-06T090000Z-c3d4-parser.md — Landed the backlog parser") {
		t.Errorf("newest journal entry is not first: %q", lines[0])
	}
	if !strings.Contains(lines[1], "2026-08-05T101500Z-a1b2-beads.md") {
		t.Errorf("second journal line = %q", lines[1])
	}
	if strings.Contains(journal.Body, "old0") {
		t.Errorf("journal section reached past the two newest:\n%s", journal.Body)
	}

	pointers, _ := pack.Section("Pointers")
	for _, want := range []string{"afs docs agent-start", "afs search"} {
		if !strings.Contains(pointers.Body, want) {
			t.Errorf("pointer section is missing %q: %q", want, pointers.Body)
		}
	}
}

// No backlog page: the section is absent entirely, not an empty heading. The
// pack is context to inject, and a heading over nothing costs tokens and says
// nothing.
func TestPrimeWithoutBacklogSkipsTasks(t *testing.T) {
	root := newInstance(t, map[string]string{
		"INDEX.md": "---\ndescription: An instance nobody has planned work in.\n---\n",
	})
	pack, err := Prime(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pack.Section("Tasks"); ok {
		t.Errorf("pack has a task section without a backlog page:\n%s", pack.Text)
	}
	if strings.Contains(pack.Text, "afs tasks") {
		t.Errorf("pack points at afs tasks with no backlog to read:\n%s", pack.Text)
	}
	if _, ok := pack.Section("Pointers"); !ok {
		t.Errorf("a missing section took the rest of the pack with it:\n%s", pack.Text)
	}
}

// No journal (no marker, no classic directory): same rule.
func TestPrimeWithoutJournalSkipsRecentJournal(t *testing.T) {
	root := newInstance(t, map[string]string{
		"INDEX.md":   "---\ndescription: An instance with no journal yet.\n---\n",
		"backlog.md": primeBacklog,
	})
	pack, err := Prime(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pack.Section("Recent journal"); ok {
		t.Errorf("pack has a journal section with no journal:\n%s", pack.Text)
	}
	if _, ok := pack.Section("Tasks"); !ok {
		t.Errorf("task section disappeared with the journal:\n%s", pack.Text)
	}
}

// An empty journal directory is the same absence as no journal at all.
func TestPrimeWithEmptyJournalSkipsRecentJournal(t *testing.T) {
	root := newInstance(t, map[string]string{
		"INDEX.md":               "---\ndescription: A fresh instance.\n---\n",
		"agent-journal/INDEX.md": "---\ndescription: Session journal.\nagentsfs_role: journal\n---\n",
	})
	pack, err := Prime(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pack.Section("Recent journal"); ok {
		t.Errorf("an empty journal produced a section:\n%s", pack.Text)
	}
}

// The task section is capped, and in-progress work takes the cap first: an
// agent that drops half-finished work to start something new is the failure the
// ordering exists to prevent.
func TestPrimeCapsTaskLines(t *testing.T) {
	var page strings.Builder
	page.WriteString("---\ndescription: A long backlog.\nagentsfs_role: backlog\n---\n\n## Now\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&page, "- [/] in flight %d\n", i)
	}
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&page, "- [ ] ready %d\n", i)
	}
	root := primeInstance(t, map[string]string{"backlog.md": page.String()})
	pack, err := Prime(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	tasks, ok := pack.Section("Tasks")
	if !ok {
		t.Fatal("no task section")
	}
	count := 0
	for _, line := range strings.Split(tasks.Body, "\n") {
		if strings.Contains(line, "[ ]") || strings.Contains(line, "[/]") {
			count++
		}
	}
	if count != primeTaskLines {
		t.Errorf("task section has %d task lines, want the cap %d:\n%s", count, primeTaskLines, tasks.Body)
	}
	if !strings.Contains(tasks.Body, "in flight 7") {
		t.Errorf("in-progress work lost its claim on the cap:\n%s", tasks.Body)
	}
	if strings.Contains(tasks.Body, "ready 2\n") {
		t.Errorf("ready work crowded out in-progress work:\n%s", tasks.Body)
	}
}

// The budget is a promise: whatever fits, the assembled pack does not exceed it
// once the non-degrading sections fit at all.
func TestPrimeRespectsBudget(t *testing.T) {
	root := primeInstance(t, nil)
	base, err := Prime(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	fixed := 0
	for _, s := range base.Sections {
		if s.Title != "Tree" {
			fixed += estTokens(s.Body)
		}
	}
	for budget := fixed + 40; budget <= base.EstTokens+50; budget += 13 {
		pack, err := Prime(root, budget)
		if err != nil {
			t.Fatal(err)
		}
		if pack.EstTokens != estTokens(pack.Text) {
			t.Fatalf("budget %d: reported %d tokens for %d", budget, pack.EstTokens, estTokens(pack.Text))
		}
		if pack.EstTokens > budget {
			t.Fatalf("budget %d: pack is %d estimated tokens:\n%s", budget, pack.EstTokens, pack.Text)
		}
	}
}

// Under pressure the tree degrades and everything else stays whole — a truncated
// task line or a cut contract pointer is worse than a shallower tree.
func TestPrimeDegradesTreeNotOtherSections(t *testing.T) {
	root := primeInstance(t, nil)
	full, err := Prime(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if full.Tree.Tier != TreeTierFull {
		t.Fatalf("the default budget did not hold the full tree (tier %q)", full.Tree.Tier)
	}

	tight, err := Prime(root, full.EstTokens-20)
	if err != nil {
		t.Fatal(err)
	}
	if tight.Tree.Tier == TreeTierFull {
		t.Fatalf("a smaller budget kept the full tree at %d tokens", tight.EstTokens)
	}
	for _, title := range []string{"Tasks", "Recent journal", "Pointers"} {
		want, _ := full.Section(title)
		got, ok := tight.Section(title)
		if !ok {
			t.Fatalf("%q section vanished under a tighter budget:\n%s", title, tight.Text)
		}
		if got.Body != want.Body {
			t.Errorf("%q section changed under a tighter budget:\n%s\n---\n%s", title, got.Body, want.Body)
		}
	}
	if tight.Tree.Tier != "" && !strings.Contains(tight.Text, tight.Tree.Note()) {
		t.Errorf("degraded tree did not say it was degraded:\n%s", tight.Text)
	}

	// Squeezed to where not even the tree's floor fits, the tree goes entirely
	// rather than pushing the pack over its budget.
	starved, err := Prime(root, estTokens(renderPrimeSections(full.Sections[:1]))+10)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := starved.Section("Tree"); ok {
		t.Errorf("a tree section survived a budget that cannot hold it:\n%s", starved.Text)
	}
}

// A backlog with nothing actionable still gets its section: "there is a
// backlog and it is quiet" is different information from "there is no
// backlog", and only the second one means the agent should go make a plan.
func TestPrimeReportsAnIdleBacklog(t *testing.T) {
	root := primeInstance(t, map[string]string{
		"backlog.md": "---\ndescription: A backlog with nothing left.\nagentsfs_role: backlog\n---\n\n## Done\n- [x] Everything\n",
	})
	pack, err := Prime(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	tasks, ok := pack.Section("Tasks")
	if !ok {
		t.Fatalf("an idle backlog produced no task section:\n%s", pack.Text)
	}
	if !strings.Contains(tasks.Body, "nothing in progress or ready") {
		t.Errorf("idle task section = %q", tasks.Body)
	}
	if !strings.Contains(tasks.Body, "Full backlog: afs tasks") {
		t.Errorf("idle task section dropped the pointer to the page: %q", tasks.Body)
	}
}
