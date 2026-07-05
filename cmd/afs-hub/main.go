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
	if ts.Len() == 0 {
		log.Print("warning: no tokens configured; every request will be rejected. Pass --token user:token")
	}

	srv, err := hub.New(store, ts, *backend)
	if err != nil {
		log.Fatalf("server: %v", err)
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
