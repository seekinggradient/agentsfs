package core

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Finding is one health problem doctor identified. Severity is "error"
// (contract violation), "warn" (probably wrong), or "info" (worth a look).
// Doctor is deterministic — no LLM — and its output is the worklist the
// gardener consumes.
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path"`
	Message  string `json:"message"`
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
			add("error", "missing-index", e.Rel, "directory has no INDEX.md describing it")
		}
	}
	if data, err := os.ReadFile(joinRel(root, "AGENTS.md")); err == nil {
		indexBodies["."] = strings.ToLower(string(data)) // root describes itself
	}

	for _, e := range entries {
		if e.IsDir || inScratch(e.Rel) || inCollection(e.Rel) {
			continue // collection contents are described collectively by its INDEX
		}
		base := baseName(e.Rel)
		if strings.HasPrefix(base, ".") {
			continue // machine files (.gitattributes etc.) describe nothing
		}
		if isMarkdown(e.Rel) {
			if Description(joinRel(root, e.Rel)) == "" {
				add("error", "missing-description", e.Rel, "markdown file has no description: frontmatter")
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

	// Link health.
	linkedFiles := map[string]bool{}
	for _, l := range links {
		if isRootContract(l.Source) || inScratch(l.Source) {
			continue
		}
		matches := idx.Resolve(l.Target)
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
			add("error", "dead-link", l.Source, fmt.Sprintf("line %d: [[%s]] resolves to no file", l.Line, l.Target))
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
