// afs is the agentsfs CLI: a thin shell over internal/core, which the MCP
// server will also wrap. No capability lives here — only argument parsing,
// prompting, and narration.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"agentsfs.ai/afs/internal/core"
	"agentsfs.ai/afs/internal/mcpserver"
)

const version = "0.1.0"

const usage = `afs — a portable, user-owned memory for AI agents

Usage:
  afs init [dir] [--yes] [--no-register] [--register-global]
      create an instance (default: current directory). Outside any git repo
      this just makes a standalone instance. INSIDE a git repo it asks where
      memory should live; pick non-interactively with one of:
        --vault   keep memory in your personal ~/agentsfs, register this
                  project to point at it (recommended; nothing enters the
                  codebase's history)
        --shared  commit memory inside this repo, shipped with the code
                  (team-shared memory)
      --yes never picks --shared for you (merging is irreversible).
      --yes auto-approves project-level registration only — global harness
      configs need an interactive yes or --register-global
  afs register <instance> [--global] [--yes]
      point the project you are standing in at an existing instance:
      appends the registration block to the nearest AGENTS.md / CLAUDE.md
      (offers to create ./AGENTS.md when neither exists); --global writes
      your global harness configs instead, so every session everywhere
      knows the instance
  afs tree [path]                          the tree with descriptions and freshness — orient here
  afs doctor [path] [--json]               deterministic health check (exit 1 on errors)
  afs backlinks <name> [path]              all [[wikilinks]] resolving to a file
  afs rename <old> <new> [path]            move a file and rewrite every link to it
  afs search <query> [path] [--semantic] [-n N]   full-text (or semantic) search over the instance
  afs reindex [path] [--embeddings]        rebuild the derived index from the files
  afs mcp [path]                           serve the same capabilities over MCP (stdio)
  afs version

File arguments to rename are relative to the instance root (cwd-relative
also accepted when the file exists there). Semantic search needs an
embedding provider: set VOYAGE_API_KEY or OPENAI_API_KEY, then run
afs reindex --embeddings once (and again after big changes). Everything
else works with no configuration.

The substrate itself is plain files + git; afs only makes reading, upkeep,
and setup cheap. See AGENTS.md in any instance for the contract.`

func main() {
	if len(os.Args) < 2 {
		fmt.Println(usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "init":
		runInit(os.Args[2:])
	case "register":
		runRegister(os.Args[2:])
	case "tree":
		runTree(os.Args[2:])
	case "doctor":
		runDoctor(os.Args[2:])
	case "backlinks":
		runBacklinks(os.Args[2:])
	case "rename":
		runRename(os.Args[2:])
	case "search":
		runSearch(os.Args[2:])
	case "reindex":
		runReindex(os.Args[2:])
	case "mcp":
		runMCP(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("afs " + version)
	case "help", "--help", "-h":
		fmt.Println(usage)
	default:
		fmt.Fprintf(os.Stderr, "afs: unknown command %q\n\n%s\n", os.Args[1], usage)
		os.Exit(2)
	}
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
	return root
}

func runRegister(args []string) {
	var global, yes bool
	pos := splitArgs(args, map[string]*bool{"--global": &global, "--yes": &yes, "-y": &yes})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: afs register <instance-path> [--global] [--yes]"))
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
		targets := core.GlobalTargets()
		if len(targets) == 0 {
			fail(fmt.Errorf("no global harness configs found (looked for ~/.claude/CLAUDE.md and ~/.codex/AGENTS.md)"))
		}
		for _, t := range targets {
			if yes || confirm(fmt.Sprintf("Register %s in %s — %s?", root, t.Label, t.Path)) {
				if err := core.Register(t.Path, root); err != nil {
					fail(err)
				}
				fmt.Printf("  registered %s in %s\n", root, t.Path)
			}
		}
		return
	}
	registerProjectAt(cwd, root, yes)
}

// registerProjectAt points the project containing cwd at the instance at
// root: it writes the nearest enclosing AGENTS.md/CLAUDE.md, or offers to
// create ./AGENTS.md when the project has no agent config yet.
func registerProjectAt(cwd, root string, yes bool) {
	var targets []core.Target
	skippedInside := 0
	for _, t := range core.ProjectTargets(cwd) {
		// An instance's own root is already its registration.
		if strings.HasPrefix(t.Path, root+string(os.PathSeparator)) {
			skippedInside++
			continue
		}
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		if skippedInside > 0 {
			fmt.Printf("you are inside %s itself — its root AGENTS.md already registers it; run this from the project that should point here, or use --global\n", root)
			return
		}
		p := joinPath(cwd, "AGENTS.md")
		if _, err := os.Stat(p); err == nil {
			fail(fmt.Errorf("%s exists but was not detected as a target — refusing to overwrite", p))
		}
		if yes || confirm(fmt.Sprintf("No AGENTS.md/CLAUDE.md found at or above %s — create %s with the registration block?", cwd, p)) {
			if err := os.WriteFile(p, []byte(core.RegistrationBlock(root)+"\n"), 0o644); err != nil {
				fail(err)
			}
			fmt.Printf("  created %s, registered %s\n", p, root)
		}
		return
	}
	for _, t := range targets {
		if yes || confirm(fmt.Sprintf("Register %s in %s — %s?", root, t.Label, t.Path)) {
			if err := core.Register(t.Path, root); err != nil {
				fail(err)
			}
			fmt.Printf("  registered %s in %s\n", root, t.Path)
		}
	}
}

func joinPath(dir, name string) string {
	return strings.TrimRight(dir, string(os.PathSeparator)) + string(os.PathSeparator) + name
}

func runTree(args []string) {
	pos := splitArgs(args, nil)
	out, err := core.Tree(instanceRoot(pos, 0))
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
	var yes, noRegister, registerGlobal bool
	var shared, vault bool
	dir := ""
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--no-register":
			noRegister = true
		case "--register-global":
			registerGlobal = true
		case "--shared":
			shared = true
		case "--vault":
			vault = true
		default:
			if strings.HasPrefix(a, "-") {
				fail(fmt.Errorf("unknown flag %q for init", a))
			}
			dir = a
		}
	}
	if shared && vault {
		fail(fmt.Errorf("choose at most one of --shared, --vault"))
	}

	target := dir
	if target == "" {
		target = "."
	}
	repoRoot, insideRepo := core.EnclosingRepoRoot(target)

	// Not inside any repo: no entanglement is possible, so there's no
	// question to ask — a standalone instance (the personal-vault shape).
	if !insideRepo && !vault {
		res := mustInit(target, core.ModeStandalone)
		narrateInit(res)
		registerAfterInit(res.Dir, yes, registerGlobal, noRegister)
		return
	}

	// Inside a repo (or --vault): the ownership decision must be explicit.
	switch {
	case vault:
		runInitVault(dir, yes, registerGlobal, noRegister)
		return
	case shared:
		// fall through to shared init below
	default:
		// No shape flag inside a repo. Never silently merge — merging is
		// irreversible (knowledge enters a possibly-shared history forever).
		if yes || !interactive() {
			fail(fmt.Errorf("you're inside the git repo at %s — choose where this memory lives:\n"+
				"  --vault   keep it in your personal ~/agentsfs and register this project (recommended)\n"+
				"  --shared  commit it inside this repo, shipped with the code (team-shared memory)\n"+
				"refusing to guess, because --shared writes knowledge into this repo's history permanently", repoRoot))
		}
		if askOwnership(repoRoot) == "vault" {
			runInitVault(dir, yes, registerGlobal, noRegister)
			return
		}
	}

	// Shared memory lives in a subdirectory — never at a code repo's root,
	// where it would mix with source files.
	if target == "." || sameDir(target, repoRoot) {
		target = filepath.Join(strings.TrimRight(target, "/"), "memory")
		if target == "memory" {
			target = "./memory"
		}
		fmt.Printf("Placing memory in a subdirectory (%s) to keep it separate from your code.\n", target)
	}

	res := mustInit(target, core.ModeShared)
	narrateInit(res)
	registerAfterInit(res.Dir, yes, registerGlobal, noRegister)
}

// runInitVault ensures a personal vault exists and registers the project the
// user is standing in to point at it — the "this memory is mine, not the
// codebase's" choice.
func runInitVault(dirArg string, yes, registerGlobal, noRegister bool) {
	vaultPath := dirArg
	if vaultPath == "" || vaultPath == "." {
		home, err := os.UserHomeDir()
		if err != nil {
			fail(err)
		}
		vaultPath = filepath.Join(home, "agentsfs")
	}
	if root, err := core.FindRoot(vaultPath); err == nil {
		fmt.Printf("Using existing vault at %s\n", root)
		vaultPath = root
	} else {
		res := mustInit(vaultPath, core.ModeStandalone)
		narrateInit(res)
		vaultPath = res.Dir
		// A new vault is worth knowing about everywhere, so offer global too.
		registerAfterInit(vaultPath, yes, registerGlobal, noRegister)
	}
	if noRegister {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	registerProjectAt(cwd, vaultPath, yes)
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
	if !res.Committed {
		fmt.Println("  note: initial commit failed (git identity not configured?) — files are staged, commit manually")
	}
}

// registerAfterInit offers to register the instance in detected harness
// configs. --yes never reaches global configs (they affect every session
// everywhere); those need --register-global or an interactive yes.
func registerAfterInit(instanceDir string, yes, registerGlobal, noRegister bool) {
	if noRegister {
		return
	}
	targets := core.DetectTargets(instanceDir)
	if len(targets) == 0 {
		fmt.Println("No harness config files found to register in (looked for global Claude Code / Codex configs and an enclosing project's AGENTS.md/CLAUDE.md).")
		return
	}
	fmt.Println("\nAgents only discover this memory if their harness config points at it.")
	for _, t := range targets {
		approved := false
		switch {
		case t.Global && registerGlobal:
			approved = true
		case t.Global && yes:
			fmt.Printf("  skipped %s (%s) — global configs need --register-global or an interactive yes\n", t.Path, t.Label)
		case yes:
			approved = true
		default:
			approved = confirm(fmt.Sprintf("Register in %s — %s?", t.Label, t.Path))
		}
		if approved {
			if err := core.Register(t.Path, instanceDir); err != nil {
				fail(err)
			}
			fmt.Printf("  registered in %s\n", t.Path)
		}
	}
}

func sameDir(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && aa == bb
}

func interactive() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

func askOwnership(repoRoot string) string {
	fmt.Printf("You're initializing inside the git repo at %s.\nWhere should this memory live?\n", repoRoot)
	fmt.Println("  [1] Vault (recommended) — your personal ~/agentsfs, private and portable; this project is registered to point at it")
	fmt.Println("  [2] Shared — committed inside this repo, ships with the code (team-shared memory; enters this repo's history)")
	fmt.Print("Choice [1]: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) == "2" {
		return "shared"
	}
	return "vault"
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
		for _, r := range results {
			fmt.Printf("%.3f  %s § %s\n      %s\n", r.Score, r.Path, r.Heading, r.Snippet)
		}
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
	for _, r := range results {
		fmt.Printf("%s § %s\n      %s\n", r.Path, r.Heading, r.Snippet)
	}
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
	server := mcpserver.New(version, start)
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
