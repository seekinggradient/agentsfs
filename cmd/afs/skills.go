package main

import (
	"fmt"
	"path/filepath"

	"agentsfs.ai/afs/internal/skills"
)

// runSkills lists the agent skills bundled in this binary and refreshes their
// on-disk copies under the afs config dir. It is deliberately list-only and
// harness-neutral: afs materializes the skills and tells you where to copy
// them, but never writes into ~/.claude/ or any other harness directory.
func runSkills(args []string) {
	// Zero args and the single arg `list` are the same thing; anything else is
	// a usage error (there is no `install` subcommand by design).
	switch len(args) {
	case 0:
	case 1:
		if args[0] != "list" {
			fail(fmt.Errorf("usage: afs skills [list]"))
		}
	default:
		fail(fmt.Errorf("usage: afs skills [list]"))
	}

	baseDir, err := skills.Dir()
	if err != nil {
		fail(err)
	}
	if err := skills.Materialize(baseDir); err != nil {
		fail(err)
	}
	list, err := skills.List()
	if err != nil {
		fail(err)
	}

	fmt.Printf("Bundled agent skills, materialized under %s:\n\n", baseDir)
	for _, s := range list {
		fmt.Printf("  %s — %s\n", s.Name, s.Description)
		fmt.Printf("    %s\n", filepath.Join(baseDir, s.Dir))
	}

	example := exampleSkillDir(list)
	fmt.Printf(`
These directories are refreshed from the afs binary each time this command
runs (re-run after `+"`afs update`"+`). afs never writes into a harness's own
skills directory. To enable a skill, copy its directory into the canonical
place your system keeps agent skills — for example ~/.claude/skills/ (or a
project's .claude/skills/) for Claude Code, or the equivalent location for your
harness:

    cp -R %q ~/.claude/skills/
`, filepath.Join(baseDir, example))
}

// exampleSkillDir picks a real skill to show in the copy example — the gardener
// when present (the most common thing a scheduled harness enables), otherwise
// the first listed skill, so the printed command always references something
// that exists on disk.
func exampleSkillDir(list []skills.Skill) string {
	if len(list) == 0 {
		return "<skill>"
	}
	example := list[0].Dir
	for _, s := range list {
		if s.Dir == "agentsfs-garden" {
			return s.Dir
		}
	}
	return example
}
