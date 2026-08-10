package core

import (
	"fmt"
	"sort"
	"strings"
)

// `afs prime` is the session orientation pack: the smallest read that lets an
// agent start working — what this instance is, what is in flight, what the tree
// looks like, what the last sessions did, and where to go next. It is assembled
// entirely from primitives that already exist (role resolution, the backlog
// parser, Tree, the journal's naming convention), so priming never becomes a
// second source of truth that can disagree with the files.
//
// Budget discipline: the pack is assembled against a token estimate (chars÷4,
// the same unit context packs budget in). Identity, tasks, journal, and
// pointers are budgeted FIRST and never truncate — half a task line or a cut
// contract pointer is worse than none. The tree takes what is left and degrades
// through the ladder in treebudget.go, because the tree is the one section that
// is still worth reading at a tenth of its size.
//
// Sections whose source is absent are skipped whole. An instance with no
// backlog page has no task section at all, rather than a heading over an
// apology: the pack is context to inject, and every line has to earn its place.

const (
	// defaultPrimeBudget is the RFC's default: enough for a real orientation,
	// small enough that an agent can afford to run it at every session start
	// without crowding the work. Prime is pull-based by contract: the
	// Orient-first section tells agents to run it; nothing injects it.
	defaultPrimeBudget = 4000

	// primeTaskLines caps the task section. Prime answers "what is in flight",
	// not "what is on the list"; `afs tasks` is one command away and says so.
	primeTaskLines = 10

	// primeJournalEntries is how many recent sessions the pack names — the same
	// "newest one or two" the contract tells a reader to open by hand.
	primeJournalEntries = 2

	// primeTreeOverhead reserves room for the tree section's heading and its
	// degradation note before the ladder runs, since those are written after the
	// budget is spent. Assembly re-checks the total and drops the tree outright
	// rather than overrun it, so the reserve only has to be roughly right.
	primeTreeOverhead = 30
)

// PrimeSection is one block of the pack. An empty Title marks the identity
// block, which leads the pack without a heading of its own.
type PrimeSection struct {
	Title string
	Body  string
}

// PrimePack is the assembled orientation pack. Text is what a caller prints;
// the rest is provenance — which sections made it in, and how far the tree had
// to degrade — so a caller (or a test) can say what the budget cost without
// re-parsing the text.
type PrimePack struct {
	Root      string
	Budget    int
	EstTokens int
	Text      string
	Sections  []PrimeSection
	Tree      BudgetedTree // zero Tier when no tree section fit the budget
}

// Section returns the named section and whether the pack has one.
func (p PrimePack) Section(title string) (PrimeSection, bool) {
	for _, s := range p.Sections {
		if s.Title == title {
			return s, true
		}
	}
	return PrimeSection{}, false
}

// Prime assembles the orientation pack for an instance. budget ≤ 0 uses
// defaultPrimeBudget. Errors are real errors: prime runs inside an instance,
// and a backlog page that claims the role but cannot be read is a broken
// instance, not an empty one.
func Prime(root string, budget int) (PrimePack, error) {
	if budget <= 0 {
		budget = defaultPrimeBudget
	}
	// One walk feeds both role resolution and the journal listing; Tree does its
	// own, and the ladder may render more than once, so there is no point
	// walking twice for the cheap parts.
	entries, err := ListEntries(root)
	if err != nil {
		return PrimePack{}, err
	}
	roles := resolveReservedFromEntries(root, entries)

	pack := PrimePack{Root: root, Budget: budget}

	// Sections that never degrade, in final order except the tree, which is
	// budgeted last and inserted after the tasks.
	head := []PrimeSection{{Body: primeIdentity(root)}}
	backlog, found, err := loadBacklogFromEntries(root, entries, roles)
	if err != nil {
		return PrimePack{}, err
	}
	if found {
		head = append(head, PrimeSection{Title: "Tasks", Body: primeTasks(backlog)})
	}
	var tail []PrimeSection
	if journal := primeJournal(root, entries, roles.Journal); journal != "" {
		tail = append(tail, PrimeSection{Title: "Recent journal", Body: journal})
	}
	tail = append(tail, PrimeSection{Title: "Pointers", Body: primePointers()})

	fixed := append(append([]PrimeSection{}, head...), tail...)
	remaining := budget - estTokens(renderPrimeSections(fixed)) - primeTreeOverhead

	sections := fixed
	if remaining > 0 {
		tree, err := TreeWithinBudget(root, ".", remaining)
		if err != nil {
			return PrimePack{}, err
		}
		if tree.Fits && tree.EstTokens <= remaining {
			pack.Tree = tree
			sections = append(append(append([]PrimeSection{}, head...),
				PrimeSection{Title: "Tree", Body: primeTree(tree)}), tail...)
		}
	}

	text := renderPrimeSections(sections)
	// Last guard: if the reserve was too optimistic the tree goes, not a
	// truncated section. Everything else was budgeted before it was written.
	if estTokens(text) > budget && pack.Tree.Tier != "" {
		pack.Tree = BudgetedTree{}
		sections = fixed
		text = renderPrimeSections(sections)
	}
	pack.Sections, pack.Text, pack.EstTokens = sections, text, estTokens(text)
	return pack, nil
}

// RenderPrimePack is the pack as plain markdown-ish text, for direct injection
// into a session. It is what Prime already put in Text; the function exists so
// callers can re-render a pack they assembled or transported.
func RenderPrimePack(p PrimePack) string {
	if p.Text != "" {
		return p.Text
	}
	return renderPrimeSections(p.Sections)
}

func renderPrimeSections(sections []PrimeSection) string {
	var b strings.Builder
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		if s.Title != "" {
			fmt.Fprintf(&b, "## %s\n", s.Title)
		}
		b.WriteString(strings.TrimRight(s.Body, "\n"))
		b.WriteString("\n")
	}
	return b.String()
}

// primeIdentity is who this instance is and which contract governs writing to
// it. The description comes from the root INDEX.md (never AGENTS.md's generic
// "this is an agentsfs" text — DirDescription already knows that order), and
// the contract version is read from the instance rather than assumed from this
// binary: the pack describes the instance in front of it, not the one this afs
// would create.
func primeIdentity(root string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# AgentsFS: %s\n", root)
	if desc := DirDescription(root, "."); desc != "" {
		b.WriteString(desc + "\n")
	}
	switch version := ContractVersion(root); {
	case version != "":
		fmt.Fprintf(&b, "Contract: agentsfs %s — read AGENTS.md before writing.\n", version)
	case fileExists(joinRel(root, "AGENTS.md")):
		// No declared version (a hand-made or pre-contract instance): the pointer
		// still holds, and it is the pointer that matters.
		b.WriteString("Contract: read AGENTS.md before writing.\n")
	}
	return b.String()
}

// primeTasks is the top of the backlog: what is being worked (resumed, not
// re-claimed), what is waiting on the reader, and what is ready to pull, capped
// at primeTaskLines between them. In-progress work is listed first and takes the
// cap first — an agent that abandons a half-finished task to start a fresh one
// is the failure this ordering exists to prevent. Owner-blocked questions come
// next, ahead of ready work: they cost the reader one line each and unblock
// tasks nothing else can move.
func primeTasks(b *Backlog) string {
	var out strings.Builder
	lines := 0
	if inProgress := b.InProgress(); len(inProgress) > 0 {
		out.WriteString("In progress\n")
		for _, t := range inProgress {
			if lines >= primeTaskLines {
				break
			}
			fmt.Fprintf(&out, "  %s\n", TaskLine(t))
			lines++
		}
	}
	if waiting := b.OwnerBlocked(); len(waiting) > 0 && lines < primeTaskLines {
		out.WriteString("Waiting on you\n")
		for _, t := range waiting {
			if lines >= primeTaskLines {
				break
			}
			fmt.Fprintf(&out, "  %s\n", TaskLine(t))
			lines++
		}
	}
	if ready := b.ReadyTasks(); len(ready) > 0 && lines < primeTaskLines {
		out.WriteString("Ready\n")
		for _, t := range ready {
			if lines >= primeTaskLines {
				break
			}
			// The band is the priority, so it leads the line: a reader scanning
			// this section is choosing what to pull, and Now/Next/Later is the
			// answer to that question.
			fmt.Fprintf(&out, "  %-5s %s\n", t.Band, TaskLine(t))
			lines++
		}
	}
	if lines == 0 {
		out.WriteString("(nothing in progress or ready)\n")
	}
	out.WriteString("Full backlog: afs tasks\n")
	return out.String()
}

func primeTree(t BudgetedTree) string {
	body := strings.TrimRight(t.Text, "\n")
	if note := t.Note(); note != "" {
		body += "\n" + note
	}
	return body + "\n"
}

// primeJournal names the newest journal entries and what each was about. The
// directory comes from resolved roles — the contract owns that name and has
// renamed it before — and "newest" is the lexically greatest filename, which the
// contract's YYYY-MM-DDTHHMMSSZ-… naming makes chronological without reading,
// or trusting, a timestamp inside any file.
func primeJournal(root string, entries []Entry, journalDir string) string {
	if journalDir == "" {
		return ""
	}
	var names []string
	for _, e := range entries {
		if e.IsDir || !strings.HasPrefix(e.Rel, journalDir+"/") || !isMarkdown(e.Rel) {
			continue
		}
		if strings.EqualFold(baseName(e.Rel), "INDEX.md") {
			continue // the journal's own conventions page, not a session
		}
		names = append(names, e.Rel)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) > primeJournalEntries {
		names = names[:primeJournalEntries]
	}
	var b strings.Builder
	for _, rel := range names {
		if desc := Description(joinRel(root, rel)); desc != "" {
			fmt.Fprintf(&b, "%s — %s\n", rel, desc)
			continue
		}
		fmt.Fprintf(&b, "%s\n", rel)
	}
	return b.String()
}

// primePointers is deliberately one line: the pack ends by naming the two
// commands that expand it, not by teaching the toolkit.
func primePointers() string {
	return "`afs docs agent-start` for the full primer; `afs search \"<words>\"` to retrieve from this memory.\n"
}
