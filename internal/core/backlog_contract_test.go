package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	afs "agentsfs.ai/afs"
)

// Contract 0.10.0 added the backlog as a page-level role; 0.11.0 makes it a
// DIRECTORY whose INDEX.md is the spine and migrates the page shape into it.
// These cover the upgrade paths onto that shape, and the vendoring that keeps
// older instances readable as un-adapted (a wrong vendored text makes pristine
// instances look customized and pushes agents into porting phantom changes).

// A stock 0.9.0 instance — one that predates the backlog entirely — upgrades and
// gains the backlog directory, marked so it actually resolves as the role.
func TestUpgradeStock090To0110AddsBacklogDirectory(t *testing.T) {
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
	if !containsString(rep.Created, "backlog/INDEX.md") {
		t.Fatalf("upgrade did not report creating backlog/INDEX.md: %+v", rep)
	}
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Backlog != "backlog" || rd.BacklogSpine != "backlog/INDEX.md" || rd.BacklogSource != RoleSourceMarker {
		t.Fatalf("laid-down backlog does not resolve as a directory role: %+v", rd)
	}
	if rd.BacklogLegacy {
		t.Error("a freshly laid-down backlog reported itself as the legacy page shape")
	}
	// The spine must be laid down byte-for-byte from the template, and its own
	// documented examples must not parse as real work.
	data, err := os.ReadFile(filepath.Join(root, "backlog", "INDEX.md"))
	if err != nil {
		t.Fatal(err)
	}
	stockSpine, ok := StockBacklogSpine(CurrentContractVersion())
	if !ok {
		t.Fatal("no stock backlog spine for the current contract")
	}
	if string(data) != stockSpine {
		t.Error("laid-down backlog/INDEX.md differs from the stock spine")
	}
	backlog, found, err := LoadBacklog(root)
	if err != nil || !found {
		t.Fatalf("LoadBacklog after upgrade: found=%v err=%v", found, err)
	}
	if n := len(backlog.Flat()); n != 0 {
		t.Errorf("the stock spine's documented examples produced %d phantom task(s)", n)
	}
	for _, f := range mustDoctor(t, root) {
		if f.Severity == "error" {
			t.Errorf("upgraded 0.9.0→0.11.0 instance is not doctor-clean: %s %s %s", f.Severity, f.Code, f.Message)
		}
	}

	// Re-running the upgrade must not create it again.
	rep, err = UpgradeContract(root)
	if err != nil {
		t.Fatal(err)
	}
	if containsString(rep.Created, "backlog/INDEX.md") {
		t.Errorf("second upgrade re-reported creating the backlog spine: %+v", rep)
	}
}

// A directory already marked for the role IS the instance's backlog, wherever it
// lives and whatever it is called. Upgrade leaves it entirely alone and lays
// nothing down beside it.
func TestUpgradeLeavesExistingBacklogDirectoryAlone(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock0100, ok := StockContract("0.10.0")
	if !ok {
		t.Fatal("no vendored 0.10.0 stock contract")
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock0100)
	existing := "---\ndescription: What I plan to do.\nagentsfs_role: backlog\n---\n\n## Now\n- [ ] Keep me exactly as I am ^keep-me\n"
	mustWrite(t, filepath.Join(root, "planning", "INDEX.md"), existing)

	rep, err := UpgradeContract(root)
	if err != nil {
		t.Fatal(err)
	}
	if containsString(rep.Created, "backlog/INDEX.md") {
		t.Errorf("upgrade created a second backlog though one is marked: %+v", rep)
	}
	if fileExists(filepath.Join(root, "backlog", "INDEX.md")) {
		t.Error("upgrade wrote a backlog directory into an instance that already has one")
	}
	data, _ := os.ReadFile(filepath.Join(root, "planning", "INDEX.md"))
	if string(data) != existing {
		t.Errorf("the instance's own backlog was modified:\n%s", data)
	}
	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Backlog != "planning" || rd.BacklogSpine != "planning/INDEX.md" || rd.BacklogLegacy {
		t.Errorf("backlog resolved to %+v, want the marked directory", rd)
	}
}

// The 0.10.0 → 0.11.0 migration: the marked page becomes the directory's
// INDEX.md — frontmatter and body preserved — the page is gone, links to it
// point at the spine, and the legacy doctor finding is cleared.
func TestUpgradeMigratesLegacyBacklogPage(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock0100, ok := StockContract("0.10.0")
	if !ok {
		t.Fatal("no vendored 0.10.0 stock contract")
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock0100)
	mustWrite(t, filepath.Join(root, "INDEX.md"), "---\ndescription: A knowledge base.\n---\n# Index\n")
	for _, d := range []struct{ dir, role, desc string }{
		{"agent-journal", RoleJournal, "Session log."},
		{"agent-scratch", RoleScratch, "Scratch."},
	} {
		mustWrite(t, filepath.Join(root, d.dir, "INDEX.md"),
			"---\ndescription: "+d.desc+"\nagentsfs_role: "+d.role+"\n---\n# "+d.dir+"\n")
	}
	page, ok := StockBacklogPage("0.10.0")
	if !ok {
		t.Fatal("no vendored 0.10.0 stock backlog page")
	}
	page = strings.Replace(page, "## Now\n", "## Now\n- [/] Ship the migration ^ship\n", 1)
	mustWrite(t, filepath.Join(root, defaultBacklogPage), page)
	mustWrite(t, filepath.Join(root, "notes", "plan.md"),
		"---\ndescription: A note that points at the backlog.\n---\n\nTracked in [[backlog#^ship]]; the whole list is [[backlog]].\nQuoted, so untouched: `[[backlog]]`.\n")

	rd, err := ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if !rd.BacklogLegacy {
		t.Fatalf("the 0.10.0 fixture did not resolve as a legacy page backlog: %+v", rd)
	}

	rep, err := UpgradeContract(root)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(rep.Created, "backlog/INDEX.md") {
		t.Fatalf("the migration did not report the new spine: %+v", rep)
	}
	if len(rep.Collided) != 0 {
		t.Fatalf("the migration reported collisions: %+v", rep.Collided)
	}
	if fileExists(filepath.Join(root, defaultBacklogPage)) {
		t.Error("the legacy backlog page survived the migration")
	}
	got, err := os.ReadFile(filepath.Join(root, "backlog", "INDEX.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != page {
		t.Errorf("the migrated spine is not the legacy page's frontmatter and body:\n%s", got)
	}

	rd, err = ResolveReservedDirs(root)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Backlog != "backlog" || rd.BacklogSpine != "backlog/INDEX.md" || rd.BacklogLegacy {
		t.Fatalf("the migrated backlog does not resolve as a directory role: %+v", rd)
	}
	backlog, found, err := LoadBacklog(root)
	if err != nil || !found {
		t.Fatalf("LoadBacklog after migration: found=%v err=%v", found, err)
	}
	if tasks := backlog.Flat(); len(tasks) != 1 || tasks[0].Slug != "ship" {
		t.Errorf("the migrated spine lost its tasks: %+v", tasks)
	}

	// Links to the old page name follow it to the spine — path-qualified,
	// because every directory has an INDEX.md and a bare [[INDEX]] is ambiguous.
	note, _ := os.ReadFile(filepath.Join(root, "notes", "plan.md"))
	for _, want := range []string{"[[backlog/INDEX#^ship]]", "[[backlog/INDEX]]"} {
		if !strings.Contains(string(note), want) {
			t.Errorf("link not retargeted at the spine (%s missing):\n%s", want, note)
		}
	}
	if !strings.Contains(string(note), "`[[backlog]]`") {
		t.Errorf("a link quoted in code was rewritten:\n%s", note)
	}
	idx, err := BuildNameIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	if m := idx.Resolve("backlog/INDEX"); len(m) != 1 || m[0] != "backlog/INDEX.md" {
		t.Errorf("the rewritten link target resolves to %v, want just the spine", m)
	}
	for _, f := range mustDoctor(t, root) {
		if f.Code == "backlog-page-role-legacy" {
			t.Errorf("the legacy finding survived the migration: %+v", f)
		}
		if f.Severity == "error" {
			t.Errorf("migrated instance is not doctor-clean: %s %s %s", f.Severity, f.Code, f.Message)
		}
	}
}

// An entry already holding the backlog's default name is the user's, not our
// template: it is reported and left untouched, never claimed or written into.
func TestUpgradeSkipsCollidingBacklogDirectory(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		clash string
	}{
		{
			// Case-insensitive name clash: the classic reserved-name incident.
			name:  "case-folded directory",
			files: map[string]string{"Backlog/INDEX.md": "---\ndescription: My reading backlog.\n---\n"},
			clash: "Backlog",
		},
		{
			// Exact name, but it already describes itself — the spine would have
			// to overwrite somebody's INDEX.md.
			name:  "exact directory with its own INDEX",
			files: map[string]string{"backlog/INDEX.md": "---\ndescription: Games I mean to play.\n---\n"},
			clash: "backlog",
		},
		{
			name:  "plain file holding the name",
			files: map[string]string{"backlog": "not even markdown\n"},
			clash: "backlog",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
				t.Fatal(err)
			}
			stock090, _ := StockContract("0.9.0")
			mustWrite(t, filepath.Join(root, "AGENTS.md"), stock090)
			for rel, body := range tc.files {
				mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), body)
			}

			rep, err := UpgradeContract(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range rep.Created {
				if strings.HasPrefix(c, "backlog") || strings.HasPrefix(c, "Backlog") {
					t.Errorf("upgrade claimed the user's %q: %+v", tc.clash, rep.Created)
				}
			}
			for rel, body := range tc.files {
				got, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
				if string(got) != body {
					t.Fatalf("the user's %s was overwritten:\n%s", rel, got)
				}
			}
			var reported bool
			for _, msg := range rep.Collided {
				if strings.Contains(msg, tc.clash) && strings.Contains(msg, RoleBacklog) {
					reported = true
				}
			}
			if !reported {
				t.Errorf("the %s collision was skipped silently: %+v", tc.clash, rep.Collided)
			}
			// Nothing plays the role, so the instance still has no backlog —
			// which is a legal state, not a broken one.
			rd, err := ResolveReservedDirs(root)
			if err != nil {
				t.Fatal(err)
			}
			if rd.Backlog != "" || rd.BacklogSource != RoleSourceNone {
				t.Errorf("an unmarked colliding entry was adopted as the role: %+v", rd)
			}
		})
	}
}

// 0.10.0's default page name is not the directory's name, so an ordinary note
// called backlog.md neither blocks the lay-down nor gets touched by it.
func TestUpgradeLaysDownBacklogDirBesideUnmarkedBacklogPage(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock090, _ := StockContract("0.9.0")
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock090)
	mine := "---\ndescription: My own list of things, not the reserved role.\n---\n# Backlog\n\nGroceries, mostly.\n"
	mustWrite(t, filepath.Join(root, defaultBacklogPage), mine)

	rep, err := UpgradeContract(root)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(rep.Created, "backlog/INDEX.md") {
		t.Errorf("an unmarked backlog.md blocked the backlog directory: %+v", rep)
	}
	got, _ := os.ReadFile(filepath.Join(root, defaultBacklogPage))
	if string(got) != mine {
		t.Fatalf("the user's backlog.md was modified:\n%s", got)
	}
}

// A legacy page cannot be migrated into a name something else already holds. The
// page is left exactly as it is — still the backlog, still readable — and the
// obstruction is reported.
func TestUpgradeReportsBlockedLegacyBacklogMigration(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock0100, _ := StockContract("0.10.0")
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock0100)
	page := "---\ndescription: Pending work.\nagentsfs_role: backlog\n---\n\n## Now\n- [ ] Survive the blocked upgrade ^survive\n"
	mustWrite(t, filepath.Join(root, defaultBacklogPage), page)
	occupied := "---\ndescription: Games I mean to play.\n---\n"
	mustWrite(t, filepath.Join(root, "backlog", "INDEX.md"), occupied)

	rep, err := UpgradeContract(root)
	if err != nil {
		t.Fatal(err)
	}
	if containsString(rep.Created, "backlog/INDEX.md") {
		t.Fatalf("the migration overwrote an existing backlog directory: %+v", rep)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "backlog", "INDEX.md")); string(got) != occupied {
		t.Fatalf("the occupying directory's INDEX.md was overwritten:\n%s", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, defaultBacklogPage)); string(got) != page {
		t.Fatalf("the legacy page was modified by a migration that could not complete:\n%s", got)
	}
	var reported bool
	for _, msg := range rep.Collided {
		if strings.Contains(msg, defaultBacklogPage) && strings.Contains(msg, "backlog") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the blocked migration was not reported: %+v", rep.Collided)
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

// The same guard for 0.10.0, the version 0.11.0 upgrades from: a pristine
// 0.10.0 instance must read as un-customized against the newly vendored text, or
// the migration refuses on instances nobody ever touched.
func TestVendored0100ReadsAsPristine(t *testing.T) {
	stock0100, ok := StockContract("0.10.0")
	if !ok {
		t.Fatal("0.10.0 stock text is not vendored")
	}
	if declared := FrontmatterValueFromReader(strings.NewReader(stock0100), "agentsfs_contract"); declared != "0.10.0" {
		t.Errorf("vendored 0.10.0 stock declares contract %q", declared)
	}
	bundled, err := BundledContract()
	if err != nil {
		t.Fatal(err)
	}
	if stock0100 == bundled {
		t.Error("the vendored 0.10.0 text is identical to the bundled contract — one of them is wrong")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "AGENTS.md"), stock0100)
	customized, known := ContractCustomized(root)
	if !known || customized {
		t.Fatalf("a byte-exact stock 0.10.0 AGENTS.md was not recognized (known=%v customized=%v)", known, customized)
	}
	if _, err := UpgradeContract(root); err != nil {
		t.Fatalf("upgrading a pristine 0.10.0 instance: %v", err)
	}
	if got := ContractVersion(root); got != CurrentContractVersion() {
		t.Errorf("pristine 0.10.0 did not upgrade: %q", got)
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

// Customization detection still works at the bundled version.
func TestCustomizedBundledContractDetected(t *testing.T) {
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
		t.Fatalf("adapted bundled contract not detected as customized (known=%v customized=%v)", known, customized)
	}
}

// The stock backlog texts must be recognizable in both shapes: the current
// spine served from the template, and 0.10.0's retired page vendored byte for
// byte, so the migration can tell a page an instance made its own from the one
// we laid down. Same failure mode as the contract text itself.
func TestStockBacklogTextsRecognized(t *testing.T) {
	spine, ok := StockBacklogSpine(CurrentContractVersion())
	if !ok {
		t.Fatal("no stock backlog spine for the current contract")
	}
	tmpl, err := fs.ReadFile(afs.TemplateFS, "template/"+defaultBacklogSpine)
	if err != nil {
		t.Fatal(err)
	}
	if spine != string(tmpl) {
		t.Error("the current stock spine is not the template's backlog/INDEX.md")
	}
	vendoredSpine, ok := StockBacklogSpine("0.11.0")
	if !ok {
		t.Fatal("no vendored 0.11.0 stock backlog spine")
	}
	if vendoredSpine != string(tmpl) {
		t.Error("vendored backlog-INDEX-0.11.0.md is not byte-identical to template/backlog/INDEX.md")
	}
	if role := FrontmatterValueFromReader(strings.NewReader(spine), roleKey); role != RoleBacklog {
		t.Errorf("stock backlog spine declares agentsfs_role: %q, want %q", role, RoleBacklog)
	}

	page, ok := StockBacklogPage("0.10.0")
	if !ok {
		t.Fatal("no vendored 0.10.0 stock backlog page")
	}
	if role := FrontmatterValueFromReader(strings.NewReader(page), roleKey); role != RoleBacklog {
		t.Errorf("stock 0.10.0 backlog page declares agentsfs_role: %q, want %q", role, RoleBacklog)
	}
	if _, ok := StockBacklogPage("0.9.0"); ok {
		t.Error("a stock backlog page exists for 0.9.0, which predates the role")
	}
	// The page shape is retired: the template no longer ships one, so nothing
	// may answer for the current version with a page.
	if _, ok := StockBacklogPage(CurrentContractVersion()); ok {
		t.Error("a page-shaped stock backlog is still served for the current contract")
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
		"`backlog/` by default",
		"blocked by owner",
		"afs tasks",
		"afs task claim",
		"afs prime",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("bundled contract does not mention %q", want)
		}
	}
	if strings.Contains(contract, "page-level role") {
		t.Error("the bundled contract still calls the backlog a page-level role")
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
