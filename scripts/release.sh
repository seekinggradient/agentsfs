#!/usr/bin/env bash
# Cross-compile afs for the platforms we care about. Single static
# binaries, no runtime deps — the whole distribution story is "download
# one file" until a package channel is decided.
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION=$(go run ./cmd/afs version 2>/dev/null | awk '{print $2}' || echo dev)
mkdir -p dist

for target in darwin/arm64 darwin/amd64 linux/arm64 linux/amd64; do
  GOOS=${target%/*} GOARCH=${target#*/} CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" \
    -o "dist/afs-${VERSION}-${target%/*}-${target#*/}" ./cmd/afs
  echo "built dist/afs-${VERSION}-${target%/*}-${target#*/}"
done
