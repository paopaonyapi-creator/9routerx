#!/usr/bin/env bash
# 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
# 9RouterX single-line installer: detects OS/arch, downloads the latest
# release binary from GitHub, installs it, and prints next steps.
#
#   curl -fsSL https://raw.githubusercontent.com/yanstd888/9routerx/main/install.sh | bash
#
# Env overrides: VERSION (e.g. v1.9.2, default: latest), BINDIR (default:
# /usr/local/bin, falls back to ~/.local/bin).
set -euo pipefail

REPO="${REPO:-yanstd888/9routerx}"
VERSION="${VERSION:-latest}"
BINDIR="${BINDIR:-/usr/local/bin}"

info()  { printf '\033[1;32m[9RouterX]\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33m[9RouterX]\033[0m %s\n' "$*" >&2; }
fatal() { printf '\033[1;31m[9RouterX]\033[0m %s\n' "$*" >&2; exit 1; }

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$OS" in
  linux|darwin) ;;
  *) fatal "unsupported OS: $OS (Linux and macOS only; Windows: download the .exe from https://github.com/$REPO/releases/latest)" ;;
esac
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) fatal "unsupported architecture: $ARCH (amd64/arm64 only)" ;;
esac

ASSET="9routerx-${OS}-${ARCH}"
# The release asset keeps its 9routerx-* name (CI, the updater, and
# scoreArchiveEntry all match it), but it is installed as `9routerx` — the
# name binaryName() reports and the service backends write into their units.
if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
info "downloading $URL ..."
curl -fsSL --retry 3 -o "$TMP/9routerx" "$URL" \
  || fatal "download failed — check https://github.com/$REPO/releases"
chmod +x "$TMP/9routerx"

if [ -w "$BINDIR" ]; then
  mv "$TMP/9routerx" "$BINDIR/9routerx"
else
  warn "$BINDIR not writable, trying sudo (or set BINDIR=\$HOME/.local/bin)"
  sudo mv "$TMP/9routerx" "$BINDIR/9routerx"
fi
trap - EXIT

info "installed to $BINDIR/9routerx"
"$BINDIR/9routerx" version || true
echo
info "start it with:"
echo "       9routerx"
echo "Dashboard: http://localhost:888 (defaults: port 888, data ~/.9router)"
