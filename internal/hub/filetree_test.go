package hub

import (
	"bytes"
	"strings"
	"testing"
)

// TestFileViewSideTree renders the file page through the real templates and
// asserts the left-nav file tree appears with the viewed note highlighted.
func TestFileViewSideTree(t *testing.T) {
	files := []RepoFile{
		{Path: "NOTE.md", Description: "top note"},
		{Path: "projects/INDEX.md", Description: "active projects"},
		{Path: "projects/plan.md", Description: "the plan"},
	}
	tree := buildTree(files, "alice", "brain")
	if !markCurrent(tree, "projects/plan.md") {
		t.Fatal("markCurrent did not find projects/plan.md")
	}

	data := fileData{
		baseData: baseData{User: "alice", Viewer: "alice", FileView: true},
		Repo:     "brain", Path: "projects/plan.md", Name: "plan.md",
		IsText: true, RawText: "body", Tree: tree,
		Backlinks: []backlinkView{{Name: "projects/source.md", Desc: "Source note", Href: "/alice/brain/blob/projects/source.md"}},
		History:   []commitView{{Short: "abc1234", Subject: "Update the plan", When: "today"}},
	}
	var buf bytes.Buffer
	if err := parsePages()["file"].ExecuteTemplate(&buf, "base", data); err != nil {
		t.Fatalf("render file page: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		`class="sidetree"`,                          // the side panel exists
		`class="filelayout file-workspace"`,         // the app-style three-plane shell exists
		`data-workspace-resizer="tree"`,             // the file list can be resized
		`data-workspace-resizer="context"`,          // note context can be resized
		`role="separator"`,                          // resize handles are keyboard-accessible
		`class="note-context"`,                      // backlinks and history sit beside the note
		`class="file-shell"`,                        // file-view-only page theming is active
		`node-name current`,                         // current file highlighted
		`href="/alice/brain/blob/projects/plan.md"`, // links into the repo
		`href="/alice/brain/blob/NOTE.md"`,          // sibling note is listed too
		`projects/source.md`,                        // backlink context is rendered
		`Update the plan`,                           // file history is rendered
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered file page missing %q", want)
		}
	}
	// Exactly one node is the current one.
	if n := strings.Count(out, "node-name current"); n != 1 {
		t.Errorf("expected exactly 1 current node, got %d", n)
	}
}

func TestFileViewSidebarKeepsFooterOutsideScrollingTree(t *testing.T) {
	style, err := assetsFS.ReadFile("assets/style.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(style)
	for selector, wants := range map[string][]string{
		".file-workspace .sidetree":       {"display: grid", "grid-template-rows: auto auto minmax(0, 1fr) auto", "overflow: hidden"},
		".file-workspace .sidetree .tree": {"min-height: 0", "overflow: auto"},
		".file-workspace .sidetree-foot":  {"position: static"},
	} {
		start := strings.Index(css, selector+" {")
		if start < 0 {
			t.Fatalf("style.css missing %s", selector)
		}
		end := strings.IndexByte(css[start:], '}')
		if end < 0 {
			t.Fatalf("style.css has unterminated %s rule", selector)
		}
		rule := css[start : start+end]
		for _, want := range wants {
			if !strings.Contains(rule, want) {
				t.Errorf("%s rule missing %q: %s", selector, want, rule)
			}
		}
	}
}

func TestFileBrowserPrioritizesCompleteNames(t *testing.T) {
	styleAsset, err := assetsFS.ReadFile("assets/style.css")
	if err != nil {
		t.Fatal(err)
	}
	baseAsset, err := assetsFS.ReadFile("assets/base.html")
	if err != nil {
		t.Fatal(err)
	}
	repoAsset, err := assetsFS.ReadFile("assets/repo.html")
	if err != nil {
		t.Fatal(err)
	}
	scriptAsset, err := assetsFS.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}

	style, base, repo, script := string(styleAsset), string(baseAsset), string(repoAsset), string(scriptAsset)
	for _, want := range []string{
		`.repo-file-browser {`,
		`--file-name-column: clamp(21rem, 38vw, 34rem)`,
		`.tree-columns,`,
		`grid-template-columns: minmax(0, var(--file-name-column)) minmax(9rem, 1fr) 6.5rem`,
		`.repo-file-browser .node-desc {`,
		`overflow: hidden; text-overflow: ellipsis; white-space: nowrap`,
		`.file-workspace .node-name {`,
		`white-space: normal; overflow-wrap: anywhere`,
	} {
		if !strings.Contains(style, want) {
			t.Errorf("filesystem browser CSS missing %q", want)
		}
	}
	for _, want := range []string{
		`class="node-main"`,
		`class="node-icon node-icon-folder"`,
		`class="node-icon node-icon-file"`,
		`aria-expanded="true"`,
		`style="--tree-depth: {{.Depth}}"`,
	} {
		if !strings.Contains(base, want) {
			t.Errorf("tree template missing %q", want)
		}
	}
	for _, want := range []string{`class="repo-file-browser"`, `class="tree-columns"`, `<span>Name</span><span>Description</span><span>Modified</span>`} {
		if !strings.Contains(repo, want) {
			t.Errorf("repository file browser missing %q", want)
		}
	}
	for _, want := range []string{`function setDirectoryCollapsed(li, collapsed)`, `caret.setAttribute("aria-expanded", collapsed ? "false" : "true")`} {
		if !strings.Contains(script, want) {
			t.Errorf("folder disclosure script missing %q", want)
		}
	}
}

func TestFileViewMobileNavigationIsUsable(t *testing.T) {
	styleAsset, err := assetsFS.ReadFile("assets/style.css")
	if err != nil {
		t.Fatal(err)
	}
	scriptAsset, err := assetsFS.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	baseAsset, err := assetsFS.ReadFile("assets/base.html")
	if err != nil {
		t.Fatal(err)
	}
	style, script, base := string(styleAsset), string(scriptAsset), string(baseAsset)

	for _, want := range []string{
		":is(.file-workspace, .shared-shell) .prose a {",
		"hanging-punctuation: first last;\n  overflow-wrap: anywhere; word-break: break-word",
		"overflow-wrap: anywhere; word-break: break-word",
		"position: fixed; z-index: 9",
		"width: min(86vw, 340px)",
		"min-width: 44px; min-height: 44px",
		".file-workspace .sidetree-close",
		"html:not(.tree-hidden) body.file-shell::before",
		".masthead .agent-trigger { min-height: 44px; }",
		".repo-download,",
		".graph-tool { height: 44px; }",
	} {
		if !strings.Contains(style, want) {
			t.Errorf("mobile file workspace CSS missing %q", want)
		}
	}
	for _, want := range []string{
		`var MOBILE_FILE_QUERY = "(max-width: 760px)"`,
		`mobileFileMedia.addEventListener("change"`,
		`e.target.closest("[data-tree-close]")`,
		"setTreeHidden(true, false)",
		`button.setAttribute("aria-expanded", expanded ? "true" : "false")`,
		`button.setAttribute("aria-label", expanded ? "Hide file list" : "Show file list")`,
		`setTreeHidden(saved === "1", false)`,
		`if (treeClose) treeClose.focus()`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("mobile file workspace script missing %q", want)
		}
	}
	if !strings.Contains(base, `window.matchMedia('(max-width: 760px)').matches`) {
		t.Error("first paint does not start with the mobile file tree closed")
	}
}
