package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNewFileFolderContext(t *testing.T) {
	ts, srv, bare := editorFixture(t)
	for _, folder := range []string{"Projects #/Weekly", "../outside", "a/../b", ".git", "a//b", " spaced "} {
		for _, method := range []string{"GET", "POST"} {
			form := url.Values{"folder": {folder}, "name": {"A thought"}, "csrf": {oauthCSRFToken(srv.sessionSecret(), "editor:alice")}}
			target := ts.URL + "/alice/notes/new"
			if method == "GET" {
				target += "?folder=" + url.QueryEscape(folder)
			}
			req, _ := http.NewRequest(method, target, strings.NewReader(form.Encode()))
			req.AddCookie(sessionCookieFor(srv, "alice"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if method == "POST" {
				req.Header.Set("Accept", "application/json")
			}
			res, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			want := 400
			if folder == "Projects #/Weekly" {
				want = 200
				if method == "POST" {
					want = 201
					var result map[string]string
					if err := json.Unmarshal(body, &result); err != nil || result["location"] != "/alice/notes/edit/Projects%20%23/Weekly/A%20thought.md" {
						t.Fatalf("create response: %s", body)
					}
				} else if !strings.Contains(string(body), `name="folder" value="Projects #/Weekly"`) {
					t.Fatalf("folder lost: %s", body)
				}
			}
			if res.StatusCode != want {
				t.Fatalf("%s %q: %d %s", method, folder, res.StatusCode, body)
			}
		}
	}
	if _, ok := BlobContent("git", bare, "HEAD", "Projects #/Weekly/A thought.md"); !ok {
		t.Fatal("file not created in selected folder")
	}
}

func newFileRequest(t *testing.T, ts *httptest.Server, srv *Server, user, repo, method, name, csrf string) (int, string, string) {
	t.Helper()
	form := url.Values{"name": {name}, "csrf": {csrf}}
	req, _ := http.NewRequest(method, ts.URL+"/alice/"+repo+"/new", strings.NewReader(form.Encode()))
	req.AddCookie(sessionCookieFor(srv, user))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := *ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body), res.Header.Get("Location")
}

func TestNewFileCreation(t *testing.T) {
	ts, srv, bare := editorFixture(t)
	csrf := oauthCSRFToken(srv.sessionSecret(), "editor:alice")
	for _, name := range []string{"Meeting notes", "Meetings/Weekly # ?.md", "todo.txt"} {
		status, body, location := newFileRequest(t, ts, srv, "alice", "notes", "POST", name, csrf)
		file := name
		if name == "Meeting notes" {
			file += ".md"
		}
		if status != 303 || location != "/alice/notes/edit/"+escapePathSegments(file) {
			t.Fatalf("create %q: %d %s %s", name, status, location, body)
		}
		if content, ok := BlobContent("git", bare, "HEAD", file); !ok || content != "" {
			t.Fatalf("missing empty file %q", file)
		}
		log, _ := gitCmd("git", bare, nil, nil, "log", "-1", "--format=%an|%s")
		if strings.TrimSpace(log) != "alice|Create "+file {
			t.Fatalf("unexpected creation version: %s", log)
		}
	}
	// A second submission cannot overwrite an existing file, including a file
	// used as a parent folder or a directory used as a new file's name.
	before := mustGitHead(bare)
	for _, name := range []string{"note.md", "note.md/child", "Meetings/Weekly # ?.md/child", "Meeting notes", "Meetings/Weekly # ?.md"} {
		status, _, _ := newFileRequest(t, ts, srv, "alice", "notes", "POST", name, csrf)
		if status != 409 || mustGitHead(bare) != before {
			t.Fatalf("collision %q: status=%d", name, status)
		}
	}
	if content, _ := BlobContent("git", bare, "HEAD", "note.md"); content != editorSeed {
		t.Fatal("existing note changed")
	}
	// Creation also bootstraps an empty workspace.
	if err := srv.Storage.EnsureRepo("alice", "empty"); err != nil {
		t.Fatal(err)
	}
	status, body, _ := newFileRequest(t, ts, srv, "alice", "empty", "POST", "First idea", csrf)
	if status != 303 {
		t.Fatalf("empty workspace: %d %s", status, body)
	}
	if _, ok := BlobContent("git", srv.Storage.RepoDir("alice", "empty"), "HEAD", "First idea.md"); !ok {
		t.Fatal("first file missing")
	}
}

func TestNewFileGuards(t *testing.T) {
	ts, srv, bare := editorFixture(t)
	csrf := oauthCSRFToken(srv.sessionSecret(), "editor:alice")
	before := mustGitHead(bare)
	for _, name := range []string{"", "../escape", "a/../escape", "/absolute.md", ".git", "a/.GiT/file", "a\\b", "a//b", "bad\nname", "-option", strings.Repeat("x", 1025)} {
		status, _, _ := newFileRequest(t, ts, srv, "alice", "notes", "POST", name, csrf)
		if status != 400 {
			t.Fatalf("invalid name %q: %d", name, status)
		}
	}
	for _, method := range []string{"GET", "POST"} {
		status, _, _ := newFileRequest(t, ts, srv, "reader", "notes", method, "Forbidden", oauthCSRFToken(srv.sessionSecret(), "editor:reader"))
		if status != 403 {
			t.Fatalf("read-only %s: %d", method, status)
		}
	}
	status, _, _ := newFileRequest(t, ts, srv, "alice", "notes", "POST", "No CSRF", "")
	if status != 403 {
		t.Fatalf("csrf: %d", status)
	}
	status, _, _ = newFileRequest(t, ts, srv, "alice", "notes", "DELETE", "No delete", csrf)
	if status != 405 || mustGitHead(bare) != before {
		t.Fatal("invalid request mutated repository")
	}
	status, body, _ := newFileRequest(t, ts, srv, "bob", "notes", "GET", "", "")
	if status != 200 || !strings.Contains(body, "Create and write") {
		t.Fatalf("writer form: %d", status)
	}
	status, body, _ = newFileRequest(t, ts, srv, "bob", "notes", "POST", "Collaborator note", oauthCSRFToken(srv.sessionSecret(), "editor:bob"))
	if status != 303 {
		t.Fatalf("writer creation: %d %s", status, body)
	}
	head := mustGitHead(bare)
	if err := repoConfigSet(bare, "afs-hub.repository-mode", repoModeEmbeddedProjectionV1); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		status, _, _ := newFileRequest(t, ts, srv, "alice", "notes", method, "No projection write", csrf)
		if status != 409 || mustGitHead(bare) != head {
			t.Fatalf("legacy projection %s: %d", method, status)
		}
	}
}

func TestCreateOnlyChecksMergedParent(t *testing.T) {
	_, srv, bare := editorFixture(t)
	base := mustGitHead(bare)
	if _, err := CommitFile("git", bare, "folder.md/existing.md", "Keep me", "agent", "Add nested file", base); err != nil {
		t.Fatal(err)
	}
	head := mustGitHead(bare)
	// These changes are disjoint by exact path but would collide structurally.
	for _, name := range []string{"folder.md", "folder.md/existing.md/child.md"} {
		_, err := srv.RepoCommit("alice", apiCommitRequest{Repo: "alice/notes", BaseRev: base, Changes: []apiChange{{Path: name}}, createOnly: true})
		if e, ok := err.(*accessError); !ok || e.status != 409 || mustGitHead(bare) != head {
			t.Fatalf("merged parent collision %q: %v", name, err)
		}
	}
	if _, err := srv.RepoCommit("alice", apiCommitRequest{Repo: "alice/notes", BaseRev: base, Changes: []apiChange{{Path: "folder.md/new.md"}}, createOnly: true}); err != nil {
		t.Fatalf("allowed sibling failed: %v", err)
	}
	if content, _ := BlobContent("git", bare, "HEAD", "folder.md/existing.md"); content != "Keep me" {
		t.Fatal("concurrent file lost")
	}
}
