package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A journal marked under a non-default name elsewhere in the tree is the
// journal — doctor's exemptions and the backlog check follow the marker, not
// the name.
func TestResolverFollowsMarkedJournalElsewhere(t *testing.T) {
	root := newInstance(t, map[string]string{
		"Work Logs/INDEX.md":                    "---\ndescription: Work logs area.\n---\n",
		"Work Logs/Sessions/INDEX.md":           "---\ndescription: Session journal.\nagentsfs_role: journal\n---\n",
		"Work Logs/Sessions/2026-07-01-work.md": "---\ndescription: Session — did a thing.\n---\n- short\n",
	})
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Journal != "Work Logs/Sessions" {
		t.Fatalf("Journal resolved to %q, want Work Logs/Sessions", rd.Journal)
	}

	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	// The short entry inside the marked journal must be exempt from stub/orphan.
	for _, f := range findings {
		if f.Path == "Work Logs/Sessions/2026-07-01-work.md" && (f.Code == "stub" || f.Code == "orphan") {
			t.Errorf("entry in the marked journal should be exempt, got %s", f.Code)
		}
	}
	// A journal resolved: no no-journal finding.
	if hasFinding(findings, "no-journal", ".") {
		t.Errorf("no-journal fired despite a marked journal: %+v", findings)
	}
}

// With nothing marked, the classic names journal/ and scratch/ still resolve
// (compat fallback for un-upgraded 0.3.0 instances).
func TestResolverClassicNameFallback(t *testing.T) {
	root := newInstance(t, map[string]string{
		"journal/INDEX.md": "---\ndescription: Session log.\n---\n",
		"scratch/INDEX.md": "---\ndescription: Scratch.\n---\n",
	})
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Journal != "journal" || rd.Scratch != "scratch" {
		t.Fatalf("classic fallback failed: %+v", rd)
	}
}

// A marker wins over the classic name when both exist: a marked dir elsewhere
// takes the role even though a classic-named journal/ is present.
func TestResolverMarkerWinsOverClassicName(t *testing.T) {
	root := newInstance(t, map[string]string{
		"journal/INDEX.md":  "---\ndescription: Old unmarked journal.\n---\n",
		"sessions/INDEX.md": "---\ndescription: The real journal.\nagentsfs_role: journal\n---\n",
	})
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Journal != "sessions" {
		t.Fatalf("marker did not win over classic name: Journal=%q", rd.Journal)
	}
}

// Two dirs marked for the same role is an error finding naming both.
func TestDoctorDuplicateRoleMarkers(t *testing.T) {
	root := newInstance(t, map[string]string{
		"a/INDEX.md": "---\ndescription: One journal.\nagentsfs_role: journal\n---\n",
		"b/INDEX.md": "---\ndescription: Another journal.\nagentsfs_role: journal\n---\n",
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range findings {
		if f.Code == "duplicate-role" {
			found = true
			if !strings.Contains(f.Message, "a") || !strings.Contains(f.Message, "b") {
				t.Errorf("duplicate-role message should name both dirs: %q", f.Message)
			}
		}
	}
	if !found {
		t.Errorf("no duplicate-role finding for two marked journals: %+v", findings)
	}
}

// No journal at all (neither marker nor classic name) is an info finding.
func TestDoctorNoJournalFinding(t *testing.T) {
	root := newInstance(t, map[string]string{
		"notes/INDEX.md": "---\ndescription: Notes.\n---\n",
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(findings, "no-journal", ".") {
		t.Errorf("expected a no-journal info finding: %+v", findings)
	}
}

// Upgrading a 0.3.0 stock instance marks its classic journal/ and scratch/ in
// place — no rename, the INDEX body is untouched apart from the added key —
// and the result is doctor-clean.
func TestUpgradeMarksClassicDirsInPlace(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock030, ok := StockContract("0.3.0")
	if !ok {
		t.Fatal("no vendored 0.3.0 stock contract")
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock030)
	journalIdx, _ := stockReservedIndexForTest(t, "journal")
	scratchIdx, _ := stockReservedIndexForTest(t, "scratch")
	mustWrite(t, filepath.Join(root, "journal", "INDEX.md"), journalIdx)
	mustWrite(t, filepath.Join(root, "scratch", "INDEX.md"), scratchIdx)

	rep, err := UpgradeContract(root)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(rep.Marked, "journal/INDEX.md") || !containsString(rep.Marked, "scratch/INDEX.md") {
		t.Fatalf("upgrade did not mark classic dirs in place: %+v", rep.Marked)
	}
	if len(rep.Created) != 0 {
		t.Errorf("upgrade created new dirs despite marking existing ones: %+v", rep.Created)
	}
	// No rename: the classic dirs still exist and now resolve via marker.
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Journal != "journal" || rd.Scratch != "scratch" {
		t.Errorf("marked classic dirs did not resolve: %+v", rd)
	}
	// The marker was inserted and the recognizably stock journal companion was
	// refreshed to current naming guidance. Scratch remains untouched.
	got, _ := os.ReadFile(filepath.Join(root, "journal", "INDEX.md"))
	if FrontmatterValueFromReader(strings.NewReader(string(got)), roleKey) != "journal" {
		t.Errorf("journal/INDEX.md did not gain the role marker:\n%s", got)
	}
	if !strings.Contains(string(got), "YYYY-MM-DDTHHMMSSZ-<unique>-<slug>.md") || !containsString(rep.Updated, "journal/INDEX.md") {
		t.Errorf("journal/INDEX.md did not receive current stock guidance:\n%s\nreport: %+v", got, rep)
	}
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Severity == "error" {
			t.Errorf("migrated instance is not doctor-clean: %s %s %s", f.Severity, f.Code, f.Message)
		}
	}
}

// Upgrading with a pre-existing UNMARKED dir named "Journal" (capital J,
// created literally so the string-level guard flags it on Linux too) must not
// claim it: no INDEX written into it, and a collision warning reported.
func TestUpgradeDoesNotClaimCollidingJournalDir(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock030, _ := StockContract("0.3.0")
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock030)
	// A personal diary dir named "Journal" — no INDEX.md, not marked.
	if err := os.MkdirAll(filepath.Join(root, "Journal"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "Journal", "diary.md"), "Dear diary.\n")

	rep, err := UpgradeContract(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Collided) == 0 {
		t.Fatalf("upgrade did not report the Journal collision: %+v", rep)
	}
	if fileExists(filepath.Join(root, "Journal", "INDEX.md")) {
		t.Errorf("upgrade wrote an INDEX.md into the user's Journal/ dir")
	}
	// The reserved default agent-journal/ must not have been created either
	// (its name collides with the classic journal name the guard tests).
	if fileExists(filepath.Join(root, "agent-journal", "INDEX.md")) {
		t.Errorf("upgrade laid down agent-journal/ despite the Journal collision")
	}
}

func stockReservedIndexForTest(t *testing.T, role string) (string, bool) {
	t.Helper()
	s, ok := stockReservedIndex030(role)
	if !ok {
		t.Fatalf("no vendored 0.3.0 stock %s INDEX", role)
	}
	return s, ok
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
