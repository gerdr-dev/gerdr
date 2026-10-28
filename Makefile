# gerdr：一个 Go 二进制 + 一个桌面 App 外壳（Electron，desktop/）。界面是 ui/ 下的源码，没有打包环节（internal/uisite）：
# 编二进制时把 ui/ 原样拷进去内嵌，第一次启动装到 ~/.gerdr/ui，之后谁改那份源码界面就跟着变。
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X github.com/zbysir/gerdr/internal/version.Version=$(VERSION)
UISNAP := internal/webui/ui

.PHONY: all ui sync-ui build test typecheck test-e2e app dmg win icon install clean dev

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

build: ui
	go build -ldflags '$(LDFLAGS)' -o bin/gerdr ./cmd/gerdr

test:
	go vet ./...
	go test ./...

# 界面的类型检查（要先在 ui/ 里装一次开发依赖：cd ui && pnpm install）。运行界面不需要它。
typecheck:
	cd ui && npx tsc --noEmit

# 对着真的 claude / codex 跑（花钱、要登录）
test-e2e:
	GERDR_E2E=codex go test ./internal/agenthost/ -run E2E -v -count=1
	GERDR_E2E=claude go test ./internal/agenthost/ -run E2E -v -count=1

# 桌面 App（desktop/build.sh）：app = 本机架构的 build/gerdr.app；dmg = mac 通用安装包；win = Windows 安装包。
# 外壳为什么是 Electron：docs/dev/WHY-ELECTRON.md
app:
	./desktop/build.sh mac-dir $(VERSION)

dmg:
	./desktop/build.sh mac $(VERSION)

win:
	./desktop/build.sh win $(VERSION)

# 重画图标（desktop/res/icon.png 入库，打包不需要 swift —— Windows 包在任何机器上都能打）
icon:
	swift desktop/icon.swift desktop/res/icon.png

# 开发：界面直接用仓库里的 ui/（改了立刻热更新），数据放在单独的目录里，别和自己日常用的那份混。
dev:
	GERDR_UI=$(CURDIR)/ui GERDR_DIR=$(HOME)/.gerdr-dev GERDR_PORT=7871 go run ./cmd/gerdr --open

clean:
	rm -rf bin build $(UISNAP)
	mkdir -p $(UISNAP) && touch $(UISNAP)/.gitkeep

install: app
	rm -rf /Applications/gerdr.app
	cp -R build/gerdr.app /Applications/
	mkdir -p $(HOME)/.local/bin
	ln -sf /Applications/gerdr.app/Contents/Resources/gerdr $(HOME)/.local/bin/gerdr
	@echo "装好了：/Applications/gerdr.app，命令行 ~/.local/bin/gerdr"
