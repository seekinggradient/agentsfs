package hub

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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

type AgentManager struct {
	Token     string // SPRITES_TOKEN
	OpenAIKey string
	ChatModel string
	HubBase   string // public URL the sprite clones from, e.g. https://hub.agentsfs.ai
	Accounts  *AccountStore
	Log       *log.Logger

	mu       sync.Mutex
	inflight map[string]bool // "user/repo" currently provisioning
}

func NewAgentManager(token, openaiKey, chatModel, hubBase string, accounts *AccountStore, logger *log.Logger) *AgentManager {
	if chatModel == "" {
		chatModel = "gpt-5.1"
	}
	if hubBase == "" {
		hubBase = "https://hub.agentsfs.ai"
	}
	return &AgentManager{
		Token: token, OpenAIKey: openaiKey, ChatModel: chatModel,
		HubBase: strings.TrimRight(hubBase, "/"), Accounts: accounts, Log: logger,
		inflight: map[string]bool{},
	}
}

// Enabled reports whether the agent feature is configured (tokens present).
func (m *AgentManager) Enabled() bool {
	return m != nil && m.Token != "" && m.OpenAIKey != "" && m.Accounts != nil
}

var agentNameRe = regexp.MustCompile(`[^a-z0-9-]+`)

func agentSpriteName(user, repo string) string {
	s := agentNameRe.ReplaceAllString(strings.ToLower(user+"-"+repo), "-")
	return "afs-" + strings.Trim(s, "-")
}

func (m *AgentManager) authed(method, path string, body io.Reader, timeout time.Duration) (*http.Response, error) {
	req, err := http.NewRequest(method, spritesAPI+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return (&http.Client{Timeout: timeout}).Do(req)
}

// spriteURL returns a sprite's public URL, or "" if it doesn't exist.
func (m *AgentManager) spriteURL(name string) string {
	resp, err := m.authed(http.MethodGet, "/sprites/"+name, nil, 15*time.Second)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var d struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&d)
	return d.URL
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

// exec runs a shell script inside the sprite and returns its combined output.
func (m *AgentManager) exec(name, script string, timeout time.Duration) (string, error) {
	req, err := http.NewRequest(http.MethodPost, spritesAPI+"/sprites/"+name+"/exec?cmd=sh&stdin=true", strings.NewReader(script))
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
	url = m.spriteURL(name)
	if url != "" && m.healthy(url) {
		return url, true
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

	// 4. Install deps, clone the repo (with the scoped credential), start the
	//    persistent agent service. rg powers search; git config carries the
	//    push credential so the agent can push its commits back.
	envs := fmt.Sprintf("PORT=8080,HOST=0.0.0.0,AGENTSFS_ROOT=/home/sprite/wiki,AGENTSFS_ALLOW_WRITES=1,AGENTSFS_AGENT_NAME=AgentsFS Agent,AGENTSFS_AGENT_EMAIL=agent@agentsfs.ai,CHAT_MODEL=%s,OPENAI_API_KEY=%s", m.ChatModel, m.OpenAIKey)
	boot := fmt.Sprintf(`set -e
command -v rg >/dev/null 2>&1 || (sudo apt-get update -qq && sudo apt-get install -y -qq ripgrep) >/dev/null 2>&1 || true
cd /home/sprite/agentsfs-chat && npm install --no-audit --no-fund >/tmp/npm.log 2>&1 || tail -5 /tmp/npm.log
rm -rf /home/sprite/wiki
git -c http.extraHeader="Authorization: Basic %s" clone %s/%s/%s.git /home/sprite/wiki 2>&1 | tail -1
git -C /home/sprite/wiki config http.extraHeader "Authorization: Basic %s"
git -C /home/sprite/wiki config user.name "AgentsFS Agent"
git -C /home/sprite/wiki config user.email "agent@agentsfs.ai"
sprite-env services delete agent >/dev/null 2>&1 || true
sprite-env services create agent --cmd npm --args start --dir /home/sprite/agentsfs-chat --http-port 8080 --env '%s' >/dev/null 2>&1
echo done`, b64auth, m.HubBase, user, repo, b64auth, envs)
	if out, err := m.exec(name, boot, 360*time.Second); err != nil {
		m.Log.Printf("agent: boot %s: %v (%s)", key, err, strings.TrimSpace(out))
		return
	}
	m.Log.Printf("agent: provisioned %s/%s", user, repo)
}
