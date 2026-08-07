package core

import (
	"strings"
	"testing"
)

// deepInstance is a tree with enough depth and enough description text that the
// ladder has somewhere to fall: four levels, every directory and file
// described, so each tier is measurably cheaper than the one above it.
func deepInstance(t *testing.T) string {
	t.Helper()
	desc := func(s string) string { return "---\ndescription: " + s + "\n---\nbody\n" }
	return newInstance(t, map[string]string{
		"INDEX.md":                       desc("Working memory for a project with several layers of structure."),
		"top.md":                         desc("A note at the root of the instance."),
		"projects/INDEX.md":              desc("Active efforts with an end state."),
		"projects/hub/INDEX.md":          desc("The embedded Hub sync work, projection through publication."),
		"projects/hub/status.md":         desc("Where the Hub sync work stands right now."),
		"projects/hub/design/INDEX.md":   desc("Design notes for the projection algorithm."),
		"projects/hub/design/cas.md":     desc("Compare-and-swap semantics for the projection commit."),
		"reference/INDEX.md":             desc("Stable facts and documents worth keeping."),
		"reference/beads.md":             desc("Comparative study of Beads against AgentsFS primitives."),
		"reference/vendors/INDEX.md":     desc("Third-party tools evaluated for this project."),
		"reference/vendors/tracker.md":   desc("Notes on the tracker we did not adopt and why."),
		"reference/vendors/storage.md":   desc("Notes on object storage options for the hosted layer."),
		"agent-journal/INDEX.md":         "---\ndescription: Session journal.\nagentsfs_role: journal\n---\n",
		"agent-journal/2026-01-01.md":    desc("An early session."),
		"agent-journal/2026-02-02Z-b.md": desc("A later session that closed out the parser work."),
	})
}

// A budget that comfortably holds the whole tree must not degrade it at all —
// the ladder is a response to pressure, not a reformatting pass.
func TestTreeWithinBudgetFullWhenItFits(t *testing.T) {
	root := deepInstance(t)
	full, err := Tree(root, ".", 0)
	if err != nil {
		t.Fatal(err)
	}
	bt, err := TreeWithinBudget(root, ".", 100000)
	if err != nil {
		t.Fatal(err)
	}
	if bt.Tier != TreeTierFull || bt.Degraded() {
		t.Fatalf("tier = %q (degraded %v), want the full tree", bt.Tier, bt.Degraded())
	}
	if bt.Text != full {
		t.Errorf("budgeted full tree differs from afs tree output:\n%s\n---\n%s", bt.Text, full)
	}
	if bt.Note() != "" {
		t.Errorf("full tree carried a degradation note: %q", bt.Note())
	}
}

// A budget of zero or less means "no budget", not "no room": a caller that has
// no ceiling gets the whole tree rather than the floor.
func TestTreeWithinBudgetNoBudgetIsFull(t *testing.T) {
	root := deepInstance(t)
	for _, budget := range []int{0, -1} {
		bt, err := TreeWithinBudget(root, ".", budget)
		if err != nil {
			t.Fatal(err)
		}
		if bt.Tier != TreeTierFull {
			t.Errorf("budget %d: tier = %q, want full", budget, bt.Tier)
		}
	}
}

// The rungs in order: full → depth-capped → names → root description. Each
// budget is derived from a real rendering rather than guessed, so the test
// asserts the ladder's behavior and not a particular fixture's byte count.
func TestTreeWithinBudgetLadder(t *testing.T) {
	root := deepInstance(t)
	full, err := Tree(root, ".", 0)
	if err != nil {
		t.Fatal(err)
	}
	depth1, err := Tree(root, ".", 1)
	if err != nil {
		t.Fatal(err)
	}
	names := stripTreeAnnotations(depth1)

	// Just under the full tree: something has to give, but there is plenty of
	// room for a capped tree that still carries descriptions.
	bt, err := TreeWithinBudget(root, ".", estTokens(full)-1)
	if err != nil {
		t.Fatal(err)
	}
	if bt.Tier != TreeTierDepth {
		t.Fatalf("tier just under the full tree = %q, want depth-capped", bt.Tier)
	}
	if bt.Depth < 1 || bt.EstTokens > estTokens(full)-1 {
		t.Errorf("depth-capped tier depth=%d est=%d budget=%d", bt.Depth, bt.EstTokens, estTokens(full)-1)
	}
	if !strings.Contains(bt.Note(), "capped to depth") {
		t.Errorf("depth-capped note does not say what happened: %q", bt.Note())
	}

	// Exactly the size of the names-only rendering: descriptions cannot fit at
	// any depth, names can.
	bt, err = TreeWithinBudget(root, ".", estTokens(names))
	if err != nil {
		t.Fatal(err)
	}
	if bt.Tier != TreeTierNames {
		t.Fatalf("tier at the names-only size = %q, want names", bt.Tier)
	}
	if strings.Contains(bt.Text, " — ") {
		t.Errorf("names-only tier still carries descriptions:\n%s", bt.Text)
	}
	if !strings.Contains(bt.Text, "…") {
		t.Errorf("names-only tier dropped the hidden-children marker:\n%s", bt.Text)
	}

	// One token: nothing fits, and the floor is returned anyway — labelled as
	// not fitting, because a caller assembling a pack has to be able to drop it.
	bt, err = TreeWithinBudget(root, ".", 1)
	if err != nil {
		t.Fatal(err)
	}
	if bt.Tier != TreeTierRoot {
		t.Fatalf("tier at budget 1 = %q, want root-description", bt.Tier)
	}
	if bt.Fits {
		t.Errorf("root-description tier claimed to fit budget 1 at %d tokens", bt.EstTokens)
	}
	if lines := strings.Count(strings.TrimRight(bt.Text, "\n"), "\n") + 1; lines != 1 {
		t.Errorf("root-description tier is %d lines:\n%s", lines, bt.Text)
	}
	if !strings.HasPrefix(bt.Text, firstLine(full)) {
		t.Errorf("root-description tier is not the tree's own root line:\n%s", bt.Text)
	}
}

// Whatever tier is chosen, a fitting result must actually fit: the estimate the
// ladder reports is the one the caller budgets against.
func TestTreeWithinBudgetNeverOverrunsWhenItFits(t *testing.T) {
	root := deepInstance(t)
	for budget := 5; budget < 400; budget += 7 {
		bt, err := TreeWithinBudget(root, ".", budget)
		if err != nil {
			t.Fatal(err)
		}
		if bt.EstTokens != estTokens(bt.Text) {
			t.Fatalf("budget %d: reported %d tokens for a %d-token rendering", budget, bt.EstTokens, estTokens(bt.Text))
		}
		if bt.Fits && bt.EstTokens > budget {
			t.Fatalf("budget %d: tier %q claims to fit at %d tokens", budget, bt.Tier, bt.EstTokens)
		}
	}
}

// The ladder scopes like Tree does, so an agent can budget one subtree.
func TestTreeWithinBudgetHonorsScope(t *testing.T) {
	root := deepInstance(t)
	bt, err := TreeWithinBudget(root, "projects", 100000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bt.Text, "projects — ") {
		t.Fatalf("scoped tree did not start at the scope:\n%s", bt.Text)
	}
	if strings.Contains(bt.Text, "reference") {
		t.Errorf("scoped tree leaked a sibling subtree:\n%s", bt.Text)
	}
}

// scopeDepth is what bounds the depth search: it must count rendered levels,
// which excludes the INDEX.md files Tree never lists.
func TestScopeDepthCountsRenderedLevels(t *testing.T) {
	root := deepInstance(t)
	got, err := scopeDepth(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	// projects/hub/design/cas.md is four rendered levels below the root; the
	// INDEX.md beside it is a fifth path segment that Tree never prints.
	if got != 4 {
		t.Errorf("scopeDepth(.) = %d, want 4", got)
	}
	got, err = scopeDepth(root, "reference")
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 { // reference/vendors/tracker.md, relative to the scope
		t.Errorf("scopeDepth(reference) = %d, want 2", got)
	}
}

func TestStripTreeAnnotationsKeepsGlyphsAndHiddenChildrenMarker(t *testing.T) {
	in := ". — Root description  [3d ago]\n" +
		"├── note.md — A note — with an em dash  [<1h ago]\n" +
		"└── dir/ — A directory  [2w ago] …\n"
	want := ".\n├── note.md\n└── dir/ …\n"
	if got := stripTreeAnnotations(in); got != want {
		t.Errorf("stripTreeAnnotations =\n%q\nwant\n%q", got, want)
	}
}
