package hub

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"agentsfs.ai/afs/internal/core"
)

// wlDisplayRe strips [[wikilink]] syntax from descriptions shown as plain text
// (the tree, cards, note header): [[a/b|Label]] -> Label, [[Name]] -> Name.
var wlDisplayRe = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)

func cleanDesc(s string) string {
	return wlDisplayRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		if i := strings.Index(inner, "|"); i >= 0 {
			inner = inner[i+1:]
		}
		return strings.TrimSpace(inner)
	})
}

// The central space: the same stable URL that serves git over HTTPS also
// renders, in a browser, a user's repos and their knowledge — the tree with
// descriptions and freshness, rendered notes with resolved [[wikilinks]] and
// backlinks, and git history. Everything reads straight from the bare repos
// and reuses core's frontmatter/link logic, so it can't drift from the CLI.

//go:embed assets
var assetsFS embed.FS

var pages = parsePages()

func parsePages() map[string]*template.Template {
	base := template.Must(template.ParseFS(assetsFS, "assets/base.html"))
	out := map[string]*template.Template{}
	for _, name := range []string{"dashboard", "repo", "file", "history", "login"} {
		out[name] = template.Must(template.Must(base.Clone()).ParseFS(assetsFS, "assets/"+name+".html"))
	}
	return out
}

type crumb struct{ Name, Href string }

type baseData struct {
	User   string
	Crumbs []crumb
}

// serveAsset serves the embedded CSS/JS/favicon publicly (no auth) so the
// login page can style itself.
func serveAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/_assets/")
	if strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	data, err := assetsFS.ReadFile("assets/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch path.Ext(name) {
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}

// serveWeb handles all browser (non-git) requests: login, logout, dashboard,
// repo, note, raw, and history. Private by default — a session cookie or a
// Basic token owning the namespace is required.
func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/login":
		s.handleLogin(w, r)
		return
	case "/logout":
		s.handleLogout(w, r)
		return
	}

	user, ok := s.webUser(r)
	if !ok {
		if r.Method == http.MethodGet {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusFound)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="afs-hub"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var segs []string
	for _, p := range strings.Split(strings.Trim(r.URL.Path, "/"), "/") {
		if p != "" {
			segs = append(segs, p)
		}
	}

	if len(segs) == 0 {
		s.renderDashboard(w, user)
		return
	}
	if segs[0] != user {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if len(segs) == 1 {
		s.renderDashboard(w, user)
		return
	}
	repo := strings.TrimSuffix(segs[1], ".git")
	if !nameRe.MatchString(user) || !nameRe.MatchString(repo) || !s.Storage.Exists(user, repo) {
		http.NotFound(w, r)
		return
	}
	rest := segs[2:]
	switch {
	case len(rest) == 0:
		s.renderRepo(w, r, user, repo)
	case rest[0] == "blob" && len(rest) > 1:
		s.renderFile(w, r, user, repo, strings.Join(rest[1:], "/"))
	case rest[0] == "raw" && len(rest) > 1:
		s.handleRaw(w, user, repo, strings.Join(rest[1:], "/"))
	case rest[0] == "history" && len(rest) == 1:
		s.renderHistory(w, user, repo)
	default:
		http.NotFound(w, r)
	}
}

// ---- auth / sessions ----

func (s *Server) webUser(r *http.Request) (string, bool) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if u, ok := parseSession(s.Tokens.secret(), c.Value); ok {
			return u, true
		}
	}
	if u, ok := s.Tokens.UserFor(tokenFromRequest(r)); ok {
		return u, true
	}
	return "", false
}

type loginData struct {
	baseData
	LoginUser, Next, Error string
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	if r.Method == http.MethodPost {
		token := strings.TrimSpace(r.FormValue("token"))
		formUser := strings.TrimSpace(r.FormValue("user"))
		authUser, ok := s.Tokens.UserFor(token)
		if ok && (formUser == "" || formUser == authUser) {
			exp := time.Now().Add(30 * 24 * time.Hour).Unix()
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookie,
				Value:    makeSession(s.Tokens.secret(), authUser, exp),
				Path:     "/",
				HttpOnly: true,
				Secure:   isHTTPS(r),
				SameSite: http.SameSiteLaxMode,
				Expires:  time.Unix(exp, 0),
			})
			http.Redirect(w, r, next, http.StatusFound)
			return
		}
		pages["login"].ExecuteTemplate(w, "base", loginData{LoginUser: formUser, Next: next, Error: "That token wasn't recognized."})
		return
	}
	pages["login"].ExecuteTemplate(w, "base", loginData{Next: next})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusFound)
}

// safeNext keeps redirect targets local (no open redirect).
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func hubBase(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// ---- pages ----

type repoCard struct {
	Name, Description, Age, Delay string
	Notes                         int
}
type dashboardData struct {
	baseData
	Repos []repoCard
}

func (s *Server) renderDashboard(w http.ResponseWriter, user string) {
	repos, err := s.Storage.ListRepos(user)
	if err != nil {
		s.Log.Printf("list repos %s: %v", user, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := dashboardData{baseData: baseData{User: user}}
	for i, name := range repos {
		desc, notes, ageUnix := s.repoMeta(user, name)
		data.Repos = append(data.Repos, repoCard{
			Name: name, Description: desc, Notes: notes,
			Age: ageString(ageUnix), Delay: fmt.Sprintf("%.2fs", float64(i)*0.05),
		})
	}
	s.renderPage(w, "dashboard", data)
}

type repoData struct {
	baseData
	Repo, Description, CloneCmd string
	Empty                       bool
	Root                        *treeNode
}

func (s *Server) renderRepo(w http.ResponseWriter, r *http.Request, user, repo string) {
	bare := s.Storage.RepoDir(user, repo)
	files, err := RepoSnapshot("git", bare, defaultRef)
	if err != nil {
		s.Log.Printf("snapshot %s/%s: %v", user, repo, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	desc, _, _ := s.repoMeta(user, repo)
	data := repoData{
		baseData:    baseData{User: user, Crumbs: []crumb{{user, "/" + user}, {repo, "/" + user + "/" + repo}}},
		Repo:        repo,
		Description: desc,
		CloneCmd:    fmt.Sprintf("git clone %s/%s/%s.git", hubBase(r), user, repo),
		Empty:       len(files) == 0,
	}
	if !data.Empty {
		data.Root = buildTree(files, user, repo)
	}
	s.renderPage(w, "repo", data)
}

type backlinkView struct{ Name, Desc, Href string }
type commitView struct{ Short, Subject, Author, When string }

type fileData struct {
	baseData
	Repo, Path, Name, Description, Age string
	IsMarkdown, IsText                 bool
	BodyHTML                           template.HTML
	RawText, RawHref                   string
	Backlinks                          []backlinkView
	History                            []commitView
}

func (s *Server) renderFile(w http.ResponseWriter, r *http.Request, user, repo, filePath string) {
	bare := s.Storage.RepoDir(user, repo)
	content, ok := BlobContent("git", bare, defaultRef, filePath)
	if !ok {
		http.NotFound(w, r)
		return
	}
	files, _ := RepoSnapshot("git", bare, defaultRef)
	paths := make([]string, 0, len(files))
	descByPath := map[string]string{}
	var ageUnix int64
	for _, f := range files {
		paths = append(paths, f.Path)
		descByPath[f.Path] = f.Description
		if f.Path == filePath {
			ageUnix = f.LastCommit
		}
	}
	idx := core.NewNameIndex(paths)

	data := fileData{
		baseData:   baseData{User: user, Crumbs: []crumb{{user, "/" + user}, {repo, "/" + user + "/" + repo}, {pathBase(filePath), ""}}},
		Repo:       repo,
		Path:       filePath,
		Name:       pathBase(filePath),
		Age:        ageString(ageUnix),
		RawHref:    "/" + user + "/" + repo + "/raw/" + filePath,
		IsMarkdown: strings.EqualFold(path.Ext(filePath), ".md"),
	}

	if data.IsMarkdown {
		data.Description = cleanDesc(core.FrontmatterValueFromReader(strings.NewReader(content), "description"))
		resolve := func(target string) (string, bool) {
			m := idx.Resolve(target)
			if len(m) == 0 {
				return "", false
			}
			return "/" + user + "/" + repo + "/blob/" + m[0], true
		}
		if html, err := renderMarkdown(content, resolve); err == nil {
			data.BodyHTML = template.HTML(html)
		}
		// Backlinks (deduped by source file).
		seen := map[string]bool{}
		for _, l := range RepoBacklinks("git", bare, defaultRef, filePath, idx) {
			if seen[l.Source] {
				continue
			}
			seen[l.Source] = true
			data.Backlinks = append(data.Backlinks, backlinkView{
				Name: l.Source, Desc: cleanDesc(descByPath[l.Source]),
				Href: "/" + user + "/" + repo + "/blob/" + l.Source,
			})
		}
	} else if utf8.ValidString(content) && !strings.ContainsRune(content, 0) {
		data.IsText = true
		data.RawText = content
	}

	for _, c := range RepoLogPath("git", bare, defaultRef, filePath, 8) {
		data.History = append(data.History, commitView{Short: c.Short, Subject: c.Subject, When: ageString(c.When)})
	}
	s.renderPage(w, "file", data)
}

func (s *Server) handleRaw(w http.ResponseWriter, user, repo, filePath string) {
	content, ok := BlobContent("git", s.Storage.RepoDir(user, repo), defaultRef, filePath)
	if !ok {
		http.NotFound(w, nil)
		return
	}
	if utf8.ValidString(content) && !strings.ContainsRune(content, 0) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+pathBase(filePath)+"\"")
	}
	w.Write([]byte(content))
}

type historyData struct {
	baseData
	Repo    string
	Commits []commitView
}

func (s *Server) renderHistory(w http.ResponseWriter, user, repo string) {
	data := historyData{
		baseData: baseData{User: user, Crumbs: []crumb{{user, "/" + user}, {repo, "/" + user + "/" + repo}, {"history", ""}}},
		Repo:     repo,
	}
	for _, c := range RepoLog("git", s.Storage.RepoDir(user, repo), defaultRef, 100) {
		data.Commits = append(data.Commits, commitView{Short: c.Short, Subject: c.Subject, Author: c.Author, When: ageString(c.When)})
	}
	s.renderPage(w, "history", data)
}

func (s *Server) renderPage(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages[name].ExecuteTemplate(w, "base", data); err != nil {
		s.Log.Printf("render %s: %v", name, err)
	}
}

// repoMeta returns a repo's root description, note count, and freshest commit
// time for dashboard/header display.
func (s *Server) repoMeta(user, repo string) (desc string, notes int, ageUnix int64) {
	bare := s.Storage.RepoDir(user, repo)
	for _, name := range []string{"AGENTS.md", "README.md"} {
		if c, ok := BlobContent("git", bare, defaultRef, name); ok {
			if d := core.FrontmatterValueFromReader(strings.NewReader(c), "description"); d != "" {
				desc = cleanDesc(d)
				break
			}
		}
	}
	files, _ := RepoSnapshot("git", bare, defaultRef)
	for _, f := range files {
		if strings.EqualFold(path.Ext(f.Path), ".md") {
			notes++
		}
		if f.LastCommit > ageUnix {
			ageUnix = f.LastCommit
		}
	}
	return desc, notes, ageUnix
}

// ---- tree building ----

type treeNode struct {
	Name       string
	Path       string
	IsDir      bool
	Desc       string
	Age        string
	Href       string
	LastCommit int64
	Children   []*treeNode
}

// buildTree turns the flat file list into a nested tree. INDEX.md files aren't
// listed; their description labels the directory (mirroring `afs tree`).
func buildTree(files []RepoFile, user, repo string) *treeNode {
	root := &treeNode{IsDir: true}
	dirs := map[string]*treeNode{"": root}
	dirDesc := map[string]string{}
	for _, f := range files {
		if pathBase(f.Path) == "INDEX.md" {
			dirDesc[pathDir(f.Path)] = cleanDesc(f.Description)
		}
	}
	var ensureDir func(p string) *treeNode
	ensureDir = func(p string) *treeNode {
		if n, ok := dirs[p]; ok {
			return n
		}
		parent := ensureDir(pathDir(p))
		n := &treeNode{Name: pathBase(p), Path: p, IsDir: true, Desc: dirDesc[p]}
		dirs[p] = n
		parent.Children = append(parent.Children, n)
		return n
	}
	for _, f := range files {
		if pathBase(f.Path) == "INDEX.md" {
			continue
		}
		parent := ensureDir(pathDir(f.Path))
		parent.Children = append(parent.Children, &treeNode{
			Name: pathBase(f.Path), Path: f.Path, Desc: cleanDesc(f.Description),
			Age: ageString(f.LastCommit), LastCommit: f.LastCommit,
			Href: "/" + user + "/" + repo + "/blob/" + f.Path,
		})
	}
	decorate(root)
	return root
}

// decorate sets each directory's freshness (its freshest descendant) and sorts
// children: files before subdirectories, alphabetically.
func decorate(n *treeNode) {
	var max int64
	for _, c := range n.Children {
		if c.IsDir {
			decorate(c)
		}
		if c.LastCommit > max {
			max = c.LastCommit
		}
	}
	if n.IsDir && max > 0 {
		n.LastCommit = max
		n.Age = ageString(max)
	}
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return !a.IsDir
		}
		return a.Name < b.Name
	})
}

func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// ageString renders a unix time as a short relative age for display.
func ageString(unix int64) string {
	if unix == 0 {
		return ""
	}
	d := time.Since(time.Unix(unix, 0))
	switch {
	case d < time.Hour:
		return "just now"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	}
}
