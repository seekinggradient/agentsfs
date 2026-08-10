package core

import (
	"strings"
)

// Reserved directory roles. A directory plays a role only when its INDEX.md
// declares it via the `agentsfs_role:` frontmatter key — the marker is the
// truth, not the directory name (contract 0.4.0). The names below are the
// template defaults and the classic-name compat fallbacks.
const (
	RoleJournal = "journal"
	RoleScratch = "scratch"
	// RoleCollection marks a directory as a body of like items (a diary, daily
	// notes, attachments) described collectively by its INDEX.md rather than
	// file-by-file. Unlike journal/scratch it is repeatable — many per instance
	// — and durable (never deletable). See doctor's collection exemptions.
	RoleCollection = "collection"

	// RoleBacklog marks this instance's backlog: prospective memory — prioritized
	// pending work in checkbox lists, parsed by tasks.go. Since contract 0.11.0 it
	// is a DIRECTORY role like the three above, declared in the directory's
	// INDEX.md, where that INDEX.md is itself the spine (the task page) as well as
	// the directory's self-description. Ticket files sit beside it, archive/ holds
	// what closed, and sub-directories are delegated sub-backlogs.
	//
	// 0.10.0's page-level marker still resolves, as legacy: an ordinary page
	// carrying the marker is the backlog, read-only, until `afs contract upgrade`
	// moves it into a directory (doctor reports backlog-page-role-legacy). Either
	// way there is no classic-name fallback: a file or directory named backlog
	// means nothing without the marker.
	RoleBacklog = "backlog"

	roleKey = "agentsfs_role"

	// defaultJournalDir / defaultScratchDir are the template defaults laid
	// down for a fresh instance (contract 0.4.0 renamed them from the classic
	// journal/ and scratch/).
	defaultJournalDir = "agent-journal"
	defaultScratchDir = "agent-scratch"

	// classicJournalDir / classicScratchDir are the pre-0.4.0 reserved names.
	// When no directory is marked for a role, tooling falls back to these so
	// un-upgraded 0.3.0 instances keep today's behavior.
	classicJournalDir = "journal"
	classicScratchDir = "scratch"
)

// Role resolution sources, reported alongside each resolved role so a consumer
// can tell a deliberately declared journal from one inferred by classic name —
// and an absent role from an empty instance.
const (
	RoleSourceMarker  = "marker"  // a directory's INDEX.md declares agentsfs_role
	RoleSourceClassic = "classic" // no marker; the pre-0.4.0 reserved name exists
	RoleSourceNone    = "none"    // the role has no home in this instance
)

// RoleDirs is the resolved set of reserved roles for an instance. Journal
// and Scratch are slash-relative directory paths ("" when the role resolves to
// nothing at all — a fresh instance with no marker and no classic-named dir).
// Duplicate* list every path marked for a role when more than one is, so
// doctor can flag the ambiguity (a role must have exactly one home). Collections
// are repeatable — every directory marked `agentsfs_role: collection`, in sorted
// order — so there is no duplicate list and no classic-name fallback for them.
//
// Backlog is the backlog's home: the directory under contract 0.11.0, or — for
// an instance still on 0.10.0's page-level marker — the marked page itself, with
// BacklogLegacy saying which. BacklogSpine is always the TASK PAGE: the
// directory's INDEX.md, or the legacy page. A consumer that wants tasks reads
// BacklogSpine (or better, LoadBacklog, which also finds delegated sub-spines);
// one that wants the collection of tickets reads Backlog and checks
// BacklogLegacy first.
//
// The JSON shape is the contract `afs roles --json` publishes: it is how other
// tools locate the journal, scratch, collections, and backlog WITHOUT hardcoding
// names that the contract may change. Consumers should read these paths rather
// than assuming "agent-journal/". `backlog_spine` and `backlog_legacy` are new
// in 0.11.0, and `backlog` changes meaning with them — a deliberate breaking
// change to `afs roles --json`, ratified by the backlog-directories RFC.
type RoleDirs struct {
	Journal       string   `json:"journal"`
	JournalSource string   `json:"journal_source"`
	Scratch       string   `json:"scratch"`
	ScratchSource string   `json:"scratch_source"`
	Collections   []string `json:"collections"`
	// Backlog is the backlog directory (0.11.0) or the marked legacy page.
	Backlog string `json:"backlog"`
	// BacklogSpine is the task page: <Backlog>/INDEX.md for a directory role,
	// the page itself for a legacy one. "" when no backlog is declared.
	BacklogSpine string `json:"backlog_spine"`
	// BacklogLegacy is true when the role came from a page-level marker — the
	// retired 0.10.0 shape, supported read-only.
	BacklogLegacy    bool     `json:"backlog_legacy,omitempty"`
	BacklogSource    string   `json:"backlog_source"`
	DuplicateJournal []string `json:"duplicate_journal,omitempty"`
	DuplicateScratch []string `json:"duplicate_scratch,omitempty"`
	DuplicateBacklog []string `json:"duplicate_backlog,omitempty"`
}

// ResolveReservedDirs scans the instance for `agentsfs_role:` markers — a
// directory's in its INDEX.md, a page's in its own frontmatter — and resolves
// each reserved role to its home. Resolution rule per role: if anything is
// marked for the role, markers win exclusively (and duplicates are reported);
// otherwise fall back to the classic name (journal/, scratch/) when that
// directory exists, so un-upgraded instances keep working. Backlog has no such
// fallback: the marker is its only truth.
func ResolveReservedDirs(root string) (RoleDirs, error) {
	entries, err := ListEntries(root)
	if err != nil {
		return RoleDirs{}, err
	}
	return resolveReservedFromEntries(root, entries), nil
}

// resolveReservedFromEntries is the entry-list form so callers that already
// walked the tree (doctor) don't walk it twice.
func resolveReservedFromEntries(root string, entries []Entry) RoleDirs {
	var journalMarked, scratchMarked, collectionMarked, backlogMarked, backlogPages []string
	haveClassicJournal, haveClassicScratch := false, false
	for _, e := range entries {
		if !e.IsDir {
			// Page-level roles. Only markdown carries frontmatter, and extraction
			// stops at the closing fence, so this costs one short read per note
			// rather than a second walk of the tree.
			//
			// Contract 0.11.0 inverted the 0.10.0 rule here: a marked INDEX.md now
			// confers the role on its DIRECTORY (the directory switch below reads
			// it), because the backlog is a directory whose INDEX.md is also its
			// spine. Every other page carrying the marker is the legacy page role —
			// still resolved so 0.10.0 instances keep working read-only, and
			// reported as such so doctor can suggest the upgrade. The root's own
			// INDEX.md is the exception that stays a page: the root is not a
			// directory entry, so nothing else would read its marker at all.
			if isMarkdown(e.Rel) && (e.Rel == "INDEX.md" || !strings.EqualFold(baseName(e.Rel), "INDEX.md")) &&
				FrontmatterValue(joinRel(root, e.Rel), roleKey) == RoleBacklog {
				backlogPages = append(backlogPages, e.Rel)
			}
			continue
		}
		// Classic-name fallback matches the exact lowercase reserved name only.
		// A dir named "Journal" (a personal diary) merely collides on a
		// case-insensitive filesystem — it must NOT be adopted as the journal;
		// the collision guard at lay-down handles that case separately.
		if e.Rel == classicJournalDir {
			haveClassicJournal = true
		}
		if e.Rel == classicScratchDir {
			haveClassicScratch = true
		}
		role := FrontmatterValue(joinRel(root, e.Rel+"/INDEX.md"), roleKey)
		switch role {
		case RoleJournal:
			journalMarked = append(journalMarked, e.Rel)
		case RoleScratch:
			scratchMarked = append(scratchMarked, e.Rel)
		case RoleCollection:
			collectionMarked = append(collectionMarked, e.Rel)
		case RoleBacklog:
			backlogMarked = append(backlogMarked, e.Rel)
		}
	}

	var rd RoleDirs
	rd.Journal, rd.JournalSource, rd.DuplicateJournal = resolveOne(journalMarked, haveClassicJournal, classicJournalDir)
	rd.Scratch, rd.ScratchSource, rd.DuplicateScratch = resolveOne(scratchMarked, haveClassicScratch, classicScratchDir)
	resolveBacklogRole(&rd, backlogMarked, backlogPages)
	// Collections are repeatable — no single-home rule, so every marked dir is
	// kept (entries arrive sorted, so this list is deterministic). Never nil, so
	// the JSON surface is always a list rather than null.
	rd.Collections = collectionMarked
	if rd.Collections == nil {
		rd.Collections = []string{}
	}
	return rd
}

// resolveBacklogRole applies the single-home rule to the backlog's two shapes.
// The directory role (0.11.0) wins outright over any legacy page: an instance
// mid-upgrade has both, and the directory is where the work moved. Otherwise a
// marked page resolves as legacy. There is no classic-name fallback for either —
// the marker is the only truth — so an unmarked backlog.md stays an ordinary
// note. Duplicates list every marked home of BOTH kinds, so doctor can name the
// stragglers a mid-upgrade instance left behind.
func resolveBacklogRole(rd *RoleDirs, dirs, pages []string) {
	var dups []string
	if len(dirs)+len(pages) > 1 {
		dups = append(append([]string{}, dirs...), pages...)
	}
	rd.DuplicateBacklog = dups
	switch {
	case len(dirs) > 0:
		// Entries arrive sorted by Rel, so the first marked directory is the
		// deterministic winner — the same tie-break every other role uses.
		rd.Backlog, rd.BacklogSpine, rd.BacklogSource = dirs[0], dirs[0]+"/INDEX.md", RoleSourceMarker
	case len(pages) > 0:
		rd.Backlog, rd.BacklogSpine, rd.BacklogSource, rd.BacklogLegacy = pages[0], pages[0], RoleSourceMarker, true
	default:
		rd.BacklogSource = RoleSourceNone
	}
}

// resolveOne applies the resolution rule for a single role. Markers win when
// present (first sorted entry is the resolved dir; any extras are duplicates);
// otherwise fall back to the classic name if that directory exists. The
// returned source records which rule applied, so callers can distinguish a
// declared role from an inferred one.
func resolveOne(marked []string, haveClassic bool, classic string) (dir, source string, dups []string) {
	if len(marked) > 0 {
		// ListEntries returns entries already sorted by Rel, so marked is too;
		// the first is deterministic. All marked dirs are reported as duplicates
		// when there's more than one so doctor can name every one of them.
		if len(marked) > 1 {
			dups = marked
		}
		return marked[0], RoleSourceMarker, dups
	}
	if haveClassic {
		return classic, RoleSourceClassic, nil
	}
	return "", RoleSourceNone, nil
}

// inRoleDir reports whether rel is the role directory or inside it. A role that
// resolved to "" (no marker, no classic dir) matches nothing.
func inRoleDir(rel, dir string) bool {
	if dir == "" {
		return false
	}
	return rel == dir || strings.HasPrefix(rel, dir+"/")
}

// belowAnyCollection reports whether rel is a *content* entry of one of the
// collection directories — strictly inside it, but not the collection's own
// INDEX.md. A collection describes its contents collectively, so doctor
// suppresses per-entry findings beneath it; the collection's descriptor
// (<collection>/INDEX.md) is exempt from the suppression so its own
// description: is still required by the ordinary rules.
func belowAnyCollection(rel string, collections []string) bool {
	for _, c := range collections {
		if strings.HasPrefix(rel, c+"/") {
			// The collection's own INDEX.md is its descriptor, not its content.
			if rel == c+"/INDEX.md" {
				return false
			}
			return true
		}
	}
	return false
}
