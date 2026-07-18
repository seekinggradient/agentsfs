package hub

import (
	"bytes"
	"strings"
	"testing"
)

// The listing description comes from the root INDEX.md when it holds a real
// per-KB description (contract 0.7.0+); it falls back to AGENTS.md then
// README.md for older instances, and never surfaces a template placeholder or
// the pre-0.7.0 contract boilerplate — that boilerplate was why every repo
// used to show the same label.
func TestRepoFilesMetaDescriptionPrecedence(t *testing.T) {
	const placeholder = "REPLACE ME: one or two sentences describing what THIS knowledge base is about and what lives in it."
	const boilerplate = "Self-describing root of this agentsfs. Read this first — it teaches any agent how to read, write, and maintain everything here."
	cases := []struct {
		name  string
		files []RepoFile
		want  string
	}{
		{
			name: "root INDEX.md wins over AGENTS.md",
			files: []RepoFile{
				{Path: "INDEX.md", Description: "Kitchen remodel planning."},
				{Path: "AGENTS.md", Description: boilerplate},
			},
			want: "Kitchen remodel planning.",
		},
		{
			name: "placeholder INDEX.md falls back to a real AGENTS.md",
			files: []RepoFile{
				{Path: "INDEX.md", Description: placeholder},
				{Path: "AGENTS.md", Description: "Team memory for the payments service."},
			},
			want: "Team memory for the payments service.",
		},
		{
			name: "no INDEX.md falls back to AGENTS.md",
			files: []RepoFile{
				{Path: "AGENTS.md", Description: "Personal research vault."},
			},
			want: "Personal research vault.",
		},
		{
			name: "boilerplate everywhere surfaces nothing",
			files: []RepoFile{
				{Path: "INDEX.md", Description: placeholder},
				{Path: "AGENTS.md", Description: boilerplate},
			},
			want: "",
		},
		{
			name: "README.md is the last fallback",
			files: []RepoFile{
				{Path: "AGENTS.md", Description: boilerplate},
				{Path: "README.md", Description: "Human intro to the vault."},
			},
			want: "Human intro to the vault.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, _ := repoFilesMeta(c.files)
			if got != c.want {
				t.Errorf("repoFilesMeta description = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRepoFileRowsPrepareSortableMetadata(t *testing.T) {
	rows := repoFileRows([]RepoFile{
		{Path: "projects/claim/status.md", Description: "Current [[claim|claim state]].", LastCommit: 1700000000},
		{Path: "media/inspection.png", LastCommit: 1699900000},
		{Path: "AGENTS.md", Description: "Agent instructions.", LastCommit: 1699800000},
		{Path: ".gitattributes", LastCommit: 1699700000},
	}, "alice", "insurance-claim")

	if len(rows) != 4 {
		t.Fatalf("len(rows) = %d, want 4", len(rows))
	}
	if got := rows[0]; got.Name != "status.md" || got.Folder != "projects/claim" || got.Type != "Markdown" || got.UpdatedUnix != 1700000000 || got.Href != "/alice/insurance-claim/blob/projects/claim/status.md" || got.DownloadHref != "/alice/insurance-claim/download/projects/claim/status.md?format=original" {
		t.Errorf("markdown row = %+v", got)
	}
	if rows[0].Description != "Current claim state." {
		t.Errorf("clean description = %q", rows[0].Description)
	}
	if rows[1].Type != "Image" {
		t.Errorf("image type = %q", rows[1].Type)
	}
	if rows[2].Folder != "Root" {
		t.Errorf("root folder = %q", rows[2].Folder)
	}
	if rows[3].Type != "File" {
		t.Errorf("dotfile type = %q", rows[3].Type)
	}
}

func TestRepoTemplateIncludesSortableFileTable(t *testing.T) {
	data := repoData{
		baseData:     baseData{User: "alice", Viewer: "alice"},
		Repo:         "insurance-claim",
		DisplayName:  "Insurance claim",
		CloneCmd:     "git clone https://hub.example/alice/insurance-claim.git",
		DownloadHref: "/alice/insurance-claim/download",
		Root:         &treeNode{IsDir: true},
		Files: []repoFileRow{{
			Name: "status.md", Path: "projects/claim/status.md", Folder: "projects/claim",
			Description: "Current claim state.", Age: "1h ago", UpdatedUnix: 1700000000,
			Href: "/alice/insurance-claim/blob/projects/claim/status.md", DownloadHref: "/alice/insurance-claim/download/projects/claim/status.md?format=original", Type: "Markdown",
		}},
		GraphNodes: 1,
	}

	var buf bytes.Buffer
	if err := parsePages()["repo"].ExecuteTemplate(&buf, "base", data); err != nil {
		t.Fatalf("render repo: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`data-repo-tab="table"`,
		`class="repo-file-table"`,
		`data-file-table-sort`,
		`data-file-sort-key="updated"`,
		`data-sort-updated="1700000000"`,
		`data-sort-folder="projects/claim"`,
		`data-sort-type="Markdown"`,
		`data-file-table-search`,
		`projects/claim/status.md`,
		`href="/alice/insurance-claim/download/projects/claim/status.md?format=original"`,
		`class="repo-download" href="/alice/insurance-claim/download"`,
		`Download repository`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered repo missing %q", want)
		}
	}
}
