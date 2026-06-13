package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A Target is a harness config file where the instance can be registered so
// agents bootstrapping from that file learn the substrate exists. Global
// targets affect every session the user runs anywhere — callers must hold
// them to a higher consent bar than project-local files.
type Target struct {
	Path   string
	Label  string
	Global bool
}

// DetectTargets finds harness files worth registering in: known global
// configs, plus the nearest AGENTS.md/CLAUDE.md in a project enclosing the
// instance (an instance initialized inside a project should be discoverable
// by that project's agents).
func DetectTargets(instanceDir string) []Target {
	var targets []Target
	if home, err := os.UserHomeDir(); err == nil {
		for _, c := range []Target{
			{filepath.Join(home, ".claude", "CLAUDE.md"), "Claude Code (global)", true},
			{filepath.Join(home, ".codex", "AGENTS.md"), "Codex (global)", true},
		} {
			if fileExists(c.Path) {
				targets = append(targets, c)
			}
		}
	}
	home, _ := os.UserHomeDir()
	for dir := filepath.Dir(instanceDir); ; dir = filepath.Dir(dir) {
		if dir == home || dir == filepath.Dir(dir) {
			break
		}
		found := false
		for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
			p := filepath.Join(dir, name)
			if fileExists(p) {
				targets = append(targets, Target{p, "enclosing project", false})
				found = true
			}
		}
		if found {
			break // nearest enclosing project only
		}
	}
	return targets
}

// RegistrationBlock is the canonical text appended to a harness file. Kept
// in sync with prompts/registration-snippet.md. The markers carry the
// instance path so multiple instances can coexist in one file and re-runs
// update in place instead of duplicating.
func RegistrationBlock(instancePath string) string {
	return fmt.Sprintf(`<!-- agentsfs:begin %[1]s -->
## Persistent memory (agentsfs)

A durable, user-owned memory lives at `+"`%[1]s`"+`.
Before starting work, read `+"`%[1]s/AGENTS.md`"+` and orient yourself.
Consult it before re-researching anything you may already know, and record
durable knowledge there as you work, following its contract.
<!-- agentsfs:end %[1]s -->`, instancePath)
}

// Register inserts or updates the registration block for instancePath in
// targetFile. Idempotent: an existing block for the same instance is
// replaced, anything else in the file is untouched.
func Register(targetFile, instancePath string) error {
	raw, err := os.ReadFile(targetFile)
	if err != nil {
		return err
	}
	content := string(raw)
	begin := "<!-- agentsfs:begin " + instancePath + " -->"
	end := "<!-- agentsfs:end " + instancePath + " -->"
	block := RegistrationBlock(instancePath)

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

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
