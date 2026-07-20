// Package hubclient is the shared client for connecting an agentsfs instance to
// a hosted agentsfs Hub and uploading it. Both the CLI (`afs hub`) and the MCP
// server use it. The hub is just a git remote that stores real git, so this is
// convenience over `git remote add` + `git push` — never load-bearing.
package hubclient

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

// DefaultURL is the hosted hub used when none is configured.
const DefaultURL = "https://hub.agentsfs.ai"

// ErrNotSignedIn means no hub login is saved on this machine.
var ErrNotSignedIn = errors.New("not signed in to a hub — run `afs hub login` first")

const gitCredentialHelper = "!afs hub credential"

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

// EnsureCredentialHelper installs the AFS-backed Git credential helper without
// disturbing any helpers the user already has configured. The helper reads the
// token from hub.json, so Git can authenticate normal fetch/pull/push commands
// without copying the token into a repository's .git/config.
func EnsureCredentialHelper() error {
	out, err := exec.Command("git", "config", "--global", "--get-all", "credential.helper").Output()
	if err == nil {
		for _, helper := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(helper) == gitCredentialHelper {
				return nil
			}
		}
	}
	return exec.Command("git", "config", "--global", "--add", "credential.helper", gitCredentialHelper).Run()
}

// HandleCredential implements Git's credential-helper protocol. The token is
// only returned for the configured Hub host (and path prefix, when present).
// Git's store and erase actions are intentionally no-ops because hub.json is
// the source of truth for the AFS credential.
func HandleCredential(action string, in io.Reader, out io.Writer) error {
	if action != "get" {
		return nil
	}
	cfg, err := Load()
	if err != nil || cfg.URL == "" || cfg.User == "" || cfg.Token == "" {
		return nil // abstain; another configured Git helper may answer
	}
	base, err := neturl.Parse(cfg.URL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil
	}
	fields := make(map[string]string)
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			fields[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if fields["protocol"] != base.Scheme || fields["host"] != base.Host {
		return nil
	}
	basePath := strings.TrimSuffix(strings.TrimSpace(base.Path), "/")
	requestPath := strings.TrimSuffix(strings.TrimSpace(fields["path"]), "/")
	if basePath != "" && requestPath != basePath && !strings.HasPrefix(requestPath, basePath+"/") {
		return nil
	}
	_, err = fmt.Fprintf(out, "protocol=%s\nhost=%s\nusername=%s\npassword=%s\n\n", base.Scheme, base.Host, cfg.User, cfg.Token)
	return err
}

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

// Repo is a repository as reported by the hub's JSON listing. Owner identifies
// the namespace; Shared and Role are set when another account shared it with
// the signed-in user.
type Repo struct {
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Notes       int    `json:"notes"`
	Public      bool   `json:"public"`
	Updated     string `json:"updated"`
	URL         string `json:"url"`
	CloneURL    string `json:"clone_url"`
	Role        string `json:"role,omitempty"`
	Shared      bool   `json:"shared,omitempty"`
}

// List returns every repository visible to the signed-in user, including
// repositories owned by other accounts that have been shared with them.
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
	// Keep the client compatible with older hubs that did not include owner in
	// the response; a missing owner can only mean the signed-in namespace.
	for i := range body.Repos {
		if body.Repos[i].Owner == "" {
			body.Repos[i].Owner = body.User
		}
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

// Push links root's git repo to the signed-in hub and pushes the current
// branch. Shared instances are split out of their enclosing repository first,
// so application files outside the AgentsFS root are never uploaded. When name
// is empty and this instance is already linked (a "hub"
// remote exists), it re-pushes to that same repo — so renaming the local folder
// can't silently spawn a duplicate hub entry. Otherwise the target repo is
// name (or the folder name). It sets a clean "hub" remote (no token) and pushes
// over an authenticated URL so the token is never written into the repo.
// Repeatable to sync updates.
func Push(root, name string) (PushResult, error) {
	var res PushResult
	cfg, err := Load()
	if err != nil {
		return res, ErrNotSignedIn
	}
	if git(root, "rev-parse", "--git-dir") != nil {
		return res, fmt.Errorf("%s is not a git repository; run `git init` (or `afs init`) first", root)
	}
	if git(root, "rev-parse", "--verify", "HEAD") != nil {
		return res, errors.New("nothing to upload yet — review and commit the AgentsFS files first")
	}

	base := strings.TrimRight(cfg.URL, "/")
	owner := cfg.User
	var slug, remote string

	// Already linked and no explicit name? Keep pushing to the same repo,
	// identified by the existing "hub" remote — not by the folder's name.
	if name == "" {
		if existing := hubRemoteURL(root); existing != "" {
			remote = existing
			if o, s, ok := parseRepoURL(existing); ok {
				owner, slug = o, s
			}
		}
	}
	if remote == "" {
		if name == "" {
			name = filepath.Base(root)
		}
		slug = Slugify(name)
		if slug == "" {
			return res, errors.New("could not derive a valid slug from the name; pass one explicitly")
		}
		remote = fmt.Sprintf("%s/%s/%s.git", base, owner, slug)
	}

	branch := currentBranch(root)
	if err := setRemote(root, "hub", remote); err != nil {
		return res, fmt.Errorf("setting the hub remote: %w", err)
	}
	revision, err := revisionForPush(root)
	if err != nil {
		return res, err
	}
	u, _ := neturl.Parse(remote)
	u.User = neturl.UserPassword(cfg.User, cfg.Token)
	cmd := exec.Command("git", "-C", root, "push", u.String(), revision+":refs/heads/"+branch)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return res, fmt.Errorf("push to the hub failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return PushResult{Slug: slug, Remote: remote, ViewURL: strings.TrimSuffix(remote, ".git"), Branch: branch}, nil
}

// revisionForPush returns the commit that represents exactly this AgentsFS
// instance. A standalone instance can push HEAD directly. A shared instance
// lives below an application repository's root, so pushing HEAD would expose
// the entire host repository; git-subtree derives an AgentsFS-only history
// whose tree is rooted at the instance instead.
func revisionForPush(root string) (string, error) {
	instance, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving AgentsFS root: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(instance); resolveErr == nil {
		instance = resolved
	}
	out, err := exec.Command("git", "-C", instance, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("finding enclosing git repository: %w", err)
	}
	repo, err := filepath.Abs(strings.TrimSpace(string(out)))
	if err != nil {
		return "", fmt.Errorf("resolving enclosing git repository: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(repo); resolveErr == nil {
		repo = resolved
	}
	if instance == repo {
		return "HEAD", nil
	}

	prefix, err := filepath.Rel(repo, instance)
	if err != nil || prefix == "." || prefix == ".." || strings.HasPrefix(prefix, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("AgentsFS root %s is not contained by git repository %s", instance, repo)
	}
	cmd := exec.Command("git", "-C", repo, "subtree", "split", "-q", "--prefix="+filepath.ToSlash(prefix), "HEAD")
	split, err := cmd.Output()
	if err != nil {
		detail := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			detail = strings.TrimSpace(string(exitErr.Stderr))
		}
		if detail != "" {
			return "", fmt.Errorf("isolating shared AgentsFS history failed: %v: %s", err, detail)
		}
		return "", fmt.Errorf("isolating shared AgentsFS history failed: %w", err)
	}
	revision := strings.TrimSpace(string(split))
	if revision == "" || git(repo, "cat-file", "-e", revision+"^{commit}") != nil {
		return "", errors.New("isolating shared AgentsFS history produced no valid commit")
	}
	return revision, nil
}

// hubRemoteURL returns the "hub" remote's URL for root, or "" if not linked.
func hubRemoteURL(root string) string {
	out, err := exec.Command("git", "-C", root, "remote", "get-url", "hub").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// parseRepoURL pulls owner + slug out of a hub repo URL
// (<base>/<owner>/<slug>.git).
func parseRepoURL(raw string) (owner, slug string, ok bool) {
	u, err := neturl.Parse(raw)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	owner = parts[len(parts)-2]
	slug = strings.TrimSuffix(parts[len(parts)-1], ".git")
	if owner == "" || slug == "" {
		return "", "", false
	}
	return owner, slug, true
}

// ParseRef splits a pull target into owner + slug. name is "<slug>" (resolved
// against defaultUser, the signed-in account) or "<user>/<slug>". The slug is
// normalized; owner is taken verbatim.
func ParseRef(name, defaultUser string) (owner, slug string, err error) {
	owner, slug = defaultUser, name
	if u, s, ok := strings.Cut(name, "/"); ok {
		owner, slug = u, s
	}
	owner = strings.TrimSpace(owner)
	slug = Slugify(slug)
	if owner == "" || slug == "" {
		return "", "", errors.New("give a repo name like `notes` or `someone/notes`")
	}
	return owner, slug, nil
}

// CloneResult describes a completed pull.
type CloneResult struct {
	Owner, Slug, Dir, ViewURL string
	Updated                   bool // pulled an existing checkout rather than cloning fresh
	Merged                    bool // folded the repo's files into an existing instance

	// Merge report. Paths are slash-relative to the target instance root (Dir).
	Added     []string // remote-only files folded in
	Skipped   []string // files byte-identical to the local copy, left as-is
	Conflicts []string // files that differed; the remote copy was quarantined, the local copy untouched
	Symlinks  []string // symlinks in the remote, never folded (a fold would materialize the link's local target as content)
	// QuarantinePath is where quarantined remote copies were written, relative
	// to Dir (set only when Conflicts is non-empty).
	QuarantinePath string
}

// Clone downloads a hub repo into a local directory. name is "<slug>" (the
// signed-in account) or "<user>/<slug>". dir defaults to ./<slug>. If dir
// already holds a git clone it pulls (--ff-only) instead, so `afs hub pull` is
// a safe, re-runnable "get me this knowledgebase here". The saved token is used
// via a one-shot auth header so it is never written into the cloned repo. The
// clean `hub` remote is also recorded, so a collaborator can run `afs hub
// status` and `afs hub push` without accidentally publishing into their own
// namespace.
//
// When merge is true it folds the repo's files into an existing agentsfs
// instead of leaving them as a nested checkout: dir is the target instance root
// (default: the agentsfs enclosing the current directory). See cloneMerge for
// the conflict semantics.
func Clone(name, dir string, merge bool) (CloneResult, error) {
	var res CloneResult
	cfg, err := Load()
	if err != nil {
		return res, ErrNotSignedIn
	}
	owner, slug, err := ParseRef(name, cfg.User)
	if err != nil {
		return res, err
	}
	base := strings.TrimRight(cfg.URL, "/")
	clean := fmt.Sprintf("%s/%s/%s.git", base, owner, slug)
	res = CloneResult{Owner: owner, Slug: slug, Dir: dir, ViewURL: base + "/" + owner + "/" + slug}

	// One-shot Authorization header: authenticates the git transport without
	// persisting the token into the repo's .git/config (origin stays clean).
	authHeader := "http.extraHeader=Authorization: Basic " +
		base64.StdEncoding.EncodeToString([]byte(cfg.User+":"+cfg.Token))

	if merge {
		return cloneMerge(res, clean, slug, dir, authHeader)
	}

	if dir == "" {
		dir = slug
	}
	res.Dir = dir

	if info, statErr := os.Stat(dir); statErr == nil {
		if !info.IsDir() {
			return res, fmt.Errorf("%s exists and is not a directory", dir)
		}
		if git(dir, "rev-parse", "--git-dir") != nil {
			return res, fmt.Errorf("%s already exists and is not a git repository — remove it, or pull from inside it", dir)
		}
		cmd := exec.Command("git", "-C", dir, "-c", authHeader, "pull", "--ff-only")
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			return res, fmt.Errorf("pull failed: %v: %s", err, strings.TrimSpace(string(out)))
		}
		if err := setRemote(dir, "hub", clean); err != nil {
			return res, fmt.Errorf("linking hub remote: %w", err)
		}
		res.Updated = true
		return res, nil
	}

	cmd := exec.Command("git", "-c", authHeader, "clone", clean, dir)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return res, fmt.Errorf("clone failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if err := setRemote(dir, "hub", clean); err != nil {
		return res, fmt.Errorf("linking hub remote: %w", err)
	}
	return res, nil
}

// cloneMerge folds a hub repo into an existing agentsfs instance rather than
// leaving it as a nested checkout. dir is the target instance root; when empty
// it is the agentsfs enclosing the current directory (`afs hub pull --merge`
// run from inside an instance folds into *that* instance, not a ./<slug>/
// subdirectory).
//
// The repo is cloned into a throwaway staging area and its files are folded in:
//   - a file that exists only remotely is added;
//   - a file byte-identical to the local copy is skipped;
//   - a file that differs from the local copy is written aside under
//     scratch/hub-merge-<slug>/ and reported — the local copy is never
//     overwritten, so nothing is silently lost in either direction.
//
// The remote's .git (history + any embedded token) and .agentsfs (its derived
// index) are never brought across: both would mark the folded files as a
// separate instance and keep them out of the target's tree/index. The target
// rebuilds its own index over the merged files.
func cloneMerge(res CloneResult, clean, slug, dir, authHeader string) (CloneResult, error) {
	target := dir
	if target == "" {
		root, err := core.FindRoot(".")
		if err != nil {
			return res, fmt.Errorf("--merge folds a knowledgebase into the agentsfs you run it from — move into an instance, or pass a target directory: %w", err)
		}
		target = root
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return res, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return res, fmt.Errorf("--merge target %s does not exist; create the instance first, or drop --merge to clone it standalone", target)
	}
	if !info.IsDir() {
		return res, fmt.Errorf("--merge target %s is not a directory", target)
	}
	res.Dir = target

	staging, err := os.MkdirTemp("", "afs-hub-merge-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(staging)
	checkout := filepath.Join(staging, slug)

	cmd := exec.Command("git", "-c", authHeader, "clone", "--quiet", clean, checkout)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return res, fmt.Errorf("clone failed: %v: %s", err, strings.TrimSpace(string(out)))
	}

	// Quarantine under the target's OWN scratch role (`afs roles` contract:
	// consumers read the resolved path, never hardcode "scratch"/"agent-scratch");
	// fall back to the classic name only when the instance has no scratch home.
	scratchDir := "scratch"
	if rd, rdErr := core.ResolveReservedDirs(target); rdErr == nil && rd.Scratch != "" {
		scratchDir = rd.Scratch
	}
	quarantineRel := filepath.ToSlash(filepath.Join(scratchDir, "hub-merge-"+slug))
	walkErr := filepath.WalkDir(checkout, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(checkout, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		// The source repo's machine territory: never fold the child's .git
		// (history + any embedded token) or .agentsfs (its derived index) into
		// the target. Skipping .agentsfs also means the target's own .agentsfs is
		// never touched.
		top, _, _ := strings.Cut(rel, "/")
		if top == ".git" || top == ".agentsfs" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Never fold a symlink: copying one materializes whatever the link
		// points at ON THIS MACHINE as file content — a hostile KB could plant
		// a link to a local secret and have the fold copy it into the instance
		// (and a later push publish it). KB content is plain files; report and
		// skip. (The old nested-clone behavior kept symlinks as symlinks, so
		// this hazard is specific to folding.)
		if d.Type()&fs.ModeSymlink != 0 {
			res.Symlinks = append(res.Symlinks, rel)
			return nil
		}
		return foldMergedFile(&res, checkout, target, quarantineRel, rel)
	})
	if walkErr != nil {
		return res, fmt.Errorf("folding %s into %s: %w", slug, target, walkErr)
	}

	sort.Strings(res.Added)
	sort.Strings(res.Skipped)
	sort.Strings(res.Conflicts)
	sort.Strings(res.Symlinks)
	if len(res.Conflicts) > 0 {
		res.QuarantinePath = quarantineRel
	}
	res.Merged = true
	return res, nil
}

// foldMergedFile places one remote file (rel, slash-relative) into target,
// classifying it as added / skipped / conflict per cloneMerge's semantics. A
// conflicting remote copy is written under quarantineRel; the local file is
// left untouched.
func foldMergedFile(res *CloneResult, checkout, target, quarantineRel, rel string) error {
	remotePath := filepath.Join(checkout, filepath.FromSlash(rel))
	localPath := filepath.Join(target, filepath.FromSlash(rel))

	localInfo, statErr := os.Stat(localPath)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		if err := copyFileTo(remotePath, localPath); err != nil {
			return err
		}
		res.Added = append(res.Added, rel)
		return nil
	case statErr != nil:
		return statErr
	case localInfo.IsDir():
		// Remote has a file where the local instance has a directory — a
		// structural clash. Quarantine the remote copy rather than disturb the
		// local tree.
		return quarantineMergedFile(res, remotePath, target, quarantineRel, rel)
	}

	remoteData, err := os.ReadFile(remotePath)
	if err != nil {
		return err
	}
	localData, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	if bytes.Equal(remoteData, localData) {
		res.Skipped = append(res.Skipped, rel)
		return nil
	}
	return quarantineMergedFile(res, remotePath, target, quarantineRel, rel)
}

func quarantineMergedFile(res *CloneResult, remotePath, target, quarantineRel, rel string) error {
	dest := filepath.Join(target, filepath.FromSlash(quarantineRel), filepath.FromSlash(rel))
	if err := copyFileTo(remotePath, dest); err != nil {
		return err
	}
	res.Conflicts = append(res.Conflicts, rel)
	return nil
}

// copyFileTo copies src to dst, creating parent directories as needed.
func copyFileTo(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
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
