package hub

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// The read-only central space (Phase 1). The same stable URL that serves git
// over HTTPS also renders, in a browser, the list of a user's repos and a
// per-repo view of the knowledge — the tree with descriptions and freshness,
// plus a copy-ready clone command. Rendering reads straight from the bare
// repos and reuses core's frontmatter parser, so it can't drift from the CLI.

var webTmpl = template.Must(template.New("web").Parse(webHTML))

type repoListData struct {
	User  string
	Repos []string
}

type fileRow struct {
	Path        string
	Description string
	Age         string
}

type repoViewData struct {
	User     string
	Repo     string
	CloneURL string
	Files    []fileRow
	Empty    bool
}

// serveWeb handles browser (non-git) requests. It is read-only and private:
// a valid token is required and may only view its own namespace.
func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	authUser, valid := s.Tokens.UserFor(tokenFromRequest(r))
	if !valid {
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

	switch len(segs) {
	case 0:
		s.renderRepoList(w, authUser)
	case 1:
		if segs[0] != authUser {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		s.renderRepoList(w, authUser)
	case 2:
		user, repo := segs[0], strings.TrimSuffix(segs[1], ".git")
		if user != authUser {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !nameRe.MatchString(user) || !nameRe.MatchString(repo) {
			http.NotFound(w, r)
			return
		}
		s.renderRepo(w, r, user, repo)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) renderRepoList(w http.ResponseWriter, user string) {
	repos, err := s.Storage.ListRepos(user)
	if err != nil {
		s.Log.Printf("list repos %s: %v", user, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "list", repoListData{User: user, Repos: repos})
}

func (s *Server) renderRepo(w http.ResponseWriter, r *http.Request, user, repo string) {
	if !s.Storage.Exists(user, repo) {
		http.NotFound(w, r)
		return
	}
	files, err := RepoSnapshot("git", s.Storage.RepoDir(user, repo), defaultRef)
	if err != nil {
		s.Log.Printf("snapshot %s/%s: %v", user, repo, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	data := repoViewData{
		User:     user,
		Repo:     repo,
		CloneURL: fmt.Sprintf("git clone %s://%s/%s/%s.git", scheme, r.Host, user, repo),
		Empty:    len(files) == 0,
	}
	for _, f := range files {
		data.Files = append(data.Files, fileRow{Path: f.Path, Description: f.Description, Age: ageString(f.LastCommit)})
	}
	s.render(w, "repo", data)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := webTmpl.ExecuteTemplate(w, name, data); err != nil {
		s.Log.Printf("render %s: %v", name, err)
	}
}

// ageString renders a unix time as a short relative age for display.
func ageString(unix int64) string {
	if unix == 0 {
		return ""
	}
	d := time.Since(time.Unix(unix, 0))
	switch {
	case d < time.Hour:
		return "<1h ago"
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

const webHTML = `
{{define "head"}}<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<style>
 body{font:15px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace;max-width:820px;margin:2rem auto;padding:0 1rem;color:#1a1a1a}
 a{color:#0b62d6;text-decoration:none} a:hover{text-decoration:underline}
 h1{font-size:1.3rem;margin:.2rem 0 1rem} .muted{color:#777}
 .clone{background:#f5f5f5;border:1px solid #e2e2e2;border-radius:6px;padding:.55rem .7rem;overflow-x:auto;white-space:nowrap}
 table{border-collapse:collapse;width:100%;margin-top:1rem} td{padding:.28rem .5rem;vertical-align:top;border-bottom:1px solid #f0f0f0}
 td.age{color:#999;text-align:right;white-space:nowrap} .desc{color:#555} ul{list-style:none;padding:0} li{padding:.25rem 0}
</style>{{end}}

{{define "list"}}{{template "head"}}
<h1>agentsfs hub — {{.User}}</h1>
{{if .Repos}}<ul>{{range .Repos}}<li>📁 <a href="/{{$.User}}/{{.}}">{{.}}</a></li>{{end}}</ul>
{{else}}<p class="muted">No repositories yet. Push one:</p>
<p class="clone">git remote add hub &lt;this-url&gt;/{{.User}}/&lt;repo&gt;.git &amp;&amp; git push hub main</p>{{end}}
{{end}}

{{define "repo"}}{{template "head"}}
<p class="muted"><a href="/{{.User}}">← {{.User}}</a></p>
<h1>{{.User}}/{{.Repo}}</h1>
<p class="clone">{{.CloneURL}}</p>
{{if .Empty}}<p class="muted">This repository has no commits yet.</p>
{{else}}<table>{{range .Files}}<tr><td>{{.Path}}{{if .Description}}<div class="desc">{{.Description}}</div>{{end}}</td><td class="age">{{.Age}}</td></tr>{{end}}</table>{{end}}
{{end}}
`
