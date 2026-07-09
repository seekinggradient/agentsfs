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

// RoleDirs is the resolved set of reserved directories for an instance. Journal
// and Scratch are slash-relative directory paths ("" when the role resolves to
// nothing at all — a fresh instance with no marker and no classic-named dir).
// Duplicate* list every directory marked for a role when more than one is, so
// doctor can flag the ambiguity (a role must have exactly one home).
type RoleDirs struct {
	Journal          string
	Scratch          string
	DuplicateJournal []string
	DuplicateScratch []string
	// journalResolved / scratchResolved record whether the role resolved via a
	// marker (or classic-name fallback) at all — distinct from Journal == ""
	// only in intent, but kept so callers reading the struct don't guess.
}

// ResolveReservedDirs scans the instance for directories whose INDEX.md
// declares an `agentsfs_role:` marker and resolves each reserved role to its
// directory. Resolution rule per role: if any directory is marked for the
// role, markers win exclusively (and duplicates are reported); otherwise fall
// back to the classic name (journal/, scratch/) when that directory exists, so
// un-upgraded instances keep working.
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
	var journalMarked, scratchMarked []string
	haveClassicJournal, haveClassicScratch := false, false
	for _, e := range entries {
		if !e.IsDir {
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
		}
	}

	var rd RoleDirs
	rd.Journal, rd.DuplicateJournal = resolveOne(journalMarked, haveClassicJournal, classicJournalDir)
	rd.Scratch, rd.DuplicateScratch = resolveOne(scratchMarked, haveClassicScratch, classicScratchDir)
	return rd
}

// resolveOne applies the resolution rule for a single role. Markers win when
// present (first sorted entry is the resolved dir; any extras are duplicates);
// otherwise fall back to the classic name if that directory exists.
func resolveOne(marked []string, haveClassic bool, classic string) (dir string, dups []string) {
	if len(marked) > 0 {
		// ListEntries returns entries already sorted by Rel, so marked is too;
		// the first is deterministic. All marked dirs are reported as duplicates
		// when there's more than one so doctor can name every one of them.
		if len(marked) > 1 {
			dups = marked
		}
		return marked[0], dups
	}
	if haveClassic {
		return classic, nil
	}
	return "", nil
}

// inRoleDir reports whether rel is the role directory or inside it. A role that
// resolved to "" (no marker, no classic dir) matches nothing.
func inRoleDir(rel, dir string) bool {
	if dir == "" {
		return false
	}
	return rel == dir || strings.HasPrefix(rel, dir+"/")
}
