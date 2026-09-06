package hub

import (
	"net/http"
	"path"
	"strings"
	"unicode/utf8"
)

type newFileData struct {
	baseData
	Repo, Name, Folder, CSRF, Error string
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
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			apiError(w, status, message)
			return
		}
		data.Error = message
		s.renderPageStatus(w, r, "newfile", data, status)
	}
	if !s.hubWritesAllowed(owner, repo) {
		http.Error(w, "This workspace needs a projection upgrade before files can be created on the Hub.", http.StatusConflict)
		return
	}
	if r.Method == http.MethodGet {
		data.Folder = r.URL.Query().Get("folder")
		if !validNewFileFolder(data.Folder) {
			fail(http.StatusBadRequest, "Choose a valid folder within this workspace.")
			return
		}
		s.renderPage(w, r, "newfile", data)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		fail(http.StatusBadRequest, "The file name could not be read. Please try a shorter name.")
		return
	}
	data.Name = strings.TrimSpace(r.PostForm.Get("name"))
	data.Folder = r.PostForm.Get("folder")
	if !verifyOAuthCSRF(s.sessionSecret(), "editor:"+viewer, r.PostForm.Get("csrf")) {
		fail(http.StatusForbidden, "Your session expired. Reload this page and try again.")
		return
	}
	if !validNewFileFolder(data.Folder) {
		fail(http.StatusBadRequest, "Choose a valid folder within this workspace.")
		return
	}
	_, safe := safeRepoPath(data.Name)
	if !safe || !validRepoPath(data.Name) || !utf8.ValidString(data.Name) || len(data.Name) > 1024 {
		fail(http.StatusBadRequest, "Enter a file name, such as Meeting notes.md. Use / between folders; avoid reserved names and relative paths.")
		return
	}
	filePath := data.Name
	if data.Folder != "" {
		filePath = data.Folder + "/" + filePath
	}
	if len(filePath) > 1024 {
		fail(http.StatusBadRequest, "This path is too long. Please use a shorter file name.")
		return
	}
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
	location := "/" + owner + "/" + repo + "/edit/" + escapePathSegments(filePath)
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusCreated, map[string]string{"location": location})
		return
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func validNewFileFolder(folder string) bool {
	if folder == "" {
		return true
	}
	clean, safe := safeRepoPath(folder)
	return safe && clean == folder && validRepoPath(folder) && utf8.ValidString(folder) && len(folder) <= 1024
}
