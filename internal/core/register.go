package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A Target is a harness config file where the instance can be connected so
// agents bootstrapping from that file learn the substrate exists. Global
// targets affect every session the user runs anywhere — callers must hold
// them to a higher consent bar than project-local files.
type Target struct {
	Path   string
	Label  string
	Global bool
}

// DetectTargets returns known global configs plus the nearest project
// enclosing the instance. Callers can use it when they want to suggest likely
// connection targets.
func DetectTargets(instanceDir string) []Target {
	return append(GlobalTargets(), ProjectTargets(filepath.Dir(instanceDir))...)
}

// GlobalTargets returns the harness config files that affect every session
// the user runs anywhere.
func GlobalTargets() []Target {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []Target
	for _, c := range []Target{
		{filepath.Join(home, ".claude", "CLAUDE.md"), "Claude Code (global)", true},
		{filepath.Join(home, ".codex", "AGENTS.md"), "Codex (global)", true},
	} {
		if dirExists(filepath.Dir(c.Path)) {
			out = append(out, c)
		}
	}
	return out
}

// ProjectTargets returns the agent config files of the nearest project at
// or above start: the closest directory level holding an AGENTS.md or
// CLAUDE.md (both, when both exist). The walk stops at the home directory —
// a file there is global config, not a project.
func ProjectTargets(start string) []Target {
	abs, err := filepath.Abs(start)
	if err != nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	for dir := abs; ; dir = filepath.Dir(dir) {
		if dir == home || dir == filepath.Dir(dir) {
			return nil
		}
		var found []Target
		for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
			if p := filepath.Join(dir, name); fileExists(p) {
				found = append(found, Target{p, "project (" + dir + ")", false})
			}
		}
		if len(found) > 0 {
			return found
		}
	}
}

// ConnectionBlock is the canonical text appended to a harness file. Kept
// in sync with prompts/connection-snippet.md. The markers carry the instance
// path so multiple instances can coexist in one file and re-runs update in
// place instead of duplicating.
func ConnectionBlock(instancePath string) string {
	return fmt.Sprintf(`<!-- agentsfs:begin %[1]s -->
## Persistent memory (agentsfs)

A durable, user-owned memory lives at `+"`%[1]s`"+`.
Before starting work, read `+"`%[1]s/AGENTS.md`"+` and orient yourself.
Consult it before re-researching anything you may already know, and record
durable knowledge there as you work, following its contract.
<!-- agentsfs:end %[1]s -->`, instancePath)
}

// RegistrationBlock is kept for older callers; use ConnectionBlock.
func RegistrationBlock(instancePath string) string {
	return ConnectionBlock(instancePath)
}

// Connect inserts or updates the connection block for instancePath in
// targetFile. Idempotent: an existing block for the same instance is
// replaced, anything else in the file is untouched.
func Connect(targetFile, instancePath string) error {
	raw, err := os.ReadFile(targetFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(raw)
	begin := "<!-- agentsfs:begin " + instancePath + " -->"
	end := "<!-- agentsfs:end " + instancePath + " -->"
	block := ConnectionBlock(instancePath)

	if i := strings.Index(content, begin); i >= 0 {
		j := strings.Index(content, end)
		if j < i {
			return fmt.Errorf("%s: malformed agentsfs markers", targetFile)
		}
		content = content[:i] + block + content[j+len(end):]
	} else {
		content = strings.TrimRight(content, "\n") + "\n\n" + block + "\n"
	}
	return os.WriteFile(targetFile, []byte(content), 0o644)
}

// Register is kept for older callers; use Connect.
func Register(targetFile, instancePath string) error {
	return Connect(targetFile, instancePath)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
