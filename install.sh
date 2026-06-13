#!/bin/sh
set -eu

repo="${AFS_REPO_URL:-https://github.com/seekinggradient/agentsfs}"
git_repo="${AFS_GIT_REPO_URL:-${repo}.git}"
git_repo_ssh="${AFS_GIT_REPO_SSH_URL:-git@github.com:seekinggradient/agentsfs.git}"
ref="${AFS_REF:-main}"
install_dir="${AFS_INSTALL_DIR:-}"

usage() {
  cat <<'EOF'
Install afs.

Usage:
  curl -fsSL https://raw.githubusercontent.com/seekinggradient/agentsfs/main/install.sh | sh

Environment:
  AFS_INSTALL_DIR       Directory for the afs binary.
  AFS_REPO_URL          HTTPS GitHub repo URL without .git.
  AFS_GIT_REPO_URL      Git clone URL for source fallback.
  AFS_GIT_REPO_SSH_URL  SSH clone URL fallback when HTTPS is unavailable.
  AFS_REF               Branch, tag, or commit for source fallback. Default: main.
EOF
}

case "${1:-}" in
  -h|--help)
    usage
    exit 0
    ;;
  "")
    ;;
  *)
    echo "afs installer: unknown argument: $1" >&2
    usage >&2
    exit 2
    ;;
esac

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "afs installer: missing required command: $1" >&2
    exit 1
  fi
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    *)
      echo "afs installer: unsupported OS: $(uname -s)" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    arm64|aarch64) echo "arm64" ;;
    x86_64|amd64) echo "amd64" ;;
    *)
      echo "afs installer: unsupported architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

download() {
  url="$1"
  dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$dest"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$url" -O "$dest"
  else
    echo "afs installer: need curl or wget to download releases" >&2
    return 1
  fi
}

install_from_release() {
  os="$(detect_os)"
  arch="$(detect_arch)"
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/agentsfs-install.XXXXXX")"
  trap 'rm -rf "$tmp"' EXIT INT TERM

  archive="$tmp/afs.tar.gz"
  checksums="$tmp/checksums.txt"
  asset="afs_latest_${os}_${arch}.tar.gz"
  url="${repo}/releases/latest/download/${asset}"

  echo "afs installer: trying release asset ${asset}"
  if ! download "$url" "$archive"; then
    return 1
  fi
  if download "${repo}/releases/latest/download/checksums.txt" "$checksums"; then
    expected="$(grep " ${asset}\$" "$checksums" | awk '{print $1}' || true)"
    if [ -n "$expected" ]; then
      if command -v shasum >/dev/null 2>&1; then
        actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
      elif command -v sha256sum >/dev/null 2>&1; then
        actual="$(sha256sum "$archive" | awk '{print $1}')"
      else
        echo "afs installer: no sha256 tool found; skipping checksum verification" >&2
        actual="$expected"
      fi
      if [ "$expected" != "$actual" ]; then
        echo "afs installer: checksum mismatch for ${asset}" >&2
        exit 1
      fi
    fi
  fi

  tar -xzf "$archive" -C "$tmp"
  mkdir -p "$install_dir"
  install "$tmp/afs" "$install_dir/afs"
  "$install_dir/afs" version
}

install_from_source() {
  need git
  need go

  tmp="$(mktemp -d "${TMPDIR:-/tmp}/agentsfs-build.XXXXXX")"
  trap 'rm -rf "$tmp"' EXIT INT TERM

  echo "afs installer: building from source (${git_repo}@${ref})"
  if ! git clone --quiet --depth 1 --branch "$ref" "$git_repo" "$tmp" 2>/dev/null; then
    if ! git clone --quiet --depth 1 --branch "$ref" "$git_repo_ssh" "$tmp" 2>/dev/null; then
      git clone --quiet "$git_repo" "$tmp" || git clone --quiet "$git_repo_ssh" "$tmp"
    fi
  fi
  if [ ! -d "$tmp/.git" ]; then
    echo "afs installer: clone failed" >&2
    exit 1
  fi
  (
    cd "$tmp"
    if ! git rev-parse --verify --quiet "$ref^{commit}" >/dev/null; then
      git fetch --quiet --depth 1 origin "$ref"
      git checkout --quiet FETCH_HEAD
    elif [ "$ref" != "main" ]; then
      git checkout --quiet "$ref"
    fi
  )

  mkdir -p "$install_dir"
  (
    cd "$tmp"
    GOBIN="$install_dir" go install ./cmd/afs
  )
  "$install_dir/afs" version
}

if [ -z "$install_dir" ]; then
  if command -v go >/dev/null 2>&1; then
    gobin="$(go env GOBIN)"
    if [ -n "$gobin" ]; then
      install_dir="$gobin"
    else
      install_dir="$(go env GOPATH)/bin"
    fi
  else
    install_dir="${HOME}/.local/bin"
  fi
fi

if ! install_from_release; then
  echo "afs installer: release asset unavailable; falling back to source install"
  install_from_source
fi

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    echo
    echo "afs installed to $install_dir."
    echo "Add it to your PATH, for example:"
    echo "  export PATH=\"$install_dir:\$PATH\""
    ;;
esac
