// afs is the agentsfs CLI: a thin shell over internal/core, which the MCP
// server will also wrap. No capability lives here — only argument parsing,
// prompting, and narration.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

const version = "0.1.0"

const usage = `afs — a portable, user-owned memory for AI agents

Usage:
  afs init [dir] [--yes] [--no-register]   create an instance (default: current directory)
  afs version

The substrate itself is plain files + git; afs only makes setup and upkeep
cheap. See AGENTS.md in any instance for the contract.`

func main() {
	if len(os.Args) < 2 {
		fmt.Println(usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "init":
		runInit(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("afs " + version)
	case "help", "--help", "-h":
		fmt.Println(usage)
	default:
		fmt.Fprintf(os.Stderr, "afs: unknown command %q\n\n%s\n", os.Args[1], usage)
		os.Exit(2)
	}
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
