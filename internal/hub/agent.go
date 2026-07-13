package hub

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
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
	inflight map[string]bool   // "user/repo" currently provisioning
	ready    map[string]string // sprite name -> URL after one successful health check
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
	req, err := http.NewRequest(method, m.spritesBase+path, body)
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

// exec runs a shell script inside the sprite and returns its combined output.
func (m *AgentManager) exec(name, script string, timeout time.Duration) (string, error) {
	req, err := http.NewRequest(http.MethodPost, m.spritesBase+"/sprites/"+name+"/exec?cmd=sh&stdin=true", strings.NewReader(script))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+m.Token)
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return string(out), nil
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
		// Sprite already up: reconcile its workspace in the background so a repo
		// created or shared since it was provisioned gets cloned in (the agent
		// re-scans the workspace live, so it shows up without a re-provision).
		go m.reconcileWorkspace(user, name, repos)
		return url, true
	}
	// Existing Sprites are persistent and commonly need longer than one health
	// timeout to wake. Never turn that ambiguous state into a destructive full
	// reprovision; the next poll will check health again.
	if url != "" {
		return url, false
	}
	key := "user:" + user
	m.mu.Lock()
	if !m.inflight[key] {
		m.inflight[key] = true
		go m.provisionUser(user, name, repos)
	}
	m.mu.Unlock()
	return url, false
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
	m.Log.Printf("agent: curl-install of afs failed on %s; falling back to embedded binary", name)
	if err := m.uploadAfs(name); err != nil {
		m.Log.Printf("agent: afs unavailable on %s (installer + embedded fallback both failed): %v", name, err)
		return false
	}
	return true
}

// uploadAfs pushes the linux afs binary into the sprite. The exec body caps
// around a few MB, so gzip the binary and stream it in small base64 chunks,
// then decode and sha256-verify inside the sprite.
func (m *AgentManager) uploadAfs(name string) error {
	bin, err := os.ReadFile(m.AfsBin)
	if err != nil {
		return err // not in the image (e.g. local dev) — caller treats as non-fatal
	}
	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if _, err := zw.Write(bin); err != nil {
		return err
	}
	zw.Close()
	b64 := base64.StdEncoding.EncodeToString(gz.Bytes())

	if _, err := m.exec(name, "mkdir -p /home/sprite/.local/bin; : > /tmp/afs.gz.b64", 30*time.Second); err != nil {
		return err
	}
	const chunk = 700_000
	for i := 0; i < len(b64); i += chunk {
		end := i + chunk
		if end > len(b64) {
			end = len(b64)
		}
		script := "cat >> /tmp/afs.gz.b64 <<'CHUNKEOF'\n" + b64[i:end] + "\nCHUNKEOF"
		if _, err := m.exec(name, script, 60*time.Second); err != nil {
			return err
		}
	}
	sum := sha256.Sum256(bin)
	want := hex.EncodeToString(sum[:])
	out, err := m.exec(name, "base64 -d /tmp/afs.gz.b64 | gunzip > /home/sprite/.local/bin/afs && chmod +x /home/sprite/.local/bin/afs && rm -f /tmp/afs.gz.b64; sha256sum /home/sprite/.local/bin/afs | cut -d' ' -f1", 60*time.Second)
	if err != nil {
		return err
	}
	if !strings.Contains(out, want) {
		return fmt.Errorf("afs upload sha mismatch")
	}
	return nil
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

// pushBundle uploads the embedded agentsfs-chat source + the linux afs binary
// into a sprite. Shared by provision (per-repo) and provisionUser (per-user).
// Returns the AFS_BIN env fragment ("" if the binary wasn't shipped).
func (m *AgentManager) pushBundle(name, key string) (afsEnv string, err error) {
	src := "rm -rf /home/sprite/agentsfs-chat && mkdir -p /home/sprite/agentsfs-chat && base64 -d > /tmp/b.tgz <<'BEOF'\n" +
		base64.StdEncoding.EncodeToString(agentBundle) + "\nBEOF\ntar xzf /tmp/b.tgz -C /home/sprite/agentsfs-chat && rm /tmp/b.tgz && echo ok"
	if _, err := m.exec(name, src, 90*time.Second); err != nil {
		return "", fmt.Errorf("upload source: %w", err)
	}
	if !m.installAfs(name) {
		return "", nil // agent still runs, with degraded (afs-less) search
	}
	return ",AFS_BIN=/home/sprite/.local/bin/afs", nil
}

// provisionUser builds the single per-user workspace sprite: it clones EVERY one
// of the user's repos as a sibling checkout under /home/sprite/workspace, seeds
// the afs hub credential so the agent can `afs hub pull`/`list`, and starts the
// agent in workspace + shell mode (it boots focused on one repo, asks which the
// user wants, and can switch + run bash across all of them).
func (m *AgentManager) provisionUser(user, name string, repos []RepoRef) {
	key := "user:" + user
	defer func() {
		m.mu.Lock()
		delete(m.inflight, key)
		m.mu.Unlock()
		if r := recover(); r != nil {
			m.Log.Printf("agent: provisionUser %s panicked: %v", user, r)
		}
	}()

	// 1. Create the sprite (409/"exists" is fine — reuse it).
	if resp, err := m.authed(http.MethodPost, "/sprites", strings.NewReader(fmt.Sprintf(`{"name":%q}`, name)), 30*time.Second); err == nil {
		resp.Body.Close()
	} else {
		m.Log.Printf("agent: create sprite %s: %v", name, err)
		return
	}

	// 2. Mint ONE credential — UserForToken is per-user, so a single PAT clones
	//    and pushes every repo in the user's namespace.
	token, err := m.Accounts.CreatePAT(user, "agent-user")
	if err != nil {
		m.Log.Printf("agent: mint token %s: %v", key, err)
		return
	}
	b64auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))

	// 3. Push the agentsfs-chat source + afs binary.
	afsEnv, err := m.pushBundle(name, key)
	if err != nil {
		m.Log.Printf("agent: %s: %v", key, err)
		return
	}

	// 4. Build the clone commands — one checkout per repo under the workspace.
	//    repo names come from ListRepos (filesystem dir names); re-validate as
	//    slugs before interpolating them into the shell, defense-in-depth. Each
	//    clone is guarded by `if … then … fi` so ONE failing repo (transient
	//    network, mid-deletion) can't abort the whole boot under `set -e` and
	//    brick every repo — a bad repo just gets skipped with a WARN.
	var clones strings.Builder
	for _, ref := range repos {
		if !validSlug(ref.Owner) || !validSlug(ref.Repo) {
			continue
		}
		// The single user PAT authenticates all clones — for shared repos it
		// presents the user as a collaborator, which the hub's git auth accepts.
		dir := "/home/sprite/workspace/" + workspaceDirName(user, ref)
		// Test git's OWN exit (write to a log, don't pipe) — piping to `tail`
		// would make the `if` see tail's exit (always 0), defeating the guard.
		clones.WriteString(cloneRepoScript(b64auth, m.HubBase, ref, dir))
	}

	// 5. Service env: workspace + shell mode, no single-repo pinning. AGENTSFS_ROOT
	//    is the workspace dir itself (always present, even if a clone failed or the
	//    user has zero repos) — the agent boots unfocused, lists the repos, and
	//    asks which to work in. PATH puts the shipped afs on the path;
	//    XDG_CONFIG_HOME points the afs hub client at the seeded hub.json.
	//    NO OpenAI key here: model calls go through the hub's /v1/agent-llm proxy,
	//    authenticated by the per-user PAT (which is only this user's own
	//    credential), so the shared model key never lives in the sprite.
	// NB: don't set PATH — the sprite's default PATH already includes both
	// /home/sprite/.local/bin (the shipped afs) and /.sprite/bin (node/npm).
	// Overriding it drops /.sprite/bin and the service can't find npm.
	envs := fmt.Sprintf(
		"PORT=8080,HOST=0.0.0.0,HOME=/home/sprite,"+
			"XDG_CONFIG_HOME=/home/sprite/.config,AGENTSFS_MODE=workspace,AGENTSFS_WORKSPACE=/home/sprite/workspace,"+
			"AGENTSFS_SEARCH_DIR=/home/sprite/workspace,AGENTSFS_ROOT=/home/sprite/workspace,AGENTSFS_ALLOW_WRITES=1,AGENTSFS_ALLOW_SHELL=1,"+
			"AGENTSFS_PREVIEW_DIR=/home/sprite/workspace/.preview,AGENTSFS_DATA_DIR=/home/sprite/.agentsfs-chat,"+
			"AGENTSFS_AGENT_NAME=AgentsFS Agent,AGENTSFS_AGENT_EMAIL=agent@agentsfs.ai,CHAT_MODEL=%s,CHAT_REASONING_EFFORT=%s,"+
			"AGENTSFS_LLM_BASE_URL=%s/v1/agent-llm,AGENTSFS_LLM_KEY=%s"+afsEnv,
		m.ChatModel, m.ChatReasoningEffort, m.HubBase, token)

	// `set -e` guards the always-required steps (deps, hub.json, service create);
	// the clone loop is self-guarded above. A unique sentinel confirms the script
	// ran to completion — m.exec can't see the remote exit code, so without this a
	// half-failed boot would be logged as success.
	boot := fmt.Sprintf(`set -e
command -v rg >/dev/null 2>&1 || (sudo apt-get update -qq && sudo apt-get install -y -qq ripgrep) >/dev/null 2>&1 || true
cd /home/sprite/agentsfs-chat && npm install --no-audit --no-fund >/tmp/npm.log 2>&1 || tail -5 /tmp/npm.log
rm -rf /home/sprite/workspace && mkdir -p /home/sprite/workspace
%[1]smkdir -p /home/sprite/.config/agentsfs
cat > /home/sprite/.config/agentsfs/hub.json <<'HUBEOF'
{"url":%[2]q,"user":%[3]q,"token":%[4]q}
HUBEOF
chmod 600 /home/sprite/.config/agentsfs/hub.json
git config --global --add credential.helper '!afs hub credential' || true
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
sprite-env services delete agent >/dev/null 2>&1 || true
sprite-env services create agent --cmd sh --args /home/sprite/boot-agent.sh --dir /home/sprite/agentsfs-chat --http-port 8080 --env '%[5]s'
echo AFS_BOOT_OK`, clones.String(), m.HubBase, user, token, envs)
	out, err := m.exec(name, boot, 480*time.Second)
	if err != nil || !strings.Contains(out, "AFS_BOOT_OK") {
		m.Log.Printf("agent: boot %s failed: err=%v out=%s", key, err, strings.TrimSpace(tailLines(out, 8)))
		return
	}
	m.Log.Printf("agent: provisioned workspace sprite for %s (%d repos)", user, len(repos))
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

	out, err := m.exec(name, "ls -1 /home/sprite/workspace 2>/dev/null", 20*time.Second)
	if err != nil {
		return // sprite busy/unreachable — next /agent load tries again
	}
	have := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if d := strings.TrimSpace(line); d != "" {
			have[d] = true
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

// tailLines returns the last n lines of s (for concise failure logging).
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
