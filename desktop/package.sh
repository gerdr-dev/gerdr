#!/bin/sh
# Builds the macOS App on Electron (docs/dev/WHY-ELECTRON-COMEBACK.md), one package per arch: Electron (desktop/main.js,
# preload.js), the core beside it (desktop/core → Resources/gerdr-shell) and the backend (Resources/gerdr).
#
#   desktop/package.sh                 # → desktop/dist/<arch>/Gerdr exp.app, desktop/dist/Gerdr-exp-<v>-mac-<arch>.dmg / .zip
#   desktop/package.sh install         # and put this Mac's arch in /Applications (an App there by that name: to the Trash)
#   NAME=Gerdr ID=io.github.zbysir.gerdr desktop/package.sh install   # as the Gerdr App itself
#   ARCH=arm64 desktop/package.sh      # just one
#   BACKEND_DIR=desktop/bin            # the release's backends (desktop/build.sh mac-backend), not built here
set -e
cd "$(dirname "$0")/.."
NAME=${NAME:-Gerdr exp}
ID=${ID:-io.github.zbysir.gerdr.exp}
SLUG=$(printf '%s' "$NAME" | tr ' ' '-')
ARCHS=${ARCH:-arm64 amd64}
OUT=desktop/dist
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-exp)}
VERSION=${VERSION#v}
SHELL_ID=$(tools/shell-id.sh)
HOST=$(uname -m | sed 's/x86_64/amd64/')

make ui >/dev/null
(cd desktop && [ -d node_modules/electron ] || npm ci --no-audit --no-fund)
SIGN_ID=$(./desktop/sign-id.sh 2>/dev/null || true)

ICNS=$(mktemp -d)
SET="$ICNS/icon.iconset"
mkdir -p "$SET"
for s in 16 32 128 256 512; do
  sips -z $s $s desktop/res/icon.png --out "$SET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s * 2)) $((s * 2)) desktop/res/icon.png --out "$SET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$SET" -o "$ICNS/icon.icns"

sign() { # $1 path, $2 identifier (optional)
  if [ -n "$SIGN_ID" ]; then
    codesign --force --sign "$SIGN_ID" ${2:+--identifier "$2"} "$1"
  else
    codesign --force --sign - ${2:+--identifier "$2"} "$1"
  fi
}

for arch in $ARCHS; do
  case $arch in
    arm64 | amd64) ;;
    *) echo "ARCH: arm64 and/or amd64" >&2; exit 1 ;;
  esac
  farch=$([ "$arch" = amd64 ] && echo x64 || echo arm64)
  RES=$(mktemp -d)
  # The backend, signed with the fixed identity and identifier so macOS privacy grants (Screen Recording /
  # Accessibility for the remote screen) survive rebuilds (desktop/sign-id.sh)
  if [ -n "${BACKEND_DIR:-}" ]; then
    cp "$BACKEND_DIR/gerdr-$farch" "$RES/gerdr"
  else
    GOOS=darwin GOARCH=$arch go build -ldflags "-s -w -X github.com/gerdr-dev/gerdr/internal/version.Version=$VERSION" -o "$RES/gerdr" ./cmd/gerdr
    sign "$RES/gerdr" io.github.zbysir.gerdr.backend
  fi
  CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -trimpath -ldflags '-s -w' -o "$RES/gerdr-shell" ./desktop/core
  sign "$RES/gerdr-shell" io.github.zbysir.gerdr.shell
  rm -rf "$OUT/$arch"
  node desktop/pack.js darwin "$farch" "$NAME" "$ID" "$VERSION" "$(git rev-parse --short HEAD)" "$SHELL_ID" "$ICNS/icon.icns" "$OUT/$arch" "$RES/gerdr" "$RES/gerdr-shell" >/dev/null
  mv "$OUT/$arch/$NAME-darwin-$farch/$NAME.app" "$OUT/$arch/$NAME.app"
  rm -rf "$OUT/$arch/$NAME-darwin-$farch" "$RES"
  APP="$OUT/$arch/$NAME.app"
  # Apple silicon won't run an unsigned bundle. Inside out: the helpers and the framework, then the App (the backend
  # and the core keep their own signatures and identifiers). With the local identity, file-access grants keyed by the
  # App's signature survive rebuilds.
  find "$APP/Contents/Frameworks" -maxdepth 1 \( -name '*.app' -o -name '*.framework' \) | while read -r f; do
    if [ -n "$SIGN_ID" ]; then codesign --force --deep --sign "$SIGN_ID" "$f"; else codesign --force --deep --sign - "$f"; fi
  done
  sign "$APP"

  DMG="$OUT/$SLUG-$VERSION-mac-$farch.dmg"
  rm -f "$OUT/$SLUG-$VERSION-mac-$farch.zip" "$DMG"
  ditto -c -k --keepParent "$APP" "$OUT/$SLUG-$VERSION-mac-$farch.zip"
  VOL=$(mktemp -d)
  ditto "$APP" "$VOL/$NAME.app"
  ln -s /Applications "$VOL/Applications"
  hdiutil create -quiet -volname "$NAME" -srcfolder "$VOL" -ov -format ULMO "$DMG"
  rm -rf "$VOL"
  echo "$arch: $(du -sh "$APP" | cut -f1) app, $(du -h "$DMG" | cut -f1) dmg"
done
rm -rf "$ICNS"

if [ "$1" = install ]; then
  APP="$OUT/$HOST/$NAME.app"
  [ -d "$APP" ] || { echo "install: no $HOST build (ARCH=$ARCHS)" >&2; exit 1; }
  # Copy next to the target first, then rename into place (a copy straight into /Applications can be stopped halfway
  # by App Management). What was there goes to the Trash, not away.
  DST="/Applications/$NAME.app"
  rm -rf "/Applications/.$NAME.app.new"
  ditto "$APP" "/Applications/.$NAME.app.new"
  if [ -e "$DST" ]; then
    osascript -e "tell application \"Finder\" to delete POSIX file \"$DST\"" >/dev/null
  fi
  mv "/Applications/.$NAME.app.new" "$DST"
  echo "installed: $DST"
fi
