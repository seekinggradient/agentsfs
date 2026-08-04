package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An explicitly supplied symlink is an intentional instance selection, unlike
// an incidental symlink found during a broad status scan. Every content command
// must therefore operate on its canonical target rather than treating the link
// itself as a zero-entry filesystem root.
func TestExplicitSymlinkInstanceCommandParity(t *testing.T) {
	home := t.TempDir()
	parent := t.TempDir()
	root := filepath.Join(parent, "actual")
	if out, err := runAFS(t, parent, home, "init", root, "--yes"); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	mustWriteFile(t, filepath.Join(root, "Alpha.md"), "---\ndescription: Alpha validation note.\n---\n\n# Alpha\n\nLinks to [[Beta]].\n")
	mustWriteFile(t, filepath.Join(root, "Beta.md"), "---\ndescription: Beta validation note.\n---\n\n# Beta\n\nUnique symlink probe content.\n")
	link := filepath.Join(parent, "brain")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "tree", args: []string{"tree", link}, want: "Beta validation note."},
		{name: "scoped tree", args: []string{"tree", filepath.Join(link, "agent-journal")}, want: "agent-journal"},
		{name: "search", args: []string{"search", "symlink probe", link, "--json"}, want: `"path": "Beta.md"`},
		{name: "roles", args: []string{"roles", link, "--json"}, want: `"journal": "agent-journal"`},
		{name: "doctor", args: []string{"doctor", link, "--json"}, want: `"path": "Alpha.md"`},
		{name: "backlinks", args: []string{"backlinks", "Beta", link}, want: "Alpha.md"},
		{name: "contract", args: []string{"contract", "status", link}, want: "contract is"},
		{name: "reindex", args: []string{"reindex", link}, want: "full-text index rebuilt:"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runAFS(t, parent, home, tc.args...)
			if err != nil {
				t.Fatalf("%s through symlink failed: %v\n%s", tc.name, err, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("%s through symlink omitted %q:\n%s", tc.name, tc.want, out)
			}
			if tc.name == "reindex" && strings.Contains(out, "rebuilt: 0 chunks") {
				t.Fatalf("reindex treated symlink as an empty root:\n%s", out)
			}
		})
	}

	t.Run("status canonical identity", func(t *testing.T) {
		out, err := runAFS(t, parent, home, "status", link, "--json")
		if err != nil {
			t.Fatalf("status through symlink failed: %v\n%s", err, out)
		}
		var report struct {
			Instances []struct {
				Path string `json:"path"`
			} `json:"instances"`
		}
		if err := json.NewDecoder(strings.NewReader(out)).Decode(&report); err != nil {
			t.Fatalf("status JSON: %v\n%s", err, out)
		}
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Instances) != 1 || report.Instances[0].Path != canonical {
			t.Fatalf("status identity = %+v, want %q", report.Instances, canonical)
		}
	})

	t.Run("rename rewrites backlinks", func(t *testing.T) {
		out, err := runAFS(t, parent, home, "rename", "Beta.md", "Gamma.md", link)
		if err != nil {
			t.Fatalf("rename through symlink failed: %v\n%s", err, out)
		}
		alpha, err := os.ReadFile(filepath.Join(root, "Alpha.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(alpha), "[[Gamma]]") || strings.Contains(string(alpha), "[[Beta]]") {
			t.Fatalf("rename did not rewrite backlink through symlink:\n%s\n%s", out, alpha)
		}
		if _, err := os.Stat(filepath.Join(root, "Gamma.md")); err != nil {
			t.Fatalf("renamed file missing: %v", err)
		}
	})
}
