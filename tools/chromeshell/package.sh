#!/bin/sh
# Builds the macOS App (default "Gerdr exp", to try next to the real Gerdr; the release passes NAME=Gerdr), one
# package per arch.
#
#   tools/chromeshell/package.sh            # → dist/<arch>/Gerdr exp.app, dist/Gerdr-exp-mac-<arch>.dmg / .zip,
#                                           #   for arm64 and amd64
#   tools/chromeshell/package.sh install    # and put this Mac's arch in /Applications (an App already there by that
#                                           # name goes to the Trash first, so it can be put back)
#   NAME=Gerdr ID=io.github.zbysir.gerdr tools/chromeshell/package.sh install   # as the Gerdr App itself
#   ARCH=arm64 tools/chromeshell/package.sh # just one
#
# Per arch, not universal: the backend is most of the package (~42MB of ~47MB per arch), a universal one carries two.
# Chromium isn't in it (the user's own browser, of the right arch already).
#
# The bundle holds the shell and the backend (used only when no Gerdr is running: otherwise the shell is a window on
# that one, main.go). LSUIElement keeps the shell itself out of the Dock: the Dock icon is the engine's, the browser
# copy that carries this icon and name (Lightron's macengine).
set -e
cd "$(dirname "$0")/../.."
NAME=${NAME:-Gerdr exp}
ID=${ID:-io.github.zbysir.gerdr.exp}
SLUG=$(printf '%s' "$NAME" | tr ' ' '-')
ARCHS=${ARCH:-arm64 amd64}
OUT=tools/chromeshell/dist
# The release passes its version (VERSION=0.2.21); else git describe. Without the v, as desktop/build.sh: the updater
# compares x.y.z[-N-gabc].
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0-exp)}
VERSION=${VERSION#v}
# Which shell this is (tools/shell-id.sh): an update from a release with the same id only swaps the backend
SHELL_ID=$(tools/shell-id.sh)
HOST=$(uname -m | sed 's/x86_64/amd64/')

make ui >/dev/null
SIGN_ID=$(./desktop/sign-id.sh 2>/dev/null || true)

# Icon: the same artwork as Gerdr.app (desktop/res/icon.png, made from ui/public/icon.svg by `make icon`).
ICNS=$(mktemp -d)
SET="$ICNS/icon.iconset"
mkdir -p "$SET"
for s in 16 32 128 256 512; do
  sips -z $s $s desktop/res/icon.png --out "$SET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s * 2)) $((s * 2)) desktop/res/icon.png --out "$SET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$SET" -o "$ICNS/icon.icns"

for arch in $ARCHS; do
  case $arch in
    arm64 | amd64) ;;
    *) echo "ARCH: arm64 and/or amd64" >&2; exit 1 ;;
  esac
  APP="$OUT/$arch/$NAME.app"
  rm -rf "$APP"
  mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

  # The backend, as desktop/build.sh builds it (UI snapshot inside). BACKEND_DIR: the ones `desktop/build.sh
  # mac-backend` already built and signed (gerdr-arm64 / gerdr-x64), as the release does: the dmg then carries the
  # very binary of the update-only .br, and the job doesn't compile the backend twice (~1 min per arch).
  farch=$([ "$arch" = amd64 ] && echo x64 || echo arm64)
  if [ -n "${BACKEND_DIR:-}" ]; then
    cp "$BACKEND_DIR/gerdr-$farch" "$APP/Contents/Resources/gerdr" # signed already, the way below
  else
    GOOS=darwin GOARCH=$arch go build -ldflags "-s -w -X github.com/gerdr-dev/gerdr/internal/version.Version=$VERSION" -o "$APP/Contents/Resources/gerdr" ./cmd/gerdr
    # Signed like desktop/build.sh signs it: the fixed "Gerdr Local Signing" identity (desktop/sign-id.sh) and the
    # same identifier, so macOS privacy grants (Screen Recording / Accessibility, the remote screen) given to the
    # backend survive rebuilds and updates. Ad hoc changes with every build and voids them. No identity here: ad hoc.
    if [ -n "$SIGN_ID" ]; then
      codesign --force --sign "$SIGN_ID" --identifier io.github.zbysir.gerdr.backend "$APP/Contents/Resources/gerdr"
    else
      codesign --force --sign - "$APP/Contents/Resources/gerdr"
    fi
  fi

  # The shell.
  CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -trimpath -ldflags '-s -w' -o "$APP/Contents/MacOS/gerdr-exp" ./tools/chromeshell

  # The menu library loaded into the engine comes with Lightron (embedded in the shell, universal).

  # icon.icns: the bundle's own (Finder); icon.png: what the shell gives Lightron for the engine (Dock, ⌘Tab).
  cp "$ICNS/icon.icns" "$APP/Contents/Resources/icon.icns"
  cp desktop/res/icon.png "$APP/Contents/Resources/icon.png"
  cat >"$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key><string>$ID</string>
  <key>CFBundleName</key><string>$NAME</string>
  <key>CFBundleDisplayName</key><string>$NAME</string>
  <key>CFBundleExecutable</key><string>gerdr-exp</string>
  <key>CFBundleIconFile</key><string>icon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleVersion</key><string>$(git rev-parse --short HEAD)</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>LSUIElement</key><true/>
  <key>GerdrShell</key><string>$SHELL_ID</string>
</dict>
</plist>
EOF

  # Apple silicon won't run an unsigned bundle; ad hoc is enough on the machine that built it.
  codesign --force --sign - "$APP"

  # <name>-<version>-mac-arm64 / -mac-x64: the names desktop/feed.sh and the updaters look for. In the dmg: the App
  # at its root (the updaters copy the first .app there) and a shortcut to /Applications to drag it onto.
  DMG="$OUT/$SLUG-$VERSION-mac-$farch.dmg"
  rm -f "$OUT/$SLUG-$VERSION-mac-$farch.zip" "$DMG"
  ditto -c -k --keepParent "$APP" "$OUT/$SLUG-$VERSION-mac-$farch.zip"
  VOL=$(mktemp -d)
  ditto "$APP" "$VOL/$NAME.app"
  ln -s /Applications "$VOL/Applications"
  hdiutil create -quiet -volname "$NAME" -srcfolder "$VOL" -ov -format ULFO "$DMG"
  rm -rf "$VOL"
  echo "$arch: $(du -sh "$APP" | cut -f1) app, $(du -h "$DMG" | cut -f1) dmg"
done
rm -rf "$ICNS"

if [ "$1" = install ]; then
  APP="$OUT/$HOST/$NAME.app"
  [ -d "$APP" ] || { echo "install: no $HOST build (ARCH=$ARCHS)" >&2; exit 1; }
  # Copy next to the target first, then rename into place: a copy straight into /Applications can be stopped halfway
  # by App Management (Makefile install). What was there goes to the Trash, not away.
  DST="/Applications/$NAME.app"
  rm -rf "/Applications/.$NAME.app.new"
  ditto "$APP" "/Applications/.$NAME.app.new"
  if [ -e "$DST" ]; then
    osascript -e "tell application \"Finder\" to delete POSIX file \"$DST\"" >/dev/null
  fi
  mv "/Applications/.$NAME.app.new" "$DST"
  echo "installed: $DST"
fi
