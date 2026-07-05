// Package hub is the agentsfs Hub: a hosted storage layer that serves real
// git repositories over HTTP so agents can `git push`/`git clone` a
// user-owned knowledge base. Phase 0 (Option A) runs the real
// git-http-backend CGI over bare repos on local disk; the Storage interface
// is the seam where an R2/S3-backed backend plugs in later without touching
// the server. Because what is stored is genuine git, `git clone` stays a
// byte-for-byte exit ramp — no invented format is ever the source of truth.
package hub

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Storage abstracts where bare git repositories live. The server never
// touches disk directly; everything goes through this interface so the
// local-filesystem backend can be swapped for an R2/S3-backed one later.
type Storage interface {
	// Root is the directory handed to git-http-backend as GIT_PROJECT_ROOT.
	// The on-disk layout under it must be <user>/<repo>.git.
	Root() string
	// RepoDir returns the absolute path to the bare repo for user/repo,
	// whether or not it exists yet.
	RepoDir(user, repo string) string
	// Exists reports whether the bare repo already exists on disk.
	Exists(user, repo string) bool
	// EnsureRepo creates the bare repo if absent, configured to accept HTTP
	// pushes. Idempotent.
	EnsureRepo(user, repo string) error
	// ListRepos returns the repo names (without the .git suffix) a user owns,
	// sorted. An unknown user yields an empty list, not an error.
	ListRepos(user string) ([]string, error)
	// RenameRepo changes a repo's slug within a user's namespace. It fails if
	// the target slug is already taken (duplicate check).
	RenameRepo(user, oldName, newName string) error
}

// LocalStorage keeps bare repos under Dir as <Dir>/<user>/<repo>.git.
type LocalStorage struct {
	Dir     string
	GitPath string // git binary; defaults to "git"
	mu      sync.Mutex
}

// NewLocalStorage creates (if needed) and returns a filesystem-backed Storage.
func NewLocalStorage(dir string) (*LocalStorage, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &LocalStorage{Dir: abs, GitPath: "git"}, nil
}

func (s *LocalStorage) Root() string { return s.Dir }

func (s *LocalStorage) RepoDir(user, repo string) string {
	return filepath.Join(s.Dir, user, repo+".git")
}

func (s *LocalStorage) Exists(user, repo string) bool {
	info, err := os.Stat(s.RepoDir(user, repo))
	return err == nil && info.IsDir()
}

func (s *LocalStorage) EnsureRepo(user, repo string) error {
	// Serialize creation so two concurrent first-contact requests (e.g. a
	// clone and a push racing) can't both run `git init --bare` on the same
	// path. Re-check existence under the lock.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Exists(user, repo) {
		return nil
	}
	dir := s.RepoDir(user, repo)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	// Initial branch is main to match the agentsfs contract's examples and to
	// keep the bare repo's HEAD consistent with what clients push.
	if out, err := exec.Command(s.GitPath, "init", "--bare", "-b", "main", "--quiet", dir).CombinedOutput(); err != nil {
		return fmt.Errorf("git init --bare %s: %v: %s", dir, err, strings.TrimSpace(string(out)))
	}
	// git-http-backend only advertises receive-pack when this is set.
	if out, err := exec.Command(s.GitPath, "-C", dir, "config", "http.receivepack", "true").CombinedOutput(); err != nil {
		return fmt.Errorf("git config http.receivepack: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RenameRepo moves user's repo from oldName to newName on disk. It rejects a
// taken target (the duplicate check). Existing clones must update their remote
// URL afterward — the slug is the identity, like a GitHub repo rename.
func (s *LocalStorage) RenameRepo(user, oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Exists(user, oldName) {
		return fmt.Errorf("no such repo %q", oldName)
	}
	if s.Exists(user, newName) {
		return fmt.Errorf("you already have a repo named %q", newName)
	}
	return os.Rename(s.RepoDir(user, oldName), s.RepoDir(user, newName))
}

func (s *LocalStorage) ListRepos(user string) ([]string, error) {
	if !nameRe.MatchString(user) {
		return nil, nil
	}
	ents, err := os.ReadDir(filepath.Join(s.Dir, user))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var repos []string
	for _, e := range ents {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".git") {
			repos = append(repos, strings.TrimSuffix(e.Name(), ".git"))
		}
	}
	sort.Strings(repos)
	return repos, nil
}
