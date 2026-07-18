package hub

import (
	"net/http"
	"testing"
)

// TestAPIPublicRepoReadViaAgentAPI covers the read-parity change: an agent's
// permissions are exactly its user's permissions, so a signed-in user who can
// already read a public repo in the browser can have their agent read it too
// — but only when it's named explicitly (owner/repo); writes stay forbidden,
// and the public repo never enters the caller's ambient /repos listing.
func TestAPIPublicRepoReadViaAgentAPI(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	bobTok := mkUser(t, acc, "bob")
	head := seedCommit(t, ts, srv, aliceTok, "alice", "pubkb", "", map[string]string{
		"AGENTS.md":  "---\ndescription: public kb\n---\n",
		"notes/x.md": "the magicword lives here\n",
	}, nil)
	if err := srv.setVisibility("alice", "pubkb", visPublic); err != nil {
		t.Fatal(err)
	}

	// bob (a non-collaborator) can read the named public repo: resolve, file,
	// tree, search all succeed.
	reads := []string{
		"/api/agent/v1/repo/alice/pubkb/resolve",
		"/api/agent/v1/repo/alice/pubkb/file?path=notes/x.md",
		"/api/agent/v1/repo/alice/pubkb/tree",
		"/api/agent/v1/repo/alice/pubkb/search?q=magicword",
	}
	for _, p := range reads {
		if code, _ := apiDo(t, ts, http.MethodGet, p, bobTok, ""); code != http.StatusOK {
			t.Errorf("bob read public %s = %d, want 200", p, code)
		}
	}

	// bob still cannot write — 403 (not 404: the repo IS reachable, just not
	// writable by a non-collaborator).
	if code := commitAs(t, ts, bobTok, "alice", "pubkb", head, map[string]string{"notes/x.md": "tampered\n"}); code != http.StatusForbidden {
		t.Fatalf("bob write to public repo = %d, want 403", code)
	}

	// The public repo does NOT enter bob's ambient listing — named-only reach,
	// never discovery.
	var repos struct {
		Repos []apiRepoJSON `json:"repos"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repos", bobTok, "", &repos)
	for _, r := range repos.Repos {
		if r.Owner == "alice" && r.Repo == "pubkb" {
			t.Fatalf("bob's /repos must NOT list alice's public repo (named-only reach): %+v", repos.Repos)
		}
	}

	// A private repo of alice's (never made public) still 404s for bob — the
	// read-parity change must not have widened anything else.
	seedCommit(t, ts, srv, aliceTok, "alice", "privatekb", "", map[string]string{"NOTE.md": "secret\n"}, nil)
	if code, _ := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/privatekb/resolve", bobTok, ""); code != http.StatusNotFound {
		t.Fatalf("bob read alice's private repo = %d, want 404", code)
	}
}
