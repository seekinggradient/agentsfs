package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newInstance builds a throwaway instance on disk for tests.
func newInstance(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	base := map[string]string{
		"AGENTS.md": "---\ndescription: Test instance root.\n---\n# root\n",
	}
	for k, v := range files {
		base[k] = v
	}
	for rel, content := range base {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDescription(t *testing.T) {
	root := newInstance(t, map[string]string{
		"a.md": "---\ndescription: \"Quoted description.\"\nother: x\n---\nbody",
		"b.md": "# no frontmatter\n",
		"c.md": "---\nsources:\n  - x\n---\nbody",
	})
	if got := Description(filepath.Join(root, "a.md")); got != "Quoted description." {
		t.Errorf("a.md description = %q", got)
	}
	if got := Description(filepath.Join(root, "b.md")); got != "" {
		t.Errorf("b.md description = %q, want empty", got)
	}
	if got := Description(filepath.Join(root, "c.md")); got != "" {
		t.Errorf("c.md description = %q, want empty", got)
	}
}

func TestLinkResolution(t *testing.T) {
	root := newInstance(t, map[string]string{
		"reference/Granite Mutual.md": "---\ndescription: d\n---\n",
		"work/Apple.md":               "---\ndescription: d\n---\n",
		"home/Apple.md":               "---\ndescription: d\n---\n",
		"docs/report.pdf":             "pdf-bytes",
	})
	idx, err := BuildNameIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		target string
		want   int
	}{
		{"Granite Mutual", 1},
		{"granite mutual", 1}, // case-insensitive
		{"Apple", 2},          // ambiguous
		{"work/Apple", 1},     // path suffix disambiguates
		{"report.pdf", 1},     // non-markdown keeps its extension
		{"Nonexistent", 0},
	}
	for _, c := range cases {
		if got := len(idx.Resolve(c.target)); got != c.want {
			t.Errorf("Resolve(%q) = %d matches, want %d", c.target, got, c.want)
		}
	}
}

func TestBacklinksSkipsContractExamples(t *testing.T) {
	root := newInstance(t, map[string]string{
		"AGENTS.md":  "---\ndescription: Root.\n---\nExample: [[Apple]]\n",
		"a.md":       "---\ndescription: d\n---\nSee [[Apple]].\n",
		"b/Apple.md": "---\ndescription: d\n---\n",
		"b/INDEX.md": "---\ndescription: d\n---\n",
	})
	links, err := Backlinks(root, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].Source != "a.md" {
		t.Errorf("Backlinks = %+v, want exactly one from a.md", links)
	}
}

func TestDoctor(t *testing.T) {
	root := newInstance(t, map[string]string{
		"notes/INDEX.md":   "---\ndescription: Notes dir; holds scan.pdf, the claim scan.\n---\n",
		"notes/good.md":    "---\ndescription: A fine note linking [[good]] nowhere bad.\n---\n" + strings.Repeat("dense content. ", 20),
		"notes/nodesc.md":  "# missing frontmatter\n" + strings.Repeat("words ", 50),
		"notes/stub.md":    "---\ndescription: Tiny.\n---\nx\n",
		"notes/dead.md":    "---\ndescription: Has a dead link.\n---\nSee [[DoesNotExist]] " + strings.Repeat("pad ", 40),
		"notes/scan.pdf":   "bytes",
		"notes/loose.bin":  "bytes",
		"bare/file.md":     "---\ndescription: In a dir with no INDEX.\n---\n" + strings.Repeat("pad ", 40),
		"scratch/INDEX.md": "---\ndescription: Scratch.\n---\n",
		"scratch/mess.md":  "no frontmatter, no problem [[Whatever]]\n",
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, f := range findings {
		got[f.Code+":"+f.Path]++
	}
	for _, want := range []string{
		"missing-index:bare",
		"missing-description:notes/nodesc.md",
		"stub:notes/stub.md",
		"dead-link:notes/dead.md",
		"undescribed-file:notes/loose.bin",
	} {
		if got[want] == 0 {
			t.Errorf("missing expected finding %s in %+v", want, findings)
		}
	}
	for path := range got {
		if strings.Contains(path, "scratch/") {
			t.Errorf("scratch should be exempt, got %s", path)
		}
		if strings.Contains(path, "scan.pdf") {
			t.Errorf("scan.pdf is mentioned in INDEX.md, should not be flagged")
		}
	}
}

func TestRenameRewritesLinks(t *testing.T) {
	root := newInstance(t, map[string]string{
		"reference/INDEX.md": "---\ndescription: d\n---\n",
		"reference/Acme.md":  "---\ndescription: d\n---\n",
		"notes/INDEX.md":     "---\ndescription: d\n---\n",
		"notes/one.md":       "---\ndescription: d\n---\nSee [[Acme]] and [[Acme|the insurer]].\n",
		"notes/two.md":       "---\ndescription: d\n---\n[[reference/Acme]] too.\n",
	})
	res, err := Rename(root, "reference/Acme.md", "Acme Insurance")
	if err != nil {
		t.Fatal(err)
	}
	if res.NewRel != "reference/Acme Insurance.md" {
		t.Errorf("NewRel = %q", res.NewRel)
	}
	if res.LinksRewrote != 3 {
		t.Errorf("LinksRewrote = %d, want 3", res.LinksRewrote)
	}
	one, _ := os.ReadFile(filepath.Join(root, "notes", "one.md"))
	if !strings.Contains(string(one), "[[Acme Insurance]]") || !strings.Contains(string(one), "[[Acme Insurance|the insurer]]") {
		t.Errorf("one.md not rewritten: %s", one)
	}
	two, _ := os.ReadFile(filepath.Join(root, "notes", "two.md"))
	if !strings.Contains(string(two), "[[Acme Insurance]]") {
		t.Errorf("two.md not rewritten: %s", two)
	}
	if fileExists(filepath.Join(root, "reference", "Acme.md")) {
		t.Error("old file still exists")
	}
}

func TestFindRoot(t *testing.T) {
	root := newInstance(t, map[string]string{"deep/dir/INDEX.md": "---\ndescription: d\n---\n"})
	got, err := FindRoot(filepath.Join(root, "deep", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	// t.TempDir may itself sit under symlinked /var → /private/var; compare resolved.
	wantR, _ := filepath.EvalSymlinks(root)
	gotR, _ := filepath.EvalSymlinks(got)
	if gotR != wantR {
		t.Errorf("FindRoot = %q, want %q", gotR, wantR)
	}
}
