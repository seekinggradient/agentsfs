package hub

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Per-repo settings live in the bare repo's own git config under the afs-hub.*
// namespace, so they travel with the repo on the volume and need no separate
// database. Visibility defaults to private; a repo is public only when
// explicitly set (behind a typed confirmation in the UI).

const (
	visPrivate = "private"
	visPublic  = "public"

	repoModeStandalone           = "standalone"
	repoModeEmbeddedProjectionV1 = "embedded-projection-v1"
	repoModeEmbeddedProjection   = "embedded-projection"
	projectionLedgerRef          = "refs/agentsfs/projection"
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
	permitted := viewer == user || s.Accounts.CollaboratorRole(user, repo, viewer) == "write"
	return permitted && s.hubWritesAllowed(user, repo)
}

// repositoryMode distinguishes ordinary repositories from embedded
// projections. A v2 projection self-identifies through its recoverable ledger
// ref. The explicit v1 value is an operator/client migration guard for legacy
// projections that predate that ref. Unmarked legacy repositories retain the
// standalone default for backward compatibility.
func (s *Server) repositoryMode(user, repo string) string {
	bare := s.Storage.RepoDir(user, repo)
	if mode := repoConfigGet(bare, "afs-hub.repository-mode"); mode != "" {
		return mode
	}
	if headOID("git", bare, projectionLedgerRef) != "" {
		return repoModeEmbeddedProjection
	}
	return repoModeStandalone
}

// hubWritesAllowed gates every writer whose commit originates on the Hub
// (editor, API/MCP/save/board writeback, and auto-gardening). Smart-HTTP Git
// pushes are intentionally outside this gate: they are the escape hatch that
// upgrades a guarded v1 projection atomically to protocol v2.
func (s *Server) hubWritesAllowed(user, repo string) bool {
	return s.repositoryMode(user, repo) != repoModeEmbeddedProjectionV1
}

func (s *Server) refreshProjectionMode(user, repo string) {
	bare := s.Storage.RepoDir(user, repo)
	if headOID("git", bare, projectionLedgerRef) != "" {
		_ = repoConfigSet(bare, "afs-hub.repository-mode", repoModeEmbeddedProjection)
	}
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

// autoGardenEnabled defaults on for a repository that has never been configured;
// an explicit false written from the account pane is the durable opt-out.
func (s *Server) autoGardenEnabled(user, repo string) bool {
	return repoConfigGet(s.Storage.RepoDir(user, repo), "afs-hub.auto-garden") != "false"
}

func (s *Server) setAutoGardenEnabled(user, repo string, enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	return repoConfigSet(s.Storage.RepoDir(user, repo), "afs-hub.auto-garden", value)
}

type autoGardenProgress struct {
	State        string
	StateAt      int64
	LastStatus   string
	LastAttempt  int64
	LastGardened int64
}

func (s *Server) autoGardenProgress(user, repo string) autoGardenProgress {
	bare := s.Storage.RepoDir(user, repo)
	parse := func(key string) int64 {
		value, _ := strconv.ParseInt(repoConfigGet(bare, key), 10, 64)
		return value
	}
	return autoGardenProgress{
		State:        repoConfigGet(bare, "afs-hub.garden-run-state"),
		StateAt:      parse("afs-hub.garden-run-at"),
		LastStatus:   repoConfigGet(bare, "afs-hub.last-garden-status"),
		LastAttempt:  parse("afs-hub.last-garden-at"),
		LastGardened: parse("afs-hub.last-gardened"),
	}
}

func (s *Server) setAutoGardenRunState(user, repo, state string, now time.Time) error {
	bare := s.Storage.RepoDir(user, repo)
	if err := repoConfigSet(bare, "afs-hub.garden-run-state", state); err != nil {
		return err
	}
	return repoConfigSet(bare, "afs-hub.garden-run-at", strconv.FormatInt(now.Unix(), 10))
}

func (s *Server) recordAutoGardenResult(user, repo, status string, finishedAt time.Time) error {
	bare := s.Storage.RepoDir(user, repo)
	if err := repoConfigSet(bare, "afs-hub.garden-run-state", "idle"); err != nil {
		return err
	}
	if err := repoConfigSet(bare, "afs-hub.last-garden-status", status); err != nil {
		return err
	}
	stamp := strconv.FormatInt(finishedAt.Unix(), 10)
	if err := repoConfigSet(bare, "afs-hub.last-garden-at", stamp); err != nil {
		return err
	}
	if status == "completed" {
		return repoConfigSet(bare, "afs-hub.last-gardened", stamp)
	}
	return nil
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
	"redesign": true, "redesign-v2": true, "oauth": true, "mcp": true,
	"s": true, // public share links live at /s/<token>
}

func isReserved(s string) bool {
	return reservedNames[strings.ToLower(strings.TrimSpace(s))]
}
