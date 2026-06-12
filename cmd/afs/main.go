// afs is the agentsfs CLI: a thin shell over internal/core, which the MCP
// server will also wrap. No capability lives here — only argument parsing,
// prompting, and narration.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

const version = "0.1.0"

const usage = `afs — a portable, user-owned memory for AI agents

Usage:
  afs init [dir] [--yes] [--no-register]   create an instance (default: current directory)
  afs tree [path]                          the tree with descriptions and freshness — orient here
  afs doctor [path] [--json]               deterministic health check (exit 1 on errors)
  afs backlinks <name> [path]              all [[wikilinks]] resolving to a file
  afs rename <old> <new> [path]            move a file and rewrite every link to it
  afs search <query> [path] [--semantic] [-n N]   full-text (or semantic) search over the instance
  afs reindex [path] [--embeddings]        rebuild the derived index from the files
  afs version

Semantic search needs an embedding provider: set VOYAGE_API_KEY or
OPENAI_API_KEY, then run afs reindex --embeddings once (and again after
big changes). Everything else works with no configuration.

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
	var yes, noRegister bool
	dir := "."
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--no-register":
			noRegister = true
		default:
			if strings.HasPrefix(a, "-") {
				fail(fmt.Errorf("unknown flag %q for init", a))
			}
			dir = a
		}
	}

	res, err := core.Init(dir)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Initialized agentsfs at %s\n", res.Dir)
	if !res.LFSAvailable {
		fmt.Println("  note: git-lfs not installed — large media won't be LFS-tracked (install git-lfs and re-add .gitattributes later if needed)")
	}
	if !res.Committed {
		fmt.Println("  note: initial commit failed (git identity not configured?) — files are staged, commit manually")
	}

	if noRegister {
		return
	}
	targets := core.DetectTargets(res.Dir)
	if len(targets) == 0 {
		fmt.Println("No harness config files found to register in (looked for global Claude Code / Codex configs and an enclosing project's AGENTS.md/CLAUDE.md).")
		return
	}
	fmt.Println("\nAgents only discover this memory if their harness config points at it.")
	for _, t := range targets {
		if yes || confirm(fmt.Sprintf("Register in %s — %s?", t.Label, t.Path)) {
			if err := core.Register(t.Path, res.Dir); err != nil {
				fail(err)
			}
			fmt.Printf("  registered in %s\n", t.Path)
		}
	}
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
