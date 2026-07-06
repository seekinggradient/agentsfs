// Package hubclient is the shared client for connecting an agentsfs instance to
// a hosted agentsfs Hub and uploading it. Both the CLI (`afs hub`) and the MCP
// server use it. The hub is just a git remote that stores real git, so this is
// convenience over `git remote add` + `git push` — never load-bearing.
package hubclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultURL is the hosted hub used when none is configured.
const DefaultURL = "https://hub.agentsfs.ai"

// ErrNotSignedIn means no hub login is saved on this machine.
var ErrNotSignedIn = errors.New("not signed in to a hub — run `afs hub login` first")

// Config is the saved hub sign-in. It lives at <config>/agentsfs/hub.json
// (0600) — the user's config dir, never inside an agentsfs repo. The token is
// stored so pushes work non-interactively (e.g. from an agent via MCP).
type Config struct {
	URL   string `json:"url"`
	User  string `json:"user"`
	Token string `json:"token"`
}

func ConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	return filepath.Join(base, "agentsfs", "hub.json")
}

func Load() (Config, error) {
	var c Config
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(data, &c)
}

func Save(c Config) error {
	p := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

func Forget() error { return os.Remove(ConfigPath()) }

// Verify reports whether a username + token authenticate to a hub (its owner
// can load their dashboard).
func Verify(url, user, token string) bool {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(url, "/")+"/"+user, nil)
	if err != nil {
		return false
	}
	req.SetBasicAuth(user, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Repo is a repository as reported by the hub's JSON listing.
type Repo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Notes       int    `json:"notes"`
	Public      bool   `json:"public"`
	Updated     string `json:"updated"`
	URL         string `json:"url"`
	CloneURL    string `json:"clone_url"`
}

// List returns every repository in the signed-in user's hub account.
func List() ([]Repo, error) {
	cfg, err := Load()
	if err != nil {
		return nil, ErrNotSignedIn
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(cfg.URL, "/")+"/"+cfg.User+"?format=json", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(cfg.User, cfg.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the hub returned %s — is your sign-in still valid? try `afs hub login`", resp.Status)
	}
	var body struct {
		User  string `json:"user"`
		Repos []Repo `json:"repos"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Repos, nil
}

// Slugify turns any name into a valid hub slug: lowercase letters/digits joined
// by single hyphens.
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
		} else if b.Len() > 0 && !prevHyphen {
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// PushResult describes a completed upload.
type PushResult struct {
	Slug, Remote, ViewURL, Branch string
}

// Push links root's git repo to the signed-in hub as name (default: the folder
// name) and pushes the current branch. It sets a clean "hub" remote (no token)
// and pushes over an authenticated URL so the token is never written into the
// repo. Repeatable to sync updates.
func Push(root, name string) (PushResult, error) {
	var res PushResult
	cfg, err := Load()
	if err != nil {
		return res, ErrNotSignedIn
	}
	if name == "" {
		name = filepath.Base(root)
	}
	slug := Slugify(name)
	if slug == "" {
		return res, errors.New("could not derive a valid slug from the name; pass one explicitly")
	}
	if git(root, "rev-parse", "--git-dir") != nil {
		return res, fmt.Errorf("%s is not a git repository; run `git init` (or `afs init`) first", root)
	}
	if git(root, "rev-parse", "--verify", "HEAD") != nil {
		return res, errors.New("nothing to upload yet — commit first: git add -A . && git commit -m 'Seed'")
	}
	branch := currentBranch(root)
	base := strings.TrimRight(cfg.URL, "/")
	remote := fmt.Sprintf("%s/%s/%s.git", base, cfg.User, slug)
	if err := setRemote(root, "hub", remote); err != nil {
		return res, fmt.Errorf("setting the hub remote: %w", err)
	}
	u, _ := neturl.Parse(remote)
	u.User = neturl.UserPassword(cfg.User, cfg.Token)
	cmd := exec.Command("git", "-C", root, "push", u.String(), "HEAD:refs/heads/"+branch)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return res, fmt.Errorf("push to the hub failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return PushResult{Slug: slug, Remote: remote, ViewURL: base + "/" + cfg.User + "/" + slug, Branch: branch}, nil
}

// StatusInfo summarizes the hub sign-in and, if root is set, whether that
// instance is linked.
type StatusInfo struct {
	SignedIn  bool
	URL, User string
	Linked    bool
	LinkedURL string
}

func GetStatus(root string) StatusInfo {
	var s StatusInfo
	if c, err := Load(); err == nil {
		s.SignedIn, s.URL, s.User = true, c.URL, c.User
	}
	if root != "" {
		if out, err := exec.Command("git", "-C", root, "remote", "get-url", "hub").Output(); err == nil {
			if u := strings.TrimSpace(string(out)); u != "" {
				s.Linked, s.LinkedURL = true, u
			}
		}
	}
	return s
}

func git(root string, args ...string) error {
	return exec.Command("git", append([]string{"-C", root}, args...)...).Run()
}

func currentBranch(root string) string {
	out, err := exec.Command("git", "-C", root, "symbolic-ref", "--short", "HEAD").Output()
	if b := strings.TrimSpace(string(out)); err == nil && b != "" {
		return b
	}
	return "main"
}

func setRemote(root, name, url string) error {
	if exec.Command("git", "-C", root, "remote", "get-url", name).Run() == nil {
		return exec.Command("git", "-C", root, "remote", "set-url", name, url).Run()
	}
	return exec.Command("git", "-C", root, "remote", "add", name, url).Run()
}
