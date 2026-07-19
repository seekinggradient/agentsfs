package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
// The bias is deliberate: a knowledge base mid-growth legitimately contains
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
const RootDescriptionPlaceholder = "REPLACE ME: one or two sentences describing what THIS knowledge base is about and what lives in it."

// legacyRootDescriptionSignature is a stable fragment of the pre-0.7.0 template
// boilerplate ("Self-describing root of this agentsfs. Read this first …") that
// served as the root description before it moved to the root INDEX.md. Doctor
// detects it too, so an instance whose per-KB description is still that
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
	// label to every surface that lists instances (the Hub, `afs status`, agent
	// orientation), so doctor nudges until it is real.
	if rootIndex := joinRel(root, "INDEX.md"); !fileExists(rootIndex) {
		add("warn", "root-index", ".", "no root INDEX.md — this knowledge base has no per-instance description; run `afs contract upgrade` to create one, then describe what this instance holds")
	} else {
		switch desc := strings.TrimSpace(Description(rootIndex)); {
		case desc == "":
			add("warn", "root-description", "INDEX.md", "root INDEX.md has no description: — set it to what this knowledge base is about and what lives in it")
		case IsPlaceholderRootDescription(desc):
			add("warn", "root-description", "INDEX.md", "root INDEX.md description is still the template placeholder — replace it with what this knowledge base is actually about")
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
	// own INDEX.md (the per-KB description and any listing of files that can't
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
			switch path := joinRel(root, e.Rel); {
			case !isReadable(path):
				// Report the real problem. Without this the file lands in the
				// missing-description bucket, sending the reader to add a
				// description to something they cannot even open.
				add("warn", "unreadable", e.Rel, "listed in the tree but cannot be read — check permissions, or replace a dangling link with the real file")
			case FrontmatterUnclosed(path):
				add("warn", "malformed-frontmatter", e.Rel, "frontmatter opens with --- but is never closed — every stricter reader (a YAML parser, Obsidian, the Hub) sees no frontmatter at all, and this scanner runs past the block and may take a key out of the prose; add the closing ---")
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

	// Both the journal backlog and the staleness check need per-file edit times,
	// which cost one `git log` over the whole instance. Resolve them once here
	// and hand the map to both rather than shelling out twice per run.
	touched, _ := gitLastTouchedTimes(root)

	// Journal backlog: the gardener empties the journal into durable notes.
	// A pile-up (many entries, or a stale oldest one) means it isn't keeping up.
	findings = append(findings, journalBacklog(root, entries, roles.Journal, touched)...)

	// Symlinks break the substrate's core promise — that the files ARE the
	// knowledge and `git clone` is the exit ramp. Git stores the link, not the
	// content, so a clone on another machine gets a dangling pointer.
	findings = append(findings, symlinkFindings(root, entries, inScratch)...)

	// Staleness against a declared update_cadence. Silent unless the instance
	// opts in by declaring one.
	findings = append(findings, staleness(root, entries, roles, touched)...)

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

func journalBacklog(root string, entries []Entry, journalDir string, times map[string]time.Time) []Finding {
	if journalDir == "" {
		return nil // no journal resolved — nothing to back up
	}
	var oldest time.Time
	count := 0
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
