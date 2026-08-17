package hub

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

// The /api/v1 save API — the fourth wrapper over the repo-access core (see
// repoaccess.go), and the first one built for a BROWSER app rather than a
// server-side agent. It is the Hub side of the Markdown To integration contract:
// markdownto.ai is a static single-page app that holds no user table and no file
// store of its own, so it signs its user in through this Hub's OAuth
// authorization server (oauth.go) and saves what they make into a real agentsfs
// instance through the endpoints here.
//
//	GET  /api/v1/me                                          profile
//	GET  /api/v1/instances                                   instances:read
//	POST /api/v1/instances                                   instances:write
//	GET  /api/v1/instances/{owner}/{inst}/files              instances:read
//	GET  /api/v1/instances/{owner}/{inst}/files/{path}       instances:read
//	PUT  /api/v1/instances/{owner}/{inst}/files/{path}       instances:write
//	POST /api/v1/instances/{owner}/{inst}/transactions       instances:write
//	POST /api/v1/instances/{owner}/{inst}/sharelinks         sharelinks:create
//
// Three properties distinguish it from the agent API next door:
//
//   - Conflicts are CONTENT-addressed, not revision-addressed. A save carries
//     `If-Match: <sha256 of the bytes you read>` and is refused with 412 when the
//     file has changed underneath it. That is deliberately the same hash the
//     Markdown To patch engine already requires on every mutation, so one
//     conflict model runs end to end from the editor to the git commit.
//   - It answers cross-origin (CORS) for a small allow-list of first-party
//     browser origins plus http loopback, because the caller is a web page. It
//     never allows credentials: a bearer token is the only way in, so an ambient
//     Hub session cookie can never be used to drive it from another site.
//   - Storage stays ordinary. A save is a real git commit in a real agentsfs
//     instance, authored by the user; the instance the API bootstraps is seeded
//     with the contract template and stays `git clone`-able like any other.
//
// It holds no capability logic of its own: access checks, the path jail, and the
// compare-and-swap commit are all RepoCommit / RepoCreate / RepoTree in
// repoaccess.go, so this surface can never drift from the git, agent, or MCP
// surfaces on who may do what.

// apiV1Prefix is the mount point. "api" is a reserved username (meta.go
// reservedNames), so this can never shadow a user namespace.
const apiV1Prefix = "/api/v1/"

// defaultInstanceName is the instance a first save bootstraps when the client
// asks for the default — the contract's "zero decisions" path: the user picks
// nothing, and what they get is a normal agentsfs instance they can clone.
const defaultInstanceName = "apps"

// defaultCollectionDir is the directory inside a bootstrapped instance that
// saved documents land in. It is declared `agentsfs_role: collection`, which is
// what makes the save legal under the AgentsFS contract: a collection describes
// its contents collectively through its INDEX.md, so files that carry a
// `markdownto:` envelope instead of a `description:` don't each become a doctor
// finding.
const defaultCollectionDir = "apps"

// defaultInstanceDescription becomes the bootstrapped instance's root INDEX.md
// description — the label the Hub's listing shows for it.
const defaultInstanceDescription = "Little apps and documents made in the browser and saved here — each one a plain markdown file you can clone, edit, or take away."

// maxFileBytes bounds a single file this API will read or write. The documents
// it exists for are markdown; anything larger belongs on the git remote (which
// has no such limit) rather than in a JSON-adjacent request.
const maxFileBytes = 8 << 20

// handleAPIV1 authenticates the caller and dispatches the versioned save API.
// CORS runs FIRST and unauthenticated: a preflight carries no Authorization
// header, so answering it with a 401 would make the browser fail the real
// request before it was ever sent.
func (s *Server) handleAPIV1(w http.ResponseWriter, r *http.Request) {
	if s.writeCORS(w, r) {
		return
	}
	if s.Accounts == nil {
		apiError(w, http.StatusNotFound, "accounts are not enabled on this hub")
		return
	}
	c, ok := s.apiV1Caller(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="agentsfs hub"`)
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	head, tail := splitFirst(strings.TrimPrefix(r.URL.Path, apiV1Prefix))
	switch head {
	case "me":
		s.apiV1Me(w, r, c)
	case "instances":
		s.apiV1Instances(w, r, c, tail)
	default:
		apiError(w, http.StatusNotFound, "unknown endpoint")
	}
}

// apiV1Instances routes everything under /instances: the collection itself
// (list, bootstrap) and the per-instance sections (files, sharelinks).
func (s *Server) apiV1Instances(w http.ResponseWriter, r *http.Request, c *apiCaller, tail string) {
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			s.apiV1ListInstances(w, c)
		case http.MethodPost:
			s.apiV1CreateInstance(w, r, c)
		default:
			w.Header().Set("Allow", "GET, POST")
			apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	owner, rest := splitFirst(tail)
	instance, rest := splitFirst(rest)
	owner = strings.ToLower(owner)
	if owner == "" || instance == "" || !nameRe.MatchString(owner) || !nameRe.MatchString(instance) {
		apiError(w, http.StatusNotFound, "no such instance")
		return
	}
	section, filePath := splitFirst(rest)
	switch section {
	case "files":
		s.apiV1Files(w, r, c, owner, instance, filePath)
	case "transactions":
		if filePath != "" {
			apiError(w, http.StatusNotFound, "unknown endpoint")
			return
		}
		s.apiV1Transaction(w, r, c, owner, instance)
	case "sharelinks":
		s.apiV1CreateShareLink(w, r, c, owner, instance)
	default:
		apiError(w, http.StatusNotFound, "unknown endpoint")
	}
}

// --- authentication + scopes ----------------------------------------------

// apiCaller is the resolved identity behind one request: who it acts as, what it
// was granted, and which registered app is holding the token (so a write can
// record the client that made it).
type apiCaller struct {
	User       string
	Scopes     map[string]bool
	ClientID   string // "" when the caller presented a PAT
	ClientName string
}

// has reports whether the caller was granted a scope.
func (c *apiCaller) has(scope string) bool { return c.Scopes[scope] }

// scopeList renders the granted scopes in canonical order, for /me.
func (c *apiCaller) scopeList() []string {
	out := []string{}
	for _, sc := range scopeOrder {
		if c.Scopes[sc] {
			out = append(out, sc)
		}
	}
	return out
}

// patScopes is what a personal access token may do on this surface. A PAT is a
// long-lived credential that lives on laptops and agent VMs, so it carries the
// read/write scopes (the same reach its owner already has over the git remote)
// but NOT sharelinks:create: minting an anonymous public URL for a private file
// is exactly what a prompt-injected agent would be steered into doing, and the
// Hub has always required a human at a browser for it (handleShareLinks). An
// OAuth token can hold that scope because a human ticked a box naming it.
func patScopes() map[string]bool {
	return map[string]bool{scopeProfile: true, scopeInstancesRead: true, scopeInstancesWrite: true}
}

// apiV1Caller resolves the bearer credential on a request. OAuth access tokens
// (scope-bearing, expiring, client-attributed) are tried first; a hub PAT is the
// deliberate power-user fallback, so the whole API is reachable with curl.
func (s *Server) apiV1Caller(r *http.Request) (*apiCaller, bool) {
	token := tokenFromRequest(r)
	if token == "" {
		return nil, false
	}
	if g, ok := s.Accounts.VerifyOAuthAccess(token); ok {
		c := &apiCaller{User: g.User, Scopes: map[string]bool{}, ClientID: g.ClientID}
		for _, sc := range scopeSlice(g.Scope) {
			c.Scopes[sc] = true
		}
		if client, ok := s.Accounts.OAuthClient(g.ClientID); ok {
			c.ClientName = client.Name
		}
		return c, true
	}
	if user, ok := s.userForToken(token); ok {
		return &apiCaller{User: user, Scopes: patScopes()}, true
	}
	return nil, false
}

// requireScope enforces one scope, answering RFC 6750's insufficient_scope 403
// (with the missing scope named, so a client knows what to ask for next time)
// and reporting whether the request may continue.
func requireScope(w http.ResponseWriter, c *apiCaller, scope string) bool {
	if c.has(scope) {
		return true
	}
	w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="`+scope+`"`)
	apiError(w, http.StatusForbidden, "insufficient scope: "+scope+" is required")
	return false
}

// --- CORS ------------------------------------------------------------------

// corsAllowedHeaders is what a preflight permits on the real request:
// Authorization for the bearer token, Content-Type for the saved bytes, If-Match
// for the conflict precondition, and X-Afs-Message for the commit message.
const corsAllowedHeaders = "Authorization, Content-Type, If-Match, X-Afs-Message"

// corsExposedHeaders is what a browser may READ off the response. Without this
// the SPA cannot see the ETag it needs for the next save's If-Match.
const corsExposedHeaders = "ETag, X-Afs-Source-Hash, X-Afs-Rev, X-Afs-Head"

// writeCORS applies the cross-origin policy and reports whether it already
// answered the request (a preflight). Two rules are load-bearing:
//
//   - The allowed origin is echoed, never "*", and Vary: Origin is always set so
//     a cache can never hand one origin's response to another.
//   - Access-Control-Allow-Credentials is never sent. This API is bearer-only,
//     so a browser can reach it with a token the user's app holds but never with
//     an ambient Hub session cookie — a cross-site page cannot ride a logged-in
//     session into someone's knowledge base.
func (s *Server) writeCORS(w http.ResponseWriter, r *http.Request) (handled bool) {
	w.Header().Add("Vary", "Origin")
	if origin := r.Header.Get("Origin"); origin != "" && s.corsOriginAllowed(origin) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Methods", "GET, HEAD, PUT, POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", corsAllowedHeaders)
		h.Set("Access-Control-Expose-Headers", corsExposedHeaders)
		h.Set("Access-Control-Max-Age", "600")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// corsOriginAllowed reports whether a browser origin may read this API's
// responses: a first-party app declared in oauth_firstparty.go, an origin the
// operator added via HUB_API_ORIGINS, or any http loopback origin (a dev server,
// whose port is unknowable in advance — and which is harmless without
// credentials, since the page still needs a bearer token to get anywhere).
func (s *Server) corsOriginAllowed(origin string) bool {
	for _, o := range firstPartyOrigins() {
		if strings.EqualFold(o, origin) {
			return true
		}
	}
	for _, o := range s.APIOrigins {
		if strings.EqualFold(strings.TrimRight(o, "/"), origin) {
			return true
		}
	}
	return isLoopbackOrigin(origin)
}

// --- /me -------------------------------------------------------------------

// apiV1DefaultTarget tells a client where a "just save it somewhere sensible"
// save should go, so the USER makes zero decisions and the CLIENT makes one
// deterministic lookup. Exists reports whether the instance is already there; it
// is informational, because a save to a missing instance bootstraps it.
type apiV1DefaultTarget struct {
	Owner    string `json:"owner"`
	Instance string `json:"instance"`
	Dir      string `json:"dir"`
	Exists   bool   `json:"exists"`
}

// apiV1Me identifies the account behind the token. It is the profile scope's
// entire surface: a username, the granted scopes, the app holding the token, and
// the default save target — no email, no password metadata, nothing else.
func (s *Server) apiV1Me(w http.ResponseWriter, r *http.Request, c *apiCaller) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		apiError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !requireScope(w, c, scopeProfile) {
		return
	}
	writeJSON(w, http.StatusOK, struct {
		User    string             `json:"user"`
		Scopes  []string           `json:"scopes"`
		Client  string             `json:"client,omitempty"`
		Default apiV1DefaultTarget `json:"defaultTarget"`
	}{
		User:   c.User,
		Scopes: c.scopeList(),
		Client: c.ClientID,
		Default: apiV1DefaultTarget{
			Owner:    c.User,
			Instance: defaultInstanceName,
			Dir:      defaultCollectionDir,
			Exists:   s.Storage.Exists(c.User, defaultInstanceName),
		},
	})
}

// --- instances -------------------------------------------------------------

// apiV1ListInstances lists the knowledge bases the caller owns or collaborates
// on. It is RepoList verbatim — the same listing the agent API and the MCP
// server show — so "my instances" means one thing across every surface.
func (s *Server) apiV1ListInstances(w http.ResponseWriter, c *apiCaller) {
	if !requireScope(w, c, scopeInstancesRead) {
		return
	}
	writeJSON(w, http.StatusOK, struct {
		User      string        `json:"user"`
		Instances []apiRepoJSON `json:"instances"`
	}{User: c.User, Instances: s.RepoList(c.User)})
}

// apiV1CreateInstanceRequest is the body of POST /api/v1/instances. Every field
// is optional: an empty body bootstraps the default instance, which is the whole
// point of the endpoint.
type apiV1CreateInstanceRequest struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Dir         *string `json:"dir"` // nil = the default collection dir; "" = none
}

// apiV1CreateInstance bootstraps an instance in the CALLER'S OWN namespace. It
// is deliberately IDEMPOTENT, unlike the agent API's create (which 409s on a
// taken slug): this is the "make sure I have somewhere to save" call a client
// makes at sign-in, and asking it twice must not be an error. An instance that
// already exists is returned as-is with created=false and is never re-seeded.
func (s *Server) apiV1CreateInstance(w http.ResponseWriter, r *http.Request, c *apiCaller) {
	if !requireScope(w, c, scopeInstancesWrite) {
		return
	}
	req := apiV1CreateInstanceRequest{}
	// An empty body is legal and means "all defaults"; only malformed JSON is an
	// error.
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			apiError(w, http.StatusBadRequest, "bad json")
			return
		}
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if name == "" {
		name = defaultInstanceName
	}
	dir := defaultCollectionDir
	if req.Dir != nil {
		dir = strings.Trim(strings.TrimSpace(*req.Dir), "/")
	}
	if dir != "" {
		if clean, ok := safeRepoPath(dir); ok {
			dir = clean
		} else {
			apiError(w, http.StatusBadRequest, "bad dir")
			return
		}
	}
	res, err := s.ensureInstance(c.User, name, dir, strings.TrimSpace(req.Description))
	if err != nil {
		writeAccessError(w, err)
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, res)
}

// apiV1Instance is what a bootstrap returns: the instance record plus what the
// bootstrap actually did, so a client can tell "I made this" from "it was
// already there" without a second call.
type apiV1Instance struct {
	Owner             string `json:"owner"`
	Instance          string `json:"instance"`
	Description       string `json:"description,omitempty"`
	Rev               string `json:"rev"`
	Created           bool   `json:"created"`
	Dir               string `json:"dir,omitempty"`
	CollectionCreated bool   `json:"collectionCreated"`
	URL               string `json:"url"`
}

// ensureInstance makes sure user/instance exists and that dir (when named) is a
// declared collection, creating only what is missing. It is the one bootstrap
// path: the explicit POST /instances and the implicit "first save into an
// instance that isn't there yet" both come through here, so they can never
// produce differently-shaped instances.
//
// What it creates is an ORDINARY agentsfs: RepoCreate seeds the same contract
// template `afs init` writes (AGENTS.md, a root INDEX.md, journal and scratch
// dirs), so a bootstrapped instance is contract-complete from birth and a plain
// `git clone` of it is a complete, self-explaining knowledge base — not a
// product-specific container.
func (s *Server) ensureInstance(user, instance, dir, description string) (apiV1Instance, error) {
	out := apiV1Instance{Owner: user, Instance: instance, Dir: dir}
	if !s.Storage.Exists(user, instance) {
		desc := description
		if desc == "" && instance == defaultInstanceName {
			desc = defaultInstanceDescription
		}
		rec, err := s.RepoCreate(user, instance, desc)
		if err != nil {
			return apiV1Instance{}, err
		}
		out.Created, out.Description, out.Rev = true, rec.Description, rec.Head
	} else {
		if _, canWrite := s.apiRepoAccess(user, instance, user); !canWrite {
			return apiV1Instance{}, accessErr(http.StatusForbidden, "no write access")
		}
		desc, _, _ := s.repoMeta(user, instance)
		out.Description, out.Rev = desc, s.RepoResolve(user, instance)
	}
	if dir != "" {
		created, rev, err := s.ensureCollectionDir(user, instance, dir)
		if err != nil {
			return apiV1Instance{}, err
		}
		if created {
			out.CollectionCreated, out.Rev = true, rev
		}
	}
	out.URL = s.PublicURL() + "/" + user + "/" + instance
	return out, nil
}

// ensureCollectionDir declares dir a collection by committing its INDEX.md, and
// does nothing at all if that INDEX.md already exists. The declaration is what
// keeps a saving client honest with the AgentsFS contract: rule 1 wants a
// `description:` in every file, and a collection (contract 0.4.0+) is the
// sanctioned exemption — a body of like items described collectively by its
// INDEX rather than one line per file. Without it, every saved document would be
// a doctor finding; with it, the instance stays clean while the files stay
// exactly the bytes their author wrote.
func (s *Server) ensureCollectionDir(owner, instance, dir string) (created bool, rev string, err error) {
	bare := s.Storage.RepoDir(owner, instance)
	head := s.RepoResolve(owner, instance)
	indexPath := dir + "/INDEX.md"
	if head != "" {
		if _, ok := BlobSize("git", bare, head, indexPath); ok {
			return false, head, nil
		}
	}
	res, err := s.RepoCommit(owner, apiCommitRequest{
		Repo:    owner + "/" + instance,
		BaseRev: head,
		Message: "Declare " + dir + "/ a collection for saved documents",
		Changes: []apiChange{{Path: indexPath, Content: collectionIndexMarkdown(dir)}},
	})
	if err != nil {
		return false, head, err
	}
	return true, res.NewRev, nil
}

// collectionIndexMarkdown is the INDEX.md that declares a saves directory. It
// carries the two keys the contract reads — a `description:` of its own and
// `agentsfs_role: collection` — and then explains itself in prose, because the
// next reader may be a human with a git clone and no idea an API wrote this.
func collectionIndexMarkdown(dir string) string {
	return "---\n" +
		"description: Documents saved here from a browser app — kanban boards, todo lists, backlogs, and notes, described collectively by this INDEX rather than one by one.\n" +
		"agentsfs_role: collection\n" +
		"---\n" +
		"\n" +
		"# " + dir + "\n" +
		"\n" +
		"Every file in this directory is a plain markdown document. Files saved from a Markdown To\n" +
		"client carry a `markdownto:` key in their own frontmatter naming the spec they conform to\n" +
		"(a kanban board, a todo list, a backlog); open one in that app to render or edit it, or just\n" +
		"read it here — it is markdown either way.\n" +
		"\n" +
		"This directory is a declared collection (`agentsfs_role: collection`), so its files are\n" +
		"described together here instead of each carrying its own `description:`. Add, rename, and\n" +
		"reorganize them freely; nothing outside this directory depends on their names.\n"
}

// --- shared helpers --------------------------------------------------------

// envelopeKey is the frontmatter key a Markdown To document declares its spec
// in (`markdownto: kanban@1`). The Hub reads exactly this one key and nothing
// else about the format: detecting it is what lets a listing answer "show me the
// little apps I made" and what a renderer keys off later. No Markdown To
// semantics enter the Hub with it.
const envelopeKey = "markdownto"

// fileMeta is the frontmatter a listing reports for one document, read with
// core's own parser so the Hub can never disagree with `afs` about what a file
// says it is.
type fileMeta struct {
	Envelope    string
	Description string
	Title       string
}

// readFileMeta extracts the reported frontmatter from a markdown file's bytes.
func readFileMeta(content []byte) fileMeta {
	return fileMeta{
		Envelope:    core.FrontmatterValueFromReader(strings.NewReader(string(content)), envelopeKey),
		Description: core.FrontmatterValueFromReader(strings.NewReader(string(content)), "description"),
		Title:       core.FrontmatterValueFromReader(strings.NewReader(string(content)), "title"),
	}
}

// envelopeMatches reports whether a file's envelope value satisfies a filter.
// An "any" filter (empty, 1, true, any, *) matches every conforming file; any
// other filter matches either the whole value ("kanban@1") or just the spec name
// before the version ("kanban"), so a client can ask for a family without
// pinning a version.
func envelopeMatches(envelope, filter string) bool {
	if envelope == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case "", "1", "true", "any", "*":
		return true
	}
	if envelope == filter {
		return true
	}
	name, _, _ := strings.Cut(envelope, "@")
	return name == filter
}
