package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"agentsfs.ai/afs/internal/core"
)

const (
	defaultHostedEndpoint = "https://agentsfs.ai"
	hostedConfigFileName  = "hosted.json"
)

const hostedUsage = `afs hosted — managed git remotes for hosted agentsfs

Usage:
  afs login [--endpoint URL] [--token TOKEN | --token-stdin]
      store a hosted API token in your OS config directory, outside any agentsfs repo.
      Create the token from the signed-in web app first.
  afs hosted create [name] [--description text]
      create a hosted filesystem. Managed deployments create a private git repo.
  afs hosted list
      list hosted filesystems visible to the configured token.
  afs hosted connect <instance-id-or-url-or-name> [path] [--remote name]
      connect the local agentsfs at path (default: cwd) to a hosted filesystem,
      add/update a git remote, and install a local credential helper.
  afs hosted status [path]
      show local git status and hosted remote/ahead-behind status.
  afs hosted push [path] [--branch main]
      run git push to the hosted remote using a short-lived credential.
  afs hosted pull [path] [--branch main]
      run git pull --ff-only from the hosted remote.
  afs hosted clone <instance-id-or-url-or-name> [dir]
      run git clone from the hosted remote, then write non-secret connection metadata.
  afs hosted backup [path] [--dry-run]
      fallback: upload local UTF-8 text files through the hosted file API.
  afs hosted restore [path] [--force]
      fallback: download hosted UTF-8 text files through the hosted file API.

Managed hosted sync is real git when the hosted API returns a git remote.
Fallback backup/restore commands are file copy operations and are not git sync.`

type hostedAuthConfig struct {
	Endpoint  string `json:"endpoint"`
	Token     string `json:"token"`
	UpdatedAt string `json:"updated_at,omitempty"`

	Path   string `json:"-"`
	Source string `json:"-"`
}

type hostedConnection struct {
	Endpoint         string `json:"endpoint"`
	FilesystemID     string `json:"filesystem_id"`
	Name             string `json:"name,omitempty"`
	ConnectedAt      string `json:"connected_at"`
	GitRemoteURL     string `json:"git_remote_url,omitempty"`
	GitRemoteName    string `json:"git_remote_name,omitempty"`
	GitDefaultBranch string `json:"git_default_branch,omitempty"`
}

type hostedFilesystem struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Slug        string        `json:"slug"`
	Description string        `json:"description"`
	FileCount   int           `json:"file_count"`
	BytesUsed   int64         `json:"bytes_used"`
	UpdatedAt   string        `json:"updated_at"`
	Remote      *hostedRemote `json:"remote,omitempty"`
}

type hostedRemote struct {
	Provider      string `json:"provider"`
	RemoteURL     string `json:"remote_url"`
	HTMLURL       string `json:"html_url"`
	Owner         string `json:"owner"`
	Repo          string `json:"repo"`
	DefaultBranch string `json:"default_branch"`
}

type hostedGitCredentials struct {
	Provider      string `json:"provider"`
	RemoteURL     string `json:"remote_url"`
	HTMLURL       string `json:"html_url"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	ExpiresAt     string `json:"expires_at"`
	DefaultBranch string `json:"default_branch"`
	Owner         string `json:"owner"`
	Repo          string `json:"repo"`
}

type hostedTreeEntry struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Depth       int    `json:"depth"`
	Size        int64  `json:"size"`
	Description string `json:"description"`
	UpdatedAt   string `json:"updatedAt"`
}

type hostedFileRecord struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type hostedFileResponse struct {
	File    hostedFileRecord `json:"file"`
	Content string           `json:"content"`
}

type hostedClient struct {
	endpoint string
	token    string
	http     *http.Client
}

func runLogin(args []string) {
	endpoint := os.Getenv("AGENTSFS_HOSTED_ENDPOINT")
	if endpoint == "" {
		endpoint = defaultHostedEndpoint
	}
	token := ""
	tokenStdin := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--endpoint":
			i++
			if i >= len(args) {
				fail(fmt.Errorf("--endpoint needs a URL"))
			}
			endpoint = args[i]
		case "--token":
			i++
			if i >= len(args) {
				fail(fmt.Errorf("--token needs a value"))
			}
			token = strings.TrimSpace(args[i])
		case "--token-stdin":
			tokenStdin = true
		case "--help", "-h":
			fmt.Println(hostedUsage)
			return
		default:
			if strings.HasPrefix(args[i], "-") {
				fail(fmt.Errorf("unknown flag %q for login", args[i]))
			}
			fail(fmt.Errorf("usage: afs login [--endpoint URL] [--token TOKEN | --token-stdin]"))
		}
	}

	if tokenStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fail(err)
		}
		token = strings.TrimSpace(string(data))
	}
	if token == "" {
		fmt.Printf("Create a CLI token from %s/app/filesystems, then run:\n  afs login --token-stdin\n", strings.TrimRight(endpoint, "/"))
		return
	}
	if !strings.HasPrefix(token, "afs_") {
		fail(fmt.Errorf("hosted tokens start with afs_; create one from the hosted web app"))
	}

	normalizedEndpoint, err := normalizeHostedEndpoint(endpoint)
	if err != nil {
		fail(err)
	}
	path, err := hostedConfigPath()
	if err != nil {
		fail(err)
	}
	if err := saveHostedAuth(hostedAuthConfig{
		Endpoint:  normalizedEndpoint,
		Token:     token,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		fail(err)
	}
	fmt.Printf("Saved hosted credentials for %s in %s\n", normalizedEndpoint, path)
}

func runHosted(args []string) {
	if len(args) == 0 {
		fmt.Println(hostedUsage)
		return
	}
	switch args[0] {
	case "create":
		runHostedCreate(args[1:])
	case "list":
		runHostedList(args[1:])
	case "connect":
		runHostedConnect(args[1:])
	case "status":
		runHostedStatus(args[1:])
	case "push":
		runHostedPush(args[1:])
	case "pull":
		runHostedPull(args[1:])
	case "clone":
		runHostedClone(args[1:])
	case "backup":
		runHostedBackup(args[1:])
	case "restore":
		runHostedRestore(args[1:])
	case "credential-helper":
		runHostedCredentialHelper(args[1:])
	case "help", "--help", "-h":
		fmt.Println(hostedUsage)
	default:
		fail(fmt.Errorf("unknown hosted command %q\n\n%s", args[0], hostedUsage))
	}
}

func runHostedCreate(args []string) {
	nameParts := []string{}
	description := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--description", "--desc":
			i++
			if i >= len(args) {
				fail(fmt.Errorf("%s needs text", args[i-1]))
			}
			description = args[i]
		case "--help", "-h":
			fmt.Println(hostedUsage)
			return
		default:
			if strings.HasPrefix(args[i], "-") {
				fail(fmt.Errorf("unknown flag %q for hosted create", args[i]))
			}
			nameParts = append(nameParts, args[i])
		}
	}
	name := strings.TrimSpace(strings.Join(nameParts, " "))
	if name == "" {
		name = "Untitled hosted filesystem"
	}

	client := mustHostedClient()
	var response struct {
		Filesystem hostedFilesystem `json:"filesystem"`
	}
	if err := client.do("POST", "/api/filesystems", map[string]string{
		"name":        name,
		"description": description,
	}, &response); err != nil {
		fail(err)
	}
	fmt.Printf("Created hosted filesystem %s (%s)\n", response.Filesystem.Name, response.Filesystem.ID)
	if response.Filesystem.Remote != nil && response.Filesystem.Remote.RemoteURL != "" {
		fmt.Printf("Managed git remote: %s\n", response.Filesystem.Remote.RemoteURL)
	}
	fmt.Printf("Connect a local agentsfs with `afs hosted connect %s`\n", response.Filesystem.ID)
}

func runHostedList(args []string) {
	pos := splitArgs(args, nil)
	if len(pos) > 0 {
		fail(fmt.Errorf("usage: afs hosted list"))
	}
	client := mustHostedClient()
	filesystems, err := client.listFilesystems()
	if err != nil {
		fail(err)
	}
	if len(filesystems) == 0 {
		fmt.Println("No hosted filesystems found.")
		return
	}
	for _, filesystem := range filesystems {
		mode := "file-api"
		if filesystem.Remote != nil && filesystem.Remote.RemoteURL != "" {
			mode = "git"
		}
		fmt.Printf("%s  %s  mode=%s files=%d bytes=%d updated=%s\n",
			filesystem.ID, filesystem.Name, mode, filesystem.FileCount, filesystem.BytesUsed, filesystem.UpdatedAt)
	}
}

func runHostedConnect(args []string) {
	remoteName := "agentsfs"
	var pos []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--remote":
			i++
			if i >= len(args) {
				fail(fmt.Errorf("--remote needs a name"))
			}
			remoteName = args[i]
		case "--help", "-h":
			fmt.Println(hostedUsage)
			return
		default:
			if strings.HasPrefix(args[i], "-") {
				fail(fmt.Errorf("unknown flag %q for hosted connect", args[i]))
			}
			pos = append(pos, args[i])
		}
	}
	if len(pos) < 1 || len(pos) > 2 {
		fail(fmt.Errorf("usage: afs hosted connect <instance-id-or-url-or-name> [path] [--remote name]"))
	}
	ref := pos[0]
	root := instanceRoot(pos[1:], 0)
	client := mustHostedClient()
	filesystem, err := client.resolveFilesystem(ref)
	if err != nil {
		fail(err)
	}
	auth, err := loadHostedAuth()
	if err != nil {
		fail(err)
	}
	conn := hostedConnection{
		Endpoint:      auth.Endpoint,
		FilesystemID:  filesystem.ID,
		Name:          filesystem.Name,
		ConnectedAt:   time.Now().UTC().Format(time.RFC3339),
		GitRemoteName: remoteName,
	}
	if filesystem.Remote != nil {
		conn.GitRemoteURL = filesystem.Remote.RemoteURL
		conn.GitDefaultBranch = filesystem.Remote.DefaultBranch
	}
	if err := saveHostedConnection(root, conn); err != nil {
		fail(err)
	}
	fmt.Printf("Connected %s to hosted filesystem %s (%s)\n", root, filesystem.Name, filesystem.ID)
	if conn.GitRemoteURL == "" {
		fmt.Println("This hosted filesystem does not expose a git remote; use `afs hosted backup` and `afs hosted restore` as file API fallbacks.")
		return
	}
	if err := configureHostedGitRemote(root, conn); err != nil {
		fail(err)
	}
	fmt.Printf("Configured git remote %q -> %s\n", conn.GitRemoteName, conn.GitRemoteURL)
	fmt.Println("You can now run `afs hosted push`, `afs hosted pull`, or ordinary git push/pull using that remote.")
}

func runHostedStatus(args []string) {
	pos := splitArgs(args, nil)
	root := instanceRoot(pos, 0)
	conn, err := loadHostedConnection(root)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Local agentsfs: %s\n", root)
	fmt.Printf("Hosted endpoint: %s\n", conn.Endpoint)
	fmt.Printf("Hosted filesystem: %s", conn.FilesystemID)
	if conn.Name != "" {
		fmt.Printf(" (%s)", conn.Name)
	}
	fmt.Println()
	if conn.GitRemoteURL == "" {
		fmt.Println("Mode: file API fallback. This connection has no git remote.")
	} else {
		fmt.Printf("Git remote: %s -> %s\n", hostedRemoteName(conn), conn.GitRemoteURL)
		fmt.Printf("Default branch: %s\n", hostedBranch(conn, ""))
	}

	localFiles, localBytes, err := localHostedFileStats(root)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Local files: %d (%d bytes)\n", localFiles, localBytes)

	auth, err := loadHostedAuth()
	if err != nil {
		fmt.Println("Hosted credentials: missing; run `afs login --token-stdin`.")
		return
	}
	if auth.Source == "environment" {
		fmt.Println("Hosted credentials: configured from environment.")
	} else {
		fmt.Printf("Hosted credentials: configured in %s\n", auth.Path)
	}
	client := newHostedClient(auth)
	filesystem, err := client.getFilesystem(conn.FilesystemID)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Remote files: %d (%d bytes), updated %s\n", filesystem.FileCount, filesystem.BytesUsed, filesystem.UpdatedAt)
	if conn.GitRemoteURL != "" {
		if err := configureHostedGitRemote(root, conn); err != nil {
			fail(err)
		}
		status, err := hostedGitStatus(root, conn)
		if err != nil {
			fmt.Printf("Git status: %v\n", err)
			return
		}
		fmt.Print(status)
	}
}

func runHostedPush(args []string) {
	branch := ""
	pos := hostedBranchArgs(args, &branch)
	root := instanceRoot(pos, 0)
	_, conn := mustHostedContext(root)
	if conn.GitRemoteURL == "" {
		fail(fmt.Errorf("this hosted connection has no git remote; use `afs hosted backup` for file API fallback"))
	}
	if err := configureHostedGitRemote(root, conn); err != nil {
		fail(err)
	}
	err := runGitWithHostedCredentials(root, conn, "push", hostedRemoteName(conn), "HEAD:refs/heads/"+hostedBranch(conn, branch))
	if err != nil {
		fail(err)
	}
	fmt.Printf("Pushed HEAD to hosted remote %s/%s\n", hostedRemoteName(conn), hostedBranch(conn, branch))
}

func runHostedPull(args []string) {
	branch := ""
	pos := hostedBranchArgs(args, &branch)
	root := instanceRoot(pos, 0)
	_, conn := mustHostedContext(root)
	if conn.GitRemoteURL == "" {
		fail(fmt.Errorf("this hosted connection has no git remote; use `afs hosted restore` for file API fallback"))
	}
	if err := configureHostedGitRemote(root, conn); err != nil {
		fail(err)
	}
	err := runGitWithHostedCredentials(root, conn, "pull", "--ff-only", hostedRemoteName(conn), hostedBranch(conn, branch))
	if err != nil {
		fail(err)
	}
	fmt.Printf("Pulled hosted remote %s/%s\n", hostedRemoteName(conn), hostedBranch(conn, branch))
}

func runHostedBackup(args []string) {
	var dryRun bool
	pos := splitArgs(args, map[string]*bool{"--dry-run": &dryRun})
	root := instanceRoot(pos, 0)
	client, conn := mustHostedContext(root)
	count, bytes, err := hostedPushFiles(root, client, conn, dryRun)
	if err != nil {
		fail(err)
	}
	if dryRun {
		fmt.Printf("Would back up %d UTF-8 text file(s), %d bytes total, to %s (%s)\n", count, bytes, conn.Name, conn.FilesystemID)
		return
	}
	fmt.Printf("Backed up %d UTF-8 text file(s), %d bytes total, to %s (%s)\n", count, bytes, conn.Name, conn.FilesystemID)
	fmt.Println("This was a file API backup, not git sync.")
}

func runHostedRestore(args []string) {
	var force bool
	pos := splitArgs(args, map[string]*bool{"--force": &force})
	root := instanceRoot(pos, 0)
	client, conn := mustHostedContext(root)
	result, err := hostedPullFiles(root, client, conn, force)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Restored %d file(s) from %s (%s); %d unchanged.\n",
		result.Written, conn.Name, conn.FilesystemID, result.Unchanged)
	fmt.Println("This was a file API restore, not git sync.")
}

func runHostedClone(args []string) {
	if len(args) < 1 || len(args) > 2 {
		fail(fmt.Errorf("usage: afs hosted clone <instance-id-or-url-or-name> [dir]"))
	}
	client := mustHostedClient()
	filesystem, err := client.resolveFilesystem(args[0])
	if err != nil {
		fail(err)
	}

	target := ""
	if len(args) == 2 {
		target = args[1]
	} else {
		target = safeHostedDirName(filesystem)
	}
	if err := ensureCloneTargetAvailable(target); err != nil {
		fail(err)
	}

	if filesystem.Remote == nil || filesystem.Remote.RemoteURL == "" {
		res := mustInit(target, core.ModeStandalone)
		conn := hostedConnection{
			Endpoint:     client.endpoint,
			FilesystemID: filesystem.ID,
			Name:         filesystem.Name,
			ConnectedAt:  time.Now().UTC().Format(time.RFC3339),
		}
		if err := saveHostedConnection(res.Dir, conn); err != nil {
			fail(err)
		}
		pullResult, err := hostedPullFiles(res.Dir, client, conn, true)
		if err != nil {
			fail(err)
		}
		fmt.Printf("Created local agentsfs at %s and imported %d hosted file(s).\n", res.Dir, pullResult.Written+pullResult.Unchanged)
		fmt.Println("This hosted filesystem does not expose a git remote; this was a file import fallback.")
		return
	}
	conn := hostedConnection{
		Endpoint:         client.endpoint,
		FilesystemID:     filesystem.ID,
		Name:             filesystem.Name,
		ConnectedAt:      time.Now().UTC().Format(time.RFC3339),
		GitRemoteURL:     filesystem.Remote.RemoteURL,
		GitRemoteName:    "agentsfs",
		GitDefaultBranch: filesystem.Remote.DefaultBranch,
	}
	if err := runGitWithHostedCredentials("", conn, "clone", hostedGitRemoteURL(conn.GitRemoteURL), target); err != nil {
		fail(err)
	}
	root, err := core.FindRoot(target)
	if err != nil {
		fail(err)
	}
	if err := saveHostedConnection(root, conn); err != nil {
		fail(err)
	}
	if err := configureHostedGitRemote(root, conn); err != nil {
		fail(err)
	}
	fmt.Printf("Cloned hosted filesystem %s (%s) into %s\n", filesystem.Name, filesystem.ID, root)
}

func runHostedCredentialHelper(args []string) {
	filesystemID := ""
	endpoint := ""
	operation := "get"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--filesystem":
			i++
			if i >= len(args) {
				fail(fmt.Errorf("--filesystem needs an id"))
			}
			filesystemID = args[i]
		case "--endpoint":
			i++
			if i >= len(args) {
				fail(fmt.Errorf("--endpoint needs a URL"))
			}
			endpoint = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				fail(fmt.Errorf("unknown flag %q for hosted credential-helper", args[i]))
			}
			operation = args[i]
		}
	}
	_, _ = io.ReadAll(os.Stdin)
	if operation != "get" {
		return
	}
	auth, err := loadHostedAuth()
	if err != nil {
		fail(err)
	}
	if endpoint != "" {
		normalized, err := normalizeHostedEndpoint(endpoint)
		if err != nil {
			fail(err)
		}
		auth.Endpoint = normalized
	}
	if filesystemID == "" {
		root, err := core.FindRoot(".")
		if err != nil {
			fail(err)
		}
		conn, err := loadHostedConnection(root)
		if err != nil {
			fail(err)
		}
		filesystemID = conn.FilesystemID
	}
	credentials, err := newHostedClient(auth).gitCredentials(filesystemID)
	if err != nil {
		fail(err)
	}
	if credentials.Username == "" || credentials.Password == "" {
		fail(fmt.Errorf("hosted API did not return git credentials"))
	}
	fmt.Printf("username=%s\n", credentials.Username)
	fmt.Printf("password=%s\n\n", credentials.Password)
}

func hostedRemoteName(conn hostedConnection) string {
	if conn.GitRemoteName != "" {
		return conn.GitRemoteName
	}
	return "agentsfs"
}

func hostedBranch(conn hostedConnection, override string) string {
	if override != "" {
		return override
	}
	if conn.GitDefaultBranch != "" {
		return conn.GitDefaultBranch
	}
	return "main"
}

func hostedBranchArgs(args []string, branch *string) []string {
	var pos []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--branch":
			i++
			if i >= len(args) {
				fail(fmt.Errorf("--branch needs a branch name"))
			}
			*branch = args[i]
		case "--help", "-h":
			fmt.Println(hostedUsage)
			os.Exit(0)
		default:
			if strings.HasPrefix(args[i], "-") {
				fail(fmt.Errorf("unknown flag %q", args[i]))
			}
			pos = append(pos, args[i])
		}
	}
	return pos
}

func configureHostedGitRemote(root string, conn hostedConnection) error {
	if conn.GitRemoteURL == "" {
		return fmt.Errorf("hosted filesystem %s does not expose a git remote", conn.FilesystemID)
	}
	if _, err := gitOutput(root, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("%s is not a git repository: %w", root, err)
	}
	remoteName := hostedRemoteName(conn)
	remoteURL := hostedGitRemoteURL(conn.GitRemoteURL)
	if _, err := gitOutput(root, "remote", "get-url", remoteName); err == nil {
		if _, err := gitOutput(root, "remote", "set-url", remoteName, remoteURL); err != nil {
			return err
		}
	} else if _, err := gitOutput(root, "remote", "add", remoteName, remoteURL); err != nil {
		return err
	}
	if _, err := gitOutput(root, "config", "--local", "--unset-all", "credential.helper", ".*hosted credential-helper.*"); err != nil {
		// Best-effort cleanup for older afs versions that installed a repo-wide helper.
		// Missing values are fine.
	}
	if hostedRemoteNeedsCredentials(remoteURL) {
		helperKey := "credential." + remoteURL + ".helper"
		if _, err := gitOutput(root, "config", "--local", "--unset-all", helperKey); err != nil {
			// Missing values are fine.
		}
		if _, err := gitOutput(root, "config", "--local", "--add", helperKey, ""); err != nil {
			return err
		}
		if _, err := gitOutput(root, "config", "--local", "--add", helperKey, hostedCredentialHelperCommand(conn)); err != nil {
			return err
		}
	}
	return nil
}

func hostedGitRemoteURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return raw
	}
	if strings.EqualFold(parsed.Host, "github.com") {
		parsed.User = url.User("x-access-token")
	}
	return parsed.String()
}

func hostedRemoteNeedsCredentials(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

func hostedCredentialHelperCommand(conn hostedConnection) string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "afs"
	}
	parts := []string{
		shellQuote(exe),
		"hosted",
		"credential-helper",
		"--filesystem",
		shellQuote(conn.FilesystemID),
		"--endpoint",
		shellQuote(conn.Endpoint),
	}
	return "!" + strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func runGitWithHostedCredentials(root string, conn hostedConnection, args ...string) error {
	_, err := gitOutputWithHostedCredentials(root, conn, args...)
	return err
}

func gitOutputWithHostedCredentials(root string, conn hostedConnection, args ...string) (string, error) {
	gitArgs := []string{
		"-c",
		"credential.helper=",
		"-c",
		"credential.helper=" + hostedCredentialHelperCommand(conn),
	}
	gitArgs = append(gitArgs, args...)
	return gitOutput(root, gitArgs...)
}

func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if root != "" {
		cmd.Dir = root
	}
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		return text, fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(text))
	}
	if text != "" && isUserFacingGitCommand(args) {
		fmt.Print(text)
	}
	return text, nil
}

func isUserFacingGitCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for len(args) >= 2 && args[0] == "-c" {
		args = args[2:]
	}
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "push", "pull", "clone":
		return true
	default:
		return false
	}
}

func hostedGitStatus(root string, conn hostedConnection) (string, error) {
	var out strings.Builder
	status, err := gitOutput(root, "status", "--short")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(status) == "" {
		out.WriteString("Working tree: clean\n")
	} else {
		out.WriteString("Working tree changes:\n")
		out.WriteString(status)
		if !strings.HasSuffix(status, "\n") {
			out.WriteString("\n")
		}
	}
	remoteName := hostedRemoteName(conn)
	branch := hostedBranch(conn, "")
	refspec := fmt.Sprintf("refs/heads/%s:refs/remotes/%s/%s", branch, remoteName, branch)
	if _, err := gitOutputWithHostedCredentials(root, conn, "fetch", "--quiet", remoteName, refspec); err != nil {
		return out.String(), err
	}
	counts, err := gitOutput(root, "rev-list", "--left-right", "--count", fmt.Sprintf("HEAD...%s/%s", remoteName, branch))
	if err != nil {
		return out.String(), err
	}
	fields := strings.Fields(counts)
	if len(fields) == 2 {
		out.WriteString(fmt.Sprintf("Ahead/behind %s/%s: ahead %s, behind %s\n", remoteName, branch, fields[0], fields[1]))
	}
	return out.String(), nil
}

type hostedPullResult struct {
	Written   int
	Unchanged int
}

func mustHostedClient() *hostedClient {
	auth, err := loadHostedAuth()
	if err != nil {
		fail(err)
	}
	return newHostedClient(auth)
}

func mustHostedContext(root string) (*hostedClient, hostedConnection) {
	auth, err := loadHostedAuth()
	if err != nil {
		fail(err)
	}
	conn, err := loadHostedConnection(root)
	if err != nil {
		fail(err)
	}
	if normalize, err := normalizeHostedEndpoint(conn.Endpoint); err == nil && normalize != auth.Endpoint {
		fmt.Fprintf(os.Stderr, "warning: local connection endpoint %s differs from credential endpoint %s; using credential endpoint\n", conn.Endpoint, auth.Endpoint)
	}
	return newHostedClient(auth), conn
}

func newHostedClient(auth hostedAuthConfig) *hostedClient {
	return &hostedClient{
		endpoint: strings.TrimRight(auth.Endpoint, "/"),
		token:    auth.Token,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *hostedClient) do(method, apiPath string, input any, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, c.endpoint+apiPath, body)
	if err != nil {
		return err
	}
	request.Header.Set("authorization", "Bearer "+c.token)
	if input != nil {
		request.Header.Set("content-type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		var apiError struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &apiError)
		if apiError.Error == "" {
			apiError.Error = strings.TrimSpace(string(data))
		}
		if apiError.Error == "" {
			apiError.Error = response.Status
		}
		return fmt.Errorf("%s %s: %s", method, apiPath, apiError.Error)
	}
	if output != nil && len(data) > 0 {
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("decode %s %s: %w", method, apiPath, err)
		}
	}
	return nil
}

func (c *hostedClient) listFilesystems() ([]hostedFilesystem, error) {
	var response struct {
		Filesystems []hostedFilesystem `json:"filesystems"`
	}
	if err := c.do("GET", "/api/filesystems", nil, &response); err != nil {
		return nil, err
	}
	return response.Filesystems, nil
}

func (c *hostedClient) getFilesystem(id string) (hostedFilesystem, error) {
	var response struct {
		Filesystem hostedFilesystem `json:"filesystem"`
	}
	err := c.do("GET", "/api/filesystems/"+url.PathEscape(id), nil, &response)
	return response.Filesystem, err
}

func (c *hostedClient) getTree(id string) ([]hostedTreeEntry, error) {
	var response struct {
		Tree []hostedTreeEntry `json:"tree"`
	}
	if err := c.do("GET", "/api/filesystems/"+url.PathEscape(id)+"/tree", nil, &response); err != nil {
		return nil, err
	}
	return response.Tree, nil
}

func (c *hostedClient) readFile(id, rel string) (hostedFileResponse, error) {
	var response hostedFileResponse
	err := c.do("GET", "/api/filesystems/"+url.PathEscape(id)+"/files?path="+url.QueryEscape(rel), nil, &response)
	return response, err
}

func (c *hostedClient) writeFile(id, rel, content string) error {
	var response struct{}
	return c.do("PUT", "/api/filesystems/"+url.PathEscape(id)+"/files", map[string]string{
		"path":    rel,
		"content": content,
		"message": "CLI push " + rel,
	}, &response)
}

func (c *hostedClient) gitCredentials(id string) (hostedGitCredentials, error) {
	var response hostedGitCredentials
	err := c.do("POST", "/api/filesystems/"+url.PathEscape(id)+"/git-credentials", map[string]string{}, &response)
	return response, err
}

func (c *hostedClient) resolveFilesystem(ref string) (hostedFilesystem, error) {
	candidate := hostedRefID(ref)
	filesystems, err := c.listFilesystems()
	if err != nil {
		return hostedFilesystem{}, err
	}
	var matches []hostedFilesystem
	for _, filesystem := range filesystems {
		if filesystem.ID == candidate || filesystem.Name == ref || filesystem.Slug == ref {
			matches = append(matches, filesystem)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return hostedFilesystem{}, fmt.Errorf("%q matches multiple hosted filesystems; use the filesystem id", ref)
	}
	return hostedFilesystem{}, fmt.Errorf("hosted filesystem %q was not found", ref)
}

func hostedPushFiles(root string, client *hostedClient, conn hostedConnection, dryRun bool) (int, int64, error) {
	entries, err := core.ListEntries(root)
	if err != nil {
		return 0, 0, err
	}
	count := 0
	var total int64
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		if err := validateHostedRelPath(entry.Rel); err != nil {
			return 0, 0, fmt.Errorf("%s: %w", entry.Rel, err)
		}
		localPath := filepath.Join(root, filepath.FromSlash(entry.Rel))
		data, err := os.ReadFile(localPath)
		if err != nil {
			return 0, 0, err
		}
		if !utf8.Valid(data) {
			return 0, 0, fmt.Errorf("%s is not UTF-8 text; hosted file API backup currently supports text files only", entry.Rel)
		}
		count++
		total += int64(len(data))
		if dryRun {
			continue
		}
		if err := client.writeFile(conn.FilesystemID, entry.Rel, string(data)); err != nil {
			return 0, 0, err
		}
	}
	return count, total, nil
}

func hostedPullFiles(root string, client *hostedClient, conn hostedConnection, force bool) (hostedPullResult, error) {
	tree, err := client.getTree(conn.FilesystemID)
	if err != nil {
		return hostedPullResult{}, err
	}
	var result hostedPullResult
	for _, entry := range tree {
		if entry.Type != "file" {
			continue
		}
		if err := validateHostedRelPath(entry.Path); err != nil {
			return result, fmt.Errorf("%s: %w", entry.Path, err)
		}
		file, err := client.readFile(conn.FilesystemID, entry.Path)
		if err != nil {
			return result, err
		}
		wrote, err := writeHostedFile(root, entry.Path, file.Content, force)
		if err != nil {
			return result, err
		}
		if wrote {
			result.Written++
		} else {
			result.Unchanged++
		}
	}
	return result, nil
}

func writeHostedFile(root, rel, content string, force bool) (bool, error) {
	dest := filepath.Join(root, filepath.FromSlash(rel))
	if err := ensureInsideRoot(root, dest); err != nil {
		return false, err
	}
	existing, err := os.ReadFile(dest)
	if err == nil {
		if string(existing) == content {
			return false, nil
		}
		if !force {
			return false, fmt.Errorf("%s differs locally; rerun with --force to overwrite", rel)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(dest, []byte(content), 0o644)
}

func validateHostedRelPath(rel string) error {
	if rel == "" || rel == "." {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return fmt.Errorf("path must be relative")
	}
	normalized := strings.ReplaceAll(rel, "\\", "/")
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("path cannot contain traversal segments")
		}
		if part == ".git" || part == ".agentsfs" {
			return fmt.Errorf("git and agentsfs internals are not hosted files")
		}
		if strings.ContainsAny(part, "\x00\n\r\t") {
			return fmt.Errorf("path cannot contain control characters")
		}
	}
	return nil
}

func localHostedFileStats(root string) (int, int64, error) {
	entries, err := core.ListEntries(root)
	if err != nil {
		return 0, 0, err
	}
	count := 0
	var bytes int64
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(entry.Rel)))
		if err != nil {
			return 0, 0, err
		}
		count++
		bytes += info.Size()
	}
	return count, bytes, nil
}

func loadHostedAuth() (hostedAuthConfig, error) {
	path, err := hostedConfigPath()
	if err != nil {
		return hostedAuthConfig{}, err
	}
	cfg := hostedAuthConfig{Endpoint: defaultHostedEndpoint, Path: path, Source: "file"}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return hostedAuthConfig{}, fmt.Errorf("%s: %w", path, err)
		}
		cfg.Path = path
		cfg.Source = "file"
	} else if !errors.Is(err, os.ErrNotExist) {
		return hostedAuthConfig{}, err
	}

	if endpoint := os.Getenv("AGENTSFS_HOSTED_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
		cfg.Source = "environment"
	}
	if token := os.Getenv("AGENTSFS_HOSTED_TOKEN"); token != "" {
		cfg.Token = token
		cfg.Source = "environment"
	}
	endpoint, err := normalizeHostedEndpoint(cfg.Endpoint)
	if err != nil {
		return hostedAuthConfig{}, err
	}
	cfg.Endpoint = endpoint
	if strings.TrimSpace(cfg.Token) == "" {
		return hostedAuthConfig{}, fmt.Errorf("hosted credentials are not configured; create a token in the web app and run `afs login --token-stdin`")
	}
	cfg.Token = strings.TrimSpace(cfg.Token)
	return cfg, nil
}

func saveHostedAuth(cfg hostedAuthConfig) error {
	path, err := hostedConfigPath()
	if err != nil {
		return err
	}
	cfg.Path = ""
	cfg.Source = ""
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func hostedConfigPath() (string, error) {
	if override := os.Getenv("AFS_CONFIG_HOME"); override != "" {
		return filepath.Join(override, "agentsfs", hostedConfigFileName), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "agentsfs", hostedConfigFileName), nil
}

func loadHostedConnection(root string) (hostedConnection, error) {
	path := hostedConnectionPath(root)
	var conn hostedConnection
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return conn, fmt.Errorf("%s is not connected to hosted agentsfs; run `afs hosted connect <filesystem>`", root)
		}
		return conn, err
	}
	if err := json.Unmarshal(data, &conn); err != nil {
		return conn, fmt.Errorf("%s: %w", path, err)
	}
	if conn.FilesystemID == "" {
		return conn, fmt.Errorf("%s is missing filesystem_id", path)
	}
	if conn.Endpoint == "" {
		conn.Endpoint = defaultHostedEndpoint
	}
	if conn.GitRemoteName == "" && conn.GitRemoteURL != "" {
		conn.GitRemoteName = "agentsfs"
	}
	if conn.GitDefaultBranch == "" && conn.GitRemoteURL != "" {
		conn.GitDefaultBranch = "main"
	}
	return conn, nil
}

func saveHostedConnection(root string, conn hostedConnection) error {
	if conn.Endpoint == "" {
		conn.Endpoint = defaultHostedEndpoint
	}
	data, err := json.MarshalIndent(conn, "", "  ")
	if err != nil {
		return err
	}
	path := hostedConnectionPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func hostedConnectionPath(root string) string {
	return filepath.Join(root, ".agentsfs", "hosted.json")
}

func normalizeHostedEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultHostedEndpoint
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("hosted endpoint must start with http:// or https://")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("hosted endpoint needs a host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("hosted endpoint must be a base URL, not an API path")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func hostedRefID(ref string) string {
	if parsed, err := url.Parse(ref); err == nil && parsed.Host != "" {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) > 0 && parts[len(parts)-1] != "" {
			return parts[len(parts)-1]
		}
	}
	return ref
}

func safeHostedDirName(filesystem hostedFilesystem) string {
	name := filesystem.Slug
	if name == "" {
		name = strings.ToLower(filesystem.Name)
		name = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				return r
			}
			if r == ' ' {
				return '-'
			}
			return -1
		}, name)
		name = strings.Trim(name, "-_")
	}
	if name == "" {
		name = filesystem.ID
	}
	return name
}

func ensureCloneTargetAvailable(target string) error {
	info, err := os.Stat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s exists and is not a directory", target)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty", target)
	}
	return nil
}

func ensureInsideRoot(root, dest string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, absDest)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return fmt.Errorf("%s would write outside %s", dest, root)
	}
	return nil
}
