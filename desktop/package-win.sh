#!/bin/sh
# Builds the Windows App on Electron (docs/dev/WHY-ELECTRON-COMEBACK.md), on macOS or Linux (no Windows machine
# needed: @electron/packager edits the exe's resources in JS, no Wine), x64 only (Windows on Arm runs it emulated).
#
#   desktop/package-win.sh                          # → desktop/dist/win/Gerdr-exp-<version>-windows-x64-setup.exe
#   NAME=Gerdr VERSION=0.2.50 desktop/package-win.sh   # as the release does
#
# In it: Electron (desktop/main.js, preload.js), the core (desktop/core → resources\gerdr-shell.exe) and the backend
# (resources\gerdr.exe). The installer needs makensis (brew install makensis / apt-get install nsis).
set -e
cd "$(dirname "$0")/.."
NAME=${NAME:-Gerdr exp}
ID=${ID:-io.github.zbysir.gerdr$([ "$NAME" = Gerdr ] || echo .exp)}
SLUG=$(printf '%s' "$NAME" | tr ' ' '-')
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-exp)}
VERSION=${VERSION#v}
SHELL_ID=$(tools/shell-id.sh)
OUT=desktop/dist/win
rm -rf "$OUT"
mkdir -p "$OUT"

make ui >/dev/null
(cd desktop && [ -d node_modules/electron ] || npm ci --no-audit --no-fund)
RES=$(mktemp -d)
GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X github.com/gerdr-dev/gerdr/internal/version.Version=$VERSION" -o "$RES/gerdr.exe" ./cmd/gerdr
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags '-s -w -H windowsgui' -o "$RES/gerdr-shell.exe" ./desktop/core
# the exe's icon (packager) and the installer's: bitmap entries (makensis crashes on PNG ones)
go run ./desktop/mkico "$RES/icon.ico" desktop/res/icon.png 16 24 32 48 64 256
go run ./desktop/mkico -bmp "$RES/setup.ico" desktop/res/icon.png 16 32 48
node desktop/pack.js win32 x64 "$NAME" "$ID" "$VERSION" "$(git rev-parse --short HEAD)" "$SHELL_ID" "$RES/icon.ico" "$OUT" "$RES/gerdr.exe" "$RES/gerdr-shell.exe" >/dev/null
APPDIR="$OUT/$NAME-win32-x64"

ZIP="$OUT/$SLUG-$VERSION-windows-x64-portable.zip"
(cd "$APPDIR" && zip -q -9 -r "$(cd .. && pwd)/$(basename "$ZIP")" .)
if command -v makensis >/dev/null; then
  # <name>-<version>-windows-x64-setup.exe: the name desktop/feed.sh and the updater look for
  SETUP="$OUT/$SLUG-$VERSION-windows-x64-setup.exe"
  LOC=en_US.UTF-8
  [ "$(uname)" = Darwin ] || LOC=C.UTF-8
  LC_ALL=$LOC LANG=$LOC makensis -V2 -DSRC="$(pwd)/$APPDIR" -DOUT="$(pwd)/$SETUP" -DICON="$RES/setup.ico" \
    -DVERSION="$VERSION" "-DNAME=$NAME" "-DREGKEY=$SLUG" desktop/installer.nsi
fi
cp "$RES/gerdr.exe" "$OUT/gerdr.exe" # for the update-only backend (release.yml compresses it)
rm -rf "$RES"
ls -lh "$OUT"/*.zip "$OUT"/*.exe 2>/dev/null | awk '{print $5, $9}'
