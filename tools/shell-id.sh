#!/usr/bin/env bash
# Prints the shell id of the desktop App (Electron, desktop/: macOS and Windows): a hash of what is in its packages
# besides the Go backend: the frozen loader (main.js, preload.js), package.json with its Electron version, the packaging
# and installer, the icon. The core and the App's main-process script ship inside the backend (internal/desktop,
# docs/dev/SHELL.md), so they update with it and aren't part of this. The release puts it in update.json
# (desktop/feed.sh), the packages carry it (gerdr-app.json, pack.js): same id = an update only needs the backend
# (~10MB), a different one = the whole package. Changing any of these files makes the next release a full download.
set -euo pipefail
cd "$(dirname "$0")/.."
{
  find desktop/mkico -type f \
    \( -name '*.go' -o -name '*.js' -o -name '*.m' -o -name '*.sh' -o -name '*.nsi' -o -name '*.syso' -o -name '*.png' \) \
    ! -name '*_test.go' ! -path '*/dist/*' | LC_ALL=C sort
  echo desktop/res/icon.png
  printf '%s\n' desktop/main.js desktop/preload.js desktop/package.json desktop/pack.js desktop/package.sh desktop/package-win.sh desktop/installer.nsi
} | {
  while read -r f; do printf '%s\n' "$f"; cat "$f"; done
} | shasum -a 256 | cut -c1-16
