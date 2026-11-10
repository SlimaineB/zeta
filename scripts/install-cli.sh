#!/usr/bin/env bash
set -eu

REPO="${ZETA_REPO:-SlimaineB/zeta}"
VERSION="${ZETA_VERSION:-latest}"
BIN_DIR="${ZETA_BIN_DIR:-$HOME/.local/bin}"

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: $1 is required" >&2
    exit 1
  fi
}

need uname

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  linux*) os=linux ;;
  darwin*) os=darwin ;;
  mingw*|msys*|cygwin*) os=windows ;;
  *)
    echo "error: unsupported operating system: $os" >&2
    exit 1
    ;;
esac

arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *)
    echo "error: unsupported architecture: $arch" >&2
    exit 1
    ;;
esac

ext=""
bin_name="zeta"
if [ "$os" = "windows" ]; then
  ext=".exe"
  bin_name="zeta.exe"
fi
asset="zeta-${os}-${arch}${ext}"

if [ "$VERSION" = "latest" ]; then
  base="https://github.com/${REPO}/releases/latest/download"
else
  case "$VERSION" in
    v*) ;;
    *) VERSION="v${VERSION}" ;;
  esac
  base="https://github.com/${REPO}/releases/download/${VERSION}"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$2" "$1"
  else
    echo "error: curl or wget is required" >&2
    exit 1
  fi
}

echo "==> Downloading ${asset}"
download "${base}/${asset}" "${tmp}/${asset}"

if download "${base}/checksums.txt" "${tmp}/checksums.txt" 2>/dev/null; then
  expected="$(awk -v f="$asset" '$2 == f {print $1}' "${tmp}/checksums.txt" | head -n1)"
  if [ -n "$expected" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      actual="$(sha256sum "${tmp}/${asset}" | awk '{print $1}')"
    else
      actual="$(shasum -a 256 "${tmp}/${asset}" | awk '{print $1}')"
    fi
    if [ "$expected" != "$actual" ]; then
      echo "error: checksum mismatch for ${asset}" >&2
      exit 1
    fi
  fi
fi

mkdir -p "$BIN_DIR"
cp "${tmp}/${asset}" "${BIN_DIR}/${bin_name}"
chmod 0755 "${BIN_DIR}/${bin_name}"
echo "==> Installed ${BIN_DIR}/${bin_name}"

case ":$PATH:" in
  *":${BIN_DIR}:"*) ;;
  *)
    echo
    echo "Add this to your shell profile:"
    echo "  export PATH=\"${BIN_DIR}:\$PATH\""
    ;;
esac

echo
echo "Next:"
echo "  zeta login --server http://zeta.local --email dev@example.com --password dev"
