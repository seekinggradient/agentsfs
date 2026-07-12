package core

import (
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
}

// DefaultStatusMaxEntries prevents an accidentally broad scan from walking a
// multi-million-entry volume indefinitely. Results explicitly say when this
// budget was reached so callers can retry with narrower roots.
const DefaultStatusMaxEntries = 500_000

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
	Path               string         `json:"path"`
	Description        string         `json:"description,omitempty"`
	DetectedBy         string         `json:"detected_by"`
	ContractVersion    string         `json:"contract_version,omitempty"`
	ContractState      string         `json:"contract_state"`
	Customized         bool           `json:"customized"`
	CustomizationKnown bool           `json:"customization_known"`
	Mode               string         `json:"mode"`
	Git                GitStatus      `json:"git"`
	Doctor             *DoctorSummary `json:"doctor,omitempty"`
	DuplicateOf        string         `json:"duplicate_of,omitempty"`
	identity           string
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
		BundledContract: CurrentContractVersion(),
		Instances:       []InstanceStatus{},
		Issues:          []StatusIssue{},
		Scopes:          []StatusScope{},
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
	return report
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
		Description:        Description(filepath.Join(path, "AGENTS.md")),
		DetectedBy:         detectedBy,
		ContractVersion:    version,
		ContractState:      state,
		Customized:         customized,
		CustomizationKnown: customizationKnown,
		Mode:               "unversioned",
	}

	repoRoot, inRepo := EnclosingRepoRoot(path)
	if inRepo {
		if sameStatusPath(path, repoRoot) {
			st.Mode = "standalone"
		} else {
			st.Mode = "shared"
		}
		if opts.Fetch {
			if _, done := fetched[repoRoot]; !done {
				fetched[repoRoot] = fetchStatusRemotes(repoRoot)
			}
		}
		st.Git, st.identity = inspectGitStatus(path, repoRoot)
		st.Git.FetchError = fetched[repoRoot]
	} else {
		st.Git.SyncState = "not-a-repository"
	}

	if opts.Doctor {
		st.Doctor = summarizeDoctor(path)
	}
	return st
}

func inspectGitStatus(instance, repoRoot string) (GitStatus, string) {
	st := GitStatus{Repository: true, Root: repoRoot, SyncState: "unknown"}
	if branch, ok := optionalGit(repoRoot, "branch", "--show-current"); ok {
		st.Branch = branch
	}
	if st.Branch == "" {
		st.Branch = "detached"
	}
	if out, ok := optionalGit(instance, "status", "--porcelain", "--untracked-files=normal", "--", "."); ok {
		st.Dirty = strings.TrimSpace(out) != ""
	} else {
		st.InspectError = "could not inspect working tree"
	}

	upstream, haveUpstream := optionalGit(repoRoot, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if haveUpstream {
		st.Upstream = upstream
	}
	remotes, _ := optionalGit(repoRoot, "remote")
	remoteNames := strings.Fields(remotes)
	remote := remoteForStatus(upstream, remoteNames)
	st.Remote = remote
	if remote == "" {
		st.SyncState = "no-remote"
		return st, ""
	}
	remoteURL, _ := optionalGit(repoRoot, "remote", "get-url", remote)
	st.RemoteKind = "git"
	if remote == "hub" || strings.Contains(strings.ToLower(remoteURL), "hub.agentsfs.ai") {
		st.RemoteKind = "hub"
	}
	identity := normalizeRemoteIdentity(remoteURL, repoRoot)
	if identity != "" {
		if rel, err := filepath.Rel(repoRoot, instance); err == nil {
			identity += "|" + filepath.ToSlash(rel)
		}
	}

	if !haveUpstream {
		st.SyncState = "no-upstream"
		return st, identity
	}
	counts, ok := optionalGit(repoRoot, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if !ok {
		st.InspectError = "could not compare HEAD with upstream"
		return st, identity
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		st.InspectError = "unexpected git ahead/behind result"
		return st, identity
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
	return st, identity
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

func fetchStatusRemotes(repoRoot string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "fetch", "--all", "--prune", "--quiet")
	cmd.Dir = repoRoot
	if _, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return "git fetch timed out after 30s"
		}
		// Remote errors can echo credential-bearing URLs. Keep fleet output
		// intentionally generic and let a user run git fetch directly for detail.
		return "git fetch failed; run git fetch in this repository for details"
	}
	return ""
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
