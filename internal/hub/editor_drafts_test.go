package hub

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func autosaveEditor(t *testing.T, ts *httptest.Server, s *Server, writer, head, content string, revision int64, action string, reconcile bool) *editorDraft {
	t.Helper()
	f := editorForm(s, writer, head, content)
	f.Set("revision", strconv.FormatInt(revision, 10))
	f.Set("action", action)
	f.Set("reconcile", strconv.FormatBool(reconcile))
	f.Set("message", "")
	status, b := editorRequest(t, ts, s, writer, "POST", f, true)
	if status != 200 {
		t.Fatalf("autosave: %d %s", status, b)
	}
	var response struct {
		Draft *editorDraft `json:"draft"`
	}
	if err := json.Unmarshal(b, &response); err != nil || response.Draft == nil {
		t.Fatalf("draft response: %s %v", b, err)
	}
	return response.Draft
}
func TestEditorAutosaveDurabilityAndBatching(t *testing.T) {
	ts, s, bare := editorFixture(t)
	initial := mustGitHead(bare)
	d := autosaveEditor(t, ts, s, "bob", initial, editorSeed+"first", 0, "autosave", false)
	if !d.Pending || mustGitHead(bare) != initial {
		t.Fatal("autosave should persist without committing")
	}
	d = autosaveEditor(t, ts, s, "bob", initial, editorSeed+"second", d.Revision, "autosave", false)
	s.checkpointEditorDrafts(d.Updated.Add(29 * time.Second))
	if mustGitHead(bare) != initial {
		t.Fatal("checkpoint before idle threshold")
	}
	// A new Server sees the same on-disk queue, without a browser being present.
	restarted, err := New(s.Storage, s.Tokens, s.GitBackend)
	if err != nil {
		t.Fatal(err)
	}
	restarted.Accounts = s.Accounts
	restarted.checkpointEditorDrafts(d.Updated.Add(31 * time.Second))
	got, _ := BlobContent("git", bare, "HEAD", "note.md")
	if got != editorSeed+"second" {
		t.Fatal("restart lost the latest writing")
	}
	log, _ := gitCmd("git", bare, nil, nil, "log", "-1", "--format=%an%n%s")
	if !strings.Contains(log, "bob\nEdit note.md") {
		t.Fatalf("authorship/message: %s", log)
	}
	count, _ := gitCmd("git", bare, nil, nil, "rev-list", "--count", initial+"..HEAD")
	if strings.TrimSpace(count) != "1" {
		t.Fatal("edits were not grouped")
	}
	checkpointHead := mustGitHead(bare)
	// Tab still has pre-checkpoint head: its next save builds on its own version.
	d = autosaveEditor(t, ts, restarted, "bob", initial, editorSeed+"third", d.Revision, "autosave", false)
	if d.Head != checkpointHead {
		t.Fatal("autosave did not advance its own base")
	}
	restarted.checkpointEditorDrafts(d.Updated.Add(31 * time.Second))
	got, _ = BlobContent("git", bare, "HEAD", "note.md")
	if got != editorSeed+"third" {
		t.Fatal("continuous session conflicted with itself")
	}
}
func TestEditorAutosaveRevisionConflictsAndRetry(t *testing.T) {
	ts, s, bare := editorFixture(t)
	head := mustGitHead(bare)
	d := autosaveEditor(t, ts, s, "alice", head, "first", 0, "autosave", false)
	retry := autosaveEditor(t, ts, s, "alice", head, "first", 0, "autosave", false)
	if retry.Revision != d.Revision {
		t.Fatal("retry duplicated a save")
	}
	f := editorForm(s, "alice", head, "second tab")
	f.Set("action", "autosave")
	f.Set("revision", "0")
	code, b := editorRequest(t, ts, s, "alice", "POST", f, true)
	if code != 409 || !strings.Contains(string(b), `"draftConflict":true`) {
		t.Fatalf("stale tab: %d %s", code, b)
	}
	_, b = editorRequest(t, ts, s, "bob", "GET", nil, true)
	if strings.Contains(string(b), `"first"`) {
		t.Fatal("private draft leaked to collaborator")
	}
	// Explicitly combining a second tab advances the revision safely.
	d = autosaveEditor(t, ts, s, "alice", head, "first and second tab", d.Revision, "checkpoint", true)
	if d.Pending {
		t.Fatal("explicit milestone not published")
	}
}
func TestEditorAutosaveConflictsAndRevokedAccess(t *testing.T) {
	ts, s, bare := editorFixture(t)
	head := mustGitHead(bare)
	d := autosaveEditor(t, ts, s, "bob", head, "my writing", 0, "autosave", false)
	if _, err := CommitFile("git", bare, "note.md", "someone else", "alice", "Concurrent edit", head); err != nil {
		t.Fatal(err)
	}
	newer := mustGitHead(bare)
	s.checkpointEditorDrafts(d.Updated.Add(time.Minute))
	stored, err := s.readEditorDraft("alice", "notes", "note.md", "bob")
	if err != nil || !stored.Conflict || stored.Content != "my writing" || mustGitHead(bare) != newer {
		t.Fatal("conflict lost writing or changed Git")
	}
	d = autosaveEditor(t, ts, s, "bob", newer, "someone else and my writing", d.Revision, "autosave", true)
	if err := s.Accounts.RemoveCollaborator("alice", "notes", "bob"); err != nil {
		t.Fatal(err)
	}
	s.checkpointEditorDrafts(d.Updated.Add(time.Minute))
	if mustGitHead(bare) != newer {
		t.Fatal("revoked writer published")
	}
	stored, _ = s.readEditorDraft("alice", "notes", "note.md", "bob")
	if !stored.Pending || stored.Error == "" {
		t.Fatal("revoked writer's durable copy was lost")
	}
}
func TestEditorAutosaveMaximumIntervalAndCrashRetry(t *testing.T) {
	ts, s, bare := editorFixture(t)
	head := mustGitHead(bare)
	d := autosaveEditor(t, ts, s, "alice", head, "continuous writing", 0, "autosave", false)
	d.Started = time.Now().Add(-6 * time.Minute)
	if err := s.writeEditorDraft(d); err != nil {
		t.Fatal(err)
	}
	s.checkpointEditorDrafts(time.Now())
	committed := mustGitHead(bare)
	if committed == head {
		t.Fatal("continuous typing never checkpointed")
	}
	// Simulate a crash after Git commit and before updating the draft record.
	if err := s.writeEditorDraft(d); err != nil {
		t.Fatal(err)
	}
	s.checkpointEditorDrafts(time.Now())
	if mustGitHead(bare) != committed {
		t.Fatal("crash retry created duplicate commit")
	}
	stored, _ := s.readEditorDraft("alice", "notes", "note.md", "alice")
	if stored.Pending {
		t.Fatal("crash retry failed to acknowledge existing version")
	}
}
func TestEditorAutosaveUndoCancelsPendingVersion(t *testing.T) {
	ts, s, bare := editorFixture(t)
	head := mustGitHead(bare)
	d := autosaveEditor(t, ts, s, "alice", head, "temporary", 0, "autosave", false)
	d = autosaveEditor(t, ts, s, "alice", head, editorSeed, d.Revision, "autosave", false)
	s.checkpointEditorDrafts(time.Now().Add(time.Minute))
	if d.Pending || mustGitHead(bare) != head {
		t.Fatal("undo to original created a version")
	}
}

func TestEditorAutosaveConcurrentTabs(t *testing.T) {
	ts, s, bare := editorFixture(t)
	head := mustGitHead(bare)
	statuses := make(chan int, 2)
	for _, text := range []string{"tab one", "tab two"} {
		go func(content string) {
			form := editorForm(s, "alice", head, content)
			form.Set("action", "autosave")
			form.Set("revision", "0")
			status, _ := editorRequest(t, ts, s, "alice", "POST", form, true)
			statuses <- status
		}(text)
	}
	first, second := <-statuses, <-statuses
	if !((first == 200 && second == 409) || (first == 409 && second == 200)) {
		t.Fatalf("concurrent draft writes: %d %d", first, second)
	}
}
func TestEditorAutosaveStorageFailureIsNotAcknowledged(t *testing.T) {
	ts, s, bare := editorFixture(t)
	head := mustGitHead(bare)
	if err := os.WriteFile(filepath.Join(s.Storage.Root(), ".editor-drafts"), []byte("unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	form := editorForm(s, "alice", head, "must not claim saved")
	form.Set("action", "autosave")
	form.Set("revision", "0")
	status, _ := editorRequest(t, ts, s, "alice", "POST", form, true)
	if status != 500 || mustGitHead(bare) != head {
		t.Fatal("unpersisted draft acknowledged or committed")
	}
}
