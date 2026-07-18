package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// commitAPI POSTs a CAS commit and returns the status and decoded response.
func commitAPI(t *testing.T, ts *httptest.Server, tok, repoSpec, baseRev string, changes []map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"repo": repoSpec, "baseRev": baseRev, "message": "x",
		"author":  map[string]string{"name": "Agent", "email": "agent@example.com"},
		"changes": changes,
	})
	code, b := apiDo(t, ts, http.MethodPost, "/api/agent/v1/commit", tok, string(body))
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return code, out
}

// --- CAS matrix ------------------------------------------------------------

func TestAPICommitFastForward(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	rev1 := seedCommit(t, ts, srv, tok, "alice", "brain", "", map[string]string{"a.md": "1\n"}, nil)

	code, out := commitAPI(t, ts, tok, "alice/brain", rev1,
		[]map[string]any{{"path": "a.md", "content": "2\n"}})
	if code != http.StatusOK {
		t.Fatalf("ff commit = %d (%v), want 200", code, out)
	}
	if out["merged"] != false {
		t.Fatalf("ff commit merged = %v, want false", out["merged"])
	}
	// The Eve client reads `newRev`; `newHead` is the alias. Both must be the
	// same non-empty commit id.
	if out["newRev"] == "" || out["newRev"] == nil || out["newRev"] != out["newHead"] {
		t.Fatalf("commit response newRev/newHead = %v / %v, want equal non-empty", out["newRev"], out["newHead"])
	}
	// New content is live.
	_, body := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/file?path=a.md", tok, "")
	if string(body) != "2\n" {
		t.Fatalf("ff content = %q, want 2", body)
	}
}

func TestAPICommitDisjointMerge(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	rev1 := seedCommit(t, ts, srv, tok, "alice", "brain", "", map[string]string{"a.md": "A\n"}, nil)

	// A concurrent commit advances HEAD by touching b.md.
	seedCommit(t, ts, srv, tok, "alice", "brain", rev1, map[string]string{"b.md": "B\n"}, nil)

	// A stale-based commit touching a DISJOINT file (c.md) must trivially merge.
	code, out := commitAPI(t, ts, tok, "alice/brain", rev1,
		[]map[string]any{{"path": "c.md", "content": "C\n"}})
	if code != http.StatusOK {
		t.Fatalf("disjoint merge = %d (%v), want 200", code, out)
	}
	if out["merged"] != true {
		t.Fatalf("disjoint merge merged = %v, want true", out["merged"])
	}
	// The concurrent b.md AND our c.md both survive at the merged HEAD.
	for path, want := range map[string]string{"a.md": "A\n", "b.md": "B\n", "c.md": "C\n"} {
		_, body := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/file?path="+path, tok, "")
		if string(body) != want {
			t.Errorf("after merge %s = %q, want %q", path, body, want)
		}
	}
}

func TestAPICommitConflict409(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	rev1 := seedCommit(t, ts, srv, tok, "alice", "brain", "", map[string]string{"a.md": "A0\n"}, nil)

	// Concurrent commit changes a.md, advancing HEAD.
	rev2 := seedCommit(t, ts, srv, tok, "alice", "brain", rev1, map[string]string{"a.md": "A1\n"}, nil)

	// A stale-based commit touching the SAME file conflicts.
	code, out := commitAPI(t, ts, tok, "alice/brain", rev1,
		[]map[string]any{{"path": "a.md", "content": "A2\n"}})
	if code != http.StatusConflict {
		t.Fatalf("overlapping commit = %d (%v), want 409", code, out)
	}
	if out["currentHead"] != rev2 {
		t.Fatalf("409 currentHead = %v, want %q", out["currentHead"], rev2)
	}
	cp, _ := out["conflictPaths"].([]any)
	if len(cp) != 1 || cp[0] != "a.md" {
		t.Fatalf("409 conflictPaths = %v, want [a.md]", out["conflictPaths"])
	}
	// The losing write did not land — a.md is still the concurrent value.
	_, body := apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/file?path=a.md", tok, "")
	if string(body) != "A1\n" {
		t.Fatalf("after conflict a.md = %q, want A1 (loser did not apply)", body)
	}
}

func TestAPICommitDelete(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	rev1 := seedCommit(t, ts, srv, tok, "alice", "brain", "", map[string]string{"a.md": "A\n", "b.md": "B\n"}, nil)

	code, _ := commitAPI(t, ts, tok, "alice/brain", rev1,
		[]map[string]any{{"path": "b.md", "delete": true}})
	if code != http.StatusOK {
		t.Fatalf("delete commit = %d, want 200", code)
	}
	code, _ = apiDo(t, ts, http.MethodGet, "/api/agent/v1/repo/alice/brain/file?path=b.md", tok, "")
	if code != http.StatusNotFound {
		t.Fatalf("deleted b.md read = %d, want 404", code)
	}
}

// --- path jail on write ----------------------------------------------------

func TestAPICommitPathJail(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	tok := mkUser(t, acc, "alice")
	rev1 := seedCommit(t, ts, srv, tok, "alice", "brain", "", map[string]string{"a.md": "A\n"}, nil)

	for _, bad := range []string{"../escape.md", "../../etc/passwd", ".git/hooks/post-update", "/abs.md", "a/../../b.md"} {
		code, _ := commitAPI(t, ts, tok, "alice/brain", rev1,
			[]map[string]any{{"path": bad, "content": "x\n"}})
		if code != http.StatusBadRequest {
			t.Errorf("commit path %q = %d, want 400", bad, code)
		}
	}
}

// --- write authorization ---------------------------------------------------

func TestAPICommitWriteAuthorization(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	aliceTok := mkUser(t, acc, "alice")
	bobTok := mkUser(t, acc, "bob")
	rev1 := seedCommit(t, ts, srv, aliceTok, "alice", "brain", "", map[string]string{"a.md": "A\n"}, nil)

	// Stranger: 404 (no access at all, existence not leaked).
	code, _ := commitAPI(t, ts, bobTok, "alice/brain", rev1,
		[]map[string]any{{"path": "a.md", "content": "hax\n"}})
	if code != http.StatusNotFound {
		t.Fatalf("stranger write = %d, want 404", code)
	}

	// Read collaborator: 403 (found, but read-only).
	if err := acc.AddCollaborator("alice", "brain", "bob", "read"); err != nil {
		t.Fatal(err)
	}
	code, _ = commitAPI(t, ts, bobTok, "alice/brain", rev1,
		[]map[string]any{{"path": "a.md", "content": "hax\n"}})
	if code != http.StatusForbidden {
		t.Fatalf("read-collab write = %d, want 403", code)
	}

	// Write collaborator: 200.
	if err := acc.AddCollaborator("alice", "brain", "bob", "write"); err != nil {
		t.Fatal(err)
	}
	code, _ = commitAPI(t, ts, bobTok, "alice/brain", rev1,
		[]map[string]any{{"path": "shared-by-bob.md", "content": "hi\n"}})
	if code != http.StatusOK {
		t.Fatalf("write-collab write = %d, want 200", code)
	}
}
