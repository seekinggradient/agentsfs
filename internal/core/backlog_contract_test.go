package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	afs "agentsfs.ai/afs"
)

// Contract 0.10.0 adds the backlog: a page-level role with its own stock page.
// These cover the upgrade path onto it, and the vendoring that keeps 0.9.0
// instances readable as un-adapted (a wrong vendored text makes pristine
// instances look customized and pushes agents into porting phantom changes).

// A stock 0.9.0 instance upgrades to 0.10.0 and gains the backlog page, marked
// so it actually resolves as the role.
func TestUpgradeStock090To0100AddsBacklog(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock090, ok := StockContract("0.9.0")
	if !ok {
		t.Fatal("no vendored 0.9.0 stock contract")
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock090)
	mustWrite(t, filepath.Join(root, "INDEX.md"), "---\ndescription: A knowledge base.\n---\n# Index\n")
	for _, d := range []struct{ dir, role, desc string }{
		{"agent-journal", RoleJournal, "Session log."},
		{"agent-scratch", RoleScratch, "Scratch."},
	} {
		mustWrite(t, filepath.Join(root, d.dir, "INDEX.md"),
			"---\ndescription: "+d.desc+"\nagentsfs_role: "+d.role+"\n---\n# "+d.dir+"\n")
	}

	rep, err := UpgradeContract(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := ContractVersion(root); got != CurrentContractVersion() {
		t.Fatalf("upgrade did not bump the contract: %q", got)
	}
	if !containsString(rep.Created, "backlog.md") {
		t.Fatalf("upgrade did not report creating backlog.md: %+v", rep)
	}
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Backlog != "backlog.md" || rd.BacklogSource != RoleSourceMarker {
		t.Fatalf("laid-down backlog does not resolve by marker: Backlog=%q Source=%q", rd.Backlog, rd.BacklogSource)
	}
	// The page must be laid down byte-for-byte from the template, and its own
	// documented example must not parse as real work (the parser skips fences).
	data, err := os.ReadFile(filepath.Join(root, "backlog.md"))
	if err != nil {
		t.Fatal(err)
	}
	stockPage, ok := StockBacklogPage(CurrentContractVersion())
	if !ok {
		t.Fatal("no stock backlog page for the current contract")
	}
	if string(data) != stockPage {
		t.Error("laid-down backlog.md differs from the stock backlog page")
	}
	backlog, found, err := LoadBacklog(root)
	if err != nil || !found {
		t.Fatalf("LoadBacklog after upgrade: found=%v err=%v", found, err)
	}
	if n := len(backlog.Flat()); n != 0 {
		t.Errorf("the stock backlog's fenced example produced %d phantom task(s)", n)
	}
	for _, f := range mustDoctor(t, root) {
		if f.Severity == "error" {
			t.Errorf("upgraded 0.9.0→0.10.0 instance is not doctor-clean: %s %s %s", f.Severity, f.Code, f.Message)
		}
	}

	// Re-running the upgrade must not create it again.
	rep, err = UpgradeContract(root)
	if err != nil {
		t.Fatal(err)
	}
	if containsString(rep.Created, "backlog.md") {
		t.Errorf("second upgrade re-reported creating backlog.md: %+v", rep)
	}
}

// An instance that already has a backlog — under any name, because the marker
// is the only truth — gains no file and keeps the one it has.
func TestUpgradeLeavesExistingMarkedBacklogAlone(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock090, _ := StockContract("0.9.0")
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock090)
	existing := "---\ndescription: What I plan to do.\nagentsfs_role: backlog\n---\n\n## Now\n- [ ] Keep me exactly as I am ^keep-me\n"
	mustWrite(t, filepath.Join(root, "planning", "roadmap.md"), existing)

	rep, err := UpgradeContract(root)
	if err != nil {
		t.Fatal(err)
	}
	if containsString(rep.Created, "backlog.md") {
		t.Errorf("upgrade created a second backlog though one is marked: %+v", rep)
	}
	if fileExists(filepath.Join(root, "backlog.md")) {
		t.Error("upgrade wrote backlog.md into an instance that already has a backlog")
	}
	data, _ := os.ReadFile(filepath.Join(root, "planning", "roadmap.md"))
	if string(data) != existing {
		t.Errorf("the instance's own backlog was modified:\n%s", data)
	}
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Backlog != "planning/roadmap.md" {
		t.Errorf("backlog resolved to %q, want the marked page", rd.Backlog)
	}
}

// A file already called backlog.md that does NOT declare the role is the user's
// note. Upgrade must skip it and say so, never overwrite it.
func TestUpgradeSkipsCollidingBacklogFilename(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock090, _ := StockContract("0.9.0")
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock090)
	mine := "---\ndescription: My own list of things, not the reserved role.\n---\n# Backlog\n\nGroceries, mostly.\n"
	mustWrite(t, filepath.Join(root, "backlog.md"), mine)

	rep, err := UpgradeContract(root)
	if err != nil {
		t.Fatal(err)
	}
	if containsString(rep.Created, "backlog.md") {
		t.Errorf("upgrade claimed the user's backlog.md: %+v", rep)
	}
	data, _ := os.ReadFile(filepath.Join(root, "backlog.md"))
	if string(data) != mine {
		t.Fatalf("the user's backlog.md was overwritten:\n%s", data)
	}
	var reported bool
	for _, msg := range rep.Collided {
		if strings.Contains(msg, "backlog.md") && strings.Contains(msg, RoleBacklog) {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the backlog.md collision was skipped silently: %+v", rep.Collided)
	}
	// Nothing plays the role, so the instance still has no backlog — which is a
	// legal state, not a broken one.
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Backlog != "" || rd.BacklogSource != RoleSourceNone {
		t.Errorf("an unmarked backlog.md was adopted as the role: %+v", rd)
	}
}

// The regression guard for the vendoring: the 0.9.0 text must be byte-exact, or
// every pristine 0.9.0 instance reads as customized, upgrade refuses, and agents
// are pushed to port adaptations that do not exist.
func TestVendored090ReadsAsPristine(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock090, ok := StockContract("0.9.0")
	if !ok {
		t.Fatal("0.9.0 stock text is not vendored")
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock090)
	customized, known := ContractCustomized(root)
	if !known {
		t.Fatal("0.9.0 stock text is not vendored — the customized-contract guard can't tell")
	}
	if customized {
		t.Fatal("a byte-exact stock 0.9.0 AGENTS.md was reported customized")
	}
	// The vendored 0.9.0 text must be the previous contract, not a copy of the
	// current one: same-text vendoring is how the 0.2.0-era drift happened.
	if declared := FrontmatterValueFromReader(strings.NewReader(stock090), "agentsfs_contract"); declared != "0.9.0" {
		t.Errorf("vendored 0.9.0 stock declares contract %q", declared)
	}
	bundled, err := BundledContract()
	if err != nil {
		t.Fatal(err)
	}
	if stock090 == bundled {
		t.Error("the vendored 0.9.0 text is identical to the bundled contract — one of them is wrong")
	}
}

// Contract 0.9.0 shipped under two different stock texts — afs 0.8.0–0.10.0
// bundled one, 0.11.x a revised one, with no version bump between them. BOTH
// are pristine, so both must read as un-customized: recognizing only the latest
// makes upgrade refuse on instances nobody ever edited and sends the agent off
// to port adaptations that do not exist. This is the 0.2.0-era incident, and it
// is reachable on 0.9.0 instances today.
func TestEveryPristine090TextReadsAsUncustomized(t *testing.T) {
	variants := StockContractVariants("0.9.0")
	if len(variants) < 2 {
		t.Fatalf("only %d stock text(s) vendored for 0.9.0; the revision shipped by afs 0.8.0–0.10.0 is missing", len(variants))
	}
	seen := map[string]bool{}
	for i, stock := range variants {
		if seen[stock] {
			t.Errorf("variant %d duplicates an earlier 0.9.0 text — one of the vendored files is wrong", i)
		}
		seen[stock] = true
		if declared := FrontmatterValueFromReader(strings.NewReader(stock), "agentsfs_contract"); declared != "0.9.0" {
			t.Errorf("0.9.0 variant %d declares contract %q", i, declared)
		}
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(root, "AGENTS.md"), stock)
		customized, known := ContractCustomized(root)
		if !known {
			t.Errorf("0.9.0 variant %d is not recognized at all", i)
		}
		if customized {
			t.Errorf("pristine 0.9.0 variant %d was reported customized — upgrade would refuse on an untouched instance", i)
		}
		// And it must actually upgrade, which is the behavior the misread broke.
		if _, err := UpgradeContract(root); err != nil {
			t.Fatalf("upgrading pristine 0.9.0 variant %d: %v", i, err)
		}
		if got := ContractVersion(root); got != CurrentContractVersion() {
			t.Errorf("variant %d did not upgrade: %q", i, got)
		}
	}
	// An edited 0.9.0 is still customized — the variant list must not turn the
	// guard off.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), variants[0]+"\n## House rule\n\nAlways cite the policy number.\n")
	if customized, known := ContractCustomized(root); !known || !customized {
		t.Errorf("an adapted 0.9.0 contract went undetected (known=%v customized=%v)", known, customized)
	}
}

// Customization detection still works at 0.10.0, against the bundled text.
func TestCustomized0100ContractDetected(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	bundled, err := BundledContract()
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), bundled)
	if customized, known := ContractCustomized(root); !known || customized {
		t.Fatalf("the bundled contract read as customized (known=%v customized=%v)", known, customized)
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), bundled+"\n## House rule\n\nAlways cite the policy number.\n")
	if customized, known := ContractCustomized(root); !known || !customized {
		t.Fatalf("adapted 0.10.0 contract not detected as customized (known=%v customized=%v)", known, customized)
	}
}

// The stock backlog page must be recognizable: vendored byte-for-byte for
// 0.10.0, and served from the template for the current version, so a later
// contract can tell a page an instance has made its own from the one we laid
// down. Same failure mode as the contract text itself.
func TestStockBacklogPageRecognized(t *testing.T) {
	vendored, ok := StockBacklogPage("0.10.0")
	if !ok {
		t.Fatal("no vendored 0.10.0 stock backlog page")
	}
	tmpl, err := fs.ReadFile(afs.TemplateFS, "template/backlog.md")
	if err != nil {
		t.Fatal(err)
	}
	if vendored != string(tmpl) {
		t.Error("vendored backlog-0.10.0.md is not byte-identical to template/backlog.md")
	}
	if role := FrontmatterValueFromReader(strings.NewReader(vendored), roleKey); role != RoleBacklog {
		t.Errorf("stock backlog page declares agentsfs_role: %q, want %q", role, RoleBacklog)
	}
	if _, ok := StockBacklogPage("0.9.0"); ok {
		t.Error("a stock backlog page exists for 0.9.0, which predates the role")
	}
}

// The bundled contract must carry the backlog rule and the commands that read
// it — the contract text is the only thing most agents ever read.
func TestBundledContractCarriesBacklogRule(t *testing.T) {
	contract, err := BundledContract()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Track intentions in the backlog",
		"agentsfs_role: backlog",
		"afs tasks",
		"afs prime",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("bundled contract does not mention %q", want)
		}
	}
	if got := FrontmatterValueFromReader(strings.NewReader(contract), "agentsfs_contract"); got != CurrentContractVersion() {
		t.Errorf("bundled contract declares %q, binary bundles %q", got, CurrentContractVersion())
	}
}

func mustDoctor(t *testing.T, root string) []Finding {
	t.Helper()
	findings, err := Doctor(root)
	if err != nil {
		t.Fatal(err)
	}
	return findings
}
