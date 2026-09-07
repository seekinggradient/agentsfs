package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func episodicInstance(t *testing.T) string {
	t.Helper()
	root := newInstance(t, map[string]string{"journal-custom/INDEX.md": "---\ndescription: Project episodes.\nagentsfs_role: journal\n---\n"})
	if _, err := EnsureJournalLayout(root); err != nil {
		t.Fatal(err)
	}
	return root
}
func candidateBootstrap() []byte {
	return []byte("---\ndescription: Recorded workspace history.\n---\n\n## Overview\nA test workspace.\n\n## Chronology\n2026: tested journal behavior.\n\n## Key knowledge\nRead [[design]] for the chosen design.\n")
}

func TestEpisodeResumeCheckpointAndFinish(t *testing.T) {
	root := episodicInstance(t)
	e, err := BeginEpisode(root, "thread/turn-1", "Investigate retries")
	if err != nil {
		t.Fatal(err)
	}
	again, err := BeginEpisode(root, "thread/turn-1", "Repeated start")
	if err != nil || again.Path != e.Path {
		t.Fatalf("resume: %+v %v", again, err)
	}
	e2, err := CheckpointEpisode(root, e.ID, e.Hash, "## Progress\nRetry succeeded after testing.", "running")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CheckpointEpisode(root, e.ID, e.Hash, "Stale overwrite", "complete"); err == nil {
		t.Fatal("accepted stale checkpoint")
	}
	done, err := CheckpointEpisode(root, e.ID, e2.Hash, "## Outcome\nRetry fix verified.", "complete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CheckpointEpisode(root, e.ID, done.Hash, "Rewriting finished work", "running"); err == nil {
		t.Fatal("reopened finished episode")
	}
	plan, err := PrepareJournal(root)
	if err != nil || len(plan.Episodes) != 1 {
		t.Fatalf("plan: %+v %v", plan, err)
	}
}

func TestConcurrentBeginIsIdempotent(t *testing.T) {
	root := episodicInstance(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := BeginEpisode(root, "same-session", "Concurrent retry"); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	all, err := JournalEpisodes(root)
	if err != nil || len(all) != 1 {
		t.Fatalf("duplicates: %v %v", all, err)
	}
}

func TestConsolidationArchivesExactSourcesAndRetainsRunning(t *testing.T) {
	root := episodicInstance(t)
	live, _ := BeginEpisode(root, "live", "Still working")
	e, _ := BeginEpisode(root, "done", "Finished task")
	e, err := CheckpointEpisode(root, e.ID, e.Hash, "## Outcome\nDone, with evidence.", "complete")
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(root, e.Path))
	legacy := "journal-custom/2025-01-01-legacy.md"
	mustWrite(t, filepath.Join(root, legacy), "---\ndescription: Legacy evidence.\n---\nOriginal historical detail.\n")
	plan, err := PrepareJournal(root)
	if err != nil || len(plan.Episodes) != 2 {
		t.Fatalf("plan: %v %v", plan, err)
	}
	if err = ConsolidateJournal(root, plan, candidateBootstrap()); err != nil {
		t.Fatal(err)
	}
	archived, _ := os.ReadFile(filepath.Join(root, "journal-custom/archive", baseName(e.Path)))
	if string(archived) != string(original) {
		t.Fatal("archive changed source bytes")
	}
	all, err := JournalEpisodes(root)
	if err != nil || len(all) != 1 || all[0].ID != live.ID {
		t.Fatalf("running episode lost: %v %v", all, err)
	}
	if _, err = BeginEpisode(root, "done", "Restart duplicate"); err == nil {
		t.Fatal("reused an archived session key")
	}
	if err = ConsolidateJournal(root, plan, candidateBootstrap()); err == nil {
		t.Fatal("replayed stale consolidation")
	}
	next, err := PrepareJournal(root)
	if err != nil || len(next.Episodes) != 0 {
		t.Fatal("archives appear as pending")
	}
}

func TestConsolidationRejectsChangedEvidenceAndOversizeBootstrap(t *testing.T) {
	root := episodicInstance(t)
	e, _ := BeginEpisode(root, "id", "Test")
	e, _ = CheckpointEpisode(root, e.ID, e.Hash, "Unfinished; owner confirmed interruption.", "interrupted")
	plan, _ := PrepareJournal(root)
	e, err := CheckpointEpisode(root, e.ID, e.Hash, "Resumed work.", "running")
	if err != nil {
		t.Fatal(err)
	}
	if err = ConsolidateJournal(root, plan, candidateBootstrap()); err == nil {
		t.Fatal("archived a resumed episode")
	}
	if _, err = os.Stat(filepath.Join(root, e.Path)); err != nil {
		t.Fatal("lost source")
	}
	if err = ValidateBootstrap(append(candidateBootstrap(), []byte(strings.Repeat("long ", 3000))...)); err == nil {
		t.Fatal("accepted oversized bootstrap")
	}
}

func TestConsolidationRecoveryAfterPartialArchive(t *testing.T) {
	root := episodicInstance(t)
	e, _ := BeginEpisode(root, "id", "Test recovery")
	e, _ = CheckpointEpisode(root, e.ID, e.Hash, "Complete result.", "complete")
	plan, _ := PrepareJournal(root)
	tx := journalTransaction{Plan: plan, Bootstrap: string(candidateBootstrap())}
	b, _ := json.Marshal(tx)
	mustWrite(t, filepath.Join(root, "journal-custom/consolidation.json"), string(b))
	mustWrite(t, filepath.Join(root, "journal-custom/bootstrap.md"), tx.Bootstrap)
	if err := os.Rename(filepath.Join(root, e.Path), filepath.Join(root, "journal-custom/archive", baseName(e.Path))); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginEpisode(root, "blocked", "Must recover first"); err == nil {
		t.Fatal("wrote during recovery")
	}
	if err := RecoverJournal(root); err != nil {
		t.Fatal(err)
	}
	if err := RecoverJournal(root); err != nil {
		t.Fatal("recovery not idempotent", err)
	}
	if _, err := os.Stat(filepath.Join(root, "journal-custom/consolidation.json")); !os.IsNotExist(err) {
		t.Fatal("intent not cleared")
	}
}

func TestJournalRejectsArchiveSymlink(t *testing.T) {
	root := episodicInstance(t)
	archive := filepath.Join(root, "journal-custom/archive")
	os.RemoveAll(archive)
	if err := os.Symlink(t.TempDir(), archive); err != nil {
		t.Skip(err)
	}
	if _, err := BeginEpisode(root, "id", "Do not follow links"); err == nil {
		t.Fatal("followed archive symlink")
	}
}

func TestPrimeIncludesHistoryAndEpisodeBodiesWithinEveryBudget(t *testing.T) {
	root := episodicInstance(t)
	mustWrite(t, filepath.Join(root, "journal-custom/bootstrap.md"), string(candidateBootstrap()))
	e, _ := BeginEpisode(root, "id", "Active investigation")
	_, err := CheckpointEpisode(root, e.ID, e.Hash, "## Progress\nDiscovered the cache invalidation bug.", "running")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "journal-custom/archive/2020-secret-detail.md"), "---\ndescription: Old.\n---\nARCHIVED_BODY_MUST_NOT_APPEAR\n")
	pack, err := Prime(root, 4000)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tested journal behavior", "cache invalidation bug", "Start or resume"} {
		if !strings.Contains(pack.Text, want) {
			t.Fatalf("missing %s: %s", want, pack.Text)
		}
	}
	if strings.Contains(pack.Text, "ARCHIVED_BODY_MUST_NOT_APPEAR") {
		t.Fatal("archive treated as recent episode")
	}
	for _, budget := range []int{1, 20, 100, 250, 500, 1000, 2000, 4000} {
		p, err := Prime(root, budget)
		if err != nil {
			t.Fatal(err)
		}
		if p.EstTokens > budget {
			t.Fatalf("budget %d exceeded by %d", budget, p.EstTokens)
		}
	}
}

func TestCheckpointPreservesCustomMetadata(t *testing.T) {
	root := episodicInstance(t)
	e, err := BeginEpisode(root, "custom", "Metadata")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, e.Path))
	b = []byte(strings.Replace(string(b), "---\n", "---\nexternal_reference: keep-me\n", 1))
	mustWrite(t, filepath.Join(root, e.Path), string(b))
	_, err = CheckpointEpisode(root, e.ID, journalHash(b), "Progress preserved.", "complete")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(root, e.Path))
	if !strings.Contains(string(b), "external_reference: keep-me") {
		t.Fatal("lost custom metadata")
	}
}

func TestArchiveCollisionLeavesBootstrapAndSourceUntouched(t *testing.T) {
	root := episodicInstance(t)
	e, _ := BeginEpisode(root, "collision", "Archive collision")
	e, _ = CheckpointEpisode(root, e.ID, e.Hash, "Original evidence.", "complete")
	plan, _ := PrepareJournal(root)
	bootstrap, _ := os.ReadFile(filepath.Join(root, "journal-custom/bootstrap.md"))
	dest := filepath.Join(root, "journal-custom/archive", baseName(e.Path))
	mustWrite(t, dest, "Existing archive")
	if err := ConsolidateJournal(root, plan, candidateBootstrap()); err == nil {
		t.Fatal("overwrote archive")
	}
	after, _ := os.ReadFile(filepath.Join(root, "journal-custom/bootstrap.md"))
	if string(after) != string(bootstrap) {
		t.Fatal("changed bootstrap on failed preflight")
	}
	if _, err := os.Stat(filepath.Join(root, e.Path)); err != nil {
		t.Fatal("lost source")
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "Existing archive" {
		t.Fatal("changed archive")
	}
}

func TestRecoveryRefusesChangedSource(t *testing.T) {
	root := episodicInstance(t)
	e, _ := BeginEpisode(root, "recovery-conflict", "Recovery conflict")
	e, _ = CheckpointEpisode(root, e.ID, e.Hash, "Original evidence.", "complete")
	plan, _ := PrepareJournal(root)
	tx := journalTransaction{Plan: plan, Bootstrap: string(candidateBootstrap())}
	b, _ := json.Marshal(tx)
	mustWrite(t, filepath.Join(root, "journal-custom/consolidation.json"), string(b))
	mustWrite(t, filepath.Join(root, e.Path), "Changed by another writer")
	if err := RecoverJournal(root); err == nil {
		t.Fatal("accepted changed evidence")
	}
	if _, err := os.Stat(filepath.Join(root, "journal-custom/consolidation.json")); err != nil {
		t.Fatal("lost recovery intent")
	}
}

func TestPrepareCreatesFirstBootstrapWithoutInventingOrOverwritingHistory(t *testing.T) {
	root := newInstance(t, map[string]string{"journal-custom/INDEX.md": "---\ndescription: Episodes.\nagentsfs_role: journal\n---\n"})
	plan, err := PrepareJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Episodes) != 0 {
		t.Fatal("invented episodes")
	}
	path := filepath.Join(root, "journal-custom/bootstrap.md")
	starter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = ValidateBootstrap(starter); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(starter), "unsynthesized starter") {
		t.Fatal("starter claims history")
	}
	if err = ConsolidateJournal(root, plan, candidateBootstrap()); err == nil {
		t.Fatal("accepted empty consolidation")
	}
	custom := string(candidateBootstrap()) + "\nAn owner's carefully curated context.\n"
	mustWrite(t, path, custom)
	if _, err = PrepareJournal(root); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != custom {
		t.Fatal("preparation overwrote existing bootstrap")
	}
}
