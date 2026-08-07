package core

import (
	"fmt"
	"strings"
)

// The tree degradation ladder: the richest rendering of an instance that fits a
// token budget. `afs tree` answers "show me everything"; a budgeted caller —
// prime, a session-start hook, an agent with 800 tokens to spare — needs the
// most informative view that still fits, and needs to be told what it lost.
//
// The ladder is the RFC's, richest first:
//
//	full tree with descriptions
//	→ depth-capped with descriptions, decreasing depth
//	→ depth-1 names only
//	→ the root description alone
//
// Every tier is rendered by Tree itself rather than by a second walker, so a
// budgeted tree is always the same tree the agent would get from `afs tree` —
// just less of it. The estimator is the context pack's chars÷4 (estTokens): one
// budgeting unit across every surface, no model-specific tokenizer.
type TreeTier string

const (
	TreeTierFull  TreeTier = "full"             // whole tree, every description
	TreeTierDepth TreeTier = "depth-capped"     // descriptions kept, depth capped
	TreeTierNames TreeTier = "names"            // top level only, names alone
	TreeTierRoot  TreeTier = "root-description" // the floor: one line
)

// BudgetedTree is one rendering plus which rung of the ladder produced it.
// Callers print Text and, when the view is degraded, Note() — an agent that
// cannot tell a capped tree from the whole instance will conclude the missing
// directories do not exist.
type BudgetedTree struct {
	Text      string
	Tier      TreeTier
	Depth     int // effective depth cap; 0 when the tier does not cap depth
	EstTokens int
	Budget    int  // 0 when the caller set no budget
	Fits      bool // false only at the floor tier, which is returned regardless
}

// Degraded reports whether anything was dropped to fit the budget.
func (t BudgetedTree) Degraded() bool { return t.Tier != TreeTierFull }

// Note is the one-line explanation of what was dropped, or "" for a full tree.
// It names the escape hatch, because the reader's next question is always "how
// do I see the rest".
func (t BudgetedTree) Note() string {
	switch t.Tier {
	case TreeTierDepth:
		return fmt.Sprintf("(tree capped to depth %d to fit the budget — `afs tree` for the full tree)", t.Depth)
	case TreeTierNames:
		return "(tree reduced to top-level names to fit the budget — `afs tree` for descriptions and depth)"
	case TreeTierRoot:
		return "(tree reduced to the root description to fit the budget — `afs tree` for the full tree)"
	default:
		return ""
	}
}

// TreeWithinBudget renders the instance (or the subdir scope, same as Tree)
// within an estimated-token budget, returning the richest tier that fits. A
// budget ≤ 0 means "no budget" and always yields the full tree — a caller that
// wants a default passes its own (prime's defaultPrimeBudget), because there is
// no sensible universal ceiling on a tree.
//
// The floor tier is returned even when it does not fit: a budget too small for
// one line is a budget too small for orientation, and answering with something
// labelled (Fits=false) beats answering with nothing.
func TreeWithinBudget(root, subdir string, budget int) (BudgetedTree, error) {
	full, err := Tree(root, subdir, 0)
	if err != nil {
		return BudgetedTree{}, err
	}
	if budget <= 0 || estTokens(full) <= budget {
		return BudgetedTree{
			Text: full, Tier: TreeTierFull, EstTokens: estTokens(full), Budget: budget, Fits: true,
		}, nil
	}

	// Depth-capped tiers. Output grows monotonically with the cap, so the
	// "decreasing depth" ladder is a binary search rather than a scan: each
	// attempt re-renders (and re-reads git history for freshness), so a deep
	// instance costs log₂(depth) renders instead of one per level. The
	// unlimited render above already failed, so the deepest cap worth trying is
	// one level shallower than the deepest entry.
	deepest, err := scopeDepth(root, subdir)
	if err != nil {
		return BudgetedTree{}, err
	}
	best, bestDepth := "", 0
	for lo, hi := 1, deepest-1; lo <= hi; {
		mid := (lo + hi) / 2
		out, err := Tree(root, subdir, mid)
		if err != nil {
			return BudgetedTree{}, err
		}
		if estTokens(out) <= budget {
			best, bestDepth = out, mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best != "" {
		return BudgetedTree{
			Text: best, Tier: TreeTierDepth, Depth: bestDepth,
			EstTokens: estTokens(best), Budget: budget, Fits: true,
		}, nil
	}

	// Names only: the same top level, with the descriptions and freshness
	// stamps — the bulk of the line — stripped.
	top, err := Tree(root, subdir, 1)
	if err != nil {
		return BudgetedTree{}, err
	}
	names := stripTreeAnnotations(top)
	if estTokens(names) <= budget {
		return BudgetedTree{
			Text: names, Tier: TreeTierNames, Depth: 1,
			EstTokens: estTokens(names), Budget: budget, Fits: true,
		}, nil
	}

	// The floor: the scope's own line, which every richer tier already starts
	// with, so this is a truncation of the full render rather than a new format.
	rootLine := firstLine(full) + "\n"
	return BudgetedTree{
		Text: rootLine, Tier: TreeTierRoot,
		EstTokens: estTokens(rootLine), Budget: budget, Fits: estTokens(rootLine) <= budget,
	}, nil
}

// scopeDepth is how many levels deep the rendered tree goes below scope — the
// only depth cap above which Tree's output stops changing. INDEX.md files are
// excluded because Tree never lists them (their description annotates the
// directory line), so counting them would waste a search step on a cap that
// renders identically to the one below it.
func scopeDepth(root, subdir string) (int, error) {
	entries, err := ListEntries(root)
	if err != nil {
		return 0, err
	}
	scope := normalizeScope(subdir)
	deepest := 0
	for _, e := range entries {
		rel := e.Rel
		if scope != "." {
			if !strings.HasPrefix(rel, scope+"/") {
				continue
			}
			rel = strings.TrimPrefix(rel, scope+"/")
		}
		if !e.IsDir && strings.EqualFold(baseName(rel), "INDEX.md") {
			continue
		}
		if d := strings.Count(rel, "/") + 1; d > deepest {
			deepest = d
		}
	}
	return deepest, nil
}

// stripTreeAnnotations reduces rendered tree lines to names and glyphs.
// annotate() appends " — <description>" and "  [<age>]" after the entry name,
// in that order, so cutting at the first of those markers leaves the name and
// its tree connectors untouched. A description containing " — " survives (the
// cut is at the first occurrence); a file NAME containing " — " or "  [" would
// be shortened, which is the price of not maintaining a second tree walker for
// one tier.
func stripTreeAnnotations(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		// " …" flags a directory whose children the depth cap hid. It is the one
		// piece of information this tier must not drop — without it a truncated
		// top level reads as a complete instance.
		more := strings.HasSuffix(line, " …")
		name := line
		if j := strings.Index(name, " — "); j >= 0 {
			name = name[:j]
		} else if j := strings.Index(name, "  ["); j >= 0 {
			name = name[:j]
		}
		if more && !strings.HasSuffix(name, " …") {
			name += " …"
		}
		lines[i] = name
	}
	return strings.Join(lines, "\n")
}
