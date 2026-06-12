// Package mcpserver exposes the same core capabilities as the CLI over the
// Model Context Protocol, for harnesses that can't shell out. No logic
// lives here — every tool is a thin adapter over internal/core.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"agentsfs.ai/afs/internal/core"
)

// New builds the MCP server. startDir anchors instance discovery: tools
// default to the instance containing it, and accept an explicit path for
// multi-instance setups.
func New(version, startDir string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "agentsfs", Version: version}, nil)

	resolve := func(path string) (string, error) {
		if path == "" {
			path = startDir
		}
		return core.FindRoot(path)
	}
	text := func(s string) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
	}

	type pathIn struct {
		Path string `json:"path,omitempty" jsonschema:"path inside the agentsfs instance (default: the instance the server was started in)"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "tree",
		Description: "Orient in the agentsfs memory: the full tree with every file and directory's one-line description and last-touched age. Call this first.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, any, error) {
		root, err := resolve(in.Path)
		if err != nil {
			return nil, nil, err
		}
		out, err := core.Tree(root)
		if err != nil {
			return nil, nil, err
		}
		return text(out), nil, nil
	})

	type searchIn struct {
		Query    string `json:"query" jsonschema:"words to search for"`
		Semantic bool   `json:"semantic,omitempty" jsonschema:"use the embedding index instead of full-text (requires afs reindex --embeddings to have run)"`
		Limit    int    `json:"limit,omitempty" jsonschema:"max results (default 10)"`
		Path     string `json:"path,omitempty" jsonschema:"path inside the instance (default: the server's instance)"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search",
		Description: "Search the agentsfs memory. Full-text by default (ranked, section-level hits with snippets); semantic optionally. Use before re-researching anything the memory may already know.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, any, error) {
		root, err := resolve(in.Path)
		if err != nil {
			return nil, nil, err
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 10
		}
		var results []core.SearchResult
		warning := ""
		if in.Semantic {
			results, warning, err = core.SemanticSearch(root, in.Query, limit)
		} else {
			results, err = core.Search(root, in.Query, limit)
		}
		if err != nil {
			return nil, nil, err
		}
		var b strings.Builder
		if warning != "" {
			fmt.Fprintf(&b, "warning: %s\n", warning)
		}
		if len(results) == 0 {
			b.WriteString("no matches (try fewer or different words)")
		}
		for _, r := range results {
			if r.Score != 0 {
				fmt.Fprintf(&b, "%.3f  ", r.Score)
			}
			fmt.Fprintf(&b, "%s § %s\n      %s\n", r.Path, r.Heading, r.Snippet)
		}
		return text(b.String()), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "doctor",
		Description: "Deterministic health check of the agentsfs memory: missing descriptions or INDEX.md files, dead/ambiguous wikilinks, stubs, orphans. Returns JSON findings — the maintenance worklist.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, any, error) {
		root, err := resolve(in.Path)
		if err != nil {
			return nil, nil, err
		}
		findings, err := core.Doctor(root)
		if err != nil {
			return nil, nil, err
		}
		if findings == nil {
			findings = []core.Finding{}
		}
		j, err := json.MarshalIndent(findings, "", "  ")
		if err != nil {
			return nil, nil, err
		}
		return text(string(j)), nil, nil
	})

	type backlinksIn struct {
		Name string `json:"name" jsonschema:"file name or [[link]] target to find references to"`
		Path string `json:"path,omitempty" jsonschema:"path inside the instance (default: the server's instance)"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "backlinks",
		Description: "Find every [[wikilink]] in the agentsfs memory that points at a given file or name — 'find all references' for knowledge.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in backlinksIn) (*mcp.CallToolResult, any, error) {
		root, err := resolve(in.Path)
		if err != nil {
			return nil, nil, err
		}
		links, err := core.Backlinks(root, in.Name)
		if err != nil {
			return nil, nil, err
		}
		if len(links) == 0 {
			return text(fmt.Sprintf("no links to %q found", in.Name)), nil, nil
		}
		var b strings.Builder
		for _, l := range links {
			fmt.Fprintf(&b, "%s:%d  [[%s]]\n", l.Source, l.Line, l.Target)
		}
		return text(b.String()), nil, nil
	})

	type renameIn struct {
		Old  string `json:"old" jsonschema:"current path of the file, relative to the instance root"`
		New  string `json:"new" jsonschema:"new name (same directory) or new relative path"`
		Path string `json:"path,omitempty" jsonschema:"path inside the instance (default: the server's instance)"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "rename",
		Description: "Rename or move a file in the agentsfs memory and rewrite every [[wikilink]] to it in one pass. Leaves changes uncommitted for review.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in renameIn) (*mcp.CallToolResult, any, error) {
		root, err := resolve(in.Path)
		if err != nil {
			return nil, nil, err
		}
		res, err := core.Rename(root, in.Old, in.New)
		if err != nil {
			return nil, nil, err
		}
		return text(fmt.Sprintf("renamed %s → %s; rewrote %d link(s) in %d file(s); changes are uncommitted — review and commit",
			res.OldRel, res.NewRel, res.LinksRewrote, len(res.FilesChanged))), nil, nil
	})

	return s
}
