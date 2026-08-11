package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIV1TransactionCommitsWholeManifestOnce(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)

	if status, body, _ := v1Do(t, ts, http.MethodPost, "/api/v1/instances", token, "", nil); status != http.StatusCreated {
		t.Fatalf("bootstrap: %d %s", status, body)
	}
	bare := srv.Storage.RepoDir("alice", "apps")
	before := srv.RepoResolve("alice", "apps")

	spine := "---\nmarkdownto: backlog@0.1\n---\n\n## Now\n\n- [ ] Cache → [[offline-cache]] ^cache\n"
	note := "---\ndescription: Cache design\n---\n\n# Offline cache\n"
	status, result := v1Transaction(t, ts, token, apiV1TransactionRequest{
		Primary: "apps/tidepool/INDEX.md",
		Message: "Add Tidepool workspace",
		Changes: []apiV1TransactionChange{
			{Path: "apps/tidepool/offline-cache.md", After: stringPointer(note)},
			{Path: "apps/tidepool/INDEX.md", After: stringPointer(spine)},
		},
	})
	if status != http.StatusOK {
		t.Fatalf("transaction: %d %v", status, result)
	}
	if result["rev"] == before || result["primary"] != "apps/tidepool/INDEX.md" {
		t.Fatalf("result = %v", result)
	}
	if url, _ := result["url"].(string); !strings.HasSuffix(url, "/alice/apps/blob/apps/tidepool/INDEX.md") {
		t.Fatalf("workspace URL = %q", url)
	}
	if got, ok := BlobContent("git", bare, defaultRef, "apps/tidepool/INDEX.md"); !ok || got != spine {
		t.Fatalf("spine = %q, %v", got, ok)
	}
	if got, ok := BlobContent("git", bare, defaultRef, "apps/tidepool/offline-cache.md"); !ok || got != note {
		t.Fatalf("note = %q, %v", got, ok)
	}

	// Both paths arrived in the one commit immediately after the bootstrap HEAD.
	changed, err := gitCmd("git", bare, nil, nil, "diff", "--name-only", before, result["rev"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(changed); strings.Join(got, ",") != "apps/tidepool/INDEX.md,apps/tidepool/offline-cache.md" {
		t.Fatalf("one-commit paths = %v", got)
	}
	log, err := gitCmd("git", bare, nil, nil, "log", "-1", "--format=%B")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "Add Tidepool workspace") || !strings.Contains(log, "Via: Markdown To (markdownto.ai)") {
		t.Fatalf("commit message = %q", log)
	}

	files := result["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	for _, raw := range files {
		file := raw.(map[string]any)
		if file["hash"] == "" {
			t.Errorf("file result has no hash: %v", file)
		}
	}
}

func TestAPIV1TransactionConflictWritesNothing(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)

	spine1 := "---\nmarkdownto: backlog@0.1\n---\n\n## Now\n"
	note1 := "# one\n"
	seedStatus, seed := v1Transaction(t, ts, token, apiV1TransactionRequest{
		Primary: "apps/tidepool/INDEX.md",
		Changes: []apiV1TransactionChange{
			{Path: "apps/tidepool/INDEX.md", After: stringPointer(spine1)},
			{Path: "apps/tidepool/note.md", After: stringPointer(note1)},
		},
	})
	if seedStatus != http.StatusOK || seed["rev"] == nil {
		t.Fatalf("seed: %d %v", seedStatus, seed)
	}

	bare := srv.Storage.RepoDir("alice", "apps")
	head := srv.RepoResolve("alice", "apps")
	spine2 := spine1 + "\n- [ ] two\n"
	note2 := "# two\n"
	stale := sourceHash([]byte("not the note we read"))
	correct := sourceHash([]byte(spine1))
	status, conflict := v1Transaction(t, ts, token, apiV1TransactionRequest{
		Primary: "apps/tidepool/INDEX.md",
		Changes: []apiV1TransactionChange{
			{Path: "apps/tidepool/INDEX.md", BeforeHash: &correct, After: &spine2},
			{Path: "apps/tidepool/note.md", BeforeHash: &stale, After: &note2},
		},
	})
	if status != http.StatusPreconditionFailed || conflict["error"] != "manifest conflict" {
		t.Fatalf("conflict: %d %v", status, conflict)
	}
	conflicts := conflict["conflicts"].([]any)
	if len(conflicts) != 1 || conflicts[0].(map[string]any)["path"] != "apps/tidepool/note.md" {
		t.Fatalf("conflicts = %v", conflicts)
	}
	if got := srv.RepoResolve("alice", "apps"); got != head {
		t.Fatalf("refused transaction moved HEAD: %s -> %s", head, got)
	}
	if got, _ := BlobContent("git", bare, defaultRef, "apps/tidepool/INDEX.md"); got != spine1 {
		t.Fatalf("the valid row was partially written: %q", got)
	}
	if got, _ := BlobContent("git", bare, defaultRef, "apps/tidepool/note.md"); got != note1 {
		t.Fatalf("the stale row changed: %q", got)
	}
}

func TestAPIV1TransactionUpdatesAndDeletesInOneCommit(t *testing.T) {
	ts, srv, acc := newAPIHub(t)
	srv.PublicBaseURL = ts.URL
	mkUser(t, acc, "alice")
	token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)

	spine1, note1 := "# Workspace\n", "# Retire me\n"
	seedStatus, seed := v1Transaction(t, ts, token, apiV1TransactionRequest{
		Primary: "apps/tidepool/INDEX.md",
		Changes: []apiV1TransactionChange{
			{Path: "apps/tidepool/INDEX.md", After: &spine1},
			{Path: "apps/tidepool/note.md", After: &note1},
		},
	})
	if seedStatus != http.StatusOK {
		t.Fatalf("seed: %d %v", seedStatus, seed)
	}
	spine2 := "# Workspace v2\n"
	spineHash, noteHash := sourceHash([]byte(spine1)), sourceHash([]byte(note1))
	status, result := v1Transaction(t, ts, token, apiV1TransactionRequest{
		Primary: "apps/tidepool/INDEX.md",
		Changes: []apiV1TransactionChange{
			{Path: "apps/tidepool/INDEX.md", BeforeHash: &spineHash, After: &spine2},
			{Path: "apps/tidepool/note.md", BeforeHash: &noteHash, After: nil},
		},
	})
	if status != http.StatusOK || result["rev"] == seed["rev"] {
		t.Fatalf("update/delete: %d %v", status, result)
	}
	bare := srv.Storage.RepoDir("alice", "apps")
	if got, _ := BlobContent("git", bare, defaultRef, "apps/tidepool/INDEX.md"); got != spine2 {
		t.Fatalf("spine = %q", got)
	}
	if _, exists := BlobContent("git", bare, defaultRef, "apps/tidepool/note.md"); exists {
		t.Fatal("deleted note still exists")
	}
	files := result["files"].([]any)
	if files[1].(map[string]any)["deleted"] != true {
		t.Fatalf("delete result = %v", files[1])
	}
}

func TestAPIV1TransactionRejectsInvalidManifestBeforeBootstrap(t *testing.T) {
	cases := []struct {
		name   string
		req    apiV1TransactionRequest
		status int
		word   string
	}{
		{
			name: "no changes", req: apiV1TransactionRequest{Primary: "apps/tidepool/INDEX.md"},
			status: http.StatusBadRequest, word: "no changes",
		},
		{
			name: "duplicate path",
			req: apiV1TransactionRequest{
				Primary: "apps/tidepool/INDEX.md",
				Changes: []apiV1TransactionChange{
					{Path: "apps/tidepool/INDEX.md", After: stringPointer("# one\n")},
					{Path: "apps/tidepool/INDEX.md", After: stringPointer("# two\n")},
				},
			},
			status: http.StatusBadRequest, word: "duplicate path",
		},
		{
			name: "bad hash",
			req: apiV1TransactionRequest{
				Primary: "apps/tidepool/INDEX.md",
				Changes: []apiV1TransactionChange{{
					Path: "apps/tidepool/INDEX.md", BeforeHash: stringPointer("stale"), After: stringPointer("# one\n"),
				}},
			},
			status: http.StatusBadRequest, word: "bad beforeHash",
		},
		{
			name: "delete without before image",
			req: apiV1TransactionRequest{
				Primary: "apps/tidepool/INDEX.md",
				Changes: []apiV1TransactionChange{{Path: "apps/tidepool/INDEX.md"}},
			},
			status: http.StatusBadRequest, word: "cannot delete absent path",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, srv, acc := newAPIHub(t)
			srv.PublicBaseURL = ts.URL
			mkUser(t, acc, "alice")
			token := mkOAuthToken(t, acc, markdownToClientID, "alice", allV1Scopes)
			status, result := v1Transaction(t, ts, token, tc.req)
			if status != tc.status || !strings.Contains(result["error"].(string), tc.word) {
				t.Fatalf("invalid manifest: %d %v", status, result)
			}
			if srv.Storage.Exists("alice", "apps") {
				t.Fatal("invalid manifest bootstrapped an instance")
			}
		})
	}
}

func v1Transaction(t *testing.T, ts *httptest.Server, token string, req apiV1TransactionRequest) (int, map[string]any) {
	t.Helper()
	status, body, _ := v1Do(t, ts, http.MethodPost, "/api/v1/instances/alice/apps/transactions", token, transactionJSON(t, req), map[string]string{
		"Content-Type": "application/json",
	})
	return status, v1JSON(t, body)
}

func stringPointer(value string) *string { return &value }

func transactionJSON(t *testing.T, req apiV1TransactionRequest) string {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
