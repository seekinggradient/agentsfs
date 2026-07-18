package hub

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestAPICreateRepoHappyPath covers POST /api/agent/v1/repos end to end: 201
// with the apiRepoJSON shape, a real seeded first commit (AGENTS.md present,
// the caller-supplied description injected into the root INDEX.md in place of
// the REPLACE-ME placeholder), the repo showing up in GET /repos, and a
// follow-up CAS commit against the seeded head succeeding.
func TestAPICreateRepoHappyPath(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")

	body, _ := json.Marshal(map[string]any{"name": "NewKB", "description": "My new knowledge base"})
	var created apiRepoJSON
	code := apiJSON(t, ts, http.MethodPost, "/api/agent/v1/repos", tok, string(body), &created)
	if code != http.StatusCreated {
		t.Fatalf("create = %d, want 201", code)
	}
	if created.Owner != "alice" || created.Name != "newkb" || created.Repo != "newkb" {
		t.Fatalf("created = %+v, want owner=alice name=repo=newkb (lowercased)", created)
	}
	if created.Description != "My new knowledge base" {
		t.Fatalf("description = %q, want 'My new knowledge base'", created.Description)
	}
	if created.Head == "" {
		t.Fatal("head should be non-empty (a real first commit)")
	}
	if created.Role != "owner" {
		t.Fatalf("role = %q, want owner", created.Role)
	}
	if created.Public {
		t.Fatal("a newly created repo must be private by default")
	}
	if !srv.Storage.Exists("alice", "newkb") {
		t.Fatal("repo should exist on disk after create")
	}

	// Contract files present at HEAD.
	code, agentsMD := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/newkb/file?path=AGENTS.md", tok, "")
	if code != http.StatusOK || len(agentsMD) == 0 {
		t.Fatalf("AGENTS.md = %d %q, want 200 non-empty", code, agentsMD)
	}
	code, indexMD := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/newkb/file?path=INDEX.md", tok, "")
	if code != http.StatusOK {
		t.Fatalf("INDEX.md = %d, want 200", code)
	}
	if !strings.Contains(string(indexMD), `description: "My new knowledge base"`) {
		t.Fatalf("INDEX.md = %q, want the description injected into the frontmatter", indexMD)
	}
	if strings.Contains(string(indexMD), "REPLACE ME") {
		t.Fatal("INDEX.md should no longer contain the template placeholder once a description is given")
	}

	// Listed in GET /repos with a matching head + description.
	var repos struct {
		Repos []apiRepoJSON `json:"repos"`
	}
	apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repos", tok, "", &repos)
	var entry *apiRepoJSON
	for i := range repos.Repos {
		if repos.Repos[i].Name == "newkb" {
			entry = &repos.Repos[i]
		}
	}
	if entry == nil {
		t.Fatalf("newkb missing from /repos: %+v", repos.Repos)
	}
	if entry.Head != created.Head {
		t.Fatalf("listed head = %q, want %q", entry.Head, created.Head)
	}
	if entry.Description != "My new knowledge base" {
		t.Fatalf("listed description = %q, want 'My new knowledge base'", entry.Description)
	}

	// A follow-up CAS commit against the seeded head works.
	if code := commitAs(t, ts, tok, "alice", "newkb", created.Head, map[string]string{"notes/first.md": "hello\n"}); code != http.StatusOK {
		t.Fatalf("follow-up commit = %d, want 200", code)
	}
}

// TestAPICreateRepoNoDescriptionKeepsPlaceholder covers the optional
// description: when omitted, the template's REPLACE-ME placeholder ships
// untouched (matching what `afs init` lays down locally), rather than an
// empty or corrupted frontmatter line.
func TestAPICreateRepoNoDescriptionKeepsPlaceholder(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")

	body, _ := json.Marshal(map[string]any{"name": "bare"})
	code, _ := apiDo(t, ts, http.MethodPost, "/api/agent/v1/repos", tok, string(body))
	if code != http.StatusCreated {
		t.Fatalf("create = %d, want 201", code)
	}
	code, indexMD := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/bare/file?path=INDEX.md", tok, "")
	if code != http.StatusOK {
		t.Fatalf("INDEX.md = %d, want 200", code)
	}
	if !strings.Contains(string(indexMD), "REPLACE ME") {
		t.Fatalf("INDEX.md = %q, want the untouched template placeholder when no description is given", indexMD)
	}
}

// TestAPICreateRepoValidation covers name validation (empty / malformed →
// 422, bad JSON → 400) and the duplicate-name conflict (409).
func TestAPICreateRepoValidation(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")

	for _, name := range []string{"", "   ", "Bad Name!", "../etc", ".hidden", "-leading-dash", "a/b"} {
		body, _ := json.Marshal(map[string]any{"name": name})
		if code, _ := apiDo(t, ts, http.MethodPost, "/api/agent/v1/repos", tok, string(body)); code != http.StatusUnprocessableEntity {
			t.Errorf("create name=%q = %d, want 422", name, code)
		}
	}
	if code, _ := apiDo(t, ts, http.MethodPost, "/api/agent/v1/repos", tok, "{not valid json"); code != http.StatusBadRequest {
		t.Errorf("create with malformed json = %d, want 400", code)
	}

	body, _ := json.Marshal(map[string]any{"name": "kb1"})
	if code, _ := apiDo(t, ts, http.MethodPost, "/api/agent/v1/repos", tok, string(body)); code != http.StatusCreated {
		t.Fatalf("create kb1 = %d, want 201", code)
	}
	if !srv.Storage.Exists("alice", "kb1") {
		t.Fatal("kb1 should exist on disk")
	}
	if code, _ := apiDo(t, ts, http.MethodPost, "/api/agent/v1/repos", tok, string(body)); code != http.StatusConflict {
		t.Fatalf("duplicate create = %d, want 409", code)
	}
}

// TestAPICreateRepoCannotSpoofNamespace covers the "caller's own namespace
// only" guarantee: the endpoint has no owner field to spoof (the PAT's
// resolved user is always the owner), so even a body that tries to smuggle
// one in is silently ignored — the repo lands under the caller, never under
// the named target, and the target's namespace is untouched.
func TestAPICreateRepoCannotSpoofNamespace(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	mkUser(t, acc, "bob")

	body, _ := json.Marshal(map[string]any{"name": "sneaky", "owner": "bob"})
	var created apiRepoJSON
	code := apiJSON(t, ts, http.MethodPost, "/api/agent/v1/repos", aliceTok, string(body), &created)
	if code != http.StatusCreated {
		t.Fatalf("create = %d, want 201", code)
	}
	if created.Owner != "alice" {
		t.Fatalf("owner = %q, want alice (an 'owner' field in the body must be ignored)", created.Owner)
	}
	if srv.Storage.Exists("bob", "sneaky") {
		t.Fatal("repo must NOT have been created in bob's namespace")
	}
	if !srv.Storage.Exists("alice", "sneaky") {
		t.Fatal("repo should exist in alice's own namespace")
	}
}

// TestAPICreateRepoGetStillLists confirms the "repos" route dispatch change
// (GET vs POST) didn't disturb the plain listing behavior for a caller who
// sends no body at all.
func TestAPICreateRepoGetStillLists(t *testing.T) {
	ts, _, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	var repos struct {
		User  string        `json:"user"`
		Repos []apiRepoJSON `json:"repos"`
	}
	if code := apiJSON(t, ts, http.MethodGet, "/api/agent/v1/repos", tok, "", &repos); code != http.StatusOK {
		t.Fatalf("GET /repos = %d, want 200", code)
	}
	if repos.User != "alice" || len(repos.Repos) != 0 {
		t.Fatalf("GET /repos = %+v, want alice with an empty list", repos)
	}
}
