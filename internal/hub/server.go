package hub

import (
	"io"
	"log"
	"net/http"
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
	Tokens     *TokenStore
	GitBackend string // path to git-http-backend
	Log        *log.Logger
}

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

	// A git Smart-HTTP request is /<user>/<repo>[.git]/<git-service>. Anything
	// else — /, /<user>, /<user>/<repo> — is a browser hitting the read-only
	// web space at the same stable URL.
	if user, repo, rest, ok := parseRepoPath(r.URL.Path); ok && isGitService(rest) {
		s.serveGit(w, r, user, repo, rest)
		return
	}
	s.serveWeb(w, r)
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

	authUser, valid := s.Tokens.UserFor(tokenFromRequest(r))
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
