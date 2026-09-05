package core

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// StatusOptions controls the optional, potentially more expensive parts of a
// cross-instance status scan. Discovery and ordinary git inspection are always
// local and read-only. Fetch is the only option that contacts remotes.
type StatusOptions struct {
	Doctor bool
	Fetch  bool
	All    bool
}

// DefaultStatusMaxEntries prevents an accidentally broad scan from walking a
// multi-million-entry volume indefinitely. Results explicitly say when this
// budget was reached so callers can retry with narrower roots.
const DefaultStatusMaxEntries = 500_000

// DefaultStatusMaxPaths bounds machine-readable worktree output. Git still
// performs one complete porcelain read, but JSON and long-lived MCP responses
// cannot grow without limit.
const DefaultStatusMaxPaths = 10_000

// DefaultStatusTimeout is per search root. It complements the deterministic
// entry budget on slow disks, network mounts, and cloud-backed directories.
const DefaultStatusTimeout = 15 * time.Second

// A hard-timed-out scan may leave one filesystem read blocked in the kernel.
// Bound those abandoned workers in long-lived MCP servers instead of allowing
// repeated scans of a bad mount to create unbounded goroutines.
var statusScanSlots = make(chan struct{}, 4)

// StatusReport is the machine-readable result returned by afs status and the
// corresponding MCP tool.
type StatusReport struct {
	SchemaVersion   int              `json:"schema_version"`
	Presentation    string           `json:"presentation"`
	SearchRoots     []string         `json:"search_roots"`
	Scopes          []StatusScope    `json:"scopes"`
	BundledContract string           `json:"bundled_contract"`
	Instances       []InstanceStatus `json:"instances"`
	Issues          []StatusIssue    `json:"issues"`
}

// StatusScope describes what one filesystem walk actually covered. Several
// requested roots can collapse into one scope when a broader root already
// contains them.
type StatusScope struct {
	SearchRoot        string   `json:"search_root"`
	RequestedRoots    []string `json:"requested_roots"`
	EntriesVisited    int      `json:"entries_visited"`
	DirectoriesSeen   int      `json:"directories_seen"`
	DirectoriesPruned int      `json:"directories_pruned"`
	Complete          bool     `json:"complete"`
	IncompleteReason  string   `json:"incomplete_reason,omitempty"`
	maxEntries        int
	timeoutSeconds    int
}

// StatusIssue records a path that could not be inspected without aborting the
// rest of a multi-root scan.
type StatusIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// InstanceStatus summarizes one locally discoverable AgentsFS root.
type InstanceStatus struct {
	Path               string            `json:"path"`
	Description        string            `json:"description,omitempty"`
	DetectedBy         string            `json:"detected_by"`
	ContractVersion    string            `json:"contract_version,omitempty"`
	ContractState      string            `json:"contract_state"`
	Customized         bool              `json:"customized"`
	CustomizationKnown bool              `json:"customization_known"`
	Mode               string            `json:"mode"`
	Git                GitStatus         `json:"git"`
	Topology           TopologyStatus    `json:"topology"`
	Worktree           WorktreeStatus    `json:"worktree"`
	HostGit            HostGitStatus     `json:"host_git"`
	Publication        PublicationStatus `json:"publication"`
	NextActions        []NextAction      `json:"next_actions"`
	Doctor             *DoctorSummary    `json:"doctor,omitempty"`
	DuplicateOf        string            `json:"duplicate_of,omitempty"`
	identity           string
}

type TopologyStatus struct {
	Mode           string `json:"mode"`
	RepositoryRoot string `json:"repository_root,omitempty"`
	Prefix         string `json:"prefix"`
}

type PathStatus struct {
	Path         string `json:"path"`
	Status       string `json:"status"`
	OriginalPath string `json:"original_path,omitempty"`
}

type WorktreeStatus struct {
	Clean           bool         `json:"clean"`
	Staged          []PathStatus `json:"staged"`
	StagedCount     int          `json:"staged_count"`
	Unstaged        []PathStatus `json:"unstaged"`
	UnstagedCount   int          `json:"unstaged_count"`
	Untracked       []string     `json:"untracked"`
	UntrackedCount  int          `json:"untracked_count"`
	Conflicted      []PathStatus `json:"conflicted"`
	ConflictedCount int          `json:"conflicted_count"`
	Truncated       bool         `json:"truncated"`
	Error           string       `json:"error,omitempty"`
}

type HostGitStatus struct {
	Repository              bool   `json:"repository"`
	Root                    string `json:"root,omitempty"`
	Branch                  string `json:"branch,omitempty"`
	Head                    string `json:"head,omitempty"`
	Upstream                string `json:"upstream,omitempty"`
	Ahead                   int    `json:"ahead"`
	Behind                  int    `json:"behind"`
	SyncState               string `json:"sync_state"`
	KnowledgeCommits        int    `json:"knowledge_commits_since_last_push"`
	KnowledgeContentChanged bool   `json:"knowledge_content_changed"`
	HistoryRewritten        bool   `json:"history_rewritten"`
	FetchError              string `json:"fetch_error,omitempty"`
	InspectError            string `json:"inspect_error,omitempty"`
}

type PublicationStatus struct {
	Linked              bool     `json:"linked"`
	Remote              string   `json:"remote,omitempty"`
	RemoteURL           string   `json:"remote_url,omitempty"`
	Repository          string   `json:"repository,omitempty"`
	Branch              string   `json:"branch"`
	State               string   `json:"state"`
	CommitsToPublish    int      `json:"commits_to_publish"`
	LastSourceCommit    string   `json:"last_source_commit,omitempty"`
	LastProjectedCommit string   `json:"last_projected_commit,omitempty"`
	IntegratedHubCommit string   `json:"integrated_hub_commit,omitempty"`
	CachedRemoteCommit  string   `json:"cached_remote_commit,omitempty"`
	RemoteState         string   `json:"remote_state"`
	HistoryRewritten    bool     `json:"history_rewritten"`
	LegacyBranches      []string `json:"legacy_branches,omitempty"`
	Error               string   `json:"error,omitempty"`
}

type NextAction struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
	Reason  string `json:"reason"`
}

// GitStatus is deliberately credential-free: it reports the selected remote
// by name and kind, but never emits its URL because URLs can contain tokens.
type GitStatus struct {
	Repository   bool   `json:"repository"`
	Root         string `json:"root,omitempty"`
	Branch       string `json:"branch,omitempty"`
	Dirty        bool   `json:"dirty"`
	Remote       string `json:"remote,omitempty"`
	RemoteKind   string `json:"remote_kind,omitempty"`
	Upstream     string `json:"upstream,omitempty"`
	Ahead        int    `json:"ahead"`
	Behind       int    `json:"behind"`
	SyncState    string `json:"sync_state"`
	FetchError   string `json:"fetch_error,omitempty"`
	InspectError string `json:"inspect_error,omitempty"`
}

// DoctorSummary keeps fleet output compact while afs doctor remains the place
// for the complete finding list.
type DoctorSummary struct {
	Findings int    `json:"findings"`
	Errors   int    `json:"errors"`
	Warnings int    `json:"warnings"`
	Info     int    `json:"info"`
	Error    string `json:"error,omitempty"`
}

// StatusInstances discovers every AgentsFS root at or below searchRoots and
// returns local contract/git health for each. If a search root is already
// inside an instance, the enclosing root is included and scanned for nested,
// independent instances too.
func StatusInstances(searchRoots []string, opts StatusOptions) StatusReport {
	if len(searchRoots) == 0 {
		searchRoots = []string{"."}
	}
	report := StatusReport{
		SchemaVersion:   2,
		BundledContract: CurrentContractVersion(),
		Instances:       []InstanceStatus{},
		Issues:          []StatusIssue{},
		Scopes:          []StatusScope{},
	}
	if !opts.All && len(searchRoots) == 1 {
		if abs, err := canonicalPath(searchRoots[0]); err == nil {
			report.SearchRoots = append(report.SearchRoots, abs)
			if root, findErr := FindRoot(abs); findErr == nil {
				root, _ = canonicalPath(root)
				report.Scopes = append(report.Scopes, StatusScope{SearchRoot: root, RequestedRoots: []string{abs}, Complete: true})
				report.Instances = append(report.Instances, inspectInstanceStatus(root, "enclosing", opts, map[string]string{}))
				report.Presentation = "focused"
				return report
			}
			report.SearchRoots = report.SearchRoots[:0]
		}
	}
	type candidate struct {
		requested string
		scan      string
	}
	var candidates []candidate
	discovered := map[string]string{}
	for _, start := range searchRoots {
		abs, err := filepath.Abs(start)
		if err != nil {
			report.Issues = append(report.Issues, StatusIssue{Path: start, Message: err.Error()})
			continue
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		report.SearchRoots = append(report.SearchRoots, abs)
		walkRoot := abs
		if enclosing, err := FindRoot(abs); err == nil {
			walkRoot = enclosing
		}
		if resolved, err := filepath.EvalSymlinks(walkRoot); err == nil {
			walkRoot = resolved
		}
		candidates = append(candidates, candidate{requested: abs, scan: walkRoot})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if len(candidates[i].scan) != len(candidates[j].scan) {
			return len(candidates[i].scan) < len(candidates[j].scan)
		}
		return candidates[i].scan < candidates[j].scan
	})
	maxEntries := DefaultStatusMaxEntries
	timeout := DefaultStatusTimeout
	for _, candidate := range candidates {
		covered := -1
		for i := range report.Scopes {
			if statusPathWithin(candidate.scan, report.Scopes[i].SearchRoot) {
				covered = i
				break
			}
		}
		if covered >= 0 {
			report.Scopes[covered].RequestedRoots = append(report.Scopes[covered].RequestedRoots, candidate.requested)
			continue
		}
		report.Scopes = append(report.Scopes, StatusScope{
			SearchRoot:     candidate.scan,
			RequestedRoots: []string{candidate.requested},
			maxEntries:     maxEntries,
			timeoutSeconds: int((timeout + time.Second - 1) / time.Second),
			Complete:       true,
		})
	}
	for i := range report.Scopes {
		result := executeStatusScope(report.Scopes[i])
		report.Scopes[i] = result.scope
		for path, detectedBy := range result.found {
			addDiscoveredStatusRoot(path, detectedBy, discovered)
		}
		report.Issues = append(report.Issues, result.issues...)
	}

	paths := make([]string, 0, len(discovered))
	for path := range discovered {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	fetched := map[string]string{}
	for _, path := range paths {
		st := inspectInstanceStatus(path, discovered[path], opts, fetched)
		report.Instances = append(report.Instances, st)
	}
	markDuplicateCheckouts(report.Instances)
	if opts.All || len(report.Instances) != 1 {
		report.Presentation = "fleet"
	} else {
		report.Presentation = "focused"
	}
	return report
}

// InspectInstanceStatus returns the shared focused status model without a
// downward fleet scan. Callers must resolve the instance first.
func InspectInstanceStatus(path string, opts StatusOptions) InstanceStatus {
	return inspectInstanceStatus(path, "explicit", opts, map[string]string{})
}

type statusScopeResult struct {
	scope  StatusScope
	found  map[string]string
	issues []StatusIssue
}

func executeStatusScope(scope StatusScope) statusScopeResult {
	run := func() statusScopeResult {
		result := statusScopeResult{scope: scope, found: map[string]string{}}
		discoverStatusRoots(&result.scope, result.found, &result.issues)
		return result
	}
	if scope.timeoutSeconds <= 0 {
		return run()
	}
	timer := time.NewTimer(time.Duration(scope.timeoutSeconds) * time.Second)
	defer timer.Stop()
	select {
	case statusScanSlots <- struct{}{}:
	case <-timer.C:
		scope.Complete = false
		scope.IncompleteReason = fmt.Sprintf("hard time limit %ds reached before a scanner was available", scope.timeoutSeconds)
		return statusScopeResult{scope: scope, found: map[string]string{}}
	}
	resultCh := make(chan statusScopeResult, 1)
	go func() {
		defer func() { <-statusScanSlots }()
		resultCh <- run()
	}()
	select {
	case result := <-resultCh:
		return result
	case <-timer.C:
		scope.Complete = false
		scope.IncompleteReason = fmt.Sprintf("hard time limit %ds reached; results from this scope were not retained", scope.timeoutSeconds)
		return statusScopeResult{scope: scope, found: map[string]string{}}
	}
}

func discoverStatusRoots(scope *StatusScope, found map[string]string, issues *[]StatusIssue) {
	root := scope.SearchRoot
	info, err := os.Stat(root)
	if err != nil {
		*issues = append(*issues, StatusIssue{Path: root, Message: err.Error()})
		scope.Complete = false
		scope.IncompleteReason = "search root could not be inspected"
		return
	}
	if !info.IsDir() {
		*issues = append(*issues, StatusIssue{Path: root, Message: "search root is not a directory"})
		scope.Complete = false
		scope.IncompleteReason = "search root is not a directory"
		return
	}
	home, _ := os.UserHomeDir()
	var deadline time.Time
	if scope.timeoutSeconds > 0 {
		deadline = time.Now().Add(time.Duration(scope.timeoutSeconds) * time.Second)
	}
	scope.EntriesVisited = 1 // the search root itself
	scope.DirectoriesSeen = 1
	stack := []string{root}
	firstDirectory := true
	for len(stack) > 0 {
		if !deadline.IsZero() && time.Now().After(deadline) {
			scope.Complete = false
			scope.IncompleteReason = fmt.Sprintf("time limit %ds reached", scope.timeoutSeconds)
			return
		}
		if !firstDirectory && scope.maxEntries > 0 && scope.EntriesVisited >= scope.maxEntries {
			scope.Complete = false
			scope.IncompleteReason = fmt.Sprintf("entry limit %d reached", scope.maxEntries)
			return
		}
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		firstDirectory = false
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			*issues = append(*issues, StatusIssue{Path: dir, Message: readErr.Error()})
			scope.Complete = false
			if scope.IncompleteReason == "" {
				scope.IncompleteReason = "one or more paths could not be inspected"
			}
			continue
		}
		var children []string
		for _, entry := range entries {
			if !deadline.IsZero() && time.Now().After(deadline) {
				scope.Complete = false
				scope.IncompleteReason = fmt.Sprintf("time limit %ds reached", scope.timeoutSeconds)
				return
			}
			if scope.maxEntries > 0 && scope.EntriesVisited >= scope.maxEntries {
				scope.Complete = false
				scope.IncompleteReason = fmt.Sprintf("entry limit %d reached", scope.maxEntries)
				return
			}
			scope.EntriesVisited++
			name := entry.Name()
			if entry.Type()&os.ModeSymlink != 0 {
				continue // broad scans never probe symlinks; pass one directly to scan it
			}
			if !entry.IsDir() {
				if name == "AGENTS.md" {
					path := filepath.Join(dir, name)
					if declaresContract(path) {
						addDiscoveredStatusRoot(dir, "AGENTS.md", found)
					}
				}
				continue
			}
			scope.DirectoriesSeen++
			if name == ".agentsfs" {
				addDiscoveredStatusRoot(dir, ".agentsfs", found)
				scope.DirectoriesPruned++
				continue
			}
			path := filepath.Join(dir, name)
			if pruneStatusDirectory(path, home) {
				scope.DirectoriesPruned++
				continue
			}
			children = append(children, path)
		}
		// os.ReadDir is name-sorted. Push in reverse so the LIFO walk remains
		// deterministic and visits lexical order.
		for i := len(children) - 1; i >= 0; i-- {
			stack = append(stack, children[i])
		}
	}
}

func addDiscoveredStatusRoot(path, detectedBy string, found map[string]string) {
	canonical := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		canonical = resolved
	}
	if existing, ok := found[canonical]; !ok || existing == "AGENTS.md" {
		found[canonical] = detectedBy
	}
}

func statusPathWithin(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func pruneStatusDirectory(path, home string) bool {
	base := filepath.Base(path)
	switch base {
	case ".git", "node_modules", ".cache", ".npm", ".pnpm-store", ".Trash",
		".venv", "venv", "__pycache__", ".tox", ".mypy_cache", ".pytest_cache",
		"target", ".gradle", ".terraform":
		return true
	}
	// Broad filesystem-root scans should not descend into kernel/runtime trees
	// or macOS system state. Passing one of these paths directly still scans it.
	switch filepath.Clean(path) {
	case "/proc", "/sys", "/dev", "/run", "/System", "/Library", "/private/var/run", "/private/var/folders":
		return true
	}
	// A home-directory scan on macOS otherwise walks a very large body of
	// application state that is not user knowledge. Supplying ~/Library itself
	// still scans it because the walk root is never pruned.
	return home != "" && path == filepath.Join(home, "Library")
}

// rootDescription resolves an instance's per-workspace label the same way the Hub
// does (repoFilesMeta in internal/hub/web.go): prefer the root INDEX.md
// (contract 0.7.0+), fall back to AGENTS.md for instances that predate it,
// then README.md, and treat the template placeholder or the pre-0.7.0
// contract boilerplate as no description at all rather than surfacing the
// same meaningless label for every instance.
func rootDescription(path string) string {
	for _, name := range []string{"INDEX.md", "AGENTS.md", "README.md"} {
		d := strings.TrimSpace(Description(filepath.Join(path, name)))
		if d != "" && !IsPlaceholderRootDescription(d) {
			return d
		}
	}
	return ""
}

func inspectInstanceStatus(path, detectedBy string, opts StatusOptions, fetched map[string]string) InstanceStatus {
	version := ContractVersion(path)
	state := "missing"
	if version != "" {
		switch CompareContractVersions(version, CurrentContractVersion()) {
		case -1:
			state = "behind"
		case 0:
			state = "current"
		default:
			state = "ahead"
		}
	}
	customized, customizationKnown := ContractCustomized(path)
	st := InstanceStatus{
		Path:               path,
		Description:        rootDescription(path),
		DetectedBy:         detectedBy,
		ContractVersion:    version,
		ContractState:      state,
		Customized:         customized,
		CustomizationKnown: customizationKnown,
		Mode:               "unversioned",
		NextActions:        []NextAction{},
	}

	repoRoot, inRepo := EnclosingRepoRoot(path)
	if inRepo {
		prefix := "."
		topologyMode := "standalone"
		if sameStatusPath(path, repoRoot) {
			st.Mode = "standalone"
		} else {
			st.Mode = "shared"
			topologyMode = "embedded"
			if rel, err := filepath.Rel(repoRoot, path); err == nil {
				prefix = filepath.ToSlash(rel)
			}
		}
		st.Topology = TopologyStatus{Mode: topologyMode, RepositoryRoot: repoRoot, Prefix: prefix}
		st.Worktree = inspectWorktreeStatus(path, repoRoot, prefix)
		st.HostGit = inspectHostGitStatus(repoRoot)
		if opts.Fetch {
			if remote := remoteForStatus(st.HostGit.Upstream, nil); remote != "" {
				key := repoRoot + "|host|" + remote
				if _, done := fetched[key]; !done {
					fetched[key] = fetchHostRemote(repoRoot, remote)
				}
				st.HostGit = inspectHostGitStatus(repoRoot)
				st.HostGit.FetchError = fetched[key]
			}
		}
		st.Publication = inspectPublicationStatus(path, repoRoot, prefix, st.HostGit)
		if opts.Fetch && st.Publication.Linked {
			key := repoRoot + "|" + st.Publication.RemoteURL
			if _, done := fetched[key]; !done {
				fetched[key] = fetchPublicationRemote(path, repoRoot, st.Publication.RemoteURL)
			}
			st.Publication = inspectPublicationStatus(path, repoRoot, prefix, st.HostGit)
			if fetched[key] == "" {
				st.Publication.RemoteState = "fresh"
			} else {
				st.Publication.Error = fetched[key]
			}
		}
		st.HostGit.KnowledgeCommits = st.Publication.CommitsToPublish
		st.HostGit.KnowledgeContentChanged = st.Publication.State == "commits-to-publish" || st.Publication.State == "diverged"
		st.HostGit.HistoryRewritten = st.Publication.HistoryRewritten
		st.Git, st.identity = legacyGitStatus(path, st)
		st.NextActions = deriveNextActions(st)
	} else {
		st.Git.SyncState = "not-a-repository"
		st.Topology = TopologyStatus{Mode: "unversioned", Prefix: "."}
		st.Worktree = WorktreeStatus{Clean: true, Staged: []PathStatus{}, Unstaged: []PathStatus{}, Untracked: []string{}, Conflicted: []PathStatus{}}
		st.HostGit.SyncState = "not-a-repository"
		st.Publication = PublicationStatus{Branch: "main", State: "unlinked", RemoteState: "unavailable"}
	}

	if opts.Doctor {
		st.Doctor = summarizeDoctor(path)
	}
	return st
}

func legacyGitStatus(instance string, st InstanceStatus) (GitStatus, string) {
	remotes, _ := optionalGit(st.HostGit.Root, "remote")
	remote := remoteForStatus(st.HostGit.Upstream, strings.Fields(remotes))
	gitStatus := GitStatus{
		Repository:   st.HostGit.Repository,
		Root:         st.HostGit.Root,
		Branch:       st.HostGit.Branch,
		Dirty:        !st.Worktree.Clean,
		Remote:       remote,
		RemoteKind:   "git",
		Upstream:     st.HostGit.Upstream,
		Ahead:        st.HostGit.Ahead,
		Behind:       st.HostGit.Behind,
		SyncState:    st.HostGit.SyncState,
		FetchError:   st.HostGit.FetchError,
		InspectError: st.HostGit.InspectError,
	}
	if gitStatus.Remote == "" && st.Publication.Linked {
		gitStatus.Remote = st.Publication.Remote
	}
	if st.Publication.Linked && gitStatus.Remote == st.Publication.Remote {
		gitStatus.RemoteKind = "hub"
	}
	remoteURL := ""
	if gitStatus.Remote != "" {
		remoteURL, _ = optionalGit(st.HostGit.Root, "remote", "get-url", gitStatus.Remote)
	}
	identity := normalizeRemoteIdentity(remoteURL, st.HostGit.Root)
	if identity != "" {
		if rel, err := filepath.Rel(st.HostGit.Root, instance); err == nil {
			identity += "|" + filepath.ToSlash(rel)
		}
	}
	return gitStatus, identity
}

// InspectWorktreeStatus returns porcelain-v2 path state scoped to one
// instance. Paths are slash-relative to the instance root.
func InspectWorktreeStatus(instance string) WorktreeStatus {
	repoRoot, ok := EnclosingRepoRoot(instance)
	if !ok {
		return WorktreeStatus{Clean: true, Staged: []PathStatus{}, Unstaged: []PathStatus{}, Untracked: []string{}, Conflicted: []PathStatus{}, Error: "not a Git repository"}
	}
	prefix := "."
	if rel, err := filepath.Rel(repoRoot, instance); err == nil && rel != "." {
		prefix = filepath.ToSlash(rel)
	}
	return inspectWorktreeStatus(instance, repoRoot, prefix)
}

func inspectWorktreeStatus(instance, repoRoot, prefix string) WorktreeStatus {
	st := WorktreeStatus{Clean: true, Staged: []PathStatus{}, Unstaged: []PathStatus{}, Untracked: []string{}, Conflicted: []PathStatus{}}
	cmd := exec.Command("git", "--no-optional-locks", "status", "--porcelain=v2", "-z", "--untracked-files=all", "--", prefix)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		st.Error = "could not inspect working tree"
		return st
	}
	records := bytes.Split(out, []byte{0})
	for i := 0; i < len(records); i++ {
		record := string(records[i])
		if record == "" {
			continue
		}
		switch record[0] {
		case '?':
			st.Untracked = append(st.Untracked, instanceRelativePath(strings.TrimPrefix(record, "? "), prefix))
		case 'u':
			fields := strings.SplitN(record, " ", 11)
			if len(fields) == 11 {
				st.Conflicted = append(st.Conflicted, PathStatus{Path: instanceRelativePath(fields[10], prefix), Status: "conflicted"})
			}
		case '1', '2':
			limit := 9
			if record[0] == '2' {
				limit = 10
			}
			fields := strings.SplitN(record, " ", limit)
			if len(fields) != limit || len(fields[1]) != 2 {
				continue
			}
			xy := fields[1]
			path := instanceRelativePath(fields[limit-1], prefix)
			original := ""
			if record[0] == '2' && i+1 < len(records) {
				i++
				original = instanceRelativePath(string(records[i]), prefix)
			}
			if isConflictCode(xy) {
				st.Conflicted = append(st.Conflicted, PathStatus{Path: path, OriginalPath: original, Status: "conflicted"})
				continue
			}
			if xy[0] != '.' {
				st.Staged = append(st.Staged, PathStatus{Path: path, OriginalPath: original, Status: gitPathStatus(xy[0])})
			}
			if xy[1] != '.' {
				st.Unstaged = append(st.Unstaged, PathStatus{Path: path, Status: gitPathStatus(xy[1])})
			}
		}
	}
	st.StagedCount = len(st.Staged)
	st.UnstagedCount = len(st.Unstaged)
	st.UntrackedCount = len(st.Untracked)
	st.ConflictedCount = len(st.Conflicted)
	st.Clean = st.StagedCount+st.UnstagedCount+st.UntrackedCount+st.ConflictedCount == 0
	truncateWorktreeStatus(&st, DefaultStatusMaxPaths)
	return st
}

func truncateWorktreeStatus(st *WorktreeStatus, limit int) {
	if limit <= 0 {
		return
	}
	remaining := limit
	trimPaths := func(paths []PathStatus) []PathStatus {
		if len(paths) <= remaining {
			remaining -= len(paths)
			return paths
		}
		st.Truncated = true
		out := paths[:remaining]
		remaining = 0
		return out
	}
	st.Conflicted = trimPaths(st.Conflicted)
	st.Staged = trimPaths(st.Staged)
	st.Unstaged = trimPaths(st.Unstaged)
	if len(st.Untracked) > remaining {
		st.Untracked = st.Untracked[:remaining]
		st.Truncated = true
	}
}

func instanceRelativePath(path, prefix string) string {
	path = filepath.ToSlash(path)
	if prefix != "." {
		path = strings.TrimPrefix(path, strings.TrimSuffix(prefix, "/")+"/")
	}
	return path
}

func isConflictCode(xy string) bool {
	switch xy {
	case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
		return true
	default:
		return false
	}
}

func gitPathStatus(code byte) string {
	switch code {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "type-changed"
	default:
		return "modified"
	}
}

func inspectHostGitStatus(repoRoot string) HostGitStatus {
	st := HostGitStatus{Repository: true, Root: repoRoot, SyncState: "unknown"}
	st.Head, _ = optionalGit(repoRoot, "rev-parse", "HEAD")
	st.Branch, _ = optionalGit(repoRoot, "branch", "--show-current")
	if st.Branch == "" {
		st.Branch = "detached"
	}
	st.Upstream, _ = optionalGit(repoRoot, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if st.Upstream == "" {
		remotes, _ := optionalGit(repoRoot, "remote")
		if strings.TrimSpace(remotes) == "" {
			st.SyncState = "no-remote"
		} else {
			st.SyncState = "no-upstream"
		}
		return st
	}
	counts, ok := optionalGit(repoRoot, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if !ok {
		st.InspectError = "could not compare HEAD with host upstream"
		return st
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		st.InspectError = "unexpected Git ahead/behind result"
		return st
	}
	st.Ahead, _ = strconv.Atoi(fields[0])
	st.Behind, _ = strconv.Atoi(fields[1])
	switch {
	case st.Ahead > 0 && st.Behind > 0:
		st.SyncState = "diverged"
	case st.Ahead > 0:
		st.SyncState = "ahead"
	case st.Behind > 0:
		st.SyncState = "behind"
	default:
		st.SyncState = "synced"
	}
	return st
}

func inspectPublicationStatus(instance, repoRoot, prefix string, host HostGitStatus) PublicationStatus {
	st := PublicationStatus{Branch: "main", State: "unlinked", RemoteState: "unavailable"}
	metadata, metadataErr := LoadPublicationMetadata(instance)
	metadataExists := metadataErr == nil
	if metadataErr == nil && metadata.RemoteURL != "" {
		st.Linked = true
		st.Remote = metadata.RemoteName
		if st.Remote == "" {
			st.Remote = "hub"
		}
		st.RemoteURL = CredentialFreeURL(metadata.RemoteURL)
		st.Repository = metadata.Repository
		if st.Repository == "" {
			st.Repository = PublicationRepository(st.RemoteURL)
		}
		if metadata.PublishBranch != "" {
			st.Branch = metadata.PublishBranch
		}
		if metadata.LastPush != nil {
			st.LastSourceCommit = metadata.LastPush.SourceRepoHead
			st.LastProjectedCommit = metadata.LastPush.ProjectedCommit
			st.CachedRemoteCommit = metadata.LastPush.VerifiedRemoteCommit
			st.RemoteState = "cached"
		}
		st.IntegratedHubCommit = metadata.IntegratedHubCommit
	} else if remoteURL, ok := optionalGit(repoRoot, "remote", "get-url", "hub"); ok && remoteURL != "" && RepositoryRemoteAppliesToInstance(instance, repoRoot) {
		st.Linked = true
		st.Remote = "hub"
		st.RemoteURL = CredentialFreeURL(remoteURL)
		st.Repository = PublicationRepository(remoteURL)
		st.State = "unknown"
		st.RemoteState = "unavailable"
		st.LegacyBranches = knownNonMainHubBranches(repoRoot)
	}
	if !st.Linked {
		return st
	}
	if cached, ok := optionalGit(repoRoot, "rev-parse", "--verify", PublicationTrackingRef(instance)); ok {
		st.CachedRemoteCommit = cached
		st.RemoteState = "cached"
	}
	if st.LastSourceCommit == "" || st.LastProjectedCommit == "" {
		if metadataExists {
			st.State = "never-published"
		} else {
			st.State = "unknown"
		}
		return st
	}
	if host.Head == "" || !gitObjectExists(repoRoot, st.LastSourceCommit+"^{commit}") {
		st.State = "unknown"
		st.Error = "last published host source commit is unavailable; run `afs hub status --fetch` or publish again after verifying history"
		return st
	}
	changed, commits, rewritten := committedInstanceChanges(repoRoot, prefix, st.LastSourceCommit, host.Head)
	st.CommitsToPublish = commits
	st.HistoryRewritten = rewritten
	remoteAhead := false
	remoteBehind := false
	remoteDiverged := false
	comparisonBase := st.LastProjectedCommit
	if st.IntegratedHubCommit != "" {
		comparisonBase = st.IntegratedHubCommit
	}
	if st.CachedRemoteCommit != "" && st.CachedRemoteCommit != comparisonBase {
		if gitIsAncestor(repoRoot, comparisonBase, st.CachedRemoteCommit) {
			remoteAhead = true
		} else if gitIsAncestor(repoRoot, st.CachedRemoteCommit, comparisonBase) {
			remoteBehind = true
		} else {
			remoteDiverged = true
		}
	}
	switch {
	case (remoteAhead || remoteDiverged) && changed:
		st.State = "diverged"
	case remoteAhead:
		st.State = "remote-ahead"
	case remoteDiverged:
		st.State = "diverged"
	case remoteBehind:
		st.State = "commits-to-publish"
	case changed:
		st.State = "commits-to-publish"
	default:
		st.State = "published"
	}
	return st
}

func knownNonMainHubBranches(repoRoot string) []string {
	out, ok := optionalGit(repoRoot, "for-each-ref", "--format=%(refname:strip=3)", "refs/remotes/hub")
	if !ok {
		return nil
	}
	var branches []string
	for _, branch := range strings.Fields(out) {
		if branch != "main" && branch != "HEAD" {
			branches = append(branches, branch)
		}
	}
	sort.Strings(branches)
	return branches
}

func committedInstanceChanges(repoRoot, prefix, lastSource, head string) (bool, int, bool) {
	if lastSource == "" || head == "" {
		return false, 0, false
	}
	rewritten := !gitIsAncestor(repoRoot, lastSource, head)
	oldTree := instanceTreeID(repoRoot, lastSource, prefix)
	newTree := instanceTreeID(repoRoot, head, prefix)
	changed := oldTree == "" || newTree == "" || oldTree != newTree
	if !changed || rewritten {
		return changed, 0, rewritten
	}
	args := []string{"rev-list", "--count", lastSource + ".." + head}
	if prefix != "." {
		args = append(args, "--", prefix)
	}
	out, ok := optionalGit(repoRoot, args...)
	if !ok {
		return true, 0, false
	}
	count, _ := strconv.Atoi(strings.TrimSpace(out))
	return true, count, false
}

func instanceTreeID(repoRoot, revision, prefix string) string {
	spec := revision + "^{tree}"
	if prefix != "." {
		spec = revision + ":" + prefix
	}
	out, _ := optionalGit(repoRoot, "rev-parse", "--verify", spec)
	return out
}

func gitObjectExists(repoRoot, object string) bool {
	return exec.Command("git", "-C", repoRoot, "cat-file", "-e", object).Run() == nil
}

func gitIsAncestor(repoRoot, older, newer string) bool {
	if older == "" || newer == "" {
		return false
	}
	return exec.Command("git", "-C", repoRoot, "merge-base", "--is-ancestor", older, newer).Run() == nil
}

func fetchPublicationRemote(instance, repoRoot, remoteURL string) string {
	if remoteURL == "" {
		return "Hub remote URL is unavailable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	refspec := "+refs/heads/main:" + PublicationTrackingRef(instance)
	cmd := exec.CommandContext(ctx, "git", "fetch", "--quiet", "--no-tags", remoteURL, refspec)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if _, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return "Hub fetch timed out after 30s"
		}
		return "Hub fetch failed; run `afs hub status --fetch` for details"
	}
	return ""
}

func fetchHostRemote(repoRoot, remote string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "fetch", "--quiet", "--prune", remote)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if _, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return "Git fetch timed out after 30s"
		}
		return "Git fetch failed; run `git fetch " + remote + "` for details"
	}
	return ""
}

func deriveNextActions(st InstanceStatus) []NextAction {
	var actions []NextAction
	dirty := st.Worktree.StagedCount + st.Worktree.UnstagedCount + st.Worktree.UntrackedCount + st.Worktree.ConflictedCount
	if dirty > 0 {
		pathspec := "."
		if st.Topology.Prefix != "." {
			pathspec = st.Topology.Prefix
		}
		actions = append(actions, NextAction{Kind: "commit", Command: "git add -- " + pathspec + " && git commit", Reason: fmt.Sprintf("%d uncommitted file(s)", dirty)})
	}
	switch st.Publication.State {
	case "unlinked", "never-published", "commits-to-publish":
		actions = append(actions, NextAction{Kind: "publish", Command: "afs hub push", Reason: "committed AgentsFS state is not published to Hub main"})
	case "remote-ahead", "diverged":
		command := "git pull hub main"
		if st.Topology.Mode == "embedded" {
			command = "afs hub pull --instance " + st.Path
		}
		actions = append(actions, NextAction{Kind: "reconcile", Command: command, Reason: "Hub main contains history that must be reconciled without force"})
	case "unknown":
		actions = append(actions, NextAction{Kind: "refresh", Command: "afs hub status --fetch", Reason: "publication provenance or remote state is unavailable"})
	}
	return actions
}

func remoteForStatus(upstream string, remotes []string) string {
	if i := strings.IndexByte(upstream, '/'); i > 0 {
		return upstream[:i]
	}
	for _, preferred := range []string{"hub", "origin"} {
		for _, remote := range remotes {
			if remote == preferred {
				return remote
			}
		}
	}
	sort.Strings(remotes)
	if len(remotes) > 0 {
		return remotes[0]
	}
	return ""
}

func optionalGit(dir string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func normalizeRemoteIdentity(raw, repoRoot string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), ".git")
		if u.Scheme != "file" {
			return "network:" + u.Host + "/" + strings.TrimLeft(u.Path, "/")
		}
		raw = u.Path
	}
	// Normalize scp-style SSH URLs such as git@github.com:owner/repo.git
	// without retaining the username.
	if colon := strings.IndexByte(raw, ':'); colon > 0 && !filepath.IsAbs(raw) {
		host := raw[:colon]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
		path := strings.TrimSuffix(strings.TrimRight(raw[colon+1:], "/"), ".git")
		return "network:" + strings.ToLower(host) + "/" + strings.TrimLeft(path, "/")
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(repoRoot, raw)
	}
	if abs, err := filepath.Abs(raw); err == nil {
		raw = abs
	}
	if resolved, err := filepath.EvalSymlinks(raw); err == nil {
		raw = resolved
	}
	return "file:" + strings.TrimSuffix(filepath.Clean(raw), ".git")
}

func markDuplicateCheckouts(instances []InstanceStatus) {
	first := map[string]string{}
	for i := range instances {
		identity := instances[i].identity
		if identity == "" {
			continue
		}
		if path, ok := first[identity]; ok {
			instances[i].DuplicateOf = path
		} else {
			first[identity] = instances[i].Path
		}
	}
}

func summarizeDoctor(root string) *DoctorSummary {
	summary := &DoctorSummary{}
	findings, err := Doctor(root)
	if err != nil {
		summary.Error = err.Error()
		return summary
	}
	summary.Findings = len(findings)
	for _, finding := range findings {
		switch finding.Severity {
		case "error":
			summary.Errors++
		case "warn":
			summary.Warnings++
		default:
			summary.Info++
		}
	}
	return summary
}

func sameStatusPath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

// StatusSyncLabel is shared by CLI narration and tests.
func StatusSyncLabel(st GitStatus) string {
	switch st.SyncState {
	case "ahead":
		return fmt.Sprintf("ahead %d", st.Ahead)
	case "behind":
		return fmt.Sprintf("behind %d", st.Behind)
	case "diverged":
		return fmt.Sprintf("diverged %d/%d", st.Ahead, st.Behind)
	default:
		return st.SyncState
	}
}
