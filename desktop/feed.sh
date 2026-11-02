#!/usr/bin/env bash
# Writes update.json for desktop/updater.js from the packages in a directory (the release's files):
#   ./desktop/feed.sh <version> <dir>
# backend = the brotli-compressed Go backend per platform (enough when the shell didn't change), app = the whole installer.
set -euo pipefail
VERSION="${1#v}"
DIR="$2"
SHELL_ID=$("$(dirname "$0")/shell-id.sh")
cd "$DIR"
entry() { printf '"%s":{"file":"%s","size":%d,"sha256":"%s"}' "$1" "$2" "$(wc -c < "$2" | tr -d ' ')" "$(shasum -a 256 "$2" | cut -d' ' -f1)"; }
join() { local IFS=,; echo "$*"; }
backend=() app=()
for p in darwin-arm64 darwin-x64 win32-x64; do
  [ -f "gerdr-$VERSION-$p.br" ] && backend+=("$(entry "$p" "gerdr-$VERSION-$p.br")")
done
# The installers may be an older version's, carried over by a release that didn't rebuild the shell (release.yml):
# they're matched by platform, not by $VERSION. Exactly one per platform.
one() {
  local m=() f
  for f in "$@"; do [ -f "$f" ] && m+=("$f"); done
  [ ${#m[@]} -le 1 ] || { echo "more than one installer: ${m[*]}" >&2; exit 1; }
  [ ${#m[@]} -eq 1 ] && echo "${m[0]}" || true
}
f=$(one Gerdr-*-mac-arm64.dmg); [ -n "$f" ] && app+=("$(entry darwin-arm64 "$f")")
f=$(one Gerdr-*-mac-x64.dmg); [ -n "$f" ] && app+=("$(entry darwin-x64 "$f")")
f=$(one "Gerdr Setup "*.exe); [ -n "$f" ] && app+=("$(entry win32-x64 "$f")")
printf '{"version":"%s","shell":"%s","backend":{%s},"app":{%s}}\n' "$VERSION" "$SHELL_ID" "$(join ${backend[@]+"${backend[@]}"})" "$(join ${app[@]+"${app[@]}"})" > update.json
cat update.json
