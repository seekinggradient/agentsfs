package hub

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
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
// → userForToken). It is strictly additive and scoped: it only ever serves
// repos the PAT's user OWNS or is a COLLABORATOR on — never another user's
// private repos, and (deliberately) not even other users' public repos, since
// this is the agent's own knowledge-base surface, not a discovery API.

// apiAgentPrefix is the mount point for the hosted-parity agent API. "api" is a
// reserved username (meta.go reservedNames), so this can never shadow a user
// namespace.
const apiAgentPrefix = "/api/agent/v1/"

// handleAPIAgent authenticates the caller by PAT and dispatches to the versioned
// agent API. Every route is gated on the same token → user resolution as the LLM
// proxy; the per-repo routes additionally enforce owner/collaborator access.
func (s *Server) handleAPIAgent(w http.ResponseWriter, r *http.Request) {
	user, ok := s.userForToken(tokenFromRequest(r))
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, apiAgentPrefix)
	head, tail := splitFirst(rest)
	switch head {
	case "repos":
		s.apiListRepos(w, r, user)
	case "usage":
		s.apiUsage(w, r, user)
	case "commit":
		s.apiCommit(w, r, user)
	case "threads":
		s.apiThreadsIndex(w, r, user)
	case "thread":
		s.apiThread(w, r, user, tail)
	case "repo":
		s.apiRepoRoute(w, r, user, tail)
	default:
		apiError(w, http.StatusNotFound, "unknown endpoint")
	}
}

// apiRepoRoute handles /repo/<owner>/<repo>/<action>. It resolves and access-
// checks the repo once, then dispatches the read action.
func (s *Server) apiRepoRoute(w http.ResponseWriter, r *http.Request, user, tail string) {
	owner, rest := splitFirst(tail)
	repo, action := splitFirst(rest)
	owner = strings.ToLower(owner)
	if owner == "" || repo == "" || !nameRe.MatchString(owner) || !nameRe.MatchString(repo) {
		apiError(w, http.StatusNotFound, "no such repo")
		return
	}
	canRead, _ := s.apiRepoAccess(owner, repo, user)
	// 404 (not 403) when the repo is missing OR the caller has no read access, so
	// the API never confirms the existence of a repo the caller can't see.
	if !s.Storage.Exists(owner, repo) || !canRead {
		apiError(w, http.StatusNotFound, "no such repo")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	bare := s.Storage.RepoDir(owner, repo)
	switch action {
	case "resolve":
		s.apiResolve(w, owner, repo, bare)
	case "file":
		s.apiFile(w, r, bare)
	case "tree":
		s.apiTree(w, r, owner, repo, bare)
	case "search":
		s.apiSearch(w, r, owner, repo, bare)
	default:
		apiError(w, http.StatusNotFound, "unknown repo action")
	}
}

// apiRepoAccess reports the caller's access to owner/repo under the agent API's
// strict scope: the owner has full access; a collaborator has their granted
// role; everyone else has none (public repos of OTHER users are intentionally
// excluded — this is a per-user KB surface, not discovery).
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
	return false, false
}

// --- reads: list, resolve, file, tree, search -----------------------------

// apiRepoJSON is one entry in the repos listing.
type apiRepoJSON struct {
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	Description string `json:"description,omitempty"`
	Head        string `json:"head"` // current HEAD commit id ("" = empty repo)
	Role        string `json:"role"` // "owner" | "write" | "read"
	Public      bool   `json:"public"`
}

// apiListRepos lists every repo the caller owns or collaborates on, each with
// its root description and current HEAD — the entry point a hosted agent uses to
// discover its knowledge bases and pin a revision per repo.
func (s *Server) apiListRepos(w http.ResponseWriter, r *http.Request, user string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	out := struct {
		User  string        `json:"user"`
		Repos []apiRepoJSON `json:"repos"`
	}{User: user, Repos: []apiRepoJSON{}}

	own, _ := s.Storage.ListRepos(user)
	for _, name := range own {
		desc, _, _ := s.repoMeta(user, name)
		out.Repos = append(out.Repos, apiRepoJSON{
			Owner: user, Repo: name, Description: desc,
			Head: headOID("git", s.Storage.RepoDir(user, name), defaultRef),
			Role: "owner", Public: s.isPublic(user, name),
		})
	}
	for _, sr := range s.Accounts.ReposSharedWith(user) {
		if !s.Storage.Exists(sr.Owner, sr.Repo) {
			continue
		}
		desc, _, _ := s.repoMeta(sr.Owner, sr.Repo)
		out.Repos = append(out.Repos, apiRepoJSON{
			Owner: sr.Owner, Repo: sr.Repo, Description: desc,
			Head: headOID("git", s.Storage.RepoDir(sr.Owner, sr.Repo), defaultRef),
			Role: sr.Role, Public: s.isPublic(sr.Owner, sr.Repo),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// apiResolve maps HEAD to a concrete commit id — the revision a caller pins for
// the rest of its unit of work. An empty repo resolves to "".
func (s *Server) apiResolve(w http.ResponseWriter, owner, repo, bare string) {
	writeJSON(w, http.StatusOK, map[string]string{
		"owner": owner,
		"repo":  repo,
		"head":  headOID("git", bare, defaultRef),
	})
}

// apiFile serves the raw bytes of one file at a pinned revision (git show
// rev:path). The resolved rev and the repo's current HEAD ride along in headers
// so the caller can detect and record skew. 400 on a bad rev, 404 on an unknown
// path.
func (s *Server) apiFile(w http.ResponseWriter, r *http.Request, bare string) {
	p, ok := safeRepoPath(r.URL.Query().Get("path"))
	if !ok {
		apiError(w, http.StatusBadRequest, "bad path")
		return
	}
	rev := r.URL.Query().Get("rev")
	oid, status := resolveRev(bare, rev)
	if status != http.StatusOK {
		apiError(w, status, "bad rev")
		return
	}
	size, ok := BlobSize("git", bare, oid, p)
	if !ok {
		apiError(w, http.StatusNotFound, "unknown path")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Afs-Rev", oid)
	w.Header().Set("X-Afs-Head", headOID("git", bare, defaultRef))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if r.Method == http.MethodHead {
		return
	}
	if err := StreamBlob("git", bare, oid, p, w); err != nil {
		// Header already committed; best effort.
		return
	}
}

// apiTreeEntry is one node in a tree listing.
type apiTreeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // "blob" | "tree"
	Size int64  `json:"size,omitempty"`
}

// apiTree lists the tree at a pinned revision under dir, to a bounded depth.
// depth defaults to 1 (immediate children); depth<=0 means unbounded. dir ""
// is the repo root. Paths are repo-relative. 400 on a bad rev, 404 on an unknown
// dir.
func (s *Server) apiTree(w http.ResponseWriter, r *http.Request, owner, repo, bare string) {
	rev := r.URL.Query().Get("rev")
	oid, status := resolveRev(bare, rev)
	if status != http.StatusOK {
		apiError(w, status, "bad rev")
		return
	}
	dir := strings.Trim(r.URL.Query().Get("dir"), "/")
	if dir != "" {
		if clean, ok := safeRepoPath(dir); ok {
			dir = clean
		} else {
			apiError(w, http.StatusBadRequest, "bad dir")
			return
		}
	}
	depth := 1
	if d := r.URL.Query().Get("depth"); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil {
			apiError(w, http.StatusBadRequest, "bad depth")
			return
		}
		depth = n
	}

	head := headOID("git", bare, defaultRef)
	out := struct {
		Repo    string         `json:"repo"`
		Rev     string         `json:"rev"`
		Head    string         `json:"head"`
		Skew    bool           `json:"skew"`
		Dir     string         `json:"dir"`
		Entries []apiTreeEntry `json:"entries"`
	}{Repo: owner + "/" + repo, Rev: oid, Head: head, Skew: oid != head, Dir: dir, Entries: []apiTreeEntry{}}

	if oid == "" { // empty repo
		writeJSON(w, http.StatusOK, out)
		return
	}
	treeish := oid
	if dir != "" {
		treeish = oid + ":" + dir
	}
	cmd := exec.Command("git", "-C", bare, "ls-tree", "-r", "-t", "-l", "-z", treeish)
	raw, err := cmd.Output()
	if err != nil {
		apiError(w, http.StatusNotFound, "unknown dir")
		return
	}
	for _, rec := range strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00") {
		if rec == "" {
			continue
		}
		// Format: "<mode> <type> <oid> <size>\t<path>" (-l adds size; trees show "-").
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		fields := strings.Fields(rec[:tab])
		if len(fields) < 3 {
			continue
		}
		rel := rec[tab+1:]
		if depth > 0 && strings.Count(rel, "/")+1 > depth {
			continue
		}
		full := rel
		if dir != "" {
			full = dir + "/" + rel
		}
		e := apiTreeEntry{Path: full, Type: fields[1]}
		if fields[1] == "blob" && len(fields) >= 4 {
			e.Size, _ = strconv.ParseInt(fields[3], 10, 64)
		}
		out.Entries = append(out.Entries, e)
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Path < out.Entries[j].Path })
	writeJSON(w, http.StatusOK, out)
}

// apiSearchResult is one search hit.
type apiSearchResult struct {
	Path    string `json:"path"`
	Matches int    `json:"matches"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
	AtRev   bool   `json:"at_rev"` // true when the snippet was read at the pinned rev
}

// apiSearch ranks matches at HEAD (the cheapest place — the tree is already
// checked out into git's object cache and needs no per-rev index) but serves the
// returned snippet CONTENT read at the pinned rev, flagging skew when HEAD≠rev
// (kb-access-and-isolation.md, the "search at HEAD, serve reads at rev" variant).
// When rev==HEAD — the common per-turn pin — there is no skew and every snippet
// is exact. When they differ and a matched file diverged at rev, the hit is still
// returned with at_rev=false and the HEAD line as a best-effort snippet.
func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request, owner, repo, bare string) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		apiError(w, http.StatusBadRequest, "empty query")
		return
	}
	if len(q) > 512 {
		q = q[:512]
	}
	rev := r.URL.Query().Get("rev")
	oid, status := resolveRev(bare, rev)
	if status != http.StatusOK {
		apiError(w, status, "bad rev")
		return
	}
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}

	head := headOID("git", bare, defaultRef)
	out := struct {
		Repo    string            `json:"repo"`
		Rev     string            `json:"rev"`
		Head    string            `json:"head"`
		Skew    bool              `json:"skew"`
		Query   string            `json:"query"`
		Results []apiSearchResult `json:"results"`
	}{Repo: owner + "/" + repo, Rev: oid, Head: head, Skew: oid != head, Query: q, Results: []apiSearchResult{}}

	if head == "" { // empty repo
		writeJSON(w, http.StatusOK, out)
		return
	}

	ranked := gitGrepRank(bare, head, q)
	for _, m := range ranked {
		if len(out.Results) >= limit {
			break
		}
		res := apiSearchResult{Path: m.path, Matches: m.count, Line: m.line, Snippet: m.text, AtRev: false}
		// Prefer the snippet as read at the pinned rev.
		if content, ok := BlobContent("git", bare, oid, m.path); ok {
			if ln, txt, found := firstMatchLine(content, q); found {
				res.Line, res.Snippet, res.AtRev = ln, txt, true
			}
		}
		out.Results = append(out.Results, res)
	}
	writeJSON(w, http.StatusOK, out)
}

// grepMatch is a ranked file match from git grep at HEAD.
type grepMatch struct {
	path  string
	count int
	line  int
	text  string
}

// gitGrepRank greps the tree at rev for lines containing ALL whitespace-
// separated terms (fixed-string, case-insensitive), ranked by match count desc
// then path. It is the cheap ranking pass; snippet content is re-read at the
// pinned rev by the caller.
func gitGrepRank(bare, rev, query string) []grepMatch {
	args := []string{"-C", bare, "grep", "-n", "-I", "-i", "-F", "--all-match"}
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return nil
	}
	for _, t := range terms {
		args = append(args, "-e", t)
	}
	args = append(args, rev)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil // git grep exits 1 on no match — treat as empty
	}
	byPath := map[string]*grepMatch{}
	var order []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		// Format with a rev: "<rev>:<path>:<lineno>:<text>".
		rest := strings.TrimPrefix(line, rev+":")
		p, r2, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		lnStr, text, ok := strings.Cut(r2, ":")
		if !ok {
			continue
		}
		m := byPath[p]
		if m == nil {
			ln, _ := strconv.Atoi(lnStr)
			m = &grepMatch{path: p, line: ln, text: strings.TrimSpace(clip(text, 240))}
			byPath[p] = m
			order = append(order, p)
		}
		m.count++
	}
	ranked := make([]grepMatch, 0, len(order))
	for _, p := range order {
		ranked = append(ranked, *byPath[p])
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].count != ranked[j].count {
			return ranked[i].count > ranked[j].count
		}
		return ranked[i].path < ranked[j].path
	})
	return ranked
}

// firstMatchLine returns the first 1-based line of content containing every
// whitespace-separated term of query (case-insensitive), and its trimmed text.
func firstMatchLine(content, query string) (int, string, bool) {
	terms := strings.Fields(strings.ToLower(query))
	for i, line := range strings.Split(content, "\n") {
		low := strings.ToLower(line)
		all := true
		for _, t := range terms {
			if !strings.Contains(low, t) {
				all = false
				break
			}
		}
		if all {
			return i + 1, strings.TrimSpace(clip(line, 240)), true
		}
	}
	return 0, "", false
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

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
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
