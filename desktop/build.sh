#!/usr/bin/env bash
# The macOS backends for a release: the two compressed ones (desktop/dist/gerdr-<version>-darwin-<arch>.br) the
# updaters download when the shell didn't change, and the binaries (desktop/bin/gerdr-<arch>) the release then packs
# into the dmgs (BACKEND_DIR=desktop/bin tools/chromeshell/package.sh). release.yml runs it.
#   ./desktop/build.sh mac-backend <version>
# This folder held the Electron shell until 0.2.21; what's left is what the release still uses (this, compress.js,
# feed.sh, sign-id.sh, res/icon.png: its path is part of the shell id, tools/shell-id.sh, so it stays put).
set -euo pipefail
cd "$(dirname "$0")/.."
TARGET="${1:-}"
VERSION="${2:-0.1.0}"
VERSION="${VERSION#v}"
[ "$TARGET" = mac-backend ] || { echo "usage: $0 mac-backend <version>" >&2; exit 1; }
LDFLAGS="-s -w -X github.com/gerdr-dev/gerdr/internal/version.Version=$VERSION"

make -s ui
rm -rf desktop/bin && mkdir -p desktop/bin desktop/dist

# A fixed signing identity for the backend when this machine has one (desktop/sign-id.sh): keeps macOS privacy grants
# (Screen Recording / Accessibility for the remote screen) across rebuilds. None = the linker's ad-hoc signature.
SIGN_ID=$(./desktop/sign-id.sh || true)
export GERDR_SIGN_ID="${SIGN_ID:--}"
sign_backend() { [ -z "$SIGN_ID" ] || codesign --force --sign "$SIGN_ID" --identifier io.github.zbysir.gerdr.backend "$1"; }

# Brotli (desktop/compress.js) takes ~40s per backend, single-threaded: run them in the background, next to the
# other arch's build, and wait for all of them before finishing (a failed one fails the build)
pids=()
for arch in arm64 x64; do
  goarch=$arch && [ "$arch" = x64 ] && goarch=amd64
  GOOS=darwin GOARCH=$goarch go build -ldflags "$LDFLAGS" -o "desktop/bin/gerdr-$arch" ./cmd/gerdr
  sign_backend "desktop/bin/gerdr-$arch"
  node desktop/compress.js "desktop/bin/gerdr-$arch" "desktop/dist/gerdr-$VERSION-darwin-$arch.br" & pids+=($!)
done
for p in "${pids[@]}"; do wait "$p"; done
ls -1 desktop/dist/*.br
