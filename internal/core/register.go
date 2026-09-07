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

// ProjectTargets selects the nearest repository root (including worktrees), or
// start itself outside Git. Never connect a different project's ancestor file.
// AGENTS.md is always a target; an existing CLAUDE.md is updated alongside it.
func ProjectTargets(start string) []Target {
	abs, err := filepath.Abs(start)
	if err != nil {
		return nil
	}
	project := abs
	for dir := abs; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			project = dir
			break
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	out := []Target{{filepath.Join(project, "AGENTS.md"), "project (" + project + ")", false}}
	if p := filepath.Join(project, "CLAUDE.md"); fileExists(p) {
		out = append(out, Target{p, "project (" + project + ")", false})
	}
	return out
}

// connectionPath makes embedded connections portable with the project.
func connectionPath(targetFile, instancePath string) string {
	rel, err := filepath.Rel(filepath.Dir(targetFile), instancePath)
	if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "./" + filepath.ToSlash(rel)
	}
	return instancePath
}

// ConnectionBlock is the canonical text appended to a harness file. Kept
// in sync with prompts/connection-snippet.md. The markers carry the instance
// path so multiple instances can coexist in one file and re-runs update in
// place instead of duplicating.
//
// The journal trigger line names the instance's actual journal directory,
// resolved from its INDEX.md `agentsfs_role: journal` marker at write time
// (contract 0.4.0), so a relocated or renamed journal is pointed at
// correctly. It falls back to the default agent-journal/ for a fresh instance
// that has laid down nothing yet.
func ConnectionBlock(instancePath string) string {
	journal := defaultJournalDir
	if rd, err := ResolveReservedDirs(instancePath); err == nil && rd.Journal != "" {
		journal = rd.Journal
	}
	return connectionBlockWithJournal(instancePath, journal)
}

func connectionBlockWithJournal(instancePath, journalDir string) string {
	return fmt.Sprintf(`<!-- agentsfs:begin %[1]s -->
## Persistent memory (agentsfs)

A durable, user-owned memory lives at `+"`%[1]s`"+`.
Before starting work, read `+"`%[1]s/AGENTS.md`"+` and orient yourself.
Consult it before re-researching anything you may already know, and record
durable knowledge there as you work, following its contract.
When `+"`afs`"+` is available, `+"`afs status %[1]s`"+` reports this instance's contract,
scoped worktree, and sync state; from a parent workspace, `+"`afs status <search-root>`"+`
discovers every local AgentsFS instance before multi-instance maintenance.
Start or resume an episode before work and checkpoint meaningful progress throughout the trajectory.
Follow `+"`%[1]s/%[2]s/INDEX.md`"+` for lifecycle and archival rules.
When this instance has a configured remote, pull before writing and immediately
push after every completed unit: use `+"`afs hub push`"+` for a Hub-linked instance
and `+"`git push`"+` for an ordinary remote. Do not wait for a user request or batch
completed work. If another checkout pushed first, reconcile before retrying;
never force-push.
<!-- agentsfs:end %[1]s -->`, instancePath, journalDir)
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
	displayPath := connectionPath(targetFile, instancePath)
	journal := defaultJournalDir
	if rd, err := ResolveReservedDirs(instancePath); err == nil && rd.Journal != "" {
		journal = rd.Journal
	}
	block := connectionBlockWithJournal(displayPath, journal)
	// Migrate a previous absolute connection without duplicating it.
	if displayPath != instancePath && strings.Contains(content, begin) {
		i := strings.Index(content, begin)
		j := strings.Index(content[i+len(begin):], end)
		if j < 0 {
			return fmt.Errorf("%s: malformed agentsfs markers", targetFile)
		}
		endAt := i + len(begin) + j + len(end)
		if strings.Contains(content, "<!-- agentsfs:begin "+displayPath+" -->") {
			content = content[:i] + content[endAt:]
		} else {
			content = content[:i] + block + content[endAt:]
		}
	}
	begin = "<!-- agentsfs:begin " + displayPath + " -->"
	end = "<!-- agentsfs:end " + displayPath + " -->"

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

// Disconnect removes the connection block for instancePath from targetFile.
// It returns true when a block was removed. Other content is left untouched.
func Disconnect(targetFile, instancePath string) (bool, error) {
	raw, err := os.ReadFile(targetFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	content, removed, err := removeConnectionBlocks(string(raw), instancePath)
	if err == nil && connectionPath(targetFile, instancePath) != instancePath {
		var extra int
		content, extra, err = removeConnectionBlocks(content, connectionPath(targetFile, instancePath))
		removed += extra
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", targetFile, err)
	}
	if removed == 0 {
		return false, nil
	}
	return true, os.WriteFile(targetFile, []byte(content), 0o644)
}

// DisconnectAll removes every agentsfs marker-fenced connection block from
// targetFile. It is intended for explicit machine-level uninstall cleanup.
func DisconnectAll(targetFile string) (int, error) {
	raw, err := os.ReadFile(targetFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	content, removed, err := removeConnectionBlocks(string(raw), "")
	if err != nil {
		return 0, fmt.Errorf("%s: %w", targetFile, err)
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, os.WriteFile(targetFile, []byte(content), 0o644)
}

func removeConnectionBlocks(content, instancePath string) (string, int, error) {
	removed := 0
	for {
		beginMarker := "<!-- agentsfs:begin"
		if instancePath != "" {
			beginMarker = "<!-- agentsfs:begin " + instancePath + " -->"
		}
		i := strings.Index(content, beginMarker)
		if i < 0 {
			return content, removed, nil
		}

		beginClose := strings.Index(content[i:], "-->")
		if beginClose < 0 {
			return content, removed, fmt.Errorf("malformed agentsfs begin marker")
		}
		beginText := content[i : i+beginClose+len("-->")]
		path := instancePath
		if path == "" {
			path = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(beginText, "<!-- agentsfs:begin"), "-->"))
			if path == "" {
				return content, removed, fmt.Errorf("malformed agentsfs begin marker")
			}
		}

		searchStart := i + beginClose + len("-->")
		endMarker := "<!-- agentsfs:end " + path + " -->"
		endRel := strings.Index(content[searchStart:], endMarker)
		if endRel < 0 {
			return content, removed, fmt.Errorf("malformed agentsfs markers")
		}
		endAfter := searchStart + endRel + len(endMarker)
		if endAfter < len(content) && content[endAfter] == '\n' {
			endAfter++
		}
		content = content[:i] + content[endAfter:]
		removed++
	}
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
