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

func UpgradeContract(root string) error {
	contract, err := BundledContract()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(contract), 0o644)
}
