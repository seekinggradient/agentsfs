package hub

import (
	"net/http"
	"path"
	"strings"
	"unicode/utf8"
)

type newFileData struct {
	baseData
	Repo, Name, CSRF, Error string
}

func (s *Server) handleNewFile(w http.ResponseWriter, r *http.Request, owner, repo, viewer string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := newFileData{
		baseData: baseData{User: owner, Viewer: viewer, Crumbs: []crumb{{owner, "/" + owner}, {repo, "/" + owner + "/" + repo}, {"New file", ""}}},
		Repo:     repo, CSRF: oauthCSRFToken(s.sessionSecret(), "editor:"+viewer),
	}
	fail := func(status int, message string) {
		data.Error = message
		s.renderPageStatus(w, r, "newfile", data, status)
	}
	if !s.hubWritesAllowed(owner, repo) {
		http.Error(w, "This workspace needs a projection upgrade before files can be created on the Hub.", http.StatusConflict)
		return
	}
	if r.Method == http.MethodGet {
		s.renderPage(w, r, "newfile", data)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		fail(http.StatusBadRequest, "The file name could not be read. Please try a shorter name.")
		return
	}
	data.Name = strings.TrimSpace(r.PostForm.Get("name"))
	if !verifyOAuthCSRF(s.sessionSecret(), "editor:"+viewer, r.PostForm.Get("csrf")) {
		fail(http.StatusForbidden, "Your session expired. Reload this page and try again.")
		return
	}
	_, safe := safeRepoPath(data.Name)
	if !safe || !validRepoPath(data.Name) || !utf8.ValidString(data.Name) || len(data.Name) > 1024 {
		fail(http.StatusBadRequest, "Enter a file name, such as Meeting notes.md. Use / between folders; avoid reserved names and relative paths.")
		return
	}
	filePath := data.Name
	if path.Ext(filePath) == "" {
		filePath += ".md"
	}
	// Creation is a real, immediately visible version. Subsequent writing uses
	// the editor's private autosave session and grouped shared versions.
	_, err := s.RepoCommit(viewer, apiCommitRequest{
		Repo: owner + "/" + repo, BaseRev: headOID("git", s.Storage.RepoDir(owner, repo), defaultRef),
		Message: "Create " + filePath, Changes: []apiChange{{Path: filePath, Content: ""}}, createOnly: true,
	})
	if err != nil {
		switch e := err.(type) {
		case *accessError:
			fail(e.status, e.msg)
		case *conflictError:
			fail(http.StatusConflict, "The workspace changed while creating your file. Please try again, or choose a different name if it already exists.")
		default:
			fail(http.StatusInternalServerError, "Your file could not be created. Please try again.")
		}
		return
	}
	http.Redirect(w, r, "/"+owner+"/"+repo+"/edit/"+escapePathSegments(filePath), http.StatusSeeOther)
}
