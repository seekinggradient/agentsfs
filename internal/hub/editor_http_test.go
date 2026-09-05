package hub

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const editorSeed = "---\ndescription: A place to think together.\ntags: [writing, research]\n---\n# A little room to think\n\nGood notes make room for an idea to grow. Start with what you know, follow what surprises you, and leave a trail for the next person.\n\n## What we’re exploring\n\nWriting is a way of discovering what matters. A useful knowledge base connects **clear thinking** with small, deliberate actions.\n\n* Gather observations before reaching conclusions.\n* Link the new idea to [[research|something you already know]].\n* Make the next step obvious.\n\n## Next steps\n\n- [x] Find a question worth asking\n- [ ] Talk to someone with a different perspective\n- [ ] Write down what changed your mind\n\n> The best note is one that helps someone think.\n"

func editorFixture(t *testing.T) (*httptest.Server, *Server, string) {
	t.Helper()
	ts, srv, acc := newDeleteTestServer(t)
	for _, user := range []string{"alice", "bob", "reader"} {
		if _, err := acc.CreateUser(user, "", "pw12345678"); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.Storage.EnsureRepo("alice", "notes"); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "work")
	runGitT(t, "", "init", "-b", "main", work)
	writeRepoFile(t, work, "note.md", editorSeed)
	writeRepoFile(t, work, "special # ?.md", "# Special path\n")
	writeRepoFile(t, work, "advanced.md", "---\ndescription: Advanced\n---\n# Advanced note\n\n<details><summary>Details</summary>Keep this HTML.</details>\n\n$$x^2$$\n")
	runGitT(t, work, "add", "-A")
	runGitT(t, work, "commit", "-m", "Seed notes")
	bare := srv.Storage.RepoDir("alice", "notes")
	runGitT(t, work, "push", bare, "main")
	acc.AddCollaborator("alice", "notes", "bob", "write")
	acc.AddCollaborator("alice", "notes", "reader", "read")
	return ts, srv, bare
}
func editorRequest(t *testing.T, ts *httptest.Server, srv *Server, user, method string, form url.Values, asJSON bool) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+"/alice/notes/edit/note.md", strings.NewReader(form.Encode()))
	req.AddCookie(sessionCookieFor(srv, user))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if asJSON {
		req.Header.Set("Accept", "application/json")
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, body
}
func editorForm(srv *Server, user, head, content string) url.Values {
	return url.Values{"csrf": {oauthCSRFToken(srv.sessionSecret(), "editor:"+user)}, "head": {head}, "content": {content}, "message": {"Clarify next steps"}}
}
func TestEditorVersionLifecycle(t *testing.T) {
	ts, srv, bare := editorFixture(t)
	status, body := editorRequest(t, ts, srv, "alice", "GET", nil, true)
	var snapshot map[string]string
	json.Unmarshal(body, &snapshot)
	if status != 200 || snapshot["content"] != editorSeed || snapshot["head"] != mustGitHead(bare) {
		t.Fatalf("snapshot: %d %s", status, body)
	}
	initial := snapshot["head"]
	// Unrelated agent changes land without blocking this note or being overwritten.
	if _, err := CommitFile("git", bare, "other.md", "Other work\n", "agent", "Other note", initial); err != nil {
		t.Fatal(err)
	}
	draft := editorSeed + "\nA new idea.\n"
	form := editorForm(srv, "bob", initial, draft)
	status, body = editorRequest(t, ts, srv, "bob", "POST", form, true)
	if status != 200 || !strings.Contains(string(body), `"merged":true`) {
		t.Fatalf("save: %d %s", status, body)
	}
	got, _ := BlobContent("git", bare, "HEAD", "other.md")
	if got != "Other work\n" {
		t.Fatal("unrelated work lost")
	}
	log, _ := gitCmd("git", bare, nil, nil, "log", "-1", "--format=%an|%cn|%s")
	if strings.TrimSpace(log) != "bob|agentsfs hub|Clarify next steps" {
		t.Fatalf("identity: %s", log)
	}
	after := mustGitHead(bare)
	// A lost success response is safely retryable with the original revision.
	status, body = editorRequest(t, ts, srv, "bob", "POST", form, true)
	if status != 200 || mustGitHead(bare) != after {
		t.Fatalf("retry manufactured a version: %d %s", status, body)
	}
	// Overlapping changes are rejected repeatedly; HTML errors retain the old base.
	form = editorForm(srv, "alice", initial, "My stale draft\n")
	for i := 0; i < 2; i++ {
		status, body = editorRequest(t, ts, srv, "alice", "POST", form, true)
		if status != 409 || !strings.Contains(string(body), `"conflict":true`) {
			t.Fatalf("conflict: %d %s", status, body)
		}
	}
	status, body = editorRequest(t, ts, srv, "alice", "POST", form, false)
	if status != 409 || !strings.Contains(string(body), `name="head" value="`+initial+`"`) || !strings.Contains(string(body), "My stale draft") {
		t.Fatalf("fallback lost base or draft: %d %s", status, body)
	}
	if mustGitHead(bare) != after {
		t.Fatal("conflict changed history")
	}
}
func TestEditorWriteGuards(t *testing.T) {
	ts, srv, bare := editorFixture(t)
	head := mustGitHead(bare)
	cases := []struct {
		name, user, method string
		form               url.Values
		want               int
	}{
		{"reader", "reader", "POST", editorForm(srv, "reader", head, "x"), 403},
		{"missing base", "alice", "POST", editorForm(srv, "alice", "", "x"), 400},
		{"symbolic base", "alice", "POST", editorForm(srv, "alice", "HEAD", "x"), 400},
		{"missing csrf", "alice", "POST", url.Values{"head": {head}, "content": {"x"}}, 403},
		{"wrong csrf", "bob", "POST", editorForm(srv, "alice", head, "x"), 403},
		{"NUL", "alice", "POST", editorForm(srv, "alice", head, "x\x00"), 400},
		{"large note", "alice", "POST", editorForm(srv, "alice", head, strings.Repeat("x", maxEditorBytes+1)), 400},
		{"method", "alice", "DELETE", nil, 405},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := editorRequest(t, ts, srv, tc.user, tc.method, tc.form, true)
			if status != tc.want {
				t.Fatalf("status %d want %d: %s", status, tc.want, body)
			}
			if mustGitHead(bare) != head {
				t.Fatal("rejected write changed history")
			}
		})
	}
	if err := repoConfigSet(bare, "afs-hub.repository-mode", repoModeEmbeddedProjectionV1); err != nil {
		t.Fatal(err)
	}
	status, _ := editorRequest(t, ts, srv, "alice", "POST", editorForm(srv, "alice", head, "x"), true)
	if status != 409 || mustGitHead(bare) != head {
		t.Fatal("legacy projection was writable")
	}
}

// Opt-in browser fixture: temporary repos and automatic auth, loopback only.
// This is test code, never included in the shipped Hub binary.
func TestEditorBrowserFixture(t *testing.T) {
	if os.Getenv("AFS_EDITOR_BROWSER_FIXTURE") != "1" {
		t.Skip("opt-in browser fixture")
	}
	_, srv, _ := editorFixture(t)
	port := "3347"
	if configured := os.Getenv("AFS_EDITOR_BROWSER_PORT"); configured != "" {
		n, err := strconv.Atoi(configured)
		if err != nil || n < 1 || n > 65535 {
			t.Fatal("invalid preview port")
		}
		port = configured
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Editor fixture ready at http://127.0.0.1:" + port + "/alice/notes/edit/note.md")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(sessionCookieFor(srv, "alice"))
		srv.ServeHTTP(w, r)
	})
	if err := http.Serve(listener, handler); err != nil {
		t.Fatal(err)
	}
}
