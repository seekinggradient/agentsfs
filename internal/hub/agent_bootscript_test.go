package hub

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The boot script, its detached-start wrapper, and the in-place env update
// are generated shell — a quoting or heredoc regression only ever surfaces
// inside a production sprite. Render them with realistic inputs and let a
// real sh syntax-check the result (sh -n parses without executing).
func TestGeneratedSpriteScriptsPassShellSyntaxCheck(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	t.Setenv("CHAT_REASONING_EFFORT", "high")
	m := NewAgentManager("tok", "oai", "", "https://hub.example", nil, nil)
	boot := m.bootScript("alice", "afs_testtoken1234", ",AFS_BIN=/home/sprite/.local/bin/afs",
		[]RepoRef{{"alice", "notes"}, {"bob", "shared-kb"}})

	// The wrapper embeds the boot script in a heredoc: no boot line may equal
	// the outer terminator or the wrapper would truncate it silently.
	for _, line := range strings.Split(boot, "\n") {
		if strings.TrimSpace(line) == "AFSRUNEOF" {
			t.Fatal("boot script contains the start wrapper's heredoc terminator")
		}
	}
	if env := m.workspaceServiceEnv("__AFS_TOKEN__", ""); strings.ContainsAny(env, "'") {
		t.Fatalf("service env contains a single quote, breaking --env quoting: %s", env)
	}

	for name, script := range map[string]string{
		"boot":      boot,
		"wrapper":   detachedStartScript(bootRunBase, boot),
		"envupdate": m.updateServiceScript(),
	} {
		file := filepath.Join(t.TempDir(), name+".sh")
		if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("sh", "-n", file).CombinedOutput(); err != nil {
			t.Fatalf("%s script fails sh -n: %v\n%s", name, err, out)
		}
	}
}
