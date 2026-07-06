package hub

import (
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	neturl "net/url"
	"os"
	"regexp"
	"strings"
)

// nameRe restricts user and repo path segments to a safe character set. It
// rejects "." and ".." and anything that could escape the storage root.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Server serves the git Smart-HTTP protocol for agentsfs repos. One stable
// URL per repo — https://host/<user>/<repo>.git — is a git remote you can
// clone, pull, and push. Reads and writes both require a token that owns the
// <user> namespace (private by default in Phase 0).
type Server struct {
	Storage    Storage
	Tokens     *TokenStore   // env-configured bootstrap tokens (backward compat)
	Accounts   *AccountStore // nil until accounts are enabled
	Agent      *AgentManager // nil/disabled until Sprites + OpenAI are configured
	GitBackend string        // path to git-http-backend
	Log        *log.Logger
}

// userForToken resolves a git/API token (Basic password or bearer) to a user:
// account-issued PATs first, then the env-configured bootstrap tokens.
func (s *Server) userForToken(token string) (string, bool) {
	if s.Accounts != nil {
		if u, ok := s.Accounts.UserForToken(token); ok {
			return u, true
		}
	}
	return s.Tokens.UserFor(token)
}

// sessionSecret is the key used to sign browser session cookies.
func (s *Server) sessionSecret() []byte {
	if s.Accounts != nil {
		return s.Accounts.SessionSecret()
	}
	return s.Tokens.secret()
}

// signupOpen controls whether self-serve signup is allowed; set from main.
var signupOpen = true

// SetSignupOpen toggles self-serve account signup.
func SetSignupOpen(open bool) { signupOpen = open }

// New builds a Server, auto-discovering git-http-backend when backendPath is
// empty.
func New(store Storage, tokens *TokenStore, backendPath string) (*Server, error) {
	if backendPath == "" {
		p, err := discoverGitHTTPBackend()
		if err != nil {
			return nil, err
		}
		backendPath = p
	}
	return &Server{Storage: store, Tokens: tokens, GitBackend: backendPath, Log: log.Default()}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok\n")
		return
	}

	// Static assets (CSS/JS/favicon) are public so the login page can style
	// itself before the user is authenticated.
	if strings.HasPrefix(r.URL.Path, "/_assets/") {
		serveAsset(w, r)
		return
	}

	// LLM proxy for agent sprites: the sprite holds NO OpenAI key — it calls the
	// hub here (authenticated by its per-user PAT) and the hub forwards to OpenAI
	// on the hub's key. Keeps the shared model key off every sprite.
	if strings.HasPrefix(r.URL.Path, "/v1/agent-llm/") {
		s.handleAgentLLM(w, r)
		return
	}

	// A git Smart-HTTP request is /<user>/<repo>[.git]/<git-service>. Anything
	// else — /, /<user>, /<user>/<repo> — is a browser hitting the read-only
	// web space at the same stable URL.
	if user, repo, rest, ok := parseRepoPath(r.URL.Path); ok && isGitService(rest) {
		s.serveGit(w, r, user, repo, rest)
		return
	}
	s.serveWeb(w, r)
}

// llmProxyTarget is where the hub forwards agent model calls.
var llmProxyTarget, _ = neturl.Parse("https://api.openai.com")

// llmAllowed restricts which OpenAI endpoints a sprite may reach through the
// hub's key, so a compromised/prompt-injected agent can't turn the shared key
// into a general-purpose OpenAI account. These are the only paths the agent and
// its voice mode use.
func llmAllowed(rest string) bool {
	return strings.HasPrefix(rest, "responses") ||
		strings.HasPrefix(rest, "chat/completions") ||
		strings.HasPrefix(rest, "realtime/")
}

// handleAgentLLM authenticates a sprite by its per-user PAT and reverse-proxies
// the request to OpenAI with the hub's key swapped in. The sprite never sees the
// OpenAI key; the PAT it does hold only authenticates as its own owner.
func (s *Server) handleAgentLLM(w http.ResponseWriter, r *http.Request) {
	if s.Agent == nil || s.Agent.OpenAIKey == "" {
		http.Error(w, "llm proxy not configured", http.StatusServiceUnavailable)
		return
	}
	if _, ok := s.userForToken(tokenFromRequest(r)); !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/agent-llm/")
	if !llmAllowed(rest) {
		http.Error(w, "endpoint not allowed", http.StatusForbidden)
		return
	}
	key := s.Agent.OpenAIKey
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = llmProxyTarget.Scheme
			req.URL.Host = llmProxyTarget.Host
			req.Host = llmProxyTarget.Host
			req.URL.Path = "/v1/" + rest
			req.Header.Set("Authorization", "Bearer "+key)
			// Don't leak the hub's client IP chain upstream.
			req.Header.Del("X-Forwarded-For")
		},
		FlushInterval: -1, // stream SSE (Responses API) chunk-by-chunk
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.Log.Printf("agent-llm proxy: %v", err)
			http.Error(w, "llm upstream error", http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// isGitService reports whether the path tail is a git Smart-HTTP endpoint.
func isGitService(rest string) bool {
	switch {
	case rest == "info/refs", rest == "git-upload-pack", rest == "git-receive-pack":
		return true
	}
	return false
}

// serveGit authenticates and proxies a git Smart-HTTP request to the real
// git-http-backend over the bare repo, auto-creating the repo on first
// contact for the namespace owner.
func (s *Server) serveGit(w http.ResponseWriter, r *http.Request, user, repo, rest string) {
	// Classify read vs write: for info/refs the service is in the query,
	// otherwise the path tail is the service name.
	service := rest
	if rest == "info/refs" {
		service = r.URL.Query().Get("service")
	}
	isWrite := service == "git-receive-pack"

	// If this repo was renamed away, 301 the old slug to its current one so
	// existing clones and `afs hub push` remotes (which point at the old URL)
	// keep working — including after a rename done in the web UI. Do this before
	// auth/auto-create, or an owner push would silently recreate the old name.
	if !s.Storage.Exists(user, repo) {
		if dest, ok := s.Storage.LookupRedirect(user, repo); ok {
			target := "/" + user + "/" + dest + ".git/" + rest
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
	}

	authUser, valid := s.userForToken(tokenFromRequest(r))
	owner := valid && authUser == user
	remoteUser := ""

	if owner {
		remoteUser = authUser
		// Owners auto-create a repo on first contact (e.g. the first push).
		if err := s.Storage.EnsureRepo(user, repo); err != nil {
			s.Log.Printf("ensure repo %s/%s: %v", user, repo, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	} else if isWrite || !s.Storage.Exists(user, repo) || !s.isPublic(user, repo) {
		// Non-owners may only anonymously READ an existing PUBLIC repo.
		w.Header().Set("WWW-Authenticate", `Basic realm="afs-hub"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Repair a dangling HEAD before advertising refs, so a `git clone` of a
	// repo that only ever received a differently-named branch (e.g. master)
	// still follows a real branch. No-op when HEAD is already valid.
	if rest == "info/refs" && s.Storage.Exists(user, repo) {
		if err := s.Storage.EnsureHEAD(user, repo); err != nil {
			s.Log.Printf("ensure HEAD %s/%s: %v", user, repo, err)
		}
	}

	// Normalize the path to the canonical <user>/<repo>.git so both
	// `.../repo.git/...` and `.../repo/...` clone URLs resolve to the same
	// on-disk bare repo that EnsureRepo just guaranteed.
	req := r.Clone(r.Context())
	req.URL.Path = "/" + user + "/" + repo + ".git/" + rest

	// net/http/cgi rejects chunked request bodies, but git streams large
	// pushes chunked. Buffer an unknown-length body to a temp file and hand
	// the backend a Content-Length body instead.
	if req.ContentLength < 0 || len(req.TransferEncoding) > 0 {
		cleanup, err := bufferBody(req)
		if err != nil {
			s.Log.Printf("buffering request body: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer cleanup()
	}

	gitBackendHandler(s.GitBackend, s.Storage.Root(), remoteUser).ServeHTTP(w, req)

	// After a push, make sure HEAD points at a real branch. A client that
	// pushes e.g. `master` into a repo initialized on `main` would otherwise
	// leave HEAD dangling, so the web view and plain clones would see an
	// "empty" repo even though the commits are there.
	if rest == "git-receive-pack" {
		if err := s.Storage.EnsureHEAD(user, repo); err != nil {
			s.Log.Printf("ensure HEAD %s/%s: %v", user, repo, err)
		}
	}
}

// parseRepoPath splits /<user>/<repo>[.git]/<git-service-path> into its parts,
// validating the names. rest is the remaining git service path
// (e.g. "info/refs" or "git-receive-pack").
func parseRepoPath(p string) (user, repo, rest string, ok bool) {
	parts := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 3)
	if len(parts) < 3 {
		return "", "", "", false
	}
	user = parts[0]
	repo = strings.TrimSuffix(parts[1], ".git")
	rest = parts[2]
	if !nameRe.MatchString(user) || !nameRe.MatchString(repo) {
		return "", "", "", false
	}
	return user, repo, rest, true
}

// bufferBody drains req.Body to a temp file and rewrites req so the body has a
// known Content-Length and no chunked transfer encoding. The returned cleanup
// removes the temp file and must be called after the response is written.
func bufferBody(req *http.Request) (func(), error) {
	tmp, err := os.CreateTemp("", "afs-hub-body-*")
	if err != nil {
		return nil, err
	}
	cleanup := func() { tmp.Close(); os.Remove(tmp.Name()) }
	// Cap the buffered body so a chunked push can't exhaust the volume. 2 GiB
	// is far above any real knowledge repo; a larger push is truncated and the
	// resulting pack is rejected by git rather than filling the disk.
	const maxBufferedBody = 2 << 30
	n, err := io.Copy(tmp, io.LimitReader(req.Body, maxBufferedBody))
	if err != nil {
		cleanup()
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, err
	}
	req.Body = tmp
	req.ContentLength = n
	req.TransferEncoding = nil
	return cleanup, nil
}
