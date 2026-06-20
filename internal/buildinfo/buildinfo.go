package buildinfo

import (
	"runtime/debug"
	"strings"
)

const (
	Version         = "0.1.0"
	ContractVersion = "0.2.0"
	RepoURL         = "https://github.com/seekinggradient/agentsfs"
	GitRepoURL      = RepoURL + ".git"
	GitRepoSSHURL   = "git@github.com:seekinggradient/agentsfs.git"
	Ref             = "main"
	InstallScript   = "https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh"
)

func VCSRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return strings.TrimSpace(setting.Value)
		}
	}
	return ""
}

func VCSModified() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.modified" && setting.Value == "true" {
			return true
		}
	}
	return false
}

func ShortRevision(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}
