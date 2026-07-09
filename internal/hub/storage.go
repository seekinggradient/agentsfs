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
	"time"
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
	// DeleteRepo soft-deletes user's repo: it moves off the served path so it
	// stops resolving, but is not destroyed outright (see LocalStorage for the
	// mechanism). Fails if the repo doesn't exist.
	DeleteRepo(user, repo string) error
	// EnsureHEAD repairs a repo whose HEAD points at an unborn branch (e.g. the
	// bare repo was initialized on main but the client pushed master) by
	// pointing HEAD at a real branch. No-op when HEAD already resolves or the
	// repo has no commits yet. Keeps the web view (reads HEAD) and plain clone
	// (follows HEAD) working regardless of the pushed branch name.
	EnsureHEAD(user, repo string) error
	// LookupRedirect resolves an old (renamed-away) slug to the current slug it
	// points at, or ("", false) if there is no redirect. Keeps old clone/push
	// URLs working after a repo is renamed.
	LookupRedirect(user, oldSlug string) (string, bool)
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
	if err := os.Rename(s.RepoDir(user, oldName), s.RepoDir(user, newName)); err != nil {
		return err
	}
	// Keep the old slug resolving to the new one so existing clones/push remotes
	// don't break (best-effort; the rename already succeeded).
	s.recordRedirect(user, oldName, newName)
	return nil
}

// DeleteRepo moves user's bare repo into <root>/.trash instead of destroying
// it outright — a fat-fingered or prompt-injected delete shouldn't be
// unrecoverable, and the operator can restore the dir by hand. ".trash" sits
// at the storage root next to user namespaces, not inside one; usernames
// can't start with "." (nameRe), so it can never collide with a real
// namespace, and ListRepos/user listing only ever read inside a <user> dir,
// so they never see it. LFS objects are left in place: they're
// content-addressed and shared by hash, so an orphaned blob after a delete
// just sits there unreferenced rather than corrupting anything.
func (s *LocalStorage) DeleteRepo(user, repo string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Exists(user, repo) {
		return fmt.Errorf("no such repo %q", repo)
	}
	trash := filepath.Join(s.Dir, ".trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(trash, fmt.Sprintf("%s--%s--%d.git", user, repo, time.Now().Unix()))
	return os.Rename(s.RepoDir(user, repo), dest)
}

// EnsureHEAD points a bare repo's HEAD at a real branch when HEAD is unborn,
// preferring main, then master, then the first branch. It is a no-op when HEAD
// already resolves to a commit or the repo has no branches yet. This is what
// makes a repo pushed from a `master` branch (into a repo initialized on main)
// show up in the web view and clone correctly, instead of looking empty.
func (s *LocalStorage) EnsureHEAD(user, repo string) error {
	bare := s.RepoDir(user, repo)
	git := s.GitPath
	if git == "" {
		git = "git"
	}
	// HEAD already resolves to a commit — nothing to repair.
	if exec.Command(git, "-C", bare, "rev-parse", "--verify", "--quiet", "HEAD").Run() == nil {
		return nil
	}
	out, err := exec.Command(git, "-C", bare, "for-each-ref", "--format=%(refname)", "refs/heads/").Output()
	if err != nil {
		return err
	}
	branches := strings.Fields(string(out))
	if len(branches) == 0 {
		return nil // genuinely empty: no commits pushed yet
	}
	pick := branches[0]
	for _, pref := range []string{"refs/heads/main", "refs/heads/master"} {
		for _, b := range branches {
			if b == pref {
				pick = pref
			}
		}
	}
	if out, err := exec.Command(git, "-C", bare, "symbolic-ref", "HEAD", pick).CombinedOutput(); err != nil {
		return fmt.Errorf("symbolic-ref HEAD %s: %v: %s", pick, err, strings.TrimSpace(string(out)))
	}
	return nil
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
