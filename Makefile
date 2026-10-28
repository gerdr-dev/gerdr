# gerdr：一个 Go 二进制 + 一个桌面 App 外壳（Electron，desktop/）。界面是 ui/ 下的源码，没有打包环节（internal/uisite）：
# 编二进制时把 ui/ 原样拷进去内嵌，第一次启动装到 ~/.gerdr/ui，之后谁改那份源码界面就跟着变。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X github.com/zbysir/gerdr/internal/version.Version=$(VERSION)
UISNAP := internal/webui/ui

.PHONY: all ui rescue sync-ui build test typecheck fmt test-e2e app dmg win icon install clean dev

all: build

# 内嵌的那份界面：只拷源码和配置，不要 node_modules（类型检查用的）。
ui:
	rm -rf $(UISNAP) && mkdir -p $(UISNAP)
	rsync -a --exclude node_modules --exclude '.git' --exclude '*.tsbuildinfo' --exclude '.gerdr-ui.json' --exclude .DS_Store ui/ $(UISNAP)/
	touch $(UISNAP)/.gitkeep

# 把仓库的 ui/ 同步到正在用的界面目录（默认 ~/.gerdr/ui）：开着的 gerdr 窗口直接热更新，不用重新打包、重启。
# 清单一起更新，下次升级不会把它当成被人改过；那边被人改过就拒绝覆盖（要覆盖：go run ./cmd/gerdr ui sync ui --force）。
sync-ui:
	go run ./cmd/gerdr ui sync ui

# 急救面板（internal/server/rescue/src，React）打成 app.js 入库：改了那边的源码跑一下（要 ui/node_modules）。
rescue:
	go generate ./internal/server

build: ui
	go build -ldflags '$(LDFLAGS)' -o bin/gerdr ./cmd/gerdr

test:
	go vet ./...
	go test ./...

# 界面的类型检查（要先在 ui/ 里装一次开发依赖：cd ui && pnpm install）。运行界面不需要它。
typecheck:
	cd ui && npx tsc --noEmit
	ui/node_modules/.bin/tsc -p internal/server/rescue/tsconfig.json
	cd ui && node_modules/.bin/prettier --check 'src/**/*.{ts,tsx}' --log-level warn || { echo '格式不对：跑 make fmt'; exit 1; }

# 界面全量 prettier（用 ui/node_modules 里钉死版本的那个，在 ui/ 里跑才读得到 .prettierrc.json / .prettierignore）
fmt:
	cd ui && node_modules/.bin/prettier --write 'src/**/*.{ts,tsx}' --log-level warn

# 对着真的 claude / codex 跑（花钱、要登录）
test-e2e:
	GERDR_E2E=codex go test ./internal/agenthost/ -run E2E -v -count=1
	GERDR_E2E=claude go test ./internal/agenthost/ -run E2E -v -count=1

# 桌面 App（desktop/build.sh）：app = 本机架构的 build/Gerdr.app；dmg = mac 通用安装包；win = Windows 安装包。
# 外壳为什么是 Electron：docs/dev/WHY-ELECTRON.md
app:
	./desktop/build.sh mac-dir $(VERSION)

dmg:
	./desktop/build.sh mac $(VERSION)

win:
	./desktop/build.sh win $(VERSION)

# 安卓 App（android/，设计见 docs/dev/ANDROID.md）。要 Android SDK（sdk.dir 写在 android/local.properties）和 JDK 17–21
# （AGP 不认更新的 JDK）。apk = 正式包，用 ~/.android-release/gerdr.jks 签（它的指纹写死在 config.OfficialAndroidCert：
# 换钥匙 App 里就刷不了 passkey）；apk-debug = 调试包（debug 钥匙签，和正式包不能互相覆盖安装）。
ANDROID_JAVA ?= /opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home
# APK_VERSION 只认 主.次.修（versionCode 按它算、只能往上走，系统拿它判「是不是升级」）；VERSION 是 git describe，不能直接用
APK_VERSION ?= 0.1.0
apk:
	cd android && JAVA_HOME=$(ANDROID_JAVA) ./gradlew assembleRelease -PappVersion=$(APK_VERSION)
	@ls -l android/app/build/outputs/apk/release/app-release.apk

apk-debug:
	cd android && JAVA_HOME=$(ANDROID_JAVA) ./gradlew assembleDebug
	@ls -l android/app/build/outputs/apk/debug/app-debug.apk

# 从 ui/public/icon.svg 重新生成 App 图标（desktop/res/icon.png 入库，打包时不需要 rsvg-convert —— Windows 包在任何机器上都能打）。
# 照 macOS 图标的网格：1024 的画布里图形占中间 824，四周各留 100，不然在 Dock 里比别的图标大一圈。要 brew install librsvg。
icon:
	rsvg-convert -w 824 -h 824 --page-width 1024 --page-height 1024 --left 100 --top 100 ui/public/icon.svg -o desktop/res/icon.png

# 开发：界面直接用仓库里的 ui/（改了立刻热更新），数据放在单独的目录里，别和自己日常用的那份混。
dev:
	GERDR_UI=$(CURDIR)/ui GERDR_DIR=$(HOME)/.gerdr-dev GERDR_PORT=7871 go run ./cmd/gerdr --open

clean:
	rm -rf bin build $(UISNAP)
	mkdir -p $(UISNAP) && touch $(UISNAP)/.gitkeep

# 先在同一块盘上复制好、再整个 mv 进去：直接 cp -R 进 /Applications，macOS 的「App 管理」保护可能拦到一半
# （大量 Operation not permitted），留下一个缺 Info.plist、打不开的半截 App（踩过）；mv 只是改名，不改包里的东西。
install: app
	rm -rf build/stage && mkdir -p build/stage && cp -R build/Gerdr.app build/stage/
	rm -rf /Applications/Gerdr.app
	mv build/stage/Gerdr.app /Applications/Gerdr.app
	mkdir -p $(HOME)/.local/bin
	ln -sf /Applications/Gerdr.app/Contents/Resources/gerdr $(HOME)/.local/bin/gerdr
	@echo "装好了：/Applications/Gerdr.app，命令行 ~/.local/bin/gerdr"
