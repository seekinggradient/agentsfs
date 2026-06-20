package docs

import (
	"io/fs"
	"strings"
	"testing"

	afs "agentsfs.ai/afs"
)

func TestTopicsRenderAndAreDocumented(t *testing.T) {
	list := List()
	all, err := Render("all")
	if err != nil {
		t.Fatalf("Render(all): %v", err)
	}
	readme := mustReadEmbedded(t, "README.md")

	for _, topic := range Topics() {
		if !strings.Contains(list, topic.Name) {
			t.Fatalf("docs list does not include topic %q:\n%s", topic.Name, list)
		}
		rendered, err := Render(topic.Name)
		if err != nil {
			t.Fatalf("Render(%q): %v", topic.Name, err)
		}
		if !strings.Contains(all, rendered) {
			t.Fatalf("Render(all) does not include topic %q", topic.Name)
		}
		if topic.Path != "" {
			data := mustReadEmbedded(t, topic.Path)
			if !strings.Contains(data, "description:") {
				t.Fatalf("%s is missing description frontmatter", topic.Path)
			}
			if !strings.Contains(readme, topic.Path) {
				t.Fatalf("README.md does not link embedded docs topic %q at %s", topic.Name, topic.Path)
			}
		}
	}
	if !strings.Contains(readme, "afs docs commands") {
		t.Fatalf("README.md does not mention the embedded commands topic")
	}
}

func TestCommandDocsStayInSync(t *testing.T) {
	usage := CommandUsage()
	commands, err := Render("commands")
	if err != nil {
		t.Fatalf("Render(commands): %v", err)
	}
	readme := mustReadEmbedded(t, "README.md")

	for _, cmd := range Commands() {
		if !strings.Contains(usage, cmd.Usage) {
			t.Fatalf("CommandUsage missing %q", cmd.Usage)
		}
		if !strings.Contains(commands, cmd.Usage) {
			t.Fatalf("commands doc missing %q", cmd.Usage)
		}
		if !strings.Contains(readme, "afs "+commandName(cmd.Usage)) {
			t.Fatalf("README.md does not mention command family %q", commandName(cmd.Usage))
		}
	}
}

func TestAgentStartKeepsSetupGuardrails(t *testing.T) {
	out, err := Render("agent-start")
	if err != nil {
		t.Fatalf("Render(agent-start): %v", err)
	}
	for _, want := range []string{
		"Do not run setup commands until the user answers",
		"What AgentsFS is",
		"Why it helps",
		"afs setup --yes",
		"Do not ask the user to design the knowledge-base taxonomy",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("agent-start docs drifted; missing %q", want)
		}
	}
}

func commandName(usage string) string {
	fields := strings.Fields(usage)
	if len(fields) < 2 || fields[0] != "afs" {
		return ""
	}
	return fields[1]
}

func mustReadEmbedded(t *testing.T, path string) string {
	t.Helper()
	data, err := fs.ReadFile(afs.DocsFS, path)
	if err != nil {
		t.Fatalf("read embedded %s: %v", path, err)
	}
	return string(data)
}
