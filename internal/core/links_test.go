package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A wikilink's resolvable target is only part of what is written: Obsidian's
// grammar is [[target#anchor|alias]]. Scanning must hand back the target for
// resolution while preserving the anchor and alias so rename can put them back.
func TestScanLinksSplitsAnchorAndAlias(t *testing.T) {
	cases := []struct {
		in                    string
		target, anchor, alias string
	}{
		{"Note", "Note", "", ""},
		{"Note#Section Two", "Note", "Section Two", ""},
		{"Note|display", "Note", "", "display"},
		{"Note#Section Two|display", "Note", "Section Two", "display"},
		{"work/Apple#Q3", "work/Apple", "Q3", ""},
		// The alias splits first, so a '#' living inside the alias text stays
		// part of the alias rather than being mistaken for an anchor.
		{"Note|a #1 pick", "Note", "", "a #1 pick"},
		{"  Note  ", "Note", "", ""},
	}
	for _, c := range cases {
		links := ScanLinksIn("f.md", "[["+c.in+"]]\n")
		if len(links) != 1 {
			t.Fatalf("[[%s]]: expected 1 link, got %d", c.in, len(links))
		}
		got := links[0]
		if got.Target != c.target || got.Anchor != c.anchor || got.Alias != c.alias {
			t.Errorf("[[%s]]: got target=%q anchor=%q alias=%q, want target=%q anchor=%q alias=%q",
				c.in, got.Target, got.Anchor, got.Alias, c.target, c.anchor, c.alias)
		}
	}
}

// An anchored link points at a real file and must not be reported dead — the
// section suffix addresses a place inside the note, not a different note.
func TestDoctorResolvesAnchoredLink(t *testing.T) {
	body := strings.Repeat("pad ", 40)
	root := newInstance(t, map[string]string{
		"INDEX.md": "---\ndescription: Root.\n---\n- alpha.md\n- beta.md\n",
		"alpha.md": "---\ndescription: Alpha.\n---\nSee [[beta#Section Two]] and [[beta#Other|the other bit]].\n" + body,
		"beta.md":  "---\ndescription: Beta.\n---\n## Section Two\n" + body,
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if hasFinding(findings, "dead-link", "alpha.md") {
		t.Errorf("anchored link reported dead: %+v", findings)
	}
}

// Tilde fences are valid CommonMark. Links quoted inside one are examples, not
// references, exactly as with backtick fences.
func TestScanLinksIgnoresTildeFences(t *testing.T) {
	content := "real [[Alpha]]\n~~~\n[[InsideTilde]]\n~~~\nafter [[Beta]]\n"
	var got []string
	for _, l := range ScanLinksIn("f.md", content) {
		got = append(got, l.Target)
	}
	want := []string{"Alpha", "Beta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A fence is closed only by its own delimiter. A ``` line inside a ~~~ block is
// content — treating it as a terminator would expose the rest of the block.
func TestScanLinksNestedFenceDelimiters(t *testing.T) {
	content := "~~~\n```\n[[Hidden]]\n```\n~~~\n[[Visible]]\n"
	links := ScanLinksIn("f.md", content)
	if len(links) != 1 || links[0].Target != "Visible" {
		t.Errorf("nested fence delimiters mishandled, got %+v", links)
	}
}

// An info string makes a fence an opener, never a closer: "```go" starts a
// block and the bare "```" ends it.
func TestScanLinksFenceInfoStringDoesNotClose(t *testing.T) {
	content := "```go\n[[Hidden]]\n```\n[[Visible]]\n"
	links := ScanLinksIn("f.md", content)
	if len(links) != 1 || links[0].Target != "Visible" {
		t.Errorf("info-string fence mishandled, got %+v", links)
	}
}

// Review regression: '#' is legal in a POSIX filename, so a file may genuinely
// be named "Note#1.md". Anchor support must not silently redirect that existing,
// correct link to "Note.md" — a wrong resolution is worse than the dead link it
// replaced. The text as written wins; anchor-stripping is only the fallback.
func TestLinkPrefersFileNamedWithHash(t *testing.T) {
	body := strings.Repeat("pad ", 40)
	root := newInstance(t, map[string]string{
		"INDEX.md":  "---\ndescription: Root.\n---\n",
		"Note.md":   "---\ndescription: Plain note.\n---\n## 1\n" + body,
		"Note#1.md": "---\ndescription: Issue one.\n---\n" + body,
		"ref.md":    "---\ndescription: Ref.\n---\nSee [[Note#1]].\n" + body,
	})
	idx, err := BuildNameIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	links := ScanLinksIn("ref.md", "See [[Note#1]].\n")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %+v", links)
	}
	got := idx.ResolveLink(links[0])
	if len(got) != 1 || got[0] != "Note#1.md" {
		t.Errorf("[[Note#1]] resolved to %v, want [Note#1.md]", got)
	}
}

// Renaming a file whose name contains '#' must replace the whole written form,
// not treat the '#' part as a heading to preserve.
func TestRenameFileNamedWithHash(t *testing.T) {
	body := strings.Repeat("pad ", 40)
	root := newInstance(t, map[string]string{
		"INDEX.md":  "---\ndescription: Root.\n---\n",
		"Note#1.md": "---\ndescription: Issue one.\n---\n" + body,
		"ref.md":    "---\ndescription: Ref.\n---\nSee [[Note#1]].\n" + body,
	})
	if _, err := Rename(root, "Note#1.md", "issue-one.md"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "ref.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[[issue-one]]") {
		t.Errorf("expected [[issue-one]] after rename, got:\n%s", data)
	}
	if strings.Contains(string(data), "issue-one#1") {
		t.Errorf("the '#1' was part of the file name, not a heading:\n%s", data)
	}
}

// Review regression: a fence this scanner fails to close swallows every link
// below it, and links it cannot see are links rename will not rewrite. A
// closing run with trailing text must still close.
func TestScanLinksClosingFenceWithTrailingText(t *testing.T) {
	content := "```\ncode\n``` end\n[[Visible]]\n"
	links := ScanLinksIn("f.md", content)
	if len(links) != 1 || links[0].Target != "Visible" {
		t.Errorf("a closing fence with trailing text must close the block, got %+v", links)
	}
}

// Review regression: [[#Section]] is in-document navigation. It names no file,
// so reporting it as a dead link produced an unactionable "[[]] resolves to no
// file" message pointing at nothing.
func TestScanLinksIgnoresSameDocumentAnchor(t *testing.T) {
	links := ScanLinksIn("f.md", "See [[#Prior art]] and [[Real]].\n")
	if len(links) != 1 || links[0].Target != "Real" {
		t.Errorf("same-document anchor should be ignored, got %+v", links)
	}
}

func TestDoctorIgnoresSameDocumentAnchor(t *testing.T) {
	body := strings.Repeat("pad ", 40)
	root := newInstance(t, map[string]string{
		"INDEX.md": "---\ndescription: Root.\n---\n- long.md\n",
		"long.md":  "---\ndescription: Long note.\n---\nJump to [[#Prior art]].\n## Prior art\n" + body,
	})
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Code == "dead-link" && f.Path == "long.md" {
			t.Errorf("same-document anchor reported dead: %s", f.Message)
		}
	}
}

// Rename rewrites by re-parsing each link rather than matching the literal
// "[[old]]" text. Matching literally would skip anchored links entirely — they
// would survive the rename still naming the old file, silently dead, while
// rename reported success.
func TestRenamePreservesAnchorAndAlias(t *testing.T) {
	body := strings.Repeat("pad ", 40)
	root := newInstance(t, map[string]string{
		"INDEX.md": "---\ndescription: Root.\n---\n",
		"alpha.md": "---\ndescription: Alpha.\n---\n" +
			"plain [[beta]], anchored [[beta#Section Two]], aliased [[beta|the beta note]], " +
			"both [[beta#Section Two|see here]], quoted `[[beta]]`.\n" + body,
		"beta.md": "---\ndescription: Beta.\n---\n" + body,
	})
	res, err := Rename(root, "beta.md", "gamma.md")
	if err != nil {
		t.Fatal(err)
	}
	if res.LinksRewrote != 4 {
		t.Errorf("rewrote %d links, want 4", res.LinksRewrote)
	}
	data, err := os.ReadFile(filepath.Join(root, "alpha.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"[[gamma]]",
		"[[gamma#Section Two]]",
		"[[gamma|the beta note]]",
		"[[gamma#Section Two|see here]]",
		"`[[beta]]`", // inline code is quotation, left untouched
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in rewritten file:\n%s", want, got)
		}
	}
	// Only the backticked example may still name beta; mask inline code the way
	// the scanner does before asserting no live link survived.
	if live := inlineCodeRe.ReplaceAllString(got, ""); strings.Contains(live, "[[beta") {
		t.Errorf("an un-rewritten live link to beta survived:\n%s", got)
	}
}

// After a rename the anchored links must still resolve — the end-to-end proof
// that scanning and rewriting share one grammar.
func TestRenamedAnchoredLinksStayResolvable(t *testing.T) {
	body := strings.Repeat("pad ", 40)
	root := newInstance(t, map[string]string{
		"INDEX.md": "---\ndescription: Root.\n---\n",
		"alpha.md": "---\ndescription: Alpha.\n---\nSee [[beta#Section Two]].\n" + body,
		"beta.md":  "---\ndescription: Beta.\n---\n" + body,
	})
	if _, err := Rename(root, "beta.md", "gamma.md"); err != nil {
		t.Fatal(err)
	}
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	if hasFinding(findings, "dead-link", "alpha.md") {
		t.Errorf("anchored link broke across rename: %+v", findings)
	}
}
