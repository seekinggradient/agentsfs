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

type Command struct {
	Group       string
	Usage       string
	Description string
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
		Name:        "hub",
		Description: "connect an agentsfs to a hosted Hub and upload it (afs hub / MCP)",
		Path:        "docs/hub.md",
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

var commands = []Command{
	{"Connect agents", "afs setup [dir] [--yes] [--global]", "create or reuse a personal agentsfs, then connect the current project"},
	{"Connect agents", "afs init [dir] [--shared] [--yes]", "create an agentsfs instance exactly at dir"},
	{"Connect agents", "afs connect <instance> [--global] [--yes]", "point a project or global harness config at an existing instance"},
	{"Connect agents", "afs mcp [path]", "serve the same capabilities over MCP"},
	{"Sync to a Hub", "afs hub login [--url URL] [--user NAME] [--token TOKEN]", "sign in to a hosted agentsfs Hub (default hub.agentsfs.ai)"},
	{"Sync to a Hub", "afs hub push [name]", "upload this agentsfs to your hub account (link + push, repeatable)"},
	{"Sync to a Hub", "afs hub pull <name> [dir] [--merge]", "download a knowledgebase into the current directory; --merge folds it into the current instance"},
	{"Sync to a Hub", "afs hub list", "list your repositories and knowledge bases shared with you on the hub"},
	{"Sync to a Hub", "afs hub status", "show hub sign-in and whether this agentsfs is linked"},
	{"Orient", "afs status [search-root...] [--json] [--doctor] [--fetch]", "summarize discovered AgentsFS instances, contract state, worktrees, sync, health, and duplicates"},
	{"Orient", "afs tree [dir] [--depth N]", "the tree with descriptions and freshness; scope to dir and cap depth on large instances"},
	{"Orient", "afs search <query> [path] [--semantic] [-n N]", "ranked full-text or semantic search over the instance"},
	{"Configure", "afs embeddings <status|setup|clear> [provider] [--yes]", "configure optional semantic search embeddings"},
	{"Maintain", "afs doctor [path] [--json]", "deterministic health check"},
	{"Maintain", "afs backlinks <name> [path]", "all [[wikilinks]] resolving to a file"},
	{"Maintain", "afs rename <old> <new> [path]", "move a file and rewrite every link to it"},
	{"Maintain", "afs reindex [path] [--embeddings]", "rebuild the derived index from the files"},
	{"Learn AgentsFS", "afs docs [topic|--all]", "read bundled AgentsFS docs; start with afs docs agent-start"},
	{"Learn AgentsFS", "afs contract [current|status|diff|upgrade] [path]", "inspect, diff, or upgrade the bundled AGENTS.md contract"},
	{"Manage", "afs update [--check] [--yes] [--force]", "check for a newer afs and update user-installed binaries"},
	{"Manage", "afs uninstall [--yes] [--dry-run] [--binary PATH] [--remove-global-connections]", "remove the CLI. Never deletes any agentsfs filesystem or git data"},
	{"Manage", "afs version", "print the installed afs version"},
}

func Topics() []Topic {
	out := make([]Topic, len(topics))
	copy(out, topics)
	return out
}

func Commands() []Command {
	out := make([]Command, len(commands))
	copy(out, commands)
	return out
}

func CommandUsage() string {
	var b strings.Builder
	for _, cmd := range commands {
		fmt.Fprintf(&b, "  %-78s %s\n", cmd.Usage, cmd.Description)
	}
	return b.String()
}

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
		return commandOverview(), nil
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

func commandOverview() string {
	var b strings.Builder
	b.WriteString("# afs commands\n")
	group := ""
	for _, cmd := range commands {
		if cmd.Group != group {
			group = cmd.Group
			fmt.Fprintf(&b, "\n%s\n", group)
		}
		fmt.Fprintf(&b, "  %s\n      %s\n", cmd.Usage, cmd.Description)
	}
	return b.String()
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
