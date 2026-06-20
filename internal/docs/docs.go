// Package docs renders the documentation embedded in the afs binary.
package docs

import (
	"fmt"
	"io/fs"
	"strings"

	afs "agentsfs.ai/afs"
)

type Topic struct {
	Name        string
	Description string
	Path        string
}

var topics = []Topic{
	{
		Name:        "agent-start",
		Description: "agent-facing primer for understanding, setting up, and using AgentsFS from a fresh workspace",
		Path:        "docs/agent-start.md",
	},
	{
		Name:        "setup",
		Description: "full setup guide for humans and agents",
		Path:        "docs/setup.md",
	},
	{
		Name:        "contract",
		Description: "the AGENTS.md contract installed into every agentsfs instance",
		Path:        "template/AGENTS.md",
	},
	{
		Name:        "commands",
		Description: "CLI and MCP command overview",
		Path:        "",
	},
}

const commandOverview = `# afs commands

Orient
  afs tree [path]
  afs search <query> [path] [--semantic] [-n N]

Maintain
  afs doctor [path] [--json]
  afs backlinks <name> [path]
  afs rename <old> <new> [path]
  afs reindex [path] [--embeddings]

Connect agents
  afs setup [dir] [--yes] [--global]
  afs init [dir] [--shared] [--yes]
  afs connect <instance> [--global] [--yes]
  afs mcp [path]

Learn AgentsFS
  afs docs
  afs docs agent-start
  afs docs setup
  afs docs contract
  afs docs commands

Manage
  afs uninstall [--yes] [--dry-run] [--binary PATH] [--remove-global-connections]
  afs version
`

func List() string {
	var b strings.Builder
	b.WriteString("afs docs topics:\n")
	for _, topic := range topics {
		fmt.Fprintf(&b, "  %-12s %s\n", topic.Name, topic.Description)
	}
	b.WriteString("\nStart here from a fresh workspace:\n  afs docs agent-start\n")
	return b.String()
}

func Render(topic string) (string, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" || topic == "list" {
		return List(), nil
	}
	if topic == "--all" || topic == "all" {
		return renderAll()
	}
	if topic == "commands" {
		return commandOverview, nil
	}
	for _, candidate := range topics {
		if candidate.Name == topic {
			data, err := fs.ReadFile(afs.DocsFS, candidate.Path)
			if err != nil {
				return "", err
			}
			return string(data), nil
		}
	}
	return "", fmt.Errorf("unknown docs topic %q\n\n%s", topic, List())
}

func renderAll() (string, error) {
	var b strings.Builder
	for i, topic := range topics {
		out, err := Render(topic.Name)
		if err != nil {
			return "", err
		}
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.WriteString(out)
	}
	return b.String(), nil
}
