package hub

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// zeroOID is git's all-zeros object id: as an update-ref expected-old value it
// means "the ref must not already exist", giving create-if-absent CAS for the
// first commit into an empty repo.
const zeroOID = "0000000000000000000000000000000000000000"

// apiChange is one file mutation in a CAS commit: write content, or delete.
type apiChange struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Delete  bool   `json:"delete"`
}

// apiCommitRequest is the body of POST /api/agent/v1/commit.
type apiCommitRequest struct {
	Repo    string `json:"repo"` // "<owner>/<repo>"
	BaseRev string `json:"baseRev"`
	Message string `json:"message"`
	Author  struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"author"`
	Changes []apiChange `json:"changes"`
}

// apiCommit applies a revision-anchored compare-and-swap commit. The write names
// the baseRev it was reasoned against; the Hub then:
//
//   - fast-forwards when HEAD is still at baseRev;
//   - trivially merges when HEAD has moved but the moved range touches paths
//     DISJOINT from this write (the changes replay onto the new HEAD's tree, so
//     concurrent work is preserved);
//   - otherwise rejects 409 with {currentHead, conflictPaths} so the agent can
//     re-read at HEAD and retry.
//
// This is optimistic concurrency with git as the arbiter — the same discipline a
// laptop `afs` checkout gets implicitly from push/pull, made explicit per write.
//
// The whole write — including the 404-before-403 access guard, which here IS the
// capability check and not merely routing — lives in RepoCommit (repoaccess.go);
// this wrapper decodes the body and translates the two error flavors: an
// accessError to its plain {"error"} status, a conflictError to the richer 409
// writeConflict body {currentHead, conflictPaths}.
func (s *Server) apiCommit(w http.ResponseWriter, r *http.Request, auth agentAPIAuth) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req apiCommitRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "bad json")
		return
	}
	if auth.grant != nil {
		owner, repo, ok := splitRepoSpec(req.Repo)
		if !ok || !auth.allowsRepo(owner, repo) {
			apiError(w, http.StatusNotFound, "no such repo")
			return
		}
		for _, change := range req.Changes {
			if change.Delete {
				apiError(w, http.StatusForbidden, "automatic gardening cannot delete files")
				return
			}
			path, ok := safeRepoPath(change.Path)
			if !ok {
				apiError(w, http.StatusBadRequest, "bad path")
				return
			}
			clean := strings.ToLower(path)
			if clean == "agents.md" {
				apiError(w, http.StatusForbidden, "automatic gardening must use the safe contract upgrade action")
				return
			}
			if strings.HasPrefix(clean, ".agentsfs/") || (filepath.Ext(clean) != ".md" && filepath.Ext(clean) != ".markdown") {
				apiError(w, http.StatusForbidden, "automatic gardening may change markdown files only")
				return
			}
		}
		if !s.Accounts.UseAutoGardenGrant(auth.credential, time.Now()) {
			apiError(w, http.StatusForbidden, "automatic gardening write limit reached")
			return
		}
		req.Message = "Automatic gardening: " + strings.TrimSpace(req.Message)
		req.Author.Name = "AgentsFS gardener"
		req.Author.Email = "gardener@agentsfs.ai"
	}
	res, err := s.RepoCommit(auth.user, req)
	if err != nil {
		if ce, ok := err.(*conflictError); ok {
			writeConflict(w, ce.head, ce.paths)
			return
		}
		writeAccessError(w, err)
		return
	}
	// `newRev` is the name the Eve client (lib/hub-client.ts apiCommit) reads;
	// `newHead` stays as an alias for any earlier consumer.
	writeJSON(w, http.StatusOK, map[string]any{
		"newRev":  res.NewRev,
		"newHead": res.NewRev,
		"merged":  res.Merged,
	})
}

// writeConflict emits the 409 a CAS write returns when HEAD has moved into the
// write's path range (or raced the ref update). conflictPaths is sorted and may
// be empty (a lost update-ref race with no identified overlap).
func writeConflict(w http.ResponseWriter, currentHead string, conflictPaths []string) {
	sort.Strings(conflictPaths)
	if conflictPaths == nil {
		conflictPaths = []string{}
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":         "head moved",
		"currentHead":   currentHead,
		"conflictPaths": conflictPaths,
	})
}

// apiCanReach reports whether user has ANY (read or write) access to owner/repo,
// used to keep 404 (not-found) distinct from 403 (found-but-read-only) on write
// routes without leaking existence to a caller with no access at all.
func (s *Server) apiCanReach(owner, repo, user string) bool {
	r, _ := s.apiRepoAccess(owner, repo, user)
	return r
}

// changedPaths returns the set of paths that differ between two commits, with
// rename detection off so every added/modified/deleted path on either side is
// counted — the conservative superset for conflict detection.
func changedPaths(bare, a, b string) map[string]bool {
	out := map[string]bool{}
	res, err := gitCmd("git", bare, nil, nil, "diff", "--name-only", "--no-renames", "-z", a, b)
	if err != nil {
		return out
	}
	for _, p := range strings.Split(strings.TrimRight(res, "\x00"), "\x00") {
		if p != "" {
			out[p] = true
		}
	}
	return out
}

// splitRepoSpec parses "<owner>/<repo>" into validated, lowercased-owner parts.
func splitRepoSpec(spec string) (owner, repo string, ok bool) {
	spec = strings.Trim(strings.TrimSpace(spec), "/")
	parts := strings.Split(spec, "/")
	if len(parts) != 2 {
		return "", "", false
	}
	owner = strings.ToLower(parts[0])
	repo = strings.TrimSuffix(parts[1], ".git")
	if !nameRe.MatchString(owner) || !nameRe.MatchString(repo) {
		return "", "", false
	}
	return owner, repo, true
}
