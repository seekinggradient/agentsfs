package hub

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"agentsfs.ai/afs/internal/buildinfo"
	afsdocs "agentsfs.ai/afs/internal/docs"
)

// mcpapi.go is the third and last wrapper over the repo-access core (see
// repoaccess.go): a remote Model Context Protocol server at /mcp that lets a
// consumer AI app — ChatGPT, claude.ai, Claude Code, Cursor — search, read, and
// write the knowledge bases a hub user owns or collaborates on. Like the JSON
// agent API, it holds NO capability logic: every tool is a thin closure over a
// RepoList / RepoSearch / RepoReadFile / RepoTree / RepoCommit method, so the
// two transports can never drift on access rules, revision semantics, or CAS
// conflict handling. See agentsfs/rfcs/hub-mcp-server.md for the full design.
//
// Three things make this file more than a mechanical re-encode:
//
//   - Auth is delegated to the SDK's spec-correct bearer middleware, verifying
//     tokens through VerifyMCPBearer (OAuth access tokens first, then PATs), and
//     emitting the RFC 9728 401 challenge that points a client at our protected-
//     resource metadata. A read-only connection never even LISTS the write tool.
//   - `search` and `fetch` implement ChatGPT's exact connector contract:
//     {results:[{id,title,url}]} / {id,title,text,url,metadata}, dual-encoded as
//     BOTH structuredContent and a matching JSON string in content[0].text, with
//     absolute hub blob URLs so citations render. One pair serves ChatGPT
//     (connectors/Deep Research/Company Knowledge) and Claude alike.
//   - `search` is the one genuinely composite tool: it fans RepoSearch out across
//     every KB in RepoList and interleaves the per-repo (already-ranked) hits —
//     composition in the adapter, not a new capability in the core.
//
// The server is built per request in stateless mode: getServer reads the
// authenticated user + scopes off the request context (placed there by the auth
// middleware) and closes them into freshly-registered tools. That is why the
// tools capture `user`/`scopes` as closure variables rather than reading a
// per-call context — a stateless streamable request has no durable session to
// hang identity on, so identity is resolved once, at server construction, per
// HTTP request.

// mcpServerName is the MCP server identity advertised in the initialize
// handshake. The version tracks the shared build version (the same one the CLI
// self-reports) so a client can tell which hub build it is talking to.
const mcpServerName = "agentsfs-hub"

// mcpFetchCap bounds a fetch result's text. It sits under Claude's ~150k-char
// tool-result cap and ChatGPT's undisclosed budget; when a file is larger the
// text is cut on a UTF-8 boundary and an explicit notice tells the model to
// narrow with tree/search rather than silently losing the tail.
const mcpFetchCap = 100000

const mcpTruncNotice = "\n\n[truncated at 100000 chars — use tree/search to narrow]"

// mcpEndpoint returns the fully-wired /mcp handler: the SDK's stateless
// Streamable HTTP handler wrapped in the SDK's RequireBearerToken middleware. It
// is built once (the handler is stateless and safe to share across requests) and
// cached; the ResourceMetadataURL is fixed at first use, by which time
// PublicBaseURL is configured.
func (s *Server) mcpEndpoint() http.Handler {
	s.mcpOnce.Do(func() {
		// The verifier resolves any bearer credential through the one seam Phase B
		// exposed: OAuth access tokens (scope-bearing, expiring) first, then PATs
		// (full scope). ok=false becomes ErrInvalidToken so the middleware answers
		// 401 with the WWW-Authenticate challenge (the only status Claude honors it
		// on). The Expiration is deliberately far-future: the SDK rejects a ZERO
		// Expiration as "missing expiration", and the real expiry has already been
		// enforced inside VerifyMCPBearer (VerifyMCPToken fails a stale OAuth token
		// closed), so a valid resolution is genuinely non-expiring for the middleware.
		verifier := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
			user, scopes, ok := s.VerifyMCPBearer(token)
			if !ok {
				return nil, auth.ErrInvalidToken
			}
			return &auth.TokenInfo{
				UserID:     user,
				Scopes:     scopes,
				Expiration: time.Now().Add(10 * 365 * 24 * time.Hour),
			}, nil
		}
		// getServer builds a per-request server scoped to the authenticated user.
		// In stateless mode this runs on every request, so identity is always the
		// caller's, resolved from the TokenInfo the middleware just placed in the
		// context.
		getServer := func(r *http.Request) *mcp.Server {
			var user string
			var scopes []string
			if ti := auth.TokenInfoFromContext(r.Context()); ti != nil {
				user, scopes = ti.UserID, ti.Scopes
			}
			return s.newMCPServer(user, scopes)
		}
		handler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true})
		s.mcpHTTPHandler = auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
			ResourceMetadataURL: s.PublicURL() + "/.well-known/oauth-protected-resource/mcp",
		})(handler)
	})
	return s.mcpHTTPHandler
}

// newMCPServer builds a per-request MCP server whose tools are bound to `user`.
// Read tools are always registered; the write tool is registered ONLY when the
// connection's scopes include afs:write, so a read-only connection never even
// lists it (the RFC's "read-only is a first-class choice"). The write handler
// re-checks the scope at call time too — defense in depth for any future non-
// stateless mode where a session might outlive the scopes it was built with.
func (s *Server) newMCPServer(user string, scopes []string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: mcpServerName, Version: buildinfo.Version}, nil)
	s.addSearchTool(srv, user)
	s.addFetchTool(srv, user)
	s.addListKBsTool(srv, user)
	s.addTreeTool(srv, user)
	s.addDocsTool(srv)
	if hasScopeSlice(scopes, scopeWrite) {
		s.addWriteTool(srv, user, scopes)
	}
	return srv
}

// --- annotations ----------------------------------------------------------
//
// Annotations drive both hosts' consent UX: ChatGPT treats an unannotated tool
// as a write (⇒ per-call confirmation) and Claude bulk-approves the read-only
// group only when the hint is present, so EVERY tool carries them.

// mcpReadAnnotations marks a tool read-only and non-destructive.
func mcpReadAnnotations() *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &no}
}

// mcpWriteAnnotations marks the write tool: not read-only, but non-destructive
// and closed-world (a commit is additive and reversible via git history), and
// non-idempotent (each call is a new commit).
func mcpWriteAnnotations() *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &no, IdempotentHint: false, OpenWorldHint: &no}
}

// --- search (ChatGPT contract, cross-KB) ----------------------------------

type mcpSearchIn struct {
	Query string `json:"query" jsonschema:"words or a phrase to search for"`
	Repo  string `json:"repo,omitempty" jsonschema:"optional owner/repo to scope the search to one knowledge base; omit to search all of yours"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results (default 10, max 25)"`
}

// mcpSearchItem is one ChatGPT-contract hit: a string id that round-trips into
// fetch unchanged, a human title, and an absolute hub URL (citations render only
// when url is non-empty).
type mcpSearchItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// mcpSearchOut is the exact {results:[...]} shape ChatGPT expects. Returned as a
// typed value so the SDK dual-encodes it: structuredContent gets the JSON object
// and content[0].text gets the identical JSON string (see server.go AddTool).
type mcpSearchOut struct {
	Results []mcpSearchItem `json:"results"`
}

func (s *Server) addSearchTool(srv *mcp.Server, user string) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "search",
		Description: "Search the user's AgentsFS knowledge bases and return ranked hits as {id, title, url}. " +
			"Searches all of the user's KBs by default; pass repo=\"owner/repo\" to scope to one. " +
			"Each id is \"owner/repo/path\" — pass it to fetch to read the full file. " +
			"This is the entry point: search to find, fetch to read, then write with the fetched rev as base_rev.",
		Annotations: mcpReadAnnotations(),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpSearchIn) (*mcp.CallToolResult, mcpSearchOut, error) {
		q := strings.TrimSpace(in.Query)
		if q == "" {
			return nil, mcpSearchOut{}, errors.New("query is required")
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 10
		}
		if limit > 25 {
			limit = 25
		}

		// Resolve the scope. A named repo is access-checked exactly the way the
		// agent API's repo routes are (exists AND readable, else "no such repo" so
		// existence never leaks). The default scope is RepoList, which is already
		// owned+shared only — never a discovery surface for strangers' public repos.
		type repoRef struct{ owner, repo string }
		var scope []repoRef
		if named := strings.TrimSpace(in.Repo); named != "" {
			owner, repo, ok := splitRepoSpec(named)
			if !ok {
				return nil, mcpSearchOut{}, fmt.Errorf("bad repo %q (want owner/repo)", in.Repo)
			}
			canRead, _ := s.apiRepoAccess(owner, repo, user)
			if !s.Storage.Exists(owner, repo) || !canRead {
				return nil, mcpSearchOut{}, fmt.Errorf("no such repo: %s/%s", owner, repo)
			}
			scope = []repoRef{{owner, repo}}
		} else {
			for _, rp := range s.RepoList(user) {
				scope = append(scope, repoRef{rp.Owner, rp.Repo})
			}
		}

		// Fan RepoSearch out per repo at HEAD (rev "" = HEAD, ctxBudget 0 = no
		// hydrated pack). RepoSearch returns hits already ranked within a repo but
		// drops the numeric score (apiSearchResult has none), so cross-repo ordering
		// keeps each repo's rank order and interleaves them round-robin — fair
		// representation without inventing a global score the core no longer exposes.
		var perRepo [][]mcpSearchItem
		for _, rr := range scope {
			res, err := s.RepoSearch(rr.owner, rr.repo, q, "", limit, 0)
			if err != nil {
				continue // best-effort; a bad-rev is impossible at HEAD, and index hiccups degrade to empty
			}
			items := make([]mcpSearchItem, 0, len(res.Results))
			for _, m := range res.Results {
				items = append(items, mcpSearchItem{
					ID:    rr.owner + "/" + rr.repo + "/" + m.Path,
					Title: mcpSearchTitle(m.Path, m.Heading, rr.owner, rr.repo),
					URL:   s.blobURL(rr.owner, rr.repo, m.Path),
				})
			}
			if len(items) > 0 {
				perRepo = append(perRepo, items)
			}
		}
		return nil, mcpSearchOut{Results: interleaveSearch(perRepo, limit)}, nil
	})
}

// mcpSearchTitle renders a hit's human title: "<path> § <heading> — <owner>/<repo>",
// dropping the section when the hit has no heading.
func mcpSearchTitle(path, heading, owner, repo string) string {
	title := path
	if h := strings.TrimSpace(heading); h != "" {
		title += " § " + h
	}
	return title + " — " + owner + "/" + repo
}

// interleaveSearch merges per-repo, per-repo-ranked result lists into one list of
// at most limit items by round-robin: hit 0 of every repo, then hit 1, and so on.
// It preserves each repo's internal ranking while giving every KB a fair share of
// the cap. The returned slice is always non-nil so the wire result is [] not null.
func interleaveSearch(perRepo [][]mcpSearchItem, limit int) []mcpSearchItem {
	out := make([]mcpSearchItem, 0, limit)
	for i := 0; len(out) < limit; i++ {
		progressed := false
		for _, items := range perRepo {
			if i < len(items) {
				out = append(out, items[i])
				progressed = true
				if len(out) >= limit {
					return out
				}
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

// --- fetch (ChatGPT contract) ---------------------------------------------

type mcpFetchIn struct {
	ID string `json:"id" jsonschema:"a search result id: \"owner/repo/path\""`
}

// mcpFetchOut is ChatGPT's fetch contract. metadata is a string map (owner, repo,
// rev). Returned typed so the SDK dual-encodes it like search.
type mcpFetchOut struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Text     string            `json:"text"`
	URL      string            `json:"url"`
	Metadata map[string]string `json:"metadata"`
}

func (s *Server) addFetchTool(srv *mcp.Server, user string) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "fetch",
		Description: "Read the full content of one file by its id (\"owner/repo/path\", as returned by search). " +
			"Returns {id, title, text, url, metadata} at the current HEAD; metadata.rev is the commit id you can pass to write as base_rev. " +
			"Text is capped at 100000 characters with a truncation notice — narrow with tree or search when a file is larger.",
		Annotations: mcpReadAnnotations(),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpFetchIn) (*mcp.CallToolResult, mcpFetchOut, error) {
		owner, repo, path, ok := splitFetchID(in.ID)
		if !ok {
			return nil, mcpFetchOut{}, fmt.Errorf("bad id %q (want owner/repo/path)", in.ID)
		}
		// Same access gate as search's named-repo path: exists AND readable, else a
		// not-found that never confirms existence.
		canRead, _ := s.apiRepoAccess(owner, repo, user)
		if !s.Storage.Exists(owner, repo) || !canRead {
			return nil, mcpFetchOut{}, fmt.Errorf("no such document: %s", in.ID)
		}
		content, oid, _, err := s.RepoReadFile(owner, repo, "", path) // rev "" = HEAD
		if err != nil {
			return nil, mcpFetchOut{}, err // accessError (bad path / unknown path) surfaces as a tool error
		}
		out := mcpFetchOut{
			ID:       in.ID,
			Title:    path,
			URL:      s.blobURL(owner, repo, path),
			Metadata: map[string]string{"owner": owner, "repo": repo, "rev": oid},
		}
		// Binary (non-UTF-8) content is not shoved through a text field: return a
		// note plus the metadata so the model knows the file exists but isn't text.
		if !utf8.Valid(content) {
			out.Metadata["binary"] = "true"
			out.Text = fmt.Sprintf("[binary file: %d bytes — not shown; open %s to view]", len(content), out.URL)
			return nil, out, nil
		}
		out.Text = capFetchText(string(content))
		return nil, out, nil
	})
}

// capFetchText truncates text to mcpFetchCap on a UTF-8 rune boundary and appends
// the notice; short text is returned unchanged.
func capFetchText(text string) string {
	if len(text) <= mcpFetchCap {
		return text
	}
	cut := mcpFetchCap
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + mcpTruncNotice
}

// splitFetchID parses "owner/repo/path" — path may contain slashes, owner/repo
// cannot (nameRe). owner is lowercased to match the storage namespace.
func splitFetchID(id string) (owner, repo, path string, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(id), "/", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	owner = strings.ToLower(parts[0])
	repo = parts[1]
	path = parts[2]
	if !nameRe.MatchString(owner) || !nameRe.MatchString(repo) || path == "" {
		return "", "", "", false
	}
	return owner, repo, path, true
}

// --- list_kbs -------------------------------------------------------------

func (s *Server) addListKBsTool(srv *mcp.Server, user string) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_kbs",
		Description: "List every AgentsFS knowledge base the user owns or collaborates on — owner/repo, role, visibility, description, and current HEAD. " +
			"Use it to discover what is reachable before scoping a search or a write.",
		Annotations: mcpReadAnnotations(),
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		repos := s.RepoList(user)
		if len(repos) == 0 {
			return mcpText("No knowledge bases yet. The user can create one with the afs CLI or by pushing a repo to the hub."), nil, nil
		}
		var b strings.Builder
		for _, r := range repos {
			vis := "private"
			if r.Public {
				vis = "public"
			}
			fmt.Fprintf(&b, "%s/%s  [%s, %s]", r.Owner, r.Repo, r.Role, vis)
			if r.Head != "" {
				fmt.Fprintf(&b, "  head %s", shortRev(r.Head))
			} else {
				b.WriteString("  (empty)")
			}
			b.WriteString("\n")
			if r.Description != "" {
				fmt.Fprintf(&b, "    %s\n", r.Description)
			}
		}
		return mcpText(b.String()), nil, nil
	})
}

// --- tree -----------------------------------------------------------------

type mcpTreeIn struct {
	Repo  string `json:"repo" jsonschema:"the owner/repo to list"`
	Dir   string `json:"dir,omitempty" jsonschema:"subdirectory to scope to (default: repo root)"`
	Depth int    `json:"depth,omitempty" jsonschema:"how many levels to descend (default 2)"`
}

func (s *Server) addTreeTool(srv *mcp.Server, user string) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "tree",
		Description: "List the file tree of a knowledge base at HEAD as an indented outline (directories end in /, files show their size). " +
			"Pass dir to focus on a subdirectory and depth to cap how deep it expands. Use it to orient before fetching or writing.",
		Annotations: mcpReadAnnotations(),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpTreeIn) (*mcp.CallToolResult, any, error) {
		owner, repo, ok := splitRepoSpec(in.Repo)
		if !ok {
			return nil, nil, fmt.Errorf("bad repo %q (want owner/repo)", in.Repo)
		}
		canRead, _ := s.apiRepoAccess(owner, repo, user)
		if !s.Storage.Exists(owner, repo) || !canRead {
			return nil, nil, fmt.Errorf("no such repo: %s/%s", owner, repo)
		}
		depth := in.Depth
		if depth <= 0 {
			depth = 2
		}
		res, err := s.RepoTree(owner, repo, "", in.Dir, depth) // rev "" = HEAD
		if err != nil {
			return nil, nil, err // accessError (bad dir / unknown dir) → tool error
		}
		if len(res.Entries) == 0 {
			return mcpText("(empty)"), nil, nil
		}
		return mcpText(renderTree(res.Dir, res.Entries)), nil, nil
	})
}

// renderTree turns the flat, sorted RepoTree entries into an indented outline.
// Indentation is the entry's depth BELOW the listed dir, so a dir-scoped listing
// starts flush-left rather than inheriting the scope's own nesting.
func renderTree(base string, entries []apiTreeEntry) string {
	var b strings.Builder
	for _, e := range entries {
		rel := e.Path
		if base != "" {
			rel = strings.TrimPrefix(rel, base+"/")
		}
		indent := strings.Repeat("  ", strings.Count(rel, "/"))
		name := pathBase(e.Path)
		if e.Type == "dir" {
			fmt.Fprintf(&b, "%s%s/\n", indent, name)
		} else {
			fmt.Fprintf(&b, "%s%s (%d bytes)\n", indent, name, e.Size)
		}
	}
	return b.String()
}

// --- docs -----------------------------------------------------------------

type mcpDocsIn struct {
	Topic string `json:"topic,omitempty" jsonschema:"docs topic: agent-start, setup, hub, contract, commands, list, or all (default agent-start)"`
}

func (s *Server) addDocsTool(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "docs",
		Description: "Read bundled AgentsFS documentation. Start with topic agent-start to learn the conventions (self-describing files, INDEX.md, wikilinks, the journal) before writing to a knowledge base.",
		Annotations: mcpReadAnnotations(),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpDocsIn) (*mcp.CallToolResult, any, error) {
		topic := strings.TrimSpace(in.Topic)
		if topic == "" {
			topic = "agent-start"
		}
		out, err := afsdocs.Render(topic)
		if err != nil {
			return nil, nil, err
		}
		return mcpText(out), nil, nil
	})
}

// --- write ----------------------------------------------------------------

type mcpWriteChange struct {
	Path    string `json:"path" jsonschema:"repo-relative file path"`
	Content string `json:"content,omitempty" jsonschema:"the file's new full content (ignored when delete is true)"`
	Delete  bool   `json:"delete,omitempty" jsonschema:"set true to delete the file at path"`
}

type mcpWriteIn struct {
	Repo    string           `json:"repo" jsonschema:"the owner/repo to commit to"`
	Message string           `json:"message,omitempty" jsonschema:"a one-line commit message"`
	Changes []mcpWriteChange `json:"changes" jsonschema:"one or more file writes/deletes to apply in a single commit"`
	BaseRev string           `json:"base_rev,omitempty" jsonschema:"the revision your changes are based on (default: current HEAD); pass the rev fetch returned"`
}

func (s *Server) addWriteTool(srv *mcp.Server, user string, scopes []string) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "write",
		Description: "Commit one or more file changes to a knowledge base. Each change is {path, content} to write, or {path, delete:true} to remove. " +
			"base_rev defaults to the current HEAD; pass the rev fetch returned so a stale write is caught. " +
			"Writing to a knowledge base that doesn't exist yet under YOUR username creates it. " +
			"Every write is an attributed, revertible git commit. If HEAD moved onto a path you changed, the tool returns a conflict with the new HEAD — re-fetch and retry with that as base_rev.",
		Annotations: mcpWriteAnnotations(),
	}, func(_ context.Context, _ *mcp.CallToolRequest, in mcpWriteIn) (*mcp.CallToolResult, any, error) {
		// Defense in depth: the tool is only registered for a write-scoped
		// connection, but re-check here so a future stateful/session mode can never
		// let a read-only token reach the commit path through a reused server.
		if !hasScopeSlice(scopes, scopeWrite) {
			return nil, nil, errors.New("this connection is read-only (afs:write scope required)")
		}
		owner, repo, ok := splitRepoSpec(in.Repo)
		if !ok {
			return nil, nil, fmt.Errorf("bad repo %q (want owner/repo)", in.Repo)
		}
		if len(in.Changes) == 0 {
			return nil, nil, errors.New("at least one change is required")
		}
		// First contact creates the KB — for the OWNER only, mirroring the git
		// surface (serveGit auto-creates on an owner's first push) so "save this
		// into my AFS" works from a fresh account. RepoCreate seeds the contract
		// template, so a KB born from a consumer app is a real agentsfs instance,
		// not a bare repo. Writes into anyone else's namespace never create.
		created := false
		if owner == strings.ToLower(user) && !s.Storage.Exists(owner, repo) {
			if _, err := s.RepoCreate(owner, repo, ""); err != nil {
				return nil, nil, err
			}
			created = true
		}
		baseRev := strings.TrimSpace(in.BaseRev)
		if baseRev == "" {
			// Default to the current HEAD. For an empty repo RepoResolve yields "",
			// which RepoCommit's empty-repo rule requires (a non-empty baseRev on an
			// empty repo is a conflict, not a bad request).
			baseRev = s.RepoResolve(owner, repo)
		}
		req := apiCommitRequest{Repo: in.Repo, BaseRev: baseRev, Message: in.Message}
		for _, c := range in.Changes {
			req.Changes = append(req.Changes, apiChange{Path: c.Path, Content: c.Content, Delete: c.Delete})
		}
		// Author is left empty: RepoCommit defaults it to the token's user and
		// user@users.agentsfs, exactly as the JSON commit API does.
		res, err := s.RepoCommit(user, req)
		if err != nil {
			// A CAS conflict is NOT a tool error: return actionable guidance so the
			// model re-reads at the new HEAD and retries, rather than seeing a failure
			// it cannot interpret.
			if ce, ok := err.(*conflictError); ok {
				return mcpText(conflictGuidance(owner, repo, ce)), nil, nil
			}
			return nil, nil, err // accessError (no such repo / no write access / bad path) → tool error
		}
		msg := fmt.Sprintf("Committed %s to %s/%s (merged=%v). New HEAD: %s.",
			shortRev(res.NewRev), owner, repo, res.Merged, res.NewRev)
		if created {
			msg = fmt.Sprintf("Created knowledge base %s/%s (seeded with the AgentsFS contract). ", owner, repo) + msg
		}
		return mcpText(msg), nil, nil
	})
}

// conflictGuidance renders the human instruction a CAS conflict returns: the new
// HEAD to re-anchor against, the conflicting paths, and the exact base_rev to
// retry with.
func conflictGuidance(owner, repo string, ce *conflictError) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Conflict: %s/%s advanced to %s since your base_rev.\n", owner, repo, ce.head)
	if len(ce.paths) > 0 {
		paths := append([]string(nil), ce.paths...)
		sort.Strings(paths)
		fmt.Fprintf(&b, "Conflicting paths: %s\n", strings.Join(paths, ", "))
	}
	fmt.Fprintf(&b, "Re-fetch the affected files at the new HEAD and retry the write with base_rev=%s.", ce.head)
	return b.String()
}

// --- shared helpers -------------------------------------------------------

// mcpText wraps a plain string as a tool result (the shape the AFS-native tools
// return; the ChatGPT-contract tools use typed structured output instead).
func mcpText(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// blobURL builds the canonical, absolute hub blob URL for a file — the citation
// target ChatGPT/Claude open. Each path segment is percent-escaped, but the "/"
// separators are preserved so the URL still names a nested path.
func (s *Server) blobURL(owner, repo, path string) string {
	var b strings.Builder
	b.WriteString(s.PublicURL())
	b.WriteString("/")
	b.WriteString(url.PathEscape(owner))
	b.WriteString("/")
	b.WriteString(url.PathEscape(repo))
	b.WriteString("/blob")
	for _, seg := range strings.Split(path, "/") {
		b.WriteString("/")
		b.WriteString(url.PathEscape(seg))
	}
	return b.String()
}

// shortRev abbreviates a commit id for human-facing messages.
func shortRev(oid string) string {
	if len(oid) > 12 {
		return oid[:12]
	}
	return oid
}

// hasScopeSlice reports whether a scope slice contains want.
func hasScopeSlice(scopes []string, want string) bool {
	for _, sc := range scopes {
		if sc == want {
			return true
		}
	}
	return false
}
