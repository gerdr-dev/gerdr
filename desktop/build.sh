#!/usr/bin/env bash
# 打桌面 App（Electron 外壳 + 内嵌的 Go 后端）。
#   ./desktop/build.sh mac-dir <version>   本机架构的 Gerdr.app（不出安装包，make app / make install 用）
#   ./desktop/build.sh mac <version>       通用（arm64 + x64）dmg
#   ./desktop/build.sh win <version>       Windows x64 安装包（nsis，在 mac 上就能交叉打）
set -euo pipefail
cd "$(dirname "$0")/.."
TARGET="${1:-mac-dir}"
VERSION="${2:-0.1.0}"
VERSION="${VERSION#v}"
# electron-builder 要求版本号是 semver；git describe 出来的 a5d916f-dirty 这种不是
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] || VERSION="0.1.0-dev"
LDFLAGS="-s -w -X github.com/gerdr-dev/gerdr/internal/version.Version=$VERSION"

make -s ui
( cd desktop && [ -d node_modules ] || npm install --no-audit --no-fund )
rm -rf desktop/bin && mkdir -p desktop/bin/mac desktop/bin/win

case "$TARGET" in
mac-dir)
  GOOS=darwin go build -ldflags "$LDFLAGS" -o desktop/bin/mac/gerdr ./cmd/gerdr
  ( cd desktop && npx electron-builder --mac dir --publish never -c.extraMetadata.version="$VERSION" )
  APP=$(ls -d desktop/dist/mac*/Gerdr.app | head -1)
  ;;
mac)
  # 后端合成一个通用二进制；Electron 那边 --universal 会把 arm64 / x64 两份 App 合起来，
  # 两边一模一样的 Mach-O 文件要在 x64ArchFiles 里点名（package.json），不然合并时报错
  GOOS=darwin GOARCH=arm64 go build -ldflags "$LDFLAGS" -o build/gerdr-arm64 ./cmd/gerdr
  GOOS=darwin GOARCH=amd64 go build -ldflags "$LDFLAGS" -o build/gerdr-amd64 ./cmd/gerdr
  lipo -create -output desktop/bin/mac/gerdr build/gerdr-arm64 build/gerdr-amd64
  ( cd desktop && npx electron-builder --mac dmg --universal --publish never -c.extraMetadata.version="$VERSION" )
  ;;
win)
  GOOS=windows GOARCH=amd64 go build -ldflags "$LDFLAGS" -o desktop/bin/win/gerdr.exe ./cmd/gerdr
  ( cd desktop && npx electron-builder --win nsis --x64 --publish never -c.extraMetadata.version="$VERSION" )
  ;;
*)
  echo "用法：$0 mac-dir|mac|win <version>" >&2
  exit 1
  ;;
esac

if [[ "$TARGET" == mac* ]]; then
  # 没配签名证书时 electron-builder 不签（identity: null），但 Apple 芯片上不签名的程序起不来：补一个 ad-hoc 签名。
  # 这样别人第一次打开要右键「打开」；正式发布配 CSC_LINK / CSC_KEY_PASSWORD 让 electron-builder 正经签名 + 公证。
  for a in desktop/dist/mac*/Gerdr.app; do
    [ -d "$a" ] && [ -z "${CSC_LINK:-}" ] && codesign --force --deep --sign - "$a"
  done
fi
if [[ "$TARGET" == mac-dir ]]; then
  rm -rf build/Gerdr.app && mkdir -p build && cp -R "$APP" build/Gerdr.app
  echo "built build/Gerdr.app ($VERSION)"
else
  ls -1 desktop/dist/*.dmg desktop/dist/*.exe 2>/dev/null || true
fi
