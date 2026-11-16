# 配置

<sub>[English](config.md)</sub>

界面上的偏好（快捷键、主题、外部 agent）在「设置」里改，存在 `~/.gerdr/config.json`。服务本身用环境变量：

| 变量 | 默认 | |
|---|---|---|
| `GERDR_PORT` | `7870` | 监听端口（被占了会自动换一个） |
| `GERDR_HOST` | `127.0.0.1` | 监听地址 |
| `GERDR_DIR` | `~/.gerdr` | 数据目录（会话、布局、扩展） |
| `GERDR_LANG` | 系统语言 | `zh` / `en`：命令行和后端写进会话的文字用哪种语言（界面自己的语言在设置里选） |
| `GERDR_TOKEN` | 自动生成 | 登录令牌，存在 `~/.gerdr/token` |
| `GERDR_CLAUDE_IDLE_MINUTES` | `15` | Claude 会话闲多久就收掉它的进程（下次说话自动接回）；`0` = 不收 |
| `GERDR_UI` | `~/.gerdr/ui` | 界面源码目录（比如单独克隆的 gerdr-ui 仓库） |
| `GERDR_ESM_CDN` | `https://esm.talizen.com` | 界面第三方包的来源（任意 esm.sh 实例，比如 `https://esm.sh`；本机有缓存） |
| `GERDR_CLAUDE_BIN` / `GERDR_CODEX_BIN` | 自动找 | agent 可执行文件的位置 |

**用程序接**（手环、脚本）：看会话、读回复、发消息、替人点审批，都有 HTTP 接口，见 [docs/API.md](../API.md)。

**安全**：Gerdr 等于一个能在你机器上跑命令的入口，所以永远要令牌，哪怕只听 127.0.0.1。
别把它的端口转发到公网 —— 要从外面用，见[远程访问](remote.zh-CN.md)。
