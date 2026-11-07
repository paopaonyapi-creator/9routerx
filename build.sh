#!/usr/bin/env bash
# 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
# Build 9routerx reproducibly: web assets first (embedded at compile time),
# then the binary with the version from VERSION.
#
# web/dist is gitignored, so a fresh clone has no SPA to embed and the Go build
# fails on `//go:embed dist/*`. This script rebuilds it when missing.
set -euo pipefail
cd "$(dirname "$0")"

BUN="${BUN:-$(command -v bun || true)}"
if [[ -z "${BUN}" ]]; then
  for candidate in "$HOME/.bun/bin/bun" /tmp/occapture/bun-linux-x64/bun; do
    [[ -x "$candidate" ]] && BUN="$candidate" && break
  done
fi

if [[ ! -f web/dist/index.html ]]; then
  if [[ -z "${BUN}" ]]; then
    echo "error: web/dist missing and bun not found (set BUN=/path/to/bun)" >&2
    exit 1
  fi
  echo "==> building web assets"
  "$BUN" web/scripts/web-build.ts
fi

VERSION="$(cat VERSION)"
echo "==> building 9routerx ${VERSION}"
go build -ldflags="-s -w -X 9routerx/proxy/internal/updater.CurrentVersion=${VERSION}" \
  -o ./9routerx ./cmd/9routerx/
echo "==> ./9routerx"
