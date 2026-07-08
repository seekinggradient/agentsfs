package core

import (
	"io/fs"
	"os"
	"path/filepath"

	afs "agentsfs.ai/afs"
	"agentsfs.ai/afs/internal/buildinfo"
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

// UpgradeContract rewrites AGENTS.md from the bundled template and lays down
// any reserved directories the template adds that a pre-existing instance
// lacks — currently journal/INDEX.md (added in contract 0.3.0). It reports
// the relative paths it created (beyond AGENTS.md) so the caller can narrate
// the diff. Existing files are never overwritten.
func UpgradeContract(root string) ([]string, error) {
	contract, err := BundledContract()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(contract), 0o644); err != nil {
		return nil, err
	}
	var created []string
	for _, rel := range []string{"journal/INDEX.md"} {
		made, err := layDownBundledFile(root, rel)
		if err != nil {
			return created, err
		}
		if made {
			created = append(created, rel)
		}
	}
	return created, nil
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
