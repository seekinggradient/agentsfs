package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Editor drafts are private, durable working copies outside the Git namespace.
// One revisioned copy per writer/note prevents two tabs from silently clobbering
// each other. This store, like the Hub's local repositories, has one server writer.
type editorDraft struct {
	Owner      string    `json:"owner"`
	Repo       string    `json:"repo"`
	Path       string    `json:"path"`
	Writer     string    `json:"writer"`
	Revision   int64     `json:"revision"`
	Head       string    `json:"head"`
	ClientHead string    `json:"clientHead"`
	Content    string    `json:"content"`
	Committed  string    `json:"committed"`
	Updated    time.Time `json:"updated"`
	Started    time.Time `json:"started"`
	Pending    bool      `json:"pending"`
	Error      string    `json:"error,omitempty"`
	Conflict   bool      `json:"conflict,omitempty"`
}

func (s *Server) editorDraftPath(owner, repo, path, writer string) string {
	key := sha256.Sum256([]byte(owner + "\x00" + repo + "\x00" + path + "\x00" + writer))
	return filepath.Join(s.Storage.Root(), ".editor-drafts", hex.EncodeToString(key[:])+".json")
}
func (s *Server) readEditorDraft(owner, repo, path, writer string) (*editorDraft, error) {
	b, err := os.ReadFile(s.editorDraftPath(owner, repo, path, writer))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d editorDraft
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return &d, nil
}
func (s *Server) writeEditorDraft(d *editorDraft) error {
	name := s.editorDraftPath(d.Owner, d.Repo, d.Path, d.Writer)
	dir := filepath.Dir(name)
	if err := os.Mkdir(dir, 0700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
	} else {
		parent, err := os.Open(filepath.Dir(dir))
		if err != nil {
			return err
		}
		err = parent.Sync()
		parent.Close()
		if err != nil {
			return err
		}
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".saving-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), name); err != nil {
		return err
	}
	// Acknowledgment follows both file and directory durability, not just a write.
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// RunEditorAutosave checkpoints durable drafts even after a browser closes or
// the Hub restarts. The caller owns its lifetime. Published commits never amend.
func (s *Server) RunEditorAutosave(ctx context.Context) {
	s.checkpointEditorDrafts(time.Now())
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.checkpointEditorDrafts(now)
		}
	}
}
func (s *Server) checkpointEditorDrafts(now time.Time) {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	names, err := filepath.Glob(filepath.Join(s.Storage.Root(), ".editor-drafts", "*.json"))
	if err != nil {
		s.Log.Printf("editor drafts: %v", err)
		return
	}
	for _, name := range names {
		b, err := os.ReadFile(name)
		if err != nil {
			s.Log.Printf("editor draft read: %v", err)
			continue
		}
		var d editorDraft
		if err = json.Unmarshal(b, &d); err != nil {
			s.Log.Printf("editor draft decode: %v", err)
			continue
		}
		if !d.Pending || d.Conflict || (now.Sub(d.Updated) < 30*time.Second && now.Sub(d.Started) < 5*time.Minute) {
			continue
		}
		s.checkpointEditorDraft(&d, "")
		if err := s.writeEditorDraft(&d); err != nil {
			s.Log.Printf("editor draft checkpoint persistence: %v", err)
		}
	}
}

// Caller holds editorMu across Git's compare-and-swap and the durable draft
// update. A crash between them is safe: an identical Git blob is an idempotent save.
func (s *Server) checkpointEditorDraft(d *editorDraft, message string) {
	if !s.canWrite(d.Owner, d.Repo, d.Writer) || !s.hubWritesAllowed(d.Owner, d.Repo) {
		d.Error = "Your draft is saved privately. Publishing needs write access to this note."
		return
	}
	bare := s.Storage.RepoDir(d.Owner, d.Repo)
	head := mustGitHead(bare)
	current, exists := BlobContent("git", bare, head, d.Path)
	if exists && current == d.Content {
		d.Head, d.Committed, d.Pending, d.Error, d.Conflict = head, d.Content, false, "", false
		return
	}
	if message == "" {
		message = "Edit " + d.Path
	}
	result, err := s.RepoCommit(d.Writer, apiCommitRequest{Repo: d.Owner + "/" + d.Repo, BaseRev: d.Head, Message: message, Changes: []apiChange{{Path: d.Path, Content: d.Content}}})
	if err != nil {
		d.Error = "Your draft is saved privately. The version could not be published; we will retry."
		if _, ok := err.(*conflictError); ok {
			d.Conflict = true
			d.Error = "Your draft is saved privately. Review the newer note before publishing."
		}
		s.Log.Printf("editor checkpoint %s/%s %s: %v", d.Owner, d.Repo, d.Path, err)
		return
	}
	d.Head, d.Committed, d.Pending, d.Error, d.Conflict = result.NewRev, d.Content, false, "", false
}

var errEditorDraftChanged = fmt.Errorf("another tab saved a newer draft")

func (s *Server) saveEditorDraft(owner, repo, path, writer, head, content string, revision int64, reconcile bool, message string, checkpoint bool) (*editorDraft, error) {
	// Caller holds editorMu. A revision is stable across background checkpoints,
	// but every accepted content change advances it, including changes back to base.
	clientHead := head
	d, err := s.readEditorDraft(owner, repo, path, writer)
	if err != nil {
		return nil, err
	}
	if d != nil && d.Revision != revision {
		// A response may have been lost. Only an exact retry is acknowledged.
		if d.Revision == revision+1 && d.Content == content {
			if checkpoint {
				s.checkpointEditorDraft(d, message)
				if err := s.writeEditorDraft(d); err != nil {
					return nil, err
				}
			}
			return d, nil
		}
		return d, errEditorDraftChanged
	}
	if d == nil && revision != 0 {
		return nil, errEditorDraftChanged
	}
	if d == nil || !d.Pending || reconcile {
		committed, ok := BlobContent("git", s.Storage.RepoDir(owner, repo), head, path)
		if !ok {
			return nil, fmt.Errorf("original note unavailable")
		}
		// A checkpoint can advance this draft's head while the tab still holds the
		// earlier one. Keep the acknowledged checkpoint as its merge base.
		if d != nil && !reconcile && d.Committed == d.Content && head == d.ClientHead {
			head, committed = d.Head, d.Committed
		}
		oldRevision := revision
		d = &editorDraft{Owner: owner, Repo: repo, Path: path, Writer: writer, Head: head, Committed: committed, Revision: oldRevision}
	}
	if d.Content != content || reconcile || d.Revision == 0 {
		d.Revision++
	}
	wasPending := d.Pending
	d.Content, d.Updated, d.ClientHead = content, time.Now(), clientHead
	d.Pending = content != d.Committed
	if d.Pending && (!wasPending || d.Started.IsZero()) {
		d.Started = d.Updated
	}
	if reconcile {
		d.Error = ""
		d.Conflict = false
	}
	if err = s.writeEditorDraft(d); err != nil {
		return nil, err
	}
	if checkpoint {
		s.checkpointEditorDraft(d, message)
		if err = s.writeEditorDraft(d); err != nil {
			return nil, err
		}
	}
	return d, nil
}
