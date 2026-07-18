package core

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	afs "agentsfs.ai/afs"
	"agentsfs.ai/afs/internal/buildinfo"
	"agentsfs.ai/afs/internal/core/contracts"
)

func ContractVersion(root string) string {
	return FrontmatterValue(joinRel(root, "AGENTS.md"), "agentsfs_contract")
}

func CurrentContractVersion() string {
	return buildinfo.ContractVersion
}

func BundledContract() (string, error) {
	data, err := fs.ReadFile(afs.TemplateFS, "template/AGENTS.md")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// StockContract returns the stock AGENTS.md text for a released contract
// version and whether one is known. The bundled (current) version is served
// from template/; older released versions are vendored under internal/core/
// contracts. This lets upgrade tell an untouched contract from a customized
// one, and lets `afs contract diff` render a three-way picture.
func StockContract(version string) (string, bool) {
	if version != "" && version == CurrentContractVersion() {
		if c, err := BundledContract(); err == nil {
			return c, true
		}
	}
	return contracts.StockContract(version)
}

// ContractCustomized reports whether the instance's AGENTS.md differs from the
// stock text of its declared version. The second return is false when we have
// no stock text to compare against (an unknown/older-than-vendored version),
// so callers can distinguish "known-clean/known-customized" from "can't tell".
func ContractCustomized(root string) (customized, known bool) {
	declared := ContractVersion(root)
	stock, ok := StockContract(declared)
	if !ok {
		return false, false
	}
	data, err := os.ReadFile(joinRel(root, "AGENTS.md"))
	if err != nil {
		return false, false
	}
	return string(data) != stock, true
}

// UpgradeReport records what an upgrade did beyond rewriting AGENTS.md, so the
// caller can narrate the diff.
type UpgradeReport struct {
	Created  []string // reserved files laid down (e.g. agent-journal/INDEX.md)
	Marked   []string // classic-named dirs whose INDEX.md gained agentsfs_role
	Updated  []string // recognizably stock companion files refreshed to current guidance
	Collided []string // messages naming reserved defaults not claimed (name clash)
}

// UpgradeContract brings an instance to the bundled contract. It:
//   - rewrites AGENTS.md from the bundled template (the caller enforces the
//     customized-contract and downgrade guards before calling);
//   - marks classic-named reserved dirs in place: an unmarked journal/ or
//     scratch/ whose INDEX.md is recognizably stock template text gains the
//     `agentsfs_role:` key in its existing frontmatter — never a move, never a
//     rewrite of the body;
//   - lays down the marked default reserved dir (agent-journal/,
//     agent-scratch/) only when no directory resolves for that role AND no
//     case-insensitive name collision exists (an existing colliding dir is
//     reported, not claimed).
//   - refreshes the active journal's body when it still matches the stock body
//     shipped with the instance's old contract, preserving its frontmatter.
//
// Customized companion files are never overwritten.
func UpgradeContract(root string) (UpgradeReport, error) {
	var rep UpgradeReport
	fromVersion := ContractVersion(root)
	contract, err := BundledContract()
	if err != nil {
		return rep, err
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(contract), 0o644); err != nil {
		return rep, err
	}

	// Lay down the root INDEX.md if the instance has none. Older contracts kept
	// the root's per-instance description in AGENTS.md; 0.7.0 moves it into a
	// root INDEX.md so upgrades never overwrite it. The file ships with the
	// REPLACE-ME placeholder — governed creation, never silent: it lands as a
	// reviewable diff and doctor nudges the agent to fill in the description.
	// An existing root INDEX.md is never touched.
	made, err := layDownBundledFile(root, "INDEX.md")
	if err != nil {
		return rep, err
	}
	if made {
		rep.Created = append(rep.Created, "INDEX.md")
	}

	// Mark classic-named reserved dirs in place where their INDEX.md is stock.
	for _, m := range []struct{ dir, role string }{
		{classicJournalDir, RoleJournal},
		{classicScratchDir, RoleScratch},
	} {
		marked, err := markClassicDirInPlace(root, m.dir, m.role)
		if err != nil {
			return rep, err
		}
		if marked {
			rep.Marked = append(rep.Marked, m.dir+"/INDEX.md")
		}
	}

	// Re-resolve after marking, then lay down defaults for any role that still
	// resolves to nothing — guarded by the case-insensitive collision check.
	roles, err := ResolveReservedDirs(root)
	if err != nil {
		return rep, err
	}
	for _, d := range []struct {
		resolved, def, role string
	}{
		{roles.Journal, defaultJournalDir, RoleJournal},
		{roles.Scratch, defaultScratchDir, RoleScratch},
	} {
		if d.resolved != "" {
			continue // a directory already plays this role
		}
		if existing, clash := collidingEntry(root, d.def); clash {
			rep.Collided = append(rep.Collided, collisionMessage(existing, d.def, d.role))
			continue
		}
		made, err := layDownBundledFile(root, d.def+"/INDEX.md")
		if err != nil {
			return rep, err
		}
		if made {
			rep.Created = append(rep.Created, d.def+"/INDEX.md")
		}
	}
	roles, err = ResolveReservedDirs(root)
	if err != nil {
		return rep, err
	}
	if roles.Journal != "" {
		updated, err := refreshStockJournalIndex(root, roles.Journal, fromVersion)
		if err != nil {
			return rep, err
		}
		if updated {
			rep.Updated = append(rep.Updated, roles.Journal+"/INDEX.md")
		}
	}
	return rep, nil
}

// refreshStockJournalIndex updates only a journal body that still matches the
// stock body shipped with fromVersion. Frontmatter is retained verbatim so a
// customized description or relocated role marker survives. Any body edit is
// treated as an intentional adaptation and left alone.
func refreshStockJournalIndex(root, journalDir, fromVersion string) (bool, error) {
	stock, ok := contracts.StockReservedIndex(RoleJournal, fromVersion)
	if !ok {
		return false, nil
	}
	path := joinRel(root, journalDir+"/INDEX.md")
	existing, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	if strings.TrimSpace(stripFrontmatter(string(existing))) != strings.TrimSpace(stripFrontmatter(stock)) {
		return false, nil
	}
	current, err := fs.ReadFile(afs.TemplateFS, "template/agent-journal/INDEX.md")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(stripFrontmatter(string(existing))) == strings.TrimSpace(stripFrontmatter(string(current))) {
		return false, nil
	}
	frontmatter, ok := frontmatterPrefix(string(existing))
	if !ok {
		return false, nil
	}
	updated := frontmatter + "\n" + strings.TrimLeft(stripFrontmatter(string(current)), "\r\n")
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func frontmatterPrefix(body string) (string, bool) {
	if !strings.HasPrefix(body, "---") {
		return "", false
	}
	lines := strings.SplitAfter(body, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(strings.TrimRight(lines[i], "\r\n")) == "---" {
			return strings.TrimRight(strings.Join(lines[:i+1], ""), "\r\n"), true
		}
	}
	return "", false
}

// markClassicDirInPlace adds `agentsfs_role: <role>` to the frontmatter of
// <dir>/INDEX.md when the directory exists, is not already marked for any
// role, and its INDEX.md is recognizably stock template text. It returns
// whether it marked the file. A non-stock INDEX.md is left entirely alone —
// the user may have repurposed a same-named directory.
func markClassicDirInPlace(root, dir, role string) (bool, error) {
	idxPath := joinRel(root, dir+"/INDEX.md")
	data, err := os.ReadFile(idxPath)
	if err != nil {
		return false, nil // no such dir/INDEX — nothing to mark
	}
	body := string(data)
	if FrontmatterValueFromReader(strings.NewReader(body), roleKey) != "" {
		return false, nil // already marked (any role) — leave it
	}
	if !isStockReservedIndex(body, role) {
		return false, nil // repurposed/customized — do not touch
	}
	marked, err := insertFrontmatterKey(body, roleKey, role)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(idxPath, []byte(marked), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// stockReservedIndex030 returns the vendored stock 0.3.0 INDEX.md text of a
// reserved role's classic directory. Exposed within the package for the
// migration and its tests.
func stockReservedIndex030(role string) (string, bool) {
	return contracts.StockReservedIndex(role, "0.3.0")
}

// isStockReservedIndex reports whether an INDEX.md body is recognizably the
// stock template text for a reserved role — the signal that it's safe to add
// the marker in place. It compares the body (frontmatter stripped) against the
// vendored 0.3.0 stock INDEX for the role, so a description the user tweaked in
// frontmatter doesn't block the migration, but a repurposed body does. A body
// that already differs in prose is left entirely alone.
func isStockReservedIndex(body, role string) bool {
	stock, ok := stockReservedIndex030(role)
	if !ok {
		return false
	}
	return strings.TrimSpace(stripFrontmatter(body)) == strings.TrimSpace(stripFrontmatter(stock))
}

// insertFrontmatterKey inserts `key: value` into an existing YAML frontmatter
// block without touching the body. The body must open with a `---` fence; the
// key is added just before the closing fence. It errors if there is no
// frontmatter block (the caller only marks stock INDEX files, which always
// have one).
func insertFrontmatterKey(body, key, value string) (string, error) {
	lines := strings.SplitAfter(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(strings.TrimRight(lines[0], "\n")) != "---" {
		return "", fmt.Errorf("no YAML frontmatter to insert %q into", key)
	}
	nl := "\n"
	if strings.HasSuffix(lines[0], "\r\n") {
		nl = "\r\n"
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(strings.TrimRight(lines[i], "\r\n")) == "---" {
			insert := key + ": " + value + nl
			out := strings.Join(lines[:i], "") + insert + strings.Join(lines[i:], "")
			return out, nil
		}
	}
	return "", fmt.Errorf("unterminated YAML frontmatter; cannot insert %q", key)
}

// reservedNamesFor lists the names that a role's reserved directory could take
// — the 0.4.0 default and the classic pre-0.4.0 name — for the collision
// guard to test existing entries against. The incident that drove the guard
// was a personal "Journal/" catching the classic journal/ lay-down on a
// case-insensitive filesystem, so both names must be guarded.
func reservedNamesFor(def string) []string {
	switch def {
	case defaultJournalDir:
		return []string{defaultJournalDir, classicJournalDir}
	case defaultScratchDir:
		return []string{defaultScratchDir, classicScratchDir}
	}
	return []string{def}
}

// collidingEntry reports whether a root-level directory collides
// case-insensitively (string-level, so it behaves identically on
// case-sensitive Linux CI and case-insensitive macOS) with any reserved name
// for the role def represents — its 0.4.0 default or its classic name. It
// returns the actual existing name. An entry whose name exactly equals the
// default is not a collision: laying the default down there is a no-op merge
// into an already-correctly-named dir, which the resolver would have caught,
// and the exact-name case is handled without a warning.
func collidingEntry(root, def string) (string, bool) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return "", false
	}
	names := reservedNamesFor(def)
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == def {
			return "", false // exact default name — not a collision
		}
		for _, n := range names {
			if strings.EqualFold(name, n) {
				return name, true
			}
		}
	}
	return "", false
}

func collisionMessage(existing, def, role string) string {
	return fmt.Sprintf("existing directory %q collides with reserved default %q — not claimed; mark a directory with 'agentsfs_role: %s' to designate your session %s", existing, def, role, roleNoun(role))
}

func roleNoun(role string) string {
	if role == RoleScratch {
		return "scratch space"
	}
	return "journal"
}

// layDownBundledFile copies template/<rel> into the instance if the target
// doesn't already exist, creating parent directories as needed. It returns
// whether it created the file. It never overwrites an existing one.
func layDownBundledFile(root, rel string) (bool, error) {
	dest := joinRel(root, rel)
	if fileExists(dest) {
		return false, nil
	}
	data, err := fs.ReadFile(afs.TemplateFS, "template/"+rel)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// ContractDiff renders the two unified diffs an agent needs to port a
// customized contract by hand:
//
//	Adaptations: stock text of the declared version → the instance's AGENTS.md
//	Changes:     stock text of the declared version → the bundled stock text
//
// With `afs contract current` (the full new stock text), these give the
// complete three-way picture. When no stock text is vendored for the declared
// version, Adaptations is empty and Changes diffs the current file against the
// bundled text instead. Diffs are rendered by shelling to `git diff
// --no-index` (git is already a hard dependency).
type ContractDiff struct {
	Declared    string // the instance's declared contract version ("" if unset)
	Bundled     string // the version this binary bundles
	HaveStock   bool   // a stock text is vendored for Declared
	Adaptations string // stock(Declared) → current AGENTS.md
	Changes     string // stock(Declared) → bundled stock  (or current → bundled when !HaveStock)
}

func ComputeContractDiff(root string) (ContractDiff, error) {
	var d ContractDiff
	d.Declared = ContractVersion(root)
	d.Bundled = CurrentContractVersion()

	current, err := os.ReadFile(joinRel(root, "AGENTS.md"))
	if err != nil {
		return d, err
	}
	bundled, err := BundledContract()
	if err != nil {
		return d, err
	}

	stock, ok := StockContract(d.Declared)
	d.HaveStock = ok
	if ok {
		if d.Adaptations, err = gitDiffNoIndex(stock, string(current), "stock-"+d.Declared, "your-AGENTS.md"); err != nil {
			return d, err
		}
		if d.Changes, err = gitDiffNoIndex(stock, bundled, "stock-"+d.Declared, "stock-"+d.Bundled); err != nil {
			return d, err
		}
	} else {
		if d.Changes, err = gitDiffNoIndex(string(current), bundled, "your-AGENTS.md", "stock-"+d.Bundled); err != nil {
			return d, err
		}
	}
	return d, nil
}

// gitDiffNoIndex writes a and b to temp files and returns `git diff
// --no-index` output. git exits 1 when the inputs differ — that is the normal,
// non-error case, so only a higher exit code (or a spawn failure) is an error.
func gitDiffNoIndex(a, b, labelA, labelB string) (string, error) {
	dir, err := os.MkdirTemp("", "afs-contract-diff-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	pa := filepath.Join(dir, labelA)
	pb := filepath.Join(dir, labelB)
	if err := os.WriteFile(pa, []byte(a), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(pb, []byte(b), 0o644); err != nil {
		return "", err
	}
	cmd := exec.Command("git", "diff", "--no-index", "--", pa, pb)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return string(out), nil // exit 1 == "files differ", the expected case
		}
		return string(out), fmt.Errorf("git diff --no-index: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// CompareContractVersions orders two contract-version strings, returning
// -1, 0, or +1 (a < b, a == b, a > b). It lets callers outside this package
// distinguish behind / current / ahead without duplicating the parse.
func CompareContractVersions(a, b string) int {
	return compareVersions(a, b)
}

// compareVersions orders two dotted numeric version strings; the shared
// implementation lives in buildinfo so the updater orders CLI release
// versions with the same rules.
func compareVersions(a, b string) int {
	return buildinfo.CompareVersions(a, b)
}
