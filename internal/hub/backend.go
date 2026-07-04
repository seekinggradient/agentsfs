package hub

import (
	"fmt"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// discoverGitHTTPBackend locates the git-http-backend CGI shipped with git,
// via `git --exec-path`. Running the real backend is "Option A": we serve the
// full git Smart-HTTP protocol without reimplementing any of it.
func discoverGitHTTPBackend() (string, error) {
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		return "", fmt.Errorf("locating git exec-path: %w", err)
	}
	p := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("git-http-backend not found at %s: %w", p, err)
	}
	return p, nil
}

// gitBackendHandler runs git-http-backend as CGI over projectRoot. PATH_INFO
// is the (already normalized) request path, so with GIT_PROJECT_ROOT set the
// backend resolves <projectRoot>/<user>/<repo>.git directly. GIT_HTTP_EXPORT_ALL
// lets it serve repos without a per-repo git-daemon-export-ok marker; access
// control is enforced by the server before we ever get here.
func gitBackendHandler(backendPath, projectRoot, remoteUser string) http.Handler {
	return &cgi.Handler{
		Path: backendPath,
		Root: "/", // strip nothing; PATH_INFO = full request path
		Dir:  projectRoot,
		Env: []string{
			"GIT_PROJECT_ROOT=" + projectRoot,
			"GIT_HTTP_EXPORT_ALL=1",
			"REMOTE_USER=" + remoteUser,
		},
	}
}
