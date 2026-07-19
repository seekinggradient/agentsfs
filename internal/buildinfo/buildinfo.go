package buildinfo

import (
	"runtime/debug"
	"strconv"
	"strings"
)

// Version is the CLI release version. Release builds override it via
// GoReleaser ldflags (-X …buildinfo.Version={{.Version}}) so binaries
// self-report their release tag; source builds report this checked-in
// default, which is kept equal to the latest tag at release time.
var Version = "0.7.0"

const (
	ContractVersion = "0.8.0"
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

// CompareVersions orders two dotted numeric version strings ("0.3.0"),
// returning -1, 0, or +1 (a < b, a == b, a > b). Missing components read
// as 0 (so "0.3" == "0.3.0") and a non-numeric component sorts as 0 rather
// than erroring — the CLI and contract versions this compares are always
// plain x.y.z.
func CompareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		x, y := versionPart(as, i), versionPart(bs, i)
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(parts[i]))
	return n
}
