package skills

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	afs "agentsfs.ai/afs"
)

// The markdownto skill is the one skill in the pack this repo does not author.
// It is copied verbatim from github.com/seekinggradient/markdownto, and
// skills/markdownto/VERSION records which commit it came from plus a sha256 per
// file. These tests are what make the copy a pin rather than a snapshot:
// editing a vendored byte without editing the manifest fails here, so the two
// ways a vendored skill rots — a local "small fix" that silently forks it, and
// an upgrade that lands without its provenance — both become build failures.

const markdowntoDir = "markdownto"

// markdowntoManifest reads skills/markdownto/VERSION into its key/value pairs,
// the same `key: value` + `#` comment format assets/mdto/VERSION uses.
func markdowntoManifest(t *testing.T) map[string]string {
	t.Helper()
	body, err := fs.ReadFile(afs.SkillsFS, skillsRoot+"/"+markdowntoDir+"/VERSION")
	if err != nil {
		t.Fatalf("read the vendored skill's manifest: %v", err)
	}
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// TestMarkdownToSkillMatchesManifest is the pin: every file the manifest names
// must be embedded with exactly the bytes it declares. The four examples/ files
// are the byte-normative templates the markdownto conformance suite validates —
// nothing here can run that suite (it needs the mdto CLI), so byte equality
// with the pinned copy IS the guarantee that they still conform.
func TestMarkdownToSkillMatchesManifest(t *testing.T) {
	manifest := markdowntoManifest(t)

	if manifest["commit"] == "" || manifest["source"] == "" {
		t.Error("skills/markdownto/VERSION must record the source repo and the commit the skill came from")
	}
	if len(manifest["commit"]) != 40 {
		t.Errorf("commit = %q, want a full 40-char sha (a short sha cannot be re-resolved once the branch moves)", manifest["commit"])
	}

	pinned := map[string]string{}
	for key, want := range manifest {
		rel, ok := strings.CutPrefix(key, "sha256 ")
		if !ok {
			continue
		}
		pinned[rel] = want
		body, err := fs.ReadFile(afs.SkillsFS, skillsRoot+"/"+markdowntoDir+"/"+rel)
		if err != nil {
			t.Errorf("manifest pins %q but it is not embedded: %v", rel, err)
			continue
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s sha256 = %s, manifest says %s — re-vendor deliberately (see skills/markdownto/VERSION)", rel, got, want)
		}
	}

	// The reverse direction: a file added to the vendored directory without a
	// manifest line would otherwise ship unpinned. VERSION describes the rest,
	// so it is the one file that cannot describe itself.
	root := skillsRoot + "/" + markdowntoDir
	err := fs.WalkDir(afs.SkillsFS, root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(name, root), "/")
		if rel == "VERSION" {
			return nil
		}
		if _, ok := pinned[rel]; !ok {
			t.Errorf("vendored file %q has no sha256 line in skills/markdownto/VERSION", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Five files: the skill and one example per spec it scaffolds.
	if len(pinned) != 5 {
		t.Errorf("manifest pins %d files, want 5 (SKILL.md + four examples)", len(pinned))
	}
}

// TestMarkdownToSkillIsSpecAgnostic guards the property that makes pinning a
// copy of someone else's skill safe at all: it teaches DISCOVERY of the spec
// family instead of hardcoding a roster, so this vendored copy does not go
// stale when markdownto ships a fifth spec. If a future re-vendor lost that,
// the pin would keep shipping a copy with a shelf life.
func TestMarkdownToSkillIsSpecAgnostic(t *testing.T) {
	body, err := fs.ReadFile(afs.SkillsFS, skillsRoot+"/"+markdowntoDir+"/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	skill := string(body)
	for _, want := range []string{
		"markdownto.ai/llms.txt", // reachable without the CLI and without a checkout
		"mdto spec",              // and with the CLI, the local source of truth
		"not as the roster",      // the instruction that keeps the list open
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("vendored SKILL.md no longer contains %q — it may have stopped teaching discovery", want)
		}
	}
}

// TestMarkdownToSkillMaterializesWholeDirectory: the examples are part of the
// skill, so a materialized copy that dropped them would be a broken skill on
// disk. This is the reason Materialize walks the directory.
func TestMarkdownToSkillMaterializesWholeDirectory(t *testing.T) {
	base := t.TempDir()
	if err := Materialize(base); err != nil {
		t.Fatalf("Materialize() error: %v", err)
	}
	for _, rel := range []string{
		"SKILL.md",
		"VERSION",
		"examples/todo.md",
		"examples/kanban.md",
		"examples/backlog.md",
		"examples/audio.md",
	} {
		embedded, err := fs.ReadFile(afs.SkillsFS, skillsRoot+"/"+markdowntoDir+"/"+rel)
		if err != nil {
			t.Fatalf("read embedded %s: %v", rel, err)
		}
		got, err := os.ReadFile(filepath.Join(base, markdowntoDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s was not materialized: %v", rel, err)
			continue
		}
		if string(got) != string(embedded) {
			t.Errorf("materialized %s does not match the embedded copy", rel)
		}
	}
}
