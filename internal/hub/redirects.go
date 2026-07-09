package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Repo-rename redirects: after a repo is renamed, its old slug keeps resolving
// to the new one (like GitHub), so already-configured clones and `afs hub push`
// remotes pointing at the old URL keep working instead of silently recreating
// an empty repo at the old name. Stored as a small JSON map (old slug -> new
// slug) per user namespace, next to their repos.

func (s *LocalStorage) redirectsPath(user string) string {
	return filepath.Join(s.Dir, user, ".afs-redirects.json")
}

func (s *LocalStorage) loadRedirects(user string) map[string]string {
	m := map[string]string{}
	if data, err := os.ReadFile(s.redirectsPath(user)); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

// LookupRedirect resolves an old slug to the current one it was renamed to,
// following a chain of renames. It only returns a target that actually exists.
func (s *LocalStorage) LookupRedirect(user, oldSlug string) (string, bool) {
	m := s.loadRedirects(user)
	cur := oldSlug
	for i := 0; i < 10; i++ {
		next, ok := m[cur]
		if !ok {
			break
		}
		cur = next
	}
	if cur != oldSlug && s.Exists(user, cur) {
		return cur, true
	}
	return "", false
}

// recordRedirect notes that oldSlug now lives at newSlug. Callers hold s.mu
// (RenameRepo does). Best-effort: a write failure doesn't undo the rename.
func (s *LocalStorage) recordRedirect(user, oldSlug, newSlug string) {
	m := s.loadRedirects(user)
	m[oldSlug] = newSlug
	// Repoint any redirect that used to target oldSlug so chains stay short.
	for k, v := range m {
		if v == oldSlug {
			m[k] = newSlug
		}
	}
	// newSlug now exists as a real repo, so it must not itself be a redirect.
	delete(m, newSlug)
	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(s.redirectsPath(user), data, 0o644)
	}
}

// dropRedirectsTo removes every mapping that resolves to slug, called after
// slug is deleted. Otherwise a client still holding an old (renamed-away) URL
// would 301 straight into the empty spot the delete just made — and since
// serveGit auto-creates on an owner's first push, that owner push would
// silently resurrect the repo at the old name instead of failing loudly.
// Unlike recordRedirect it is called outside DeleteRepo, so it takes s.mu
// itself to keep the redirect file's read-modify-write serialized.
func (s *LocalStorage) dropRedirectsTo(user, slug string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.loadRedirects(user)
	changed := false
	for k, v := range m {
		if v == slug {
			delete(m, k)
			changed = true
		}
	}
	if !changed {
		return
	}
	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(s.redirectsPath(user), data, 0o644)
	}
}
