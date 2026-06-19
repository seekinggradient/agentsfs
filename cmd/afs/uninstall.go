package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"agentsfs.ai/afs/internal/core"
)

const uninstallUsage = `afs uninstall — remove the CLI without deleting your data

Usage:
  afs uninstall [--yes] [--dry-run] [--binary PATH] [--remove-global-connections]

By default this removes:
  - the installed afs binary, when it is in a user install directory

It never deletes any agentsfs filesystem, git repo, or project-local
AGENTS.md / CLAUDE.md connection block. Use --remove-global-connections to
also remove agentsfs blocks from known global Claude/Codex harness config files.`

type uninstallOptions struct {
	yes                     bool
	dryRun                  bool
	removeGlobalConnections bool
	binaryOverride          string
}

func runUninstall(args []string) {
	var opts uninstallOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--yes", "-y":
			opts.yes = true
		case "--dry-run":
			opts.dryRun = true
		case "--remove-global-connections":
			opts.removeGlobalConnections = true
		case "--binary":
			i++
			if i >= len(args) {
				fail(fmt.Errorf("--binary needs a path"))
			}
			opts.binaryOverride = args[i]
		case "--help", "-h":
			fmt.Println(uninstallUsage)
			return
		default:
			if strings.HasPrefix(args[i], "-") {
				fail(fmt.Errorf("unknown flag %q for uninstall", args[i]))
			}
			fail(fmt.Errorf("usage: afs uninstall [--yes] [--dry-run] [--binary PATH] [--remove-global-connections]"))
		}
	}

	plan, err := buildUninstallPlan(opts)
	if err != nil {
		fail(err)
	}
	printUninstallPlan(plan, opts)
	if len(plan.actions) == 0 {
		fmt.Println("Nothing to uninstall. No agentsfs data was touched.")
		return
	}
	if opts.dryRun {
		fmt.Println("Dry run only. No files were changed.")
		return
	}
	if !opts.yes && !confirm("Proceed with uninstall?") {
		fmt.Println("Canceled. No files were changed.")
		return
	}
	if err := applyUninstallPlan(plan); err != nil {
		fail(err)
	}
	fmt.Println("Uninstall complete.")
	fmt.Println("Did not delete any agentsfs filesystem, git repo, or project-local connection block.")
}

type uninstallPlan struct {
	binaryPath      string
	binaryNote      string
	globalTargets   []core.Target
	removeGlobal    bool
	actions         []string
	nonFatalNotices []string
}

func buildUninstallPlan(opts uninstallOptions) (uninstallPlan, error) {
	var plan uninstallPlan
	plan.removeGlobal = opts.removeGlobalConnections

	binaryPath, binaryNote, err := uninstallBinaryCandidate(opts.binaryOverride)
	if err != nil {
		return plan, err
	}
	plan.binaryPath = binaryPath
	plan.binaryNote = binaryNote
	if binaryPath != "" {
		plan.actions = append(plan.actions, "remove binary "+binaryPath)
	} else if binaryNote != "" {
		plan.nonFatalNotices = append(plan.nonFatalNotices, binaryNote)
	}

	if opts.removeGlobalConnections {
		for _, target := range core.GlobalTargets() {
			if fileExistsForUninstall(target.Path) {
				plan.globalTargets = append(plan.globalTargets, target)
				plan.actions = append(plan.actions, "remove global agentsfs connection blocks from "+target.Path)
			}
		}
	}

	return plan, nil
}

func printUninstallPlan(plan uninstallPlan, opts uninstallOptions) {
	if len(plan.actions) > 0 {
		fmt.Println("This will:")
		for _, action := range plan.actions {
			fmt.Println("  - " + action)
		}
	}
	for _, notice := range plan.nonFatalNotices {
		fmt.Println("Note: " + notice)
	}
	if opts.removeGlobalConnections && len(plan.globalTargets) == 0 {
		fmt.Println("No global agentsfs connection blocks found in known harness config files.")
	}
	fmt.Println("This will NOT delete any agentsfs filesystem, git repo, or project-local connection block.")
}

func applyUninstallPlan(plan uninstallPlan) error {
	for _, target := range plan.globalTargets {
		removed, err := core.DisconnectAll(target.Path)
		if err != nil {
			return err
		}
		if removed > 0 {
			fmt.Printf("Removed %d agentsfs block(s) from %s\n", removed, target.Path)
		}
	}
	if plan.binaryPath != "" {
		if err := removeIfExists(plan.binaryPath); err != nil {
			return err
		}
	}
	return nil
}

func uninstallBinaryCandidate(override string) (string, string, error) {
	if override != "" {
		path, err := filepath.Abs(override)
		if err != nil {
			return "", "", err
		}
		if err := validateUninstallBinary(path, true); err != nil {
			return "", "", err
		}
		return path, "", nil
	}

	if exe, err := os.Executable(); err == nil {
		exe = cleanExecutablePath(exe)
		if validateUninstallBinary(exe, false) == nil && isUserInstallPath(exe) && !isTempExecutablePath(exe) {
			return exe, "", nil
		}
	}

	path, err := exec.LookPath("afs")
	if err != nil {
		return "", "afs was not found on PATH; remove any package-manager install manually if needed", nil
	}
	path = cleanExecutablePath(path)
	if err := validateUninstallBinary(path, false); err != nil {
		return "", err.Error(), nil
	}
	if !isUserInstallPath(path) {
		return "", fmt.Sprintf("afs on PATH is %s; it looks package-manager or system managed, so uninstall it with that manager or pass --binary PATH", path), nil
	}
	if isTempExecutablePath(path) {
		return "", fmt.Sprintf("afs on PATH is a temporary executable at %s; not removing it", path), nil
	}
	return path, "", nil
}

func validateUninstallBinary(path string, explicit bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, not an afs binary", path)
	}
	name := filepath.Base(path)
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	}
	if name != "afs" {
		if explicit {
			return fmt.Errorf("refusing to remove %s: binary name must be afs", path)
		}
		return fmt.Errorf("%s does not look like an afs binary", path)
	}
	return nil
}

func cleanExecutablePath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func isUserInstallPath(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	for _, dir := range []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "go", "bin"),
	} {
		if pathInDir(path, dir) {
			return true
		}
	}
	if gobin := os.Getenv("GOBIN"); gobin != "" && pathInDir(path, gobin) {
		return true
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" && pathInDir(path, filepath.Join(gopath, "bin")) {
		return true
	}
	return false
}

func pathInDir(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func isTempExecutablePath(path string) bool {
	temp := filepath.Clean(os.TempDir())
	return pathInDir(path, temp) || strings.Contains(path, string(os.PathSeparator)+"go-build")
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func fileExistsForUninstall(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
