package hub

import (
	"encoding/json"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"agentsfs.ai/afs/internal/core"
)

// The hosted-parity agent API — a PAT-authenticated JSON surface under
// /api/agent/v1/* that gives a hosted agent (the Eve app) the same
// revision-pinned reads + compare-and-swap writes a local `afs` checkout has,
// without cloning. It is the Hub-side implementation of the KB-access decision
// (docs/eve-migration-research kb-access-and-isolation.md, Decision D):
//
//   - Reads are revision-pinned: the caller resolves HEAD→rev once for a unit of
//     work, then serves every file/tree/search read at that rev, so a turn never
//     sees a torn or mixed-revision view even while other writers commit.
//   - Writes are CAS commits: every write names the baseRev it reasoned against;
//     the Hub fast-forwards, trivially merges a disjoint concurrent move, or
//     rejects a conflict (409) — optimistic concurrency with git as the arbiter.
//
// Auth is the same PAT path as the LLM proxy (Authorization: Bearer <afs_ PAT>
// → userForToken). It is strictly additive and scoped: an agent's permissions
// are exactly its user's permissions. Reads and writes both always work on
// repos the PAT's user OWNS or is a COLLABORATOR on. Reads ADDITIONALLY work
// on any other user's PUBLIC repo — the same read reach a signed-in user has
// in the browser — but only when the caller names it explicitly (owner/repo);
// apiListRepos never surfaces another user's public repos, so an agent's
// ambient discovery stays exactly owned+shared and this never becomes a
// discovery API. Writes to a repo the caller doesn't own or collaborate on
// stay forbidden regardless of visibility (see apiRepoAccess).

// apiAgentPrefix is the mount point for the hosted-parity agent API. "api" is a
// reserved username (meta.go reservedNames), so this can never shadow a user
// namespace.
const apiAgentPrefix = "/api/agent/v1/"

type agentAPIAuth struct {
	user       string
	credential string
	grant      *AutoGardenGrant
}

func (a agentAPIAuth) allowsRepo(owner, repo string) bool {
	return a.grant == nil || (strings.EqualFold(owner, a.grant.Username) && repo == a.grant.Repo)
}

func (a agentAPIAuth) allowsThread(tail string) bool {
	if a.grant == nil {
		return true
	}
	id, _ := splitFirst(tail)
	return id == a.grant.ThreadID
}

// handleAPIAgent authenticates the caller by PAT and dispatches to the versioned
// agent API. Every route is gated on the same token → user resolution as the LLM
// proxy; the per-repo routes additionally enforce owner/collaborator access.
func (s *Server) handleAPIAgent(w http.ResponseWriter, r *http.Request) {
	token := tokenFromRequest(r)
	auth := agentAPIAuth{}
	if grant, ok := s.autoGardenGrantForToken(token, time.Now()); ok {
		auth.user, auth.credential, auth.grant = grant.Username, token, &grant
	} else if user, ok := s.userForToken(token); ok {
		auth.user = user
	} else {
		w.Header().Set("WWW-Authenticate", "Bearer")
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, apiAgentPrefix)
	head, tail := splitFirst(rest)
	switch head {
	case "repos":
		if r.Method == http.MethodPost {
			if auth.grant != nil {
				apiError(w, http.StatusForbidden, "maintenance grants cannot create repositories")
			} else {
				s.apiCreateRepo(w, r, auth.user)
			}
		} else {
			s.apiListRepos(w, r, auth)
		}
	case "usage":
		if auth.grant != nil {
			apiError(w, http.StatusForbidden, "maintenance grants cannot access usage")
		} else {
			s.apiUsage(w, r, auth.user)
		}
	case "commit":
		s.apiCommit(w, r, auth)
	case "threads":
		if auth.grant != nil {
			apiError(w, http.StatusForbidden, "maintenance grants cannot list threads")
		} else {
			s.apiThreadsIndex(w, r, auth.user)
		}
	case "thread":
		if !auth.allowsThread(tail) {
			apiError(w, http.StatusNotFound, "no such thread")
		} else {
			s.apiThread(w, r, auth.user, tail)
		}
	case "repo":
		s.apiRepoRoute(w, r, auth, tail)
	default:
		apiError(w, http.StatusNotFound, "unknown endpoint")
	}
}

func (s *Server) autoGardenGrantForToken(token string, now time.Time) (AutoGardenGrant, bool) {
	if s.Accounts == nil {
		return AutoGardenGrant{}, false
	}
	return s.Accounts.AutoGardenGrantForToken(token, now)
}

// apiRepoRoute handles /repo/<owner>/<repo>/<action>. It resolves and access-
// checks the repo once, then dispatches the read action.
func (s *Server) apiRepoRoute(w http.ResponseWriter, r *http.Request, auth agentAPIAuth, tail string) {
	owner, rest := splitFirst(tail)
	repo, action := splitFirst(rest)
	owner = strings.ToLower(owner)
	if owner == "" || repo == "" || !nameRe.MatchString(owner) || !nameRe.MatchString(repo) {
		apiError(w, http.StatusNotFound, "no such repo")
		return
	}
	if !auth.allowsRepo(owner, repo) {
		apiError(w, http.StatusNotFound, "no such repo")
		return
	}
	canRead, _ := s.apiRepoAccess(owner, repo, auth.user)
	// 404 (not 403) when the repo is missing OR the caller has no read access, so
	// the API never confirms the existence of a repo the caller can't see.
	if !s.Storage.Exists(owner, repo) || !canRead {
		apiError(w, http.StatusNotFound, "no such repo")
		return
	}
	if action == "contract-upgrade" {
		s.apiMaintenanceContractUpgrade(w, r, auth, owner, repo)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	switch action {
	case "resolve":
		s.apiResolve(w, owner, repo)
	case "file":
		s.apiFile(w, r, owner, repo)
	case "tree":
		s.apiTree(w, r, owner, repo)
	case "search":
		s.apiSearch(w, r, owner, repo)
	case "doctor":
		s.apiMaintenanceDoctor(w, owner, repo)
	default:
		apiError(w, http.StatusNotFound, "unknown repo action")
	}
}

// apiRepoAccess reports the caller's access to owner/repo. It mirrors what the
// caller could do to this repo in the browser: the owner has full access; a
// collaborator has their granted role; and anyone may READ a repo that is
// public, with no collaborator grant at all — the same reach a signed-in
// browser user already has, since an agent's permissions are exactly its
// user's permissions. Public visibility never grants write access. This is
// deliberately still not a discovery surface: apiListRepos only ever lists
// owned + shared repos, so a public repo enters an agent's scope only when
// the caller names it explicitly (owner/repo) via apiRepoRoute or apiCommit.
func (s *Server) apiRepoAccess(owner, repo, user string) (canRead, canWrite bool) {
	if user == "" {
		return false, false
	}
	if strings.ToLower(owner) == strings.ToLower(user) {
		return true, true
	}
	role := ""
	if s.Accounts != nil {
		role = s.Accounts.CollaboratorRole(owner, repo, user)
	}
	switch role {
	case "write":
		return true, true
	case "read":
		return true, false
	}
	if s.isPublic(owner, repo) {
		return true, false
	}
	return false, false
}

// --- reads: list, resolve, file, tree, search -----------------------------

// apiRepoJSON is one entry in the repos listing. Name duplicates Repo because
// the Eve app's hub client (lib/hub-client.ts HubRepo) reads `name`; `repo`
// stays as an alias so either spelling works.
type apiRepoJSON struct {
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Description string `json:"description,omitempty"`
	Head        string `json:"head"` // current HEAD commit id ("" = empty repo)
	Role        string `json:"role"` // "owner" | "write" | "read"
	Public      bool   `json:"public"`
}

// apiListRepos lists every repo the caller owns or collaborates on, each with
// its root description and current HEAD — the entry point a hosted agent uses to
// discover its knowledge bases and pin a revision per repo. The listing itself
// is RepoList; this wrapper only enforces the method and wraps the slice in the
// {user, repos} envelope the Eve client reads.
func (s *Server) apiListRepos(w http.ResponseWriter, r *http.Request, auth agentAPIAuth) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	repos := s.RepoList(auth.user)
	if auth.grant != nil {
		filtered := make([]apiRepoJSON, 0, 1)
		for _, repo := range repos {
			if auth.allowsRepo(repo.Owner, repo.Name) {
				filtered = append(filtered, repo)
			}
		}
		repos = filtered
	}
	writeJSON(w, http.StatusOK, struct {
		User  string        `json:"user"`
		Repos []apiRepoJSON `json:"repos"`
	}{User: auth.user, Repos: repos})
}

// apiResolve maps HEAD to a concrete commit id — the revision a caller pins for
// the rest of its unit of work. An empty repo resolves to "". `rev` and `head`
// carry the same value: the Eve client (apiResolveHead) reads `rev`, and `head`
// stays as the descriptive alias. The resolution is RepoResolve; this wrapper
// only shapes the JSON envelope.
func (s *Server) apiResolve(w http.ResponseWriter, owner, repo string) {
	rev := s.RepoResolve(owner, repo)
	writeJSON(w, http.StatusOK, map[string]string{
		"owner": owner,
		"repo":  repo,
		"rev":   rev,
		"head":  rev,
	})
}

// apiFile serves the raw bytes of one file at a pinned revision (git show
// rev:path). The resolved rev and the repo's current HEAD ride along in headers
// so the caller can detect and record skew. 400 on a bad rev, 404 on an unknown
// path. RepoFileInfo does the path jail, rev resolution, and blob stat; this
// wrapper keeps the streaming leg (StreamBlob straight to the socket) so a large
// media object never buffers in the hub's heap — the reason the byte-returning
// RepoReadFile is left to non-streaming transports.
func (s *Server) apiFile(w http.ResponseWriter, r *http.Request, owner, repo string) {
	oid, head, size, err := s.RepoFileInfo(owner, repo, r.URL.Query().Get("rev"), r.URL.Query().Get("path"))
	if err != nil {
		writeAccessError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Afs-Rev", oid)
	w.Header().Set("X-Afs-Head", head)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if r.Method == http.MethodHead {
		return
	}
	// The path already passed the jail inside RepoFileInfo; re-clean it (a pure
	// function) to hand StreamBlob the same value the stat used.
	p, _ := safeRepoPath(r.URL.Query().Get("path"))
	if err := StreamBlob("git", s.Storage.RepoDir(owner, repo), oid, p, w); err != nil {
		// Header already committed; best effort.
		return
	}
}

// apiTreeEntry is one node in a tree listing. Type uses the Eve client's
// vocabulary ("file" | "dir" — lib/hub-client.ts HubTreeEntry), not git's
// blob/tree.
type apiTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "file" | "dir"
	Size int64  `json:"size,omitempty"`
}

// apiTree lists the tree at a pinned revision under dir, to a bounded depth.
// depth defaults to 1 (immediate children); depth<=0 means unbounded. dir ""
// is the repo root. Paths are repo-relative. 400 on a bad rev, 404 on an unknown
// dir. The listing is RepoTree; this wrapper owns only the pure-HTTP concerns:
// parsing the depth query string to an int (an int can't be "malformed", so the
// "bad depth" 400 stays here) and adding the "owner/repo" Repo label at encode
// time. depth is parsed before RepoTree runs, so a malformed depth is rejected up
// front — the same 400 status the endpoint always returned.
func (s *Server) apiTree(w http.ResponseWriter, r *http.Request, owner, repo string) {
	depth := 1
	if d := r.URL.Query().Get("depth"); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil {
			apiError(w, http.StatusBadRequest, "bad depth")
			return
		}
		depth = n
	}
	res, err := s.RepoTree(owner, repo, r.URL.Query().Get("rev"), r.URL.Query().Get("dir"), depth)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Repo    string         `json:"repo"`
		Rev     string         `json:"rev"`
		Head    string         `json:"head"`
		Skew    bool           `json:"skew"`
		Dir     string         `json:"dir"`
		Entries []apiTreeEntry `json:"entries"`
	}{Repo: owner + "/" + repo, Rev: res.Rev, Head: res.Head, Skew: res.Skew, Dir: res.Dir, Entries: res.Entries})
}

// apiSearchResult is one section-level hit from the core retrieval pipeline.
// heading names the matched section; line is 1-based when the snippet could be
// located in the served text and 0 otherwise (kept for wire compatibility);
// at_rev is true when the snippet text is verified present at the pinned rev.
type apiSearchResult struct {
	Path    string `json:"path"`
	Heading string `json:"heading"`
	Snippet string `json:"snippet"`
	Line    int    `json:"line"`
	AtRev   bool   `json:"at_rev"`
}

// apiSearchPack is the serialized core.ContextPack returned in context mode.
type apiSearchPack struct {
	Docs                []apiPackDoc `json:"docs"`
	BudgetUsedEstTokens int          `json:"budget_used_est_tokens"`
	Pointers            []string     `json:"pointers"`
}

// apiPackDoc is one hydrated document. content is re-read at the pinned rev
// (BlobContent) when rev != head and the file exists there; at_rev records
// whether that pinned re-read happened (false ⇒ content is the HEAD-cache text).
type apiPackDoc struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
	Content     string `json:"content"`
	AtRev       bool   `json:"at_rev"`
}

// apiSearch ranks matches with the core retrieval pipeline (the same engine a
// local `afs search` runs) over a cached, sparse, text-only checkout of HEAD, so
// hub and local return identical results. It preserves the "search at HEAD,
// serve content at the pin" contract: ranking is always at HEAD; each result's
// snippet is verified against the pinned rev (at_rev); and in context mode
// (&context=N, N an estimated-token budget) each pack doc's content is re-read
// at the pin. skew flags HEAD ≠ rev, unchanged.
func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request, owner, repo string) {
	// Parse the numeric knobs from the query string (a pure HTTP concern): a
	// limit is passed through only when it parses (RepoSearch defaults it to 20
	// and clamps to 100); context=N switches on the hydrated pack only for N>0.
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil {
			limit = n
		}
	}
	ctxBudget := 0
	if c := r.URL.Query().Get("context"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			ctxBudget = n
		}
	}
	res, err := s.RepoSearch(owner, repo, r.URL.Query().Get("q"), r.URL.Query().Get("rev"), limit, ctxBudget)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Repo    string            `json:"repo"`
		Rev     string            `json:"rev"`
		Head    string            `json:"head"`
		Skew    bool              `json:"skew"`
		Query   string            `json:"query"`
		Results []apiSearchResult `json:"results"`
		Pack    *apiSearchPack    `json:"pack,omitempty"`
	}{Repo: owner + "/" + repo, Rev: res.Rev, Head: res.Head, Skew: res.Skew, Query: res.Query, Results: res.Results, Pack: res.Pack})
}

// serializePack renders a core.ContextPack for the wire, re-reading each doc's
// content at the pinned rev (BlobContent) when rev != head and the file exists
// there, so the pack honors "search at HEAD, serve content at the pin". A doc
// whose file is absent at the pin keeps its HEAD-cache content with at_rev=false.
//
// The pinned re-read is re-shaped back to ~the estimated size the HEAD-served
// pack contributed for that doc: substituting the whole file wholesale would
// discard core.SearchContext's budget shaping and blow the budget (empirically
// ~46×). BudgetUsedEstTokens is recomputed from what is actually returned so the
// reported figure matches the served content on both the HEAD and the pin paths.
func serializePack(pack core.ContextPack, bare, oid, head string) *apiSearchPack {
	out := &apiSearchPack{
		Docs:     make([]apiPackDoc, 0, len(pack.Docs)),
		Pointers: pack.Pointers,
	}
	if out.Pointers == nil {
		out.Pointers = []string{}
	}
	total := 0
	for _, d := range pack.Docs {
		doc := apiPackDoc{Path: d.Path, Description: d.Description, Reason: d.Reason, Content: d.Content}
		if oid == head {
			doc.AtRev = true // the HEAD-cache content already IS the pinned content
		} else if content, ok := BlobContent("git", bare, oid, d.Path); ok {
			// Re-read at the pin, then re-shape to ~the size core served at HEAD so
			// the pinned path stays within the budget the pack was built under.
			doc.Content, doc.AtRev = core.ShapeToBudget(content, core.EstTokens(d.Content)), true
		}
		// Mirror core.SearchContext's accounting (header + content) over the bytes
		// actually returned, so a re-shaped pin reports its real usage.
		total += core.EstTokens(doc.Path+doc.Description+doc.Reason) + core.EstTokens(doc.Content)
		out.Docs = append(out.Docs, doc)
	}
	out.BudgetUsedEstTokens = total
	return out
}

// ftsHiOpen/ftsHiClose bracket the highlighted tokens in a core FTS snippet
// (core/pipeline.go emits snippet(..., '«', '»', '…', 14)). The bracketed text
// is verbatim document content, so finding it in a file proves the snippet is
// present there.
const (
	ftsHiOpen  = '«'
	ftsHiClose = '»'
)

// snippetNeedles pulls the highlighted fragments out of a pipeline snippet.
func snippetNeedles(snippet string) []string {
	var out []string
	rest := snippet
	for {
		i := strings.IndexRune(rest, ftsHiOpen)
		if i < 0 {
			break
		}
		rest = rest[i+len(string(ftsHiOpen)):]
		j := strings.IndexRune(rest, ftsHiClose)
		if j < 0 {
			break
		}
		if frag := strings.TrimSpace(rest[:j]); frag != "" {
			out = append(out, frag)
		}
		rest = rest[j+len(string(ftsHiClose)):]
	}
	return out
}

// locateSnippet finds the 1-based line of content containing a highlighted
// snippet term (case-insensitive), proving the snippet text is present there.
// Returns (0,false) when the snippet has no highlighted term or none is found.
func locateSnippet(content, snippet string) (int, bool) {
	needles := snippetNeedles(snippet)
	if len(needles) == 0 {
		return 0, false
	}
	needle := strings.ToLower(needles[0])
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(strings.ToLower(line), needle) {
			return i + 1, true
		}
	}
	return 0, false
}

// --- shared helpers -------------------------------------------------------

// resolveRev turns a caller-supplied rev into a concrete commit id. rev "" means
// HEAD. It returns (oid, 200) on success, ("", 400) on a syntactically bad or
// non-resolving rev, and ("", 200) for an empty repo when rev is HEAD (the
// caller distinguishes an empty repo by an empty oid).
func resolveRev(bare, rev string) (string, int) {
	if rev == "" || rev == "HEAD" {
		return headOID("git", bare, defaultRef), http.StatusOK
	}
	if !validRev(rev) {
		return "", http.StatusBadRequest
	}
	oid := headOID("git", bare, rev)
	if oid == "" {
		return "", http.StatusBadRequest
	}
	return oid, http.StatusOK
}

// validRev rejects anything that isn't a plausible single revision: no leading
// dash (arg-injection guard), no range syntax ("..") and only revision-safe
// characters. It is a syntactic gate; headOID does the real resolution.
func validRev(rev string) bool {
	if rev == "" || strings.HasPrefix(rev, "-") || strings.Contains(rev, "..") {
		return false
	}
	if len(rev) > 200 {
		return false
	}
	for _, c := range rev {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '/' || c == '.' || c == '^' || c == '~' || c == '@':
		default:
			return false
		}
	}
	return true
}

// safeRepoPath jails a caller-supplied repo path: it must be relative, must not
// escape via "..", and must never touch a ".git" component (path-jail; there is
// no working tree and thus no symlink following, since every read/write goes
// through git's object store by path). Returns the cleaned slash path.
func safeRepoPath(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) || strings.Contains(p, `\`) {
		return "", false
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." || strings.EqualFold(seg, ".git") {
			return "", false
		}
	}
	return clean, true
}

// splitFirst splits "a/b/c" into ("a", "b/c"); a leading slash is ignored.
func splitFirst(p string) (first, rest string) {
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeAccessError is the single seam that maps a core capability failure back
// onto the wire. A core method (repoaccess.go) returns an accessError carrying
// the status the endpoint always used; this unwraps it into the identical
// {"error": msg} body at that status. Any other error type is an unexpected
// internal fault and becomes a generic 500 rather than leaking detail — in
// practice the read/create wrappers only ever produce accessErrors, and the
// commit wrapper handles its conflictError before reaching here.
func writeAccessError(w http.ResponseWriter, err error) {
	if ae, ok := err.(*accessError); ok {
		apiError(w, ae.status, ae.msg)
		return
	}
	apiError(w, http.StatusInternalServerError, "internal error")
}

// --- metering ingest ------------------------------------------------------

// apiUsage records a model call the hosted agent made (outside the Hub's own LLM
// proxy) into the same MetricsStore that backs /admin/metrics, attributed to the
// PAT's user. costUSD is recomputed from the price table when the caller omits
// it, so hosted-Eve usage lines up beside sprite usage in the operator view.
func (s *Server) apiUsage(w http.ResponseWriter, r *http.Request, user string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Model        string   `json:"model"`
		InputTokens  int      `json:"inputTokens"`
		OutputTokens int      `json:"outputTokens"`
		CostUSD      *float64 `json:"costUSD"`
		Endpoint     string   `json:"endpoint"`
		LatencyMs    int      `json:"latencyMs"`
		Status       int      `json:"status"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		apiError(w, http.StatusBadRequest, "bad json")
		return
	}
	cost := 0.0
	if body.CostUSD != nil {
		cost = *body.CostUSD
	} else {
		cost = costUSD(body.Model, body.InputTokens, body.OutputTokens)
	}
	status := body.Status
	if status == 0 {
		status = http.StatusOK // an ingested usage row is a completed call by default
	}
	endpoint := body.Endpoint
	if endpoint == "" {
		endpoint = "eve-agent"
	}
	s.Metrics.Record(LLMCall{
		Ts: time.Now().Unix(), User: user, Endpoint: endpoint, Model: body.Model,
		Status: status, LatencyMs: body.LatencyMs,
		InputTokens: body.InputTokens, OutputTokens: body.OutputTokens, CostUSD: cost,
	})
	writeJSON(w, http.StatusOK, map[string]any{"recorded": true, "costUSD": cost})
}
