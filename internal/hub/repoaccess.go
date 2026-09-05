package hub

import (
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

// repoaccess.go holds the agent API's capability logic as plain, HTTP-free
// methods on *Server — one core, thin wrappers. Everything a hosted agent can
// DO to a workspace (list, resolve, read, tree, search, commit, create)
// lives here as a function that takes already-parsed arguments and returns a
// typed result or a typed error, never touching an http.ResponseWriter or
// *http.Request. The JSON handlers in apiagent*.go are the first wrapper: they
// parse HTTP input, call one of these methods, and translate the result back to
// the exact wire shape the Eve client already speaks. An upcoming MCP adapter is
// the second wrapper over the SAME methods, so the two surfaces can never drift
// on access rules, revision-pin semantics, or CAS conflict handling — the
// behavior is defined once, here, and both transports merely re-encode it.
//
// The split is deliberately mechanical: no capability decision moved, none was
// added. The access checks (owner/collaborator/public, 404-vs-403 ordering) and
// the git plumbing are byte-for-byte what the handlers ran inline before; only
// their home changed, so both transports share one audited implementation.

// accessError carries an HTTP status alongside a message so a capability failure
// can travel out of an HTTP-free core method and still be mapped back to the
// exact status the agent API has always returned. The HTTP handlers unwrap it
// via writeAccessError; the MCP adapter will map the SAME status values onto
// tool-level errors, so a "bad rev" is a 400 on both surfaces without either
// wrapper re-deciding what counts as which failure. It intentionally mirrors the
// old inline apiError(w, status, msg) calls one-for-one — the status and message
// are unchanged, only their delivery is deferred to the wrapper.
type accessError struct {
	status int
	msg    string
}

// Error lets an accessError satisfy the error interface; the message is the same
// human string the endpoint used to write into the {"error": ...} body.
func (e *accessError) Error() string { return e.msg }

// accessErr constructs an accessError. It reads at call sites exactly like the
// apiError(w, status, msg) it replaces, so the mechanical extraction stays easy
// to audit against the original handlers.
func accessErr(status int, msg string) *accessError {
	return &accessError{status: status, msg: msg}
}

// conflictError is the CAS-specific failure a commit returns when HEAD has moved
// into the write's path range (or raced the ref update). It is distinct from
// accessError because the wire shape is richer: the HTTP handler maps it to the
// 409 writeConflict body {currentHead, conflictPaths}, not to a plain {"error"}.
// head is the current HEAD to re-read against; paths is the overlapping set (nil
// for a lost update-ref race with no identified overlap — writeConflict renders
// nil as an empty, sorted list, preserving the empty-vs-absent detail exactly).
type conflictError struct {
	head  string
	paths []string
}

// Error satisfies the error interface; the string matches the "error" field the
// 409 body has always carried, so nothing observable depends on distinguishing
// the conflict flavors by message.
func (e *conflictError) Error() string { return "head moved" }

// --- reads: list, resolve, file, tree, search -----------------------------

// RepoList returns every repo the caller owns or collaborates on, each with its
// root description and current HEAD — the ambient discovery surface an agent
// pins a revision against. It deliberately never surfaces another user's public
// repo: discovery stays exactly owned+shared, and a public repo only enters an
// agent's scope when the caller NAMES it (see apiRepoAccess). The slice is
// initialized non-nil so the wrapper renders an empty listing as [] rather than
// null, matching the Eve client's expectation.
func (s *Server) RepoList(user string) []apiRepoJSON {
	repos := []apiRepoJSON{}
	own, _ := s.Storage.ListRepos(user)
	for _, name := range own {
		desc, _, _ := s.repoMeta(user, name)
		repos = append(repos, apiRepoJSON{
			Owner: user, Name: name, Repo: name, Description: desc,
			Head: headOID("git", s.Storage.RepoDir(user, name), defaultRef),
			Role: "owner", Public: s.isPublic(user, name),
		})
	}
	for _, sr := range s.Accounts.ReposSharedWith(user) {
		if !s.Storage.Exists(sr.Owner, sr.Repo) {
			continue
		}
		desc, _, _ := s.repoMeta(sr.Owner, sr.Repo)
		repos = append(repos, apiRepoJSON{
			Owner: sr.Owner, Name: sr.Repo, Repo: sr.Repo, Description: desc,
			Head: headOID("git", s.Storage.RepoDir(sr.Owner, sr.Repo), defaultRef),
			Role: sr.Role, Public: s.isPublic(sr.Owner, sr.Repo),
		})
	}
	return repos
}

// RepoResolve maps HEAD to a concrete commit id — the revision a caller pins for
// the rest of its unit of work. An empty repo resolves to "". Callers guard
// access before resolving (the route dispatch does the 404 check); this is the
// pure "what is HEAD right now" step, shared verbatim by every read.
func (s *Server) RepoResolve(owner, repo string) (rev string) {
	return headOID("git", s.Storage.RepoDir(owner, repo), defaultRef)
}

// RepoFileInfo validates a file read at a pinned revision WITHOUT buffering the
// blob: it path-jails the request, resolves the rev, and stats the blob's size,
// returning the resolved oid and the repo's current HEAD so the caller can
// record skew. It is the metadata half of a file read — the HTTP handler uses it
// to set headers and then streams the bytes straight from git (StreamBlob), so a
// large media object never lands in the hub's heap. The pinned oid and HEAD are
// read in the same order the endpoint always used (HEAD only after the blob is
// known to exist), so the reported skew is timing-identical to before. Errors
// are accessErrors carrying the exact status the endpoint returned: 400 for a
// bad path or rev, 404 for a path absent at the pin.
func (s *Server) RepoFileInfo(owner, repo, rev, path string) (oid, head string, size int64, err error) {
	bare := s.Storage.RepoDir(owner, repo)
	p, ok := safeRepoPath(path)
	if !ok {
		return "", "", 0, accessErr(http.StatusBadRequest, "bad path")
	}
	oid, status := resolveRev(bare, rev)
	if status != http.StatusOK {
		return "", "", 0, accessErr(status, "bad rev")
	}
	size, ok = BlobSize("git", bare, oid, p)
	if !ok {
		return "", "", 0, accessErr(http.StatusNotFound, "unknown path")
	}
	return oid, headOID("git", bare, defaultRef), size, nil
}

// RepoReadFile is the buffering counterpart to RepoFileInfo: it returns the full
// bytes of a file at a pinned rev, plus the resolved oid and current HEAD. The
// HTTP handler never needs it — it streams — but a transport with no
// ResponseWriter to stream into (the MCP adapter) does. Validation and error
// mapping are identical to RepoFileInfo (400 bad path/rev, 404 absent at the
// pin), so the two paths can never disagree on what a file read is allowed to do.
func (s *Server) RepoReadFile(owner, repo, rev, path string) (content []byte, oid, head string, err error) {
	bare := s.Storage.RepoDir(owner, repo)
	p, ok := safeRepoPath(path)
	if !ok {
		return nil, "", "", accessErr(http.StatusBadRequest, "bad path")
	}
	oid, status := resolveRev(bare, rev)
	if status != http.StatusOK {
		return nil, "", "", accessErr(status, "bad rev")
	}
	c, ok := BlobContent("git", bare, oid, p)
	if !ok {
		return nil, "", "", accessErr(http.StatusNotFound, "unknown path")
	}
	return []byte(c), oid, headOID("git", bare, defaultRef), nil
}

// treeResult is the HTTP-free shape of a tree listing. The wrapper adds the
// Repo ("owner/repo") label at encode time; everything an agent reasons about —
// the pinned Rev, current Head, Skew flag, listed Dir, and sorted Entries — is
// carried here so the MCP adapter renders the same view without re-deriving it.
type treeResult struct {
	Rev     string
	Head    string
	Skew    bool
	Dir     string
	Entries []apiTreeEntry
}

// RepoTree lists the tree at a pinned revision under dir, to a bounded depth.
// depth<=0 means unbounded; dir "" is the repo root. It resolves the rev and
// path-jails dir itself (both capability checks shared with MCP), reads HEAD for
// the skew flag, and shells ls-tree exactly as the endpoint did. depth is passed
// pre-parsed: converting the query string to an int is a pure HTTP concern the
// wrapper owns (an int can never be "malformed"), so the "bad depth" 400 never
// reaches this method. Errors are accessErrors: 400 for a bad rev or dir, 404
// for a dir absent at the pin.
func (s *Server) RepoTree(owner, repo, rev, dir string, depth int) (treeResult, error) {
	bare := s.Storage.RepoDir(owner, repo)
	oid, status := resolveRev(bare, rev)
	if status != http.StatusOK {
		return treeResult{}, accessErr(status, "bad rev")
	}
	dir = strings.Trim(dir, "/")
	if dir != "" {
		if clean, ok := safeRepoPath(dir); ok {
			dir = clean
		} else {
			return treeResult{}, accessErr(http.StatusBadRequest, "bad dir")
		}
	}

	head := headOID("git", bare, defaultRef)
	out := treeResult{Rev: oid, Head: head, Skew: oid != head, Dir: dir, Entries: []apiTreeEntry{}}

	if oid == "" { // empty repo
		return out, nil
	}
	treeish := oid
	if dir != "" {
		treeish = oid + ":" + dir
	}
	cmd := exec.Command("git", "-C", bare, "ls-tree", "-r", "-t", "-l", "-z", treeish)
	raw, err := cmd.Output()
	if err != nil {
		return treeResult{}, accessErr(http.StatusNotFound, "unknown dir")
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
		var e apiTreeEntry
		switch fields[1] {
		case "blob":
			e = apiTreeEntry{Path: full, Type: "file"}
			if len(fields) >= 4 {
				e.Size, _ = strconv.ParseInt(fields[3], 10, 64)
			}
		case "tree":
			e = apiTreeEntry{Path: full, Type: "dir"}
		default: // commit (submodule) etc. — not part of a workspace
			continue
		}
		out.Entries = append(out.Entries, e)
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Path < out.Entries[j].Path })
	return out, nil
}

// searchResult is the HTTP-free shape of a search response. The wrapper adds the
// Repo label; the pinned Rev, current Head, Skew flag, echoed Query, ranked
// Results, and optional hydrated Pack are all carried here so the MCP adapter
// gets an identical view, including the "search at HEAD, serve at the pin"
// contract already resolved in the fields.
type searchResult struct {
	Rev     string
	Head    string
	Skew    bool
	Query   string
	Results []apiSearchResult
	Pack    *apiSearchPack
}

// RepoSearch ranks matches with the core retrieval pipeline over a cached,
// text-only checkout of HEAD, so the hub and a local `afs search` return
// identical results. It preserves the endpoint's exact semantics:
//
//   - HEAD is read ONCE and reused as the skew baseline. Resolving an unpinned
//     rev through resolveRev would read HEAD a second time; a push landing
//     between the two reads would then falsely report skew for a request that
//     never pinned — so the single read is load-bearing, not incidental.
//   - Ranking is always at HEAD; each result's snippet is verified against the
//     PINNED rev (at_rev), and in context mode each pack doc is re-read at the pin.
//   - A cache/index or search failure DEGRADES to an empty result set (logged,
//     no error): the surface stays available and the next query retries the
//     build, so a transient index problem never turns into a 500.
//
// limit and ctxBudget arrive pre-parsed from the wrapper; limit is defaulted to
// 20 and clamped to 100 here so both transports share the bound, and a
// non-positive ctxBudget simply skips the hydrated pack. A bad rev is the only
// hard error (accessError 400); everything else is best-effort.
func (s *Server) RepoSearch(owner, repo, q, rev string, limit, ctxBudget int) (searchResult, error) {
	bare := s.Storage.RepoDir(owner, repo)
	q = strings.TrimSpace(q)
	if q == "" {
		return searchResult{}, accessErr(http.StatusBadRequest, "empty query")
	}
	if len(q) > 512 {
		q = q[:512]
	}
	// Read HEAD exactly once and reuse it for an unpinned request; for an explicit
	// rev, resolve it but keep the single HEAD read for the skew comparison.
	head := headOID("git", bare, defaultRef)
	oid := head
	if rev != "" && rev != "HEAD" {
		resolved, status := resolveRev(bare, rev)
		if status != http.StatusOK {
			return searchResult{}, accessErr(status, "bad rev")
		}
		oid = resolved
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	out := searchResult{Rev: oid, Head: head, Skew: oid != head, Query: q, Results: []apiSearchResult{}}

	if head == "" { // empty repo
		return out, nil
	}

	cacheDir, err := s.search.ensure(owner, repo, bare, head)
	if err != nil {
		// A cache/index failure degrades to an empty result set rather than an
		// error: the surface stays available and the next query retries the build.
		s.Log.Printf("search cache %s/%s: %v", owner, repo, err)
		return out, nil
	}

	results, err := core.Search(cacheDir, q, limit)
	if err != nil {
		s.Log.Printf("search %s/%s: %v", owner, repo, err)
		return out, nil
	}
	for _, m := range results {
		res := apiSearchResult{Path: m.Path, Heading: m.Heading, Snippet: m.Snippet}
		// Verify the snippet against the bytes at the pin (== HEAD when no skew),
		// and locate its line when cheap. A file absent or diverged at the pin
		// leaves at_rev=false — best-effort, never fatal.
		if content, ok := BlobContent("git", bare, oid, m.Path); ok {
			if line, found := locateSnippet(content, m.Snippet); found {
				res.Line, res.AtRev = line, true
			} else if oid == head {
				// At HEAD the snippet came from this very content, so it is present
				// even when there is no highlighted term to pin a line to (e.g. a
				// description/structural snippet).
				res.AtRev = true
			}
		}
		out.Results = append(out.Results, res)
	}

	if ctxBudget > 0 {
		if pack, err := core.SearchContext(cacheDir, q, ctxBudget); err == nil {
			out.Pack = serializePack(pack, bare, oid, head)
		} else {
			s.Log.Printf("search context %s/%s: %v", owner, repo, err)
		}
	}
	return out, nil
}

// --- write: commit --------------------------------------------------------

// commitResult is the HTTP-free outcome of a successful CAS commit: the new
// revision and whether it was produced by a trivial disjoint merge rather than a
// plain fast-forward. The wrapper renders NewRev under both the `newRev` and the
// alias `newHead` field the Eve client reads.
type commitResult struct {
	NewRev string
	Merged bool
}

// RepoCommit applies a revision-anchored compare-and-swap commit — the write
// half of the agent API, and the one capability where the ACCESS CHECK itself is
// the guard (not just routing), so it lives here in the core rather than in a
// wrapper. It reproduces the endpoint's decisions exactly:
//
//   - 404 before 403: a caller with NO access to owner/repo gets "no such repo"
//     (existence is never leaked); a reader who lacks write gets 403. This
//     ordering is security-relevant and is preserved verbatim.
//   - Empty repo requires an empty baseRev and creates the root commit under the
//     zero-OID "ref must not yet exist" CAS; a non-empty baseRev on an empty repo
//     is a conflict, not a bad request.
//   - A present HEAD requires baseRev; HEAD==baseRev fast-forwards, a moved-but-
//     disjoint HEAD trivially merges, and an overlapping move conflicts (409).
//
// Path failures (empty set, jail violation, duplicate), a bad baseRev, and every
// git-plumbing failure become accessErrors carrying their original status and
// message; the two conflict paths become a conflictError the wrapper maps to the
// 409 body. Nothing here writes to the wire, so the MCP adapter runs the same
// arbitration with git as the referee.
func (s *Server) RepoCommit(user string, req apiCommitRequest) (commitResult, error) {
	owner, repo, ok := splitRepoSpec(req.Repo)
	if !ok {
		return commitResult{}, accessErr(http.StatusBadRequest, "bad repo")
	}
	_, canWrite := s.apiRepoAccess(owner, repo, user)
	// 404 (not 403) when the repo is missing OR the caller has no access at all,
	// so a write route never confirms the existence of a repo the caller can't see.
	if !s.Storage.Exists(owner, repo) || !s.apiCanReach(owner, repo, user) {
		return commitResult{}, accessErr(http.StatusNotFound, "no such repo")
	}
	if !canWrite {
		return commitResult{}, accessErr(http.StatusForbidden, "no write access")
	}
	if !s.hubWritesAllowed(owner, repo) {
		return commitResult{}, accessErr(http.StatusConflict, "embedded projection is read-only on the Hub until it is upgraded with afs hub pull")
	}

	// Path-jail every change and reject empty/duplicate paths up front.
	if len(req.Changes) == 0 {
		return commitResult{}, accessErr(http.StatusBadRequest, "no changes")
	}
	seen := map[string]bool{}
	changePaths := make([]string, 0, len(req.Changes))
	for i, c := range req.Changes {
		p, ok := safeRepoPath(c.Path)
		if !ok {
			return commitResult{}, accessErr(http.StatusBadRequest, "bad path: "+c.Path)
		}
		if seen[p] {
			return commitResult{}, accessErr(http.StatusBadRequest, "duplicate path: "+p)
		}
		seen[p] = true
		req.Changes[i].Path = p
		changePaths = append(changePaths, p)
	}

	bare := s.Storage.RepoDir(owner, repo)
	branchRef, err := gitCmd("git", bare, nil, nil, "symbolic-ref", "HEAD")
	if err != nil {
		return commitResult{}, accessErr(http.StatusInternalServerError, "resolve branch")
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
		// exist" — so a concurrent first commit that wins the race makes ours a
		// conflict.
		if strings.TrimSpace(req.BaseRev) != "" {
			return commitResult{}, &conflictError{head: head, paths: nil}
		}
		parent, expectedOld = "", zeroOID
	case req.BaseRev == "":
		return commitResult{}, accessErr(http.StatusBadRequest, "baseRev required")
	default:
		baseOID := headOID("git", bare, req.BaseRev)
		if baseOID == "" || !validRev(req.BaseRev) {
			return commitResult{}, accessErr(http.StatusBadRequest, "bad baseRev")
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
				return commitResult{}, &conflictError{head: head, paths: conflicts}
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
		return commitResult{}, accessErr(http.StatusInternalServerError, "index")
	}
	idx.Close()
	os.Remove(idx.Name())
	defer os.Remove(idx.Name())
	env := append(os.Environ(), "GIT_INDEX_FILE="+idx.Name())
	if parent != "" {
		if _, err := gitCmd("git", bare, env, nil, "read-tree", parent); err != nil {
			return commitResult{}, accessErr(http.StatusInternalServerError, "read-tree")
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
				return commitResult{}, accessErr(http.StatusInternalServerError, "delete "+c.Path)
			}
			continue
		}
		blob, err := gitCmd("git", bare, env, strings.NewReader(c.Content), "hash-object", "-w", "--stdin")
		if err != nil {
			return commitResult{}, accessErr(http.StatusInternalServerError, "hash "+c.Path)
		}
		blob = strings.TrimSpace(blob)
		if _, err := gitCmd("git", bare, env, nil, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+c.Path); err != nil {
			return commitResult{}, accessErr(http.StatusInternalServerError, "stage "+c.Path)
		}
	}
	tree, err := gitCmd("git", bare, env, nil, "write-tree")
	if err != nil {
		return commitResult{}, accessErr(http.StatusInternalServerError, "write-tree")
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
		return commitResult{}, accessErr(http.StatusInternalServerError, "commit-tree")
	}
	commit = strings.TrimSpace(commit)

	// Compare-and-swap the branch. update-ref fails atomically if HEAD moved
	// again since we read it (a lost race), or — for a root commit — if the ref
	// came into existence meanwhile (expectedOld ""). Either way: conflict,
	// re-read. HEAD is re-read here so the reported currentHead is the value that
	// beat us, exactly as before.
	if _, err := gitCmd("git", bare, nil, nil, "update-ref", branchRef, commit, expectedOld); err != nil {
		return commitResult{}, &conflictError{head: headOID("git", bare, defaultRef), paths: nil}
	}
	// A ref moved: repair a dangling HEAD (defensive; branchRef is HEAD's target)
	// and let the per-repo view rebuild lazily on its next read (it keys on the
	// commit id, so the move is detected automatically).
	_ = s.Storage.EnsureHEAD(owner, repo)

	return commitResult{NewRev: commit, Merged: merged}, nil
}

// --- create ---------------------------------------------------------------

// RepoCreate provisions a new bare repo in the CALLER'S OWN namespace — user is
// always the PAT's resolved identity, never a value from the request body, so
// this can never create a repo for someone else. The repo is private by default
// and seeded with a real first commit of the embedded AgentsFS contract template
// (seedContractTemplate), so it is contract-complete from birth rather than an
// empty ref the caller must bootstrap; a non-empty description replaces the root
// INDEX.md placeholder so the listing shows a real label immediately. Failures
// map to their original statuses: 422 for an invalid name, 409 when the slug is
// taken, 500 (with the same best-effort soft-delete cleanup so a failed seed
// doesn't wedge the name) when EnsureRepo or the seed fails.
func (s *Server) RepoCreate(user, name, description string) (apiRepoJSON, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || !nameRe.MatchString(name) {
		return apiRepoJSON{}, accessErr(http.StatusUnprocessableEntity, "invalid repo name")
	}
	if s.Storage.Exists(user, name) {
		return apiRepoJSON{}, accessErr(http.StatusConflict, "a repo with that name already exists")
	}
	if err := s.Storage.EnsureRepo(user, name); err != nil {
		s.Log.Printf("ensure repo %s/%s: %v", user, name, err)
		return apiRepoJSON{}, accessErr(http.StatusInternalServerError, "create repo")
	}
	if err := repoConfigSet(s.Storage.RepoDir(user, name), "afs-hub.repository-mode", repoModeStandalone); err != nil {
		return apiRepoJSON{}, accessErr(http.StatusInternalServerError, "record repository mode")
	}
	desc := strings.TrimSpace(description)
	commit, err := seedContractTemplate(s.Storage.RepoDir(user, name), user, desc)
	if err != nil {
		s.Log.Printf("seed template %s/%s: %v", user, name, err)
		// Best-effort cleanup: an empty, unseeded bare repo left behind would
		// otherwise 409 every retry (Storage.Exists would be true) with no way to
		// recover the name — soft-delete it so the slug is free again.
		if delErr := s.Storage.DeleteRepo(user, name); delErr != nil {
			s.Log.Printf("cleanup after failed seed %s/%s: %v", user, name, delErr)
		}
		return apiRepoJSON{}, accessErr(http.StatusInternalServerError, "seed contract template")
	}
	return apiRepoJSON{
		Owner: user, Name: name, Repo: name, Description: desc,
		Head: commit, Role: "owner", Public: s.isPublic(user, name),
	}, nil
}
