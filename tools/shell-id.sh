#!/usr/bin/env bash
# Prints the shell id of the desktop shells (tools/chromeshell on macOS, tools/winshell on Windows): a hash of what is in
# their packages besides the Go backend (their code, the bridge, the installer, the icon, internal/selfupdate, which
# they compile in, and the version of Lightron they are built on, from go.mod). The release puts it in update.json (desktop/feed.sh), the packages carry
# it (package.sh: Info.plist GerdrShell / -X main.shellID): same id = an update only needs the backend (~10MB), a
# different one = the whole package. Changing any of these files therefore makes the next release a full download.
# The Electron App (desktop/main.js, preload.js, package.json: its Electron version; desktop/core) is in it too.
set -euo pipefail
cd "$(dirname "$0")/.."
{
  find tools/chromeshell tools/winshell internal/selfupdate desktop/core -type f \
    \( -name '*.go' -o -name '*.js' -o -name '*.m' -o -name '*.sh' -o -name '*.nsi' -o -name '*.syso' -o -name '*.png' \) \
    ! -name '*_test.go' ! -path '*/dist/*' | LC_ALL=C sort
  echo desktop/res/icon.png
  printf '%s\n' desktop/main.js desktop/preload.js desktop/package.json desktop/pack.js desktop/package.sh
} | {
  while read -r f; do printf '%s\n' "$f"; cat "$f"; done
  grep 'github.com/zbysir/lightron' go.mod
} | shasum -a 256 | cut -c1-16
