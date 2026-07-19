// afs is the agentsfs CLI: a thin shell over internal/core, which the MCP
// server will also wrap. No capability lives here — only argument parsing,
// prompting, and narration.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"

	"agentsfs.ai/afs/internal/buildinfo"
	"agentsfs.ai/afs/internal/core"
	afsdocs "agentsfs.ai/afs/internal/docs"
	"agentsfs.ai/afs/internal/mcpserver"
	"agentsfs.ai/afs/internal/update"
)

var usage = `afs — a portable, user-owned memory for AI agents

Usage:
` + afsdocs.CommandUsage() + `

File arguments to rename are relative to the instance root (cwd-relative
also accepted when the file exists there). Semantic search needs an
embedding provider: set VOYAGE_API_KEY or OPENAI_API_KEY, then run
afs reindex --embeddings once (and again after big changes). Everything
else works with no configuration.

For semantic search setup, run afs embeddings setup openai or set
OPENAI_API_KEY/VOYAGE_API_KEY in the environment.

The substrate itself is plain files + git; afs only makes reading, upkeep,
and setup cheap. See AGENTS.md in any instance for the contract.`

func main() {
	if len(os.Args) < 2 {
		fmt.Println(usage)
		os.Exit(2)
	}
	maybeNotifyUpdate(os.Args[1], os.Args[2:])
	switch os.Args[1] {
	case "init":
		runInit(os.Args[2:])
	case "setup":
		runSetup(os.Args[2:])
	case "connect":
		runConnect(os.Args[2:])
	case "uninstall":
		runUninstall(os.Args[2:])
	case "register":
		fmt.Fprintln(os.Stderr, "afs: `register` is deprecated; use `afs connect`")
		runConnect(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "tree":
		runTree(os.Args[2:])
	case "doctor":
		runDoctor(os.Args[2:])
	case "roles":
		runRoles(os.Args[2:])
	case "backlinks":
		runBacklinks(os.Args[2:])
	case "rename":
		runRename(os.Args[2:])
	case "search":
		runSearch(os.Args[2:])
	case "embeddings":
		runEmbeddings(os.Args[2:])
	case "reindex":
		runReindex(os.Args[2:])
	case "docs":
		runDocs(os.Args[2:])
	case "contract":
		runContract(os.Args[2:])
	case "update":
		runUpdate(os.Args[2:])
	case "mcp":
		runMCP(os.Args[2:])
	case "hub":
		runHub(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("afs " + buildinfo.Version)
	case "help", "--help", "-h":
		fmt.Println(usage)
	default:
		fmt.Fprintf(os.Stderr, "afs: unknown command %q\n\n%s\n", os.Args[1], usage)
		os.Exit(2)
	}
}

func maybeNotifyUpdate(command string, args []string) {
	if !updateNotificationCommand(command, args) || !update.NotificationDue(time.Now()) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	status, err := update.Check(ctx, buildinfo.Version, buildinfo.VCSRevision())
	if err != nil || status.UpToDate {
		return
	}
	if status.LatestVersion != "" {
		fmt.Fprintf(os.Stderr, "afs: update available (%s -> %s). Run `afs update`.\n",
			status.LocalVersion, status.LatestVersion)
		return
	}
	fmt.Fprintf(os.Stderr, "afs: update available (%s -> %s). Run `afs update`.\n",
		shortOrUnknown(status.LocalRevision), buildinfo.ShortRevision(status.RemoteRevision))
}

func updateNotificationCommand(command string, args []string) bool {
	switch command {
	// "doctor" is the maintenance path: a long-lived gardener install whose
	// only traffic is `afs doctor` must still learn a newer afs (and thus a
	// newer contract) exists. The nudge is stderr-only, so `doctor --json`
	// stdout stays clean.
	case "setup", "init", "connect", "doctor", "help", "--help", "-h":
		return true
	default:
		return false
	}
}

func shortOrUnknown(rev string) string {
	if rev == "" {
		return "unknown"
	}
	return buildinfo.ShortRevision(rev)
}

func runDocs(args []string) {
	if len(args) > 1 {
		fail(fmt.Errorf("usage: afs docs [topic|--all]"))
	}
	topic := ""
	if len(args) == 1 {
		topic = args[0]
	}
	out, err := afsdocs.Render(topic)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
	if !strings.HasSuffix(out, "\n") {
		fmt.Println()
	}
}

func runUpdate(args []string) {
	var check, yes, force bool
	pos := splitArgs(args, map[string]*bool{"--check": &check, "--yes": &yes, "-y": &yes, "--force": &force})
	if len(pos) > 0 {
		fail(fmt.Errorf("usage: afs update [--check] [--yes] [--force]"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	status, err := update.Check(ctx, buildinfo.Version, buildinfo.VCSRevision())
	if err != nil && check {
		fail(err)
	}
	if err == nil {
		printUpdateStatus(status)
		if check || (status.UpToDate && !force) {
			return
		}
	} else if !force {
		fail(fmt.Errorf("could not check latest version: %w (pass --force to reinstall anyway)", err))
	}

	installDir, note, err := updateInstallDir()
	if err != nil {
		fail(err)
	}
	if installDir == "" {
		fail(fmt.Errorf("%s", note))
	}
	if !yes && !confirm(fmt.Sprintf("Update afs in %s?", installDir)) {
		fmt.Println("Update cancelled.")
		return
	}
	if err := runInstallScript(installDir); err != nil {
		fail(err)
	}
}

func printUpdateStatus(status update.Status) {
	if status.LatestVersion != "" {
		if status.UpToDate {
			fmt.Printf("afs is up to date (%s, latest release v%s)\n", status.LocalVersion, status.LatestVersion)
			return
		}
		fmt.Printf("afs update available: local %s, latest release v%s\n", status.LocalVersion, status.LatestVersion)
		return
	}
	local := shortOrUnknown(status.LocalRevision)
	remote := buildinfo.ShortRevision(status.RemoteRevision)
	if status.UpToDate {
		fmt.Printf("afs is up to date (%s, %s %s)\n", buildinfo.Version, status.Ref, local)
		return
	}
	fmt.Printf("afs update available: local %s (%s), latest %s %s\n",
		buildinfo.Version, local, status.Ref, remote)
}

func updateInstallDir() (string, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	path := cleanExecutablePath(exe)
	if err := validateUninstallBinary(path, false); err != nil || isTempExecutablePath(path) {
		return "", "afs is running from a temporary build; install it with `GOBIN=\"$HOME/.local/bin\" go install ./cmd/afs` from a checkout", nil
	}
	if !isUserInstallPath(path) {
		return "", fmt.Sprintf("afs is installed at %s, which looks package-manager or system managed. Use that manager instead, for example `brew reinstall --HEAD seekinggradient/agentsfs/afs`.", path), nil
	}
	return filepath.Dir(path), "", nil
}

func runInstallScript(installDir string) error {
	url := os.Getenv("AFS_INSTALL_SCRIPT_URL")
	if url == "" {
		url = buildinfo.InstallScript
	}
	if source := os.Getenv("AGENTSFS_SOURCE"); source != "" {
		return runSourceInstall(installDir, source)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "afs update: installer download failed (%v); falling back to source build\n", err)
		return runSourceInstall(installDir, "")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "afs update: installer unavailable (%s); falling back to source build\n", resp.Status)
		return runSourceInstall(installDir, "")
	}
	tmp, err := os.CreateTemp("", "afs-install-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	cmd := exec.Command("sh", tmp.Name())
	cmd.Env = append(os.Environ(), "AFS_INSTALL_DIR="+installDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runSourceInstall(installDir, source string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("source fallback needs Go on PATH: %w", err)
	}
	if source != "" {
		cmd := exec.Command("go", "install", "./cmd/afs")
		cmd.Dir = source
		cmd.Env = append(os.Environ(), "GOBIN="+installDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("source fallback needs git on PATH: %w", err)
	}
	tmp, err := os.MkdirTemp("", "agentsfs-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	ref := os.Getenv("AFS_UPDATE_REF")
	if ref == "" {
		ref = buildinfo.Ref
	}
	repos := []string{os.Getenv("AFS_UPDATE_REPO"), os.Getenv("AFS_UPDATE_REPO_SSH")}
	if repos[0] == "" {
		repos[0] = buildinfo.GitRepoURL
	}
	if repos[1] == "" {
		repos[1] = buildinfo.GitRepoSSHURL
	}
	var cloneErr error
	for _, repo := range repos {
		if repo == "" {
			continue
		}
		cmd := exec.Command("git", "clone", "--quiet", "--depth", "1", "--branch", ref, repo, tmp)
		if out, err := cmd.CombinedOutput(); err != nil {
			cloneErr = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
			continue
		}
		cloneErr = nil
		break
	}
	if cloneErr != nil {
		return fmt.Errorf("source fallback clone failed: %w", cloneErr)
	}
	cmd := exec.Command("go", "install", "./cmd/afs")
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(), "GOBIN="+installDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runContract(args []string) {
	if len(args) == 0 || args[0] == "current" {
		contract, err := core.BundledContract()
		if err != nil {
			fail(err)
		}
		fmt.Print(contract)
		return
	}
	switch args[0] {
	case "status":
		pos := splitArgs(args[1:], nil)
		root := instanceRoot(pos, 0)
		printContractStatus(root)
	case "diff":
		pos := splitArgs(args[1:], nil)
		root := instanceRoot(pos, 0)
		printContractDiff(root)
	case "upgrade":
		var yes, force bool
		pos := splitArgs(args[1:], map[string]*bool{"--yes": &yes, "-y": &yes, "--force": &force})
		root := instanceRoot(pos, 0)
		current := core.ContractVersion(root)
		bundled := core.CurrentContractVersion()
		if current == bundled && !force {
			fmt.Printf("AGENTS.md contract is already current (%s)\n", current)
			return
		}
		// Refuse to overwrite a newer contract with this older binary's
		// bundled one — that silently downgrades the instance.
		if core.CompareContractVersions(current, bundled) > 0 && !force {
			fail(fmt.Errorf("this afs bundles contract %s but the instance is on %s — upgrading would downgrade it; run `afs update` first (or pass --force to override)", bundled, current))
		}
		// Refuse to clobber a hand-adapted contract. The upgrading agent
		// should port its adaptations by hand — `afs contract diff` gives it
		// the full picture.
		if customized, known := core.ContractCustomized(root); known && customized && !force {
			fail(fmt.Errorf("this contract is customized — run `afs contract diff` to see your adaptations and what %s changes, port them by hand and set agentsfs_contract: %s yourself, or pass --force to overwrite", bundled, bundled))
		}
		if dirty, err := gitPathDirty(root, "AGENTS.md"); err == nil && dirty && !force {
			fail(fmt.Errorf("AGENTS.md has uncommitted changes; review them first or pass --force"))
		}
		if !yes && !confirm(fmt.Sprintf("Replace %s with bundled contract %s?", filepath.Join(root, "AGENTS.md"), bundled)) {
			fmt.Println("Contract upgrade cancelled.")
			return
		}
		rep, err := core.UpgradeContract(root)
		if err != nil {
			fail(err)
		}
		fmt.Printf("Updated AGENTS.md to contract %s.", bundled)
		for _, rel := range rep.Marked {
			fmt.Printf(" Marked %s with its agentsfs_role.", rel)
		}
		for _, rel := range rep.Created {
			fmt.Printf(" Created %s.", rel)
		}
		for _, rel := range rep.Updated {
			fmt.Printf(" Updated stock companion %s.", rel)
		}
		for _, msg := range rep.Collided {
			fmt.Printf(" Warning: %s.", msg)
		}
		fmt.Println(" Review the git diff and commit.")
	default:
		fail(fmt.Errorf("usage: afs contract [current|status|diff|upgrade] [path] [--yes] [--force]"))
	}
}

// printContractDiff renders the two labeled diffs an agent needs to port a
// customized contract by hand.
func printContractDiff(root string) {
	d, err := core.ComputeContractDiff(root)
	if err != nil {
		fail(err)
	}
	declared := d.Declared
	if declared == "" {
		declared = "(unset)"
	}
	if d.HaveStock {
		fmt.Printf("=== Your adaptations (stock %s → your AGENTS.md) ===\n", declared)
		if strings.TrimSpace(d.Adaptations) == "" {
			fmt.Println("(none — your AGENTS.md matches the stock text of its declared version)")
		} else {
			fmt.Print(d.Adaptations)
		}
		fmt.Printf("\n=== What %s changes (stock %s → stock %s) ===\n", d.Bundled, declared, d.Bundled)
		fmt.Print(d.Changes)
		fmt.Printf("\nSee the full new contract text with `afs contract current`.\n")
	} else {
		fmt.Printf("No vendored stock text for the declared contract version %s — showing your AGENTS.md against the bundled %s only.\n\n", declared, d.Bundled)
		fmt.Printf("=== Your AGENTS.md → stock %s ===\n", d.Bundled)
		fmt.Print(d.Changes)
		fmt.Printf("\nSee the full new contract text with `afs contract current`.\n")
	}
}

func printContractStatus(root string) {
	current := core.ContractVersion(root)
	bundled := core.CurrentContractVersion()
	if current == "" {
		fmt.Printf("%s: contract version missing; bundled contract is %s\n", root, bundled)
		return
	}
	// A customized contract (AGENTS.md differs from its version's stock text)
	// can't be upgraded by a straight overwrite — flag it so the reader knows
	// to port adaptations by hand.
	custom := ""
	if customized, known := core.ContractCustomized(root); known && customized {
		custom = " (customized)"
	}
	switch core.CompareContractVersions(current, bundled) {
	case 0:
		fmt.Printf("%s: contract is current (%s)%s\n", root, current, custom)
	case -1:
		if custom != "" {
			fmt.Printf("%s: contract is %s (customized); bundled is %s — see `afs contract diff`, port adaptations by hand, then set agentsfs_contract: %s (or `afs contract upgrade %s --force` to overwrite).\n",
				root, current, bundled, bundled, root)
		} else {
			fmt.Printf("%s: contract is %s; bundled contract is %s. Run `afs contract upgrade %s`.\n",
				root, current, bundled, root)
		}
	default: // instance is ahead of this binary
		fmt.Printf("%s: contract is %s; this afs bundles the older %s. Run `afs update` — do not upgrade the contract from here.\n",
			root, current, bundled)
	}
}

func gitPathDirty(root, path string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain", "--", path)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// splitArgs separates flags from positionals so flags work in any position.
func splitArgs(args []string, known map[string]*bool) []string {
	var pos []string
	for _, a := range args {
		if b, ok := known[a]; ok {
			*b = true
		} else if strings.HasPrefix(a, "-") {
			fail(fmt.Errorf("unknown flag %q", a))
		} else {
			pos = append(pos, a)
		}
	}
	return pos
}

func instanceRoot(pos []string, at int) string {
	start := "."
	if len(pos) > at {
		start = pos[at]
	}
	root, err := core.FindRoot(start)
	if err != nil {
		fail(err)
	}
	noteStaleContract(root)
	return root
}

// contractNoticeOnce keeps the stale-contract nudge to one line per process,
// however many instance-scoped calls resolve the root.
var contractNoticeOnce sync.Once

// noteStaleContract prints a one-line stderr nudge when the resolved
// instance's contract has fallen behind the version bundled in this binary.
// It never mutates anything — the fix is the explicit `afs contract upgrade`,
// which lands as a reviewable diff. doctor and the contract subcommands
// report staleness themselves, so they suppress the duplicate here.
func noteStaleContract(root string) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "contract", "doctor":
			return
		}
	}
	contractNoticeOnce.Do(func() {
		got := core.ContractVersion(root)
		cur := core.CurrentContractVersion()
		if got == "" {
			return
		}
		switch core.CompareContractVersions(got, cur) {
		case -1:
			fmt.Fprintf(os.Stderr,
				"afs: this instance's contract (%s) is behind the bundled %s — run `afs contract upgrade` to update AGENTS.md.\n",
				got, cur)
		case 1:
			// The instance is newer than this binary — upgrading here would
			// downgrade it. Point at the binary, not the contract.
			fmt.Fprintf(os.Stderr,
				"afs: this instance's contract (%s) is newer than this afs's bundled %s — run `afs update`; do not run `afs contract upgrade`.\n",
				got, cur)
		}
	})
}

func runConnect(args []string) {
	var global, yes bool
	pos := splitArgs(args, map[string]*bool{"--global": &global, "--yes": &yes, "-y": &yes})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: afs connect <instance-path> [--global] [--yes]"))
	}
	root, err := core.FindRoot(pos[0])
	if err != nil {
		fail(fmt.Errorf("%s is not (inside) an agentsfs instance: %w", pos[0], err))
	}
	cwd, err := os.Getwd()
	if err != nil {
		fail(err)
	}

	if global {
		connectGlobal(root, yes)
		return
	}
	connectProjectAt(cwd, root, yes)
}

func connectGlobal(root string, yes bool) {
	targets := core.GlobalTargets()
	if len(targets) == 0 {
		fail(fmt.Errorf("no global harness configs found (looked for ~/.claude/CLAUDE.md and ~/.codex/AGENTS.md)"))
	}
	for _, t := range targets {
		if yes || confirm(fmt.Sprintf("Connect %s in %s — %s?", root, t.Label, t.Path)) {
			if err := core.Connect(t.Path, root); err != nil {
				fail(err)
			}
			fmt.Printf("  connected %s in %s\n", root, t.Path)
		}
	}
}

// connectProjectAt points the project containing cwd at the instance at
// root: it writes the nearest enclosing AGENTS.md/CLAUDE.md, or offers to
// create ./AGENTS.md when the project has no agent config yet.
func connectProjectAt(cwd, root string, yes bool) {
	var targets []core.Target
	skippedInside := 0
	for _, t := range core.ProjectTargets(cwd) {
		// An instance's own root is already its connection point.
		if strings.HasPrefix(t.Path, root+string(os.PathSeparator)) {
			skippedInside++
			continue
		}
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		if skippedInside > 0 {
			fmt.Printf("you are inside %s itself — its root AGENTS.md already connects it; run this from the project that should point here, or use --global\n", root)
			return
		}
		p := joinPath(cwd, "AGENTS.md")
		if _, err := os.Stat(p); err == nil {
			fail(fmt.Errorf("%s exists but was not detected as a target — refusing to overwrite", p))
		}
		if yes || confirm(fmt.Sprintf("No AGENTS.md/CLAUDE.md found at or above %s — create %s with the connection block?", cwd, p)) {
			if err := os.WriteFile(p, []byte(core.ConnectionBlock(root)+"\n"), 0o644); err != nil {
				fail(err)
			}
			fmt.Printf("  created %s, connected %s\n", p, root)
		}
		return
	}
	for _, t := range targets {
		if yes || confirm(fmt.Sprintf("Connect %s in %s — %s?", root, t.Label, t.Path)) {
			if err := core.Connect(t.Path, root); err != nil {
				fail(err)
			}
			fmt.Printf("  connected %s in %s\n", root, t.Path)
		}
	}
}

func joinPath(dir, name string) string {
	return strings.TrimRight(dir, string(os.PathSeparator)) + string(os.PathSeparator) + name
}

func runTree(args []string) {
	depth := 0
	var pos []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-d", "--depth":
			if i+1 >= len(args) {
				fail(fmt.Errorf("%s needs a number", args[i]))
			}
			i++
			if _, err := fmt.Sscanf(args[i], "%d", &depth); err != nil {
				fail(fmt.Errorf("bad depth %q", args[i]))
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				fail(fmt.Errorf("unknown flag %q", args[i]))
			}
			pos = append(pos, args[i])
		}
	}
	// No path → the whole instance discovered from cwd (unchanged default).
	// A path both locates the instance and scopes the view to that subtree.
	root, subdir := "", "."
	var err error
	if len(pos) == 0 {
		root, err = core.FindRoot(".")
	} else {
		root, subdir, err = core.ResolveScope(pos[0])
	}
	if err != nil {
		fail(err)
	}
	noteStaleContract(root)
	out, err := core.Tree(root, subdir, depth)
	if err != nil {
		fail(err)
	}
	fmt.Print(out)
}

func runDoctor(args []string) {
	var asJSON bool
	pos := splitArgs(args, map[string]*bool{"--json": &asJSON})
	findings, err := core.Doctor(instanceRoot(pos, 0))
	if err != nil {
		fail(err)
	}
	errors := 0
	for _, f := range findings {
		if f.Severity == "error" {
			errors++
		}
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if findings == nil {
			findings = []core.Finding{}
		}
		if err := enc.Encode(findings); err != nil {
			fail(err)
		}
	} else if len(findings) == 0 {
		fmt.Println("doctor: healthy — no findings")
	} else {
		for _, f := range findings {
			fmt.Printf("%-5s %-20s %s — %s\n", f.Severity, f.Code, f.Path, f.Message)
		}
		fmt.Printf("\n%d finding(s), %d error(s)\n", len(findings), errors)
	}
	if errors > 0 {
		os.Exit(1)
	}
}

// runRoles reports where this instance's reserved roles actually live. The
// contract lets any directory claim a role through its INDEX.md marker, and the
// default names have changed before (0.4.0 renamed journal/ to agent-journal/),
// so tooling that hardcodes a name is betting on a detail the contract owns.
// This is the supported way to ask instead of assume.
func runRoles(args []string) {
	var asJSON bool
	pos := splitArgs(args, map[string]*bool{"--json": &asJSON})
	roles, err := core.ResolveReservedDirs(instanceRoot(pos, 0))
	if err != nil {
		fail(err)
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(roles); err != nil {
			fail(err)
		}
		return
	}
	show := func(label, dir, source string) {
		if dir == "" {
			fmt.Printf("%-12s (none)\n", label)
			return
		}
		fmt.Printf("%-12s %s (by %s)\n", label, dir, source)
	}
	show("journal", roles.Journal, roles.JournalSource)
	show("scratch", roles.Scratch, roles.ScratchSource)
	if len(roles.Collections) == 0 {
		fmt.Printf("%-12s (none)\n", "collections")
	} else {
		fmt.Printf("%-12s %s\n", "collections", strings.Join(roles.Collections, ", "))
	}
	for _, d := range roles.DuplicateJournal {
		fmt.Fprintf(os.Stderr, "warning: %s also declares agentsfs_role: journal\n", d)
	}
	for _, d := range roles.DuplicateScratch {
		fmt.Fprintf(os.Stderr, "warning: %s also declares agentsfs_role: scratch\n", d)
	}
}

func runStatus(args []string) {
	var asJSON, withDoctor, fetch bool
	roots := splitArgs(args, map[string]*bool{
		"--json":   &asJSON,
		"--doctor": &withDoctor,
		"--fetch":  &fetch,
	})
	if len(roots) == 0 {
		roots = []string{"."}
	}
	report := core.StatusInstances(roots, core.StatusOptions{
		Doctor: withDoctor,
		Fetch:  fetch,
	})
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fail(err)
		}
		return
	}
	printStatusReport(report, withDoctor)
}

func printStatusReport(report core.StatusReport, withDoctor bool) {
	printStatusScopes(report.Scopes)
	if len(report.Instances) == 0 {
		fmt.Printf("No AgentsFS instances found beneath %s.\n", strings.Join(report.SearchRoots, ", "))
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "PATH\tCONTRACT\tMODE\tWORKTREE\tSYNC\tHEALTH\tDUPLICATE")
		behind, customized, dirty, duplicates := 0, 0, 0, 0
		for _, st := range report.Instances {
			if st.ContractState == "behind" {
				behind++
			}
			if st.Customized {
				customized++
			}
			if st.Git.Dirty {
				dirty++
			}
			if st.DuplicateOf != "" {
				duplicates++
			}
			duplicate := "-"
			if st.DuplicateOf != "" {
				duplicate = "same as " + st.DuplicateOf
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				st.Path,
				statusContractLabel(st),
				st.Mode,
				statusWorktreeLabel(st.Git),
				core.StatusSyncLabel(st.Git),
				statusDoctorLabel(st.Doctor, withDoctor),
				duplicate,
			)
		}
		_ = w.Flush()
		fmt.Printf("\n%d instance(s); %d behind; %d customized; %d dirty; %d duplicate checkout(s).\n",
			len(report.Instances), behind, customized, dirty, duplicates)
	}
	for _, issue := range report.Issues {
		fmt.Fprintf(os.Stderr, "afs status: could not inspect %s: %s\n", issue.Path, issue.Message)
	}
	for _, st := range report.Instances {
		if st.Git.FetchError != "" {
			fmt.Fprintf(os.Stderr, "afs status: %s: %s\n", st.Path, st.Git.FetchError)
		}
		if st.Git.InspectError != "" {
			fmt.Fprintf(os.Stderr, "afs status: %s: %s\n", st.Path, st.Git.InspectError)
		}
	}
}

func printStatusScopes(scopes []core.StatusScope) {
	fmt.Println("Status scope: AgentsFS instances discoverable within:")
	if len(scopes) == 0 {
		fmt.Println("  (no valid search roots)")
	}
	for _, scope := range scopes {
		fmt.Printf("  %s\n", scope.SearchRoot)
	}
	fmt.Println("Pass a different directory, or several directories, to broaden or narrow this view.")
	for _, scope := range scopes {
		if scope.Complete {
			fmt.Printf("Scan complete for %s: %d entries visited, %d directories seen, %d pruned.\n",
				scope.SearchRoot, scope.EntriesVisited, scope.DirectoriesSeen, scope.DirectoriesPruned)
			continue
		}
		advice := "Review the reported path errors or pass a narrower accessible root."
		if strings.Contains(scope.IncompleteReason, "limit") {
			advice = "Pass one or more narrower search roots and rerun status."
		}
		fmt.Printf("WARNING: scan incomplete for %s after %d entries (%s); results are partial. %s\n",
			scope.SearchRoot, scope.EntriesVisited, scope.IncompleteReason, advice)
	}
	fmt.Println()
}

func statusContractLabel(st core.InstanceStatus) string {
	label := st.ContractState
	if st.ContractVersion != "" {
		label = st.ContractVersion + " " + label
	}
	if st.Customized {
		label += " custom"
	} else if st.ContractVersion != "" && !st.CustomizationKnown {
		label += " custom?"
	}
	return label
}

func statusWorktreeLabel(st core.GitStatus) string {
	if !st.Repository {
		return "not git"
	}
	if st.InspectError != "" {
		return "unknown"
	}
	if st.Dirty {
		return "dirty"
	}
	return "clean"
}

func statusDoctorLabel(summary *core.DoctorSummary, requested bool) string {
	if !requested {
		return "not checked"
	}
	if summary == nil || summary.Error != "" {
		return "unknown"
	}
	if summary.Errors > 0 {
		return fmt.Sprintf("%d errors", summary.Errors)
	}
	if summary.Warnings > 0 {
		return fmt.Sprintf("%d warnings", summary.Warnings)
	}
	if summary.Info > 0 {
		return fmt.Sprintf("%d info", summary.Info)
	}
	return "healthy"
}

func runBacklinks(args []string) {
	pos := splitArgs(args, nil)
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: afs backlinks <name> [path]"))
	}
	links, err := core.Backlinks(instanceRoot(pos, 1), pos[0])
	if err != nil {
		fail(err)
	}
	if len(links) == 0 {
		fmt.Printf("no links to %q found\n", pos[0])
		return
	}
	for _, l := range links {
		fmt.Printf("%s:%d  [[%s]]\n", l.Source, l.Line, l.Target)
	}
}

func runRename(args []string) {
	pos := splitArgs(args, nil)
	if len(pos) < 2 {
		fail(fmt.Errorf("usage: afs rename <old> <new> [path]"))
	}
	res, err := core.Rename(instanceRoot(pos, 2), pos[0], pos[1])
	if err != nil {
		fail(err)
	}
	fmt.Printf("renamed %s → %s; rewrote %d link(s) in %d file(s)\n",
		res.OldRel, res.NewRel, res.LinksRewrote, len(res.FilesChanged))
	fmt.Println("changes are uncommitted — review and commit")
}

func runInit(args []string) {
	// Hand-rolled so flags work in any position (stdlib flag stops at the
	// first positional argument, and agents type `afs init dir --yes`).
	var yes bool
	var shared bool
	dir := ""
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--shared":
			shared = true
		case "--vault":
			fail(fmt.Errorf("`--vault` was removed; use `afs setup [dir]` to create or reuse a personal agentsfs and connect this project"))
		case "--no-register", "--register-global":
			fail(fmt.Errorf("`afs init` only creates files now; use `afs setup` for first-run setup or `afs connect` for project/global connections"))
		default:
			if strings.HasPrefix(a, "-") {
				fail(fmt.Errorf("unknown flag %q for init", a))
			}
			if dir != "" {
				fail(fmt.Errorf("usage: afs init [dir] [--shared] [--yes]"))
			}
			dir = a
		}
	}
	_ = yes // accepted because agents commonly pass it; init itself has no prompts.

	target := dir
	if target == "" {
		target = "."
	}
	repoRoot, insideRepo := core.EnclosingRepoRoot(target)

	if shared && !insideRepo {
		fail(fmt.Errorf("--shared only makes sense inside a git repo; drop --shared for a standalone agentsfs"))
	}

	if insideRepo && !shared {
		fail(fmt.Errorf("you're inside the git repo at %s. Choose where this agentsfs should live:\n"+
			"  personal, outside this repo: afs setup ~/agentsfs\n"+
			"  shared with this codebase: afs init ./agentsfs --shared\n"+
			"refusing to create an instance inside a repo unless --shared is explicit", repoRoot))
	}

	if !insideRepo {
		res := mustInit(target, core.ModeStandalone)
		narrateInit(res)
		fmt.Printf("Next: connect a project with `afs connect %s`, or use `afs setup` for the one-command flow.\n", res.Dir)
		return
	}

	// Shared memory lives in a subdirectory — never at a code repo's root,
	// where it would mix with source files.
	if target == "." || sameDir(target, repoRoot) {
		target = filepath.Join(strings.TrimRight(target, "/"), "agentsfs")
		if target == "agentsfs" {
			target = "./agentsfs"
		}
		fmt.Printf("Placing memory in a subdirectory (%s) to keep it separate from your code.\n", target)
	}

	res := mustInit(target, core.ModeShared)
	narrateInit(res)
}

func runSetup(args []string) {
	var yes, global bool
	dir := ""
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--global":
			global = true
		default:
			if strings.HasPrefix(a, "-") {
				fail(fmt.Errorf("unknown flag %q for setup", a))
			}
			if dir != "" {
				fail(fmt.Errorf("usage: afs setup [dir] [--yes] [--global]"))
			}
			dir = a
		}
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fail(err)
		}
		dir = filepath.Join(home, "agentsfs")
	}

	root := dir
	if existing, err := core.FindRoot(dir); err == nil {
		fmt.Printf("Using existing agentsfs at %s\n", existing)
		root = existing
	} else {
		if repoRoot, insideRepo := core.EnclosingRepoRoot(dir); insideRepo {
			fail(fmt.Errorf("`afs setup` creates a personal agentsfs outside code repos, but %s is inside %s.\n"+
				"Choose an outside path, e.g. `afs setup ~/agentsfs`, or make team-shared memory explicit with `afs init ./agentsfs --shared`", dir, repoRoot))
		}
		res := mustInit(dir, core.ModeStandalone)
		narrateInit(res)
		root = res.Dir
	}
	cwd, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	connectProjectAt(cwd, root, yes)
	if global {
		connectGlobal(root, true)
	}
}

func mustInit(dir string, mode core.InitMode) *core.InitResult {
	res, err := core.Init(dir, mode)
	if err != nil {
		fail(err)
	}
	return res
}

func narrateInit(res *core.InitResult) {
	fmt.Printf("Initialized agentsfs at %s\n", res.Dir)
	if res.Mode == core.ModeShared {
		fmt.Println("  mode: shared — committed into the enclosing repo; this memory ships with the code")
	}
	if !res.LFSAvailable {
		fmt.Println("  note: git-lfs not installed — large media won't be LFS-tracked (install git-lfs and re-add .gitattributes later if needed)")
	} else if !res.LFSConfigured {
		fmt.Println("  note: LFS setup left to the host repo (hooks and .gitattributes are its call)")
	}
	if res.CommitSkipped != "" {
		fmt.Printf("  note: initial commit skipped for safety — %s. AgentsFS files remain staged for review.\n", res.CommitSkipped)
	} else if !res.Committed {
		fmt.Println("  note: initial commit failed (git identity not configured?) — files are staged, commit manually")
	}
	for _, msg := range res.Collisions {
		fmt.Printf("  warning: %s\n", msg)
	}
}

func sameDir(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && aa == bb
}

func runSearch(args []string) {
	var semantic bool
	limit := 10
	var pos []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--semantic":
			semantic = true
		case "-n", "--limit":
			if i+1 >= len(args) {
				fail(fmt.Errorf("%s needs a number", args[i]))
			}
			i++
			if _, err := fmt.Sscanf(args[i], "%d", &limit); err != nil {
				fail(fmt.Errorf("bad limit %q", args[i]))
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				fail(fmt.Errorf("unknown flag %q", args[i]))
			}
			pos = append(pos, args[i])
		}
	}
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: afs search <query> [path] [--semantic] [-n N]"))
	}
	root := instanceRoot(pos, 1)

	if semantic {
		results, warning, err := core.SemanticSearch(root, pos[0], limit)
		if err != nil {
			fail(err)
		}
		if warning != "" {
			fmt.Fprintln(os.Stderr, "warning:", warning)
		}
		if len(results) == 0 {
			fmt.Println("no matches (try fewer or different words)")
			return
		}
		printSearchResults(results, true)
		return
	}
	results, err := core.Search(root, pos[0], limit)
	if err != nil {
		fail(err)
	}
	if len(results) == 0 {
		fmt.Println("no matches (try fewer or different words, or --semantic)")
		return
	}
	printSearchResults(results, false)
}

func printSearchResults(results []core.SearchResult, semantic bool) {
	for i, r := range results {
		if i > 0 {
			fmt.Println()
		}
		if semantic {
			fmt.Printf("%d. %s  score %.3f\n", i+1, r.Path, r.Score)
		} else {
			fmt.Printf("%d. %s\n", i+1, r.Path)
		}
		if r.Heading != "" {
			fmt.Printf("   section: %s\n", r.Heading)
		}
		if snippet := cleanSearchSnippet(r.Snippet); snippet != "" {
			for _, line := range wrapSearchSnippet(snippet, 88) {
				fmt.Printf("   %s\n", line)
			}
		}
	}
}

func cleanSearchSnippet(snippet string) string {
	return strings.Join(strings.Fields(snippet), " ")
}

func wrapSearchSnippet(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	var current strings.Builder
	currentLen := 0
	for _, word := range words {
		wordLen := len([]rune(word))
		if currentLen > 0 && currentLen+1+wordLen > width {
			lines = append(lines, current.String())
			current.Reset()
			currentLen = 0
		}
		if currentLen > 0 {
			current.WriteString(" ")
			currentLen++
		}
		current.WriteString(word)
		currentLen += wordLen
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

func runEmbeddings(args []string) {
	if len(args) == 0 {
		runEmbeddingsStatus(nil)
		return
	}
	switch args[0] {
	case "status":
		runEmbeddingsStatus(args[1:])
	case "setup":
		runEmbeddingsSetup(args[1:])
	case "clear":
		runEmbeddingsClear(args[1:])
	default:
		fail(fmt.Errorf("usage: afs embeddings <status|setup|clear>"))
	}
}

func runEmbeddingsStatus(args []string) {
	if len(args) != 0 {
		fail(fmt.Errorf("usage: afs embeddings status"))
	}
	path, pathErr := core.EmbeddingConfigPath()
	provider, err := core.DetectEmbeddingProvider()
	if err != nil {
		fmt.Println("embedding provider: not configured")
		fmt.Printf("reason: %v\n", err)
		if pathErr == nil {
			fmt.Printf("config file: %s\n", path)
		}
		fmt.Println("next: afs embeddings setup openai")
		return
	}
	fmt.Printf("embedding provider: %s\n", provider.Name)
	fmt.Printf("model: %s\n", provider.Model)
	fmt.Printf("endpoint: %s\n", provider.URL)
	fmt.Printf("key: %s from %s\n", provider.KeyName, provider.KeySource)
	if pathErr == nil {
		fmt.Printf("config file: %s\n", path)
	}
}

func runEmbeddingsSetup(args []string) {
	var yes bool
	providerName := ""
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		default:
			if strings.HasPrefix(a, "-") {
				fail(fmt.Errorf("unknown flag %q for embeddings setup", a))
			}
			if providerName != "" {
				fail(fmt.Errorf("usage: afs embeddings setup <openai|voyage> [--yes]"))
			}
			providerName = a
		}
	}
	if providerName == "" {
		fail(fmt.Errorf("usage: afs embeddings setup <openai|voyage> [--yes]"))
	}
	providerName, err := core.NormalizeEmbeddingProvider(providerName)
	if err != nil {
		fail(err)
	}
	keyName, err := core.EmbeddingKeyName(providerName)
	if err != nil {
		fail(err)
	}
	path, err := core.EmbeddingConfigPath()
	if err != nil {
		fail(err)
	}
	if _, err := os.Stat(path); err == nil && !yes {
		if !confirm(fmt.Sprintf("Replace existing embedding config at %s?", path)) {
			fmt.Println("Embedding setup cancelled.")
			return
		}
	} else if err != nil && !os.IsNotExist(err) {
		fail(err)
	}

	key := os.Getenv(keyName)
	if key != "" {
		fmt.Printf("Using %s from the current environment.\n", keyName)
	} else {
		key, err = readSecretLine(fmt.Sprintf("Paste %s API key: ", embeddingProviderTitle(providerName)))
		if err != nil {
			fail(err)
		}
	}
	path, err = core.SaveEmbeddingConfig(providerName, key)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Saved %s embedding config to %s\n", providerName, path)
	fmt.Printf("  key: %s\n", keyName)
	fmt.Println("Next: run `afs reindex --embeddings` from an agentsfs, then use `afs search \"...\" --semantic`.")
}

func runEmbeddingsClear(args []string) {
	var yes bool
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		default:
			fail(fmt.Errorf("usage: afs embeddings clear [--yes]"))
		}
	}
	path, err := core.EmbeddingConfigPath()
	if err != nil {
		fail(err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("No embedding config found at %s\n", path)
		return
	} else if err != nil {
		fail(err)
	}
	if !yes && !confirm(fmt.Sprintf("Remove embedding config at %s?", path)) {
		fmt.Println("Embedding config left unchanged.")
		return
	}
	path, err = core.ClearEmbeddingConfig()
	if err != nil {
		fail(err)
	}
	fmt.Printf("Removed embedding config at %s\n", path)
	fmt.Println("Note: OPENAI_API_KEY, VOYAGE_API_KEY, and AFS_EMBED_* in the shell still override this file.")
}

func embeddingProviderTitle(provider string) string {
	switch provider {
	case "openai":
		return "OpenAI"
	case "voyage":
		return "Voyage"
	default:
		return provider
	}
}

func readSecretLine(prompt string) (string, error) {
	fmt.Print(prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		data, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func runReindex(args []string) {
	var embeddings bool
	pos := splitArgs(args, map[string]*bool{"--embeddings": &embeddings})
	root := instanceRoot(pos, 0)
	n, err := core.ReindexFTS(root)
	if err != nil {
		fail(err)
	}
	fmt.Printf("full-text index rebuilt: %d chunks\n", n)
	if embeddings {
		n, err := core.ReindexEmbeddings(root)
		if err != nil {
			fail(err)
		}
		fmt.Printf("embedding index rebuilt: %d chunks\n", n)
	}
}

func runMCP(args []string) {
	pos := splitArgs(args, nil)
	start := "."
	if len(pos) > 0 {
		start = pos[0]
	}
	server := mcpserver.New(buildinfo.Version, start)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fail(err)
	}
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "afs:", err)
	os.Exit(1)
}
