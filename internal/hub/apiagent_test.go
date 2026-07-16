package hub

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newAPIHub builds an AccountStore-backed hub with a metrics + thread store, the
// setup the hosted-parity agent API needs. It does NOT require git-http-backend
// (the API uses git plumbing directly), so a placeholder backend path skips
// discovery.
func newAPIHub(t *testing.T) (*httptest.Server, *Server, *AccountStore) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	store, err := NewLocalStorage(filepath.Join(dir, "repos"))
	if err != nil {
		t.Fatal(err)
	}
	acc, err := OpenAccounts(filepath.Join(dir, "acc.db"))
	if err != nil {
		t.Fatal(err)
	}
	mets, err := OpenMetrics(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(store, NewTokenStore(), "git-http-backend-placeholder")
	if err != nil {
		t.Fatal(err)
	}
	srv.Accounts = acc
	srv.Metrics = mets
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, srv, acc
}

// mkUser creates an account and returns a PAT for it.
func mkUser(t *testing.T, acc *AccountStore, name string) string {
	t.Helper()
	if _, err := acc.CreateUser(name, name+"@example.com", "pw12345678"); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	tok, err := acc.CreatePAT(name, "test")
	if err != nil {
		t.Fatalf("pat %s: %v", name, err)
	}
	return tok
}

// apiDo issues an authenticated JSON request and returns status + body bytes.
func apiDo(t *testing.T, ts *httptest.Server, method, path, tok, body string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

// apiJSON issues a request and unmarshals the JSON body into v.
func apiJSON(t *testing.T, ts *httptest.Server, method, path, tok, body string, v any) int {
	t.Helper()
	code, b := apiDo(t, ts, method, path, tok, body)
	if v != nil && len(b) > 0 {
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatalf("%s %s: unmarshal %q: %v", method, path, b, err)
		}
	}
	return code
}

// seedCommit creates one commit through the commit API and returns the new head.
// It EnsureRepo's the repo first so the first commit lands in an existing (empty)
// bare repo, exercising the root-commit path.
func seedCommit(t *testing.T, ts *httptest.Server, srv *Server, tok, owner, repo, baseRev string, files map[string]string, deletes []string) string {
	t.Helper()
	if err := srv.Storage.EnsureRepo(owner, repo); err != nil {
		t.Fatal(err)
	}
	var changes []map[string]any
	for p, c := range files {
		changes = append(changes, map[string]any{"path": p, "content": c})
	}
	for _, p := range deletes {
		changes = append(changes, map[string]any{"path": p, "delete": true})
	}
	body, _ := json.Marshal(map[string]any{
		"repo": owner + "/" + repo, "baseRev": baseRev, "message": "seed", "changes": changes,
	})
	var out struct {
		NewHead string `json:"newHead"`
		Merged  bool   `json:"merged"`
	}
	code := apiJSON(t, ts, http.MethodPost, "/api/agent/v1/commit", tok, string(body), &out)
	if code != http.StatusOK {
		t.Fatalf("seed commit = %d, want 200", code)
	}
	if out.NewHead == "" {
		t.Fatal("seed commit returned empty head")
	}
	return out.NewHead
}

// --- auth ------------------------------------------------------------------

func TestAPIAuthRejectsBadAndMissingPAT(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	mkUser(t, acc, "alice")

	for _, tc := range []struct {
		name, tok string
	}{
		{"missing token", ""},
		{"garbage token", "afs_not_a_real_token"},
	} {
		code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repos", tc.tok, "")
		if code != http.StatusUnauthorized {
			t.Errorf("%s: /repos = %d, want 401", tc.name, code)
		}
	}
}

// --- repos listing + isolation --------------------------------------------

func TestAPIListReposScopedToOwnerAndCollaborator(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	bobTok := mkUser(t, acc, "bob")

	seedCommit(t, ts, srv, aliceTok, "alice", "brain", "", map[string]string{"AGENTS.md": "---\ndescription: alice brain\n---\n"}, nil)
	seedCommit(t, ts, srv, aliceTok, "alice", "shared", "", map[string]string{"NOTE.md": "hi\n"}, nil)
	seedCommit(t, ts, srv, bobTok, "bob", "bobrepo", "", map[string]string{"NOTE.md": "b\n"}, nil)

	// Share alice/shared with bob (read).
	if err := acc.AddCollaborator("alice", "shared", "bob", "read"); err != nil {
		t.Fatal(err)
	}

	var aliceRepos struct {
		Repos []apiRepoJSON `json:"repos"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repos", aliceTok, "", &aliceRepos)
	names := map[string]apiRepoJSON{}
	for _, r := range aliceRepos.Repos {
		names[r.Owner+"/"+r.Repo] = r
	}
	if _, ok := names["alice/brain"]; !ok {
		t.Fatal("alice should see her own brain repo")
	}
	if names["alice/brain"].Head == "" {
		t.Fatal("repo listing should include current HEAD")
	}
	if names["alice/brain"].Description != "alice brain" {
		t.Fatalf("description = %q, want 'alice brain'", names["alice/brain"].Description)
	}

	var bobRepos struct {
		Repos []apiRepoJSON `json:"repos"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repos", bobTok, "", &bobRepos)
	bobSees := map[string]string{}
	for _, r := range bobRepos.Repos {
		bobSees[r.Owner+"/"+r.Repo] = r.Role
	}
	if _, ok := bobSees["alice/brain"]; ok {
		t.Fatal("bob must NOT see alice's private brain repo")
	}
	if bobSees["alice/shared"] != "read" {
		t.Fatalf("bob should see alice/shared as read collaborator, got %q", bobSees["alice/shared"])
	}
	if bobSees["bob/bobrepo"] != "owner" {
		t.Fatalf("bob should own bobrepo, got %q", bobSees["bob/bobrepo"])
	}
}

func TestAPIRepoReadIsolation(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	bobTok := mkUser(t, acc, "bob")
	seedCommit(t, ts, srv, aliceTok, "alice", "brain", "", map[string]string{"NOTE.md": "secret\n"}, nil)

	// bob cannot resolve, read, tree, or search alice's private repo → 404 (never
	// leaks existence).
	for _, path := range []string{
		"/api/agent/v1/repo/alice/brain/resolve",
		"/api/agent/v1/repo/alice/brain/file?path=NOTE.md",
		"/api/agent/v1/repo/alice/brain/tree",
		"/api/agent/v1/repo/alice/brain/search?q=secret",
	} {
		code, _ := apiDo(t, ts, http.MethodGet, path, bobTok, "")
		if code != http.StatusNotFound {
			t.Errorf("bob GET %s = %d, want 404", path, code)
		}
	}
	// alice can.
	code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/file?path=NOTE.md", aliceTok, "")
	if code != http.StatusOK {
		t.Fatalf("alice read own file = %d, want 200", code)
	}
}

// --- revision pinning ------------------------------------------------------

func TestAPIRevPinningServesOldRevisionAfterWrite(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	rev1 := seedCommit(t, ts, srv, tok, "alice", "brain", "", map[string]string{"NOTE.md": "version one\n"}, nil)

	// Overwrite NOTE.md at HEAD.
	rev2 := seedCommit(t, ts, srv, tok, "alice", "brain", rev1, map[string]string{"NOTE.md": "version two\n"}, nil)
	if rev1 == rev2 {
		t.Fatal("second commit should advance HEAD")
	}

	// The pinned old revision still serves the old content.
	code, body := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/file?path=NOTE.md&rev="+rev1, tok, "")
	if code != http.StatusOK || string(body) != "version one\n" {
		t.Fatalf("read at rev1 = %d %q, want 200 'version one'", code, body)
	}
	// HEAD serves the new content.
	code, body = apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/file?path=NOTE.md", tok, "")
	if code != http.StatusOK || string(body) != "version two\n" {
		t.Fatalf("read at HEAD = %d %q, want 200 'version two'", code, body)
	}
}

func TestAPIResolveMatchesHead(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	head := seedCommit(t, ts, srv, tok, "alice", "brain", "", map[string]string{"NOTE.md": "x\n"}, nil)
	var out struct{ Head string }
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/resolve", tok, "", &out)
	if out.Head != head {
		t.Fatalf("resolve head = %q, want %q", out.Head, head)
	}
}

// --- file: 404 unknown path, 400 bad rev, path jail ------------------------

func TestAPIFileErrors(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	seedCommit(t, ts, srv, tok, "alice", "brain", "", map[string]string{"NOTE.md": "hi\n"}, nil)

	cases := []struct {
		name, path string
		want       int
	}{
		{"unknown path", "/api/agent/v1/repo/alice/brain/file?path=nope.md", http.StatusNotFound},
		{"bad rev", "/api/agent/v1/repo/alice/brain/file?path=NOTE.md&rev=zzz-not-a-rev", http.StatusBadRequest},
		{"path traversal", "/api/agent/v1/repo/alice/brain/file?path=../../etc/passwd", http.StatusBadRequest},
		{"dotgit path", "/api/agent/v1/repo/alice/brain/file?path=.git/config", http.StatusBadRequest},
		{"absolute path", "/api/agent/v1/repo/alice/brain/file?path=/etc/passwd", http.StatusBadRequest},
		{"empty path", "/api/agent/v1/repo/alice/brain/file", http.StatusBadRequest},
	}
	for _, tc := range cases {
		code, _ := apiDo(t, ts, http.MethodGet, tc.path, tok, "")
		if code != tc.want {
			t.Errorf("%s: %s = %d, want %d", tc.name, tc.path, code, tc.want)
		}
	}
}

// --- tree ------------------------------------------------------------------

func TestAPITree(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	seedCommit(t, ts, srv, tok, "alice", "brain", "", map[string]string{
		"AGENTS.md":      "root\n",
		"notes/a.md":     "a\n",
		"notes/b.md":     "b\n",
		"notes/sub/c.md": "c\n",
	}, nil)

	// depth 1 at root: top-level entries only (AGENTS.md + notes dir).
	var root struct {
		Entries []apiTreeEntry `json:"entries"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/tree?depth=1", tok, "", &root)
	got := map[string]string{}
	for _, e := range root.Entries {
		got[e.Path] = e.Type
	}
	if got["AGENTS.md"] != "blob" || got["notes"] != "tree" {
		t.Fatalf("depth-1 root entries = %+v", root.Entries)
	}
	if _, deep := got["notes/a.md"]; deep {
		t.Fatal("depth-1 must not include nested notes/a.md")
	}

	// dir=notes depth unbounded: sees a.md, b.md, sub/, sub/c.md.
	var sub struct {
		Entries []apiTreeEntry `json:"entries"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/tree?dir=notes&depth=0", tok, "", &sub)
	subGot := map[string]bool{}
	for _, e := range sub.Entries {
		subGot[e.Path] = true
	}
	for _, want := range []string{"notes/a.md", "notes/b.md", "notes/sub/c.md"} {
		if !subGot[want] {
			t.Errorf("tree(dir=notes) missing %q; got %+v", want, sub.Entries)
		}
	}
}

// --- search: rank at HEAD, content at rev, skew flag -----------------------

func TestAPISearchAtRevAndSkew(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	rev1 := seedCommit(t, ts, srv, tok, "alice", "brain", "", map[string]string{
		"target.md": "the quick brown fox\nmagicword lives here\n",
		"other.md":  "unrelated content\n",
	}, nil)

	// At HEAD (no skew): the hit is served at rev == head, exact.
	var s1 struct {
		Skew    bool              `json:"skew"`
		Results []apiSearchResult `json:"results"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/search?q=magicword", tok, "", &s1)
	if s1.Skew {
		t.Fatal("search at HEAD should report skew=false")
	}
	if len(s1.Results) == 0 || s1.Results[0].Path != "target.md" || !s1.Results[0].AtRev {
		t.Fatalf("search results = %+v, want target.md at_rev", s1.Results)
	}
	if !strings.Contains(s1.Results[0].Snippet, "magicword") {
		t.Fatalf("snippet = %q, want it to contain the match", s1.Results[0].Snippet)
	}

	// Advance HEAD by touching an UNRELATED file, then search pinned at rev1.
	seedCommit(t, ts, srv, tok, "alice", "brain", rev1, map[string]string{"other.md": "changed\n"}, nil)
	var s2 struct {
		Skew    bool              `json:"skew"`
		Rev     string            `json:"rev"`
		Head    string            `json:"head"`
		Results []apiSearchResult `json:"results"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/search?q=magicword&rev="+rev1, tok, "", &s2)
	if !s2.Skew {
		t.Fatal("search pinned at an older rev than HEAD should report skew=true")
	}
	if s2.Rev == s2.Head {
		t.Fatal("rev and head should differ under skew")
	}
	// target.md is unchanged between rev1 and HEAD, so its snippet still reads at rev.
	if len(s2.Results) == 0 || !s2.Results[0].AtRev {
		t.Fatalf("skewed search results = %+v, want target.md served at_rev", s2.Results)
	}
}

// --- metering ingest -------------------------------------------------------

func TestAPIUsageRecordsMetricsRow(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")

	// costUSD omitted → recomputed from the price table (gpt-5.1: $2.5/1M in).
	body := `{"model":"gpt-5.1","inputTokens":1000000,"outputTokens":0,"endpoint":"eve.responses","latencyMs":42}`
	code, resp := apiDo(t, ts, http.MethodPost, "/api/agent/v1/usage", tok, body)
	if code != http.StatusOK {
		t.Fatalf("usage ingest = %d, want 200", code)
	}
	if !bytes.Contains(resp, []byte(`"recorded":true`)) {
		t.Fatalf("usage response = %s", resp)
	}

	sm, err := srv.Metrics.Summary(24)
	if err != nil {
		t.Fatal(err)
	}
	if sm.TotalCalls != 1 || sm.TotalInput != 1000000 {
		t.Fatalf("metrics summary = %+v, want 1 call / 1M input", sm)
	}
	if len(sm.Users) != 1 || sm.Users[0].User != "alice" {
		t.Fatalf("metrics user = %+v, want alice", sm.Users)
	}
	if sm.TotalCost != 2.5 {
		t.Fatalf("recomputed cost = %v, want 2.5", sm.TotalCost)
	}
}
