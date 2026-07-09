package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A stock 0.4.0 instance upgrades to the bundled 0.5.0 cleanly: the newly
// vendored 0.4.0 stock text lets ContractCustomized recognize it as un-adapted
// (so upgrade would not refuse), the upgrade bumps the version, and doctor stays
// clean. Its already-marked default reserved dirs resolve unchanged.
func TestUpgradeStock040To050(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock040, ok := StockContract("0.4.0")
	if !ok {
		t.Fatal("no vendored 0.4.0 stock contract")
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock040)
	// 0.4.0 stock instances already ship the marked default reserved dirs.
	for _, d := range []struct{ dir, role, desc string }{
		{"agent-journal", "journal", "Session log."},
		{"agent-scratch", "scratch", "Scratch."},
	} {
		mustWrite(t, filepath.Join(root, d.dir, "INDEX.md"),
			"---\ndescription: "+d.desc+"\nagentsfs_role: "+d.role+"\n---\n# "+d.dir+"\n")
	}

	// The vendored 0.4.0 text must make the guard read this as un-customized.
	customized, known := ContractCustomized(root)
	if !known {
		t.Fatal("0.4.0 stock text is not vendored — the customized-contract guard can't tell")
	}
	if customized {
		t.Fatal("a byte-exact stock 0.4.0 AGENTS.md was reported customized")
	}

	if _, err := UpgradeContract(root); err != nil {
		t.Fatal(err)
	}
	if got := ContractVersion(root); got != CurrentContractVersion() {
		t.Errorf("upgrade did not bump the contract: %q", got)
	}
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Journal != "agent-journal" || rd.Scratch != "agent-scratch" {
		t.Errorf("reserved dirs did not resolve after upgrade: %+v", rd)
	}
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Severity == "error" {
			t.Errorf("upgraded 0.4.0→0.5.0 instance is not doctor-clean: %s %s %s", f.Severity, f.Code, f.Message)
		}
	}
}

// A customized 0.4.0 contract (any byte difference from stock) is recognized as
// customized against the newly vendored 0.4.0 text — the signal the CLI uses to
// refuse upgrade without --force.
func TestCustomized040ContractDetected(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock040, _ := StockContract("0.4.0")
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock040+"\n## House rule\n\nAlways cite the policy number.\n")
	customized, known := ContractCustomized(root)
	if !known || !customized {
		t.Fatalf("adapted 0.4.0 contract not detected as customized (known=%v customized=%v)", known, customized)
	}
}

// findingsFor returns the codes of every finding whose path is rel.
func findingsFor(findings []Finding, rel string) []string {
	var out []string
	for _, f := range findings {
		if f.Path == rel {
			out = append(out, f.Code)
		}
	}
	return out
}

// A directory marked agentsfs_role: collection describes its contents
// collectively: everything strictly below it — files with no frontmatter,
// subdirectories with no INDEX, unlinked short files, and a dead [[link]]
// sourced inside — produces ZERO findings, while the collection's own INDEX is
// still checked. The same shapes OUTSIDE a collection still produce findings
// (the control case below).
func TestCollectionSuppressesSubtreeFindings(t *testing.T) {
	root := newInstance(t, map[string]string{
		// A collection with a truthful description, holding frontmatter-less
		// files, a subdir without its own INDEX, and a file with a dead link.
		"diary/INDEX.md":            "---\ndescription: Personal daily notes, kept collectively.\nagentsfs_role: collection\n---\n# Diary\n",
		"diary/2026-07-01.md":       "Woke up. Wrote some notes. No frontmatter here.\n",
		"diary/2026-07-02.md":       "short\n", // would be a stub outside a collection
		"diary/media/photo-note.md": "A note in a subdir with no INDEX and a [[Nowhere]] dead link.\n",
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"diary/2026-07-01.md",
		"diary/2026-07-02.md",
		"diary/media",
		"diary/media/photo-note.md",
	} {
		if codes := findingsFor(findings, rel); len(codes) != 0 {
			t.Errorf("collection content %s produced findings %v, want none", rel, codes)
		}
	}
}

// Control: the identical shapes OUTSIDE any collection still produce their
// findings, proving the suppression is the collection marker's doing.
func TestNonCollectionSubtreeStillFlagged(t *testing.T) {
	root := newInstance(t, map[string]string{
		"notes/INDEX.md":            "---\ndescription: Active notes.\n---\n# Notes\n",
		"notes/2026-07-01.md":       "Woke up. Wrote some notes. No frontmatter here.\n",
		"notes/2026-07-02.md":       "short\n",
		"notes/media/photo-note.md": "A note in a subdir with no INDEX and a [[Nowhere]] dead link.\n",
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"notes/2026-07-01.md":       "missing-description",
		"notes/2026-07-02.md":       "stub",
		"notes/media":               "missing-index",
		"notes/media/photo-note.md": "dead-link",
	}
	for rel, code := range want {
		if !containsString(findingsFor(findings, rel), code) {
			t.Errorf("expected %s finding on %s outside a collection; got %v", code, rel, findingsFor(findings, rel))
		}
	}
}

// The collection's own INDEX.md is not exempt: a missing description: on it is
// still flagged (the existing per-directory rules cover the dir itself).
func TestCollectionIndexMissingDescriptionFlagged(t *testing.T) {
	root := newInstance(t, map[string]string{
		// Marker present but no description in frontmatter.
		"attachments/INDEX.md": "---\nagentsfs_role: collection\n---\n# Attachments\n",
		"attachments/a.bin":    "binary-ish\n",
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	// The INDEX describes a directory but has no description: — the ordinary
	// missing-description rule for markdown fires on the INDEX itself.
	if !containsString(findingsFor(findings, "attachments/INDEX.md"), "missing-description") {
		t.Errorf("collection INDEX with no description: was not flagged; findings: %+v", findings)
	}
}

// Collections are repeatable: two coexist with no duplicate-role error. The
// duplicate-role rule stays scoped to journal and scratch.
func TestTwoCollectionsCoexist(t *testing.T) {
	root := newInstance(t, map[string]string{
		"diary/INDEX.md":   "---\ndescription: Daily diary.\nagentsfs_role: collection\n---\n",
		"gallery/INDEX.md": "---\ndescription: Saved images.\nagentsfs_role: collection\n---\n",
	})
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rd.Collections) != 2 {
		t.Fatalf("resolved %d collections, want 2: %+v", len(rd.Collections), rd.Collections)
	}
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Code == "duplicate-role" {
			t.Errorf("duplicate-role fired for two collections (it is journal/scratch only): %+v", f)
		}
	}
}

// A [[wikilink]] sourced from inside a collection still resolves for backlinks
// (only doctor findings are suppressed, not link resolution).
func TestBacklinkFromInsideCollectionResolves(t *testing.T) {
	root := newInstance(t, map[string]string{
		"reference/INDEX.md":  "---\ndescription: Reference.\n---\n",
		"reference/Dana.md":   "---\ndescription: Dana's page.\n---\n# Dana\n",
		"diary/INDEX.md":      "---\ndescription: Daily diary.\nagentsfs_role: collection\n---\n",
		"diary/2026-07-01.md": "Talked to [[Dana]] today.\n",
	})
	links, err := ScanLinks(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := BuildNameIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, l := range links {
		if l.Source == "diary/2026-07-01.md" && l.Target == "Dana" {
			if matches := idx.Resolve(l.Target); len(matches) == 1 && matches[0] == "reference/Dana.md" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("[[Dana]] from inside the collection did not resolve to reference/Dana.md via the link index")
	}
	// And doctor raises no dead-link for that same link.
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Path == "diary/2026-07-01.md" && (f.Code == "dead-link" || f.Code == "ambiguous-link") {
			t.Errorf("link finding sourced inside a collection should be suppressed: %+v", f)
		}
	}
}

// A dead [[link]] whose TARGET is inside a collection still resolves (the
// collection is fully indexed), and the orphan check sees the resolution — so a
// collection file pointed at from a durable note is not reported orphan.
func TestCollectionContentIsIndexedForResolution(t *testing.T) {
	root := newInstance(t, map[string]string{
		"reference/INDEX.md":  "---\ndescription: Reference.\n---\nSee [[2026-07-01]].\n",
		"diary/INDEX.md":      "---\ndescription: Daily diary.\nagentsfs_role: collection\n---\n",
		"diary/2026-07-01.md": "A day.\n",
	})
	idx, err := BuildNameIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	if matches := idx.Resolve("2026-07-01"); len(matches) != 1 || matches[0] != "diary/2026-07-01.md" {
		t.Errorf("collection file not resolvable by name (should stay indexed): %v", matches)
	}
	// The reference INDEX's [[2026-07-01]] resolves, so no dead-link there.
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Path == "reference/INDEX.md" && f.Code == "dead-link" {
			t.Errorf("link into a collection reported dead though the target is indexed: %+v", f)
		}
	}
}

// A dot-directory (.obsidian/ with junk) is machine territory: it produces no
// findings and does not appear in the walk (tree/search/doctor all use
// ListEntries).
func TestDotDirIgnored(t *testing.T) {
	root := newInstance(t, map[string]string{
		"notes/INDEX.md":         "---\ndescription: Notes.\n---\n",
		"notes/a.md":             "---\ndescription: A note.\n---\n# A\n\nSome durable content that is long enough not to be a stub for the orphan and stub checks.\n",
		".obsidian/app.json":     "{\"junk\": true}\n",
		".obsidian/workspace":    "binary-ish junk\n",
		".obsidian/plugins/x.js": "console.log('x')\n",
		".trash/deleted.md":      "no frontmatter, would be flagged if walked\n",
	})
	entries, err := ListEntries(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Rel, ".obsidian") || strings.HasPrefix(e.Rel, ".trash") {
			t.Errorf("dot-directory content leaked into the walk: %s", e.Rel)
		}
	}
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if strings.HasPrefix(f.Path, ".obsidian") || strings.HasPrefix(f.Path, ".trash") {
			t.Errorf("doctor produced a finding for dot-dir content: %+v", f)
		}
	}
}

// A role marker (journal/scratch) on a directory INSIDE a collection still
// resolves normally — collection opacity is a doctor concern, not a resolver
// one. (Nested/edge case from the brief.)
func TestRoleMarkerInsideCollectionStillResolves(t *testing.T) {
	root := newInstance(t, map[string]string{
		"archive/INDEX.md":          "---\ndescription: Archived material.\nagentsfs_role: collection\n---\n",
		"archive/sessions/INDEX.md": "---\ndescription: Old session journal.\nagentsfs_role: journal\n---\n",
	})
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Journal != "archive/sessions" {
		t.Errorf("journal marker inside a collection did not resolve: Journal=%q", rd.Journal)
	}
	if len(rd.Collections) != 1 || rd.Collections[0] != "archive" {
		t.Errorf("collection not resolved alongside the nested journal: %+v", rd.Collections)
	}
}
