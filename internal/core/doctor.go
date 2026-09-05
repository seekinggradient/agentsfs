package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Finding is one health problem doctor identified. Doctor is deterministic —
// no LLM — and its output is the worklist the gardener consumes.
//
// Severity is advice about urgency, not a verdict on the knowledge:
//
//   - "error" — the instance is structurally ambiguous and tooling cannot
//     behave predictably until a human decides (two directories claiming one
//     reserved role). `afs doctor` exits non-zero only for these.
//   - "warn"  — a real deviation from the contract that a gardener should fix:
//     a missing description, a dead link, a stale note. Normal in a knowledge
//     base being actively written; never a reason to fail a command.
//   - "info"  — worth a look, no action implied.
//
// The bias is deliberate: a workspace mid-growth legitimately contains
// forward-referencing links and half-written notes, and a tool that treats
// those as errors trains people to ignore it. Reserve "error" for genuine
// ambiguity, and let everything else be a worklist.
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path"`
	Message  string `json:"message"`
}

// RootDescriptionPlaceholder is the description the template ships in a fresh
// instance's root INDEX.md. It is deliberately a demand, not a value: doctor
// flags it until an agent replaces it with a real description of this instance.
const RootDescriptionPlaceholder = "REPLACE ME: one or two sentences describing what THIS workspace is about and what lives in it."

// legacyRootDescriptionSignature is a stable fragment of the pre-0.7.0 template
// boilerplate ("Self-describing root of this agentsfs. Read this first …") that
// served as the root description before it moved to the root INDEX.md. Doctor
// detects it too, so an instance whose per-workspace description is still that
// boilerplate (e.g. copied into the new INDEX.md) gets the same nudge —
// covering both the old and new template phrasings.
const legacyRootDescriptionSignature = "Self-describing root of this agentsfs"

// IsPlaceholderRootDescription reports whether a root description is still an
// unhelpful template default — the current placeholder (matched by its stable
// "REPLACE ME" prefix, so later wording tweaks still trip it) or the legacy
// boilerplate. It is the signal for the root-description finding, and the Hub
// uses it to avoid surfacing a placeholder as a repo's label.
func IsPlaceholderRootDescription(desc string) bool {
	d := strings.TrimSpace(desc)
	return strings.HasPrefix(d, "REPLACE ME") || strings.Contains(d, legacyRootDescriptionSignature)
}

// Doctor checks instance health. The scratch dir is exempt from everything
// (mess is legal there); the root contract files are exempt from link checks
// (their example links are teaching material). Reserved directories (journal,
// scratch) are resolved by their INDEX.md `agentsfs_role:` marker, falling
// back to the classic names journal/ and scratch/ when nothing is marked
// (contract 0.4.0). A directory marked `agentsfs_role: collection` describes its
// contents collectively: every entry strictly below it is exempt from per-entry
// findings, and links sourced there raise no link findings — but the collection
// stays fully indexed and durable, and its own INDEX.md is checked normally
// (contract 0.6.0).
func Doctor(root string) ([]Finding, error) {
	entries, err := ListEntries(root)
	if err != nil {
		return nil, err
	}
	links, err := ScanLinks(root)
	if err != nil {
		return nil, err
	}
	idx, err := BuildNameIndex(root)
	if err != nil {
		return nil, err
	}
	roles := resolveReservedFromEntries(root, entries)
	inScratch := func(rel string) bool { return inRoleDir(rel, roles.Scratch) }
	inJournal := func(rel string) bool { return inRoleDir(rel, roles.Journal) }
	// A collection describes its contents collectively: everything strictly
	// below a collection dir is exempt from per-entry findings (missing-index,
	// missing/undescribed-file, stub, orphan) and link findings sourced there.
	// The collection dir's own INDEX.md is checked by the ordinary rules.
	inCollection := func(rel string) bool { return belowAnyCollection(rel, roles.Collections) }

	var findings []Finding
	add := func(sev, code, path, msg string) {
		findings = append(findings, Finding{sev, code, path, msg})
	}

	// Reserved-role health. Two dirs marked for one role is an error — a role
	// must have exactly one home. No journal at all is an info nudge.
	for _, dup := range roles.DuplicateJournal {
		add("error", "duplicate-role", dup, fmt.Sprintf("multiple directories declare agentsfs_role: journal (%s) — a role must have exactly one home; keep the marker on one", strings.Join(roles.DuplicateJournal, ", ")))
	}
	for _, dup := range roles.DuplicateScratch {
		add("error", "duplicate-role", dup, fmt.Sprintf("multiple directories declare agentsfs_role: scratch (%s) — a role must have exactly one home; keep the marker on one", strings.Join(roles.DuplicateScratch, ", ")))
	}
	for _, dup := range roles.DuplicateBacklog {
		add("error", "duplicate-backlog", dup, fmt.Sprintf("multiple directories or pages declare agentsfs_role: backlog (%s) — a role must have exactly one home; keep the marker on one", strings.Join(roles.DuplicateBacklog, ", ")))
	}
	if roles.Journal == "" {
		add("info", "no-journal", ".", "no session journal declared — create agent-journal/ or mark a directory with agentsfs_role: journal")
	}

	if got := ContractVersion(root); got == "" {
		add("warn", "contract-version", "AGENTS.md", "missing agentsfs_contract version; run `afs contract status`")
	} else if cur := CurrentContractVersion(); compareVersions(got, cur) < 0 {
		add("warn", "contract-version", "AGENTS.md", fmt.Sprintf("contract version %s is older than bundled %s; run `afs contract upgrade`", got, cur))
	} else if compareVersions(got, cur) > 0 {
		// The instance is on a newer contract than this binary knows.
		// `afs contract upgrade` here would DOWNGRADE it — tell the agent
		// to update afs itself instead, never to upgrade the contract.
		add("warn", "contract-version", "AGENTS.md", fmt.Sprintf("contract version %s is newer than this afs's bundled %s; run `afs update` — do not run `afs contract upgrade`, it would downgrade this instance", got, cur))
	}

	// The root describes itself through its own INDEX.md — kept out of the
	// contract-managed AGENTS.md so upgrades never rewrite it. A missing root
	// INDEX.md (older instances predate it) or a description left at the
	// template placeholder / pre-0.7.0 boilerplate propagates a meaningless
	// label to every surface that lists instances (the Hub, `afs tree`,
	// `afs status --json`, agent orientation), so doctor nudges until it is
	// real.
	if rootIndex := joinRel(root, "INDEX.md"); !fileExists(rootIndex) {
		add("warn", "root-index", ".", "no root INDEX.md — this workspace has no per-instance description; run `afs contract upgrade` to create one, then describe what this instance holds")
	} else {
		switch desc := strings.TrimSpace(Description(rootIndex)); {
		case desc == "":
			add("warn", "root-description", "INDEX.md", "root INDEX.md has no description: — set it to what this workspace is about and what lives in it")
		case IsPlaceholderRootDescription(desc):
			add("warn", "root-description", "INDEX.md", "root INDEX.md description is still the template placeholder — replace it with what this workspace is actually about")
		}
	}

	// Per-directory INDEX presence and per-file descriptions.
	indexBodies := map[string]string{} // dir → lowercased INDEX.md content
	for _, e := range entries {
		if !e.IsDir || inScratch(e.Rel) {
			continue
		}
		idxPath := joinRel(root, e.Rel+"/INDEX.md")
		if data, err := os.ReadFile(idxPath); err == nil {
			indexBodies[e.Rel] = strings.ToLower(string(data))
		} else if !inCollection(e.Rel) {
			// A directory inside a collection is described collectively — it
			// needs no INDEX.md of its own.
			add("warn", "missing-index", e.Rel, "directory has no INDEX.md describing it")
		}
	}
	// The root describes itself through both AGENTS.md (the contract) and its
	// own INDEX.md (the per-workspace description and any listing of files that can't
	// describe themselves); a root-dir file mentioned in either counts.
	var rootIndexBody strings.Builder
	if data, err := os.ReadFile(joinRel(root, "AGENTS.md")); err == nil {
		rootIndexBody.Write([]byte(strings.ToLower(string(data))))
	}
	if data, err := os.ReadFile(joinRel(root, "INDEX.md")); err == nil {
		rootIndexBody.WriteByte('\n')
		rootIndexBody.Write([]byte(strings.ToLower(string(data))))
	}
	indexBodies["."] = rootIndexBody.String()

	for _, e := range entries {
		if e.IsDir || inScratch(e.Rel) || inCollection(e.Rel) {
			continue // collection contents are described collectively by its INDEX
		}
		if e.Rel == "INDEX.md" {
			continue // the root INDEX.md is handled by the dedicated root check above
		}
		base := baseName(e.Rel)
		if strings.HasPrefix(base, ".") {
			continue // machine files (.gitattributes etc.) describe nothing
		}
		if isMarkdown(e.Rel) {
			path := joinRel(root, e.Rel)
			switch problem := FrontmatterProblem(path); {
			case !isReadable(path):
				// Report the real problem. Without this the file lands in the
				// missing-description bucket, sending the reader to add a
				// description to something they cannot even open.
				add("warn", "unreadable", e.Rel, "listed in the tree but cannot be read — check permissions, or replace a dangling link with the real file")
			case problem != "":
				// afs parses frontmatter with a real YAML parser, so it can now
				// flag exactly what Obsidian and the Hub reject — an unclosed
				// fence, or a block that is not valid YAML — instead of silently
				// reading past it. afs still extracts what it can (see
				// FrontmatterValueFromReader), so this is a fix-me nudge, not a
				// loss of the description.
				add("warn", "malformed-frontmatter", e.Rel, problem+" — every stricter reader (a YAML parser, Obsidian, the Hub) will disagree with what afs salvaged; fix the frontmatter")
			case Description(path) == "":
				add("warn", "missing-description", e.Rel, "markdown file has no description: frontmatter")
			}
		} else {
			// Non-markdown files must be described in their directory's INDEX.md.
			if !indexMentions(indexBodies[parentOf(e.Rel)], base) {
				add("warn", "undescribed-file", e.Rel, "not mentioned in its directory's INDEX.md (binaries can't describe themselves)")
			}
		}
	}

	// Journal backlog: the gardener empties the journal into durable notes.
	// A pile-up (many entries, or a stale oldest one) means it isn't keeping up.
	findings = append(findings, journalBacklog(root, entries, roles.Journal)...)

	// Backlog health: the task grammar's identifiers and edges must resolve, or
	// derived views (ready work, blockers) quietly mislead.
	findings = append(findings, backlogFindings(root, entries, roles)...)

	// Symlinks break the substrate's core promise — that the files ARE the
	// knowledge and `git clone` is the exit ramp. Git stores the link, not the
	// content, so a clone on another machine gets a dangling pointer.
	findings = append(findings, symlinkFindings(root, entries, inScratch)...)

	// Link health.
	linkedFiles := map[string]bool{}
	for _, l := range links {
		if isRootContract(l.Source) || inScratch(l.Source) {
			continue
		}
		matches := idx.ResolveLink(l)
		for _, m := range matches {
			// Resolution still runs for links sourced inside a collection so
			// backlinks and the orphan check see them — only the findings below
			// are suppressed.
			linkedFiles[m] = true
		}
		if inCollection(l.Source) {
			continue // collection contents are collectively described; no link findings
		}
		switch {
		case len(matches) == 0:
			add("warn", "dead-link", l.Source, fmt.Sprintf("line %d: [[%s]] resolves to no file", l.Line, l.Target))
		case len(matches) > 1:
			add("warn", "ambiguous-link", l.Source, fmt.Sprintf("line %d: [[%s]] matches %s — disambiguate with a path suffix", l.Line, l.Target, strings.Join(matches, ", ")))
		}
	}

	// Orphans and stubs: fragmentation's early warning signs. Journal
	// entries are episodic — legitimately short and unlinked — so they are
	// exempt here (but still need a description, checked above).
	for _, e := range entries {
		if e.IsDir || inScratch(e.Rel) || inJournal(e.Rel) || inCollection(e.Rel) || !isMarkdown(e.Rel) {
			continue // collection contents are collectively described — never stubs/orphans
		}
		base := baseName(e.Rel)
		if isRootContract(e.Rel) || strings.EqualFold(base, "INDEX.md") {
			continue
		}
		body, err := os.ReadFile(joinRel(root, e.Rel))
		if err == nil && len(strings.TrimSpace(stripFrontmatter(string(body)))) < 120 {
			add("warn", "stub", e.Rel, "nearly empty — expand it or consolidate it into a denser note")
		}
		if !linkedFiles[e.Rel] && !mentionedInOwnIndex(indexBodies, e.Rel) {
			add("info", "orphan", e.Rel, "no wikilinks point here and its directory's INDEX.md doesn't mention it")
		}
	}
	return findings, nil
}

// journalBacklog warns when journal/ has more than journalBacklogCount
// entries or its oldest entry is older than journalBacklogDays — either
// means the gardener hasn't folded entries into durable notes. Dates come
// from the same git-freshness source afs tree uses, with an mtime fallback
// for untracked files.
const (
	journalBacklogCount = 10
	journalBacklogDays  = 14
)

func journalBacklog(root string, entries []Entry, journalDir string) []Finding {
	if journalDir == "" {
		return nil // no journal resolved — nothing to back up
	}
	var oldest time.Time
	count := 0
	times, _ := gitLastTouchedTimes(root)
	for _, e := range entries {
		if e.IsDir || !inRoleDir(e.Rel, journalDir) || !isMarkdown(e.Rel) {
			continue
		}
		if strings.EqualFold(baseName(e.Rel), "INDEX.md") {
			continue
		}
		count++
		if t, ok := times[e.Rel]; ok && (oldest.IsZero() || t.Before(oldest)) {
			oldest = t
		}
	}
	if count == 0 {
		return nil
	}
	oldestDays := 0
	if !oldest.IsZero() {
		oldestDays = int(time.Since(oldest).Hours() / 24)
	}
	if count > journalBacklogCount || oldestDays > journalBacklogDays {
		msg := fmt.Sprintf("%d session note(s) pending consolidation (oldest %dd) — run the gardener to fold them into durable notes", count, oldestDays)
		return []Finding{{"warn", "journal-backlog", journalDir, msg}}
	}
	return nil
}

// backlogFindings checks the backlog's task grammar and, since contract 0.11.0,
// the shape of the directory around it: tickets that no line links, files that
// closed but never moved to the archive, delegations that point nowhere useful.
// Everything here is "warn" or "info" — they are contract deviations a gardener
// fixes, not the structural ambiguity that stops tooling from behaving
// predictably; the backlog still parses, and its derived views still render,
// they just carry a claim that doesn't hold. (Two homes claiming the role IS
// that ambiguity, and is reported as an error alongside the other duplicate-role
// findings.)
func backlogFindings(root string, entries []Entry, roles RoleDirs) []Finding {
	if roles.BacklogSpine == "" {
		return nil // no backlog declared — nothing to check
	}
	b, ok, err := loadBacklogFromEntries(root, entries, roles)
	if err != nil || !ok {
		// A page resolved but cannot be read. The tree walk reports the
		// unreadable file itself; don't say it twice.
		return nil
	}

	var out []Finding
	if roles.BacklogLegacy {
		out = append(out, Finding{"warn", "backlog-page-role-legacy", roles.BacklogSpine,
			"the backlog is a page-level agentsfs_role marker (contract 0.10.0); 0.11.0 makes it a directory whose INDEX.md is the spine — run `afs contract upgrade` to move it"})
	}

	tasks := b.Flat()
	byPage := map[string][]*Task{}
	for _, t := range tasks {
		byPage[t.Page] = append(byPage[t.Page], t)
	}
	pageIdx := NewNameIndex(b.Pages)

	// A slug is an identifier, so a repeat makes every [[#^slug]] pointing at it
	// ambiguous — the same failure ambiguous-link reports for file names. The
	// namespace is per page, so the check is too: two pages may each name a task
	// ^ship without either reference becoming ambiguous.
	knownSlugs := map[string]map[string]bool{}
	for _, page := range b.Pages {
		slugLines := map[string][]int{}
		var slugOrder []string
		for _, t := range byPage[page] {
			if t.Slug == "" {
				continue
			}
			if _, seen := slugLines[t.Slug]; !seen {
				slugOrder = append(slugOrder, t.Slug)
			}
			slugLines[t.Slug] = append(slugLines[t.Slug], t.Line)
		}
		known := map[string]bool{}
		for slug := range slugLines {
			known[slug] = true
		}
		knownSlugs[page] = known
		for _, slug := range slugOrder {
			if len(slugLines[slug]) > 1 {
				out = append(out, Finding{"warn", "duplicate-task-slug", page,
					fmt.Sprintf("^%s names %d tasks (lines %s) — a slug is an identifier; [[#^%s]] cannot say which one it means", slug, len(slugLines[slug]), formatLineList(slugLines[slug]), slug)})
			}
		}
	}

	for _, t := range tasks {
		for i, ref := range t.BlockedRefs {
			target := ""
			if i < len(t.blockedRefTargets) {
				target = t.blockedRefTargets[i]
			}
			page := resolveBlockerPage(t, target, byPage, pageIdx)
			if knownSlugs[page][ref] {
				continue
			}
			out = append(out, Finding{"warn", "dangling-task-ref", t.Page,
				fmt.Sprintf("line %d: blocked by [[%s#^%s]], which no task there defines — the block can never lift on its own", t.Line, target, ref)})
		}
		// Nesting is decomposition: a parent is complete only when its children
		// are. The parser never auto-flips a checkbox — the file is the source of
		// truth — so the contradiction is reported for a human to settle. Only
		// same-page nesting is counted here; the cross-file case is its own
		// finding, with its own fix.
		if open := openStructural(t); t.Status == TaskDone && open > 0 {
			out = append(out, Finding{"warn", "task-parent-inconsistent", t.Page,
				fmt.Sprintf("line %d: task is checked off but %d subtask(s) below it are still open or in progress", t.Line, open)})
		}
		if t.Status == TaskDone || t.Status == TaskDropped {
			if open := openDelegatedTasks(t); open > 0 {
				out = append(out, Finding{"warn", "delegation-terminal", t.Page,
					fmt.Sprintf("line %d: delegation is closed but the sub-backlog it links still has %d open task(s) — nothing releases them while the delegating line is terminal", t.Line, open)})
			}
		}
	}

	for _, page := range b.UndelegatedPages {
		out = append(out, Finding{"info", "sub-backlog-undelegated", page,
			"no task on the spine delegates to this sub-backlog, so nothing in it is ever ready — link it from a task, or leave it parked deliberately"})
	}

	return append(out, backlogFileFindings(root, entries, roles, tasks)...)
}

// backlogFileFindings checks the files AROUND the task grammar: ticket detail
// files and the archive. A ticket is earned by a task line, so a ticket no line
// links is either orphaned work or work that closed and never moved; an archived
// file, conversely, must be closed and unreferenced by live work.
//
// Reference resolution is the ordinary link-name one, restricted to files inside
// the backlog: [[backlog/voice-lanes]] and [[voice-lanes]] both name the ticket,
// and a link to a note elsewhere in the instance is not a ticket reference.
func backlogFileFindings(root string, entries []Entry, roles RoleDirs, tasks []*Task) []Finding {
	if roles.BacklogLegacy || roles.Backlog == "" {
		return nil // a page backlog has no directory to check
	}
	var tickets, archived, all []string
	for _, e := range entries {
		if e.IsDir || !isMarkdown(e.Rel) || !strings.HasPrefix(e.Rel, roles.Backlog+"/") {
			continue
		}
		if strings.EqualFold(baseName(e.Rel), "INDEX.md") {
			continue // a spine (or the archive collection's descriptor), not a ticket
		}
		all = append(all, e.Rel)
		if inBacklogArchive(e.Rel, roles.Backlog) {
			archived = append(archived, e.Rel)
			continue
		}
		tickets = append(tickets, e.Rel)
	}
	if len(all) == 0 {
		return nil
	}

	idx := NewNameIndex(all)
	refs := map[string]int{}     // file → how many task lines link it
	liveRefs := map[string]int{} // … of which are non-terminal
	for _, t := range tasks {
		if !strings.Contains(t.Text, "[[") {
			continue
		}
		live := t.Status != TaskDone && t.Status != TaskDropped
		for _, l := range ScanLinksIn(t.Page, t.Text) {
			for _, m := range idx.ResolveLink(l) {
				refs[m]++
				if live {
					liveRefs[m]++
				}
			}
		}
	}

	var out []Finding
	for _, rel := range tickets {
		switch {
		case refs[rel] == 0:
			out = append(out, Finding{"warn", "ticket-orphaned", rel,
				"no task links this ticket — link it from the spine line it belongs to, or archive it if its task closed"})
		case liveRefs[rel] == 0:
			// The RFC's ticket-unarchived is "the spine line is gone"; a deleted
			// line leaves nothing to detect it by, so the detectable half is
			// reported: every line that links this ticket has closed, and the
			// gardener's sweep has not moved it.
			out = append(out, Finding{"warn", "ticket-unarchived", rel,
				"every task linking this ticket is closed, but the ticket is still in the backlog — the archive sweep should move it and stamp closed:"})
		}
	}
	for _, rel := range archived {
		if liveRefs[rel] > 0 {
			out = append(out, Finding{"warn", "archive-live-ticket", rel,
				"an open task still links this archived file — reopen it out of the archive, or close the task"})
			continue
		}
		if archiveYearPageRe.MatchString(strings.ToLower(baseName(rel))) {
			continue // a per-year rollup page, not an archived ticket
		}
		if ClosedDate(joinRel(root, rel)) == "" {
			out = append(out, Finding{"warn", "archive-live-ticket", rel,
				"archived file has no closed: date — the sweep stamps one when it moves a ticket in"})
		}
	}
	return out
}

// openStructural counts open descendants reached by NESTING only — the
// same-page decomposition task-parent-inconsistent is about.
func openStructural(t *Task) int {
	n := 0
	for _, c := range t.Children {
		if c.Status == TaskOpen || c.Status == TaskInProgress {
			n++
		}
		n += openStructural(c)
	}
	return n
}

// openDelegatedTasks counts the open work on the sub-spines a task delegates to,
// transitively. It is openDescendants minus the same-page half.
func openDelegatedTasks(t *Task) int {
	n := 0
	for _, d := range t.Delegates {
		if d.Status == TaskOpen || d.Status == TaskInProgress {
			n++
		}
		n += openDescendants(d)
	}
	return n
}

func formatLineList(lines []int) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = strconv.Itoa(l)
	}
	return strings.Join(parts, ", ")
}

// symlinkFindings reports entries that are symbolic links. AgentsFS promises
// the files are the knowledge and `git clone` is the exit ramp; git records a
// symlink as a pointer, so cloning elsewhere yields a dangling reference and
// the "content" silently disappears. A link pointing outside the instance is
// the worse case — that content is not in the repository at all — but even an
// internal one duplicates identity and makes link resolution ambiguous.
//
// Scratch is exempt: mess is legal there.
func symlinkFindings(root string, entries []Entry, exempt func(string) bool) []Finding {
	var out []Finding
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	for _, e := range entries {
		if exempt(e.Rel) {
			continue
		}
		path := joinRel(root, e.Rel)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			out = append(out, Finding{"warn", "broken-symlink", e.Rel,
				"symbolic link does not resolve — the content it names is missing"})
			continue
		}
		rel, err := filepath.Rel(realRoot, resolved)
		escapes := err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
		if escapes {
			out = append(out, Finding{"warn", "symlink", e.Rel,
				"symbolic link points outside the instance — git stores the link, not the content, so a clone elsewhere loses it; copy the file in instead"})
			continue
		}
		out = append(out, Finding{"info", "symlink", e.Rel,
			"symbolic link inside the instance — the same content under two names makes [[wikilink]] resolution ambiguous"})
	}
	return out
}

// isReadable reports whether the file can actually be opened. The tree walk
// lists entries with Lstat, so a dangling symlink or an unreadable file appears
// like any other note until something tries to read it.
func isReadable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func mentionedInOwnIndex(indexBodies map[string]string, rel string) bool {
	body := indexBodies[parentOf(rel)]
	base := baseName(rel)
	return indexMentions(body, base) || indexMentions(body, strings.TrimSuffix(base, ".md"))
}

// indexMentions is a whole-word substring match: a file named `x` is not
// "mentioned" just because some INDEX sentence contains the letter x.
func indexMentions(body, name string) bool {
	if name == "" {
		return false
	}
	re := regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(strings.ToLower(name)) + `([^a-z0-9]|$)`)
	return re.MatchString(body)
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---") {
		return s
	}
	rest := s[3:]
	if i := strings.Index(rest, "\n---"); i >= 0 {
		after := rest[i+4:]
		if j := strings.Index(after, "\n"); j >= 0 {
			return after[j+1:]
		}
		return ""
	}
	return s
}
