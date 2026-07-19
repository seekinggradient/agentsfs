package core

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Knowledge goes stale, and a knowledge base has no way to say so on its own.
// An instance may declare how fast its material moves with `update_cadence:` in
// its root INDEX.md, and doctor then warns about notes that have not been
// touched in well over that long:
//
//	---
//	description: what this knowledge base holds
//	update_cadence: weekly
//	---
//
// A directory's own INDEX.md may override the root for its subtree, so a
// fast-moving areas/ and a slow reference/ can coexist. A note whose subject is
// genuinely inactive declares `dormant: true` and is exempt.
//
// The check is entirely opt-in: an instance that declares no cadence anywhere
// gets no staleness findings at all. Nothing existing starts warning because
// this shipped.
const cadenceKey = "update_cadence"

// dormantKey marks a note whose subject is inactive. It suppresses staleness
// only — description, link, and every other rule still applies.
const dormantKey = "dormant"

// staleMultiplier is how many cadence periods a note may go untouched before
// doctor mentions it. One missed cycle is normal; three is a pattern.
const staleMultiplier = 3

// cadenceDays maps the declared cadences to their period. These are the only
// accepted values; anything else is reported as unrecognized rather than
// silently treated as "never stale".
var cadenceDays = map[string]int{
	"daily":   1,
	"weekly":  7,
	"monthly": 30,
}

// CadenceFor returns the update cadence governing a file, resolved from the
// nearest INDEX.md at or above its directory, falling back to the root. The
// empty string means no cadence governs it and it is never stale.
func CadenceFor(root, rel string) string {
	cadence, _ := cadenceForDir(root, parentOf(rel), nil)
	return cadence
}

// cadenceForDir resolves the cadence governing a directory and reports WHICH
// INDEX.md declared it, so a bad value is reported against the file that
// actually contains it rather than the note that happens to inherit it. The
// optional cache memoizes per directory: without it, resolution re-reads every
// ancestor INDEX.md once per markdown file in the instance.
func cadenceForDir(root, dir string, cache map[string]cadenceSource) (cadence, declaredIn string) {
	if c, ok := cache[dir]; ok {
		return c.cadence, c.declaredIn
	}
	idx := indexPathFor(dir)
	if c := FrontmatterValue(joinRel(root, idx), cadenceKey); c != "" {
		cadence, declaredIn = strings.ToLower(strings.TrimSpace(c)), idx
	} else if dir == "." || dir == "" {
		cadence, declaredIn = "", ""
	} else {
		cadence, declaredIn = cadenceForDir(root, parentOf(dir), cache)
	}
	if cache != nil {
		cache[dir] = cadenceSource{cadence, declaredIn}
	}
	return cadence, declaredIn
}

type cadenceSource struct {
	cadence    string
	declaredIn string
}

func indexPathFor(dir string) string {
	if dir == "." || dir == "" {
		return "INDEX.md"
	}
	return dir + "/INDEX.md"
}

// staleness reports notes that have gone untouched for more than
// staleMultiplier cadence periods, plus any unrecognized cadence value. It
// returns nothing at all when the instance declares no cadence.
//
// Exemptions mirror the rest of doctor: scratch is ephemeral, the journal has
// its own backlog check, collection contents are described collectively, and
// INDEX.md files are descriptors rather than knowledge.
func staleness(root string, entries []Entry, roles RoleDirs, times map[string]time.Time) []Finding {
	var findings []Finding
	now := time.Now()
	cache := map[string]cadenceSource{}
	reportedBadCadence := map[string]bool{}

	for _, e := range entries {
		if e.IsDir || !isMarkdown(e.Rel) {
			continue
		}
		if inRoleDir(e.Rel, roles.Scratch) || inRoleDir(e.Rel, roles.Journal) {
			continue
		}
		if belowAnyCollection(e.Rel, roles.Collections) {
			continue
		}
		if isRootContract(e.Rel) || strings.EqualFold(baseName(e.Rel), "INDEX.md") {
			continue
		}
		cadence, declaredIn := cadenceForDir(root, parentOf(e.Rel), cache)
		if cadence == "" {
			continue
		}
		period, ok := cadenceDays[cadence]
		if !ok {
			// Report the bad value against the INDEX.md that actually declares
			// it — which may be an ancestor, not the note's own directory — and
			// only once per declaring file.
			if !reportedBadCadence[declaredIn] {
				reportedBadCadence[declaredIn] = true
				findings = append(findings, Finding{"warn", "unknown-cadence", declaredIn,
					fmt.Sprintf("update_cadence: %q is not one of daily, weekly, monthly — staleness is not being checked", cadence)})
			}
			continue
		}
		path := joinRel(root, e.Rel)
		if strings.EqualFold(strings.TrimSpace(FrontmatterValue(path, dormantKey)), "true") {
			continue
		}
		touched, ok := lastTouched(times, path, e.Rel)
		if !ok {
			continue
		}
		limit := staleMultiplier * period
		age := int(now.Sub(touched).Hours() / 24)
		if age > limit {
			findings = append(findings, Finding{"warn", "stale", e.Rel,
				fmt.Sprintf("untouched for %dd, past %dx the %s cadence (%dd) — refresh it, or set dormant: true if its subject is inactive", age, staleMultiplier, cadence, limit)})
		}
	}
	return findings
}

// lastTouched is when a file last changed: its git commit time, or its mtime
// when git has no record (untracked, or not a repo at all).
func lastTouched(times map[string]time.Time, path, rel string) (time.Time, bool) {
	if t, ok := times[rel]; ok {
		return t, true
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}
