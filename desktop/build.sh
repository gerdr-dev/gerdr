#!/usr/bin/env bash
# 打桌面 App（Electron 外壳 + 内嵌的 Go 后端）。
#   ./desktop/build.sh mac-dir <version>   本机架构的 Gerdr.app（不出安装包，make app / make install 用）
#   ./desktop/build.sh mac <version>       两个 dmg：Gerdr-<版本>-mac-arm64.dmg（Apple 芯片）、-mac-x64.dmg（Intel）
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
  # Apple 芯片、Intel 各出一个 dmg，不打通用包：通用包是两份 Electron + 两份后端摞在一起，体积翻倍，
  # 而每台机器只用得上其中一份。两次共用 bin/mac 这个位置放后端，所以一个架构打完再换下一个
  for arch in arm64 x64; do
    goarch=$arch && [ "$arch" = x64 ] && goarch=amd64
    GOOS=darwin GOARCH=$goarch go build -ldflags "$LDFLAGS" -o desktop/bin/mac/gerdr ./cmd/gerdr
    ( cd desktop && npx electron-builder --mac dmg --$arch --publish never -c.extraMetadata.version="$VERSION" \
      -c.dmg.artifactName='${productName}-${version}-mac-${arch}.${ext}' )
  done
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

# 没配签名证书时，mac 的 App 由 electron-builder 的 afterPack 钩子补 ad-hoc 签名（desktop/adhoc-sign.js）：
# 得在打 dmg 之前签，这里等它跑完再签的话装进 dmg 的那份是没签的
if [[ "$TARGET" == mac-dir ]]; then
  rm -rf build/Gerdr.app && mkdir -p build && cp -R "$APP" build/Gerdr.app
  echo "built build/Gerdr.app ($VERSION)"
else
  ls -1 desktop/dist/*.dmg desktop/dist/*.exe 2>/dev/null || true
fi
