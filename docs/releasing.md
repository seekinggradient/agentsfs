# Releasing agentsfs

This repo supports three install paths:

- `curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh`
- `brew tap seekinggradient/agentsfs https://github.com/seekinggradient/agentsfs.git && brew install --HEAD seekinggradient/agentsfs/afs`
- source checkout with `go install ./cmd/afs`

## Curl installer

`install.sh` first tries to download the latest GitHub release asset for the user's OS and architecture:

- `afs_latest_darwin_arm64.tar.gz`
- `afs_latest_darwin_amd64.tar.gz`
- `afs_latest_linux_arm64.tar.gz`
- `afs_latest_linux_amd64.tar.gz`

It verifies `checksums.txt` when available, installs `afs`, and prints a `PATH` hint if needed.

If no release asset exists yet, it falls back to cloning the repo and building from source. That fallback needs Go and git.

## Homebrew

The current formula is a HEAD formula:

```sh
brew tap seekinggradient/agentsfs https://github.com/seekinggradient/agentsfs.git
brew install --HEAD seekinggradient/agentsfs/afs
```

This works before the first stable release because Homebrew builds from `main`.

Later, create a dedicated tap repo, usually `seekinggradient/homebrew-agentsfs`, and have GoReleaser publish a stable formula there. Homebrew's one-argument tap form expects GitHub tap repos to use the `homebrew-` prefix; the two-argument `brew tap owner/name URL` form works with this repo today.

## Cutting a release

The GitHub Actions release workflow runs GoReleaser on tags that start with `v`.

Before tagging:

```sh
go test ./...
gofmt -w $(find . -name '*.go' -not -path './dist/*')
git diff --check
```

Then:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The workflow creates a GitHub release with archives and `checksums.txt`.

Smoke-test the curl path after the workflow completes:

```sh
tmp="$(mktemp -d)"
AFS_INSTALL_DIR="$tmp" curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh
"$tmp/afs" version
```

Smoke-test Homebrew:

```sh
brew tap seekinggradient/agentsfs https://github.com/seekinggradient/agentsfs.git
brew reinstall --HEAD seekinggradient/agentsfs/afs
brew test afs
```

## Local release check

If GoReleaser is installed locally:

```sh
goreleaser check
goreleaser release --snapshot --clean
```

The snapshot command writes local artifacts under `dist/` without publishing.
