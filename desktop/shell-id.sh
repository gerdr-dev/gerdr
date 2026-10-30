#!/usr/bin/env bash
# Prints the shell id: a hash of everything in the App besides the Go backend (main.js, preload.js, updater.js,
# the Electron version pinned in package.json, icons). build.sh writes it into the packaged package.json (gerdrShell) and
# the release puts it in update.json: same id = an update only needs the backend (desktop/updater.js), a different
# one = the whole App. Changing any of these files therefore makes the next release a full ~140MB download.
set -euo pipefail
cd "$(dirname "$0")"
cat main.js preload.js updater.js package.json res/* | shasum -a 256 | cut -c1-16
