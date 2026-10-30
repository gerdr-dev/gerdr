# 后台常驻（launchd）

设置 → 通用 →「后台常驻」打开后，gerdr 装成一个 LaunchAgent（`~/Library/LaunchAgents/io.github.zbysir.gerdr.plist`，
代码在 `internal/service`）：⌘Q 退出 App 会话照跑、手机远程照样连得上，登录就起、崩了被拉起来。只做了 macOS。

**On by default** for the App: at start (`--app`, before taking the lock) gerdr installs it unless the person turned it
off in Settings, which leaves `~/.gerdr/service-off` (`service.OptedOut`). So ⌘Q leaves running agents to finish.
Updates restart it only when idle (docs/dev/UPDATE.md).

## 和 App 的分工：开了就始终由后台跑，App 只是窗口

一个数据目录只能有一个 gerdr（`server.lock`，两份进程同时写会话日志会互相覆盖）。

- **打开开关**：装 plist、bootstrap，服务起来先等锁（`gerdr --service` 拿不到锁是**等**，不退 —— 退了 KeepAlive
  每 10 秒拉一次）。App 起的那个后端随即让位：`/api/service` 回完响应调 `OnServiceInstalled`，进程以退出码 75
  （`exitHandover`）退出，服务拿到锁接手；`desktop/main.js` 看到 75 重起一个后端。代价：后端重启一次，正在跑的 agent 被打断。
- **App 启动**（`--app`，`cmd/gerdr/attach.go` 的 `appAttach`）：装了服务就**不抢锁**，只连后台（打印它的 `GERDR_READY`）；
  装着但没加载就重新 bootstrap。没装、但被终端里的 gerdr 占着时，连过去并等着接班，那边退了自己接上、再打一次 `GERDR_READY`。
  `desktop/main.js` 按行读，地址变了才重新载入，没变就靠页面自己的 WebSocket 重连。
- **关掉开关**：后台服务删 plist、bootout 自己；App 的后端发现没装了，拿到锁接上。

为什么不在打开时「等 App 退出再交接」：那样开关打开后看起来什么都没变，要到 ⌘Q 才生效，还得解释现在是谁在跑。

## plist 里不放什么

- 不放 `GERDR_TOKEN` 和 DNS 凭据：`launchctl print` 谁都能看。令牌在 `~/.gerdr/token`，DNS 凭据在 `remote.json`，起来自己读。
- 不放 PATH：`sysenv.AdoptLoginPath` 开机问登录 shell（和从访达起的 App 同一个问题）。
  That read can fail (it timed out once on a busy machine right after a restart). It used to be a `sync.Once`, so
  one failure left every agent of that service without node / npx / go / ffmpeg until the next restart, and
  gerdr ran a stale `/usr/local/bin/codex` that macOS kills as malware. Now a failure still adds the fallback dirs,
  is logged ("login shell PATH not read"), and retries on the next agent spawn. Settings → Labs → Diagnosis
  (`internal/doctor`) compares the service's PATH with a fresh login shell's and can re-read it without a restart.
- `ProcessType=Interactive`：不写会被当后台任务限速（App Nap）。

## 坑

- 改了 plist 只 `kickstart` 不生效（launchd 不重读）：要 bootout + bootstrap，`service.Install` 就是这么做的。
- `launchctl bootout` returns in milliseconds while the old process is still exiting; a `bootstrap` in that window
  fails with `5: Input/output error` and nothing is loaded. This took the service down for 4.5 minutes when an
  update moved its binary (appAttach → Install, error ignored, and appAttach only re-loaded at start). `Install` now
  waits until `launchctl print` stops finding the label (exit 113) and retries; appAttach re-loads an installed but
  unloaded service every 15s.
- 调试实例（数据目录不是 `~/.gerdr`）和 `go run` 出来的临时二进制不让装：Label 只有一个，会把日常那份顶掉。
  调试实例看到日常那份 plist 也不算「装了」（`Installed` 先看 `Why`），不然它的 App 后端会一直等一个不会来的服务。
- 接口 `/api/service` 只在本机口：远程能关的话，手机上点一下就把自己连着的那个 gerdr 停了。
- 服务的日志在 `~/.gerdr/logs/gerdr.log`（launchd 起的进程没有终端）。
