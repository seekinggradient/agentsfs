package hub

import (
	"agentsfs.ai/afs/internal/core"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const listenMaxSource = 1 << 20
const listenMaxAudio = 12 << 20
const listenCacheLimit = 32 << 20
const listenService = "https://mdto-narrate-api.fly.dev"

type listenState struct {
	mu     sync.Mutex
	jobs   map[string]*listenJob
	active map[string]int
	bytes  int
	pats   *AgentPATStore
	// Test seam only; requests never choose an upstream or supply a credential.
	upstream string
}
type listenJob struct {
	done    chan struct{}
	body    []byte
	status  int
	retry   string
	expires time.Time
}
type listenPageData struct {
	baseData
	Repo, Initial string
}
type listenSource struct {
	Path       string          `json:"path"`
	Hash       string          `json:"hash"`
	HTML       string          `json:"html"`
	Passages   []listenPassage `json:"passages"`
	guided     bool
	guidedText string
}

// All routes pass through the repository's normal read ACL. Listening grants
// neither write access nor an agent run. Generation additionally needs a session.
func (s *Server) handleListen(w http.ResponseWriter, r *http.Request, owner, repo, viewer string, rest []string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if len(rest) > 1 {
		http.NotFound(w, r)
		return
	}
	action := ""
	if len(rest) == 1 {
		action = rest[0]
	}
	method := http.MethodGet
	if action == "speech" {
		method = http.MethodPost
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		apiError(w, 405, "method not allowed")
		return
	}
	if action == "" {
		// Deliberately a full document: browser audio and pending requests have one
		// lifecycle, independent of the Hub's PJAX navigation and agent dock.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; media-src 'self' blob:; connect-src 'self'; font-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
		s.renderPage(w, r, "listen", listenPageData{baseData: baseData{User: owner, Viewer: viewer}, Repo: repo, Initial: r.URL.Query().Get("path")})
		return
	}
	if action == "pages" {
		view, err := s.repoView(owner, repo)
		if err != nil {
			apiError(w, 500, "Could not load workspace pages.")
			return
		}
		pages := []string{}
		for _, f := range view.Files {
			if listenMarkdown(f.Path) {
				pages = append(pages, f.Path)
			}
		}
		writeJSON(w, 200, map[string]any{"pages": pages})
		return
	}
	if action == "source" {
		source, err := s.listenSource(owner, repo, r.URL.Query().Get("path"))
		if err != nil {
			apiError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, source)
		return
	}
	if action != "voices" && action != "speech" {
		http.NotFound(w, r)
		return
	}
	user, ok := s.webSessionUser(r)
	if !ok || user != viewer || s.Accounts == nil {
		apiError(w, 401, "Sign in to use Gemini voices.")
		return
	}
	if action == "speech" && !mdtoSameOrigin(r) {
		apiError(w, 403, "Narration must start from this Hub.")
		return
	}
	token := s.listenToken(user)
	if token == "" {
		apiError(w, 503, "Voice authentication is unavailable. Please try again.")
		return
	}
	if action == "voices" {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		body, status, retry := s.listenRequest(ctx, token, "/v1/narrate/voices", nil)
		listenResponse(w, body, status, retry)
		return
	}
	var input struct {
		Path  string `json:"path"`
		Hash  string `json:"hash"`
		Index int    `json:"index"`
		Voice string `json:"voice"`
		Text  string `json:"text,omitempty"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || dec.Decode(&struct{}{}) != io.EOF || input.Index < 0 || len(input.Voice) > 40 || input.Voice == "" {
		apiError(w, 400, "Invalid narration request.")
		return
	}
	for _, c := range input.Voice {
		if c < 'A' || c > 'z' || (c > 'Z' && c < 'a') {
			apiError(w, 400, "Invalid voice.")
			return
		}
	}
	source, err := s.listenSource(owner, repo, input.Path)
	if err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if input.Hash != source.Hash {
		apiError(w, 409, "This page changed. Reload the page before continuing.")
		return
	}
	if input.Index >= len(source.Passages) {
		apiError(w, 400, "That passage is not on this page.")
		return
	}
	speechText := source.Passages[input.Index].Text
	if input.Text != "" {
		// The host validates an exact authored beat. Independently constrain the
		// service to visible text in the current, authorized manuscript.
		normalized := strings.Join(strings.Fields(input.Text), " ")
		if !source.guided || len([]rune(input.Text)) > 1200 || normalized == "" || !strings.Contains(source.guidedText, normalized) {
			apiError(w, 400, "Speech must come from this guided manuscript.")
			return
		}
		speechText = normalized
	}
	payload, _ := json.Marshal(map[string]string{"text": speechText, "voice": input.Voice, "pace": "natural"})
	// Identity, source version and voice all participate. Recheck ACL and source
	// BEFORE a cache hit, including for readers whose access was revoked.
	key := sourceHash([]byte(user + "\x00" + owner + "/" + repo + "\x00" + source.Hash + "\x00" + s.listenOrigin() + "\x00" + string(payload)))
	state := &s.narration
	state.mu.Lock()
	if state.jobs == nil {
		state.jobs = map[string]*listenJob{}
		state.active = map[string]int{}
	}
	now := time.Now()
	for k, j := range state.jobs {
		if !j.expires.IsZero() && (now.After(j.expires) || state.bytes > listenCacheLimit) {
			state.bytes -= len(j.body)
			delete(state.jobs, k)
		}
	}
	job := state.jobs[key]
	if job == nil {
		if state.active[user] >= 2 {
			state.mu.Unlock()
			w.Header().Set("Retry-After", "3")
			apiError(w, 429, "Two passages are already being prepared. Try again shortly.")
			return
		}
		job = &listenJob{done: make(chan struct{})}
		state.jobs[key] = job
		state.active[user]++
		// The provider might finish after a browser cancels. Retain that result so
		// pause/seek/reload cannot blindly submit the same paid synthesis twice.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
			defer cancel()
			body, status, retry := s.listenRequest(ctx, token, "/v1/narrate/synthesize", payload)
			state.mu.Lock()
			defer state.mu.Unlock()
			job.body, job.status, job.retry = body, status, retry
			ttl := 30 * time.Minute
			if status != 200 {
				ttl = time.Minute
			}
			job.expires = time.Now().Add(ttl)
			state.bytes += len(body)
			state.active[user]--
			close(job.done)
		}()
	}
	state.mu.Unlock()
	select {
	case <-r.Context().Done():
		return
	case <-job.done:
		listenResponse(w, job.body, job.status, job.retry)
	}
}
func listenMarkdown(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".md" || ext == ".markdown"
}
func (s *Server) listenSource(owner, repo, p string) (*listenSource, error) {
	if !validRepoPath(p) || !listenMarkdown(p) {
		return nil, fmt.Errorf("Choose a Markdown page in this workspace.")
	}
	bare := s.Storage.RepoDir(owner, repo)
	rev := headOID("git", bare, defaultRef)
	size, ok := BlobSize("git", bare, rev, p)
	if !ok || size > listenMaxSource {
		return nil, fmt.Errorf("Page missing or larger than the 1 MB reading limit.")
	}
	content, ok := BlobContent("git", bare, rev, p)
	if !ok || !utf8.ValidString(content) {
		return nil, fmt.Errorf("This page could not be read as Markdown.")
	}
	source := &listenSource{Path: p, Hash: sourceHash([]byte(content)), Passages: []listenPassage{}}
	resolve := func(target string) (string, bool) {
		u, err := url.Parse(target)
		if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "#") {
			return "", false
		}
		rel := path.Clean(path.Join(path.Dir(p), u.Path))
		if !validRepoPath(rel) {
			return "", false
		}
		if _, ok := BlobSize("git", bare, rev, rel); !ok {
			return "", false
		}
		return (&url.URL{Path: "/" + owner + "/" + repo + "/raw/" + rel}).String(), true
	}
	view, err := s.repoView(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("Could not read workspace links.")
	}
	paths := make([]string, 0, len(view.Files))
	for _, f := range view.Files {
		paths = append(paths, f.Path)
	}
	idx := core.NewNameIndex(paths)
	wiki := func(target string) (string, bool) {
		matches := idx.Resolve(target)
		if len(matches) == 0 {
			return "", false
		}
		return (&url.URL{Path: "/" + owner + "/" + repo + "/blob/" + matches[0]}).String(), true
	}
	link := func(target string) (string, bool) {
		u, e := url.Parse(target)
		if e != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "#") {
			return "", false
		}
		rel := path.Clean(path.Join(path.Dir(p), u.Path))
		if !validRepoPath(rel) {
			return "", false
		}
		return (&url.URL{Path: "/" + owner + "/" + repo + "/blob/" + rel, Fragment: u.Fragment}).String(), true
	}
	rendered, err := renderMarkdownWith(content, markdownOptions{narration: &source.Passages, resolveImage: resolve, resolveWiki: wiki, resolveLink: link})
	if err != nil {
		return nil, fmt.Errorf("Could not render this Markdown page.")
	}
	source.HTML = rendered
	source.guided = strings.TrimSpace(core.FrontmatterValueFromReader(strings.NewReader(content), "markdownto")) == "guided-narration@0.1"
	if source.guided {
		var texts []string
		for _, passage := range source.Passages {
			texts = append(texts, passage.Text)
		}
		source.guidedText = strings.Join(strings.Fields(strings.Join(texts, " ")), " ")
	}
	return source, nil
}
func (s *Server) listenToken(user string) string {
	st := &s.narration
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.pats == nil {
		st.pats = NewAgentPATStore(filepath.Join(s.Storage.Root(), ".narration-pats.json"))
	}
	if token, ok := st.pats.Get(user); ok {
		if u, valid := s.Accounts.UserForToken(token); valid && u == user {
			return token
		}
		if st.pats.Delete(user) != nil {
			return ""
		}
	}
	token, err := st.pats.GetOrMint(user, func() (string, error) { return s.Accounts.CreatePAT(user, "Hub page narration") })
	if err != nil {
		return ""
	}
	return token
}
func (s *Server) listenOrigin() string {
	if s.narration.upstream != "" {
		return s.narration.upstream
	}
	return listenService
}
func (s *Server) listenRequest(ctx context.Context, token, route string, body []byte) ([]byte, int, string) {
	method := http.MethodGet
	if body != nil {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, s.listenOrigin()+route, bytes.NewReader(body))
	if err != nil {
		return []byte(`{"error":"Voice service is unavailable."}`), 503, "60"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return []byte(`{"error":"The voice service did not respond. Wait a minute before retrying this passage."}`), 502, "60"
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, listenMaxAudio+1))
	if err != nil || len(data) > listenMaxAudio || !json.Valid(data) {
		return []byte(`{"error":"The voice service returned an invalid response."}`), 502, "60"
	}
	if res.StatusCode != 200 {
		status := res.StatusCode
		if status < 400 || status > 599 {
			status = 502
		}
		// Upstream diagnostics must not expose credentials or provider internals.
		msg := []byte(`{"error":"Gemini could not prepare this passage. Please retry shortly."}`)
		if status == 429 {
			msg = []byte(`{"error":"Voice limit reached. Please wait before retrying."}`)
		}
		retry := res.Header.Get("Retry-After")
		if retry == "" {
			retry = "60"
		}
		return msg, status, retry
	}
	if route == "/v1/narrate/synthesize" && !validListenAudio(data) {
		return []byte(`{"error":"Gemini returned incomplete audio. Please retry this passage shortly."}`), 502, "60"
	}
	return data, 200, ""
}
func listenResponse(w http.ResponseWriter, body []byte, status int, retry string) {
	if retry != "" {
		w.Header().Set("Retry-After", retry)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

var listenPCM = regexp.MustCompile(`^audio/L16;codec=pcm;rate=([0-9]+)$`)

func validListenAudio(body []byte) bool {
	var result struct {
		Audio struct {
			Data     string  `json:"data"`
			MIME     string  `json:"mimeType"`
			Duration float64 `json:"durationMs"`
		} `json:"audio"`
	}
	if json.Unmarshal(body, &result) != nil || result.Audio.Duration <= 0 {
		return false
	}
	match := listenPCM.FindStringSubmatch(result.Audio.MIME)
	if match == nil {
		return false
	}
	rate, _ := strconv.Atoi(match[1])
	if rate < 8000 || rate > 96000 {
		return false
	}
	pcm, err := base64.StdEncoding.DecodeString(result.Audio.Data)
	return err == nil && len(pcm) > 0 && len(pcm)%2 == 0
}
