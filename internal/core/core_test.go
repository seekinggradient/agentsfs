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
		"AGENTS.md": "---\ndescription: Test instance root.\nagentsfs_contract: 0.2.0\n---\n# root\n",
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

func TestContractVersion(t *testing.T) {
	root := newInstance(t, nil)
	if got := ContractVersion(root); got != CurrentContractVersion() {
		t.Fatalf("ContractVersion = %q, want %q", got, CurrentContractVersion())
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
		"AGENTS.md":  "---\ndescription: Root.\nagentsfs_contract: 0.2.0\n---\nExample: [[Apple]]\n",
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

func TestDoctorFlagsOldContract(t *testing.T) {
	root := newInstance(t, map[string]string{
		"AGENTS.md": "---\ndescription: Test instance root.\nagentsfs_contract: 0.1.0\n---\n# root\n",
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Code == "contract-version" && f.Path == "AGENTS.md" {
			return
		}
	}
	t.Fatalf("Doctor did not flag old contract: %+v", findings)
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

// Finding 5 regression: rename must not rewrite quoted links — same
// quotation semantics as the scanner.
func TestRenameLeavesQuotedLinksAlone(t *testing.T) {
	root := newInstance(t, map[string]string{
		"reference/INDEX.md": "---\ndescription: d\n---\n",
		"reference/Acme.md":  "---\ndescription: d\n---\n",
		"notes/INDEX.md":     "---\ndescription: d\n---\n",
		"notes/mixed.md": "---\ndescription: d\n---\n" +
			"Real link: [[Acme]] but quoted `[[Acme]]` stays.\n" +
			"```\n[[Acme]] inside a fence also stays\n```\n",
	})
	res, err := Rename(root, "reference/Acme.md", "Acme Corp")
	if err != nil {
		t.Fatal(err)
	}
	if res.LinksRewrote != 1 {
		t.Errorf("LinksRewrote = %d, want exactly 1 (the prose link)", res.LinksRewrote)
	}
	data, _ := os.ReadFile(filepath.Join(root, "notes", "mixed.md"))
	got := string(data)
	for _, want := range []string{
		"Real link: [[Acme Corp]]",
		"quoted `[[Acme]]` stays",
		"[[Acme]] inside a fence also stays",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("mixed.md missing %q:\n%s", want, got)
		}
	}
}

// Doctor nit regression: short-named binaries aren't "mentioned" by a
// coincidental letter sequence in INDEX prose.
func TestDoctorWholeWordIndexMention(t *testing.T) {
	root := newInstance(t, map[string]string{
		"data/INDEX.md": "---\ndescription: Holds extra exports.\n---\nThe `dump.bin` file is the raw export.\n",
		"data/x":        "bytes", // "extra" contains "x" — must still be flagged
		"data/dump.bin": "bytes",
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	flagged := map[string]bool{}
	for _, f := range findings {
		if f.Code == "undescribed-file" {
			flagged[f.Path] = true
		}
	}
	if !flagged["data/x"] {
		t.Error("data/x false-passed via substring match")
	}
	if flagged["data/dump.bin"] {
		t.Error("data/dump.bin is genuinely mentioned, should not be flagged")
	}
}

func TestTreeScopeAndDepth(t *testing.T) {
	root := newInstance(t, map[string]string{
		"a.md":                        "---\ndescription: Top file.\n---\n",
		"memory/INDEX.md":             "---\ndescription: Memory area.\n---\n",
		"memory/notes.md":             "---\ndescription: Loose notes.\n---\n",
		"memory/projects/INDEX.md":    "---\ndescription: Projects.\n---\n",
		"memory/projects/alpha.md":    "---\ndescription: Alpha project.\n---\n",
		"memory/projects/sub/deep.md": "---\ndescription: Deep file.\n---\n",
	})

	full, err := Tree(root, ".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a.md", "memory/", "projects/", "alpha.md", "deep.md"} {
		if !strings.Contains(full, want) {
			t.Errorf("full tree missing %q:\n%s", want, full)
		}
	}

	scoped, err := Tree(root, "memory/projects", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(scoped, "memory/projects — Projects.") {
		t.Errorf("scoped root line wrong:\n%s", scoped)
	}
	if !strings.Contains(scoped, "alpha.md") || !strings.Contains(scoped, "deep.md") {
		t.Errorf("scoped tree missing subtree entries:\n%s", scoped)
	}
	// Descriptions are unique per file, so they're a precise leak check
	// ("a.md" would match "alpha.md" as a substring).
	for _, unwanted := range []string{"Top file.", "Loose notes."} {
		if strings.Contains(scoped, unwanted) {
			t.Errorf("scoped tree leaked %q outside scope:\n%s", unwanted, scoped)
		}
	}

	shallow, err := Tree(root, ".", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shallow, "Top file.") {
		t.Errorf("depth-1 tree missing top-level file:\n%s", shallow)
	}
	if !strings.Contains(shallow, "memory/ — Memory area. …") {
		t.Errorf("depth-1 tree should mark memory/ as having hidden children:\n%s", shallow)
	}
	if strings.Contains(shallow, "Loose notes.") || strings.Contains(shallow, "projects/") {
		t.Errorf("depth-1 tree should not descend into memory/:\n%s", shallow)
	}

	if _, err := Tree(root, "does/not/exist", 0); err == nil {
		t.Error("scoping to a missing directory should error")
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
