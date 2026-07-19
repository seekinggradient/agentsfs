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

// TestSearchBlankQueryNeverErrors is the Finding-2 regression: a query with no
// FTS terms (empty or all-whitespace) once produced `MATCH ”`, an fts5 syntax
// error surfaced raw by the CLI. It must now degrade gracefully — no error, and
// the structural seeds still flow — for both "" and "   ".
func TestSearchBlankQueryNeverErrors(t *testing.T) {
	root := copyFixtureInstance(t, "insurance-claim")
	for _, q := range []string{"", "   "} {
		results, err := Search(root, q, 10)
		if err != nil {
			t.Fatalf("Search(%q) errored (want graceful no-match/seeds): %v", q, err)
		}
		// Whatever surfaces is a structural/link seed, never a body FTS hit (there
		// were no terms), and the call must not blow up.
		_ = results
	}
	// The context depth shares the same candidate generator, so it must be safe too.
	if _, err := SearchContext(root, "   ", 0); err != nil {
		t.Fatalf("SearchContext(blank) errored: %v", err)
	}
}

// TestContextPackTinyBudgetNoEmptyDoc is the Finding-3 regression: a budget too
// small for even a header plus a truncated body once emitted a doc with empty
// content (and counted its header tokens). The pack must instead skip such a doc
// and, if that empties it, be honestly empty.
func TestContextPackTinyBudgetNoEmptyDoc(t *testing.T) {
	root := copyFixtureInstance(t, "insurance-claim")
	pack, err := SearchContext(root, "what is the status of the insurance claim", 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range pack.Docs {
		if strings.TrimSpace(d.Content) == "" {
			t.Fatalf("pack emitted a contentless doc: %+v", d)
		}
	}
	// A budget of 1 cannot fit any header, so the honest answer is an empty pack
	// with zero budget consumed — not a header-only doc that overshoots.
	if len(pack.Docs) != 0 {
		t.Fatalf("budget 1 fit %d docs, want 0 (nothing fits a 1-token budget)", len(pack.Docs))
	}
	if pack.BudgetUsedEstTokens != 0 {
		t.Fatalf("empty pack reported %d est tokens, want 0", pack.BudgetUsedEstTokens)
	}
	if len(pack.Pointers) != len(pack.Docs) {
		t.Fatalf("pointers=%d docs=%d, want equal", len(pack.Pointers), len(pack.Docs))
	}
}

// TestDescriptionSectionIsBodyHit is the Finding-4 regression: a literal
// `## description` section and the synthetic frontmatter-description row once
// shared the heading "description", so a body match on the section was
// misclassified as a description signal and hydrated the intro instead of the
// section. With the sentinel, the section ranks and hydrates as a BODY hit while
// frontmatter descriptions still rank via the description signal.
func TestDescriptionSectionIsBodyHit(t *testing.T) {
	root := newInstance(t, map[string]string{
		"notes/INDEX.md": "---\ndescription: Notes.\n---\n",
		"notes/spec.md": "---\ndescription: frontmattertoken summary\n---\n# Spec\n\n" +
			"## description\n\nThe bodytoken lives in the description section body.\n",
	})

	// A body-token query ranks spec.md first as a BODY hit carrying the real
	// section heading "description" (not swallowed by the description signal).
	res, err := Search(root, "bodytoken", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 || res[0].Path != "notes/spec.md" {
		t.Fatalf("bodytoken should rank spec.md first: %+v", res)
	}
	if res[0].Heading != "description" {
		t.Fatalf("literal ## description body hit should carry heading %q, got %q", "description", res[0].Heading)
	}

	// The classification is the observable of the bug: under the old collision the
	// section matched as the *description* signal. It must now be a *body* hit.
	pack, err := SearchContext(root, "bodytoken", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Docs) == 0 || pack.Docs[0].Path != "notes/spec.md" {
		t.Fatalf("pack top doc should be spec.md: %+v", pack.Docs)
	}
	if !strings.Contains(pack.Docs[0].Reason, "body fts") {
		t.Fatalf("literal ## description match must be a body hit, reason=%q", pack.Docs[0].Reason)
	}
	if strings.Contains(pack.Docs[0].Reason, "description") {
		t.Fatalf("literal ## description body match must not be the description signal, reason=%q", pack.Docs[0].Reason)
	}
	if !strings.Contains(pack.Docs[0].Content, "bodytoken lives in the description section") {
		t.Fatalf("pack should hydrate the ## description section body: %+v", pack.Docs)
	}

	// The frontmatter description still ranks the file — via the description
	// signal — on its own distinct token.
	fres, err := Search(root, "frontmattertoken", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(fres) == 0 || fres[0].Path != "notes/spec.md" {
		t.Fatalf("frontmattertoken should rank spec.md via the description signal: %+v", fres)
	}
	fpack, err := SearchContext(root, "frontmattertoken", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(fpack.Docs) == 0 || !strings.Contains(fpack.Docs[0].Reason, "description") {
		t.Fatalf("frontmattertoken should still fire the description signal: %+v", fpack.Docs)
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
