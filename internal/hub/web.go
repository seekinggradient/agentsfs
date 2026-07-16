package hub

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	texttemplate "text/template"
	"time"
	"unicode/utf8"

	afs "agentsfs.ai/afs"
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
	for _, n := range []string{
		"assets/style.css",
		"assets/redesign.css",
		"assets/redesign-v2.css",
		"assets/app.js",
		"assets/redesign-v2.js",
		"assets/hero-agentsfs-home.webp",
		"assets/favicon.svg",
		"assets/favicon-32.png",
		"assets/apple-touch-icon.png",
		"assets/og-hero.png",
	} {
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
	for _, name := range []string{"home", "redesign", "redesign-v2", "dashboard", "repo", "file", "history", "login", "edit", "settings", "signup", "account"} {
		out[name] = template.Must(template.Must(base.Clone()).ParseFS(assetsFS, "assets/"+name+".html"))
	}
	return out
}

type crumb struct{ Name, Href string }

// baseData is embedded in every page. User is the namespace the page belongs to
// (used to build URLs); Viewer is who is signed in ("" when anonymous), used for
// the header's account chip and logout.
type baseData struct {
	User      string
	Viewer    string
	Crumbs    []crumb
	Home      bool
	Dashboard bool
	FileView  bool
	AgentURL  string // when set, base.html renders the agent trigger + side dock
}

// agentPath returns the in-hub agent URL for a repo when the viewer owns it and
// the agent feature is on, else "" (so the dock/trigger stay hidden). It points
// at the single per-user workspace agent, pre-focused on this repo (?repo=), so
// there is one sprite per user backing both entry points.
func (s *Server) agentPath(user, repo, viewer string) string {
	if viewer == user && s.Agent.Enabled() {
		return "/agent/?repo=" + url.QueryEscape(repo)
	}
	return ""
}

// userAgentPath returns the top-level cross-repo agent URL for the signed-in
// user, or "" when the feature is off.
func (s *Server) userAgentPath(viewer string) string {
	if viewer != "" && s.Agent.Enabled() {
		return "/agent/"
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

// servePWA serves the progressive-web-app plumbing from stable root paths (the
// service worker in particular must live at the root to control the whole
// origin). All files are the same embedded assets; only the paths + headers
// differ from serveAsset.
func servePWA(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/manifest.webmanifest":
		data, err := assetsFS.ReadFile("assets/manifest.webmanifest")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(data)
	case "/sw.js":
		data, err := assetsFS.ReadFile("assets/sw.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		// Let the worker claim the whole origin, and have browsers revalidate it
		// on every load so a redeploy ships a new worker promptly.
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(data)
	case "/apple-touch-icon.png", "/apple-touch-icon-precomposed.png":
		data, err := assetsFS.ReadFile("assets/apple-touch-icon.png")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

// serveWeb handles all browser (non-git) requests: login, logout, dashboard,
// repo, note, raw, and history. Private by default — a session cookie or a
// Basic token owning the namespace is required.
func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/robots.txt":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "User-agent: *\nAllow: /\nDisallow: /account\nDisallow: /admin/\nDisallow: /agent/\nDisallow: /login\nDisallow: /signup\nDisallow: /redesign\nDisallow: /redesign-v2\n\nSitemap: https://hub.agentsfs.ai/sitemap.xml\n")
		return
	case "/sitemap.xml":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://hub.agentsfs.ai/</loc><changefreq>weekly</changefreq><priority>1.0</priority></url>
</urlset>
`)
		return
	case "/redesign":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.renderRedesign(w, r)
		return
	case "/redesign-v2":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.renderRedesignV2(w, r)
		return
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
	if strings.HasPrefix(r.URL.Path, "/invite/") {
		s.handleCollaboratorInvite(w, r, strings.TrimPrefix(r.URL.Path, "/invite/"))
		return
	}

	// Top-level per-user agent (workspace mode): ONE sprite per user, spanning
	// all their knowledge bases. Handled before user/repo parsing — like
	// /account it is inherently the viewer's own namespace, so owner==viewer is
	// implicit and no cross-user access is possible.
	if r.URL.Path == "/agent" || strings.HasPrefix(r.URL.Path, "/agent/") {
		v, ok := s.webUser(r)
		if !ok {
			s.needLogin(w, r)
			return
		}
		s.handleUserAgent(w, r, v)
		return
	}

	// Hosted-Eve upstream mode also forwards eve's workflow-world callback
	// prefix, un-stripped, behind the same session gate. This honors eve's
	// documented reverse-proxy contract (forward both /eve/ and
	// /.well-known/workflow/). In the Vercel-hosted topology this callback is
	// server-to-server (Vercel Workflow → the eve deployment) and does not
	// traverse the Hub, so this route is a no-op there; it exists for the
	// self-hosted-Eve-behind-the-Hub fallback. Only claimed in Eve mode, and
	// ".well-known" can never be a username, so the user namespace is unaffected.
	if s.Agent.EveMode() && strings.HasPrefix(r.URL.Path, "/.well-known/workflow/") {
		v, ok := s.webUser(r)
		if !ok {
			s.needLogin(w, r)
			return
		}
		s.Agent.EveProxy(w, r, v)
		return
	}

	// Admin console (operator-only): fleet-wide model-usage metrics + the signup
	// allowlist / waitlist. Gated on HUB_ADMIN_USER.
	if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
		v, ok := s.webUser(r)
		if !ok {
			s.needLogin(w, r)
			return
		}
		if s.AdminUser == "" || v != s.AdminUser {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/admin/access":
			s.handleAdminAccess(w, r)
		default: // /admin, /admin/metrics
			s.handleAdminMetrics(w, r)
		}
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
			if len(segs) == 0 && (r.Method == http.MethodGet || r.Method == http.MethodHead) && !wantsJSON(r) {
				s.renderHome(w, r)
				return
			}
			s.needLogin(w, r)
			return
		}
		if len(segs) == 1 && strings.ToLower(segs[0]) != viewer {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if wantsJSON(r) {
			s.dashboardJSON(w, r, viewer)
		} else {
			s.renderDashboard(w, r, viewer)
		}
		return
	}

	// Canonicalize the namespace to lowercase (usernames are lowercase) so the
	// owner check + collaborator lookups can't desync from a mixed-case URL.
	user := strings.ToLower(segs[0])
	repo := strings.TrimSuffix(segs[1], ".git")
	if !nameRe.MatchString(user) || !nameRe.MatchString(repo) || !s.Storage.Exists(user, repo) {
		http.NotFound(w, r)
		return
	}
	owner := isAuthed && viewer == user
	role := ""
	if isAuthed && !owner {
		role = s.Accounts.CollaboratorRole(user, repo, viewer)
	}
	rest := segs[2:]
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}

	// Authorize by route: settings + the per-repo agent are owner-only; edit
	// needs write (owner or a write collaborator); read routes allow the owner,
	// any collaborator, or anyone if the repo is public.
	var allowed bool
	switch sub {
	case "settings", "agent":
		allowed = owner
	case "edit":
		allowed = owner || role == "write"
	default:
		allowed = owner || role != "" || s.isPublic(user, repo)
	}
	if !allowed {
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
	case rest[0] == "download" && len(rest) == 1:
		s.handleRepoDownload(w, r, user, repo)
	case rest[0] == "history" && len(rest) == 1:
		s.renderHistory(w, r, user, repo, viewer)
	case rest[0] == "settings" && len(rest) == 1:
		s.handleSettings(w, r, user, repo, viewer)
	case rest[0] == "agent":
		s.handleAgent(w, r, user, repo)
	case (rest[0] == "blob" || rest[0] == "raw" || rest[0] == "download" || rest[0] == "edit") && len(rest) > 1:
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
		case "download":
			s.handleDownload(w, r, user, repo, fp)
		case "edit":
			s.handleEdit(w, r, user, repo, fp, viewer)
		}
	default:
		http.NotFound(w, r)
	}
}

func isLoopbackPreview(r *http.Request) bool {
	host := r.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
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
		// Attribute the commit to whoever made the edit (owner OR a write
		// collaborator), not the namespace owner — git blame stays truthful.
		_, err := CommitFile("git", bare, filePath, content, viewer, r.FormValue("message"), r.FormValue("head"))
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
		s.renderPage(w, r, "edit", editData{
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
	s.renderPage(w, r, "edit", editData{
		baseData: baseData{User: user, Viewer: viewer, Crumbs: crumbs},
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
	Collaborators                     []Collaborator
	PendingInvites                    []CollaboratorInvite
	InviteLink, InvitePrompt          string
	Notice, Error                     string
}

type collaboratorPromptData struct {
	Owner, Repo, Role, InviteURL string
}

// collaboratorAgentPrompt renders the single source-of-truth handoff prompt
// shipped in prompts/. Its afs onboarding pointer deliberately targets the
// canonical agent-start documentation instead of duplicating that guide here.
func collaboratorAgentPrompt(owner, repo, role, inviteURL string) (string, error) {
	source, err := afs.DocsFS.ReadFile("prompts/collaborator-invite.md")
	if err != nil {
		return "", err
	}
	tmpl, err := texttemplate.New("collaborator-invite").Parse(string(source))
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	err = tmpl.Execute(&out, collaboratorPromptData{Owner: owner, Repo: repo, Role: role, InviteURL: inviteURL})
	return strings.TrimSpace(out.String()), err
}

// handleSettings is the owner-only repo settings page: visibility (with a typed
// confirmation to go public), display name, and slug rename (with a duplicate
// check). serveWeb already guarantees the caller owns the repo.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request, user, repo, viewer string) {
	var inviteLink, invitePrompt string
	render := func(slug, notice, errMsg string) {
		s.renderPage(w, r, "settings", settingsData{
			baseData:       baseData{User: user, Viewer: viewer, Crumbs: []crumb{{user, "/" + user}, {slug, "/" + user + "/" + slug}, {"settings", ""}}},
			Repo:           slug,
			DisplayName:    s.displayName(user, slug),
			Slug:           slug,
			CloneURL:       fmt.Sprintf("%s/%s/%s.git", hubBase(r), user, slug),
			Public:         s.isPublic(user, slug),
			Collaborators:  s.Accounts.ListCollaborators(user, slug),
			PendingInvites: s.Accounts.ListCollaboratorInvites(user, slug),
			InviteLink:     inviteLink, InvitePrompt: invitePrompt,
			Notice: notice, Error: errMsg,
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
	case "add-collaborator":
		if s.Accounts == nil {
			render(repo, "", "Accounts are not enabled on this hub.")
			return
		}
		email := strings.TrimSpace(r.FormValue("email"))
		role := r.FormValue("role")
		u, token, err := s.Accounts.AddCollaboratorByEmail(user, repo, email, role)
		if err != nil {
			render(repo, "", err.Error())
			return
		}
		if u != "" {
			render(repo, u+" can now "+role+" this repo. They'll see it under \"Shared with you\".", "")
		} else {
			inviteLink = hubBase(r) + "/invite/" + token
			var promptErr error
			invitePrompt, promptErr = collaboratorAgentPrompt(user, repo, role, inviteLink)
			if promptErr != nil {
				s.Log.Printf("render collaborator invite prompt: %v", promptErr)
			}
			render(repo, "Invitation created for "+email+".", "")
		}
	case "remove-collaborator":
		if s.Accounts != nil {
			s.Accounts.RemoveCollaborator(user, repo, r.FormValue("username"))
		}
		render(repo, "Collaborator removed.", "")
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
		oldBare := s.Storage.RepoDir(user, repo)
		if err := s.Storage.RenameRepo(user, repo, newSlug); err != nil {
			render(repo, "", err.Error())
			return
		}
		if s.LFS != nil {
			if err := s.LFS.RenameRepo(user, repo, newSlug); err != nil {
				s.Log.Printf("rename lfs %s/%s -> %s: %v", user, repo, newSlug, err)
			}
		}
		s.Accounts.RenameRepoCollaborators(user, repo, newSlug) // keep grants attached
		s.views.drop(oldBare)                                   // stale entry keyed by the old path would just linger
		http.Redirect(w, r, "/"+user+"/"+newSlug+"/settings", http.StatusFound)
	case "delete-repo":
		// Deliberately session-only: webUser (used for `owner` above) also accepts
		// a PAT, and PATs live on remote agent VMs. A prompt-injected agent must
		// not be able to destroy a knowledge base, so the one irreversible-ish
		// action on this page requires the human to be sitting at the browser.
		if u, ok := s.webSessionUser(r); !ok || u != user {
			render(repo, "", "Deleting a repository must be confirmed from the web app while signed in.")
			return
		}
		if r.FormValue("confirm") != repo {
			render(repo, "", "To delete it, type the repo slug ("+repo+") exactly to confirm.")
			return
		}
		bare := s.Storage.RepoDir(user, repo)
		if err := s.Storage.DeleteRepo(user, repo); err != nil {
			render(repo, "", "Could not delete the repository.")
			return
		}
		// dropRedirectsTo is LocalStorage-specific (redirects are a filesystem
		// implementation detail, not part of the Storage interface) — skip it for
		// a future backend that doesn't have this type.
		if ls, ok := s.Storage.(*LocalStorage); ok {
			ls.dropRedirectsTo(user, repo)
		}
		s.Accounts.DeleteRepoCollaborators(user, repo)
		s.views.drop(bare)
		s.Log.Printf("deleted repo %s/%s", user, repo)
		http.Redirect(w, r, "/"+user, http.StatusFound)
	default:
		render(repo, "", "")
	}
}

// ---- auth / sessions ----

// webSessionUser is webUser restricted to the cookie path, for actions that
// must not be reachable with a PAT (destructive ones — PATs live on remote
// agent VMs, and a prompt-injected agent must not be able to trigger them).
func (s *Server) webSessionUser(r *http.Request) (string, bool) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if u, ok := parseSession(s.sessionSecret(), c.Value); ok {
			return u, true
		}
	}
	return "", false
}

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
	LoginUser, Email, Next, Error       string
	InviteToken, InviteRepo, InviteRole string
	AllowlistActive                     bool   // signup is invite-gated
	Waitlisted                          bool   // this submission was added to the waitlist
	WaitEmail                           string // the email recorded on the waitlist
}

// looksLikeEmail is a light structural check (not RFC-perfect): enough to reject
// blanks and obvious typos before we gate on it — local@host with a dotted host
// that has characters on both sides of its last dot, and no whitespace.
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || strings.ContainsAny(s, " \t") {
		return false
	}
	host := s[at+1:]
	dot := strings.LastIndexByte(host, '.')
	return dot > 0 && dot < len(host)-1
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	inviteToken := strings.TrimSpace(r.FormValue("invite"))
	var invite *Invite
	if inviteToken != "" && s.Accounts != nil {
		var ok bool
		invite, ok = s.Accounts.InviteForToken(inviteToken)
		if !ok {
			http.Error(w, "that invitation is invalid or expired", http.StatusNotFound)
			return
		}
	}
	if s.Accounts == nil || (!signupOpen && invite == nil) {
		http.Error(w, "signup is disabled on this hub", http.StatusForbidden)
		return
	}
	next := safeNext(r.FormValue("next"))
	gated := s.Accounts.AllowlistActive()
	render := func(d signupData) {
		d.Next = next
		d.AllowlistActive = gated
		d.InviteToken = inviteToken
		if invite != nil {
			d.InviteRepo = invite.Owner + "/" + invite.Repo
			d.InviteRole = invite.Role
			if d.Email == "" {
				d.Email = invite.Email
			}
		}
		pages["signup"].ExecuteTemplate(w, "base", d)
	}
	if r.Method == http.MethodPost {
		username := strings.ToLower(strings.TrimSpace(r.FormValue("user")))
		email := strings.TrimSpace(r.FormValue("email"))
		pw := r.FormValue("password")
		fail := func(msg string) {
			render(signupData{LoginUser: username, Email: email, Error: msg})
		}
		switch {
		case !validSlug(username):
			fail("Username must be lowercase letters, digits, and hyphens (e.g. jane-doe).")
		case invite != nil && normEmail(email) != invite.Email:
			fail("Use the email address this invitation was sent to.")
		case email != "" && !looksLikeEmail(email):
			fail("Enter a valid email address.")
		case gated && !looksLikeEmail(email):
			fail("A valid email is required to request access.")
		case invite != nil && !looksLikeEmail(email):
			fail("A valid email is required to accept this invitation.")
		case len(pw) < 8:
			fail("Password must be at least 8 characters.")
		case invite == nil && gated && !s.Accounts.IsAllowed(email):
			// Not on the allowlist → record the request on the waitlist and show a
			// friendly confirmation instead of creating an account.
			if err := s.Accounts.AddToWaitlist(email, username); err != nil {
				s.Log.Printf("waitlist %q: %v", email, err)
			}
			render(signupData{Waitlisted: true, WaitEmail: email})
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
			if invite != nil {
				if err := s.Accounts.AcceptCollaboratorInvite(inviteToken, username); err != nil {
					s.Log.Printf("accept collaborator invite for %q: %v", username, err)
					fail("Your account was created, but the invitation could not be attached. Please ask the owner to send a new link.")
					return
				}
			}
			s.Accounts.RemoveFromWaitlist(email) // admitted — no longer waiting
			s.setSession(w, r, username)
			http.Redirect(w, r, next, http.StatusFound)
		}
		return
	}
	render(signupData{})
}

// handleCollaboratorInvite redeems an invite for an already signed-in matching
// account, or sends a new recipient to the signup form with the email locked
// to the invitation.
func (s *Server) handleCollaboratorInvite(w http.ResponseWriter, r *http.Request, token string) {
	if s.Accounts == nil || token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inv, ok := s.Accounts.InviteForToken(token)
	if !ok {
		http.Error(w, "that invitation is invalid or expired", http.StatusNotFound)
		return
	}
	if viewer, loggedIn := s.webSessionUser(r); loggedIn {
		user, matchingAccount := s.Accounts.UserByEmail(inv.Email)
		if !matchingAccount || user.Username != viewer {
			http.Error(w, "this invitation belongs to a different email address", http.StatusForbidden)
			return
		}
		if err := s.Accounts.AcceptCollaboratorInvite(token, viewer); err != nil {
			http.Error(w, "could not accept invitation", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/"+inv.Owner+"/"+inv.Repo, http.StatusSeeOther)
		return
	}

	next := "/" + inv.Owner + "/" + inv.Repo
	http.Redirect(w, r, "/signup?invite="+url.QueryEscape(token)+"&next="+url.QueryEscape(next), http.StatusSeeOther)
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
		s.renderPage(w, r, "account", accountData{
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
	Name, DisplayName, Age, CloneCmd, AccessLabel string
	Notes                                         int
	UpdatedUnix                                   int64
	Public                                        bool
}
type sharedCard struct {
	Owner, Name, DisplayName, Age, Role, RoleLabel, CloneCmd string
	Notes                                                    int
	UpdatedUnix                                              int64
}
type dashboardData struct {
	baseData
	Repos  []repoCard
	Shared []sharedCard
}

// dashboardDisplayName keeps the configured human-facing name when one exists,
// and otherwise turns the URL slug into a readable label. The slug remains
// visible below it, so presentation never obscures repository identity.
func dashboardDisplayName(displayName, slug string) string {
	if displayName != "" && displayName != slug {
		return displayName
	}
	readable := strings.ReplaceAll(slug, "-", " ")
	if readable == "" {
		return slug
	}
	return strings.ToUpper(readable[:1]) + readable[1:]
}

type homeData struct {
	baseData
	SignupOpen   bool
	InviteOnly   bool
	AgentEnabled bool
	NoIndex      bool
}

func (s *Server) renderHome(w http.ResponseWriter, r *http.Request) {
	s.renderMarketingPage(w, r, "redesign-v2", false)
}

func (s *Server) renderRedesign(w http.ResponseWriter, r *http.Request) {
	s.renderMarketingPage(w, r, "redesign", true)
}

func (s *Server) renderRedesignV2(w http.ResponseWriter, r *http.Request) {
	s.renderMarketingPage(w, r, "redesign-v2", true)
}

func (s *Server) renderMarketingPage(w http.ResponseWriter, r *http.Request, page string, noIndex bool) {
	open := s.Accounts != nil && signupOpen
	viewer, _ := s.webUser(r)
	s.renderPage(w, r, page, homeData{
		baseData:     baseData{Home: true, Viewer: viewer},
		SignupOpen:   open,
		InviteOnly:   open && s.Accounts.AllowlistActive(),
		AgentEnabled: s.Agent.Enabled(),
		NoIndex:      noIndex,
	})
}

func (s *Server) renderDashboard(w http.ResponseWriter, r *http.Request, user string) {
	repos, err := s.Storage.ListRepos(user)
	if err != nil {
		s.Log.Printf("list repos %s: %v", user, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	base := hubBase(r)
	data := dashboardData{baseData: baseData{User: user, Viewer: user, Dashboard: true, AgentURL: s.userAgentPath(user)}}
	for _, name := range repos {
		_, notes, ageUnix := s.repoMeta(user, name)
		displayName := dashboardDisplayName(s.displayName(user, name), name)
		data.Repos = append(data.Repos, repoCard{
			Name: name, DisplayName: displayName, Notes: notes,
			Age: ageString(ageUnix), UpdatedUnix: ageUnix,
			CloneCmd: "git clone " + base + "/" + user + "/" + name + ".git",
			Public:   s.isPublic(user, name),
		})
		if data.Repos[len(data.Repos)-1].Public {
			data.Repos[len(data.Repos)-1].AccessLabel = "Public"
		} else {
			data.Repos[len(data.Repos)-1].AccessLabel = "Private"
		}
	}
	// Repos other people shared with this user (skip any whose repo was deleted).
	for _, sr := range s.Accounts.ReposSharedWith(user) {
		if !s.Storage.Exists(sr.Owner, sr.Repo) {
			continue
		}
		_, notes, ageUnix := s.repoMeta(sr.Owner, sr.Repo)
		roleLabel := "Read only"
		if sr.Role == "write" {
			roleLabel = "Can edit"
		}
		displayName := dashboardDisplayName(s.displayName(sr.Owner, sr.Repo), sr.Repo)
		data.Shared = append(data.Shared, sharedCard{
			Owner: sr.Owner, Name: sr.Repo, DisplayName: displayName, Notes: notes,
			Age: ageString(ageUnix), UpdatedUnix: ageUnix, Role: sr.Role, RoleLabel: roleLabel,
			CloneCmd: "git clone " + base + "/" + sr.Owner + "/" + sr.Repo + ".git",
		})
	}
	w.Header().Set("Cache-Control", "private, no-store")
	s.renderPage(w, r, "dashboard", data)
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
		Owner       string `json:"owner"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Notes       int    `json:"notes"`
		Public      bool   `json:"public"`
		Updated     string `json:"updated,omitempty"`
		URL         string `json:"url"`
		CloneURL    string `json:"clone_url"`
		Role        string `json:"role,omitempty"`
		Shared      bool   `json:"shared,omitempty"`
	}
	out := struct {
		User  string     `json:"user"`
		Repos []repoJSON `json:"repos"`
	}{User: user, Repos: []repoJSON{}}
	for _, name := range repos {
		desc, notes, ageUnix := s.repoMeta(user, name)
		out.Repos = append(out.Repos, repoJSON{
			Owner: user, Name: name, Description: desc, Notes: notes,
			Public:   s.isPublic(user, name),
			Updated:  ageString(ageUnix),
			URL:      base + "/" + user + "/" + name,
			CloneURL: base + "/" + user + "/" + name + ".git",
		})
	}
	for _, sr := range s.Accounts.ReposSharedWith(user) {
		if !s.Storage.Exists(sr.Owner, sr.Repo) {
			continue
		}
		desc, notes, ageUnix := s.repoMeta(sr.Owner, sr.Repo)
		out.Repos = append(out.Repos, repoJSON{
			Owner: sr.Owner, Name: sr.Repo, Description: desc, Notes: notes,
			Public: s.isPublic(sr.Owner, sr.Repo), Updated: ageString(ageUnix),
			URL:      base + "/" + sr.Owner + "/" + sr.Repo,
			CloneURL: base + "/" + sr.Owner + "/" + sr.Repo + ".git",
			Role:     sr.Role, Shared: true,
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(out)
}

type repoData struct {
	baseData
	Repo, DisplayName, Description, CloneCmd, DownloadHref string
	Public, CanWrite                                       bool
	Role                                                   string // collaborator role for the viewer ("" = owner/none)
	Empty                                                  bool
	AgentEnabled                                           bool // show the "talk to an agent" button
	Root                                                   *treeNode
	Files                                                  []repoFileRow
	GraphJSON                                              template.JS
	GraphNodes, GraphLinks                                 int
}

type repoFileRow struct {
	Name, Path, Folder, Description, Age, Href, DownloadHref, Type string
	UpdatedUnix                                                    int64
}

func repoFileRows(files []RepoFile, user, repo string) []repoFileRow {
	rows := make([]repoFileRow, 0, len(files))
	for _, file := range files {
		folder := pathDir(file.Path)
		if folder == "" || folder == "." {
			folder = "Root"
		}
		baseName := pathBase(file.Path)
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(file.Path)), ".")
		if strings.HasPrefix(baseName, ".") && !strings.Contains(baseName[1:], ".") {
			ext = ""
		}
		typeLabel := strings.ToUpper(ext)
		switch ext {
		case "md", "mdown", "markdown":
			typeLabel = "Markdown"
		case "png", "jpg", "jpeg", "gif", "webp", "svg":
			typeLabel = "Image"
		case "json", "yaml", "yml", "toml", "csv", "tsv":
			typeLabel = "Data"
		case "":
			typeLabel = "File"
		}
		rows = append(rows, repoFileRow{
			Name: baseName, Path: file.Path, Folder: folder,
			Description: cleanDesc(file.Description), Age: ageString(file.LastCommit),
			Href:         "/" + user + "/" + repo + "/blob/" + file.Path,
			DownloadHref: "/" + user + "/" + repo + "/download/" + file.Path + "?format=original",
			Type:         typeLabel, UpdatedUnix: file.LastCommit,
		})
	}
	return rows
}

func (s *Server) renderRepo(w http.ResponseWriter, r *http.Request, user, repo, viewer string) {
	view, err := s.repoView(user, repo)
	if err != nil {
		s.Log.Printf("snapshot %s/%s: %v", user, repo, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(view.Files) == 0 {
		// May only look empty because HEAD points at an unborn branch (client
		// pushed a differently-named branch, e.g. master). Repair and re-read.
		if err := s.Storage.EnsureHEAD(user, repo); err == nil {
			if v, err := s.repoView(user, repo); err == nil {
				view = v
			}
		}
	}
	desc, _, _ := repoFilesMeta(view.Files)
	data := repoData{
		baseData:     baseData{User: user, Viewer: viewer, Crumbs: []crumb{{user, "/" + user}, {repo, "/" + user + "/" + repo}}, AgentURL: s.agentPath(user, repo, viewer)},
		Repo:         repo,
		DisplayName:  s.displayName(user, repo),
		Description:  desc,
		CloneCmd:     fmt.Sprintf("git clone %s/%s/%s.git", hubBase(r), user, repo),
		DownloadHref: "/" + user + "/" + repo + "/download",
		Public:       s.isPublic(user, repo),
		CanWrite:     s.canWrite(user, repo, viewer),
		Role:         collabRoleFor(s.Accounts, user, repo, viewer),
		Empty:        len(view.Files) == 0,
		AgentEnabled: viewer == user && s.Agent.Enabled(),
	}
	if !data.Empty {
		data.Root = buildTree(view.Files, user, repo)
		data.Files = repoFileRows(view.Files, user, repo)
		data.GraphJSON = template.JS(view.GraphJSON)
		data.GraphNodes = len(view.Graph.Nodes)
		data.GraphLinks = len(view.Graph.Links)
	}
	s.renderPage(w, r, "repo", data)
}

// handleAgent (per-repo route) now redirects to the single per-user workspace
// agent, pre-focused on this repo. There's one sprite per user backing both the
// repo button and the top-level agent; this keeps old /<user>/<repo>/agent links
// (and any open dock) working.
func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request, user, repo string) {
	http.Redirect(w, r, "/agent/?repo="+url.QueryEscape(repo), http.StatusFound)
}

// handleUserAgent proxies the signed-in user to their single cross-repo agent
// sprite (workspace mode), provisioning it on first use with a self-refreshing
// "starting" page. Mirrors handleAgent's trailing-slash + Ensure/Proxy pattern.
func (s *Server) handleUserAgent(w http.ResponseWriter, r *http.Request, viewer string) {
	if !s.Agent.Enabled() {
		http.Error(w, "the agent feature is not configured on this hub", http.StatusServiceUnavailable)
		return
	}
	const prefix = "/agent"
	// Trailing slash on the base so the agent's relative asset/API paths resolve
	// under this prefix (…/agent/styles.css, …/agent/api/chat). Preserve the
	// ?repo= hint across the redirect so the repo button lands pre-focused.
	if r.URL.Path == prefix {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		target := prefix + "/"
		if q := r.URL.RawQuery; q != "" {
			target += "?" + q
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	// Hosted-Eve upstream mode: no sprite, no provisioning, no embedded UI and
	// no route allow-list — the trusted Eve app serves its own shell + API, so
	// the Hub just authenticates (done by the caller) and reverse-proxies the
	// whole /agent/* surface UN-stripped (the app is basePath="/agent"-aware, so
	// its routes live under /agent upstream too). Selected by HUB_EVE_AGENT_URL;
	// when unset this branch is skipped and the sprite path below is unchanged.
	if s.Agent.EveMode() {
		s.Agent.EveProxy(w, r, viewer)
		return
	}
	agentPath, ok := relativeAgentPath(r.URL.Path, prefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	route, ok := enforceAgentRoute(w, r.Method, agentPath, false)
	if !ok {
		return
	}
	// Explicit user-initiated retry from the starting page. It only clears the
	// backoff/wake-grace gates for the next load (idempotent, harmless), so a
	// GET is acceptable; the redirect strips the param so the page's own
	// refresh can't re-trigger it.
	if agentPath == "/" && r.URL.Query().Get("retry") == "1" {
		s.Agent.RetryProvision(viewer)
		http.Redirect(w, r, prefix+"/", http.StatusFound)
		return
	}
	// The workspace is the user's own repos PLUS every repo shared with them, so
	// their agent can read/write across all of them.
	own, _ := s.Storage.ListRepos(viewer)
	refs := make([]RepoRef, 0, len(own))
	for _, name := range own {
		refs = append(refs, RepoRef{Owner: viewer, Repo: name})
	}
	for _, sr := range s.Accounts.ReposSharedWith(viewer) {
		if s.Storage.Exists(sr.Owner, sr.Repo) {
			refs = append(refs, RepoRef{Owner: sr.Owner, Repo: sr.Repo})
		}
	}
	spriteURL, ready := s.Agent.EnsureUser(viewer, refs)
	if !ready {
		if r.URL.Path != prefix+"/" {
			http.Error(w, "agent is starting", http.StatusServiceUnavailable)
			return
		}
		// The refresh below only polls state: provisioning attempts are gated
		// by AgentManager's state machine (single-flight + backoff), so a
		// refresh — or twenty tabs of it — can never start another attempt or
		// mint another credential.
		st := s.Agent.ProvisionStatus(viewer)
		statusHTML := ""
		switch {
		case st.Running:
			label := provisionStageLabel(st.Stage)
			if st.Attempt > 1 {
				label += fmt.Sprintf(" (attempt %d)", st.Attempt)
			}
			statusHTML = `<p class="page-sub"><b>` + template.HTMLEscapeString(label) + `</b></p>`
		case st.LastError != "":
			wait := "shortly"
			if d := time.Until(st.NextRetry); d > 0 {
				wait = "in " + d.Round(time.Second).String()
			}
			statusHTML = `<p class="page-sub">The last start attempt failed: ` +
				template.HTMLEscapeString(st.LastError) +
				`</p><p class="page-sub">Retrying automatically ` + wait +
				` — or <a href="/agent/?retry=1">retry now</a>.</p>`
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html lang=en><head><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<meta http-equiv=refresh content=4>
<title>Starting your agent</title>
<link rel=stylesheet href=%[2]q>
<style>.starting{max-width:540px;margin:14vh auto;text-align:center;padding:0 1.25rem}
.spin{width:26px;height:26px;border:3px solid var(--edge);border-top-color:var(--accent);border-radius:50%%;animation:sp .9s linear infinite;margin:0 auto 1.6rem}
@keyframes sp{to{transform:rotate(360deg)}}</style></head>
<body><div class="starting"><div class="spin"></div>
<h1 class="page-title">Waking your agent…</h1>
<p class="page-sub">Setting up a private sandbox for <b>%[1]s</b> and cloning your knowledge bases. The first start takes about a minute — this page refreshes itself, then hands you to the agent.</p>
%[3]s<p style="margin-top:1.6rem"><a href="/%[1]s">← back to your dashboard</a></p></div></body></html>`,
			viewer, assetURL("style.css"), statusHTML)
		return
	}
	if route.kind == agentRouteUI {
		serveAgentUI(w, r, agentPath)
		return
	}
	// Reverse-proxy to the sprite, injecting the Sprites bearer server-side, so
	// the user stays authenticated here on the hub and never sees the sprites.dev
	// login — and the sprite stays private to our org.
	s.Agent.Proxy(w, r, spriteURL, prefix)
}

// provisionStageLabel maps internal provisioning stage names (including the
// per-repo "clone owner/repo" markers the boot script emits) to friendly
// starting-page copy.
func provisionStageLabel(stage string) string {
	if repo, ok := strings.CutPrefix(stage, "clone "); ok {
		return "Cloning " + repo + "…"
	}
	switch stage {
	case "sprite":
		return "Creating your private sandbox…"
	case "bundle":
		return "Uploading the agent…"
	case "afs":
		return "Installing tools…"
	case "credentials":
		return "Issuing credentials…"
	case "boot", "starting", "service-stop":
		return "Starting setup…"
	case "deps":
		return "Installing dependencies…"
	case "workspace":
		return "Preparing the workspace…"
	case "service-create", "done":
		return "Starting the agent service…"
	case "health":
		return "Waiting for the agent to come up…"
	}
	return "Setting up…"
}

// handleAdminMetrics renders the operator's fleet-wide model-usage view: totals
// and a per-user breakdown (cost / tokens / errors) over a window (default 24h)
// and all-time, from the metrics the LLM proxy records. JSON via ?format=json.
func (s *Server) handleAdminMetrics(w http.ResponseWriter, r *http.Request) {
	if s.Metrics == nil {
		http.Error(w, "metrics are not enabled on this hub", http.StatusServiceUnavailable)
		return
	}
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n > 0 {
			hours = n
		}
	}
	window, err := s.Metrics.Summary(hours)
	if err != nil {
		s.Log.Printf("metrics summary: %v", err)
		http.Error(w, "metrics error", http.StatusInternalServerError)
		return
	}
	allTime, _ := s.Metrics.Summary(24 * 3650) // ~10y = effectively all-time

	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]any{"windowHours": hours, "window": window, "allTime": allTime})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang=en><head><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<title>Hub metrics</title><link rel=stylesheet href=%q>
<style>html{color-scheme:light dark}.m{max-width:920px;margin:2rem auto;padding:0 1.25rem}
table{border-collapse:collapse;width:100%%;margin:.5rem 0 2rem}
th,td{text-align:left;padding:.45rem .65rem;border-bottom:1px solid var(--edge)}
td.n,th.n{text-align:right;font-variant-numeric:tabular-nums}
.cards{display:flex;gap:1rem;flex-wrap:wrap;margin:1rem 0 1.5rem}
.card{background:var(--paper-2);border:1px solid var(--edge);border-radius:10px;padding:.7rem 1.1rem;min-width:6rem}
.card b{display:block;font-size:1.5rem;line-height:1.1;color:var(--ink)}.card span{color:var(--muted);font-size:.8rem}</style>
</head><body><div class=m>
<h1 class="page-title">Model usage</h1>
<p class="page-sub"><a href="/admin/access">Access &amp; waitlist</a> · Metered at the hub LLM proxy across every agent sprite. <a href="?hours=24">24h</a> · <a href="?hours=168">7d</a> · <a href="?format=json">JSON</a></p>`,
		assetURL("style.css"))

	card := func(label string, sm MetricsSummary) {
		fmt.Fprintf(w, `<h2>%s</h2><div class=cards>
<div class=card><b>%s</b><span>calls</span></div>
<div class=card><b>$%.2f</b><span>cost</span></div>
<div class=card><b>%s</b><span>tokens (in+out)</span></div>
<div class=card><b>%d</b><span>errors</span></div></div>`,
			template.HTMLEscapeString(label), humanInt(sm.TotalCalls), sm.TotalCost, humanInt(sm.TotalInput+sm.TotalOutput), sm.Errors)
	}
	card(fmt.Sprintf("Last %dh", hours), window)
	card("All-time", allTime)

	fmt.Fprintf(w, `<h2>By user (last %dh)</h2><table>
<tr><th>User</th><th class=n>Calls</th><th class=n>In tok</th><th class=n>Out tok</th><th class=n>Cost</th><th class=n>Errors</th></tr>`, hours)
	if len(window.Users) == 0 {
		fmt.Fprint(w, `<tr><td colspan=6>No model calls in this window yet.</td></tr>`)
	}
	for _, u := range window.Users {
		fmt.Fprintf(w, `<tr><td>%s</td><td class=n>%d</td><td class=n>%s</td><td class=n>%s</td><td class=n>$%.3f</td><td class=n>%d</td></tr>`,
			template.HTMLEscapeString(u.User), u.Calls, humanInt(u.InputTokens), humanInt(u.OutputTokens), u.CostUSD, u.Errors)
	}
	fmt.Fprint(w, `</table></div></body></html>`)
}

func humanInt(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return strconv.Itoa(n)
	}
}

// handleAdminAccess is the operator's signup gate: it lists the waitlist and the
// allowlist and lets the admin admit people (waitlist → allowlist), add an email
// directly, dismiss a request, or revoke access. An empty allowlist means signup
// is open to anyone; the first email added flips the hub to invite-only.
func (s *Server) handleAdminAccess(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil {
		http.Error(w, "accounts are not enabled on this hub", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodPost {
		email := strings.TrimSpace(r.FormValue("email"))
		switch r.FormValue("action") {
		case "admit": // waitlist → allowlist
			if email != "" {
				s.Accounts.AllowEmail(email)
				s.Accounts.RemoveFromWaitlist(email)
			}
		case "allow": // add straight to the allowlist
			if looksLikeEmail(email) {
				s.Accounts.AllowEmail(email)
			}
		case "dismiss": // drop a waitlist request without admitting
			s.Accounts.RemoveFromWaitlist(email)
		case "revoke": // remove from the allowlist
			s.Accounts.RemoveAllowed(email)
		}
		http.Redirect(w, r, "/admin/access", http.StatusSeeOther)
		return
	}

	allow := s.Accounts.ListAllowlist()
	wait := s.Accounts.ListWaitlist()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang=en><head><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<title>Access &amp; waitlist</title><link rel=stylesheet href=%q>
<style>html{color-scheme:light dark}.m{max-width:760px;margin:2rem auto;padding:0 1.25rem}
table{border-collapse:collapse;width:100%%;margin:.5rem 0 1.5rem}
th,td{text-align:left;padding:.45rem .65rem;border-bottom:1px solid var(--edge);vertical-align:middle}
form.inline{display:inline;margin:0}
.banner{background:var(--paper-2);border:1px solid var(--edge);border-radius:10px;padding:.7rem 1rem;margin:1rem 0;color:var(--ink)}
.addform{display:flex;gap:.5rem;margin:.5rem 0 2rem;flex-wrap:wrap}
.addform input{flex:1;min-width:14rem;padding:.45rem .6rem;border:1px solid var(--edge);border-radius:8px;background:var(--paper-2);color:var(--ink)}
button.mini{padding:.3rem .7rem;border:1px solid var(--edge);border-radius:7px;background:var(--paper-2);color:var(--ink);cursor:pointer;font-size:.85rem}
button.admit{background:#18c987;color:#04150f;border-color:#12b075}</style>
</head><body><div class=m>
<h1 class="page-title">Access &amp; waitlist</h1>
<p class="page-sub"><a href="/admin/metrics">← Model usage</a></p>`, assetURL("style.css"))

	if len(allow) == 0 {
		fmt.Fprint(w, `<div class=banner><b>Signup is open to anyone.</b> Add an email (or admit someone) to switch the hub to invite-only — after that, non-invited signups go to the waitlist.</div>`)
	} else {
		fmt.Fprintf(w, `<div class=banner><b>Invite-only.</b> %d email(s) may create an account; everyone else joins the waitlist.</div>`, len(allow))
	}

	fmt.Fprintf(w, `<h2>Waitlist (%d)</h2><table>
<tr><th>Email</th><th>Wanted username</th><th>Requested</th><th></th></tr>`, len(wait))
	if len(wait) == 0 {
		fmt.Fprint(w, `<tr><td colspan=4>Nobody waiting.</td></tr>`)
	}
	for _, e := range wait {
		em := template.HTMLEscapeString(e.Email)
		fmt.Fprintf(w, `<tr><td>%s</td><td>%s</td><td>%s</td><td>`+
			`<form class=inline method=post action="/admin/access"><input type=hidden name=action value=admit><input type=hidden name=email value="%s"><button class="mini admit">Admit</button></form> `+
			`<form class=inline method=post action="/admin/access"><input type=hidden name=action value=dismiss><input type=hidden name=email value="%s"><button class=mini>Dismiss</button></form>`+
			`</td></tr>`,
			em, template.HTMLEscapeString(e.Username), ageString(e.CreatedAt), em, em)
	}

	fmt.Fprintf(w, `<h2>Allowlist (%d)</h2>
<form class=addform method=post action="/admin/access"><input type=hidden name=action value=allow><input name=email type=email placeholder="invite@example.com" required><button class="mini admit" type=submit>Add email</button></form>
<table><tr><th>Email</th><th></th></tr>`, len(allow))
	if len(allow) == 0 {
		fmt.Fprint(w, `<tr><td colspan=2>Empty — signup is open.</td></tr>`)
	}
	for _, e := range allow {
		em := template.HTMLEscapeString(e)
		fmt.Fprintf(w, `<tr><td>%s</td><td><form class=inline method=post action="/admin/access"><input type=hidden name=action value=revoke><input type=hidden name=email value="%s"><button class=mini>Remove</button></form></td></tr>`, em, em)
	}
	fmt.Fprint(w, `</table></div></body></html>`)
}

type backlinkView struct{ Name, Desc, Href string }
type commitView struct{ Hash, Short, Subject, Author, When string }

type diffLine struct {
	Kind string
	Mark string
	Text string
}

type historyDiff struct {
	Hash, Short, Subject, Author, When string
	Lines                              []diffLine
}

type fileData struct {
	baseData
	Repo, Path, Name, Description, Age     string
	Head                                   string // HEAD commit the page was rendered at (review comments anchor to it)
	ContentType, PreviewKind, SizeLabel    string
	IsMarkdown, IsText, TooLarge, CanWrite bool
	CanExport                              bool
	BodyHTML                               template.HTML
	RawText, RawHref, DownloadHref         string
	SelectedHash                           string
	Selected                               *historyDiff
	Backlinks                              []backlinkView
	History                                []commitView
	Tree                                   *treeNode // repo file tree for the left nav
}

const maxLFSPointerBytes int64 = 1024

func (s *Server) renderFile(w http.ResponseWriter, r *http.Request, user, repo, filePath, viewer string) {
	bare := s.Storage.RepoDir(user, repo)
	size, ok := BlobSize("git", bare, defaultRef, filePath)
	if !ok {
		http.NotFound(w, r)
		return
	}
	view, err := s.repoView(user, repo)
	if err != nil {
		view = &repoView{} // still render the note; just without tree/backlinks
	}
	files := view.Files
	paths := make([]string, 0, len(files))
	pathSet := make(map[string]struct{}, len(files))
	var ageUnix int64
	for _, f := range files {
		paths = append(paths, f.Path)
		pathSet[f.Path] = struct{}{}
		if f.Path == filePath {
			ageUnix = f.LastCommit
		}
	}
	idx := core.NewNameIndex(paths)

	data := fileData{
		baseData:     baseData{User: user, Viewer: viewer, Crumbs: []crumb{{user, "/" + user}, {repo, "/" + user + "/" + repo}, {pathBase(filePath), ""}}, FileView: true, AgentURL: s.agentPath(user, repo, viewer)},
		Repo:         repo,
		Path:         filePath,
		Name:         pathBase(filePath),
		Head:         strings.TrimSpace(mustGitHead(bare)),
		Age:          ageString(ageUnix),
		ContentType:  fileContentType(filePath),
		PreviewKind:  filePreviewKind(filePath),
		SizeLabel:    formatFileSize(size),
		RawHref:      "/" + user + "/" + repo + "/raw/" + filePath,
		DownloadHref: "/" + user + "/" + repo + "/download/" + filePath,
		IsMarkdown:   strings.EqualFold(path.Ext(filePath), ".md"),
		CanWrite:     s.canWrite(user, repo, viewer),
	}

	// Left-nav file tree: reuse the repo landing page's tree, with the current
	// note highlighted. Same data already loaded above — no extra git calls.
	if len(files) > 0 {
		data.Tree = buildTree(files, user, repo)
		markCurrent(data.Tree, filePath)
	}

	// Only read text that is valid UTF-8 and not enormous. Media gets a preview
	// without being buffered; bigger or binary files link to /raw instead.
	const maxRenderBytes = 1 << 20 // 1 MiB
	if data.PreviewKind != "" && size <= maxLFSPointerBytes && s.LFS != nil {
		if content, contentOK := BlobContent("git", bare, defaultRef, filePath); contentOK {
			if ptr, isPtr := ParseLFSPointer(content); isPtr {
				data.SizeLabel = formatFileSize(ptr.Size)
			}
		}
	}
	if data.PreviewKind == "" && size <= maxRenderBytes {
		content, contentOK := BlobContent("git", bare, defaultRef, filePath)
		if !contentOK {
			http.NotFound(w, r)
			return
		}
		renderable := utf8.ValidString(content) && !strings.ContainsRune(content, 0)
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
			resolveImage := func(target string) (string, bool) {
				u, err := url.Parse(strings.TrimSpace(target))
				if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "#") {
					return "", false
				}
				rel := path.Clean(path.Join(path.Dir(filePath), u.Path))
				if !validRepoPath(rel) {
					return "", false
				}
				if _, ok := pathSet[rel]; !ok {
					return "", false
				}
				raw := &url.URL{Path: "/" + user + "/" + repo + "/raw/" + rel, RawQuery: u.RawQuery, Fragment: u.Fragment}
				return raw.String(), true
			}
			if html, err := renderMarkdown(content, resolve, resolveImage); err == nil {
				data.BodyHTML = template.HTML(html)
			}
			// Backlinks come from the cached wikilink graph (one entry per source).
			for _, n := range graphBacklinks(view.Graph, filePath) {
				data.Backlinks = append(data.Backlinks, backlinkView{Name: n.Path, Desc: n.Desc, Href: n.Href})
			}
		} else if renderable {
			data.IsText = true
			data.RawText = content
		}
	} else if data.PreviewKind == "" {
		data.TooLarge = true
	}
	data.CanExport = (data.IsMarkdown || data.IsText) && size <= maxExportBytes

	requestedCommit := strings.TrimSpace(r.URL.Query().Get("commit"))
	for _, c := range RepoLogPath("git", bare, defaultRef, filePath, 8) {
		selected := strings.EqualFold(requestedCommit, c.Hash) || strings.EqualFold(requestedCommit, c.Short)
		data.History = append(data.History, commitView{Hash: c.Hash, Short: c.Short, Subject: c.Subject, Author: c.Author, When: ageString(c.When)})
		if selected {
			data.SelectedHash = c.Hash
			data.Selected = &historyDiff{Hash: c.Hash, Short: c.Short, Subject: c.Subject, Author: c.Author, When: ageString(c.When)}
			if raw, ok := CommitDiffPath("git", bare, c.Hash, filePath); ok {
				data.Selected.Lines = parseDiffLines(raw)
			}
		}
	}
	s.renderPage(w, r, "file", data)
}

func (s *Server) handleRaw(w http.ResponseWriter, user, repo, filePath string) {
	bare := s.Storage.RepoDir(user, repo)
	size, ok := BlobSize("git", bare, defaultRef, filePath)
	if !ok {
		http.NotFound(w, nil)
		return
	}

	// LFS pointers are tiny, so inspect only tiny Git blobs. Regular media and
	// large files go straight from git to the response without buffering.
	if size <= maxLFSPointerBytes {
		if content, contentOK := BlobContent("git", bare, defaultRef, filePath); contentOK {
			if ptr, isPtr := ParseLFSPointer(content); isPtr && s.LFS != nil {
				rc, objectSize, err := s.LFS.Open(user, repo, ptr.OID, ptr.Size)
				if err != nil {
					http.Error(w, "LFS object is missing from this hub.", http.StatusNotFound)
					return
				}
				defer rc.Close()
				setRawHeaders(w, filePath, true)
				w.Header().Set("Content-Length", strconv.FormatInt(objectSize, 10))
				io.Copy(w, rc)
				return
			}
			if shouldServeRawAsText(filePath, content) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Header().Set("Content-Length", strconv.FormatInt(int64(len(content)), 10))
				w.Write([]byte(content))
				return
			}
		}
	}

	setRawHeaders(w, filePath, true)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if err := StreamBlob("git", bare, defaultRef, filePath, w); err != nil {
		return
	}
}

func fileContentType(filePath string) string {
	ext := strings.ToLower(path.Ext(filePath))
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	// Keep previews useful on hosts whose /etc/mime.types is sparse.
	switch ext {
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	}
	return ""
}

func filePreviewKind(filePath string) string {
	ct := fileContentType(filePath)
	switch {
	case strings.HasPrefix(ct, "image/") && safeInlineRawType(filePath, ct):
		return "image"
	case strings.HasPrefix(ct, "audio/") && safeInlineRawType(filePath, ct):
		return "audio"
	case strings.HasPrefix(ct, "video/") && safeInlineRawType(filePath, ct):
		return "video"
	case ct == "application/pdf":
		return "pdf"
	default:
		return ""
	}
}

func formatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(size)
	for _, unit := range units {
		value /= 1024
		if value < 1024 || unit == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%d B", size)
}

func shouldServeRawAsText(filePath, content string) bool {
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return false
	}
	ct := fileContentType(filePath)
	if strings.HasPrefix(ct, "text/") || ct == "application/json" || ct == "application/xml" || ct == "application/javascript" {
		return true
	}
	// Unknown extensions retain the old, useful behavior: valid UTF-8 is a
	// text response unless the extension identifies a known binary type.
	return ct == ""
}

func setRawHeaders(w http.ResponseWriter, filePath string, attachUnknown bool) {
	ct := fileContentType(filePath)
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if attachUnknown && !safeInlineRawType(filePath, ct) && w.Header().Get("Content-Disposition") == "" {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+dispositionName(pathBase(filePath))+"\"")
	}
}

func safeInlineRawType(filePath, contentType string) bool {
	switch strings.ToLower(path.Ext(filePath)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif":
		return strings.HasPrefix(contentType, "image/")
	case ".mp3", ".m4a", ".wav", ".flac", ".ogg", ".oga", ".aac":
		return strings.HasPrefix(contentType, "audio/")
	case ".mp4", ".m4v", ".webm", ".mov", ".ogv":
		return strings.HasPrefix(contentType, "video/")
	case ".pdf":
		return contentType == "application/pdf"
	default:
		return false
	}
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
	Repo         string
	Commits      []commitView
	SelectedHash string
	Selected     *historyDiff
}

func (s *Server) renderHistory(w http.ResponseWriter, r *http.Request, user, repo, viewer string) {
	data := historyData{
		baseData: baseData{User: user, Viewer: viewer, Crumbs: []crumb{{user, "/" + user}, {repo, "/" + user + "/" + repo}, {"history", ""}}},
		Repo:     repo,
	}
	commits := RepoLog("git", s.Storage.RepoDir(user, repo), defaultRef, 100)
	requested := strings.TrimSpace(r.URL.Query().Get("commit"))
	for _, c := range commits {
		selected := strings.EqualFold(requested, c.Hash) || strings.EqualFold(requested, c.Short)
		data.Commits = append(data.Commits, commitView{Hash: c.Hash, Short: c.Short, Subject: c.Subject, Author: c.Author, When: ageString(c.When)})
		if selected {
			data.SelectedHash = c.Hash
			if raw, ok := CommitDiff("git", s.Storage.RepoDir(user, repo), c.Hash); ok {
				data.Selected = &historyDiff{
					Hash: c.Hash, Short: c.Short, Subject: c.Subject,
					Author: c.Author, When: ageString(c.When), Lines: parseDiffLines(raw),
				}
			}
		}
	}
	s.renderPage(w, r, "history", data)
}

func parseDiffLines(raw string) []diffLine {
	raw = strings.TrimSuffix(raw, "\n")
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	parsed := make([]diffLine, 0, len(lines))
	for _, line := range lines {
		kind, mark := "context", " "
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			kind, mark = "meta", "·"
		case strings.HasPrefix(line, "+"):
			kind, mark = "add", "+"
		case strings.HasPrefix(line, "-"):
			kind, mark = "remove", "−"
		case strings.HasPrefix(line, "@@"):
			kind, mark = "hunk", "·"
		case strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") || strings.HasPrefix(line, "new file") || strings.HasPrefix(line, "deleted file") || strings.HasPrefix(line, "Binary files"):
			kind, mark = "meta", "·"
		}
		parsed = append(parsed, diffLine{Kind: kind, Mark: mark, Text: line})
	}
	return parsed
}

func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Compress rendered pages: a large knowledge base's tree + graph is ~1 MB of
	// highly repetitive HTML that gzips ~10×. BestSpeed keeps the CPU cost tiny.
	var out io.Writer = w
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
		defer gz.Close()
		out = gz
	}
	root := "base"
	if name == "redesign" || name == "redesign-v2" {
		root = name
	}
	if err := pages[name].ExecuteTemplate(out, root, data); err != nil {
		s.Log.Printf("render %s: %v", name, err)
	}
}

// repoMeta returns a repo's root description, note count, and freshest commit
// time for dashboard/header display. It reads from the per-repo view cache,
// so a dashboard of many repos costs one rev-parse per repo, not a re-scan.
func (s *Server) repoMeta(user, repo string) (desc string, notes int, ageUnix int64) {
	view, err := s.repoView(user, repo)
	if err != nil {
		return "", 0, 0
	}
	return repoFilesMeta(view.Files)
}

// repoFilesMeta derives the header metadata from a snapshot: the root
// AGENTS.md (or README.md) description, note count, and freshest commit.
func repoFilesMeta(files []RepoFile) (desc string, notes int, ageUnix int64) {
	for _, f := range files {
		if strings.EqualFold(path.Ext(f.Path), ".md") {
			notes++
		}
		if f.LastCommit > ageUnix {
			ageUnix = f.LastCommit
		}
	}
	for _, name := range []string{"AGENTS.md", "README.md"} {
		for _, f := range files {
			if f.Path == name && f.Description != "" {
				return cleanDesc(f.Description), notes, ageUnix
			}
		}
	}
	return "", notes, ageUnix
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
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
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
