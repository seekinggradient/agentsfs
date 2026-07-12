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
