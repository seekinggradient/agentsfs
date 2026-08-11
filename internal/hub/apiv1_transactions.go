package hub

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// The browser save API's atomic, multi-file sibling. A Markdown To workspace is a directory,
// and its normative mutations are manifests: every before-image is checked before any write and
// every member lands in one commit. Repeating the single-file PUT endpoint would make a partial
// workspace observable, so this endpoint maps the manifest straight onto RepoCommit's one-tree
// compare-and-swap boundary.

const maxTransactionFiles = 4096

type apiV1TransactionChange struct {
	Path       string  `json:"path"`
	BeforeHash *string `json:"beforeHash"`
	After      *string `json:"after"`
}

type apiV1TransactionRequest struct {
	Message string                   `json:"message"`
	Primary string                   `json:"primary"`
	Changes []apiV1TransactionChange `json:"changes"`
}

type apiV1TransactionFile struct {
	Path    string `json:"path"`
	Hash    string `json:"hash,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

type apiV1TransactionResult struct {
	Owner           string                 `json:"owner"`
	Instance        string                 `json:"instance"`
	Rev             string                 `json:"rev"`
	Merged          bool                   `json:"merged"`
	InstanceCreated bool                   `json:"instanceCreated"`
	Primary         string                 `json:"primary"`
	URL             string                 `json:"url"`
	Files           []apiV1TransactionFile `json:"files"`
}

type apiV1TransactionConflict struct {
	Path     string  `json:"path"`
	Expected *string `json:"expected"`
	Current  *string `json:"current"`
}

func (s *Server) apiV1Transaction(w http.ResponseWriter, r *http.Request, c *apiCaller, owner, instance string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !requireScope(w, c, scopeInstancesWrite) {
		return
	}

	var req apiV1TransactionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20))
	if err := dec.Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "bad json")
		return
	}
	if len(req.Changes) == 0 {
		apiError(w, http.StatusBadRequest, "no changes")
		return
	}
	if len(req.Changes) > maxTransactionFiles {
		apiError(w, http.StatusRequestEntityTooLarge, "too many files in one transaction")
		return
	}

	primary, ok := safeRepoPath(req.Primary)
	if !ok {
		apiError(w, http.StatusBadRequest, "bad primary path")
		return
	}

	seen := map[string]bool{}
	for i := range req.Changes {
		change := &req.Changes[i]
		path, ok := safeRepoPath(change.Path)
		if !ok {
			apiError(w, http.StatusBadRequest, "bad path: "+change.Path)
			return
		}
		if seen[path] {
			apiError(w, http.StatusBadRequest, "duplicate path: "+path)
			return
		}
		seen[path] = true
		change.Path = path
		if change.BeforeHash != nil && !validSourceHash(*change.BeforeHash) {
			apiError(w, http.StatusBadRequest, "bad beforeHash for "+path)
			return
		}
		if change.BeforeHash == nil && change.After == nil {
			apiError(w, http.StatusBadRequest, "cannot delete absent path: "+path)
			return
		}
		if change.After != nil && len([]byte(*change.After)) > maxFileBytes {
			apiError(w, http.StatusRequestEntityTooLarge, path+" is too large for this API; clone the instance and push instead")
			return
		}
	}

	instanceCreated := false
	if owner == strings.ToLower(c.User) && !s.Storage.Exists(owner, instance) {
		created, err := s.ensureInstance(c.User, instance, defaultCollectionDir, "")
		if err != nil {
			writeAccessError(w, err)
			return
		}
		instanceCreated = created.Created
	}
	if !s.apiV1CanRead(w, owner, instance, c.User) {
		return
	}
	if _, canWrite := s.apiRepoAccess(owner, instance, c.User); !canWrite {
		apiError(w, http.StatusForbidden, "no write access")
		return
	}

	bare := s.Storage.RepoDir(owner, instance)
	head := s.RepoResolve(owner, instance)
	conflicts := transactionConflicts(bare, head, req.Changes, nil)
	if len(conflicts) > 0 {
		writeTransactionConflict(w, head, "one or more workspace files changed since you read them", conflicts)
		return
	}

	// A primary file is the durable URL returned to the browser. It must exist after the manifest,
	// otherwise a successful transaction would hand back a dead link.
	primaryExists := false
	if head != "" {
		_, primaryExists = BlobContent("git", bare, head, primary)
	}
	for _, change := range req.Changes {
		if change.Path == primary {
			primaryExists = change.After != nil
		}
	}
	if !primaryExists {
		apiError(w, http.StatusBadRequest, "primary path does not exist after this transaction")
		return
	}

	changes := make([]apiChange, 0, len(req.Changes))
	files := make([]apiV1TransactionFile, 0, len(req.Changes))
	for _, change := range req.Changes {
		if change.After == nil {
			changes = append(changes, apiChange{Path: change.Path, Delete: true})
			files = append(files, apiV1TransactionFile{Path: change.Path, Deleted: true})
			continue
		}
		// Idempotent rows still get their current hash in the response, but do not manufacture an
		// empty Git change. A well-behaved client filters them; accepting one makes retries robust.
		if current, exists := BlobContent("git", bare, head, change.Path); exists && current == *change.After {
			files = append(files, apiV1TransactionFile{Path: change.Path, Hash: sourceHash([]byte(*change.After))})
			continue
		}
		changes = append(changes, apiChange{Path: change.Path, Content: *change.After})
		files = append(files, apiV1TransactionFile{Path: change.Path, Hash: sourceHash([]byte(*change.After))})
	}

	if len(changes) == 0 {
		writeJSON(w, http.StatusOK, apiV1TransactionResult{
			Owner: owner, Instance: instance, Rev: head, InstanceCreated: instanceCreated,
			Primary: primary, URL: s.blobURL(owner, instance, primary), Files: files,
		})
		return
	}

	res, err := s.RepoCommit(c.User, apiCommitRequest{
		Repo: owner + "/" + instance, BaseRev: head,
		Message: transactionCommitMessage(req.Message, c), Changes: changes,
	})
	if err != nil {
		if ce, ok := err.(*conflictError); ok {
			latest := ce.head
			paths := ce.paths
			if len(paths) == 0 {
				for _, change := range req.Changes {
					paths = append(paths, change.Path)
				}
			}
			conflicts := transactionConflicts(bare, latest, req.Changes, paths)
			writeTransactionConflict(w, latest, "the workspace changed while this transaction was being committed", conflicts)
			return
		}
		writeAccessError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, apiV1TransactionResult{
		Owner: owner, Instance: instance, Rev: res.NewRev, Merged: res.Merged,
		InstanceCreated: instanceCreated, Primary: primary,
		URL: s.blobURL(owner, instance, primary), Files: files,
	})
}

func validSourceHash(hash string) bool {
	if len(hash) != 64 || strings.ToLower(hash) != hash {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

func transactionConflicts(bare, head string, changes []apiV1TransactionChange, only []string) []apiV1TransactionConflict {
	wanted := map[string]bool{}
	for _, path := range only {
		wanted[path] = true
	}
	var conflicts []apiV1TransactionConflict
	for i := range changes {
		change := &changes[i]
		if len(wanted) > 0 && !wanted[change.Path] {
			continue
		}
		var current *string
		if head != "" {
			if content, exists := BlobContent("git", bare, head, change.Path); exists {
				hash := sourceHash([]byte(content))
				current = &hash
			}
		}
		matches := (change.BeforeHash == nil && current == nil) ||
			(change.BeforeHash != nil && current != nil && *change.BeforeHash == *current)
		if !matches {
			conflicts = append(conflicts, apiV1TransactionConflict{
				Path: change.Path, Expected: change.BeforeHash, Current: current,
			})
		}
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Path < conflicts[j].Path })
	return conflicts
}

func writeTransactionConflict(w http.ResponseWriter, head, why string, conflicts []apiV1TransactionConflict) {
	if conflicts == nil {
		conflicts = []apiV1TransactionConflict{}
	}
	writeJSON(w, http.StatusPreconditionFailed, map[string]any{
		"error": "manifest conflict", "why": why, "rev": head, "conflicts": conflicts,
	})
}

func transactionCommitMessage(message string, c *apiCaller) string {
	message = strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	if message == "" {
		message = "Save Markdown To workspace"
	}
	if via := clientLabel(c); via != "" {
		message += "\n\nVia: " + via + "\n"
	}
	return message
}
