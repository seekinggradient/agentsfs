package hub

import (
	"encoding/hex"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
)

const maxEditorBytes = 4 << 20

type editData struct {
	baseData
	Repo, Path, Name, Content, Head, Error, Message, CSRF, BlobHref string
	Markdown                                                        bool
}

// The browser pins both its read and write to one revision. Only an explicit
// conflict reconciliation may change that base; re-rendering an error never does.
func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request, user, repo, filePath, viewer string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bare := s.Storage.RepoDir(user, repo)
	blobURL := "/" + user + "/" + repo + "/blob/" + escapePathSegments(filePath)
	crumbs := []crumb{{user, "/" + user}, {repo, "/" + user + "/" + repo}, {pathBase(filePath), blobURL}}
	data := editData{
		baseData: baseData{User: user, Viewer: viewer, Crumbs: crumbs},
		Repo:     repo, Path: filePath, Name: pathBase(filePath), BlobHref: blobURL,
		CSRF:     oauthCSRFToken(s.sessionSecret(), "editor:"+viewer),
		Markdown: strings.EqualFold(path.Ext(filePath), ".md") || strings.EqualFold(path.Ext(filePath), ".markdown"),
	}
	jsonResponse := strings.Contains(r.Header.Get("Accept"), "application/json")
	fail := func(status int, message string) {
		if jsonResponse {
			apiError(w, status, message)
			return
		}
		data.Error = message
		s.renderPageStatus(w, r, "edit", data, status)
	}
	if r.Method == http.MethodGet {
		data.Head = mustGitHead(bare)
		content, ok := BlobContent("git", bare, data.Head, filePath)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
			http.Redirect(w, r, blobURL, http.StatusFound)
			return
		}
		data.Content = content
		if len(content) > maxEditorBytes {
			http.Error(w, "This file is too large for browser editing. Use a local checkout.", http.StatusRequestEntityTooLarge)
			return
		}
		if !s.hubWritesAllowed(user, repo) {
			http.Error(w, "This knowledge base needs a projection upgrade before it can be edited on the Hub.", http.StatusConflict)
			return
		}
		if jsonResponse {
			writeJSON(w, http.StatusOK, map[string]string{"head": data.Head, "content": content})
			return
		}
		s.renderPage(w, r, "edit", data)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 3*maxEditorBytes+16384) // URL-encoded UTF-8 can triple in size.
	if err := r.ParseForm(); err != nil {
		apiError(w, http.StatusRequestEntityTooLarge, "The edit could not be read. Download your draft and try a smaller file.")
		return
	}
	data.Content, data.Head, data.Message = r.PostForm.Get("content"), r.PostForm.Get("head"), strings.TrimSpace(r.PostForm.Get("message"))
	if !verifyOAuthCSRF(s.sessionSecret(), "editor:"+viewer, r.PostForm.Get("csrf")) {
		fail(http.StatusForbidden, "Your editing session expired. Download your draft, then sign in and reopen this note.")
		return
	}
	if !s.hubWritesAllowed(user, repo) {
		fail(http.StatusConflict, "This knowledge base needs a projection upgrade before it can be edited on the Hub.")
		return
	}
	if len(data.Content) > maxEditorBytes || !utf8.ValidString(data.Content) || strings.ContainsRune(data.Content, 0) {
		fail(http.StatusBadRequest, "The note must be valid text, no larger than 4 MB.")
		return
	}
	_, oidErr := hex.DecodeString(data.Head)
	if len(data.Head) != 40 || oidErr != nil {
		fail(http.StatusBadRequest, "The original version is missing. Download your draft and reopen this note.")
		return
	}
	if _, ok := BlobContent("git", bare, data.Head, filePath); !ok {
		fail(http.StatusBadRequest, "The original note could not be found.")
		return
	}
	if utf8.RuneCountInString(data.Message) > 500 {
		fail(http.StatusBadRequest, "Keep the version description under 500 characters.")
		return
	}
	if data.Message == "" {
		data.Message = "Update " + filePath
	}
	// A retry after a lost response (or an unchanged save) must not create another commit.
	head := mustGitHead(bare)
	current, exists := BlobContent("git", bare, head, filePath)
	if exists && current == data.Content {
		if jsonResponse {
			writeJSON(w, http.StatusOK, map[string]any{"head": head, "url": blobURL, "unchanged": true})
			return
		}
		http.Redirect(w, r, blobURL, http.StatusSeeOther)
		return
	}
	result, err := s.RepoCommit(viewer, apiCommitRequest{Repo: user + "/" + repo, BaseRev: data.Head, Message: data.Message, Changes: []apiChange{{Path: filePath, Content: data.Content}}})
	if err != nil {
		if conflict, ok := err.(*conflictError); ok {
			if jsonResponse {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "This note has a newer version. Review it alongside your draft before saving.", "conflict": true, "head": conflict.head})
				return
			}
			fail(http.StatusConflict, "This note has a newer version. Your draft is preserved below; download it before reopening the current note.")
			return
		}
		s.Log.Printf("editor commit %s/%s %s: %v", user, repo, filePath, err)
		fail(http.StatusInternalServerError, "The version could not be saved. Your draft is still here; please try again.")
		return
	}
	if jsonResponse {
		writeJSON(w, http.StatusOK, map[string]any{"head": result.NewRev, "url": blobURL, "merged": result.Merged})
		return
	}
	http.Redirect(w, r, blobURL, http.StatusSeeOther)
}

func escapePathSegments(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
