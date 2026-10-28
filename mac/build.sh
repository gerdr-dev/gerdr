#!/usr/bin/env bash
# 打 gerdr.app：Swift 外壳 + 内嵌的 Go 后端（bin/gerdr，先 make build）。
# 只需要 Xcode Command Line Tools（swiftc / iconutil / codesign），不需要 Xcode。
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${1:-0.1.0}"
VERSION="${VERSION#v}"
OUT=build/gerdr.app
rm -rf "$OUT" build/icon.iconset
mkdir -p "$OUT/Contents/MacOS" "$OUT/Contents/Resources" build/icon.iconset

# 1. 外壳（arm64 + x86_64 通用）
for arch in arm64 x86_64; do
  swiftc -O -target "$arch-apple-macos13.0" -o "build/gerdr-app-$arch" mac/Sources/main.swift \
    -framework AppKit -framework WebKit -framework UserNotifications
done
lipo -create -output "$OUT/Contents/MacOS/gerdr-app" build/gerdr-app-arm64 build/gerdr-app-x86_64

# 2. 后端（两个架构合成一个通用二进制）
LDFLAGS="-s -w -X github.com/zbysir/gerdr/internal/version.Version=$VERSION"
GOOS=darwin GOARCH=arm64 go build -ldflags "$LDFLAGS" -o build/gerdr-arm64 ./cmd/gerdr
GOOS=darwin GOARCH=amd64 go build -ldflags "$LDFLAGS" -o build/gerdr-amd64 ./cmd/gerdr
lipo -create -output "$OUT/Contents/Resources/gerdr" build/gerdr-arm64 build/gerdr-amd64

# 3. 图标
swift mac/icon.swift build/icon-1024.png
for s in 16 32 128 256 512; do
  sips -z $s $s build/icon-1024.png --out "build/icon.iconset/icon_${s}x${s}.png" >/dev/null
  sips -z $((s*2)) $((s*2)) build/icon-1024.png --out "build/icon.iconset/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns build/icon.iconset -o "$OUT/Contents/Resources/AppIcon.icns"

# 4. Info.plist + 签名（ad-hoc；没有 Apple 开发者证书时别人第一次打开要右键「打开」）
sed "s/__VERSION__/$VERSION/g" mac/Info.plist > "$OUT/Contents/Info.plist"
if [[ -n "${CODESIGN_IDENTITY:-}" ]]; then
  codesign --force --options runtime --timestamp --sign "$CODESIGN_IDENTITY" "$OUT/Contents/Resources/gerdr"
  codesign --force --options runtime --timestamp --sign "$CODESIGN_IDENTITY" "$OUT"
else
  codesign --force --sign - "$OUT/Contents/Resources/gerdr"
  codesign --force --sign - "$OUT"
fi
echo "built $OUT ($VERSION)"
