package hub

import (
	"os"
	"testing"
	"time"
)

// TestPerfBreakdown times the pieces of a repo-page render against a real bare
// repo (set AFS_PERF_REPO to its path). Skipped in normal runs; use it with a
// large knowledge base to see where a slow page spends its time.
func TestPerfBreakdown(t *testing.T) {
	bare := os.Getenv("AFS_PERF_REPO")
	if bare == "" {
		t.Skip("set AFS_PERF_REPO to a bare repo path")
	}
	step := func(name string, fn func()) {
		t0 := time.Now()
		fn()
		t.Logf("%-28s %v", name, time.Since(t0))
	}

	var files []RepoFile
	step("RepoSnapshot", func() { files, _ = RepoSnapshot("git", bare, "HEAD") })
	t.Logf("%-28s %d files", "  (size)", len(files))

	var entries []repoTreeEntry
	step("repoTreeEntries", func() { entries, _ = repoTreeEntries("git", bare, "HEAD") })
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	var contents map[string][]byte
	step("lastCommitTimes", func() { lastCommitTimes("git", bare, "HEAD", paths) })
	step("markdownBlobContents", func() { contents = markdownBlobContents("git", bare, entries) })
	step("buildRepoGraphFrom", func() { BuildRepoGraph("git", bare, "HEAD", "u", "r", files) })
	step("buildTree", func() { buildTree(files, "u", "r") })

	oid := headOID("git", bare, "HEAD")
	var view *repoView
	step("buildRepoView (cold)", func() { view, _ = buildRepoView("git", bare, oid, "u", "r", nil) })
	step("buildRepoView (incremental)", func() { _, _ = buildRepoView("git", bare, oid, "u", "r", view) })
	_ = contents
}
