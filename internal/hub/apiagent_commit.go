package hub

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
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
func (s *Server) apiCommit(w http.ResponseWriter, r *http.Request, user string) {
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
	owner, repo, ok := splitRepoSpec(req.Repo)
	if !ok {
		apiError(w, http.StatusBadRequest, "bad repo")
		return
	}
	_, canWrite := s.apiRepoAccess(owner, repo, user)
	if !s.Storage.Exists(owner, repo) || !s.apiCanReach(owner, repo, user) {
		apiError(w, http.StatusNotFound, "no such repo")
		return
	}
	if !canWrite {
		apiError(w, http.StatusForbidden, "no write access")
		return
	}

	// Path-jail every change and reject empty/duplicate paths up front.
	if len(req.Changes) == 0 {
		apiError(w, http.StatusBadRequest, "no changes")
		return
	}
	seen := map[string]bool{}
	changePaths := make([]string, 0, len(req.Changes))
	for i, c := range req.Changes {
		p, ok := safeRepoPath(c.Path)
		if !ok {
			apiError(w, http.StatusBadRequest, "bad path: "+c.Path)
			return
		}
		if seen[p] {
			apiError(w, http.StatusBadRequest, "duplicate path: "+p)
			return
		}
		seen[p] = true
		req.Changes[i].Path = p
		changePaths = append(changePaths, p)
	}

	bare := s.Storage.RepoDir(owner, repo)
	branchRef, err := gitCmd("git", bare, nil, nil, "symbolic-ref", "HEAD")
	if err != nil {
		apiError(w, http.StatusInternalServerError, "resolve branch")
		return
	}
	branchRef = strings.TrimSpace(branchRef)
	head := headOID("git", bare, defaultRef) // "" for an empty (unborn) repo

	// Decide the parent to build on and the CAS expected-old value.
	var parent string // commit our changes replay onto ("" = root commit)
	var expectedOld string
	merged := false
	switch {
	case head == "":
		// Empty repo: baseRev must be empty; create the root commit. The CAS
		// expected-old is the zero OID, which git reads as "the ref must not yet
		// exist" — so a concurrent first commit that wins the race makes ours 409.
		if strings.TrimSpace(req.BaseRev) != "" {
			writeConflict(w, head, nil)
			return
		}
		parent, expectedOld = "", zeroOID
	case req.BaseRev == "":
		apiError(w, http.StatusBadRequest, "baseRev required")
		return
	default:
		baseOID := headOID("git", bare, req.BaseRev)
		if baseOID == "" || !validRev(req.BaseRev) {
			apiError(w, http.StatusBadRequest, "bad baseRev")
			return
		}
		if baseOID == head {
			parent, expectedOld = head, head // fast-forward
		} else {
			// HEAD moved. Merge iff the moved range is disjoint from our changes.
			moved := changedPaths(bare, baseOID, head)
			var conflicts []string
			for _, p := range changePaths {
				if moved[p] {
					conflicts = append(conflicts, p)
				}
			}
			if len(conflicts) > 0 {
				writeConflict(w, head, conflicts)
				return
			}
			parent, expectedOld, merged = head, head, true
		}
	}

	// Build the new tree in a throwaway index seeded from parent's tree. The temp
	// file is removed immediately: git treats a 0-byte index as corrupt, so we
	// hand it a NON-existent path and let update-index/read-tree create a fresh
	// index there (the deferred remove reaps whatever git writes).
	idx, err := os.CreateTemp("", "afs-api-idx-*")
	if err != nil {
		apiError(w, http.StatusInternalServerError, "index")
		return
	}
	idx.Close()
	os.Remove(idx.Name())
	defer os.Remove(idx.Name())
	env := append(os.Environ(), "GIT_INDEX_FILE="+idx.Name())
	if parent != "" {
		if _, err := gitCmd("git", bare, env, nil, "read-tree", parent); err != nil {
			apiError(w, http.StatusInternalServerError, "read-tree")
			return
		}
	}
	for _, c := range req.Changes {
		if c.Delete {
			// A bare repo has no work tree, so update-index --force-remove is
			// refused; stage the removal via --index-info with mode 0 (the
			// worktree-free deletion form). Removing an already-absent path is a
			// harmless no-op.
			rm := "0 " + zeroOID + "\t" + c.Path + "\n"
			if _, err := gitCmd("git", bare, env, strings.NewReader(rm), "update-index", "--index-info"); err != nil {
				apiError(w, http.StatusInternalServerError, "delete "+c.Path)
				return
			}
			continue
		}
		blob, err := gitCmd("git", bare, env, strings.NewReader(c.Content), "hash-object", "-w", "--stdin")
		if err != nil {
			apiError(w, http.StatusInternalServerError, "hash "+c.Path)
			return
		}
		blob = strings.TrimSpace(blob)
		if _, err := gitCmd("git", bare, env, nil, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+c.Path); err != nil {
			apiError(w, http.StatusInternalServerError, "stage "+c.Path)
			return
		}
	}
	tree, err := gitCmd("git", bare, env, nil, "write-tree")
	if err != nil {
		apiError(w, http.StatusInternalServerError, "write-tree")
		return
	}
	tree = strings.TrimSpace(tree)

	// Author is the human/agent; committer is the Hub, so `git blame` stays
	// truthful about who authored the change.
	authorName := strings.TrimSpace(req.Author.Name)
	if authorName == "" {
		authorName = user
	}
	authorEmail := strings.TrimSpace(req.Author.Email)
	if authorEmail == "" {
		authorEmail = user + "@users.agentsfs"
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		message = "Update via agent API"
	}
	commitEnv := append(env,
		"GIT_AUTHOR_NAME="+authorName, "GIT_AUTHOR_EMAIL="+authorEmail,
		"GIT_COMMITTER_NAME=agentsfs hub", "GIT_COMMITTER_EMAIL=hub@agentsfs",
	)
	commitArgs := []string{"commit-tree", tree}
	if parent != "" {
		commitArgs = append(commitArgs, "-p", parent)
	}
	commitArgs = append(commitArgs, "-F", "-")
	commit, err := gitCmd("git", bare, commitEnv, strings.NewReader(message), commitArgs...)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "commit-tree")
		return
	}
	commit = strings.TrimSpace(commit)

	// Compare-and-swap the branch. update-ref fails atomically if HEAD moved
	// again since we read it (a lost race), or — for a root commit — if the ref
	// came into existence meanwhile (expectedOld ""). Either way: 409, re-read.
	if _, err := gitCmd("git", bare, nil, nil, "update-ref", branchRef, commit, expectedOld); err != nil {
		writeConflict(w, headOID("git", bare, defaultRef), nil)
		return
	}
	// A ref moved: repair a dangling HEAD (defensive; branchRef is HEAD's target)
	// and let the per-repo view rebuild lazily on its next read (it keys on the
	// commit id, so the move is detected automatically).
	_ = s.Storage.EnsureHEAD(owner, repo)

	writeJSON(w, http.StatusOK, map[string]any{
		"newHead": commit,
		"merged":  merged,
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
