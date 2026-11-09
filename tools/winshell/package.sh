#!/bin/sh
# Builds the Windows shell's package, on macOS or Linux (no Windows machine needed; the release builds it on Linux).
#
#   tools/winshell/package.sh                       # → dist/Gerdr-exp-<version>-windows-x64-setup.exe and -portable.zip
#   NAME=Gerdr VERSION=0.2.21 tools/winshell/package.sh   # as the release does
#
# In it: GerdrExp.exe (this shell, WebView2) + gerdr.exe (the backend, UI snapshot inside, as desktop/build.sh builds
# it) + icon.ico. x64 only (Windows on Arm runs it emulated). The installer needs makensis (brew install makensis /
# apt-get install nsis); the zip is made without it.
set -e
cd "$(dirname "$0")/../.."
NAME=${NAME:-Gerdr exp}
SLUG=$(printf '%s' "$NAME" | tr ' ' '-')
# The release passes its version; else git describe. Without the v (the updater compares x.y.z[-N-gabc]).
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-exp)}
VERSION=${VERSION#v}
# Which shell this is (tools/shell-id.sh): an update from a release with the same id only swaps the backend
SHELL_ID=$(tools/shell-id.sh)
OUT=tools/winshell/dist
STAGE="$OUT/stage"
rm -rf "$STAGE"
mkdir -p "$STAGE"

make ui >/dev/null
GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X github.com/gerdr-dev/gerdr/internal/version.Version=$VERSION" -o "$STAGE/gerdr.exe" ./cmd/gerdr
GOOS=windows GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -H windowsgui -X main.version=$VERSION -X 'main.appName=$NAME' -X main.shellID=$SHELL_ID" \
  -o "$STAGE/GerdrExp.exe" ./tools/winshell

# icon.ico for the shortcuts (PNG entries; the window's icon is in the exe, main.go), and the installer's own icon:
# bitmap entries (makensis crashes on PNG ones), not shipped
go run ./tools/winshell/mkico "$STAGE/icon.ico" desktop/res/icon.png 16 24 32 48 64 256
go run ./tools/winshell/mkico -bmp "$OUT/setup.ico" desktop/res/icon.png 16 32 48

ZIP="$OUT/$SLUG-$VERSION-windows-x64-portable.zip"
rm -f "$ZIP"
(cd "$STAGE" && zip -q -9 "../$(basename "$ZIP")" GerdrExp.exe gerdr.exe icon.ico)

if command -v makensis >/dev/null; then
  # <name>-<version>-windows-x64-setup.exe: the name desktop/feed.sh and the updater look for
  SETUP="$OUT/$SLUG-$VERSION-windows-x64-setup.exe"
  # With no locale set, Homebrew's makensis 3.12 aborts on any script (std::bad_alloc). Linux has C.UTF-8.
  LOC=en_US.UTF-8
  [ "$(uname)" = Darwin ] || LOC=C.UTF-8
  LC_ALL=$LOC LANG=$LOC makensis -V2 -DSRC="$(pwd)/$STAGE" -DOUT="$(pwd)/$SETUP" -DICON="$(pwd)/$OUT/setup.ico" \
    -DVERSION="$VERSION" "-DNAME=$NAME" "-DREGKEY=$SLUG" tools/winshell/installer.nsi
fi
rm -f "$OUT/setup.ico"
ls -lh "$OUT"/*.zip "$OUT"/*.exe 2>/dev/null | awk '{print $5, $9}'
