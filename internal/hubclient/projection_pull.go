package hubclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

type ProjectionPullOptions struct {
	Adopt    bool
	Continue bool
	Abort    bool
}

type ProjectionPullResult struct {
	Repository   string
	RemoteCommit string
	HostCommit   string
	InstanceRoot string
	Prefix       string
	Already      bool
	Adopted      bool
	Conflicts    []string
}

type pendingProjectionPull struct {
	InstanceRoot string                   `json:"instance_root"`
	RemoteURL    string                   `json:"remote_url"`
	Repository   string                   `json:"repository"`
	HostHead     string                   `json:"host_head"`
	BaseCommit   string                   `json:"base_commit"`
	RemoteCommit string                   `json:"remote_commit"`
	Adopted      bool                     `json:"adopted,omitempty"`
	Metadata     core.PublicationMetadata `json:"metadata"`
}

// PullProjection translates Hub-rooted commits back under an embedded
// instance's host-repository prefix. It uses the last published projection as
// the explicit three-way base, then records the Hub tip in an ordinary folded
// host commit. The next push creates an exact projection snapshot whose parent
// is that recorded Hub tip.
func PullProjection(root, name string, opts ProjectionPullOptions) (ProjectionPullResult, error) {
	var res ProjectionPullResult
	cfg, err := Load()
	if err != nil {
		return res, ErrNotSignedIn
	}
	resolution, err := core.ResolveInstance(root, core.ResolveInstanceOptions{})
	if err != nil {
		return res, err
	}
	if resolution.Mode != "embedded" {
		return res, errors.New("history-aware `afs hub pull` is for an embedded instance projection; standalone Hub repositories use ordinary `git pull --ff-only`")
	}
	root = resolution.InstanceRoot
	repoRoot := resolution.RepoRoot
	res.InstanceRoot, res.Prefix = root, resolution.Prefix

	if opts.Abort {
		if err := abortProjectionPull(repoRoot); err != nil {
			return res, err
		}
		return res, nil
	}
	if opts.Continue {
		return continueProjectionPull(repoRoot)
	}
	if mergeInProgress(repoRoot) || projectionPullPending(repoRoot) {
		return res, errors.New("a Git merge or AgentsFS projection pull is already in progress; resolve it and run `afs hub pull --continue`, or run `afs hub pull --abort`")
	}
	if dirty, _ := gitOutput(repoRoot, "status", "--porcelain"); dirty != "" {
		return res, errors.New("the host repository has uncommitted work; commit or stash it before pulling an embedded Hub projection")
	}

	metadata, metadataErr := core.LoadPublicationMetadata(root)
	baseURL := strings.TrimRight(core.CredentialFreeURL(cfg.URL), "/")
	owner, slug, remote := cfg.User, "", ""
	if name != "" {
		owner, slug, err = ParseRef(name, cfg.User)
		if err != nil {
			return res, err
		}
		remote = fmt.Sprintf("%s/%s/%s.git", baseURL, owner, slug)
		if metadataErr == nil && metadata.Repository != "" && metadata.Repository != owner+"/"+slug {
			metadata, metadataErr = core.PublicationMetadata{}, os.ErrNotExist
		}
	} else {
		if metadataErr != nil || metadata.RemoteURL == "" {
			return res, errors.New("this embedded instance has no instance-local Hub identity (.agentsfs/hub.json is missing or invalid); pass an explicit owner/repository target — no folder-name or repository-remote guess will be made")
		}
		remote = metadata.RemoteURL
		var ok bool
		owner, slug, ok = parseRepoURL(remote)
		if !ok {
			return res, errors.New("the instance-local Hub URL is invalid; repair .agentsfs/hub.json or pass an explicit owner/repository target")
		}
	}
	repository := owner + "/" + slug
	res.Repository = repository
	authenticated := authenticatedRemote(remote, cfg)
	remoteCommit, err := lsRemoteRef(repoRoot, authenticated, "refs/heads/main")
	if err != nil {
		return res, err
	}
	if remoteCommit == "" {
		return res, fmt.Errorf("%s has no main branch to pull", repository)
	}
	if err := fetchRemoteRef(repoRoot, authenticated, "refs/heads/main", core.PublicationTrackingRef(root)); err != nil {
		return res, fmt.Errorf("fetching hub/main: %w", err)
	}
	head, ok := gitOutput(repoRoot, "rev-parse", "HEAD")
	if !ok {
		return res, errors.New("the host repository has no commit to merge into")
	}
	remoteLedger, err := lsRemoteRef(repoRoot, authenticated, core.ProjectionLedgerRef)
	if err != nil {
		return res, err
	}
	if remoteLedger != "" {
		if err := fetchRemoteRef(repoRoot, authenticated, core.ProjectionLedgerRef, core.PublicationLedgerTrackingRef(root)); err != nil {
			return res, fmt.Errorf("fetching the Hub projection ledger: %w", err)
		}
		ledger, ledgerErr := readProjectionLedger(repoRoot, core.PublicationLedgerTrackingRef(root))
		if ledgerErr != nil {
			return res, fmt.Errorf("the Hub projection ledger is invalid: %w", ledgerErr)
		}
		if ledger.Repository != repository {
			return res, fmt.Errorf("the Hub projection ledger identifies %s, not %s; refusing to cross-link repositories", ledger.Repository, repository)
		}
		metadata = metadataFromLedger(remote, ledger)
		metadataErr = nil
	}
	baseMatchesHost := metadataErr == nil && publicationBaseMatchesHost(repoRoot, resolution.Prefix, head, metadata)
	if metadataErr == nil && metadata.LastPush != nil && !baseMatchesHost {
		metadata.LastPush = nil
		metadata.IntegratedHubCommit = ""
	}
	if remoteLedger == "" && baseMatchesHost {
		ledgerCommit, ledgerErr := publishLegacyProjectionLedger(repoRoot, authenticated, repository, remoteCommit, metadata, cfg)
		if ledgerErr != nil {
			return res, fmt.Errorf("upgrading the legacy projection identity before pull: %w", ledgerErr)
		}
		_ = exec.Command("git", "-C", repoRoot, "update-ref", core.PublicationLedgerTrackingRef(root), ledgerCommit).Run()
	}
	res.RemoteCommit = remoteCommit
	if gitIsAncestor(repoRoot, remoteCommit, head) || projectionTipFromHistory(repoRoot, repository) == remoteCommit {
		res.Already = true
		metadata = projectionMetadataForPull(metadata, remote, repository)
		metadata.IntegratedHubCommit = remoteCommit
		if err := core.SavePublicationMetadata(root, metadata); err != nil {
			return res, err
		}
		res.HostCommit = head
		return res, nil
	}

	lastPublished := ""
	if metadataErr == nil && metadata.LastPush != nil {
		lastPublished = metadata.LastPush.VerifiedRemoteCommit
	}
	if lastPublished == remoteCommit {
		// The Hub has not changed since the last push. Its content is already in
		// the host even though the projected commit is intentionally not a host
		// ancestor.
		res.Already = true
		res.HostCommit = head
		return res, nil
	}

	baseCommit := projectionTipFromHistory(repoRoot, repository)
	if baseCommit == "" {
		baseCommit = lastPublished
	}
	if baseCommit == "" {
		plain, splitErr := revisionForPushOnto(root, "")
		if splitErr == nil && gitTreesEqual(repoRoot, plain, remoteCommit) && opts.Adopt {
			baseCommit = remoteCommit
			res.Adopted = true
		} else {
			return res, fmt.Errorf("no recoverable projection base exists for %s\n\nnothing was changed. If this checkout is known to contain exactly the Hub state, rerun with `--adopt`; otherwise restore .agentsfs/hub.json or reconcile in a clean migration checkout", repository)
		}
	}
	if !gitIsAncestor(repoRoot, baseCommit, remoteCommit) {
		return res, fmt.Errorf("recorded projection base %s is not an ancestor of hub/main %s; the Hub history appears rewritten and was not merged", shortCommit(baseCommit), shortCommit(remoteCommit))
	}

	metadata = projectionMetadataForPull(metadata, remote, repository)
	pending := pendingProjectionPull{
		InstanceRoot: root, RemoteURL: core.CredentialFreeURL(remote),
		Repository: repository, HostHead: head, BaseCommit: baseCommit,
		RemoteCommit: remoteCommit, Adopted: res.Adopted, Metadata: metadata,
	}
	if err := beginProjectionMerge(repoRoot, resolution.Prefix, head, baseCommit, remoteCommit, pending); err != nil {
		return res, err
	}
	conflicts := unmergedPaths(repoRoot)
	if len(conflicts) > 0 {
		res.Conflicts = conflicts
		return res, fmt.Errorf("Hub changes were applied under %s/ with %d conflict(s): %s\nresolve them, `git add` the resolutions, then run `afs hub pull --continue` (or `afs hub pull --abort`)", resolution.Prefix, len(conflicts), strings.Join(conflicts, ", "))
	}
	return continueProjectionPull(repoRoot)
}

func projectionMetadataForPull(metadata core.PublicationMetadata, remote, repository string) core.PublicationMetadata {
	metadata.Mode = core.PublicationModeEmbeddedProjection
	metadata.SyncVersion = core.ProjectionProtocolVersion
	metadata.LedgerRef = core.ProjectionLedgerRef
	metadata.RemoteName = "hub"
	metadata.RemoteURL = core.CredentialFreeURL(remote)
	metadata.Repository = repository
	metadata.PublishBranch = "main"
	return metadata
}

func beginProjectionMerge(repoRoot, prefix, head, baseHub, remoteHub string, pending pendingProjectionPull) error {
	baseTree, err := treeWithProjection(repoRoot, head, prefix, baseHub)
	if err != nil {
		return fmt.Errorf("building the projection merge base: %w", err)
	}
	theirsTree, err := treeWithProjection(repoRoot, head, prefix, remoteHub)
	if err != nil {
		return fmt.Errorf("mapping hub/main under %s/: %w", prefix, err)
	}
	if err := savePendingProjectionPull(repoRoot, pending); err != nil {
		return err
	}
	readTree := exec.Command("git", "-C", repoRoot, "read-tree", "-m", "-u", baseTree, head+"^{tree}", theirsTree)
	if out, err := readTree.CombinedOutput(); err != nil {
		_ = exec.Command("git", "-C", repoRoot, "reset", "--merge", head).Run()
		_ = removePendingProjectionPull(repoRoot)
		return fmt.Errorf("preparing the three-way projection merge: %v: %s", err, strings.TrimSpace(string(out)))
	}
	mergeFiles := exec.Command("git", "-C", repoRoot, "merge-index", "git-merge-one-file", "-a")
	if out, err := mergeFiles.CombinedOutput(); err != nil && len(unmergedPaths(repoRoot)) == 0 {
		_ = exec.Command("git", "-C", repoRoot, "reset", "--merge", head).Run()
		_ = removePendingProjectionPull(repoRoot)
		return fmt.Errorf("merging projection files: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func treeWithProjection(repoRoot, hostCommit, prefix, hubCommit string) (string, error) {
	idx, err := os.CreateTemp("", "afs-projection-index-*")
	if err != nil {
		return "", err
	}
	idxPath := idx.Name()
	if err := idx.Close(); err != nil {
		return "", err
	}
	defer os.Remove(idxPath)
	env := append(os.Environ(), "GIT_INDEX_FILE="+idxPath)
	run := func(stdin string, args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
		cmd.Env = env
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	if _, err := run("", "read-tree", hostCommit); err != nil {
		return "", err
	}
	paths, err := exec.Command("git", "-C", repoRoot, "ls-tree", "-r", "--name-only", hostCommit, "--", prefix).Output()
	if err != nil {
		return "", err
	}
	for _, path := range strings.Split(strings.TrimSpace(string(paths)), "\n") {
		if path == "" {
			continue
		}
		if _, err := run("", "update-index", "--force-remove", "--", path); err != nil {
			return "", err
		}
	}
	if _, err := run("", "read-tree", "--prefix="+filepath.ToSlash(prefix)+"/", hubCommit+"^{tree}"); err != nil {
		return "", err
	}
	return run("", "write-tree")
}

func mergeInProgress(repoRoot string) bool {
	return exec.Command("git", "-C", repoRoot, "rev-parse", "-q", "--verify", "MERGE_HEAD").Run() == nil
}

func projectionPullPending(repoRoot string) bool {
	path, err := pendingProjectionPullPath(repoRoot)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func unmergedPaths(repoRoot string) []string {
	out, _ := exec.Command("git", "-C", repoRoot, "diff", "--name-only", "--diff-filter=U").Output()
	var paths []string
	for _, path := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func pendingProjectionPullPath(repoRoot string) (string, error) {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--git-path", "AFS_HUB_PULL").Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	return path, nil
}

func savePendingProjectionPull(repoRoot string, pending pendingProjectionPull) error {
	path, err := pendingProjectionPullPath(repoRoot)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func loadPendingProjectionPull(repoRoot string) (pendingProjectionPull, error) {
	var pending pendingProjectionPull
	path, err := pendingProjectionPullPath(repoRoot)
	if err != nil {
		return pending, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pending, errors.New("no AgentsFS Hub projection pull is pending")
	}
	if err := json.Unmarshal(data, &pending); err != nil {
		return pending, err
	}
	return pending, nil
}

func removePendingProjectionPull(repoRoot string) error {
	path, err := pendingProjectionPullPath(repoRoot)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func continueProjectionPull(repoRoot string) (ProjectionPullResult, error) {
	var res ProjectionPullResult
	pending, err := loadPendingProjectionPull(repoRoot)
	if err != nil {
		return res, err
	}
	res.InstanceRoot = pending.InstanceRoot
	res.Repository = pending.Repository
	res.RemoteCommit = pending.RemoteCommit
	res.Adopted = pending.Adopted
	if resolution, resolveErr := core.ResolveInstance(pending.InstanceRoot, core.ResolveInstanceOptions{}); resolveErr == nil {
		res.Prefix = resolution.Prefix
	}
	if conflicts := unmergedPaths(repoRoot); len(conflicts) > 0 {
		res.Conflicts = conflicts
		return res, fmt.Errorf("%d projection conflict(s) remain unresolved: %s", len(conflicts), strings.Join(conflicts, ", "))
	}
	if mergeInProgress(repoRoot) {
		return res, errors.New("a separate Git merge is in progress; finish or abort it before continuing the AgentsFS projection pull")
	}
	headBefore, _ := gitOutput(repoRoot, "rev-parse", "HEAD")
	if headBefore == pending.HostHead {
		message := fmt.Sprintf("Fold Hub projection %s\n\nagentsfs-hub-base: %s\nagentsfs-hub-tip: %s\nagentsfs-hub-repo: %s\n", pending.Repository, pending.BaseCommit, pending.RemoteCommit, pending.Repository)
		cmd := exec.Command("git", "-C", repoRoot, "commit", "--allow-empty", "-m", message)
		cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
		if out, err := cmd.CombinedOutput(); err != nil {
			return res, fmt.Errorf("committing the projection merge: %v: %s", err, strings.TrimSpace(string(out)))
		}
	}
	head, ok := gitOutput(repoRoot, "rev-parse", "HEAD")
	if !ok || projectionTipFromHistory(repoRoot, pending.Repository) != pending.RemoteCommit {
		return res, errors.New("the completed fold does not record the fetched Hub commit; publication state was not advanced")
	}
	pending.Metadata.IntegratedHubCommit = pending.RemoteCommit
	if err := core.SavePublicationMetadata(pending.InstanceRoot, pending.Metadata); err != nil {
		return res, fmt.Errorf("recording the integrated Hub state: %w", err)
	}
	if err := removePendingProjectionPull(repoRoot); err != nil {
		return res, err
	}
	res.HostCommit = head
	return res, nil
}

func abortProjectionPull(repoRoot string) error {
	pending, err := loadPendingProjectionPull(repoRoot)
	if err != nil {
		return err
	}
	if mergeInProgress(repoRoot) {
		return errors.New("a separate Git merge is in progress; abort it with Git before aborting the AgentsFS projection pull")
	}
	cmd := exec.Command("git", "-C", repoRoot, "reset", "--merge", pending.HostHead)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("aborting projection pull: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return removePendingProjectionPull(repoRoot)
}

// projectionTipFromHistory recovers a folded pull after cloning the host on a
// new machine, where .agentsfs/hub.json is intentionally absent. Trailers are
// data, not heuristics: a candidate is accepted only for the exact Hub repo.
func projectionTipFromHistory(repoRoot, repository string) string {
	format := "%H%x1f%(trailers:key=agentsfs-hub-tip,valueonly)%x1f%(trailers:key=agentsfs-hub-repo,valueonly)%x1e"
	out, err := exec.Command("git", "-C", repoRoot, "log", "--format="+format).Output()
	if err != nil {
		return ""
	}
	for _, record := range strings.Split(string(out), "\x1e") {
		fields := strings.Split(record, "\x1f")
		if len(fields) < 3 {
			continue
		}
		tip := strings.TrimSpace(fields[1])
		repo := strings.TrimSpace(fields[2])
		if repo == repository && tip != "" && git(repoRoot, "cat-file", "-e", tip+"^{commit}") == nil {
			return tip
		}
	}
	return ""
}
