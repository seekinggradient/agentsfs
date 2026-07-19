// Package mcpserver exposes the same core capabilities as the CLI over the
// Model Context Protocol, for harnesses that can't shell out. No logic
// lives here — every tool is a thin adapter over internal/core.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"agentsfs.ai/afs/internal/core"
	afsdocs "agentsfs.ai/afs/internal/docs"
	"agentsfs.ai/afs/internal/hubclient"
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

	type docsIn struct {
		Topic string `json:"topic,omitempty" jsonschema:"docs topic to read: agent-start, setup, hub, contract, commands, list, or all (default: agent-start)"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "docs",
		Description: "Read bundled AgentsFS documentation. Use topic agent-start from a fresh workspace to understand what AgentsFS is, why it helps, and how to set it up before an instance exists.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in docsIn) (*mcp.CallToolResult, any, error) {
		topic := in.Topic
		if strings.TrimSpace(topic) == "" {
			topic = "agent-start"
		}
		out, err := afsdocs.Render(topic)
		if err != nil {
			return nil, nil, err
		}
		return text(out), nil, nil
	})

	type statusIn struct {
		Roots  []string `json:"roots,omitempty" jsonschema:"directories to search recursively; relative paths resolve against the server start directory (default: start directory or its enclosing AgentsFS instance)"`
		Doctor bool     `json:"doctor,omitempty" jsonschema:"run afs doctor for each discovered instance and include compact finding counts"`
		Fetch  bool     `json:"fetch,omitempty" jsonschema:"explicitly contact git remotes before calculating ahead/behind state; false keeps the operation local and read-only"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "status",
		Description: "Discover every local AgentsFS instance beneath one or more directories and return JSON contract, git, sync, optional doctor, and duplicate-checkout status. Use this from an unfamiliar machine or workspace before creating another knowledge base or planning contract migrations. It is local and read-only unless fetch is explicitly true.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in statusIn) (*mcp.CallToolResult, any, error) {
		roots := in.Roots
		if len(roots) == 0 {
			roots = []string{startDir}
		} else {
			resolved := make([]string, 0, len(roots))
			for _, root := range roots {
				if !filepath.IsAbs(root) {
					root = filepath.Join(startDir, root)
				}
				resolved = append(resolved, root)
			}
			roots = resolved
		}
		report := core.StatusInstances(roots, core.StatusOptions{
			Doctor: in.Doctor,
			Fetch:  in.Fetch,
		})
		j, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return nil, nil, err
		}
		return text(string(j)), nil, nil
	})

	type treeIn struct {
		Path  string `json:"path,omitempty" jsonschema:"directory to scope the tree to; locates the instance and shows only that subtree (default: the whole instance the server started in)"`
		Depth int    `json:"depth,omitempty" jsonschema:"max levels to show below the starting directory; 0 or omitted means unlimited. Use e.g. 2 on a large instance to orient without expanding everything"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "tree",
		Description: "Orient in the agentsfs memory: an indented tree with every file and directory's one-line description and last-touched age. Call this first. On a large instance, pass path to focus on one subdirectory and depth to cap how deep it expands.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in treeIn) (*mcp.CallToolResult, any, error) {
		root, subdir := "", "."
		var err error
		if strings.TrimSpace(in.Path) == "" {
			root, err = core.FindRoot(startDir)
		} else {
			root, subdir, err = core.ResolveScope(in.Path)
		}
		if err != nil {
			return nil, nil, err
		}
		out, err := core.Tree(root, subdir, in.Depth)
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

	mcp.AddTool(s, &mcp.Tool{
		Name:        "roles",
		Description: "Where this instance's reserved roles live: the session journal, the scratch space, and any collections. Returns JSON with each role's directory and how it was resolved (marker, classic name, or none). Use this to locate the journal rather than assuming a directory name — the contract owns those names and has changed them before.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, any, error) {
		root, err := resolve(in.Path)
		if err != nil {
			return nil, nil, err
		}
		roles, err := core.ResolveReservedDirs(root)
		if err != nil {
			return nil, nil, err
		}
		j, err := json.MarshalIndent(roles, "", "  ")
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

	mcp.AddTool(s, &mcp.Tool{
		Name:        "hub_status",
		Description: "Check whether the user is signed in to a hosted agentsfs Hub, and whether this instance is linked to it. Call before hub_push.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, any, error) {
		root, _ := resolve(in.Path)
		st := hubclient.GetStatus(root)
		if !st.SignedIn {
			return text("Not signed in to a hub. Ask the user to run `afs hub login` (they create an access token at the hub's /account page first)."), nil, nil
		}
		msg := fmt.Sprintf("Signed in to %s as %s.", st.URL, st.User)
		if st.Linked {
			msg += " This agentsfs is linked: " + st.LinkedURL + " — hub_push syncs updates."
		} else if root != "" {
			msg += " This agentsfs is not linked yet — call hub_push to upload it."
		}
		return text(msg), nil, nil
	})

	type hubPushIn struct {
		Name string `json:"name,omitempty" jsonschema:"name/slug for the repo on the hub (default: the instance folder's name)"`
		Path string `json:"path,omitempty" jsonschema:"path inside the instance (default: the server's instance)"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "hub_push",
		Description: "Upload this agentsfs to the user's hosted Hub account (git push). Requires the user to have run `afs hub login` first (check with hub_status). Adds a 'hub' git remote and pushes the current branch; repeatable to sync updates. Repos are private by default. Returns the hub URL.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in hubPushIn) (*mcp.CallToolResult, any, error) {
		root, err := resolve(in.Path)
		if err != nil {
			return nil, nil, err
		}
		res, err := hubclient.Push(root, in.Name)
		if err != nil {
			return nil, nil, err
		}
		return text(fmt.Sprintf("Uploaded to %s (branch %s). It is private by default; the user can make it public in the hub's repo Settings.", res.ViewURL, res.Branch)), nil, nil
	})

	type hubPullIn struct {
		Name  string `json:"name" jsonschema:"repo to pull: a slug in the user's account, or <user>/<slug> for someone else's"`
		Dir   string `json:"dir,omitempty" jsonschema:"target directory (default: <slug> under the server's start dir); a relative path resolves against the start dir"`
		Merge bool   `json:"merge,omitempty" jsonschema:"drop the pulled repo's .git so its notes fold into the surrounding instance (combine knowledgebases); needs a directory that doesn't exist yet"`
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "hub_pull",
		Description: "Download a knowledgebase from the user's hosted Hub into the local filesystem (real git clone; re-run to update an existing checkout). Requires the user to have run `afs hub login`. Use to get a specific agentsfs wherever you are working. Set merge to fold it into the current instance instead of keeping it as its own repo. Returns where it landed.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in hubPullIn) (*mcp.CallToolResult, any, error) {
		cfg, err := hubclient.Load()
		if err != nil {
			return text("Not signed in to a hub. Ask the user to run `afs hub login` first."), nil, nil
		}
		_, slug, err := hubclient.ParseRef(in.Name, cfg.User)
		if err != nil {
			return nil, nil, err
		}
		dir := in.Dir
		if dir == "" {
			dir = filepath.Join(startDir, slug)
		} else if !filepath.IsAbs(dir) {
			dir = filepath.Join(startDir, dir)
		}
		res, err := hubclient.Clone(in.Name, dir, in.Merge)
		if err != nil {
			return nil, nil, err
		}
		verb := "Cloned"
		switch {
		case res.Merged:
			verb = "Merged (dropped .git)"
		case res.Updated:
			verb = "Updated"
		}
		return text(fmt.Sprintf("%s %s/%s into %s — view at %s", verb, res.Owner, res.Slug, res.Dir, res.ViewURL)), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "hub_list",
		Description: "List all repositories visible to the user in the hosted agentsfs Hub, including knowledge bases shared with them — owner/name, access role, visibility (private/public), note count, last update, and URL. Requires the user to have run `afs hub login`.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		repos, err := hubclient.List()
		if err != nil {
			return nil, nil, err
		}
		if len(repos) == 0 {
			return text("No repositories on the hub yet. Use hub_push to upload one."), nil, nil
		}
		var b strings.Builder
		for _, r := range repos {
			vis := "private"
			if r.Public {
				vis = "public"
			}
			name := r.Name
			access := "owned"
			if r.Shared {
				name = r.Owner + "/" + r.Name
				access = r.Role
			}
			fmt.Fprintf(&b, "%s  [%s, %s]  %d notes  updated %s\n    %s\n", name, access, vis, r.Notes, r.Updated, r.URL)
			if r.Description != "" {
				fmt.Fprintf(&b, "    %s\n", r.Description)
			}
		}
		return text(b.String()), nil, nil
	})

	return s
}
