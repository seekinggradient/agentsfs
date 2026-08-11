package hub

import (
	"bytes"
	"strings"
	"testing"
)

// findChild returns the direct child of n named name (a directory keeps its
// trailing-slash-free name), or nil.
func findChild(n *treeNode, name string) *treeNode {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestTreeDirectoriesLinkToTheirIndex pins the reachability rule: an INDEX.md
// row stays hidden (its description decorates the directory), but the directory
// row itself becomes a link to that INDEX.md — the only way the backlog spine
// is reachable from a Files view. Directories with no INDEX.md stay plain.
func TestTreeDirectoriesLinkToTheirIndex(t *testing.T) {
	files := []RepoFile{
		{Path: "INDEX.md", Description: "the whole base"},
		{Path: "NOTE.md", Description: "top note"},
		{Path: "backlog/INDEX.md", Description: "the spine", LastCommit: 100},
		{Path: "backlog/ticket.md", Description: "one ticket", LastCommit: 90},
		{Path: "notes/loose.md", Description: "no index here"},
		{Path: ".agentsfs/config.json"},
	}
	tree := buildTree(files, "alice", "brain")

	backlog := findChild(tree, "backlog")
	if backlog == nil {
		t.Fatal("backlog directory missing from the tree")
	}
	if backlog.Href != "/alice/brain/blob/backlog/INDEX.md" {
		t.Errorf("backlog dir Href = %q, want the blob view of its INDEX.md", backlog.Href)
	}
	if backlog.Desc != "the spine" {
		t.Errorf("backlog dir Desc = %q, want the INDEX.md description", backlog.Desc)
	}
	for _, c := range backlog.Children {
		if c.Name == "INDEX.md" {
			t.Error("INDEX.md should stay hidden as a file row")
		}
	}

	notes := findChild(tree, "notes")
	if notes == nil {
		t.Fatal("notes directory missing from the tree")
	}
	if notes.Href != "" {
		t.Errorf("a directory without an INDEX.md must not be a link, got %q", notes.Href)
	}

	// .agentsfs is treated like any other directory: listed, and unlinked
	// because it holds no INDEX.md.
	dotAgents := findChild(tree, ".agentsfs")
	if dotAgents == nil {
		t.Fatal(".agentsfs directory handling changed: it is no longer listed")
	}
	if dotAgents.Href != "" {
		t.Errorf(".agentsfs Href = %q, want no link", dotAgents.Href)
	}

	// The repo root's own INDEX.md never turns the (unrendered) root into a link.
	if tree.Href != "" {
		t.Errorf("root node Href = %q, want empty", tree.Href)
	}
}

// TestTreeKeepsIndexOnlyDirectories covers the sub-backlog shape introduced by
// contract 0.11.0: a directory whose only file is INDEX.md used to be pruned
// entirely, making the sub-spine invisible in both Files views.
func TestTreeKeepsIndexOnlyDirectories(t *testing.T) {
	files := []RepoFile{
		{Path: "backlog/INDEX.md", Description: "the spine", LastCommit: 100},
		{Path: "backlog/voice/INDEX.md", Description: "voice work", LastCommit: 120},
	}
	tree := buildTree(files, "alice", "brain")

	backlog := findChild(tree, "backlog")
	if backlog == nil {
		t.Fatal("backlog directory missing")
	}
	voice := findChild(backlog, "voice")
	if voice == nil {
		t.Fatal("an INDEX-only directory was pruned from the tree")
	}
	if voice.Href != "/alice/brain/blob/backlog/voice/INDEX.md" {
		t.Errorf("voice dir Href = %q, want its INDEX.md blob view", voice.Href)
	}
	if voice.Desc != "voice work" {
		t.Errorf("voice dir Desc = %q, want the INDEX.md description", voice.Desc)
	}
	if voice.Age == "" {
		t.Error("an INDEX-only directory should still report freshness from its INDEX.md")
	}

	// Viewing an INDEX.md highlights the directory row that stands in for it.
	if !markCurrent(tree, "backlog/voice/INDEX.md") {
		t.Fatal("markCurrent did not find the directory standing in for backlog/voice/INDEX.md")
	}
	if !voice.Current {
		t.Error("the directory row for the viewed INDEX.md is not marked current")
	}
}

// TestTreeFilterMatchesLinkedDirectoryRows keeps the filter box from hiding
// INDEX-only directories, which have no leaf rows to match on their behalf.
func TestTreeFilterMatchesLinkedDirectoryRows(t *testing.T) {
	asset, err := assetsFS.ReadFile("assets/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	if !strings.Contains(string(asset), `tree.querySelectorAll("li.dir > .row a.node-name")`) {
		t.Error("tree filter does not consider linked directory rows")
	}
}

// TestTreeTemplateRendersLinkedDirectoryRows checks the rendered markup, since
// both Files views (repo home and the file-page sidebar) share "treenode".
func TestTreeTemplateRendersLinkedDirectoryRows(t *testing.T) {
	files := []RepoFile{
		{Path: "backlog/INDEX.md", Description: "the spine"},
		{Path: "backlog/voice/INDEX.md", Description: "voice work"},
		{Path: "notes/loose.md", Description: "no index here"},
	}
	tree := buildTree(files, "alice", "brain")
	markCurrent(tree, "backlog/INDEX.md")

	for name, data := range map[string]any{
		"repo": repoData{baseData: baseData{User: "alice", Viewer: "alice"}, Repo: "brain", DisplayName: "brain", Root: tree},
		"file": fileData{
			baseData: baseData{User: "alice", Viewer: "alice", FileView: true},
			Repo:     "brain", Path: "backlog/INDEX.md", Name: "INDEX.md",
			IsText: true, RawText: "body", Tree: tree,
		},
	} {
		var buf bytes.Buffer
		if err := parsePages()[name].ExecuteTemplate(&buf, "base", data); err != nil {
			t.Fatalf("render %s page: %v", name, err)
		}
		out := buf.String()
		for _, want := range []string{
			`href="/alice/brain/blob/backlog/INDEX.md">backlog/</a>`,
			`href="/alice/brain/blob/backlog/voice/INDEX.md">voice/</a>`,
			`<span class="node-name" title="notes/">notes/</span>`,
			`style="--tree-depth: 1"`,
			`the spine`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s page missing %q", name, want)
			}
		}
		if n := strings.Count(out, "node-name current"); n != 1 {
			t.Errorf("%s page: expected exactly 1 current node, got %d", name, n)
		}
	}
}

func TestTreeSortsFoldersBeforeFiles(t *testing.T) {
	tree := buildTree([]RepoFile{
		{Path: "z-last.md"},
		{Path: "projects/plan.md"},
		{Path: "archive/INDEX.md"},
		{Path: "a-first.md"},
	}, "alice", "brain")

	want := []string{"archive", "projects", "a-first.md", "z-last.md"}
	if len(tree.Children) != len(want) {
		t.Fatalf("root children = %d, want %d", len(tree.Children), len(want))
	}
	for i, name := range want {
		if tree.Children[i].Name != name {
			t.Errorf("child %d = %q, want %q", i, tree.Children[i].Name, name)
		}
	}
}
