package hub

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	neturl "net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// AgentManager provisions and reaches a write-capable agent for a repo, running
// in a Fly Sprite (https://sprites.dev). One sprite per repo, created on demand,
// auto-sleeping when idle. The agent clones the repo, and edits/commits/pushes
// it back to this hub — so the knowledge base stays the source of truth.
//
// The agentsfs-chat source is embedded and pushed into the sprite; the sprite's
// default image already has node + git, and ripgrep is apt-installed.

//go:embed agent-bundle.tgz
var agentBundle []byte

const spritesAPI = "https://api.sprites.dev/v1"

const defaultChatModel = "gpt-5.6-luna"
const defaultChatReasoningEffort = "high"

type AgentManager struct {
	Token               string // SPRITES_TOKEN
	OpenAIKey           string
	ChatModel           string
	ChatReasoningEffort string
	HubBase             string // public URL the sprite clones from, e.g. https://hub.agentsfs.ai
	AfsBin              string // path to a linux/amd64 afs binary to ship into sprites
	Accounts            *AccountStore
	Log                 *log.Logger
	spritesBase         string

	// DevURL (env HUB_AGENT_DEV_URL) is a local-development escape hatch: when
	// set, the agent feature is considered enabled, no sprite is ever looked up
	// or provisioned, and /agent/* API+preview traffic is proxied to this URL
	// (a locally running agentsfs-chat) with no Sprites bearer attached. The
	// same route allow-list and response hardening apply as in production.
	// Never set this on a deployed hub.
	DevURL string

	mu       sync.Mutex
	inflight map[string]bool            // legacy per-repo provisioning + reconcile single-flight guards
	ready    map[string]string          // sprite name -> URL after one successful health check
	state    map[string]*provisionState // "user:<user>" -> workspace provisioning state machine
	afsFixed map[string]bool            // users whose missing-afs repair already ran this process

	// Tunables with production defaults (set by NewAgentManager); tests
	// shorten them and point spritesBase at a fake Sprites API.
	sleep        func(time.Duration)
	pollInterval time.Duration // detached-run and health poll cadence
	bootBudget   time.Duration // max wall-clock for one boot run before its outcome is "unknown"
	healthWait   time.Duration // how long the service gets to answer /api/health after boot
	wakeGrace    time.Duration // how long an existing sprite may stay unhealthy before we reprovision
}

func NewAgentManager(token, openaiKey, chatModel, hubBase string, accounts *AccountStore, logger *log.Logger) *AgentManager {
	if chatModel == "" {
		chatModel = defaultChatModel
	}
	chatReasoningEffort := os.Getenv("CHAT_REASONING_EFFORT")
	if chatReasoningEffort == "" {
		chatReasoningEffort = defaultChatReasoningEffort
	}
	if hubBase == "" {
		hubBase = "https://hub.agentsfs.ai"
	}
	afsBin := os.Getenv("AFS_LINUX_BIN")
	if afsBin == "" {
		afsBin = "/usr/local/bin/afs-linux" // baked into the hub image by deploy/Dockerfile
	}
	return &AgentManager{
		Token: token, OpenAIKey: openaiKey, ChatModel: chatModel, ChatReasoningEffort: chatReasoningEffort,
		HubBase: strings.TrimRight(hubBase, "/"), AfsBin: afsBin,
		Accounts: accounts, Log: logger,
		spritesBase: spritesAPI,
		inflight:    map[string]bool{},
		ready:       map[string]string{},
		state:       map[string]*provisionState{},
		afsFixed:    map[string]bool{},

		sleep:        time.Sleep,
		pollInterval: 5 * time.Second,
		bootBudget:   15 * time.Minute,
		healthWait:   150 * time.Second,
		wakeGrace:    3 * time.Minute,
	}
}

// logf logs when a logger is configured (nil in some tests and local dev).
func (m *AgentManager) logf(format string, args ...any) {
	if m.Log != nil {
		m.Log.Printf(format, args...)
	}
}

// Enabled reports whether the agent feature is configured (tokens present, or
// the local-dev proxy override).
func (m *AgentManager) Enabled() bool {
	if m == nil {
		return false
	}
	if m.DevURL != "" {
		return true
	}
	return m.Token != "" && m.OpenAIKey != "" && m.Accounts != nil
}

var agentNameRe = regexp.MustCompile(`[^a-z0-9-]+`)

func agentSpriteName(user, repo string) string {
	s := agentNameRe.ReplaceAllString(strings.ToLower(user+"-"+repo), "-")
	return "afs-" + strings.Trim(s, "-")
}

// agentUserSpriteName is the ONE sprite per user that holds all their knowledge
// bases (workspace mode). The "afs-user-" prefix is a reserved namespace (see
// reservedNames) so it can never collide with a per-repo "afs-<user>-<repo>".
func agentUserSpriteName(user string) string {
	s := agentNameRe.ReplaceAllString(strings.ToLower(user), "-")
	return "afs-user-" + strings.Trim(s, "-")
}

func (m *AgentManager) authed(method, path string, body io.Reader, timeout time.Duration) (*http.Response, error) {
	req, err := http.NewRequest(method, m.api()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return (&http.Client{Timeout: timeout}).Do(req)
}

// spriteURL returns a Sprite's public URL. A missing Sprite is ("", nil),
// while control-plane/network failures remain errors: callers must never turn
// an ambiguous lookup failure into a destructive provision.
func (m *AgentManager) spriteURL(name string) (string, error) {
	resp, err := m.authed(http.MethodGet, "/sprites/"+name, nil, 15*time.Second)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sprite lookup returned %s", resp.Status)
	}
	var d struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return "", fmt.Errorf("decode sprite lookup: %w", err)
	}
	if d.URL == "" {
		return "", fmt.Errorf("sprite lookup returned no URL")
	}
	return d.URL, nil
}

// healthy reports whether the agent server answers over its URL.
func (m *AgentManager) healthy(url string) bool {
	req, err := http.NewRequest(http.MethodGet, url+"/api/health", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+m.Token)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// cachedReadyURL avoids putting every browser asset and API request through
// two remote control-plane calls (sprite lookup + health). Once a Sprite has
// answered health successfully, its stable URL can be proxied directly for the
// rest of this Hub process. The proxy remains the authoritative signal if the
// service later becomes unreachable.
func (m *AgentManager) cachedReadyURL(name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready[name]
}

func (m *AgentManager) rememberReadyURL(name, url string) {
	m.mu.Lock()
	m.ready[name] = url
	m.mu.Unlock()
}

// forgetReadyURL drops a cached ready URL once the proxy fails against it, so
// the next request goes back through the health check (and, if the service is
// really gone, into the wake-grace / reprovision path) instead of being pinned
// to a dead upstream for the life of the process.
func (m *AgentManager) forgetReadyURL(url string) {
	m.mu.Lock()
	for k, v := range m.ready {
		if v == url {
			delete(m.ready, k)
		}
	}
	m.mu.Unlock()
}

// Ensure returns the agent's URL and whether it is ready to serve. If it isn't,
// it kicks off provisioning once (in the background) and returns ready=false so
// the caller can show a "starting" page and poll.
func (m *AgentManager) Ensure(user, repo string) (url string, ready bool) {
	name := agentSpriteName(user, repo)
	if url = m.cachedReadyURL(name); url != "" {
		return url, true
	}
	var lookupErr error
	url, lookupErr = m.spriteURL(name)
	if lookupErr != nil {
		return "", false
	}
	if url != "" && m.healthy(url) {
		m.rememberReadyURL(name, url)
		return url, true
	}
	// A transient health timeout is not evidence that an existing persistent
	// Sprite needs to be rebuilt. Its service may simply be cold-starting. Let
	// the caller show the starting state and poll again; automatic reprovisioning
	// here can wipe a healthy workspace because one request was slow.
	if url != "" {
		return url, false
	}
	key := user + "/" + repo
	m.mu.Lock()
	if !m.inflight[key] {
		m.inflight[key] = true
		go m.provision(user, repo, name)
	}
	m.mu.Unlock()
	return url, false
}

// RepoRef identifies a repo to clone into a user's workspace. Owner may differ
// from the sprite's user when the repo was shared with them (collaborator).
type RepoRef struct{ Owner, Repo string }

// EnsureUser is the per-user (cross-repo) counterpart of Ensure: it returns the
// URL of the user's single workspace sprite and whether it's ready, kicking off
// provisioning (which clones every repo in `repos`) once in the background if
// not. `repos` is passed in from the web layer so AgentManager needn't depend on
// Storage.
func (m *AgentManager) EnsureUser(user string, repos []RepoRef) (url string, ready bool) {
	if m.DevURL != "" {
		return m.DevURL, true // local dev: no sprite, always ready
	}
	name := agentUserSpriteName(user)
	if url = m.cachedReadyURL(name); url != "" {
		return url, true
	}
	var lookupErr error
	url, lookupErr = m.spriteURL(name)
	if lookupErr != nil {
		return "", false
	}
	if url != "" && m.healthy(url) {
		m.rememberReadyURL(name, url)
		m.clearProvisionState(user)
		// Sprite already up: reconcile its workspace in the background so a repo
		// created or shared since it was provisioned gets cloned in (the agent
		// re-scans the workspace live, so it shows up without a re-provision).
		go m.reconcileWorkspace(user, name, repos)
		return url, true
	}
	m.maybeStartProvision(user, name, repos, url != "")
	return url, false
}

// provisionState is the per-user workspace provisioning state machine. It is
// what stands between the starting page's meta refresh and a provisioning
// stampede: every trigger (refresh, parallel tabs, retries) funnels through
// maybeStartProvision, which runs at most one attempt at a time and backs off
// between failed attempts instead of re-minting credentials every 4 seconds.
type provisionState struct {
	Running        bool
	Stage          string
	Attempt        int
	LastError      string // scrubbed; shown on the starting page
	StartedAt      time.Time
	NextRetry      time.Time
	FirstUnhealthy time.Time // when an EXISTING sprite was first seen unhealthy
	Force          bool      // explicit user retry: skip the wake grace period
}

// AgentProvisionStatus is a read-only snapshot for the starting page.
type AgentProvisionStatus struct {
	Running   bool
	Stage     string
	Attempt   int
	LastError string
	NextRetry time.Time
}

// maybeStartProvision decides whether this (unhealthy) page load should kick
// off a provisioning attempt. An existing sprite gets a wake grace period
// first: a slow health check usually means the sprite is waking up, and
// rebuilding it would wipe its workspace for no reason (a full boot re-clones
// every repo). Only a sprite that stays unhealthy past the grace — or one that
// doesn't exist at all — is (re)provisioned.
func (m *AgentManager) maybeStartProvision(user, name string, repos []RepoRef, spriteExists bool) {
	key := "user:" + user
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.state[key]
	if st == nil {
		st = &provisionState{}
		m.state[key] = st
	}
	if st.Running || now.Before(st.NextRetry) {
		return
	}
	if spriteExists && st.Attempt == 0 && !st.Force {
		if st.FirstUnhealthy.IsZero() {
			st.FirstUnhealthy = now
			return
		}
		if now.Sub(st.FirstUnhealthy) < m.wakeGrace {
			return
		}
		m.logf("agent: sprite %s still unhealthy after %s wake grace; reprovisioning", name, m.wakeGrace)
	}
	st.Running = true
	st.Attempt++
	st.Stage = "starting"
	st.StartedAt = now
	st.Force = false
	go m.provisionUser(user, name, repos)
}

// ProvisionStatus reports the workspace provisioning state for user (zero
// value when none is tracked).
func (m *AgentManager) ProvisionStatus(user string) AgentProvisionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.state["user:"+user]
	if st == nil {
		return AgentProvisionStatus{}
	}
	return AgentProvisionStatus{
		Running: st.Running, Stage: st.Stage, Attempt: st.Attempt,
		LastError: st.LastError, NextRetry: st.NextRetry,
	}
}

// RetryProvision is the explicit user-initiated retry: it clears the backoff
// gate and the wake grace so the next EnsureUser starts an attempt immediately.
func (m *AgentManager) RetryProvision(user string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.state["user:"+user]; st != nil && !st.Running {
		st.NextRetry = time.Time{}
		st.Force = true
	}
}

func (m *AgentManager) clearProvisionState(user string) {
	m.mu.Lock()
	delete(m.state, "user:"+user)
	m.mu.Unlock()
}

func (m *AgentManager) setStage(key, stage string) {
	m.mu.Lock()
	st := m.state[key]
	var elapsed time.Duration
	if st != nil {
		st.Stage = stage
		elapsed = time.Since(st.StartedAt)
	}
	m.mu.Unlock()
	m.logf("agent: provision %s stage=%s (t+%.0fs)", key, stage, elapsed.Seconds())
}

// finishAttempt closes out a provisioning attempt: success clears the state,
// failure records the (already scrubbed) error and arms the backoff gate.
func (m *AgentManager) finishAttempt(key string, ok bool, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.state[key]
	if st == nil {
		return
	}
	if ok {
		delete(m.state, key)
		return
	}
	st.Running = false
	st.Stage = ""
	st.LastError = errMsg
	st.NextRetry = time.Now().Add(provisionBackoff(st.Attempt))
}

func provisionBackoff(attempt int) time.Duration {
	steps := []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}
	if attempt >= 1 && attempt <= len(steps) {
		return steps[attempt-1]
	}
	return 10 * time.Minute
}

const agentPreviewCSP = "default-src 'none'; " +
	"script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
	"img-src data: blob:; font-src data:; media-src data: blob:; " +
	"worker-src 'none'; connect-src 'none'; frame-src 'none'; object-src 'none'; " +
	"base-uri 'none'; form-action 'none'; " +
	"sandbox allow-scripts"

const agentAPICSP = "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; sandbox"

// hardenAgentProxyResponse keeps the private sprite from acquiring ambient
// authority over the hub origin. In particular, preview documents are authored
// by the coding agent and must run with an opaque origin (no allow-same-origin).
// The restrictive policy supports only self-contained static artifacts; Hub
// previews cannot depend on multi-file loads because opaque-origin requests
// have no Hub cookie.
func hardenAgentProxyResponse(resp *http.Response) error {
	route, ok := classifyAgentPath(resp.Request.URL.Path)
	if !ok || route.kind == agentRouteUI {
		return fmt.Errorf("unexpected agent response path %q", resp.Request.URL.Path)
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Errorf("agent redirect status %d is not allowed", resp.StatusCode)
	}

	// Rebuild, rather than edit, the untrusted upstream header map. This keeps
	// Set-Cookie, Location/Refresh, Clear-Site-Data, CORS, reporting, and other
	// origin-affecting headers from ever reaching the Hub client.
	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	headers := make(http.Header)
	if encoding == "gzip" || encoding == "br" || encoding == "deflate" {
		headers.Set("Content-Encoding", encoding)
	}
	headers.Set("Cache-Control", "no-store")
	headers.Set("X-Content-Type-Options", "nosniff")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("Cross-Origin-Resource-Policy", "same-origin")
	if route.kind == agentRoutePreview {
		contentType := previewContentType(resp.Request.URL.Path)
		headers.Set("Content-Type", contentType)
		headers.Set("Content-Security-Policy", agentPreviewCSP)
		if contentType == "application/octet-stream" {
			headers.Set("Content-Disposition", "attachment")
		}
	} else {
		headers.Set("Content-Type", route.contentType)
		headers.Set("Content-Security-Policy", agentAPICSP)
		headers.Set("X-Frame-Options", "DENY")
		if resp.Request.URL.Path == "/api/chat" {
			headers.Set("Cache-Control", "no-store, no-transform")
			headers.Set("X-Accel-Buffering", "no")
		}
	}
	resp.Header = headers
	resp.Trailer = nil
	resp.ContentLength = -1
	return nil
}

// Proxy reverse-proxies a request through to the sprite's agent server: it
// strips the /<user>/<repo>/agent prefix and injects the Sprites bearer token,
// so the browser stays on the hub (already authenticated) and never sees the
// sprites.dev login, while the sprite stays private to our org. Streams SSE.
func (m *AgentManager) Proxy(w http.ResponseWriter, r *http.Request, spriteURL, prefix string) {
	p, ok := relativeAgentPath(r.URL.Path, prefix)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, ok := enforceAgentRoute(w, r.Method, p, true); !ok {
		return
	}
	target, err := neturl.Parse(spriteURL)
	if err != nil {
		http.Error(w, "bad agent url", http.StatusInternalServerError)
		return
	}
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			req.URL.Path = p
			// Keep the private hop uncompressed. The response hardener rebuilds
			// headers, so forwarding a newly-supported encoding without also
			// preserving its Content-Encoding would corrupt the body. The public
			// Fly edge can still compress the final Hub response for the browser.
			req.Header.Set("Accept-Encoding", "identity")
			// Authentication terminates at the hub. The private sprite needs its
			// Sprites bearer, never the user's hub session cookie. In local-dev
			// mode the upstream is a loopback agentsfs-chat with no auth of its
			// own — forward no credential at all.
			req.Header.Del("Cookie")
			if m.DevURL != "" {
				req.Header.Del("Authorization")
			} else {
				req.Header.Set("Authorization", "Bearer "+m.Token)
			}
		},
		FlushInterval:  -1, // flush each chunk immediately so SSE streams
		ModifyResponse: hardenAgentProxyResponse,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			m.forgetReadyURL(spriteURL)
			if m.Log != nil {
				m.Log.Printf("agent proxy %s: %v", spriteURL, err)
			}
			http.Error(w, "agent unreachable", http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// afsInstallURL is the public installer that downloads the prebuilt afs release
// binary for the sprite's OS/arch (no Go/git). Matches docs/setup.md.
const afsInstallURL = "https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh"

// installAfs puts the afs CLI on the sprite. It PREFERS the published release
// binary via the curl installer — fast, arch-correct, and self-updatable — and
// falls back to chunk-uploading the linux binary embedded in the hub image only
// when the installer can't run (offline sprite, no release asset). Returns
// whether afs ended up installed. The fallback keeps provisioning working even
// if GitHub is unreachable, so this is strictly more reliable than before.
func (m *AgentManager) installAfs(name string) bool {
	// rm any prior binary first, and gate on afs actually RUNNING (not just an
	// exec bit) so a stale/partial binary from an earlier attempt can't pass and
	// skip the sha256-verified embedded fallback.
	script := "mkdir -p /home/sprite/.local/bin; rm -f /home/sprite/.local/bin/afs; " +
		"{ curl -fsSL " + afsInstallURL + " | AFS_INSTALL_DIR=/home/sprite/.local/bin sh; } >/tmp/afs-install.log 2>&1 || true; " +
		"/home/sprite/.local/bin/afs version >/dev/null 2>&1 && echo AFS_INSTALLED"
	if out, err := m.exec(name, script, 150*time.Second); err == nil && strings.Contains(out, "AFS_INSTALLED") {
		return true
	}
	m.logf("agent: curl-install of afs failed on %s; falling back to embedded binary", name)
	if err := m.uploadAfs(name); err != nil {
		m.logf("agent: afs unavailable on %s (installer + embedded fallback both failed): %v", name, scrub(err.Error()))
		return false
	}
	return true
}

// uploadAfs pushes the linux afs binary into the sprite via the verified
// chunk-file protocol (see uploadFileChunks) — the old blind append stream
// could silently lose a chunk and only fail at the final checksum.
func (m *AgentManager) uploadAfs(name string) error {
	bin, err := os.ReadFile(m.AfsBin)
	if err != nil {
		return err // not in the image (e.g. local dev) — caller treats as non-fatal
	}
	return m.uploadFileChunks(name, "/home/sprite/.local/bin/afs", bin)
}

func (m *AgentManager) provision(user, repo, name string) {
	key := user + "/" + repo
	defer func() {
		m.mu.Lock()
		delete(m.inflight, key)
		m.mu.Unlock()
		if r := recover(); r != nil {
			m.Log.Printf("agent: provision %s panicked: %v", key, r)
		}
	}()

	// 1. Create the sprite (a 409/"exists" is fine — reuse it).
	if resp, err := m.authed(http.MethodPost, "/sprites", strings.NewReader(fmt.Sprintf(`{"name":%q}`, name)), 30*time.Second); err == nil {
		resp.Body.Close()
	} else {
		m.Log.Printf("agent: create sprite %s: %v", name, err)
		return
	}

	// 2. Mint a scoped push credential for this repo's owner.
	token, err := m.Accounts.CreatePAT(user, "agent-sprite:"+repo)
	if err != nil {
		m.Log.Printf("agent: mint token %s: %v", key, err)
		return
	}
	b64auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))

	// 3. Push the embedded agentsfs-chat source (small — one exec).
	src := "rm -rf /home/sprite/agentsfs-chat && mkdir -p /home/sprite/agentsfs-chat && base64 -d > /tmp/b.tgz <<'BEOF'\n" +
		base64.StdEncoding.EncodeToString(agentBundle) + "\nBEOF\ntar xzf /tmp/b.tgz -C /home/sprite/agentsfs-chat && rm /tmp/b.tgz && echo ok"
	if _, err := m.exec(name, src, 90*time.Second); err != nil {
		m.Log.Printf("agent: upload source %s: %v", key, err)
		return
	}

	// 3b. Ship the afs CLI so the agent WRAPS afs (tree, ranked/semantic search,
	//     backlinks) instead of reimplementing it — one source of truth. Set
	//     AFS_BIN so the agent finds it. Non-fatal: if the binary isn't in the
	//     hub image the agent still runs (degraded search), so we don't abort.
	afsEnv := ""
	if m.installAfs(name) {
		afsEnv = ",AFS_BIN=/home/sprite/.local/bin/afs"
	}

	// 4. Install deps, clone the repo (with the scoped credential), start the
	//    persistent agent service. git config carries the push credential so the
	//    agent can push its commits back. The same PAT authenticates model calls
	//    through the Hub proxy; the operator's OpenAI key never enters the Sprite.
	envs := m.repoServiceEnv(repo, token, afsEnv)
	boot := fmt.Sprintf(`set -e
command -v rg >/dev/null 2>&1 || (sudo apt-get update -qq && sudo apt-get install -y -qq ripgrep) >/dev/null 2>&1 || true
cd /home/sprite/agentsfs-chat && npm install --no-audit --no-fund >/tmp/npm.log 2>&1 || tail -5 /tmp/npm.log
rm -rf /home/sprite/wiki
git -c http.extraHeader="Authorization: Basic %s" clone %s/%s/%s.git /home/sprite/wiki 2>&1 | tail -1
git -C /home/sprite/wiki config http.extraHeader "Authorization: Basic %s"
git -C /home/sprite/wiki remote add hub %s/%s/%s.git
git config --global --add credential.helper '!afs hub credential' || true
git -C /home/sprite/wiki config user.name "AgentsFS Agent"
git -C /home/sprite/wiki config user.email "agent@agentsfs.ai"
sprite-env services delete agent >/dev/null 2>&1 || true
sprite-env services create agent --cmd npm --args start --dir /home/sprite/agentsfs-chat --http-port 8080 --env '%s' >/dev/null 2>&1
echo done`, b64auth, m.HubBase, user, repo, b64auth, m.HubBase, user, repo, envs)
	if out, err := m.exec(name, boot, 360*time.Second); err != nil {
		m.Log.Printf("agent: boot %s: %v (%s)", key, err, strings.TrimSpace(out))
		return
	}
	m.Log.Printf("agent: provisioned %s/%s", user, repo)
}

// repoServiceEnv builds the legacy per-repository service environment. Keep
// this path on the same PAT-authenticated Hub LLM proxy as provisionUser: a
// user's shell has root inside its Sprite, so putting m.OpenAIKey here would
// disclose the shared operator credential.
func (m *AgentManager) repoServiceEnv(repo, token, afsEnv string) string {
	return fmt.Sprintf(
		"PORT=8080,HOST=0.0.0.0,AGENTSFS_ROOT=/home/sprite/wiki,AGENTSFS_NAME=%s,"+
			"AGENTSFS_ALLOW_WRITES=1,AGENTSFS_AGENT_NAME=AgentsFS Agent,AGENTSFS_AGENT_EMAIL=agent@agentsfs.ai,"+
			"CHAT_MODEL=%s,CHAT_REASONING_EFFORT=%s,AGENTSFS_LLM_BASE_URL=%s/v1/agent-llm,AGENTSFS_LLM_KEY=%s%s",
		repo, m.ChatModel, m.ChatReasoningEffort, m.HubBase, token, afsEnv)
}

// pushBundle uploads the embedded agentsfs-chat source into the sprite. The
// tarball is ~100KB so a single exec carries it; the sentinel plus the
// leading rm -rf make a lost-response retry safe and provable.
func (m *AgentManager) pushBundle(name string) error {
	src := "rm -rf /home/sprite/agentsfs-chat && mkdir -p /home/sprite/agentsfs-chat && base64 -d > /tmp/b.tgz <<'BEOF'\n" +
		base64.StdEncoding.EncodeToString(agentBundle) + "\nBEOF\ntar xzf /tmp/b.tgz -C /home/sprite/agentsfs-chat && rm /tmp/b.tgz && echo AFS_BUNDLE_OK"
	if _, err := m.execVerified(name, src, "AFS_BUNDLE_OK", 120*time.Second, 3); err != nil {
		return fmt.Errorf("upload source: %w", err)
	}
	return nil
}

// serviceMarkerPath records, on the sprite itself, which service configuration
// the agent service was created with. reconcileWorkspace compares it against
// serviceMarker() so a config-only change (CHAT_MODEL, CHAT_REASONING_EFFORT)
// becomes an in-place service update instead of a full re-provision.
const serviceMarkerPath = "/home/sprite/.afs-service-config"

func (m *AgentManager) serviceMarker() string {
	return fmt.Sprintf("v1 model=%s effort=%s", m.ChatModel, m.ChatReasoningEffort)
}

// workspaceServiceEnv builds the agent service env. token is the plaintext PAT
// during boot, or the __AFS_TOKEN__ placeholder for in-place updates (spliced
// in on the sprite from hub.json, so the credential never transits again).
//
// Workspace + shell mode, no single-repo pinning: AGENTSFS_ROOT is the
// workspace dir itself (always present, even if a clone failed or the user has
// zero repos) — the agent boots unfocused, lists the repos, and asks which to
// work in. NO OpenAI key here: model calls go through the hub's /v1/agent-llm
// proxy, authenticated by the per-user PAT (only this user's own credential),
// so the shared model key never lives in the sprite.
// NB: don't set PATH — the sprite's default PATH already includes both
// /home/sprite/.local/bin (the shipped afs) and /.sprite/bin (node/npm).
// Overriding it drops /.sprite/bin and the service can't find npm.
func (m *AgentManager) workspaceServiceEnv(token, afsEnv string) string {
	return fmt.Sprintf(
		"PORT=8080,HOST=0.0.0.0,HOME=/home/sprite,"+
			"XDG_CONFIG_HOME=/home/sprite/.config,AGENTSFS_MODE=workspace,AGENTSFS_WORKSPACE=/home/sprite/workspace,"+
			"AGENTSFS_SEARCH_DIR=/home/sprite/workspace,AGENTSFS_ROOT=/home/sprite/workspace,AGENTSFS_ALLOW_WRITES=1,AGENTSFS_ALLOW_SHELL=1,"+
			"AGENTSFS_PREVIEW_DIR=/home/sprite/workspace/.preview,AGENTSFS_DATA_DIR=/home/sprite/.agentsfs-chat,"+
			"AGENTSFS_AGENT_NAME=AgentsFS Agent,AGENTSFS_AGENT_EMAIL=agent@agentsfs.ai,CHAT_MODEL=%s,CHAT_REASONING_EFFORT=%s,"+
			"AGENTSFS_LLM_BASE_URL=%s/v1/agent-llm,AGENTSFS_LLM_KEY=%s"+afsEnv,
		m.ChatModel, m.ChatReasoningEffort, m.HubBase, token)
}

// bootScript is the full workspace boot, run DETACHED on the sprite (see
// startDetached): stop the old service first so health can't report a stale
// agent as the new one, install deps, rebuild the workspace clones, then
// create the service and write the config marker. Stage markers (st) feed the
// starting page and the per-stage duration log. `set -e` guards the required
// steps; each clone is self-guarded so one bad repo can't brick the rest.
func (m *AgentManager) bootScript(user, token, afsEnv string, repos []RepoRef) string {
	b64auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
	var clones strings.Builder
	for _, ref := range repos {
		// repo names come from ListRepos (filesystem dir names); re-validate as
		// slugs before interpolating them into the shell, defense-in-depth.
		if !validSlug(ref.Owner) || !validSlug(ref.Repo) {
			continue
		}
		// The single user PAT authenticates all clones — for shared repos it
		// presents the user as a collaborator, which the hub's git auth accepts.
		dir := "/home/sprite/workspace/" + workspaceDirName(user, ref)
		clones.WriteString("st 'clone " + ref.Owner + "/" + ref.Repo + "'\n")
		clones.WriteString(cloneRepoScript(b64auth, m.HubBase, ref, dir))
	}
	envs := m.workspaceServiceEnv(token, afsEnv)
	return fmt.Sprintf(`set -e
st() { [ -n "$AFS_RUN_BASE" ] && echo "$1" >> "$AFS_RUN_BASE.stage" || true; }
st service-stop
sprite-env services delete agent >/dev/null 2>&1 || true
st deps
command -v rg >/dev/null 2>&1 || (sudo apt-get update -qq && sudo apt-get install -y -qq ripgrep) >/dev/null 2>&1 || true
cd /home/sprite/agentsfs-chat
npm install --no-audit --no-fund >/tmp/npm.log 2>&1 || { tail -n 5 /tmp/npm.log; exit 1; }
st workspace
rm -rf /home/sprite/workspace && mkdir -p /home/sprite/workspace
mkdir -p /home/sprite/.config/agentsfs
cat > /home/sprite/.config/agentsfs/hub.json <<'HUBEOF'
{"url":%[2]q,"user":%[3]q,"token":%[4]q}
HUBEOF
chmod 600 /home/sprite/.config/agentsfs/hub.json
git config --global --add credential.helper '!afs hub credential' || true
%[1]sst service-create
cat > /home/sprite/boot-agent.sh <<'AGENTEOF'
#!/bin/sh
# Runs on every (cold-)start of the agent service (boot/cold-wake/crash — never
# mid-session), so it's the safe place to freshen things: pull the latest for
# each knowledge base and update afs from its release channel. This runs in the
# BACKGROUND so a slow/unhealthy hub can't delay the agent's health endpoint
# coming up; each step is best-effort + bounded, so a failure never blocks boot.
(
  TO="timeout 20"; command -v timeout >/dev/null 2>&1 || TO=""
  export GIT_TERMINAL_PROMPT=0
  for d in /home/sprite/workspace/*/; do
    [ -d "${d}.git" ] || continue
    (cd "$d" && $TO git pull --ff-only --quiet) >/dev/null 2>&1 || true
  done
  [ -x /home/sprite/.local/bin/afs ] && $TO /home/sprite/.local/bin/afs update --yes >/dev/null 2>&1 || true
) &
cd /home/sprite/agentsfs-chat
exec npm start
AGENTEOF
chmod +x /home/sprite/boot-agent.sh
sprite-env services create agent --cmd sh --args /home/sprite/boot-agent.sh --dir /home/sprite/agentsfs-chat --http-port 8080 --env '%[5]s'
printf '%%s' '%[6]s' > %[7]s
st done
echo AFS_BOOT_OK`, clones.String(), m.HubBase, user, token, envs, m.serviceMarker(), serviceMarkerPath)
}

// ensureSprite creates the sprite if needed; "already exists" is success.
// Unlike the old code, a real API failure (bad token, quota, 5xx) is reported
// instead of silently proceeding to exec against a sprite that was never
// created. Creation is idempotent, so plain retries are safe.
func (m *AgentManager) ensureSprite(name string) error {
	var lastErr error
	for i := 0; i < 3; i++ {
		if i > 0 {
			m.sleep(time.Duration(i) * 2 * time.Second)
		}
		resp, err := m.authed(http.MethodPost, "/sprites", strings.NewReader(fmt.Sprintf(`{"name":%q}`, name)), 30*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		if (resp.StatusCode >= 200 && resp.StatusCode < 300) || resp.StatusCode == http.StatusConflict {
			return nil
		}
		lastErr = fmt.Errorf("create sprite: http %d: %s", resp.StatusCode, snippet(scrub(string(body))))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			break // a 4xx won't get better by retrying
		}
	}
	return lastErr
}

// keepAlive keeps THIS hub machine awake while a provisioning attempt runs.
// fly.toml uses auto_stop_machines="suspend" with min_machines_running=0, so
// once the user closes the starting page there is no inbound traffic and Fly
// suspends the hub — freezing the provisioning goroutine mid-flight (observed
// in the July 2026 reprovision incident). Pinging our own public URL through
// the Fly edge counts as inbound traffic and holds the machine up; maxAge
// bounds it so a wedged attempt can't pin the machine forever.
func (m *AgentManager) keepAlive(maxAge time.Duration) (stop func()) {
	if m.HubBase == "" || strings.Contains(m.HubBase, "localhost") || strings.Contains(m.HubBase, "127.0.0.1") {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		deadline := time.Now().Add(maxAge)
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			if time.Now().After(deadline) {
				return
			}
			if resp, err := client.Get(m.HubBase + "/healthz"); err == nil {
				resp.Body.Close()
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// waitHealthy polls the agent's health endpoint until it answers or budget
// runs out. This — not any exec response — is the final arbiter of whether a
// boot produced a working agent.
func (m *AgentManager) waitHealthy(url string, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for {
		if m.healthy(url) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		m.sleep(m.pollInterval)
	}
}

// sweepStalePATs revokes automatic agent PATs superseded by a successful full
// provision. Safe because a full provision rewrote every place the workspace
// sprite holds a credential (hub.json, each clone's http.extraHeader, the
// service env) with keepID's token, and the retired per-repo sprites were the
// only consumers of "agent-sprite:*" tokens. User-named PATs are never touched.
func (m *AgentManager) sweepStalePATs(user string, keepID int64) {
	pats, err := m.Accounts.ListPATs(user)
	if err != nil {
		m.logf("agent: sweep stale PATs for %s: %v", user, err)
		return
	}
	n := 0
	for _, p := range pats {
		if p.ID == keepID {
			continue
		}
		if p.Name == "agent-user" || p.Name == "agent-reconcile" || strings.HasPrefix(p.Name, "agent-sprite:") {
			if err := m.Accounts.RevokePAT(user, p.ID); err == nil {
				n++
			}
		}
	}
	if n > 0 {
		m.logf("agent: revoked %d stale automatic PAT(s) for %s", n, user)
	}
}

// provisionUser builds the single per-user workspace sprite: it clones EVERY
// one of the user's repos as a sibling checkout under /home/sprite/workspace,
// seeds the afs hub credential, and starts the agent in workspace + shell
// mode. The long-running boot runs DETACHED on the sprite and is observed by
// polling, so losing an HTTP response no longer means losing (or worse,
// re-running) the boot; the service health endpoint is the final arbiter.
func (m *AgentManager) provisionUser(user, name string, repos []RepoRef) {
	key := "user:" + user
	t0 := time.Now()
	var patToken string
	var patID int64 = -1
	adopted := false

	defer func() {
		if r := recover(); r != nil {
			m.logf("agent: provision %s panicked: %v", key, r)
			m.finishAttempt(key, false, "internal error during provisioning")
		}
	}()
	stopKeepalive := m.keepAlive(20 * time.Minute)
	defer stopKeepalive()

	// redact this attempt's secrets from anything logged or stored, on top of
	// the generic pattern scrubber.
	redact := func(s string) string {
		if patToken != "" {
			s = strings.ReplaceAll(s, patToken, "afs_[redacted]")
			s = strings.ReplaceAll(s, base64.StdEncoding.EncodeToString([]byte(user+":"+patToken)), "[redacted]")
		}
		return scrub(s)
	}
	fail := func(stage string, err error, revokePAT bool) {
		msg := redact(fmt.Sprintf("%s: %v", stage, err))
		m.logf("agent: provision %s failed after %.0fs — %s", key, time.Since(t0).Seconds(), msg)
		if revokePAT && patID >= 0 {
			if rerr := m.Accounts.RevokePAT(user, patID); rerr != nil {
				m.logf("agent: provision %s: revoke this attempt's PAT: %v", key, rerr)
			} else {
				m.logf("agent: provision %s: revoked this attempt's PAT", key)
			}
		}
		m.finishAttempt(key, false, msg)
	}
	succeed := func(how string) {
		m.logf("agent: provisioned workspace sprite for %s (%d repos) in %.0fs%s", user, len(repos), time.Since(t0).Seconds(), how)
		if !adopted && patID >= 0 {
			m.sweepStalePATs(user, patID)
		}
		m.finishAttempt(key, true, "")
	}

	m.setStage(key, "sprite")
	if err := m.ensureSprite(name); err != nil {
		fail("create sprite", err, false)
		return
	}

	// A previous attempt's boot may still be running on this sprite (its
	// response was lost, or the hub restarted mid-attempt). Adopt it instead of
	// starting a competitor — two concurrent boots wipe each other's workspace.
	var res detachedResult
	var bootErr error
	if p, err := m.probeDetached(name, bootRunBase); err == nil && !p.done && p.running {
		adopted = true
		m.logf("agent: provision %s: adopting boot already running on %s (stage %q)", key, name, p.stage)
		m.setStage(key, "boot")
		res, bootErr = m.waitDetached(name, bootRunBase, m.bootBudget, func(s string) { m.setStage(key, s) })
	} else {
		m.setStage(key, "bundle")
		if err := m.pushBundle(name); err != nil {
			fail("upload agent bundle", err, false)
			return
		}

		// Ship the afs CLI so the agent WRAPS afs (tree, ranked/semantic search,
		// backlinks) instead of reimplementing it. Non-fatal: without it the
		// agent still runs with degraded search — never discard an otherwise
		// healthy provision over this optional tool (reconcileWorkspace retries
		// the install later).
		m.setStage(key, "afs")
		afsEnv := ""
		afsStart := time.Now()
		if m.installAfs(name) {
			afsEnv = ",AFS_BIN=/home/sprite/.local/bin/afs"
			m.logf("agent: provision %s: afs installed in %.0fs", key, time.Since(afsStart).Seconds())
		} else {
			m.logf("agent: provision %s: continuing without afs (degraded search)", key)
		}

		// Mint ONE credential — UserForToken is per-user, so a single PAT clones
		// and pushes every repo in the user's namespace. Failed attempts revoke
		// it below whenever it provably isn't referenced by a created service.
		m.setStage(key, "credentials")
		token, id, err := m.Accounts.CreatePATWithID(user, "agent-user")
		if err != nil {
			fail("mint access token", err, false)
			return
		}
		patToken, patID = token, id

		m.setStage(key, "boot")
		started, err := m.startDetached(name, bootRunBase, m.bootScript(user, token, afsEnv, repos))
		if err != nil {
			fail("start boot", err, true)
			return
		}
		if !started {
			// Lost a race with another boot starter: adopt the live run. Our
			// fresh PAT is referenced nowhere — drop it.
			adopted = true
			if patID >= 0 {
				_ = m.Accounts.RevokePAT(user, patID)
				patToken, patID = "", -1
			}
			m.logf("agent: provision %s: boot already running, adopting it", key)
		}
		res, bootErr = m.waitDetached(name, bootRunBase, m.bootBudget, func(s string) { m.setStage(key, s) })
	}

	if bootErr != nil {
		if execOutcomeUnknown(bootErr) {
			// We lost sight of the boot, not necessarily the boot itself. Ask
			// the service before declaring failure — the incident's "err=<nil>
			// out=" retries re-provisioned sprites that had already succeeded.
			m.logf("agent: provision %s: boot outcome unknown (%s); consulting service health", key, redact(bootErr.Error()))
			m.setStage(key, "health")
			if url, err := m.spriteURL(name); err == nil && url != "" && m.waitHealthy(url, m.healthWait) {
				m.rememberReadyURL(name, url)
				succeed(" (boot response lost; service verified healthy)")
				return
			}
			// Still unknown: the boot may finish later and its service would
			// hold this PAT — do NOT revoke; the next successful provision
			// sweeps superseded tokens.
			fail("boot", bootErr, false)
			return
		}
		// Definite death before the service was created (the boot deletes the
		// service first and creates it last): nothing can reference the PAT.
		fail("boot", bootErr, !adopted)
		return
	}
	if res.rc != 0 {
		fail("boot", fmt.Errorf("boot script exited rc=%d: %s", res.rc, redact(snippet(res.logTail))), !adopted)
		return
	}

	m.setStage(key, "health")
	url, urlErr := m.spriteURL(name)
	if urlErr != nil || url == "" || !m.waitHealthy(url, m.healthWait) {
		// The service WAS created and holds this attempt's PAT; revoking it
		// would strand the agent half-working if it comes up late. Leave it —
		// the next successful provision sweeps it.
		fail("service health", fmt.Errorf("service did not answer /api/health within %s", m.healthWait), false)
		return
	}
	m.rememberReadyURL(name, url)
	m.logf("agent: provision %s boot stages: %s", key, formatSpans(res.spans))
	succeed("")
}

// workspaceDirName is the checkout dir name for a ref: a user's own repo keeps
// its bare slug; a repo shared BY someone else is owner-qualified so it can't
// collide with a same-named own repo.
func workspaceDirName(user string, ref RepoRef) string {
	if ref.Owner == user {
		return ref.Repo
	}
	return ref.Owner + "--" + ref.Repo
}

// cloneRepoScript returns the guarded clone block used for both initial agent
// provisioning and later shared-repo reconciliation. The explicit owner path
// and clean hub remote are essential for collaborators: the local directory
// name must never determine where a subsequent push is published.
func cloneRepoScript(b64auth, hubBase string, ref RepoRef, dir string) string {
	return fmt.Sprintf(
		"if git -c http.extraHeader=\"Authorization: Basic %[1]s\" clone %[2]s/%[3]s/%[4]s.git %[5]s >/tmp/clone.log 2>&1; then\n"+
			"  git -C %[5]s config http.extraHeader \"Authorization: Basic %[1]s\"\n"+
			"  git -C %[5]s remote add hub %[2]s/%[3]s/%[4]s.git\n"+"  git -C %[5]s config user.name \"AgentsFS Agent\"\n"+
			"  git -C %[5]s config user.email \"agent@agentsfs.ai\"\n"+
			"else echo \"WARN: clone failed for %[3]s/%[4]s: $(tail -1 /tmp/clone.log)\"; fi\n",
		b64auth, hubBase, ref.Owner, ref.Repo, dir)
}

// reconcileWorkspace clones into an ALREADY-RUNNING sprite any of `refs` (the
// user's own + shared repos) that aren't checked out yet, so a repo created or
// shared since the sprite was provisioned appears without a full re-provision.
// Strictly additive: it never wipes or re-clones existing repos, and mints no
// credential when nothing is missing. The agent re-scans the workspace live on
// list_repos, so a fresh clone shows up on its next call — no service restart.
func (m *AgentManager) reconcileWorkspace(user, name string, refs []RepoRef) {
	key := "reconcile:" + user
	m.mu.Lock()
	if m.inflight[key] {
		m.mu.Unlock()
		return
	}
	m.inflight[key] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.inflight, key)
		m.mu.Unlock()
		if r := recover(); r != nil {
			m.Log.Printf("agent: reconcile %s panicked: %v", user, r)
		}
	}()

	// One probe collects everything reconcile cares about: the checked-out
	// repos, the service-config marker (model/effort drift), and whether the
	// optional afs CLI made it in.
	out, err := m.exec(name,
		"ls -1 /home/sprite/workspace 2>/dev/null; echo AFS_SCAN_DIVIDER; cat "+serviceMarkerPath+" 2>/dev/null; echo; echo AFS_SCAN_DIVIDER; [ -x /home/sprite/.local/bin/afs ] && echo AFS_PRESENT || echo AFS_ABSENT",
		20*time.Second)
	if err != nil {
		return // sprite busy/unreachable — next /agent load tries again
	}
	parts := strings.Split(out, "AFS_SCAN_DIVIDER")
	marker, afsPresent := "", true
	if len(parts) == 3 {
		marker = strings.TrimSpace(parts[1])
		afsPresent = strings.Contains(parts[2], "AFS_PRESENT")
	}
	have := map[string]bool{}
	for _, line := range strings.Split(parts[0], "\n") {
		if d := strings.TrimSpace(line); d != "" {
			have[d] = true
		}
	}

	// A missing afs (optional at boot, see provisionUser) is repaired here,
	// off the request path, at most once per hub process per user.
	fixedAfs := false
	if !afsPresent {
		m.mu.Lock()
		tried := m.afsFixed[user]
		m.afsFixed[user] = true
		m.mu.Unlock()
		if !tried && m.installAfs(name) {
			fixedAfs = true
			m.logf("agent: %s: installed missing afs on running sprite", user)
		}
	}

	// Config drift (deployed hub has a different model/effort than the service
	// was created with) — or a freshly repaired afs that the service env
	// doesn't reference yet — gets an in-place service update: seconds of
	// restart instead of a destructive full re-provision.
	if marker != m.serviceMarker() || fixedAfs {
		if marker != m.serviceMarker() {
			m.logf("agent: %s service config drift (have %q, want %q); updating in place", user, scrub(marker), m.serviceMarker())
		}
		if err := m.updateServiceEnv(name); err != nil {
			m.logf("agent: %s in-place service update failed: %v", user, scrub(err.Error()))
		} else {
			m.logf("agent: %s service env updated in place", user)
		}
	}
	var missing []RepoRef
	for _, ref := range refs {
		if !validSlug(ref.Owner) || !validSlug(ref.Repo) {
			continue
		}
		if !have[workspaceDirName(user, ref)] {
			missing = append(missing, ref)
		}
	}
	if len(missing) == 0 {
		return // nothing new — no credential minted, cheapest common case
	}

	token, err := m.Accounts.CreatePAT(user, "agent-reconcile")
	if err != nil {
		m.Log.Printf("agent: reconcile %s: mint token: %v", user, err)
		return
	}
	b64auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
	var script strings.Builder
	script.WriteString("mkdir -p /home/sprite/workspace\n")
	for _, ref := range missing {
		dir := "/home/sprite/workspace/" + workspaceDirName(user, ref)
		script.WriteString(cloneRepoScript(b64auth, m.HubBase, ref, dir))
	}
	if out, err := m.exec(name, script.String(), 180*time.Second); err != nil {
		m.Log.Printf("agent: reconcile %s: clone failed: %v (%s)", user, err, strings.TrimSpace(tailLines(out, 4)))
		return
	}
	m.Log.Printf("agent: reconciled workspace for %s (+%d repo(s))", user, len(missing))
}

// updateServiceEnv recreates the agent service IN PLACE with the current
// model/effort configuration, reusing the credential already on the sprite:
// the token is read from hub.json sprite-side and spliced into the env there,
// so it never transits again and no new PAT is minted. This is what makes a
// config-only change a seconds-long service restart instead of a full
// re-provision. The token check runs before the service delete, so an
// unparsable hub.json aborts with the old service still standing.
func (m *AgentManager) updateServiceEnv(name string) error {
	_, err := m.execVerified(name, m.updateServiceScript(), "AFS_ENV_UPDATED", 90*time.Second, 2)
	return err
}

func (m *AgentManager) updateServiceScript() string {
	envs := m.workspaceServiceEnv("__AFS_TOKEN__", "")
	return fmt.Sprintf(`set -e
TOKEN=$(sed -n 's/.*"token":"\([^"]*\)".*/\1/p' /home/sprite/.config/agentsfs/hub.json)
[ -n "$TOKEN" ]
ENVS=$(printf '%%s' '%s' | sed "s|__AFS_TOKEN__|$TOKEN|")
[ -x /home/sprite/.local/bin/afs ] && ENVS="$ENVS,AFS_BIN=/home/sprite/.local/bin/afs" || true
sprite-env services delete agent >/dev/null 2>&1 || true
sprite-env services create agent --cmd sh --args /home/sprite/boot-agent.sh --dir /home/sprite/agentsfs-chat --http-port 8080 --env "$ENVS"
printf '%%s' '%s' > %s
echo AFS_ENV_UPDATED`, envs, m.serviceMarker(), serviceMarkerPath)
}

// tailLines returns the last n lines of s (for concise failure logging).
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
