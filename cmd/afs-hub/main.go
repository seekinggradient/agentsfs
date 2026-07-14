// Command afs-hub is the agentsfs Hub server (Phase 0): a real-git remote you
// can `git push` to and `git clone` from. It runs the git-http-backend CGI
// over bare repositories, so what it stores is genuine git and a plain clone
// is always a complete exit ramp. Storage is local disk today; the Storage
// interface is where an R2/S3 backend plugs in later.
//
// Usage:
//
//	afs-hub --addr :8080 --dir ./afs-hub-data --token alice:$(openssl rand -hex 16)
//
// A client then clones/pushes with the token as the HTTP password:
//
//	git clone http://alice:TOKEN@localhost:8080/alice/brain.git
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentsfs.ai/afs/internal/hub"
)

type tokenFlags []string

func (t *tokenFlags) String() string { return strings.Join(*t, ",") }
func (t *tokenFlags) Set(v string) error {
	*t = append(*t, v)
	return nil
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dir := flag.String("dir", "./afs-hub-data", "directory holding bare repositories")
	backend := flag.String("git-http-backend", "", "path to git-http-backend (auto-discovered if empty)")
	var tokens tokenFlags
	flag.Var(&tokens, "token", "grant access as user:token (repeatable); also read from AFS_HUB_TOKENS")
	flag.Parse()

	store, err := hub.NewLocalStorage(*dir)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	ts := hub.NewTokenStore()
	specs := append(splitEnvTokens(os.Getenv("AFS_HUB_TOKENS")), []string(tokens)...)
	for _, spec := range specs {
		user, tok, ok := strings.Cut(spec, ":")
		if !ok || user == "" || tok == "" {
			log.Fatalf("bad token %q: want user:token", spec)
		}
		ts.Add(user, tok)
	}
	srv, err := hub.New(store, ts, *backend)
	if err != nil {
		log.Fatalf("server: %v", err)
	}

	// Accounts: a small SQLite store on the same volume for usernames, argon2
	// password hashes, and per-account git tokens (PATs). Self-serve signup +
	// password login for the web; PATs (or the bootstrap env tokens) for git.
	acc, err := hub.OpenAccounts(filepath.Join(store.Root(), "afs-hub.db"))
	if err != nil {
		log.Fatalf("accounts: %v", err)
	}
	// Bootstrap: ensure an account exists for each env-configured user so its
	// namespace has an owner and it can set a password / mint tokens. The env
	// token keeps authenticating it via the token fallback, so nothing breaks.
	for _, spec := range specs {
		if u, _, ok := strings.Cut(spec, ":"); ok && u != "" && !acc.Exists(u) {
			if _, err := acc.CreateUser(u, "", ""); err != nil {
				log.Printf("bootstrap account %q: %v", u, err)
			}
		}
	}
	srv.Accounts = acc

	// Signup allowlist: seed the invite list from AFS_HUB_ALLOWLIST (comma-
	// separated emails). A non-empty allowlist flips signup to invite-only —
	// invited emails create accounts, everyone else lands on the waitlist. The
	// list is declarative here but also extendable from /admin/access, so seeds
	// are additive (never removes an email an admin added later).
	for _, email := range strings.Split(os.Getenv("AFS_HUB_ALLOWLIST"), ",") {
		if email = strings.TrimSpace(email); email != "" {
			if err := acc.AllowEmail(email); err != nil {
				log.Printf("allowlist seed %q: %v", email, err)
			}
		}
	}

	// Agent-in-a-Sprite: when SPRITES_TOKEN + OPENAI_API_KEY are set, each repo
	// gets a "talk to an agent" button that provisions a write-capable agent in
	// a Fly Sprite. Disabled (button hidden) when unset.
	srv.Agent = hub.NewAgentManager(
		os.Getenv("SPRITES_TOKEN"),
		os.Getenv("OPENAI_API_KEY"),
		os.Getenv("CHAT_MODEL"),
		os.Getenv("HUB_PUBLIC_URL"),
		acc,
		log.Default(),
	)
	// Local development only: proxy /agent/* to a locally running agentsfs-chat
	// instead of provisioning sprites, so the full hub+agent experience can be
	// exercised on a laptop. Never set this on a deployed hub.
	if dev := os.Getenv("HUB_AGENT_DEV_URL"); dev != "" {
		srv.Agent.DevURL = strings.TrimRight(dev, "/")
		log.Printf("agent DEV override: proxying /agent/* to %s (no sprites)", srv.Agent.DevURL)
	}
	// Hosted-Eve upstream mode (docs/eve-hub-integration.md): when
	// HUB_EVE_AGENT_URL is set, /agent/* reverse-proxies to a trusted
	// Vercel-hosted Eve deployment instead of provisioning per-user sprites. The
	// shared secret signs the identity handoff the Eve app verifies. Unset ==
	// today's sprite path, unchanged.
	if eve := os.Getenv("HUB_EVE_AGENT_URL"); eve != "" {
		srv.Agent.EveURL = strings.TrimRight(eve, "/")
		srv.Agent.EveSecret = os.Getenv("HUB_EVE_AGENT_SECRET")
		if srv.Agent.EveSecret == "" {
			log.Print("WARNING: HUB_EVE_AGENT_URL set without HUB_EVE_AGENT_SECRET; identity handoff is unsigned")
		}
		log.Printf("agent EVE upstream: proxying /agent/* to %s (no sprites)", srv.Agent.EveURL)
	}
	if srv.Agent.Enabled() {
		log.Print("agent-in-a-sprite enabled")
	}

	// Operator observability: meter every model call the LLM proxy handles into a
	// SQLite store on the same volume, viewable at /admin/metrics by HUB_ADMIN_USER.
	if mets, err := hub.OpenMetrics(filepath.Join(store.Root(), "afs-hub-metrics.db")); err != nil {
		log.Printf("metrics: %v (disabled)", err)
	} else {
		srv.Metrics = mets
		srv.AdminUser = os.Getenv("HUB_ADMIN_USER")
		if srv.AdminUser != "" {
			log.Printf("operator metrics enabled; /admin/metrics visible to %q", srv.AdminUser)
		}
	}

	if os.Getenv("AFS_HUB_OPEN_SIGNUP") == "false" {
		hub.SetSignupOpen(false)
	}
	if ts.Len() == 0 {
		log.Print("no bootstrap tokens set; users sign up via the web (or set AFS_HUB_TOKENS)")
	}

	// ReadHeaderTimeout defends against slow-header (slowloris) clients;
	// IdleTimeout reaps idle keep-alives. Read/Write timeouts are deliberately
	// left unset — git clone/push of a large repo is a legitimately long
	// request/response and must not be cut off mid-transfer.
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("afs-hub listening on %s, repos in %s", *addr, store.Root())
	log.Fatal(httpSrv.ListenAndServe())
}

// splitEnvTokens parses a comma-separated AFS_HUB_TOKENS value into specs.
func splitEnvTokens(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}
