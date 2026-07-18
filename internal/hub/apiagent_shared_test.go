package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Shared-repo (vault) correctness at the agent API layer. This is the top user
// priority ("my wife's agent sometimes couldn't read the vault I shared"): when
// A shares a repo with B via the Hub's per-repo collaborator machinery, B's
// agent PAT must be able to read (and, with write role, commit to) exactly that
// repo — and nothing else of A's.
//
// The shared-repo listing shape (documented for the Eve-side agent): a repo
// shared WITH the caller appears in GET /api/agent/v1/repos as
//
//	{"owner":"<A>","name":"<repo>","repo":"<repo>","description":"…",
//	 "head":"<oid>","role":"read"|"write","public":<bool>}
//
// i.e. `owner` is the SHARING user (not the caller), `name`==`repo` is the bare
// slug (NOT "owner/repo"), and `role` is the granted collaborator role. The
// per-repo routes address it as /repo/<owner>/<repo>/… using that owner+name.

// (a) read collaborator: B lists the repo as read, reads succeed at a pinned
// rev, and /commit is 403.
func TestAPISharedRepoReadCollaborator(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	bobTok := mkUser(t, acc, "bob")
	seedCommit(t, ts, srv, aliceTok, "alice", "vault", "", map[string]string{
		"AGENTS.md":  "---\ndescription: shared vault\n---\n",
		"notes/x.md": "the magicword lives here\n",
	}, nil)

	if err := acc.AddCollaborator("alice", "vault", "bob", "read"); err != nil {
		t.Fatal(err)
	}

	// Listing shape.
	var repos struct {
		Repos []apiRepoJSON `json:"repos"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repos", bobTok, "", &repos)
	var entry *apiRepoJSON
	for i := range repos.Repos {
		if repos.Repos[i].Owner == "alice" && repos.Repos[i].Name == "vault" {
			entry = &repos.Repos[i]
		}
	}
	if entry == nil {
		t.Fatalf("bob's /repos does not include alice/vault: %+v", repos.Repos)
	}
	if entry.Role != "read" || entry.Repo != "vault" || entry.Head == "" {
		t.Fatalf("shared entry = %+v, want role=read repo=vault head!=\"\"", *entry)
	}
	if entry.Description != "shared vault" {
		t.Fatalf("shared entry description = %q, want 'shared vault'", entry.Description)
	}

	// Pin a rev, then read file / tree / search at it — all succeed.
	var res struct {
		Rev string `json:"rev"`
	}
	if code := apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/vault/resolve", bobTok, "", &res); code != http.StatusOK || res.Rev == "" {
		t.Fatalf("bob resolve = %d rev=%q, want 200 non-empty", code, res.Rev)
	}
	reads := []string{
		"/api/agent/v1/repo/alice/vault/file?path=notes/x.md&rev=" + res.Rev,
		"/api/agent/v1/repo/alice/vault/tree?rev=" + res.Rev + "&depth=0",
		"/api/agent/v1/repo/alice/vault/search?q=magicword&rev=" + res.Rev,
	}
	for _, p := range reads {
		if code, _ := apiDo(t, ts, http.MethodGet, p, bobTok, ""); code != http.StatusOK {
			t.Fatalf("bob read %s = %d, want 200", p, code)
		}
	}

	// A read collaborator cannot commit → 403 (not 404: the repo is reachable).
	code := commitAs(t, ts, bobTok, "alice", "vault", res.Rev, map[string]string{"notes/x.md": "tampered\n"})
	if code != http.StatusForbidden {
		t.Fatalf("read-collab bob /commit = %d, want 403", code)
	}
}

// (b) write collaborator: B can commit, and the role shows as write.
func TestAPISharedRepoWriteCollaborator(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	bobTok := mkUser(t, acc, "bob")
	head := seedCommit(t, ts, srv, aliceTok, "alice", "vault", "", map[string]string{"NOTE.md": "v1\n"}, nil)

	if err := acc.AddCollaborator("alice", "vault", "bob", "write"); err != nil {
		t.Fatal(err)
	}

	var repos struct {
		Repos []apiRepoJSON `json:"repos"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repos", bobTok, "", &repos)
	if role := sharedRole(repos.Repos, "alice", "vault"); role != "write" {
		t.Fatalf("bob role on alice/vault = %q, want write", role)
	}

	if code := commitAs(t, ts, bobTok, "alice", "vault", head, map[string]string{"NOTE.md": "v2 by bob\n"}); code != http.StatusOK {
		t.Fatalf("write-collab bob /commit = %d, want 200", code)
	}
	// The write really landed, readable by the owner.
	code, body := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/vault/file?path=NOTE.md", aliceTok, "")
	if code != http.StatusOK || string(body) != "v2 by bob\n" {
		t.Fatalf("alice read after bob's write = %d %q, want 200 'v2 by bob'", code, body)
	}
}

// (c) unshare: every route 404s for B and the repo drops off B's listing.
func TestAPISharedRepoUnshareRevokesAccess(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	bobTok := mkUser(t, acc, "bob")
	head := seedCommit(t, ts, srv, aliceTok, "alice", "vault", "", map[string]string{"NOTE.md": "v1\n"}, nil)
	if err := acc.AddCollaborator("alice", "vault", "bob", "read"); err != nil {
		t.Fatal(err)
	}
	// Sanity: bob can read while shared.
	if code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/vault/resolve", bobTok, ""); code != http.StatusOK {
		t.Fatalf("pre-unshare bob resolve = %d, want 200", code)
	}

	if err := acc.RemoveCollaborator("alice", "vault", "bob"); err != nil {
		t.Fatal(err)
	}

	var repos struct {
		Repos []apiRepoJSON `json:"repos"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repos", bobTok, "", &repos)
	if role := sharedRole(repos.Repos, "alice", "vault"); role != "" {
		t.Fatalf("after unshare bob still lists alice/vault (role %q)", role)
	}
	for _, p := range []string{
		"/api/agent/v1/repo/alice/vault/resolve",
		"/api/agent/v1/repo/alice/vault/file?path=NOTE.md",
		"/api/agent/v1/repo/alice/vault/tree",
		"/api/agent/v1/repo/alice/vault/search?q=v1",
	} {
		if code, _ := apiDo(t, ts, http.MethodGet, p, bobTok, ""); code != http.StatusNotFound {
			t.Errorf("after unshare bob GET %s = %d, want 404", p, code)
		}
	}
	// Commit now 404s (no access), not 403.
	if code := commitAs(t, ts, bobTok, "alice", "vault", head, map[string]string{"NOTE.md": "x\n"}); code != http.StatusNotFound {
		t.Fatalf("after unshare bob /commit = %d, want 404", code)
	}
}

// (d) isolation: a collaborator on one shared repo still sees NONE of the
// sharer's other (unshared) repos, by listing or by direct route.
func TestAPISharedRepoDoesNotLeakOtherRepos(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	bobTok := mkUser(t, acc, "bob")
	seedCommit(t, ts, srv, aliceTok, "alice", "vault", "", map[string]string{"NOTE.md": "shared\n"}, nil)
	seedCommit(t, ts, srv, aliceTok, "alice", "secret", "", map[string]string{"NOTE.md": "private\n"}, nil)
	if err := acc.AddCollaborator("alice", "vault", "bob", "read"); err != nil {
		t.Fatal(err)
	}

	var repos struct {
		Repos []apiRepoJSON `json:"repos"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repos", bobTok, "", &repos)
	if sharedRole(repos.Repos, "alice", "vault") != "read" {
		t.Fatal("bob should see the shared alice/vault")
	}
	if sharedRole(repos.Repos, "alice", "secret") != "" {
		t.Fatalf("bob must NOT see alice's unshared secret repo: %+v", repos.Repos)
	}
	// And the unshared repo 404s on every route (existence never leaks).
	for _, p := range []string{
		"/api/agent/v1/repo/alice/secret/resolve",
		"/api/agent/v1/repo/alice/secret/file?path=NOTE.md",
	} {
		if code, _ := apiDo(t, ts, http.MethodGet, p, bobTok, ""); code != http.StatusNotFound {
			t.Errorf("bob GET unshared %s = %d, want 404", p, code)
		}
	}
	if code := commitAs(t, ts, bobTok, "alice", "secret", "", map[string]string{"NOTE.md": "x\n"}); code != http.StatusNotFound {
		t.Fatalf("bob /commit to unshared repo = %d, want 404", code)
	}
}

// (e) a brand-new user with zero repos gets an empty (but working) /repos and
// thread store — no errors, no leakage.
func TestAPINewUserEmptyReposAndThreads(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	carolTok := mkUser(t, acc, "carol")

	var repos struct {
		User  string        `json:"user"`
		Repos []apiRepoJSON `json:"repos"`
	}
	if code := apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repos", carolTok, "", &repos); code != http.StatusOK {
		t.Fatalf("new user /repos = %d, want 200", code)
	}
	if repos.User != "carol" || len(repos.Repos) != 0 {
		t.Fatalf("new user /repos = %+v, want carol with empty list", repos)
	}

	var threads struct {
		Threads []map[string]any `json:"threads"`
	}
	if code := apiJSON(t, ts, http.MethodGet, "/api/agent/v1/threads", carolTok, "", &threads); code != http.StatusOK {
		t.Fatalf("new user /threads = %d, want 200", code)
	}
	if len(threads.Threads) != 0 {
		t.Fatalf("new user /threads = %+v, want empty", threads.Threads)
	}
	// A missing thread is a clean 404, and the store is writable (empty != broken).
	if code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/thread/newthread01", carolTok, ""); code != http.StatusNotFound {
		t.Fatalf("new user missing thread = %d, want 404", code)
	}
	if code, _ := apiDo(t, ts, http.MethodPut, "/api/agent/v1/thread/newthread01", carolTok, `{"record":{"threadId":"newthread01","title":"hi"}}`); code != http.StatusOK {
		t.Fatalf("new user PUT thread = %d, want 200", code)
	}
}

// Hard constraint: the agent API exposes NO way to delete a Hub repo. Deletion-
// shaped requests all fail and the repo survives. (Repo deletion lives only in
// the web settings handler, which is deliberately session-cookie-only and
// rejects PATs — see webSessionUser.)
func TestAPIHasNoRepoDeletionEndpoint(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	seedCommit(t, ts, srv, aliceTok, "alice", "vault", "", map[string]string{"NOTE.md": "v1\n"}, nil)

	for _, tc := range []struct{ method, path string }{
		{http.MethodDelete, "/api/agent/v1/repo/alice/vault"},
		{http.MethodDelete, "/api/agent/v1/repo/alice/vault/resolve"},
		{http.MethodPost, "/api/agent/v1/repo/alice/vault/delete"},
		{http.MethodDelete, "/api/agent/v1/repos"},
	} {
		code, _ := apiDo(t, ts, tc.method, tc.path, aliceTok, "")
		if code == http.StatusOK {
			t.Errorf("%s %s = 200; a deletion-shaped request must not succeed", tc.method, tc.path)
		}
	}
	if !srv.Storage.Exists("alice", "vault") {
		t.Fatal("repo was deleted through the agent API — must be impossible")
	}
}

// --- shared test helpers ---------------------------------------------------

// commitAs issues a CAS commit of files as tok and returns the status code.
func commitAs(t *testing.T, ts *httptest.Server, tok, owner, repo, baseRev string, files map[string]string) int {
	t.Helper()
	var changes []map[string]any
	for p, c := range files {
		changes = append(changes, map[string]any{"path": p, "content": c})
	}
	body, _ := json.Marshal(map[string]any{
		"repo": owner + "/" + repo, "baseRev": baseRev, "message": "test", "changes": changes,
	})
	code, _ := apiDo(t, ts, http.MethodPost, "/api/agent/v1/commit", tok, string(body))
	return code
}

// sharedRole returns the caller's role on owner/repo within a /repos listing, or
// "" when the repo is absent from the listing.
func sharedRole(repos []apiRepoJSON, owner, repo string) string {
	for _, r := range repos {
		if r.Owner == owner && r.Name == repo {
			return r.Role
		}
	}
	return ""
}
