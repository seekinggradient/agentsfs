package hub

import (
	"io/fs"
	"strings"
	"testing"

	afs "agentsfs.ai/afs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The Hub ships the Markdown To skill to every agent it serves.
//
// "Provisioning" here is not a bundle of files copied into a workspace — a
// remote agent working against Hub knowledge bases has no workspace and no
// skills directory. It is the docs tool, which every /mcp connection gets
// unconditionally (newMCPServer registers it before any scope is consulted).
// These tests are the guard that the skill actually rides that channel: it
// arrives verbatim, it is discoverable without knowing its name, and it comes
// to a read-only connection like any other read.

// mustEmbeddedSkill returns the vendored SKILL.md bytes — the same embed the
// pin test in internal/skills asserts against skills/markdownto/VERSION.
func mustEmbeddedSkill(t *testing.T) string {
	t.Helper()
	body, err := fs.ReadFile(afs.SkillsFS, "skills/markdownto/SKILL.md")
	if err != nil {
		t.Fatalf("read the embedded markdownto skill: %v", err)
	}
	return string(body)
}

// TestMCPServesMarkdownToSkill: an agent connected to the Hub can read the
// skill, byte for byte. Byte equality matters more than a substring match —
// the skill's scaffolds are literal file contents an agent copies, so a
// transform anywhere on this path would hand agents subtly wrong templates.
func TestMCPServesMarkdownToSkill(t *testing.T) {
	ts, _, acc := newMCPHub(t)
	tok := mkUser(t, acc, "alice")
	sess := mcpConnect(t, ts, tok)

	got := firstText(t, callText(t, sess, "docs", map[string]any{"topic": "markdownto"}))
	if got != mustEmbeddedSkill(t) {
		t.Fatalf("docs topic markdownto did not return the vendored skill verbatim (got %d bytes)", len(got))
	}
	// The parts an agent needs to author a conforming file without the CLI: the
	// envelope rule, a literal scaffold, and where the live specs are.
	for _, want := range []string{
		"markdownto: <name>@<major>.<minor>",
		"markdownto: kanban@0.1",
		"markdownto.ai/llms.txt",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("skill served over MCP is missing %q", want)
		}
	}
}

// TestMCPMarkdownToSkillIsDiscoverable: an agent that does not already know the topic
// name has to be able to find it. Both routes are checked — the tool
// description it sees in tools/list, and the topic index the tool itself
// prints — because an agent that has to guess "markdownto" is not provisioned
// with anything.
func TestMCPMarkdownToSkillIsDiscoverable(t *testing.T) {
	ts, _, acc := newMCPHub(t)
	tok := mkUser(t, acc, "alice")
	sess := mcpConnect(t, ts, tok)

	res, err := sess.ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var docsTool *mcp.Tool
	for _, tl := range res.Tools {
		if tl.Name == "docs" {
			docsTool = tl
		}
	}
	if docsTool == nil {
		t.Fatal("tools/list has no docs tool")
	}
	if !strings.Contains(docsTool.Description, "markdownto") {
		t.Errorf("docs tool description does not mention the markdownto topic: %q", docsTool.Description)
	}

	index := firstText(t, callText(t, sess, "docs", map[string]any{"topic": "list"}))
	if !strings.Contains(index, "markdownto") {
		t.Errorf("docs topic list does not name markdownto:\n%s", index)
	}
}

// TestMCPMarkdownToSkillReachesReadOnlyConnections: the skill is instructions, so a
// connection that may not write still gets it. This is the "out of the box,
// unconditional" claim stated as a test — if the skill ever moved behind a
// scope or a setting, this fails.
func TestMCPMarkdownToSkillReachesReadOnlyConnections(t *testing.T) {
	ts, _, acc := newMCPHub(t)
	mkUser(t, acc, "alice")

	// A read-only OAuth token, the same seam the write-rejection tests use.
	access, _, err := acc.IssueOAuthTokens("cli_test", "alice", scopeRead)
	if err != nil {
		t.Fatal(err)
	}
	sess := mcpConnect(t, ts, access)

	got := firstText(t, callText(t, sess, "docs", map[string]any{"topic": "markdownto"}))
	if got != mustEmbeddedSkill(t) {
		t.Fatal("a read-only connection did not receive the skill")
	}
}
