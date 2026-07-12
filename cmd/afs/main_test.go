package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"agentsfs.ai/afs/internal/core"
	afsdocs "agentsfs.ai/afs/internal/docs"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_AFS_HELPER") != "1" {
		return
	}
	for i, a := range os.Args {
		if a == "--" {
			os.Args = append([]string{"afs"}, os.Args[i+1:]...)
			main()
			return
		}
	}
	os.Exit(2)
}

func TestSetupCreatesPersonalInstanceAndConnectsProject(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	runGit(t, project, "init", "-b", "main")

	out, err := runAFS(t, project, home, "setup", "--yes")
	if err != nil {
		t.Fatalf("afs setup failed: %v\n%s", err, out)
	}

	instance := filepath.Join(home, "agentsfs")
	if _, err := os.Stat(filepath.Join(instance, "AGENTS.md")); err != nil {
		t.Fatalf("personal agentsfs was not initialized: %v", err)
	}

	projectAgents, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatalf("project was not connected: %v", err)
	}
	if !strings.Contains(string(projectAgents), instance) {
		t.Fatalf("project AGENTS.md does not point at instance %q:\n%s", instance, projectAgents)
	}
}

func TestSetupSupportsDescriptiveCustomInstanceName(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	runGit(t, project, "init", "-b", "main")
	instance := filepath.Join(home, "AgentsFS-stocks")

	out, err := runAFS(t, project, home, "setup", instance, "--yes")
	if err != nil {
		t.Fatalf("afs setup with custom instance name failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(instance, ".agentsfs")); err != nil {
		t.Fatalf("custom-named agentsfs was not initialized: %v", err)
	}
	projectAgents, err := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projectAgents), instance) {
		t.Fatalf("project connection does not point at custom instance %q:\n%s", instance, projectAgents)
	}
}

func TestInitInsideGitRepoRequiresShared(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	runGit(t, project, "init", "-b", "main")

	out, err := runAFS(t, project, home, "init", "--yes")
	if err == nil {
		t.Fatalf("afs init inside repo unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, "afs setup ~/agentsfs") || !strings.Contains(out, "afs init ./agentsfs --shared") {
		t.Fatalf("failure did not explain the two safe choices:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(project, "agentsfs")); !os.IsNotExist(err) {
		t.Fatalf("init without --shared created project agentsfs unexpectedly: %v", err)
	}
}

func TestInitSharedAtRepoRootUsesAgentsfsSubdir(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	runGit(t, project, "init", "-b", "main")

	out, err := runAFS(t, project, home, "init", "--shared", "--yes")
	if err != nil {
		t.Fatalf("afs init --shared failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(project, "agentsfs", "AGENTS.md")); err != nil {
		t.Fatalf("shared agentsfs was not initialized in ./agentsfs: %v\n%s", err, out)
	}
}

func TestInitSharedOutsideGitRepoFails(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()

	out, err := runAFS(t, dir, home, "init", "notes", "--shared")
	if err == nil {
		t.Fatalf("afs init --shared outside repo unexpectedly succeeded:\n%s", out)
	}
	if !strings.Contains(out, "--shared only makes sense inside a git repo") {
		t.Fatalf("failure did not explain that --shared needs a repo:\n%s", out)
	}
}

// An older afs must refuse to "upgrade" an instance whose contract is newer
// than the binary's bundled one — that would silently downgrade it. The
// stale-contract notice and status must point at `afs update`, not upgrade.
func TestContractUpgradeRefusesToDowngradeNewerInstance(t *testing.T) {
	home := t.TempDir()
	inst := t.TempDir()
	if err := os.Mkdir(filepath.Join(inst, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(inst, "AGENTS.md"),
		"---\ndescription: Future root.\nagentsfs_contract: 99.0.0\n---\n# This folder is an agentsfs\n")

	out, err := runAFS(t, inst, home, "contract", "upgrade", inst, "--yes")
	if err == nil {
		t.Fatalf("upgrade should refuse to downgrade a newer instance:\n%s", out)
	}
	if !strings.Contains(out, "afs update") || !strings.Contains(out, "downgrade") {
		t.Fatalf("refusal should mention downgrade and `afs update`:\n%s", out)
	}
	got := core.ContractVersion(inst)
	if got != "99.0.0" {
		t.Fatalf("AGENTS.md was downgraded to %q despite the guard", got)
	}

	status, err := runAFS(t, inst, home, "contract", "status", inst)
	if err != nil {
		t.Fatalf("contract status failed: %v\n%s", err, status)
	}
	if !strings.Contains(status, "afs update") {
		t.Fatalf("status of an ahead-of-binary instance should point at `afs update`:\n%s", status)
	}
}

// A customized AGENTS.md (any byte difference from its version's stock text)
// makes `contract upgrade` refuse without --force, and the refusal points at
// `afs contract diff`. --force overwrites it.
func TestContractUpgradeRefusesCustomizedContract(t *testing.T) {
	home := t.TempDir()
	inst := t.TempDir()
	if err := os.Mkdir(filepath.Join(inst, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock030, ok := core.StockContract("0.3.0")
	if !ok {
		t.Fatal("no vendored 0.3.0 stock contract")
	}
	// One byte of adaptation appended — the contract is now customized.
	customized := stock030 + "\n## House rule\n\nAlways cite the policy number.\n"
	mustWriteFile(t, filepath.Join(inst, "AGENTS.md"), customized)

	out, err := runAFS(t, inst, home, "contract", "upgrade", inst, "--yes")
	if err == nil {
		t.Fatalf("upgrade should refuse a customized contract:\n%s", out)
	}
	if !strings.Contains(out, "customized") || !strings.Contains(out, "afs contract diff") {
		t.Fatalf("refusal should mention customization and `afs contract diff`:\n%s", out)
	}
	// Still customized (untouched).
	if got := core.ContractVersion(inst); got != "0.3.0" {
		t.Fatalf("customized AGENTS.md was changed despite the refusal: version now %q", got)
	}

	// --force overwrites it to the bundled contract.
	out, err = runAFS(t, inst, home, "contract", "upgrade", inst, "--yes", "--force")
	if err != nil {
		t.Fatalf("upgrade --force failed on a customized contract: %v\n%s", err, out)
	}
	if got := core.ContractVersion(inst); got != core.CurrentContractVersion() {
		t.Fatalf("upgrade --force did not bring AGENTS.md current: %q", got)
	}
}

// `afs contract diff` on a customized 0.3.0 instance prints both labeled
// sections: the instance's adaptations and what the new version changes.
func TestContractDiffShowsBothSections(t *testing.T) {
	home := t.TempDir()
	inst := t.TempDir()
	if err := os.Mkdir(filepath.Join(inst, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock030, _ := core.StockContract("0.3.0")
	customized := stock030 + "\n## House rule\n\nAlways cite the policy number.\n"
	mustWriteFile(t, filepath.Join(inst, "AGENTS.md"), customized)

	out, err := runAFS(t, inst, home, "contract", "diff", inst)
	if err != nil {
		t.Fatalf("contract diff failed: %v\n%s", err, out)
	}
	for _, want := range []string{"Your adaptations", "What " + core.CurrentContractVersion() + " changes", "House rule"} {
		if !strings.Contains(out, want) {
			t.Fatalf("contract diff output missing %q:\n%s", want, out)
		}
	}
}

// A stock 0.4.0 instance upgrades to the current bundled contract cleanly: the
// newly vendored 0.4.0 stock text lets the customized-contract guard pass, so
// upgrade proceeds without --force and bumps the version.
func TestContractUpgradeStock040Instance(t *testing.T) {
	home := t.TempDir()
	inst := t.TempDir()
	if err := os.Mkdir(filepath.Join(inst, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock040, ok := core.StockContract("0.4.0")
	if !ok {
		t.Fatal("no vendored 0.4.0 stock contract")
	}
	mustWriteFile(t, filepath.Join(inst, "AGENTS.md"), stock040)

	out, err := runAFS(t, inst, home, "contract", "upgrade", inst, "--yes")
	if err != nil {
		t.Fatalf("upgrade of a stock 0.4.0 instance failed: %v\n%s", err, out)
	}
	if got := core.ContractVersion(inst); got != core.CurrentContractVersion() {
		t.Fatalf("stock 0.4.0 instance not upgraded to current: %q", got)
	}
}

// A customized 0.4.0 AGENTS.md makes `contract upgrade` refuse without --force
// (guarded against the newly vendored 0.4.0 stock text), and `contract diff`
// renders both labeled sections for the 0.4.0 instance.
func TestContract040CustomizedRefusesAndDiffs(t *testing.T) {
	home := t.TempDir()
	inst := t.TempDir()
	if err := os.Mkdir(filepath.Join(inst, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	stock040, _ := core.StockContract("0.4.0")
	customized := stock040 + "\n## House rule\n\nAlways cite the policy number.\n"
	mustWriteFile(t, filepath.Join(inst, "AGENTS.md"), customized)

	out, err := runAFS(t, inst, home, "contract", "upgrade", inst, "--yes")
	if err == nil {
		t.Fatalf("upgrade should refuse a customized 0.4.0 contract:\n%s", out)
	}
	if !strings.Contains(out, "customized") || !strings.Contains(out, "afs contract diff") {
		t.Fatalf("refusal should mention customization and `afs contract diff`:\n%s", out)
	}
	if got := core.ContractVersion(inst); got != "0.4.0" {
		t.Fatalf("customized 0.4.0 AGENTS.md changed despite the refusal: version now %q", got)
	}

	diff, err := runAFS(t, inst, home, "contract", "diff", inst)
	if err != nil {
		t.Fatalf("contract diff failed for a 0.4.0 instance: %v\n%s", err, diff)
	}
	for _, want := range []string{"Your adaptations", "What " + core.CurrentContractVersion() + " changes", "House rule"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("contract diff for a 0.4.0 instance missing %q:\n%s", want, diff)
		}
	}
}

// `afs connect` writes the resolved journal path into the block's trigger
// line: the default agent-journal/ for a fresh instance, and the marked dir
// when the journal has been relocated.
func TestConnectWritesResolvedJournalPath(t *testing.T) {
	home := t.TempDir()

	// Fresh instance → default agent-journal/.
	fresh := filepath.Join(home, "kb-fresh")
	if _, err := runAFS(t, home, home, "init", fresh, "--yes"); err != nil {
		t.Fatalf("init failed")
	}
	project := t.TempDir()
	runGit(t, project, "init", "-b", "main")
	if out, err := runAFS(t, project, home, "connect", fresh, "--yes"); err != nil {
		t.Fatalf("connect failed: %v\n%s", err, out)
	}
	block, _ := os.ReadFile(filepath.Join(project, "AGENTS.md"))
	if !strings.Contains(string(block), fresh+"/agent-journal/") {
		t.Fatalf("fresh connect block did not use the default journal path:\n%s", block)
	}

	// Relocated journal (marked under a non-default name) → that path.
	reloc := filepath.Join(home, "kb-reloc")
	if _, err := runAFS(t, home, home, "init", reloc, "--yes"); err != nil {
		t.Fatalf("init reloc failed")
	}
	// Remove the default journal and mark a differently-named dir.
	if err := os.RemoveAll(filepath.Join(reloc, "agent-journal")); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(reloc, "sessions", "INDEX.md"),
		"---\ndescription: Session journal.\nagentsfs_role: journal\n---\n")
	project2 := t.TempDir()
	runGit(t, project2, "init", "-b", "main")
	if out, err := runAFS(t, project2, home, "connect", reloc, "--yes"); err != nil {
		t.Fatalf("connect reloc failed: %v\n%s", err, out)
	}
	block2, _ := os.ReadFile(filepath.Join(project2, "AGENTS.md"))
	if !strings.Contains(string(block2), reloc+"/sessions/") {
		t.Fatalf("relocated connect block did not resolve the marked journal path:\n%s", block2)
	}
}

func TestHelpDoesNotAdvertiseHostedCommands(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	out, err := runAFS(t, project, home, "help")
	if err != nil {
		t.Fatalf("afs help failed: %v\n%s", err, out)
	}
	for _, unwanted := range []string{"afs login", "afs hosted", "hosted sync", "backup/restore"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("help still advertised %q:\n%s", unwanted, out)
		}
	}
}

func TestHelpDocumentsUninstallCommand(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	out, err := runAFS(t, project, home, "help")
	if err != nil {
		t.Fatalf("afs help failed: %v\n%s", err, out)
	}
	for _, want := range []string{"afs uninstall", "Never deletes any agentsfs", "filesystem or git data"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help did not contain %q:\n%s", want, out)
		}
	}
}

func TestHelpDocumentsDocsCommand(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	out, err := runAFS(t, project, home, "help")
	if err != nil {
		t.Fatalf("afs help failed: %v\n%s", err, out)
	}
	for _, want := range []string{"afs docs", "afs docs agent-start"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help did not contain %q:\n%s", want, out)
		}
	}
}

func TestCommandDocsCoverDispatch(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, "switch os.Args[1]")
	end := strings.Index(text, "func runDocs")
	if start < 0 || end < start {
		t.Fatalf("could not isolate top-level command dispatch in main.go")
	}
	dispatch := text[start:end]

	documented := map[string]bool{}
	for _, cmd := range afsdocs.Commands() {
		fields := strings.Fields(cmd.Usage)
		if len(fields) >= 2 {
			documented[fields[1]] = true
		}
	}
	ignore := map[string]bool{
		"register": true, // deprecated alias for connect
		"help":     true,
	}
	re := regexp.MustCompile(`case "([^"]+)"`)
	for _, match := range re.FindAllStringSubmatch(dispatch, -1) {
		name := match[1]
		if ignore[name] {
			continue
		}
		if !documented[name] {
			t.Fatalf("command %q is dispatched by the CLI but missing from internal/docs command registry", name)
		}
	}
}

func TestStatusDiscoversMultipleInstancesAsJSON(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	for _, name := range []string{"AgentsFS-personal", "nested/AgentsFS-stocks"} {
		root := filepath.Join(workspace, name)
		if err := os.MkdirAll(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWriteFile(t, filepath.Join(root, "AGENTS.md"), "---\ndescription: Test instance.\nagentsfs_contract: "+core.CurrentContractVersion()+"\n---\n# This folder is an agentsfs\n")
	}

	out, err := runAFS(t, workspace, home, "status", workspace, "--json")
	if err != nil {
		t.Fatalf("afs status failed: %v\n%s", err, out)
	}
	var report core.StatusReport
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&report); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, out)
	}
	if len(report.Instances) != 2 {
		t.Fatalf("status found %d instances, want 2: %+v", len(report.Instances), report.Instances)
	}
	for _, instance := range report.Instances {
		if instance.ContractState != "current" {
			t.Fatalf("instance status = %+v, want current contract", instance)
		}
	}
}

func TestStatusWithoutPathFindsEnclosingInstance(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "AgentsFS-custom")
	inside := filepath.Join(root, "projects", "deep")
	if err := os.MkdirAll(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, "AGENTS.md"), "---\ndescription: Test instance.\nagentsfs_contract: "+core.CurrentContractVersion()+"\n---\n# This folder is an agentsfs\n")

	out, err := runAFS(t, inside, home, "status", "--json")
	if err != nil {
		t.Fatalf("afs status failed: %v\n%s", err, out)
	}
	var report core.StatusReport
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&report); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, out)
	}
	canonical, _ := filepath.EvalSymlinks(root)
	if len(report.Instances) != 1 || report.Instances[0].Path != canonical {
		t.Fatalf("status did not resolve enclosing instance %q: %+v", canonical, report.Instances)
	}
}

func TestStatusDoesNotPerformImplicitNetworkUpdateCheck(t *testing.T) {
	if updateNotificationCommand("status", nil) {
		t.Fatal("afs status must remain local unless --fetch is explicit")
	}
	if !updateNotificationCommand("doctor", nil) {
		t.Fatal("doctor should retain the existing once-daily update notification")
	}
}

func TestStatusHumanOutputNarratesScope(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	root := filepath.Join(workspace, "AgentsFS-personal")
	if err := os.MkdirAll(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, "AGENTS.md"), "---\ndescription: Test instance.\nagentsfs_contract: "+core.CurrentContractVersion()+"\n---\n# This folder is an agentsfs\n")

	out, err := runAFS(t, workspace, home, "status", workspace)
	if err != nil {
		t.Fatalf("afs status failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Status scope: AgentsFS instances discoverable within:",
		"Pass a different directory, or several directories, to broaden or narrow this view.",
		"Scan complete",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestDocsAgentStartWorksOutsideInstance(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	out, err := runAFS(t, project, home, "docs", "agent-start")
	if err != nil {
		t.Fatalf("afs docs agent-start failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"What AgentsFS is",
		"Why it helps",
		"Do not run setup commands until the user answers",
		"afs status ~",
		"afs setup --yes",
		"Do not ask the user to design the knowledge-base taxonomy",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("agent-start docs did not contain %q:\n%s", want, out)
		}
	}
}

func TestEmbeddingsSetupWritesUserConfig(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	out, err := runAFSWithInputEnv(t, project, home, "sk-test\n", nil, "embeddings", "setup", "openai", "--yes")
	if err != nil {
		t.Fatalf("afs embeddings setup failed: %v\n%s", err, out)
	}
	configPath := filepath.Join(home, ".config", "agentsfs", "embeddings.env")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("embedding config was not written: %v\n%s", err, out)
	}
	text := string(data)
	for _, want := range []string{"AFS_EMBED_PROVIDER='openai'", "OPENAI_API_KEY='sk-test'"} {
		if !strings.Contains(text, want) {
			t.Fatalf("embedding config missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(out, "sk-test") {
		t.Fatalf("setup output leaked the key:\n%s", out)
	}

	out, err = runAFS(t, project, home, "embeddings", "status")
	if err != nil {
		t.Fatalf("afs embeddings status failed: %v\n%s", err, out)
	}
	for _, want := range []string{"embedding provider: openai", "key: OPENAI_API_KEY", configPath} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
}

func TestEmbeddingsClearRemovesUserConfig(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	if out, err := runAFSWithInputEnv(t, project, home, "sk-test\n", nil, "embeddings", "setup", "openai", "--yes"); err != nil {
		t.Fatalf("afs embeddings setup failed: %v\n%s", err, out)
	}
	out, err := runAFS(t, project, home, "embeddings", "clear", "--yes")
	if err != nil {
		t.Fatalf("afs embeddings clear failed: %v\n%s", err, out)
	}
	configPath := filepath.Join(home, ".config", "agentsfs", "embeddings.env")
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("embedding config still exists after clear: %v", err)
	}
	if !strings.Contains(out, "Removed embedding config") {
		t.Fatalf("clear did not report removal:\n%s", out)
	}
}

func TestSearchRendersReadableResults(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".agentsfs"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(root, "AGENTS.md"), "# This folder is an agentsfs\n")
	mustWriteFile(t, filepath.Join(root, "notes.md"), "---\ndescription: Claim note.\n---\n# Claim\n\n## Next actions\n\nSend the bank statement before the deadline.\n")

	out, err := runAFS(t, root, home, "search", "bank statement")
	if err != nil {
		t.Fatalf("afs search failed: %v\n%s", err, out)
	}
	for _, want := range []string{"1. notes.md", "section: Next actions", "Send the"} {
		if !strings.Contains(out, want) {
			t.Fatalf("search output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, " § ") {
		t.Fatalf("search output still uses old separator:\n%s", out)
	}
}

func TestUninstallRemovesBinaryButKeepsData(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "afs")
	instance := filepath.Join(home, "agentsfs")
	mustWriteFile(t, binary, "#!/bin/sh\n")
	mustWriteFile(t, filepath.Join(instance, "AGENTS.md"), "# Memory\n")

	out, err := runAFS(t, project, home, "uninstall", "--yes", "--binary", binary)
	if err != nil {
		t.Fatalf("afs uninstall failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(binary); !os.IsNotExist(err) {
		t.Fatalf("uninstall did not remove binary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(instance, "AGENTS.md")); err != nil {
		t.Fatalf("uninstall removed agentsfs data: %v", err)
	}
	if !strings.Contains(out, "Did not delete any agentsfs filesystem") {
		t.Fatalf("uninstall did not explain data safety:\n%s", out)
	}
}

func TestUninstallDryRunDoesNotRemoveFiles(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "afs")
	mustWriteFile(t, binary, "#!/bin/sh\n")

	out, err := runAFS(t, project, home, "uninstall", "--dry-run", "--binary", binary)
	if err != nil {
		t.Fatalf("afs uninstall --dry-run failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("dry run removed binary: %v", err)
	}
	if !strings.Contains(out, "Dry run only") {
		t.Fatalf("dry run did not report no changes:\n%s", out)
	}
}

func TestUninstallCanRemoveGlobalConnectionBlocks(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "afs")
	instance := filepath.Join(home, "agentsfs")
	globalConfig := filepath.Join(home, ".codex", "AGENTS.md")
	mustWriteFile(t, binary, "#!/bin/sh\n")
	mustWriteFile(t, globalConfig, "before\n\n"+core.ConnectionBlock(instance)+"\n\nafter\n")

	out, err := runAFS(t, project, home, "uninstall", "--yes", "--remove-global-connections", "--binary", binary)
	if err != nil {
		t.Fatalf("afs uninstall failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(globalConfig)
	if err != nil {
		t.Fatalf("global config missing: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "agentsfs:begin") || strings.Contains(text, instance) {
		t.Fatalf("global agentsfs block was not removed:\n%s", text)
	}
	if !strings.Contains(text, "before") || !strings.Contains(text, "after") {
		t.Fatalf("global config content was not preserved:\n%s", text)
	}
}

func TestUninstallRefusesNonAfsBinaryOverride(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "not-afs")
	mustWriteFile(t, binary, "#!/bin/sh\n")

	out, err := runAFS(t, project, home, "uninstall", "--yes", "--binary", binary)
	if err == nil {
		t.Fatalf("afs uninstall unexpectedly removed non-afs binary:\n%s", out)
	}
	if !strings.Contains(out, "binary name must be afs") {
		t.Fatalf("failure did not explain binary validation:\n%s", out)
	}
}

func runAFS(t *testing.T, dir, home string, args ...string) (string, error) {
	return runAFSWithInputEnv(t, dir, home, "", nil, args...)
}

func runAFSWithInputEnv(t *testing.T, dir, home, stdin string, extraEnv []string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{"-test.run=TestHelperProcess", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Env = append(os.Environ(),
		"GO_WANT_AFS_HELPER=1",
		"HOME="+home,
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"AFS_NO_UPDATE_CHECK=1",
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
