// Package docs renders the documentation embedded in the afs binary and
// holds the hand-maintained CLI command table behind afs help, afs docs
// commands, and the docs tool on both MCP servers — the local afs mcp
// (internal/mcpserver) and the Hub's remote /mcp (internal/hub/mcpapi.go).
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
		Name:        "concepts",
		Description: "the vocabulary: instance, knowledge base, contract, roles, wikilinks, Hub, Eve",
		Path:        "docs/concepts.md",
	},
	{
		Name:        "capabilities",
		Description: "what each surface can do: CLI, local afs mcp, Hub web, Hub /mcp",
		Path:        "docs/capabilities.md",
	},
	{
		Name:        "setup",
		Description: "full setup guide for humans and agents",
		Path:        "docs/setup.md",
	},
	{
		Name:        "mcp",
		Description: "both MCP servers: the local afs mcp and the Hub's remote /mcp",
		Path:        "docs/mcp.md",
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
		Description: "CLI command overview",
		Path:        "",
	},
}

// descWrapWidth is how wide a command description is allowed to get before
// wrapping, not counting its 6-space hanging indent — chosen so a wrapped
// line plus indent comfortably fits an 80-column terminal.
const descWrapWidth = 72

var commands = []Command{
	{"Connect agents", "afs setup [dir] [--yes] [--global]", "create or reuse a personal agentsfs, then connect the current project"},
	{"Connect agents", "afs init [dir] [--shared] [--yes]", "create an agentsfs instance exactly at dir"},
	{"Connect agents", "afs connect <instance> [--global] [--yes]", "point a project or global harness config at an existing instance"},
	{"Connect agents", "afs mcp [path]", "serve 12 of these commands as MCP tools (docs, status, tree, search, doctor, roles, backlinks, rename, hub_status, hub_push, hub_pull, hub_list) for harnesses that can't shell out — not the full CLI, and not the same tool set as the Hub's separate MCP endpoint (search, fetch, list_kbs, tree, docs, write)"},
	{"Sync to a Hub", "afs hub login [--url URL] [--user NAME] [--token TOKEN]", "sign in to a hosted agentsfs Hub (default hub.agentsfs.ai)"},
	{"Sync to a Hub", "afs hub push [name]", "upload this agentsfs to your hub account (link + push, repeatable)"},
	{"Sync to a Hub", "afs hub pull <name> [dir] [--merge]", "download a knowledge base into the current directory; --merge folds it into the current instance"},
	{"Sync to a Hub", "afs hub list", "list your repositories and knowledge bases shared with you on the hub"},
	{"Sync to a Hub", "afs hub status", "show hub sign-in and whether this agentsfs is linked"},
	{"Sync to a Hub", "afs hub logout", "forget the saved hub sign-in on this machine"},
	{"Orient", "afs status [search-root...] [--json] [--doctor] [--fetch]", "summarize discovered AgentsFS instances, contract state, worktrees, sync, health, and duplicates"},
	{"Orient", "afs tree [dir] [-d|--depth N]", "the tree with descriptions and freshness; scope to dir and cap depth on large instances"},
	{"Orient", "afs search <query> [path] [--context[=N]] [--json] [--semantic] [-n|--limit N]", "ranked search; --context hydrates the top hits into a token-budgeted pack (CLI-only — ignores --semantic and -n/--limit)"},
	{"Orient", "afs roles [path] [--json]", "where the reserved roles live (journal, scratch, collections) — ask instead of hardcoding names"},
	{"Configure", "afs embeddings [status] | setup <openai|voyage> [--yes] | clear [--yes]", "check status, set up, or clear optional semantic-search embeddings"},
	{"Maintain", "afs doctor [path] [--json]", "deterministic health check; exits 1 if any finding is severity error"},
	{"Maintain", "afs backlinks <name> [path]", "all [[wikilinks]] resolving to a file"},
	{"Maintain", "afs rename <old> <new> [path]", "move a file and rewrite every link to it"},
	{"Maintain", "afs reindex [path] [--embeddings]", "rebuild the derived index from the files"},
	{"Learn AgentsFS", "afs docs [topic|--all]", "read bundled AgentsFS docs; start with afs docs agent-start"},
	{"Learn AgentsFS", "afs contract [current|status|diff|upgrade] [path] [--yes] [--force]", "inspect the bundled AGENTS.md contract, or diff/upgrade this instance's AGENTS.md against it"},
	{"Learn AgentsFS", "afs skills [list]", "materialize the bundled agent skills (agentsfs-setup, agentsfs-remember, agentsfs-adopt, agentsfs-garden) to disk on every run, and print where to copy them"},
	{"Manage", "afs update [--check] [--yes] [--force]", "check for a newer afs and update user-installed binaries"},
	{"Manage", "afs uninstall [--yes] [--dry-run] [--binary PATH] [--remove-global-connections]", "remove the CLI. Never deletes any agentsfs filesystem or git data — but it does clear the materialized skills cache, and --remove-global-connections rewrites global harness config (~/.claude/CLAUDE.md, ~/.codex/AGENTS.md)"},
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

// CommandUsage renders the command table for the primary `afs help` /
// bare `afs` screen: grouped under the same priority-ordered headers as
// commandOverview, with descriptions wrapped and hanging-indented under
// their usage line instead of padded onto one line regardless of width.
func CommandUsage() string {
	return strings.TrimRight(renderCommandGroups(""), "\n") + "\n"
}

func List() string {
	var b strings.Builder
	b.WriteString("afs docs topics:\n")
	for _, topic := range topics {
		fmt.Fprintf(&b, "  %-13s %s\n", topic.Name, topic.Description)
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

// commandOverview is `afs docs commands`: the same grouped table as
// CommandUsage, under a markdown-ish header, for reading outside the help
// screen.
func commandOverview() string {
	return "# afs commands\n" + renderCommandGroups("")
}

// renderCommandGroups writes the command table under its Group headers, in
// the order groups first appear in the table (a deliberate priority order —
// Connect agents, Sync to a Hub, Orient, ... — not alphabetical). Every
// group, including the first, gets a blank line before its header. prefix,
// if non-empty, is written before that first blank line. Descriptions wrap
// to descWrapWidth with a hanging indent so long entries (the mcp and
// uninstall rows both run well past 78 columns) don't rely on terminal
// soft-wrap.
func renderCommandGroups(prefix string) string {
	var b strings.Builder
	if prefix != "" {
		b.WriteString(prefix)
	}
	group := ""
	for _, cmd := range commands {
		if cmd.Group != group {
			group = cmd.Group
			fmt.Fprintf(&b, "\n%s\n", group)
		}
		fmt.Fprintf(&b, "  %s\n", cmd.Usage)
		for _, line := range wrapDescription(cmd.Description) {
			fmt.Fprintf(&b, "      %s\n", line)
		}
	}
	return b.String()
}

// wrapDescription greedily wraps text on word boundaries to descWrapWidth.
// It never breaks a word, so a single token longer than the width still
// prints on its own (overlong) line rather than being cut.
func wrapDescription(text string) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	var line strings.Builder
	for _, w := range words {
		if line.Len() > 0 && line.Len()+1+len(w) > descWrapWidth {
			lines = append(lines, line.String())
			line.Reset()
		}
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(w)
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
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
