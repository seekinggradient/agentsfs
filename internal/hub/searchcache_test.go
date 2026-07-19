package hub

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"agentsfs.ai/afs/internal/core"
)

// Phase B tests: the per-repo agent search endpoint served by the core
// retrieval pipeline over a sparse, text-only checkout cache of the bare repo.
//
// Each test builds a real bare repo from a COPY of fixtures/insurance-claim by
// committing the fixture into a work repo and pushing it directly to the bare on
// disk (bypassing the hub's HTTP receive-pack seam), so the cache is exercised
// via its LAZY rebuild path — the correctness guarantee — not the push-seam
// optimization.

const statusPath = "projects/water-damage-claim/status.md"

// searchResp mirrors the /search wire envelope, reusing the endpoint's own
// result/pack types so the test asserts against the exact serialized shape.
type searchResp struct {
	Repo    string            `json:"repo"`
	Rev     string            `json:"rev"`
	Head    string            `json:"head"`
	Skew    bool              `json:"skew"`
	Query   string            `json:"query"`
	Results []apiSearchResult `json:"results"`
	Pack    *apiSearchPack    `json:"pack"`
}

// gitRun runs a git subcommand in dir with a hermetic identity, failing the test
// on error.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{
		"-C", dir,
		"-c", "user.name=afs-test",
		"-c", "user.email=afs@test.local",
		"-c", "commit.gpgsign=false",
	}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

// copyFixtureToWork copies the insurance-claim fixture's tracked content into
// work, skipping machine territory (.agentsfs, .git), the LFS .gitattributes,
// and any committed media — the test lays down its own tiny PNG so the
// media-exclusion assertion never depends on git-lfs.
func copyFixtureToWork(t *testing.T, work string) {
	t.Helper()
	src := filepath.Join("..", "..", "fixtures", "insurance-claim")
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("fixture not found: %v", err)
	}
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		if top == ".agentsfs" || top == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == ".gitattributes" {
			return nil
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(rel), ".png") {
			return nil
		}
		target := filepath.Join(work, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// newFixtureRepo materializes the fixture into a work repo and pushes it to a
// fresh bare repo under the server's storage. It returns the work dir and the
// bare dir. A tiny regular-blob PNG (no .gitattributes ⇒ no LFS) is committed so
// media exclusion can be checked.
func newFixtureRepo(t *testing.T, srv *Server, owner, repo string) (work, bare string) {
	t.Helper()
	work = filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFixtureToWork(t, work)
	png := filepath.Join(work, filepath.FromSlash("projects/water-damage-claim/media/kitchen-sink-inspection.png"))
	if err := os.MkdirAll(filepath.Dir(png), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x00fake-image-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	gitRun(t, work, "init", "-q")
	gitRun(t, work, "add", "-A")
	gitRun(t, work, "commit", "-q", "-m", "seed fixture")

	if err := srv.Storage.EnsureRepo(owner, repo); err != nil {
		t.Fatal(err)
	}
	bare = srv.Storage.RepoDir(owner, repo)
	gitRun(t, work, "push", "-q", bare, "HEAD:refs/heads/main")
	if err := srv.Storage.EnsureHEAD(owner, repo); err != nil {
		t.Fatal(err)
	}
	return work, bare
}

// pushChange writes files into the work repo, commits, and pushes to the bare
// (directly, not via the hub HTTP seam), returning the new bare HEAD.
func pushChange(t *testing.T, srv *Server, work, bare, owner, repo string, files map[string]string) string {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, work, "add", "-A")
	gitRun(t, work, "commit", "-q", "-m", "update")
	gitRun(t, work, "push", "-q", bare, "HEAD:refs/heads/main")
	if err := srv.Storage.EnsureHEAD(owner, repo); err != nil {
		t.Fatal(err)
	}
	return headOID("git", bare, defaultRef)
}

func doSearch(t *testing.T, ts *httptest.Server, tok, query, extra string) searchResp {
	t.Helper()
	path := "/api/agent/v1/repo/alice/brain/search?q=" + url.QueryEscape(query) + extra
	var resp searchResp
	if code := apiJSON(t, ts, http.MethodGet, path, tok, "", &resp); code != http.StatusOK {
		t.Fatalf("search %q = %d, want 200", query, code)
	}
	return resp
}

// (a) parity: the natural-language status query ranks status.md first, the same
// document the core eval (internal/core/search_pipeline_test.go) requires.
func TestHubSearchParityWithCorePipeline(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	newFixtureRepo(t, srv, "alice", "brain")

	resp := doSearch(t, ts, tok, "what is the status of the insurance claim", "")
	if len(resp.Results) == 0 {
		t.Fatal("no results for the status query")
	}
	if resp.Results[0].Path != statusPath {
		t.Fatalf("top result = %q, want %q\nresults: %+v", resp.Results[0].Path, statusPath, resp.Results)
	}
	if resp.Skew {
		t.Fatal("a HEAD query should not report skew")
	}
	if !resp.Results[0].AtRev {
		t.Fatalf("top result at HEAD should be at_rev=true: %+v", resp.Results[0])
	}
	if resp.Results[0].Heading == "" {
		t.Fatal("result should carry a section heading")
	}
}

// (b) the cache rebuilds lazily when HEAD advances: a token committed after the
// first query becomes searchable, and the version dir/marker record the new OID.
func TestHubSearchCacheRebuildsOnHeadAdvance(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	work, bare := newFixtureRepo(t, srv, "alice", "brain")

	// Warm the cache at the first head. The new note does not exist yet, so a
	// body match on its unique token cannot appear. (Structural seeds like
	// status.md always surface, so the proof is the new file's presence/rank, not
	// an empty result set.)
	r1 := doSearch(t, ts, tok, "zzqxmarker", "")
	for _, res := range r1.Results {
		if res.Path == "reference/settlement.md" {
			t.Fatalf("settlement.md must not exist before the push: %+v", r1.Results)
		}
	}

	newHead := pushChange(t, srv, work, bare, "alice", "brain", map[string]string{
		"reference/settlement.md": "---\ndescription: settlement terms\n---\n# Settlement\n\nThe zzqxmarker settlement offer was logged.\n",
	})

	// After the push a real body match on the unique token must rank the new note
	// first — above the always-present structural seeds — proving the cache was
	// rebuilt and reindexed from the advanced HEAD.
	r2 := doSearch(t, ts, tok, "zzqxmarker", "")
	if len(r2.Results) == 0 || r2.Results[0].Path != "reference/settlement.md" {
		t.Fatalf("after push, want reference/settlement.md first: %+v", r2.Results)
	}

	dir := srv.search.dirFor("alice", "brain", newHead)
	if !isBuiltCacheDir(dir) {
		t.Fatalf("cache for the new head %s was not built", newHead)
	}
	marker, err := os.ReadFile(filepath.Join(dir, ".agentsfs", cacheMarkerName))
	if err != nil || strings.TrimSpace(string(marker)) != newHead {
		t.Fatalf("marker OID = %q err=%v, want %s", marker, err, newHead)
	}
}

// (c) context mode returns a pack whose top doc is status.md with its full
// "Next actions" section hydrated.
func TestHubSearchContextPack(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	newFixtureRepo(t, srv, "alice", "brain")

	resp := doSearch(t, ts, tok, "what is the status of the insurance claim", "&context=4000")
	if resp.Pack == nil || len(resp.Pack.Docs) == 0 {
		t.Fatal("context mode returned no pack")
	}
	top := resp.Pack.Docs[0]
	if top.Path != statusPath {
		t.Fatalf("top pack doc = %q, want %q", top.Path, statusPath)
	}
	for _, want := range []string{
		"## Next actions",
		"send written dispute of the estimate",
		"Request the itemized line-item breakdown",
	} {
		if !strings.Contains(top.Content, want) {
			t.Fatalf("top doc content missing %q\n---\n%s", want, top.Content)
		}
	}
	if !top.AtRev {
		t.Fatal("a pack doc at HEAD should be at_rev=true")
	}
	if resp.Pack.BudgetUsedEstTokens <= 0 {
		t.Fatal("pack should report a positive estimated-token budget usage")
	}
}

// (d) rev pinning: after HEAD advances, a query pinned at the OLD rev reports
// skew and serves the pack doc's content at that old rev (at_rev=true), never
// the HEAD-only content.
func TestHubSearchContextPackServedAtPin(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	work, bare := newFixtureRepo(t, srv, "alice", "brain")
	head1 := headOID("git", bare, defaultRef)

	orig, err := os.ReadFile(filepath.Join(work, filepath.FromSlash(statusPath)))
	if err != nil {
		t.Fatal(err)
	}
	edited := string(orig) + "\n\nHEADONLYMARKER added at the new head.\n"
	head2 := pushChange(t, srv, work, bare, "alice", "brain", map[string]string{statusPath: edited})
	if head1 == head2 {
		t.Fatal("HEAD did not advance")
	}

	resp := doSearch(t, ts, tok, "what is the status of the insurance claim", "&context=4000&rev="+head1)
	if !resp.Skew {
		t.Fatal("a query pinned at an older rev than HEAD must report skew=true")
	}
	if resp.Rev != head1 || resp.Head != head2 {
		t.Fatalf("rev/head = %s/%s, want %s/%s", resp.Rev, resp.Head, head1, head2)
	}
	if resp.Pack == nil || len(resp.Pack.Docs) == 0 {
		t.Fatal("no pack returned under skew")
	}
	top := resp.Pack.Docs[0]
	if top.Path != statusPath {
		t.Fatalf("top pack doc = %q, want %q", top.Path, statusPath)
	}
	if strings.Contains(top.Content, "HEADONLYMARKER") {
		t.Fatalf("pack doc served at the old rev must not contain the HEAD-only marker\n---\n%s", top.Content)
	}
	if !top.AtRev {
		t.Fatal("pack doc served at the pin should be at_rev=true")
	}
}

// (f) Finding-1 regression: on the skew path serializePack re-reads each pack
// doc at the pin, but must re-shape it to ~the size core served at HEAD rather
// than substituting the whole file. A large pinned doc under a small budget must
// keep both the returned content and the reported budget within ~1.3× the budget.
func TestHubSearchContextPackPinnedRespectsBudget(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	work, bare := newFixtureRepo(t, srv, "alice", "brain")

	// A large document (many KB) that exists at the pin. Substituting it whole
	// (the bug) would overshoot a small budget many-fold.
	big := "---\ndescription: quantumwidget notes\n---\n# Quantumwidget\n\n" +
		strings.Repeat("quantumwidget details and more quantumwidget context. ", 400)
	pinRev := pushChange(t, srv, work, bare, "alice", "brain", map[string]string{
		"reference/quantumwidget.md": big,
	})
	// Advance HEAD past the pin so the request is skewed and serializePack takes
	// the pinned re-read path.
	pushChange(t, srv, work, bare, "alice", "brain", map[string]string{
		"reference/touch.md": "---\ndescription: touch\n---\n# Touch\n\nunrelated content.\n",
	})

	const budget = 200
	resp := doSearch(t, ts, tok, "quantumwidget", "&context="+strconv.Itoa(budget)+"&rev="+pinRev)
	if !resp.Skew {
		t.Fatal("a query pinned before HEAD must report skew")
	}
	if resp.Pack == nil || len(resp.Pack.Docs) == 0 {
		t.Fatal("no pack returned under skew")
	}
	if !resp.Pack.Docs[0].AtRev {
		t.Fatal("the pinned pack doc should be at_rev=true (served from the pin)")
	}
	// The re-shaped pin must not blow the budget — neither the actual returned
	// content nor the recomputed report may exceed ~1.3×.
	limit := budget * 13 / 10
	actual := 0
	for _, d := range resp.Pack.Docs {
		actual += core.EstTokens(d.Path+d.Description+d.Reason) + core.EstTokens(d.Content)
	}
	if actual > limit {
		t.Fatalf("pinned pack served ~%d est tokens, want <= %d (budget %d)", actual, limit, budget)
	}
	if resp.Pack.BudgetUsedEstTokens > limit {
		t.Fatalf("reported budget %d exceeds %d (budget %d)", resp.Pack.BudgetUsedEstTokens, limit, budget)
	}
}

// (g) Finding-5 regression: an unpinned request reads HEAD once and reuses it as
// the served rev, so rev == head and it can never spuriously report skew — even
// right after HEAD advances (both the rev and the skew comparison see the same
// post-push head).
func TestHubSearchUnpinnedRevMatchesHead(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	work, bare := newFixtureRepo(t, srv, "alice", "brain")

	assertUnpinned := func(when string) {
		resp := doSearch(t, ts, tok, "what is the status of the insurance claim", "")
		head := headOID("git", bare, defaultRef)
		if resp.Skew {
			t.Fatalf("%s: unpinned request reported skew (rev=%s head=%s)", when, resp.Rev, resp.Head)
		}
		if resp.Rev != resp.Head || resp.Rev != head {
			t.Fatalf("%s: unpinned rev/head = %s/%s, want both = %s", when, resp.Rev, resp.Head, head)
		}
	}
	assertUnpinned("at first head")

	pushChange(t, srv, work, bare, "alice", "brain", map[string]string{
		"reference/settlement.md": "---\ndescription: settlement terms\n---\n# Settlement\n\nnew note.\n",
	})
	assertUnpinned("after head advance")
}

// (e) media exclusion: a committed .png is never materialized into the cache,
// while the indexable status.md is.
func TestHubSearchCacheExcludesMedia(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	newFixtureRepo(t, srv, "alice", "brain")

	doSearch(t, ts, tok, "status", "") // any query builds the cache

	cacheRepoDir := filepath.Join(srv.Storage.Root(), ".searchcache", "alice", "brain")
	sawStatus, sawPNG := false, false
	if err := filepath.WalkDir(cacheRepoDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(p), ".png") {
			sawPNG = true
		}
		if strings.HasSuffix(p, filepath.FromSlash(statusPath)) {
			sawStatus = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sawPNG {
		t.Fatal("the cache must never contain media (.png)")
	}
	if !sawStatus {
		t.Fatal("the cache should contain the materialized status.md")
	}
}
