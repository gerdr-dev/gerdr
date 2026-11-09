#!/usr/bin/env bash
# Prints the shell id of the desktop shells (tools/chromeshell on macOS, tools/winshell on Windows): a hash of what is in
# their packages besides the Go backend (their code, the menu library, the bridge, the installer, the icon, and
# internal/selfupdate, which they compile in). The release puts it in update.json (desktop/feed.sh), the packages carry
# it (package.sh: Info.plist GerdrShell / -X main.shellID): same id = an update only needs the backend (~10MB), a
# different one = the whole package. Changing any of these files therefore makes the next release a full download.
# An Electron App (desktop/, retired) has another id, so its updater installs the whole package: that moves it over.
set -euo pipefail
cd "$(dirname "$0")/.."
{
  find tools/chromeshell tools/winshell internal/selfupdate -type f \
    \( -name '*.go' -o -name '*.js' -o -name '*.m' -o -name '*.sh' -o -name '*.nsi' -o -name '*.syso' -o -name '*.png' \) \
    ! -name '*_test.go' ! -path '*/dist/*' | LC_ALL=C sort
  echo desktop/res/icon.png
} | while read -r f; do printf '%s\n' "$f"; cat "$f"; done | shasum -a 256 | cut -c1-16
