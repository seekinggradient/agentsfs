package hub

import (
	"regexp"
	"strings"
)

// Per-repo settings live in the bare repo's own git config under the afs-hub.*
// namespace, so they travel with the repo on the volume and need no separate
// database. Visibility defaults to private; a repo is public only when
// explicitly set (behind a typed confirmation in the UI).

const (
	visPrivate = "private"
	visPublic  = "public"
)

func repoConfigGet(bareDir, key string) string {
	out, err := gitCmd("git", bareDir, nil, nil, "config", "--local", key)
	if err != nil {
		return "" // unset → git exits non-zero
	}
	return strings.TrimSpace(out)
}

func repoConfigSet(bareDir, key, val string) error {
	_, err := gitCmd("git", bareDir, nil, nil, "config", "--local", key, val)
	return err
}

// isPublic reports whether a repo is marked public. Default (unset) is private.
func (s *Server) isPublic(user, repo string) bool {
	return repoConfigGet(s.Storage.RepoDir(user, repo), "afs-hub.visibility") == visPublic
}

func (s *Server) setVisibility(user, repo, vis string) error {
	if vis != visPublic {
		vis = visPrivate
	}
	return repoConfigSet(s.Storage.RepoDir(user, repo), "afs-hub.visibility", vis)
}

// canWrite reports whether viewer may edit/commit to user/repo: the owner, or a
// write collaborator. Drives the edit affordances; settings stay owner-only.
func (s *Server) canWrite(user, repo, viewer string) bool {
	if viewer == "" {
		return false
	}
	return viewer == user || s.Accounts.CollaboratorRole(user, repo, viewer) == "write"
}

// collabRoleFor returns viewer's collaborator role on user/repo, or "" when the
// viewer owns the repo or isn't a collaborator (for a "shared with you" badge).
func collabRoleFor(acc *AccountStore, user, repo, viewer string) string {
	if viewer == "" || viewer == user {
		return ""
	}
	return acc.CollaboratorRole(user, repo, viewer)
}

// displayName is the repo's human-facing name; defaults to the slug.
func (s *Server) displayName(user, repo string) string {
	if dn := repoConfigGet(s.Storage.RepoDir(user, repo), "afs-hub.displayname"); dn != "" {
		return dn
	}
	return repo
}

func (s *Server) setDisplayName(user, repo, name string) error {
	return repoConfigSet(s.Storage.RepoDir(user, repo), "afs-hub.displayname", strings.TrimSpace(name))
}

// slugRe validates a repo slug: lowercase letters/digits joined by single
// hyphens, not leading/trailing with a hyphen. GitHub-ish, URL-clean.
var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// validSlug reports whether s is a usable repo slug (1–63 chars).
func validSlug(s string) bool {
	return len(s) >= 1 && len(s) <= 63 && slugRe.MatchString(s)
}

// reservedNames are usernames that would collide with a top-level route (e.g.
// /agent, /account) or the per-user agent sprite namespace (afs-user-<user>),
// so they can't be claimed at signup. Existing accounts are unaffected.
var reservedNames = map[string]bool{
	"agent": true, "user": true, "account": true, "login": true, "logout": true,
	"signup": true, "api": true, "assets": true, "admin": true, "static": true,
}

func isReserved(s string) bool {
	return reservedNames[strings.ToLower(strings.TrimSpace(s))]
}
