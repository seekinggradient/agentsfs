package hub

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// assetVersion is a content hash appended to asset URLs so a deploy that
// changes the CSS/JS busts browser caches immediately (no stale styling).
var assetVersion = computeAssetVersion()

func computeAssetVersion() string {
	h := sha256.New()
	for _, n := range []string{"assets/style.css", "assets/app.js", "assets/hero-agentsfs-home.webp"} {
		if b, err := assetsFS.ReadFile(n); err == nil {
			h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

func assetURL(name string) string { return "/_assets/" + name + "?v=" + assetVersion }

func parsePages() map[string]*template.Template {
	fm := template.FuncMap{"asset": assetURL}
	base := template.Must(template.New("base.html").Funcs(fm).ParseFS(assetsFS, "assets/base.html"))
	out := map[string]*template.Template{}
	for _, name := range []string{"home", "dashboard", "repo", "file", "history", "login", "edit", "settings", "signup", "account"} {
		out[name] = template.Must(template.Must(base.Clone()).ParseFS(assetsFS, "assets/"+name+".html"))
	}
	return out
}

type crumb struct{ Name, Href string }

// baseData is embedded in every page. User is the namespace the page belongs to
// (used to build URLs); Viewer is who is signed in ("" when anonymous), used for
// the header's account chip and logout.
type baseData struct {
	User     string
	Viewer   string
	Crumbs   []crumb
	Home     bool
	AgentURL string // when set, base.html renders the agent trigger + side dock
}

// agentPath returns the in-hub agent URL for a repo when the viewer owns it and
// the agent feature is on, else "" (so the dock/trigger stay hidden).
func (s *Server) agentPath(user, repo, viewer string) string {
	if viewer == user && s.Agent.Enabled() {
		return "/" + user + "/" + repo + "/agent/"
	}
	return ""
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
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".webp":
		w.Header().Set("Content-Type", "image/webp")
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
	case "/signup":
		s.handleSignup(w, r)
		return
	case "/account":
		v, ok := s.webUser(r)
		if !ok {
			s.needLogin(w, r)
			return
		}
		s.handleAccount(w, r, v)
		return
	}

	viewer, isAuthed := s.webUser(r) // ("", false) when anonymous

	var segs []string
	for _, p := range strings.Split(strings.Trim(r.URL.Path, "/"), "/") {
		if p != "" {
			segs = append(segs, p)
		}
	}

	// The dashboard and a user's index are always private to that user.
	if len(segs) == 0 || len(segs) == 1 {
		if !isAuthed {
			if len(segs) == 0 && r.Method == http.MethodGet && !wantsJSON(r) {
				s.renderHome(w)
				return
			}
			s.needLogin(w, r)
			return
		}
		if len(segs) == 1 && segs[0] != viewer {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if wantsJSON(r) {
			s.dashboardJSON(w, r, viewer)
		} else {
			s.renderDashboard(w, viewer)
		}
		return
	}

	user := segs[0]
	repo := strings.TrimSuffix(segs[1], ".git")
	if !nameRe.MatchString(user) || !nameRe.MatchString(repo) || !s.Storage.Exists(user, repo) {
		http.NotFound(w, r)
		return
	}
	owner := isAuthed && viewer == user
	rest := segs[2:]
	ownerOnly := len(rest) > 0 && (rest[0] == "edit" || rest[0] == "settings" || rest[0] == "agent")

	// Authorize: owner-only routes need the owner; read routes allow the owner
	// or anyone if the repo is public.
	if ownerOnly {
		if !owner {
			if isAuthed {
				http.Error(w, "forbidden", http.StatusForbidden)
			} else {
				s.needLogin(w, r)
			}
			return
		}
	} else if !owner && !s.isPublic(user, repo) {
		if isAuthed {
			http.Error(w, "forbidden", http.StatusForbidden)
		} else {
			s.needLogin(w, r)
		}
		return
	}

	switch {
	case len(rest) == 0:
		s.renderRepo(w, r, user, repo, viewer)
	case rest[0] == "history" && len(rest) == 1:
		s.renderHistory(w, user, repo, viewer)
	case rest[0] == "settings" && len(rest) == 1:
		s.handleSettings(w, r, user, repo, viewer)
	case rest[0] == "agent":
		s.handleAgent(w, r, user, repo)
	case (rest[0] == "blob" || rest[0] == "raw" || rest[0] == "edit") && len(rest) > 1:
		fp := strings.Join(rest[1:], "/")
		if !validRepoPath(fp) {
			http.NotFound(w, r)
			return
		}
		switch rest[0] {
		case "blob":
			s.renderFile(w, r, user, repo, fp, viewer)
		case "raw":
			s.handleRaw(w, user, repo, fp)
		case "edit":
			s.handleEdit(w, r, user, repo, fp, viewer)
		}
	default:
		http.NotFound(w, r)
	}
}

// needLogin redirects a browser GET to the login page, or returns 401 for
// anything else.
func (s *Server) needLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusFound)
		return
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="afs-hub"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// validRepoPath guards the file path in blob/raw/edit URLs: defense-in-depth
// against traversal or git-arg injection even though git itself won't resolve
// a ref:path outside the tree. Rejects empty, absolute, "." / ".." segments,
// backslashes, control characters, and a leading "-".
func validRepoPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") || strings.Contains(p, "\\") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	for _, r := range p {
		if r < 0x20 {
			return false
		}
	}
	return true
}

type editData struct {
	baseData
	Repo, Path, Name, Content, Head, Error string
}

// handleEdit renders the editor (GET) and lands a real commit (POST). Writes
// require the same namespace-owning auth as everything else; SameSite=Lax
// session cookies keep cross-site form POSTs from carrying credentials.
func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request, user, repo, filePath, viewer string) {
	bare := s.Storage.RepoDir(user, repo)
	crumbs := []crumb{{user, "/" + user}, {repo, "/" + user + "/" + repo}, {pathBase(filePath), "/" + user + "/" + repo + "/blob/" + filePath}}

	if r.Method == http.MethodPost {
		content := strings.ReplaceAll(r.FormValue("content"), "\r\n", "\n")
		_, err := CommitFile("git", bare, filePath, content, user, r.FormValue("message"), r.FormValue("head"))
		if err == nil {
			http.Redirect(w, r, "/"+user+"/"+repo+"/blob/"+filePath, http.StatusFound)
			return
		}
		msg := "Could not save the note."
		if errors.Is(err, ErrStale) {
			msg = "This note changed since you opened it — copy your text, reload, and reapply."
		} else {
			s.Log.Printf("commit %s/%s %s: %v", user, repo, filePath, err)
		}
		s.renderPage(w, "edit", editData{
			baseData: baseData{User: user, Viewer: viewer, Crumbs: crumbs},
			Repo:     repo, Path: filePath, Name: pathBase(filePath),
			Content: content, Head: strings.TrimSpace(mustGitHead(bare)), Error: msg,
		})
		return
	}

	content, ok := BlobContent("git", bare, defaultRef, filePath)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		http.Redirect(w, r, "/"+user+"/"+repo+"/blob/"+filePath, http.StatusFound)
		return
	}
	s.renderPage(w, "edit", editData{
		baseData: baseData{User: user, Crumbs: crumbs},
		Repo:     repo, Path: filePath, Name: pathBase(filePath),
		Content: content, Head: strings.TrimSpace(mustGitHead(bare)),
	})
}

func mustGitHead(bareDir string) string {
	out, _ := gitCmd("git", bareDir, nil, nil, "rev-parse", "HEAD")
	return strings.TrimSpace(out)
}

type settingsData struct {
	baseData
	Repo, DisplayName, Slug, CloneURL string
	Public                            bool
	Notice, Error                     string
}

// handleSettings is the owner-only repo settings page: visibility (with a typed
// confirmation to go public), display name, and slug rename (with a duplicate
// check). serveWeb already guarantees the caller owns the repo.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request, user, repo, viewer string) {
	render := func(slug, notice, errMsg string) {
		s.renderPage(w, "settings", settingsData{
			baseData:    baseData{User: user, Viewer: viewer, Crumbs: []crumb{{user, "/" + user}, {slug, "/" + user + "/" + slug}, {"settings", ""}}},
			Repo:        slug,
			DisplayName: s.displayName(user, slug),
			Slug:        slug,
			CloneURL:    fmt.Sprintf("%s/%s/%s.git", hubBase(r), user, slug),
			Public:      s.isPublic(user, slug),
			Notice:      notice, Error: errMsg,
		})
	}
	if r.Method != http.MethodPost {
		render(repo, "", "")
		return
	}
	switch r.FormValue("action") {
	case "make-public":
		if r.FormValue("confirm") != repo {
			render(repo, "", "To make it public, type the repo slug ("+repo+") exactly to confirm.")
			return
		}
		if err := s.setVisibility(user, repo, visPublic); err != nil {
			render(repo, "", "Could not update visibility.")
			return
		}
		render(repo, "This repository is now public — anyone with the link can read and clone it.", "")
	case "make-private":
		if err := s.setVisibility(user, repo, visPrivate); err != nil {
			render(repo, "", "Could not update visibility.")
			return
		}
		render(repo, "This repository is private again.", "")
	case "rename-display":
		s.setDisplayName(user, repo, r.FormValue("displayname"))
		render(repo, "Display name updated.", "")
	case "rename-slug":
		newSlug := strings.TrimSpace(r.FormValue("slug"))
		if newSlug == repo {
			render(repo, "", "")
			return
		}
		if !validSlug(newSlug) {
			render(repo, "", "Slugs use lowercase letters, digits, and hyphens (e.g. my-notes).")
			return
		}
		if err := s.Storage.RenameRepo(user, repo, newSlug); err != nil {
			render(repo, "", err.Error())
			return
		}
		http.Redirect(w, r, "/"+user+"/"+newSlug+"/settings", http.StatusFound)
	default:
		render(repo, "", "")
	}
}

// ---- auth / sessions ----

func (s *Server) webUser(r *http.Request) (string, bool) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if u, ok := parseSession(s.sessionSecret(), c.Value); ok {
			return u, true
		}
	}
	if u, ok := s.userForToken(tokenFromRequest(r)); ok {
		return u, true
	}
	return "", false
}

type loginData struct {
	baseData
	LoginUser, Next, Error string
	SignupOpen             bool
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	if r.Method == http.MethodPost {
		username := strings.ToLower(strings.TrimSpace(r.FormValue("user")))
		// The password field accepts an account password OR a valid git token
		// for that user (so existing token-only accounts can still sign in).
		secret := r.FormValue("password")
		if authUser, ok := s.checkLogin(username, secret); ok {
			s.setSession(w, r, authUser)
			http.Redirect(w, r, next, http.StatusFound)
			return
		}
		pages["login"].ExecuteTemplate(w, "base", loginData{LoginUser: username, Next: next, Error: "Wrong username or password.", SignupOpen: s.Accounts != nil && signupOpen})
		return
	}
	pages["login"].ExecuteTemplate(w, "base", loginData{Next: next, SignupOpen: s.Accounts != nil && signupOpen})
}

// checkLogin accepts an account password or a valid token for the user.
func (s *Server) checkLogin(username, secret string) (string, bool) {
	if s.Accounts != nil && username != "" {
		if u, err := s.Accounts.VerifyPassword(username, secret); err == nil {
			return u.Username, true
		}
	}
	if tu, ok := s.userForToken(secret); ok && (username == "" || tu == username) {
		return tu, true
	}
	return "", false
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, user string) {
	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    makeSession(s.sessionSecret(), user, exp),
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(exp, 0),
	})
}

type signupData struct {
	baseData
	LoginUser, Email, Next, Error string
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil || !signupOpen {
		http.Error(w, "signup is disabled on this hub", http.StatusForbidden)
		return
	}
	next := safeNext(r.FormValue("next"))
	if r.Method == http.MethodPost {
		username := strings.ToLower(strings.TrimSpace(r.FormValue("user")))
		email := strings.TrimSpace(r.FormValue("email"))
		pw := r.FormValue("password")
		fail := func(msg string) {
			pages["signup"].ExecuteTemplate(w, "base", signupData{LoginUser: username, Email: email, Next: next, Error: msg})
		}
		switch {
		case !validSlug(username):
			fail("Username must be lowercase letters, digits, and hyphens (e.g. jane-doe).")
		case len(pw) < 8:
			fail("Password must be at least 8 characters.")
		default:
			if _, err := s.Accounts.CreateUser(username, email, pw); err != nil {
				if errors.Is(err, ErrUserExists) {
					fail("That username is taken.")
				} else {
					s.Log.Printf("signup %q: %v", username, err)
					fail("Could not create the account.")
				}
				return
			}
			s.setSession(w, r, username)
			http.Redirect(w, r, next, http.StatusFound)
		}
		return
	}
	pages["signup"].ExecuteTemplate(w, "base", signupData{Next: next})
}

type patView struct {
	ID                  int64
	Name, Created, Used string
}
type accountData struct {
	baseData
	Username      string
	Host          string
	HasPassword   bool
	PATs          []patView
	NewToken      string
	Notice, Error string
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request, viewer string) {
	if s.Accounts == nil {
		http.NotFound(w, r)
		return
	}
	render := func(newToken, notice, errMsg string) {
		pats, _ := s.Accounts.ListPATs(viewer)
		var pv []patView
		for _, p := range pats {
			used := "never used"
			if p.LastUsed > 0 {
				used = "used " + ageString(p.LastUsed)
			}
			pv = append(pv, patView{ID: p.ID, Name: p.Name, Created: ageString(p.Created), Used: used})
		}
		s.renderPage(w, "account", accountData{
			baseData:    baseData{User: viewer, Viewer: viewer, Crumbs: []crumb{{viewer, "/" + viewer}, {"account", ""}}},
			Username:    viewer,
			Host:        r.Host,
			HasPassword: s.Accounts.HasPassword(viewer),
			PATs:        pv, NewToken: newToken, Notice: notice, Error: errMsg,
		})
	}
	if r.Method != http.MethodPost {
		render("", "", "")
		return
	}
	switch r.FormValue("action") {
	case "set-password":
		if pw := r.FormValue("password"); len(pw) < 8 {
			render("", "", "Password must be at least 8 characters.")
		} else if err := s.Accounts.SetPassword(viewer, pw); err != nil {
			render("", "", "Could not set the password.")
		} else {
			render("", "Password updated.", "")
		}
	case "create-token":
		plain, err := s.Accounts.CreatePAT(viewer, r.FormValue("name"))
		if err != nil {
			render("", "", "Could not create the token.")
		} else {
			render(plain, "Access token created — copy it now, it won't be shown again.", "")
		}
	case "revoke-token":
		var id int64
		fmt.Sscanf(r.FormValue("id"), "%d", &id)
		s.Accounts.RevokePAT(viewer, id)
		render("", "Token revoked.", "")
	default:
		render("", "", "")
	}
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
	Public                        bool
}
type dashboardData struct {
	baseData
	Repos []repoCard
}

type homeData struct {
	baseData
	SignupOpen bool
}

func (s *Server) renderHome(w http.ResponseWriter) {
	s.renderPage(w, "home", homeData{baseData: baseData{Home: true}, SignupOpen: s.Accounts != nil && signupOpen})
}

func (s *Server) renderDashboard(w http.ResponseWriter, user string) {
	repos, err := s.Storage.ListRepos(user)
	if err != nil {
		s.Log.Printf("list repos %s: %v", user, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := dashboardData{baseData: baseData{User: user, Viewer: user}}
	for i, name := range repos {
		desc, notes, ageUnix := s.repoMeta(user, name)
		data.Repos = append(data.Repos, repoCard{
			Name: name, Description: desc, Notes: notes,
			Age: ageString(ageUnix), Delay: fmt.Sprintf("%.2fs", float64(i)*0.05),
			Public: s.isPublic(user, name),
		})
	}
	s.renderPage(w, "dashboard", data)
}

// wantsJSON reports whether the caller wants a machine-readable response, so an
// agent (or the afs CLI) can list repos as JSON at the same dashboard URL.
func wantsJSON(r *http.Request) bool {
	return r.URL.Query().Get("format") == "json" || strings.HasPrefix(r.Header.Get("Accept"), "application/json")
}

// dashboardJSON returns the signed-in user's repositories as JSON.
func (s *Server) dashboardJSON(w http.ResponseWriter, r *http.Request, user string) {
	repos, err := s.Storage.ListRepos(user)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	base := hubBase(r)
	type repoJSON struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Notes       int    `json:"notes"`
		Public      bool   `json:"public"`
		Updated     string `json:"updated,omitempty"`
		URL         string `json:"url"`
		CloneURL    string `json:"clone_url"`
	}
	out := struct {
		User  string     `json:"user"`
		Repos []repoJSON `json:"repos"`
	}{User: user, Repos: []repoJSON{}}
	for _, name := range repos {
		desc, notes, ageUnix := s.repoMeta(user, name)
		out.Repos = append(out.Repos, repoJSON{
			Name: name, Description: desc, Notes: notes,
			Public:   s.isPublic(user, name),
			Updated:  ageString(ageUnix),
			URL:      base + "/" + user + "/" + name,
			CloneURL: base + "/" + user + "/" + name + ".git",
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(out)
}

type repoData struct {
	baseData
	Repo, DisplayName, Description, CloneCmd string
	Public, CanWrite                         bool
	Empty                                    bool
	AgentEnabled                             bool // show the "talk to an agent" button
	Root                                     *treeNode
}

func (s *Server) renderRepo(w http.ResponseWriter, r *http.Request, user, repo, viewer string) {
	bare := s.Storage.RepoDir(user, repo)
	files, err := RepoSnapshot("git", bare, defaultRef)
	if err != nil {
		s.Log.Printf("snapshot %s/%s: %v", user, repo, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(files) == 0 {
		// May only look empty because HEAD points at an unborn branch (client
		// pushed a differently-named branch, e.g. master). Repair and re-read.
		if err := s.Storage.EnsureHEAD(user, repo); err == nil {
			files, _ = RepoSnapshot("git", bare, defaultRef)
		}
	}
	desc, _, _ := s.repoMeta(user, repo)
	data := repoData{
		baseData:     baseData{User: user, Viewer: viewer, Crumbs: []crumb{{user, "/" + user}, {repo, "/" + user + "/" + repo}}, AgentURL: s.agentPath(user, repo, viewer)},
		Repo:         repo,
		DisplayName:  s.displayName(user, repo),
		Description:  desc,
		CloneCmd:     fmt.Sprintf("git clone %s/%s/%s.git", hubBase(r), user, repo),
		Public:       s.isPublic(user, repo),
		CanWrite:     viewer == user,
		Empty:        len(files) == 0,
		AgentEnabled: viewer == user && s.Agent.Enabled(),
	}
	if !data.Empty {
		data.Root = buildTree(files, user, repo)
	}
	s.renderPage(w, "repo", data)
}

// handleAgent sends the owner to a write-capable agent for this repo, running in
// a Fly Sprite. If the sprite is ready it redirects there; otherwise it kicks
// off provisioning and shows a self-refreshing "starting" page.
func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request, user, repo string) {
	if !s.Agent.Enabled() {
		http.Error(w, "the agent feature is not configured on this hub", http.StatusServiceUnavailable)
		return
	}
	prefix := "/" + user + "/" + repo + "/agent"
	// Trailing slash on the base so the agent's relative asset/API paths resolve
	// under this prefix (…/agent/styles.css, …/agent/api/chat).
	if r.URL.Path == prefix {
		http.Redirect(w, r, prefix+"/", http.StatusFound)
		return
	}
	url, ready := s.Agent.Ensure(user, repo)
	if !ready {
		if r.URL.Path != prefix+"/" {
			http.Error(w, "agent is starting", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang=en><head><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<meta http-equiv=refresh content=4>
<title>Starting agent · %[1]s/%[2]s</title>
<link rel=stylesheet href=%[3]q>
<style>.starting{max-width:540px;margin:14vh auto;text-align:center;padding:0 1.25rem}
.spin{width:26px;height:26px;border:3px solid var(--edge);border-top-color:var(--accent);border-radius:50%%;animation:sp .9s linear infinite;margin:0 auto 1.6rem}
@keyframes sp{to{transform:rotate(360deg)}}</style></head>
<body><div class="starting"><div class="spin"></div>
<h1 class="page-title">Waking your agent…</h1>
<p class="page-sub">Setting up a private sandbox for <b>%[1]s/%[2]s</b> and cloning the knowledge base. The first start takes about a minute — this page refreshes itself, then hands you to the agent.</p>
<p style="margin-top:1.6rem"><a href="/%[1]s/%[2]s">← back to the repository</a></p></div></body></html>`,
			user, repo, assetURL("style.css"))
		return
	}
	// Reverse-proxy to the sprite, injecting the Sprites bearer server-side, so
	// the user stays authenticated here on the hub and never sees the
	// sprites.dev login — and the sprite stays private to our org.
	s.Agent.Proxy(w, r, url, prefix)
}

type backlinkView struct{ Name, Desc, Href string }
type commitView struct{ Short, Subject, Author, When string }

type fileData struct {
	baseData
	Repo, Path, Name, Description, Age string
	IsMarkdown, IsText, CanWrite       bool
	BodyHTML                           template.HTML
	RawText, RawHref                   string
	Backlinks                          []backlinkView
	History                            []commitView
	Tree                               *treeNode // repo file tree for the left nav
}

func (s *Server) renderFile(w http.ResponseWriter, r *http.Request, user, repo, filePath, viewer string) {
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
		baseData:   baseData{User: user, Viewer: viewer, Crumbs: []crumb{{user, "/" + user}, {repo, "/" + user + "/" + repo}, {pathBase(filePath), ""}}, AgentURL: s.agentPath(user, repo, viewer)},
		Repo:       repo,
		Path:       filePath,
		Name:       pathBase(filePath),
		Age:        ageString(ageUnix),
		RawHref:    "/" + user + "/" + repo + "/raw/" + filePath,
		IsMarkdown: strings.EqualFold(path.Ext(filePath), ".md"),
		CanWrite:   viewer == user,
	}

	// Left-nav file tree: reuse the repo landing page's tree, with the current
	// note highlighted. Same data already loaded above — no extra git calls.
	if len(files) > 0 {
		data.Tree = buildTree(files, user, repo)
		markCurrent(data.Tree, filePath)
	}

	// Only render text that is valid UTF-8 and not enormous; bigger or binary
	// files link to /raw instead (and aren't editable in the browser).
	const maxRenderBytes = 1 << 20 // 1 MiB
	renderable := utf8.ValidString(content) && !strings.ContainsRune(content, 0) && len(content) <= maxRenderBytes
	data.IsMarkdown = data.IsMarkdown && renderable

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
	} else if renderable {
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
		w.Header().Set("Content-Disposition", "attachment; filename=\""+dispositionName(pathBase(filePath))+"\"")
	}
	w.Write([]byte(content))
}

// dispositionName sanitizes a filename for a quoted Content-Disposition value:
// drop control chars (incl. CR/LF header injection) and escape backslash/quote.
func dispositionName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20:
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

type historyData struct {
	baseData
	Repo    string
	Commits []commitView
}

func (s *Server) renderHistory(w http.ResponseWriter, user, repo, viewer string) {
	data := historyData{
		baseData: baseData{User: user, Viewer: viewer, Crumbs: []crumb{{user, "/" + user}, {repo, "/" + user + "/" + repo}, {"history", ""}}},
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
	Current    bool // the file currently being viewed (for the file-page side tree)
	Children   []*treeNode
}

// markCurrent flags the leaf whose path matches filePath so the side tree can
// highlight the note being read. Returns whether it was found in this subtree.
func markCurrent(n *treeNode, filePath string) bool {
	found := false
	for _, c := range n.Children {
		if !c.IsDir && c.Path == filePath {
			c.Current = true
			found = true
		} else if c.IsDir && markCurrent(c, filePath) {
			found = true
		}
	}
	return found
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
