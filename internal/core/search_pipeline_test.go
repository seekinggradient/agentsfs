package core

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyFixtureInstance copies a committed fixture KB into a fresh temp dir so
// tests exercise the real content without writing a derived .agentsfs/index.db
// back into the checked-in fixture. The machine-territory .agentsfs/ and any
// .git are skipped and a clean empty .agentsfs/ is recreated, so the pipeline
// reindexes from scratch (and, notably, under the current tokenizer).
func copyFixtureInstance(t *testing.T, fixture string) string {
	t.Helper()
	src := filepath.Join("..", "..", "fixtures", fixture)
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("fixture %s not found: %v", src, err)
	}
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Skip derived index state and git plumbing; regenerate the former.
		top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		if top == ".agentsfs" || top == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dst
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// rankOf returns the 1-based rank of path in results, or 0 if absent.
func rankOf(results []SearchResult, path string) int {
	for i, r := range results {
		if r.Path == path {
			return i + 1
		}
	}
	return 0
}

const statusDoc = "projects/water-damage-claim/status.md"

// TestRetrievalEval is the gate for R2/R3: the natural-language queries that
// returned zero hits before the pipeline must now rank the right document, and
// the queries that already worked must not regress. Weights (pipeline.go) are
// tuned against exactly this set.
func TestRetrievalEval(t *testing.T) {
	root := copyFixtureInstance(t, "insurance-claim")

	cases := []struct {
		query  string
		want   string
		maxRnk int // required rank must be ≤ this (1 = must be #1)
	}{
		{"what is the status of the insurance claim", statusDoc, 1},
		{"what is the plan for disputing the estimate", statusDoc, 2},
		{"how much money are we asking for", statusDoc, 3},
		{"dispute plan", statusDoc, 3},
		{"deductible", "reference/Granite Mutual.md", 1},
		{"adjuster estimate", "journal/2026-06-10-drafted-estimate-dispute.md", 3},
	}
	for _, tc := range cases {
		results, err := Search(root, tc.query, 10)
		if err != nil {
			t.Fatalf("Search(%q): %v", tc.query, err)
		}
		got := rankOf(results, tc.want)
		if got == 0 || got > tc.maxRnk {
			t.Errorf("query %q: %s ranked %d, want ≤ %d\n%s",
				tc.query, tc.want, got, tc.maxRnk, dumpRanks(results))
		}
	}
}

// TestContractFilesNeverOutrankContent: AGENTS.md/CLAUDE.md are instructions
// for agents, not knowledge content — a content question must rank the real
// answer above them (contractDemotion in pipeline.go). They remain
// discoverable, just demoted.
func TestContractFilesNeverOutrankContent(t *testing.T) {
	root := copyFixtureInstance(t, "insurance-claim")
	for _, q := range []string{
		"how much money are we asking for",
		"what is the status of the insurance claim",
	} {
		results, err := Search(root, q, 10)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		status, agents := rankOf(results, statusDoc), rankOf(results, "AGENTS.md")
		if agents != 0 && (status == 0 || agents < status) {
			t.Errorf("query %q: AGENTS.md (rank %d) outranks %s (rank %d)\n%s",
				q, agents, statusDoc, status, dumpRanks(results))
		}
	}
}

func dumpRanks(results []SearchResult) string {
	var b strings.Builder
	for i, r := range results {
		b.WriteString("  #")
		b.WriteByte(byte('1' + i))
		b.WriteString(" ")
		b.WriteString(r.Path)
		if r.Heading != "" {
			b.WriteString(" § ")
			b.WriteString(r.Heading)
		}
		b.WriteString("\n")
		if i >= 8 {
			break
		}
	}
	return b.String()
}

// TestContextPackHydratesFullAnswer verifies --context returns the full text of
// the answer (not just a locating snippet): status.md's whole "Next actions"
// section, including the dated deadline, must be present in the top document.
func TestContextPackHydratesFullAnswer(t *testing.T) {
	root := copyFixtureInstance(t, "insurance-claim")
	pack, err := SearchContext(root, "what is the status of the insurance claim", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Docs) == 0 {
		t.Fatal("empty context pack")
	}
	if pack.Docs[0].Path != statusDoc {
		t.Fatalf("top context doc = %s, want %s", pack.Docs[0].Path, statusDoc)
	}
	content := pack.Docs[0].Content
	for _, want := range []string{
		"## Next actions",
		"By 2026-06-20",
		"send written dispute of the estimate",
		"Request the itemized line-item breakdown",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("top doc content missing %q\n--- content ---\n%s", want, content)
		}
	}
	// The provenance list names every included doc.
	if len(pack.Pointers) != len(pack.Docs) {
		t.Errorf("pointers=%d docs=%d, want equal", len(pack.Pointers), len(pack.Docs))
	}
	if pack.Pointers[0] != statusDoc {
		t.Errorf("first pointer = %s, want %s", pack.Pointers[0], statusDoc)
	}
	// The plain rendering must actually contain the hydrated answer too.
	if !strings.Contains(RenderContextPack(pack), "send written dispute of the estimate") {
		t.Error("rendered pack does not contain the hydrated Next actions text")
	}
}

// TestContextPackRespectsBudget checks the estimated-token budget is honored
// within a small tolerance and that a tight budget still returns something.
func TestContextPackRespectsBudget(t *testing.T) {
	root := copyFixtureInstance(t, "insurance-claim")
	const budget = 400
	pack, err := SearchContext(root, "what is the status of the insurance claim", budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Docs) == 0 {
		t.Fatal("tight budget returned nothing; the top hit should be truncated to fit")
	}
	if pack.BudgetUsedEstTokens > budget*12/10 {
		t.Errorf("budget used %d exceeds %d + 20%%", pack.BudgetUsedEstTokens, budget)
	}
	// A generous budget should fit at least the whole top document.
	big, err := SearchContext(root, "what is the status of the insurance claim", 40000)
	if err != nil {
		t.Fatal(err)
	}
	if big.BudgetUsedEstTokens <= pack.BudgetUsedEstTokens {
		t.Errorf("larger budget did not hydrate more (%d ≤ %d)", big.BudgetUsedEstTokens, pack.BudgetUsedEstTokens)
	}
}

// TestContextPackJSON verifies the structured pack round-trips through JSON with
// the documented field names, so --json consumers can decode it.
func TestContextPackJSON(t *testing.T) {
	root := copyFixtureInstance(t, "insurance-claim")
	pack, err := SearchContext(root, "dispute plan", 0)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(blob, &generic); err != nil {
		t.Fatalf("pack JSON did not parse: %v", err)
	}
	for _, key := range []string{"docs", "budget_used_est_tokens", "pointers"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("pack JSON missing key %q; got %s", key, blob)
		}
	}
	// And the strongly-typed shape decodes back.
	var round ContextPack
	if err := json.Unmarshal(blob, &round); err != nil {
		t.Fatalf("pack JSON did not decode into ContextPack: %v", err)
	}
	if len(round.Docs) > 0 && round.Docs[0].Path == "" {
		t.Error("decoded doc has empty path")
	}
}
