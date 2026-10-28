#!/usr/bin/env bash
# 打 dmg：build/gerdr-<version>.dmg（拖进 Applications 那种）。
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-0.1.0}"
VERSION="${VERSION#v}"
STAGE=build/dmg
rm -rf "$STAGE" && mkdir -p "$STAGE"
cp -R build/gerdr.app "$STAGE/"
ln -s /Applications "$STAGE/Applications"
rm -f "build/gerdr-$VERSION.dmg"
hdiutil create -volname "gerdr" -srcfolder "$STAGE" -ov -format UDZO "build/gerdr-$VERSION.dmg" >/dev/null
echo "built build/gerdr-$VERSION.dmg"
