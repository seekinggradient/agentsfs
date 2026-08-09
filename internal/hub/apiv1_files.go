package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

// The file half of the /api/v1 save API: read a document, save a document, list
// the documents in an instance, and publish one at a share link. See apiv1.go
// for the surface as a whole.
//
// The conflict model is the reason this file exists rather than reusing the
// agent API's commit endpoint. An agent reasons about a REVISION ("I read the
// repo at rev X, commit my changes against X"); an editor reasons about the
// BYTES it has open ("I loaded these bytes, save only if they are still there").
// The Markdown To patch engine already demands the latter — every mutation
// carries an `expect` hash and is refused rather than applied when the source
// moved — so this API speaks the same language, which makes the conflict
// surface identical from the editor's undo stack all the way down to the git
// commit: one hash, checked twice, never a silent overwrite.

// sourceHash is the content hash this API's ETag and If-Match speak: lowercase
// hex SHA-256 over the file's exact bytes.
//
// It is defined to be byte-identical to the Markdown To patch engine's
// `sourceHash` (packages/core/src/patch/hash.ts — sha256 of the document text,
// utf-8, lowercase hex), so a client can hand the hash it already holds straight
// to If-Match with no re-derivation, and the value it reads back from a save is
// exactly the `expect` its next patch needs. It is NOT a git object id: a git
// blob oid is sha1 over "blob <len>\0"+content and would force the editor to
// implement git's hashing to say what it is holding.
func sourceHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// etagMatches reports whether an If-Match header value covers hash. It accepts
// the RFC 7232 list form (`"a", "b"`), quoted or bare tags (a bare hex hash is
// what a client that never read the spec will send, and refusing it would only
// produce mystifying 412s), and rejects weak validators — a weak tag means
// "semantically equivalent", which is not a safe basis for overwriting bytes.
func etagMatches(ifMatch, hash string) bool {
	for _, tag := range strings.Split(ifMatch, ",") {
		tag = strings.TrimSpace(tag)
		if strings.HasPrefix(tag, "W/") {
			continue
		}
		if strings.Trim(tag, `"`) == hash {
			return true
		}
	}
	return false
}

// apiV1Files dispatches the /files section: a listing when no path follows, a
// read or a save when one does.
func (s *Server) apiV1Files(w http.ResponseWriter, r *http.Request, c *apiCaller, owner, instance, filePath string) {
	if filePath == "" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			apiError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.apiV1ListFiles(w, r, c, owner, instance)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.apiV1GetFile(w, r, c, owner, instance, filePath)
	case http.MethodPut:
		s.apiV1PutFile(w, r, c, owner, instance, filePath)
	default:
		w.Header().Set("Allow", "GET, HEAD, PUT")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// --- read ------------------------------------------------------------------

// apiV1GetFile serves one file's raw bytes plus the hash a later save must
// present. The body is served as octet-stream with nosniff, exactly as the agent
// API serves file bytes: this endpoint must never become a way to get the Hub to
// render attacker-controlled HTML on its own origin.
func (s *Server) apiV1GetFile(w http.ResponseWriter, r *http.Request, c *apiCaller, owner, instance, filePath string) {
	if !requireScope(w, c, scopeInstancesRead) {
		return
	}
	if !s.apiV1CanRead(w, owner, instance, c.User) {
		return
	}
	p, ok := safeRepoPath(filePath)
	if !ok {
		apiError(w, http.StatusBadRequest, "bad path")
		return
	}
	bare := s.Storage.RepoDir(owner, instance)
	rev := r.URL.Query().Get("rev")
	if oid, status := resolveRev(bare, rev); status != http.StatusOK {
		apiError(w, status, "bad rev")
		return
	} else if size, ok := BlobSize("git", bare, oid, p); ok && size > maxFileBytes {
		apiError(w, http.StatusRequestEntityTooLarge, "file is too large for this API; clone the instance or use the raw endpoint")
		return
	}
	content, oid, head, err := s.RepoReadFile(owner, instance, rev, p)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	hash := sourceHash(content)
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("ETag", `"`+hash+`"`)
	h.Set("X-Afs-Source-Hash", hash)
	h.Set("X-Afs-Rev", oid)
	h.Set("X-Afs-Head", head)
	h.Set("Content-Length", strconv.Itoa(len(content)))
	h.Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	w.Write(content)
}

// apiV1CanRead enforces the read gate and writes the failure itself, returning
// whether the caller may proceed. It answers 404 (never 403) when the instance
// is missing OR unreadable, so the API never confirms the existence of an
// instance the caller cannot see.
func (s *Server) apiV1CanRead(w http.ResponseWriter, owner, instance, user string) bool {
	canRead, _ := s.apiRepoAccess(owner, instance, user)
	if !s.Storage.Exists(owner, instance) || !canRead {
		apiError(w, http.StatusNotFound, "no such instance")
		return false
	}
	return true
}

// --- save ------------------------------------------------------------------

// apiV1SaveResult is what a successful save returns. hash is the saved bytes'
// new source hash — the value the client holds for its next If-Match, so a
// save-then-save-again loop never needs a round trip through GET.
type apiV1SaveResult struct {
	Owner           string `json:"owner"`
	Instance        string `json:"instance"`
	Path            string `json:"path"`
	Hash            string `json:"hash"`
	Rev             string `json:"rev"`
	Created         bool   `json:"created"`
	Merged          bool   `json:"merged"`
	InstanceCreated bool   `json:"instanceCreated"`
	Collection      bool   `json:"collection"`
	URL             string `json:"url"`
}

// apiV1PutFile saves one file as a real git commit, refusing rather than
// overwriting when the bytes moved underneath the client. The precondition
// rules, in full:
//
//   - `If-Match: <hash>` — save only if the file's current bytes hash to that
//     value. Anything else is 412 with the CURRENT hash in the body, so the
//     client can re-read, rebase its edit, and retry without guessing.
//   - `If-Match` on a file that does not exist — 412. There is no current
//     representation to match, and treating it as a create would silently turn a
//     "someone deleted this" race into a resurrection.
//   - `If-Match: *` — save only if the file exists (RFC 7232's "any current
//     representation"): an overwrite that doesn't care what it overwrites.
//   - No `If-Match` at all — CREATE only. Saving over an existing file without
//     naming what you expected to find is refused with 428 Precondition
//     Required, again carrying the current hash. This is the one place the API
//     is stricter than HTTP requires, and deliberately: a first save from a
//     playground has no hash to offer, but every subsequent one does, and an
//     unconditional PUT is exactly the silent overwrite the whole hash model
//     exists to prevent.
//
// Saving into an instance that does not exist yet, in the caller's OWN
// namespace, bootstraps it (ensureInstance) — the contract's zero-decisions
// path. Writes into anyone else's namespace never create.
func (s *Server) apiV1PutFile(w http.ResponseWriter, r *http.Request, c *apiCaller, owner, instance, filePath string) {
	if !requireScope(w, c, scopeInstancesWrite) {
		return
	}
	p, ok := safeRepoPath(filePath)
	if !ok {
		apiError(w, http.StatusBadRequest, "bad path")
		return
	}
	content, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFileBytes))
	if err != nil {
		apiError(w, http.StatusRequestEntityTooLarge, "file is too large for this API; clone the instance and push instead")
		return
	}

	// First contact bootstraps the instance, for the owner only — mirroring the
	// git surface (an owner's first push auto-creates) and the MCP write tool, so
	// "save this" works from a brand-new account with no setup step. The
	// directory the file lands in is declared a collection at the same time, so
	// the instance is contract-clean the moment it exists.
	instanceCreated := false
	if owner == strings.ToLower(c.User) && !s.Storage.Exists(owner, instance) {
		res, err := s.ensureInstance(c.User, instance, path.Dir(p), "")
		if err != nil {
			writeAccessError(w, err)
			return
		}
		instanceCreated = res.Created
	}

	// 404 before 403, exactly as RepoCommit orders it: a caller with no access at
	// all is told the instance does not exist; a reader who lacks write is told
	// they lack write.
	if !s.apiV1CanRead(w, owner, instance, c.User) {
		return
	}
	if _, canWrite := s.apiRepoAccess(owner, instance, c.User); !canWrite {
		apiError(w, http.StatusForbidden, "no write access")
		return
	}

	bare := s.Storage.RepoDir(owner, instance)
	head := s.RepoResolve(owner, instance)
	current, exists := "", false
	if head != "" {
		current, exists = BlobContent("git", bare, head, p)
	}
	currentHash := ""
	if exists {
		currentHash = sourceHash([]byte(current))
	}

	ifMatch := strings.TrimSpace(r.Header.Get("If-Match"))
	switch {
	case ifMatch == "":
		if exists {
			s.writePreconditionRequired(w, currentHash, head)
			return
		}
	case ifMatch == "*":
		if !exists {
			s.writeHashMismatch(w, "", head, "the file does not exist, so there is nothing to match")
			return
		}
	default:
		if !exists {
			s.writeHashMismatch(w, "", head, "the file does not exist at this path")
			return
		}
		if !etagMatches(ifMatch, currentHash) {
			s.writeHashMismatch(w, currentHash, head, "the file changed since you read it")
			return
		}
	}

	res, err := s.RepoCommit(c.User, apiCommitRequest{
		Repo:    owner + "/" + instance,
		BaseRev: head,
		Message: saveCommitMessage(r, p, exists, c),
		Changes: []apiChange{{Path: p, Content: string(content)}},
	})
	if err != nil {
		if ce, ok := err.(*conflictError); ok {
			// HEAD moved onto this very path between the hash check and the commit
			// — the same "someone else got there first" answer, arrived at a few
			// milliseconds later. Report it with the current hash so the client's
			// recovery path is identical to a 412's.
			latest := ""
			if cur, ok := BlobContent("git", bare, ce.head, p); ok {
				latest = sourceHash([]byte(cur))
			}
			s.writeHashMismatch(w, latest, ce.head, "the file changed while this save was being committed")
			return
		}
		writeAccessError(w, err)
		return
	}

	hash := sourceHash(content)
	status := http.StatusOK
	if !exists {
		status = http.StatusCreated
	}
	w.Header().Set("ETag", `"`+hash+`"`)
	w.Header().Set("X-Afs-Source-Hash", hash)
	writeJSON(w, status, apiV1SaveResult{
		Owner: owner, Instance: instance, Path: p,
		Hash: hash, Rev: res.NewRev,
		Created: !exists, Merged: res.Merged, InstanceCreated: instanceCreated,
		Collection: isCollectionDir(bare, res.NewRev, path.Dir(p)),
		URL:        s.blobURL(owner, instance, p),
	})
}

// writeHashMismatch is the 412: the file is not what the caller expected. hash
// is the CURRENT hash ("" when the file is gone), so a client can re-read,
// merge, and retry without a second request to discover what happened.
func (s *Server) writeHashMismatch(w http.ResponseWriter, hash, head, reason string) {
	writeJSON(w, http.StatusPreconditionFailed, map[string]any{
		"error": "hash mismatch",
		"hash":  hash,
		"rev":   head,
		"why":   reason,
	})
}

// writePreconditionRequired is the 428: the file exists and the save named no
// expectation. It carries the current hash, so the correct retry (this same PUT
// with If-Match set) needs no extra round trip.
func (s *Server) writePreconditionRequired(w http.ResponseWriter, hash, head string) {
	writeJSON(w, http.StatusPreconditionRequired, map[string]any{
		"error": "if-match required",
		"hash":  hash,
		"rev":   head,
		"why":   "this file already exists; send If-Match with the hash of the bytes you read to overwrite it",
	})
}

// saveCommitMessage builds the commit message for a save. The subject is the
// client's (X-Afs-Message, or ?message= for callers stuck with a URL and no
// header — curl, a form action), defaulting to something readable rather than
// blank. A `Via:` trailer always records the app that made the change, so `git
// log` in a plain clone says which front door a commit came through — the author
// is and remains the human.
func saveCommitMessage(r *http.Request, p string, exists bool, c *apiCaller) string {
	msg := strings.TrimSpace(r.Header.Get("X-Afs-Message"))
	if msg == "" {
		msg = strings.TrimSpace(r.URL.Query().Get("message"))
	}
	// A message is a commit subject; newlines would forge a body (or a trailer).
	msg = strings.TrimSpace(strings.SplitN(msg, "\n", 2)[0])
	if msg == "" {
		verb := "Update"
		if !exists {
			verb = "Add"
		}
		msg = verb + " " + pathBase(p)
	}
	via := clientLabel(c)
	if via == "" {
		return msg
	}
	return msg + "\n\nVia: " + via + "\n"
}

// clientLabel names the app behind a token for a commit trailer: "Markdown To
// (markdownto.ai)" for a registered client, its bare id when it registered no
// name, and "" for a PAT (whose holder is a person or their own tooling, with
// nothing more specific to say).
func clientLabel(c *apiCaller) string {
	if c.ClientID == "" {
		return ""
	}
	if c.ClientName == "" {
		return c.ClientID
	}
	return c.ClientName + " (" + c.ClientID + ")"
}

// isCollectionDir reports whether dir is declared `agentsfs_role: collection` at
// rev. A save answers with it so a client can tell whether the document it just
// wrote is covered by a collective description or owes the instance a
// `description:` of its own — the difference between a clean `afs doctor` and a
// finding. The instance root ("." — a file saved at the top level) is never a
// collection: the root INDEX.md describes the whole knowledge base.
func isCollectionDir(bare, rev, dir string) bool {
	if dir == "" || dir == "." || rev == "" {
		return false
	}
	content, ok := BlobContent("git", bare, rev, dir+"/INDEX.md")
	if !ok {
		return false
	}
	return core.FrontmatterValueFromReader(strings.NewReader(content), "agentsfs_role") == core.RoleCollection
}

// --- listing ---------------------------------------------------------------

// apiV1FileEntry is one markdown document in a listing. markdownto carries the
// document's envelope value when it declares one, which is what makes "show me
// the little apps I made" a single query rather than a client-side scan.
type apiV1FileEntry struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Size        int    `json:"size"`
	Hash        string `json:"hash"`
	Markdownto  string `json:"markdownto,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`
}

// listLimits bound a listing: how many entries are considered at all, and how
// many are returned. A knowledge base is a personal-scale thing, but the API
// must stay predictable on a large one.
const (
	maxListScan    = 5000
	defaultListCap = 100
	maxListCap     = 500
)

// apiV1ListFiles lists the MARKDOWN documents in an instance, optionally
// narrowed to a directory and to files carrying a `markdownto:` envelope.
//
// Markdown-only is a deliberate scope, not an oversight: the whole value of this
// listing is what a document SAYS about itself (its envelope, title, and
// description) and the hash a client needs to open it for editing, and neither
// exists for a PNG. Every blob, of every type, remains listable through the
// agent API's tree endpoint, the web UI, and a plain clone.
//
// The read is one `ls-tree` plus one batched `cat-file`, the same pair the repo
// pages use — not one git process per file — so listing a large instance costs
// two subprocesses regardless of how many documents it holds.
func (s *Server) apiV1ListFiles(w http.ResponseWriter, r *http.Request, c *apiCaller, owner, instance string) {
	if !requireScope(w, c, scopeInstancesRead) {
		return
	}
	if !s.apiV1CanRead(w, owner, instance, c.User) {
		return
	}
	bare := s.Storage.RepoDir(owner, instance)
	rev, status := resolveRev(bare, r.URL.Query().Get("rev"))
	if status != http.StatusOK {
		apiError(w, status, "bad rev")
		return
	}
	dir := strings.Trim(r.URL.Query().Get("dir"), "/")
	if dir != "" {
		clean, ok := safeRepoPath(dir)
		if !ok {
			apiError(w, http.StatusBadRequest, "bad dir")
			return
		}
		dir = clean
	}
	limit := defaultListCap
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			apiError(w, http.StatusBadRequest, "bad limit")
			return
		}
		limit = min(n, maxListCap)
	}
	filter, filtering := "", false
	if vals, ok := r.URL.Query()[envelopeKey]; ok {
		filtering = true
		if len(vals) > 0 {
			filter = vals[0]
		}
	}

	files := []apiV1FileEntry{}
	if rev != "" {
		entries, err := repoTreeEntries("git", bare, rev)
		if err != nil {
			apiError(w, http.StatusInternalServerError, "read instance")
			return
		}
		if len(entries) > maxListScan {
			entries = entries[:maxListScan]
		}
		contents := markdownBlobContents("git", bare, entries)
		for _, e := range entries {
			if !strings.EqualFold(path.Ext(e.Path), ".md") {
				continue
			}
			if dir != "" && !strings.HasPrefix(e.Path, dir+"/") {
				continue
			}
			body, ok := contents[e.Path]
			if !ok {
				continue
			}
			meta := readFileMeta(body)
			if filtering && !envelopeMatches(meta.Envelope, filter) {
				continue
			}
			files = append(files, apiV1FileEntry{
				Path: e.Path, Name: pathBase(e.Path), Size: len(body),
				Hash: sourceHash(body), Markdownto: meta.Envelope,
				Title: meta.Title, Description: meta.Description,
				URL: s.blobURL(owner, instance, e.Path),
			})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	}
	truncated := len(files) > limit
	if truncated {
		files = files[:limit]
	}
	writeJSON(w, http.StatusOK, struct {
		Owner     string           `json:"owner"`
		Instance  string           `json:"instance"`
		Rev       string           `json:"rev"`
		Dir       string           `json:"dir,omitempty"`
		Files     []apiV1FileEntry `json:"files"`
		Truncated bool             `json:"truncated"`
	}{Owner: owner, Instance: instance, Rev: rev, Dir: dir, Files: files, Truncated: truncated})
}

// --- share links -----------------------------------------------------------

// apiV1ShareLinkRequest is the body of POST /instances/{owner}/{inst}/sharelinks.
type apiV1ShareLinkRequest struct {
	Path          string `json:"path"`
	IncludeLinked bool   `json:"includeLinked"`
}

// apiV1CreateShareLink mints an unlisted public URL for one file — the Hub
// feature that already exists (sharelink.go), reached by an app instead of by
// the share page. Two gates beyond the scope check:
//
//   - OWNER only. A collaborator with write access may commit to an instance but
//     may not publish somebody else's knowledge to an anonymous URL, matching
//     the web share page (which is owner-only).
//   - Never a PAT. The web page requires a browser session for this precise
//     reason: publishing a private file is the action a prompt-injected agent
//     would be steered into, and a PAT lives on machines that read untrusted
//     input all day. An OAuth token reaches here only because a human read
//     "Create share links" on the consent screen and left the box ticked, which
//     is the same human decision the share page demands — so patScopes withholds
//     the scope and this check needs no special case.
func (s *Server) apiV1CreateShareLink(w http.ResponseWriter, r *http.Request, c *apiCaller, owner, instance string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !requireScope(w, c, scopeShareLinksCreate) {
		return
	}
	if !s.apiV1CanRead(w, owner, instance, c.User) {
		return
	}
	if owner != strings.ToLower(c.User) {
		apiError(w, http.StatusForbidden, "only the owner of an instance can publish a share link for it")
		return
	}
	var req apiV1ShareLinkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "bad json")
		return
	}
	p, ok := safeRepoPath(req.Path)
	if !ok || !validRepoPath(p) {
		apiError(w, http.StatusBadRequest, "bad path")
		return
	}
	if _, ok := BlobSize("git", s.Storage.RepoDir(owner, instance), defaultRef, p); !ok {
		apiError(w, http.StatusNotFound, "no such file")
		return
	}
	token, err := s.Accounts.CreateShareLink(owner, instance, p, req.IncludeLinked)
	if err != nil {
		s.Log.Printf("api v1 create share link %s/%s %s: %v", owner, instance, p, err)
		apiError(w, http.StatusInternalServerError, "could not create a share link")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"owner":         owner,
		"instance":      instance,
		"path":          p,
		"url":           s.PublicURL() + sharePrefix + token,
		"token":         token,
		"includeLinked": req.IncludeLinked,
	})
}
