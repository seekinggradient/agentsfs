package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// All methods below run under editorMu. Draft content never enters a served
// repository's object database: hiding a Git ref alone would not make it private.
// The private bare repository contains one branch per writer/document session.
// Completed branches remain readable for retries; starting a new session archives
// the previous tip and starts fresh. Shared publication squashes the cumulative
// content delta through RepoCommit, retaining its authorization and CAS checks.
func (s *Server) editorDraftGitDir() string {
	return filepath.Join(s.Storage.Root(), ".editor-drafts.git")
}
func editorDraftKey(owner, repo, path, writer string) string {
	key := sha256.Sum256([]byte(owner + "\x00" + repo + "\x00" + path + "\x00" + writer))
	return hex.EncodeToString(key[:])
}
func editorDraftRef(owner, repo, path, writer string) string {
	return "refs/heads/drafts/" + editorDraftKey(owner, repo, path, writer)
}

// Legacy preview format: retained only for migration into real Git history.
func (s *Server) editorDraftPath(owner, repo, path, writer string) string {
	return filepath.Join(s.Storage.Root(), ".editor-drafts", editorDraftKey(owner, repo, path, writer)+".json")
}
func (s *Server) editorGit(env []string, in io.Reader, args ...string) (string, error) {
	// Explicit fsync also hardens loose objects; Git's platform default can omit
	// them. fsyncMethod avoids macOS's weaker writeout-only default.
	args = append([]string{"-c", "core.fsync=all", "-c", "core.fsyncMethod=fsync"}, args...)
	return gitCmd("git", s.editorDraftGitDir(), env, in, args...)
}
func syncEditorDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (s *Server) ensureEditorDraftGit() error {
	dir := s.editorDraftGitDir()
	if info, err := os.Stat(dir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("editor Git store is not a directory")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.MkdirTemp(s.Storage.Root(), ".editor-git-init-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if _, err = gitCmd("git", temp, nil, nil, "init", "--bare", "--quiet", "--object-format=sha1", "-b", "main"); err != nil {
		return err
	}
	// No remotes, shared object alternates, HTTP receive-pack, or exported refs.
	// Set hardening in config too so operator recovery operations use it as well.
	for _, setting := range [][2]string{{"core.fsync", "all"}, {"core.fsyncMethod", "fsync"}, {"gc.auto", "256"}} {
		if _, err = gitCmd("git", temp, nil, nil, "config", setting[0], setting[1]); err != nil {
			return err
		}
	}
	// Harden the initial config, HEAD, and directory entries too. Object/ref
	// fsync on later saves cannot repair a lost repository initialization.
	if err = filepath.WalkDir(temp, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type().IsRegular() {
			return syncEditorDirectory(path)
		}
		return nil
	}); err != nil {
		return err
	}
	if err = os.Rename(temp, dir); err != nil {
		return err
	}
	return syncEditorDirectory(s.Storage.Root())
}
func (s *Server) editorBranchTip(ref string) (string, error) {
	if _, err := os.Stat(s.editorDraftGitDir()); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	// for-each-ref returns success with empty output for a missing ref, but does
	// not conflate a corrupt/inaccessible store with a new document.
	out, err := s.editorGit(nil, nil, "for-each-ref", "--format=%(objectname)", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
func (s *Server) readEditorDraftCommit(oid string) (*editorDraft, error) {
	meta, err := s.editorGit(nil, nil, "show", oid+":draft.json")
	if err != nil {
		return nil, err
	}
	var d editorDraft
	if err = json.Unmarshal([]byte(meta), &d); err != nil {
		return nil, err
	}
	if d.Content, err = s.editorGit(nil, nil, "show", oid+":content.md"); err != nil {
		return nil, err
	}
	if d.Committed, err = s.editorGit(nil, nil, "show", oid+":base.md"); err != nil {
		return nil, err
	}
	d.tip = oid
	return &d, nil
}
func (s *Server) readEditorDraft(owner, repo, path, writer string) (*editorDraft, error) {
	tip, err := s.editorBranchTip(editorDraftRef(owner, repo, path, writer))
	if err != nil {
		return nil, err
	}
	if tip != "" {
		d, err := s.readEditorDraftCommit(tip)
		if err != nil {
			return nil, err
		}
		if d.Owner != owner || d.Repo != repo || d.Path != path || d.Writer != writer {
			return nil, fmt.Errorf("editor draft identity mismatch")
		}
		return d, nil
	}
	b, err := os.ReadFile(s.editorDraftPath(owner, repo, path, writer))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var d editorDraft
	if err = json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if d.Owner != owner || d.Repo != repo || d.Path != path || d.Writer != writer {
		return nil, fmt.Errorf("legacy editor draft identity mismatch")
	}
	return &d, nil
}
func (s *Server) writeEditorDraft(d *editorDraft) error {
	if err := s.ensureEditorDraftGit(); err != nil {
		return err
	}
	ref := editorDraftRef(d.Owner, d.Repo, d.Path, d.Writer)
	old, err := s.editorBranchTip(ref)
	if err != nil {
		return err
	}
	if old != d.tip {
		return errEditorDraftChanged
	}
	// Content and base are distinct blobs: an unchanged base is deduplicated and
	// per-keystroke metadata never duplicates a whole note inside JSON.
	meta := *d
	meta.Content = ""
	meta.Committed = ""
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	var treeInput strings.Builder
	for _, entry := range []struct{ name, content string }{{"base.md", d.Committed}, {"content.md", d.Content}, {"draft.json", string(encoded)}} {
		blob, err := s.editorGit(nil, strings.NewReader(entry.content), "hash-object", "-w", "--stdin")
		if err != nil {
			return err
		}
		fmt.Fprintf(&treeInput, "100644 blob %s\t%s\n", strings.TrimSpace(blob), entry.name)
	}
	tree, err := s.editorGit(nil, strings.NewReader(treeInput.String()), "mktree")
	if err != nil {
		return err
	}
	tree = strings.TrimSpace(tree)
	parent := old
	if old != "" {
		oldTree, err := s.editorGit(nil, nil, "rev-parse", old+"^{tree}")
		if err != nil {
			return err
		}
		if strings.TrimSpace(oldTree) == tree {
			return nil
		}
		previous, err := s.readEditorDraftCommit(old)
		if err != nil {
			return err
		}
		if !previous.Pending && d.Pending {
			// Archive before moving the authoritative branch. A crash at any point
			// leaves the old session reachable; no multi-ref atomicity is assumed.
			archive := "refs/heads/archive/" + editorDraftKey(d.Owner, d.Repo, d.Path, d.Writer) + "/" + old
			if _, err = s.editorGit(nil, nil, "update-ref", archive, old); err != nil {
				return err
			}
			parent = ""
		}
	}
	message := "Autosave draft"
	if !d.Pending {
		message = "Complete draft"
	}
	message += "\n\nDocument: " + d.Owner + "/" + d.Repo + "/" + d.Path + "\n"
	env := append(os.Environ(), "GIT_AUTHOR_NAME="+d.Writer, "GIT_AUTHOR_EMAIL="+d.Writer+"@users.agentsfs", "GIT_COMMITTER_NAME=agentsfs hub", "GIT_COMMITTER_EMAIL=hub@agentsfs")
	args := []string{"commit-tree", tree, "-F", "-"}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	commit, err := s.editorGit(env, strings.NewReader(message), args...)
	if err != nil {
		return err
	}
	commit = strings.TrimSpace(commit)
	expected := old
	if expected == "" {
		expected = zeroOID
	}
	if _, err = s.editorGit(env, nil, "update-ref", ref, commit, expected); err != nil {
		return err
	}
	d.tip = commit
	return nil
}
func (s *Server) pendingEditorBranches() ([]string, error) {
	if _, err := os.Stat(s.editorDraftGitDir()); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	// Subjects are fixed status markers. Listing commits is cheap and avoids
	// loading the content of every completed document on each five-second tick.
	out, err := s.editorGit(nil, nil, "for-each-ref", "--format=%(objectname) %(subject)", "refs/heads/drafts/")
	if err != nil {
		return nil, err
	}
	var pending []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		oid, subject, ok := strings.Cut(line, " ")
		if ok && subject == "Autosave draft" {
			pending = append(pending, oid)
		}
	}
	return pending, nil
}
func (s *Server) migrateEditorDrafts() error {
	names, err := filepath.Glob(filepath.Join(s.Storage.Root(), ".editor-drafts", "*.json"))
	if err != nil {
		return err
	}
	for _, name := range names {
		b, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		var d editorDraft
		if err = json.Unmarshal(b, &d); err != nil {
			return err
		}
		if name != s.editorDraftPath(d.Owner, d.Repo, d.Path, d.Writer) {
			return fmt.Errorf("legacy editor draft identity mismatch")
		}
		tip, err := s.editorBranchTip(editorDraftRef(d.Owner, d.Repo, d.Path, d.Writer))
		if err != nil {
			return err
		}
		if tip == "" {
			if err = s.writeEditorDraft(&d); err != nil {
				return err
			}
		}
		// Keep the old format as a recovery copy, but never reimport it over Git.
		if err = os.Rename(name, name+".migrated"); err != nil {
			return err
		}
		if err = syncEditorDirectory(filepath.Dir(name)); err != nil {
			return err
		}
	}
	return nil
}

// Plumbing commits do not trigger Git's usual automatic packing. Check every
// minute so large notes are delta-compressed rather than accumulating unbounded
// loose snapshots. Reachable archived sessions are preserved by ordinary GC.
func (s *Server) maintainEditorDraftGit() {
	s.editorMu.Lock()
	defer s.editorMu.Unlock()
	if _, err := os.Stat(s.editorDraftGitDir()); errors.Is(err, os.ErrNotExist) {
		return
	}
	if _, err := s.editorGit(nil, nil, "-c", "gc.auto=256", "gc", "--auto", "--no-detach"); err != nil {
		s.Log.Printf("editor draft Git maintenance: %v", err)
	}
}
