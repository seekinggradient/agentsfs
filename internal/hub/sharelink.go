package hub

import (
	"html/template"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"

	"agentsfs.ai/afs/internal/core"
)

// Public share links let a repo owner publish ONE file at an unguessable URL
// while the repo stays private everywhere else. The token is the entire
// credential, so the routes here run with no viewer identity at all — what a
// token may serve is recomputed from the record and the repo's current HEAD on
// every request, never frozen at mint time. That is what makes a share track
// content updates and makes revocation immediate.

// sharePrefix is the flat, user-namespace-free route space share links live in
// ("s" is a reservedName, so it can never be a username).
const sharePrefix = "/s/"

// maxShareTextBytes bounds what a shared page will render as text; anything
// larger is offered as a download instead, matching the file page's limit.
const maxShareTextBytes = 1 << 20

// shareScope is what one token may serve at the current HEAD: the pages
// reachable under /p/ and the media reachable under /a/. Both are empty for a
// root file that isn't markdown — there is nothing to walk.
type shareScope struct {
	Pages  map[string]bool
	Assets map[string]bool
}

// setShareHeaders marks every /s/ response uncrawlable and unsniffable and
// keeps the referrer off the (private) hub URL space the reader came from.
func setShareHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

// shareNotFound is the single failure response for the whole /s/ space:
// unknown token, revoked token, uncovered path, or a file deleted at HEAD all
// look identical, so a probe learns nothing from the difference.
func shareNotFound(w http.ResponseWriter) {
	setShareHeaders(w)
	http.Error(w, "not found", http.StatusNotFound)
}

// handleShared serves the public share routes: /s/<token>, /s/<token>/p/<path>
// (a linked page), and /s/<token>/a/<path> (embedded media). No authentication
// runs here — possession of the token is the authorization.
func (s *Server) handleShared(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		setShareHeaders(w)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var segs []string
	for _, p := range strings.Split(strings.TrimPrefix(r.URL.Path, sharePrefix), "/") {
		if p != "" {
			segs = append(segs, p)
		}
	}
	if len(segs) == 0 {
		shareNotFound(w)
		return
	}
	rec, ok := s.Accounts.LookupShareLink(segs[0])
	if !ok || !s.Storage.Exists(rec.Owner, rec.Repo) {
		shareNotFound(w)
		return
	}
	token := segs[0]
	scope := s.shareScope(rec)

	switch {
	case len(segs) == 1:
		s.serveSharedFile(w, r, token, rec, scope, rec.Path)
	case segs[1] == "p" && len(segs) > 2:
		linked := strings.Join(segs[2:], "/")
		if !validRepoPath(linked) || !rec.IncludeLinked || !scope.Pages[linked] {
			shareNotFound(w)
			return
		}
		s.serveSharedFile(w, r, token, rec, scope, linked)
	case segs[1] == "a" && len(segs) > 2:
		asset := strings.Join(segs[2:], "/")
		// Double gate. Membership in the embed set alone is not enough: a note
		// can embed any path, so without the type check an image reference to
		// NOTE.md would publish that note as a bonus. safeInlineRawType is the
		// same allowlist /raw inlines by (image/audio/video/pdf), which never
		// includes HTML or markdown — so /a/ can only ever serve media.
		if !validRepoPath(asset) || !scope.Assets[asset] || !safeInlineRawType(asset, fileContentType(asset)) {
			shareNotFound(w)
			return
		}
		s.serveSharedAsset(w, rec, asset)
	default:
		shareNotFound(w)
	}
}

// serveSharedFile renders or streams one covered file, by type: the file itself
// for `?download=1`, a live sandboxed document for HTML, the Markdown To
// rendering for a note that declares an envelope (with `?view=markdown` as its
// escape hatch), the public reading chrome for other markdown and text, inline
// media for what /raw would inline, and a download for the rest.
func (s *Server) serveSharedFile(w http.ResponseWriter, r *http.Request, token string, rec *ShareLink, scope shareScope, filePath string) {
	bare := s.Storage.RepoDir(rec.Owner, rec.Repo)
	size, ok := BlobSize("git", bare, defaultRef, filePath)
	if !ok {
		shareNotFound(w)
		return
	}

	// "Download the file" survives distribution, for every covered file and
	// whatever view it normally gets. The reader of a shared link is never
	// captive to the Hub's rendering of it — that is the promise the whole
	// project rests on, and a share link is where it would be easiest to quietly
	// drop. Served as an opaque attachment, so no repo-authored bytes are ever
	// interpreted on this origin.
	if r.URL.Query().Get("download") != "" {
		served := s.serveRepoBlob(w, rec.Owner, rec.Repo, filePath, func(w http.ResponseWriter) {
			setShareHeaders(w)
			h := w.Header()
			h.Set("Content-Type", "application/octet-stream")
			h.Set("Content-Disposition", "attachment; filename=\""+dispositionName(pathBase(filePath))+"\"")
		})
		if !served {
			shareNotFound(w)
		}
		return
	}

	if htmlRenderable(filePath) {
		// Identical serving path to /render: the opaque-origin sandbox is what
		// makes repo-authored HTML safe to hand to an anonymous browser.
		served := s.serveRepoBlob(w, rec.Owner, rec.Repo, filePath, func(w http.ResponseWriter) {
			setShareHeaders(w)
			setHTMLRenderHeaders(w)
		})
		if !served {
			shareNotFound(w)
		}
		return
	}

	if ct := fileContentType(filePath); safeInlineRawType(filePath, ct) {
		s.serveSharedAsset(w, rec, filePath)
		return
	}

	if size <= maxShareTextBytes {
		if content, contentOK := BlobContent("git", bare, defaultRef, filePath); contentOK &&
			utf8.ValidString(content) && !strings.ContainsRune(content, 0) {
			data := sharedPageData{
				Title:        pathBase(filePath),
				DownloadHref: sharedFileURL(token, rec.Path, filePath) + "?download=1",
			}
			if markdownPath(filePath) {
				// A conforming document IS its rendered view here: the share link
				// is how a little app is distributed (hub-contract §4), and a
				// reader who follows one should meet the board, not its source.
				// `?view=markdown` is the escape hatch, always one click away on
				// the rendered page, so the rendering never captures the file.
				data.MdtoEnvelope = mdtoEnvelope(filePath, content)
				if data.MdtoEnvelope != "" {
					if r.URL.Query().Get("view") != "markdown" {
						s.serveSharedMdto(w, r, token, rec, filePath, data.MdtoEnvelope, content)
						return
					}
					data.MdtoHref = sharedFileURL(token, rec.Path, filePath)
				}
				body, err := s.renderSharedMarkdown(content, token, rec, scope, filePath)
				if err != nil {
					s.Log.Printf("render shared markdown %s/%s %s: %v", rec.Owner, rec.Repo, filePath, err)
					shareNotFound(w)
					return
				}
				data.BodyHTML = body
			} else {
				data.IsText = true
				data.RawText = content
			}
			setShareHeaders(w)
			s.renderPage(w, r, "share", data)
			return
		}
	}

	// Anything left is binary, oversized, or a type the hub will not inline:
	// hand it over as an opaque download rather than letting the browser decide
	// what to do with repo-authored bytes on this origin.
	served := s.serveRepoBlob(w, rec.Owner, rec.Repo, filePath, func(w http.ResponseWriter) {
		setShareHeaders(w)
		h := w.Header()
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", "attachment; filename=\""+dispositionName(pathBase(filePath))+"\"")
	})
	if !served {
		shareNotFound(w)
	}
}

// serveSharedAsset streams media inline. Callers must have established that the
// path is inline-safe (safeInlineRawType) — this only sets headers and streams.
func (s *Server) serveSharedAsset(w http.ResponseWriter, rec *ShareLink, filePath string) {
	served := s.serveRepoBlob(w, rec.Owner, rec.Repo, filePath, func(w http.ResponseWriter) {
		setShareHeaders(w)
		ct := fileContentType(filePath)
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
	})
	if !served {
		shareNotFound(w)
	}
}

// sharedPageData backs the standalone public chrome (assets/share.html) — no
// masthead, no nav, no viewer identity, nothing that assumes a hub session.
type sharedPageData struct {
	Title    string
	BodyHTML template.HTML
	RawText  string
	IsText   bool
	// Set when this note declares a `markdownto:` envelope and the reader
	// deliberately asked for the plain markdown instead: the way back to the
	// rendered view, so the escape hatch is a detour and not a one-way door.
	MdtoEnvelope, MdtoHref string
	DownloadHref           string
}

// sharedFileURL is the public URL a covered file is served at: the token itself
// for the share's root file, and the /p/ space for a linked page.
func sharedFileURL(token, rootPath, filePath string) string {
	if filePath == rootPath {
		return sharePrefix + token
	}
	return sharePrefix + token + "/p/" + filePath
}

// serveSharedMdto is the anonymous half of the Markdown To rendering: the same
// thin page the authenticated file view serves, with every link pointed back
// into this token's own URL space. No viewer identity is involved anywhere —
// possession of the token is the whole authorization, exactly as for the plain
// shared page.
func (s *Server) serveSharedMdto(w http.ResponseWriter, r *http.Request, token string, rec *ShareLink, filePath, envelope, content string) {
	base := sharedFileURL(token, rec.Path, filePath)
	data := newMdtoPageData(pathBase(filePath), envelope, content)
	data.MarkdownHref = base + "?view=markdown"
	data.DownloadHref = base + "?download=1"

	// setMdtoHeaders is a superset of setShareHeaders (same noindex, nosniff,
	// no-referrer, no-store) plus the page's CSP.
	setMdtoHeaders(w)
	s.renderPage(w, r, "mdto", data)
}

// renderSharedMarkdown renders a covered note into the public chrome with every
// URL rewritten for the token's own space: embedded media through /a/, links to
// covered pages through /p/, and links to anything else back to the ordinary
// (still access-controlled) hub URL.
func (s *Server) renderSharedMarkdown(content, token string, rec *ShareLink, scope shareScope, filePath string) (template.HTML, error) {
	view, err := s.repoView(rec.Owner, rec.Repo)
	if err != nil {
		view = &repoView{}
	}
	pathSet := repoPathSet(view.Files)
	paths := make([]string, 0, len(view.Files))
	for _, f := range view.Files {
		paths = append(paths, f.Path)
	}
	idx := core.NewNameIndex(paths)

	pageURL := func(target string, u *url.URL) string {
		if scope.Pages[target] {
			return (&url.URL{Path: sharePrefix + token + "/p/" + target, RawQuery: u.RawQuery, Fragment: u.Fragment}).String()
		}
		return (&url.URL{Path: "/" + rec.Owner + "/" + rec.Repo + "/blob/" + target, RawQuery: u.RawQuery, Fragment: u.Fragment}).String()
	}

	opt := markdownOptions{
		resolveWiki: func(target string) (string, bool) {
			m := idx.Resolve(target)
			if len(m) == 0 {
				return "", false
			}
			return pageURL(m[0], &url.URL{}), true
		},
		resolveLink: func(target string) (string, bool) {
			rel, u, ok := resolveRepoRelative(filePath, target, pathSet)
			if !ok {
				return "", false
			}
			return pageURL(rel, u), true
		},
		resolveImage: func(target string) (string, bool) {
			rel, u, ok := resolveRepoRelative(filePath, target, pathSet)
			if !ok || !scope.Assets[rel] || !safeInlineRawType(rel, fileContentType(rel)) {
				return "", false
			}
			return (&url.URL{Path: sharePrefix + token + "/a/" + rel, RawQuery: u.RawQuery, Fragment: u.Fragment}).String(), true
		},
	}
	html, err := renderMarkdownWith(content, opt)
	if err != nil {
		return "", err
	}
	return template.HTML(html), nil
}

// shareScope computes, at the current HEAD, everything the token may serve
// beyond its root file. A root that isn't markdown has no scope: there is no
// link or embed syntax to walk, and the HTML route is sandboxed to an opaque
// origin where relative subresources would not load anyway.
func (s *Server) shareScope(rec *ShareLink) shareScope {
	scope := shareScope{Pages: map[string]bool{}, Assets: map[string]bool{}}
	if !markdownPath(rec.Path) {
		return scope
	}
	view, err := s.repoView(rec.Owner, rec.Repo)
	if err != nil {
		return scope
	}
	links, assets := s.shareTargets(view, rec.Owner, rec.Repo, rec.Path)
	for _, a := range assets {
		scope.Assets[a] = true
	}
	if !rec.IncludeLinked {
		return scope
	}
	// The root itself is reachable under /p/ too, so a linked page's link back
	// to it stays inside the share instead of bouncing to the private hub URL.
	scope.Pages[rec.Path] = true
	for _, l := range links {
		scope.Pages[l] = true
	}
	for _, l := range links {
		if !markdownPath(l) || l == rec.Path {
			continue
		}
		_, linkedAssets := s.shareTargets(view, rec.Owner, rec.Repo, l)
		for _, a := range linkedAssets {
			scope.Assets[a] = true
		}
	}
	return scope
}

// shareTargets reads filePath at HEAD and returns its one-hop outbound links to
// other repo files and the repo files it embeds as media, both as paths that
// exist at HEAD. Markdown links resolve relative to the file's directory;
// [[wikilinks]] resolve through core's name index, exactly as the hub's link
// graph and the rendered page do.
func (s *Server) shareTargets(view *repoView, user, repo, filePath string) (links, assets []string) {
	content, ok := BlobContent("git", s.Storage.RepoDir(user, repo), defaultRef, filePath)
	if !ok {
		return nil, nil
	}
	pathSet := repoPathSet(view.Files)
	paths := make([]string, 0, len(view.Files))
	for _, f := range view.Files {
		paths = append(paths, f.Path)
	}
	idx := core.NewNameIndex(paths)

	linkSet := map[string]bool{}
	assetSet := map[string]bool{}
	source := []byte(stripFrontmatter(content))
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(source))
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Image:
			if rel, _, ok := resolveRepoRelative(filePath, string(node.Destination), pathSet); ok {
				assetSet[rel] = true
			}
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			if rel, _, ok := resolveRepoRelative(filePath, string(node.Destination), pathSet); ok && rel != filePath {
				linkSet[rel] = true
			}
		}
		return ast.WalkContinue, nil
	})
	for _, l := range core.ScanLinksIn(filePath, content) {
		m := idx.Resolve(l.Target)
		if len(m) > 0 && m[0] != filePath {
			linkSet[m[0]] = true
		}
	}
	return sortedKeys(linkSet), sortedKeys(assetSet)
}

// resolveRepoRelative maps a markdown target written relative to fromPath onto
// a repo path that exists at HEAD, returning the parsed target so the caller
// can carry its query/fragment onto the rewritten URL. Absolute, external, and
// anchor-only targets belong to the document, not the repo, and are rejected.
func resolveRepoRelative(fromPath, target string, pathSet map[string]struct{}) (string, *url.URL, bool) {
	target = strings.TrimSpace(target)
	u, err := url.Parse(target)
	if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" ||
		strings.HasPrefix(target, "/") || strings.HasPrefix(target, "#") {
		return "", nil, false
	}
	rel := path.Clean(path.Join(path.Dir(fromPath), u.Path))
	if !validRepoPath(rel) {
		return "", nil, false
	}
	if _, ok := pathSet[rel]; !ok {
		return "", nil, false
	}
	return rel, u, true
}

func repoPathSet(files []RepoFile) map[string]struct{} {
	set := make(map[string]struct{}, len(files))
	for _, f := range files {
		set[f.Path] = struct{}{}
	}
	return set
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func markdownPath(p string) bool { return strings.EqualFold(path.Ext(p), ".md") }

// ---- owner-side management ----

// shareLinkView is one live link as the owner sees it. The raw token is not
// stored, so a row can only ever identify a link by what it publishes and when
// it was minted — never by its URL.
type shareLinkView struct {
	ID            int64
	Path          string
	Name          string
	Href          string // the file's ordinary (access-controlled) hub page
	URL           string // the public /s/ URL; "" for rows minted before tokens were stored
	Created       string
	IncludeLinked bool
}

// shareLinkRows renders records for the two owner-side lists: the per-file
// share page and the repo's settings page, which lists every published file.
// base is the absolute hub origin (hubBase), so rows carry a copyable URL.
func shareLinkRows(base string, links []ShareLink) []shareLinkView {
	out := make([]shareLinkView, 0, len(links))
	for _, l := range links {
		v := shareLinkView{
			ID:            l.ID,
			Path:          l.Path,
			Name:          pathBase(l.Path),
			Href:          "/" + l.Owner + "/" + l.Repo + "/blob/" + l.Path,
			Created:       time.Unix(l.CreatedAt, 0).UTC().Format("2 Jan 2006"),
			IncludeLinked: l.IncludeLinked,
		}
		if l.Token != "" {
			v.URL = base + sharePrefix + l.Token
		}
		out = append(out, v)
	}
	return out
}

type shareManageData struct {
	baseData
	Repo, Path, Name string
	IsMarkdown       bool
	LinkedPages      []string
	Links            []shareLinkView
	NewURL           string
	Notice, Error    string
}

// handleShareLinks is the owner's share management page for one file: mint a
// link, see what a link with "include linked pages" would currently expose, and
// revoke. serveWeb already established that the caller owns the repo.
func (s *Server) handleShareLinks(w http.ResponseWriter, r *http.Request, user, repo, filePath, viewer string) {
	// Deliberately session-only, like delete-repo: webUser (which authorized
	// this route) also accepts a PAT, and PATs live on remote agent VMs.
	// Publishing private knowledge to an anonymous URL is precisely what a
	// prompt-injected agent would be steered into doing, so the human has to be
	// sitting at the browser.
	if u, ok := s.webSessionUser(r); !ok || u != user {
		http.Error(w, "Share links must be managed from the web app while signed in.", http.StatusForbidden)
		return
	}
	if _, ok := BlobSize("git", s.Storage.RepoDir(user, repo), defaultRef, filePath); !ok {
		s.renderNotFound(w, r, viewer, user, repo)
		return
	}

	var newURL string
	render := func(notice, errMsg string) {
		data := shareManageData{
			baseData: baseData{User: user, Viewer: viewer, Crumbs: []crumb{
				{user, "/" + user}, {repo, "/" + user + "/" + repo},
				{pathBase(filePath), "/" + user + "/" + repo + "/blob/" + filePath}, {"share", ""},
			}, AgentURL: s.pageAgentURL(user, repo, viewer)},
			Repo:       repo,
			Path:       filePath,
			Name:       pathBase(filePath),
			IsMarkdown: markdownPath(filePath),
			NewURL:     newURL,
			Notice:     notice,
			Error:      errMsg,
		}
		if data.IsMarkdown {
			if view, err := s.repoView(user, repo); err == nil {
				data.LinkedPages, _ = s.shareTargets(view, user, repo, filePath)
			}
		}
		data.Links = shareLinkRows(hubBase(r), s.Accounts.ListShareLinksForPath(user, repo, filePath))
		s.renderPage(w, r, "sharelinks", data)
	}

	if r.Method != http.MethodPost {
		render("", "")
		return
	}
	if s.Accounts == nil {
		render("", "Accounts are not enabled on this hub.")
		return
	}
	switch r.FormValue("action") {
	case "create":
		token, err := s.Accounts.CreateShareLink(user, repo, filePath, r.FormValue("includeLinked") != "")
		if err != nil {
			s.Log.Printf("create share link %s/%s %s: %v", user, repo, filePath, err)
			render("", "Could not create a share link.")
			return
		}
		newURL = hubBase(r) + sharePrefix + token
		render("Share link created.", "")
	case "revoke":
		id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
		if err != nil {
			render("", "Could not revoke that link.")
			return
		}
		if err := s.Accounts.DeleteShareLink(user, repo, id); err != nil {
			render("", "Could not revoke that link.")
			return
		}
		render("Share link revoked. It now 404s for everyone.", "")
	default:
		render("", "")
	}
}
