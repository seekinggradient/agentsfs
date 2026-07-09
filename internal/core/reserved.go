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
// doctor can flag the ambiguity (a role must have exactly one home). Collections
// are repeatable — every directory marked `agentsfs_role: collection`, in sorted
// order — so there is no duplicate list and no classic-name fallback for them.
type RoleDirs struct {
	Journal          string
	Scratch          string
	Collections      []string
	DuplicateJournal []string
	DuplicateScratch []string
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
	var journalMarked, scratchMarked, collectionMarked []string
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
		case RoleCollection:
			collectionMarked = append(collectionMarked, e.Rel)
		}
	}

	var rd RoleDirs
	rd.Journal, rd.DuplicateJournal = resolveOne(journalMarked, haveClassicJournal, classicJournalDir)
	rd.Scratch, rd.DuplicateScratch = resolveOne(scratchMarked, haveClassicScratch, classicScratchDir)
	// Collections are repeatable — no single-home rule, so every marked dir is
	// kept (entries arrive sorted, so this list is deterministic).
	rd.Collections = collectionMarked
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
