package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const freshBody = "Some real content that is long enough not to read as a stub note, with enough words to clear the threshold comfortably."

// backdate makes a file look untouched since n days ago. Doctor falls back to
// mtime when git has no record, and newInstance does not git-init, so this is
// what the staleness check actually reads in tests.
func backdate(t *testing.T, root, rel string, days int) {
	t.Helper()
	when := time.Now().AddDate(0, 0, -days)
	if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(rel)), when, when); err != nil {
		t.Fatal(err)
	}
}

// Staleness is opt-in. An instance that never declares a cadence must never
// hear about stale notes, so shipping the check cannot make existing knowledge
// bases start complaining.
func TestStalenessSilentWithoutCadence(t *testing.T) {
	root := newInstance(t, map[string]string{
		"INDEX.md": "---\ndescription: Root.\n---\n",
		"note.md":  "---\ndescription: A note.\n---\n" + freshBody,
	})
	backdate(t, root, "note.md", 3650)
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if hasFinding(findings, "stale", "note.md") {
		t.Errorf("stale reported with no declared cadence: %+v", findings)
	}
}

// With a cadence declared, a note untouched for more than 3x that period is the
// warning the whole feature exists for.
func TestStalenessFlagsNotePastThreeCadences(t *testing.T) {
	root := newInstance(t, map[string]string{
		"INDEX.md": "---\ndescription: Root.\nupdate_cadence: weekly\n---\n",
		"old.md":   "---\ndescription: Old note.\n---\n" + freshBody,
		"new.md":   "---\ndescription: New note.\n---\n" + freshBody,
	})
	backdate(t, root, "old.md", 30) // > 3 * 7
	backdate(t, root, "new.md", 5)  // well inside
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(findings, "stale", "old.md") {
		t.Errorf("expected stale for old.md: %+v", findings)
	}
	if hasFinding(findings, "stale", "new.md") {
		t.Errorf("new.md flagged stale: %+v", findings)
	}
}

// dormant: true is how a note says its subject went quiet on purpose. It
// suppresses staleness only — the note still owes a description and live links.
func TestStalenessDormantExempt(t *testing.T) {
	root := newInstance(t, map[string]string{
		"INDEX.md":   "---\ndescription: Root.\nupdate_cadence: weekly\n---\n",
		"dormant.md": "---\ndescription: Retired subject.\ndormant: true\n---\n" + freshBody,
		"live.md":    "---\ndescription: Live subject.\n---\n" + freshBody,
	})
	backdate(t, root, "dormant.md", 400)
	backdate(t, root, "live.md", 400)
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if hasFinding(findings, "stale", "dormant.md") {
		t.Errorf("dormant note flagged stale: %+v", findings)
	}
	if !hasFinding(findings, "stale", "live.md") {
		t.Errorf("expected stale for live.md: %+v", findings)
	}
}

// One instance can hold material moving at different speeds. A directory's own
// INDEX.md governs its subtree, so a daily area and a monthly reference shelf
// are judged by their own clocks.
func TestStalenessDirectoryCadenceOverridesRoot(t *testing.T) {
	root := newInstance(t, map[string]string{
		"INDEX.md":            "---\ndescription: Root.\nupdate_cadence: daily\n---\n",
		"reference/INDEX.md":  "---\ndescription: Slow reference.\nupdate_cadence: monthly\n---\n",
		"reference/stable.md": "---\ndescription: Stable fact.\n---\n" + freshBody,
		"fast.md":             "---\ndescription: Fast note.\n---\n" + freshBody,
	})
	backdate(t, root, "reference/stable.md", 20) // inside 3 * 30
	backdate(t, root, "fast.md", 20)             // far past 3 * 1
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if hasFinding(findings, "stale", "reference/stable.md") {
		t.Errorf("monthly subtree judged by the root's daily cadence: %+v", findings)
	}
	if !hasFinding(findings, "stale", "fast.md") {
		t.Errorf("expected stale for fast.md under a daily cadence: %+v", findings)
	}
}

// A misspelled cadence must not silently mean "never stale" — the instance
// thinks it is being checked and is not.
func TestStalenessUnknownCadenceReported(t *testing.T) {
	root := newInstance(t, map[string]string{
		"INDEX.md": "---\ndescription: Root.\nupdate_cadence: fortnightly\n---\n",
		"note.md":  "---\ndescription: A note.\n---\n" + freshBody,
	})
	backdate(t, root, "note.md", 400)
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(findings, "unknown-cadence", "INDEX.md") {
		t.Errorf("expected unknown-cadence: %+v", findings)
	}
	for _, f := range findings {
		if f.Code == "unknown-cadence" && !strings.Contains(f.Message, "fortnightly") {
			t.Errorf("message should name the bad value, got %q", f.Message)
		}
	}
}

// The journal has its own backlog check and scratch is ephemeral; neither
// should also be reported stale.
func TestStalenessSkipsJournalAndScratch(t *testing.T) {
	root := newInstance(t, map[string]string{
		"INDEX.md":               "---\ndescription: Root.\nupdate_cadence: daily\n---\n",
		"agent-journal/INDEX.md": "---\ndescription: Journal.\nagentsfs_role: journal\n---\n",
		"agent-journal/entry.md": "---\ndescription: A session note.\n---\n" + freshBody,
		"agent-scratch/INDEX.md": "---\ndescription: Scratch.\nagentsfs_role: scratch\n---\n",
		"agent-scratch/draft.md": "---\ndescription: A draft.\n---\n" + freshBody,
	})
	backdate(t, root, "agent-journal/entry.md", 400)
	backdate(t, root, "agent-scratch/draft.md", 400)
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if hasFinding(findings, "stale", "agent-journal/entry.md") {
		t.Errorf("journal entry flagged stale: %+v", findings)
	}
	if hasFinding(findings, "stale", "agent-scratch/draft.md") {
		t.Errorf("scratch draft flagged stale: %+v", findings)
	}
}

// Review regression: a bad cadence must be reported against the INDEX.md that
// actually declares it. Blaming the note's own directory sends the reader to a
// file that does not contain the typo.
func TestStalenessUnknownCadenceBlamesDeclaringIndex(t *testing.T) {
	root := newInstance(t, map[string]string{
		"INDEX.md":        "---\ndescription: Root.\nupdate_cadence: fortnightly\n---\n",
		"deep/INDEX.md":   "---\ndescription: Deep, declares nothing.\n---\n",
		"deep/a/INDEX.md": "---\ndescription: Deeper, declares nothing.\n---\n",
		"deep/a/note.md":  "---\ndescription: A note.\n---\n" + freshBody,
	})
	backdate(t, root, "deep/a/note.md", 400)
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(findings, "unknown-cadence", "INDEX.md") {
		t.Errorf("expected unknown-cadence against the declaring root INDEX.md: %+v", findings)
	}
	for _, bad := range []string{"deep/INDEX.md", "deep/a/INDEX.md"} {
		if hasFinding(findings, "unknown-cadence", bad) {
			t.Errorf("blamed %s, which declares no cadence: %+v", bad, findings)
		}
	}
}

// CadenceFor is the resolution rule on its own: nearest INDEX.md wins, walking
// up to the root.
func TestCadenceForResolvesNearestIndex(t *testing.T) {
	root := newInstance(t, map[string]string{
		"INDEX.md":     "---\ndescription: Root.\nupdate_cadence: weekly\n---\n",
		"a/INDEX.md":   "---\ndescription: A.\n---\n",
		"a/b/INDEX.md": "---\ndescription: B.\nupdate_cadence: daily\n---\n",
		"a/note.md":    "---\ndescription: note.\n---\n",
		"a/b/note.md":  "---\ndescription: note.\n---\n",
		"top.md":       "---\ndescription: note.\n---\n",
	})
	cases := map[string]string{
		"top.md":      "weekly", // root
		"a/note.md":   "weekly", // a/ declares none, inherits root
		"a/b/note.md": "daily",  // nearest wins
	}
	for rel, want := range cases {
		if got := CadenceFor(root, rel); got != want {
			t.Errorf("CadenceFor(%q) = %q, want %q", rel, got, want)
		}
	}
}
