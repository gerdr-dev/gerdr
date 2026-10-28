# 远程访问：云端中继 + 配对码 + passkey

照搬 herdr-web 的那一套（它的 `docs/dev/SECURITY.md` 和 `docs/dev/RELAY.md` 是完整的设计理由，动这块之前
先读那两份）。这里只写 gerdr 和它**不一样**的地方，以及搬过来时会静默出错的几条。

```
 手机浏览器 ──TLS──▶ herdr-relay（VPS）──按 SNI 原样转──▶ gerdr 的隧道（主动连出去的 WebSocket + yamux）
 https://gerdr.example.com        不解密                      TLS 在这里终止 → server.RemoteHandler → remoteGuard
```

代码：`internal/auth`（设备表 / 配对码 / passkey / 限速，照 herdr-web）、`internal/relay`（**只有主机那半边**，中继本身
在 herdr-web 仓库里）、`internal/acme`（DNS-01 签证书）、`internal/remote`（把上面拼起来）、`internal/server/remoteapi.go`
（远程的门 + `/api/remote/*`）、`internal/server/pairpage.html`（配对页）、`cmd/gerdr/remote.go`（`gerdr remote …`）、
界面 `ui/src/components/RemoteSettings.tsx` + `ui/src/lib/passkey.ts`。

## 中继能拿到什么

和 herdr-web 一样，**按中继那台 VPS 已经被人拿下来设计**：设备表、passkey 公钥、证书私钥、DNS token 都在本机；
中继只读 ClientHello 里的 SNI，后面的字节原样转。它能做的只剩断网、看元数据，以及**占着这个域名的 IP 自己去签一张证书
做中间人** —— 靠 DNS 上一条 CAA 堵：

```
CAA 0 issue "letsencrypt.org; accounturi=<这台机器的 ACME 账户>; validationmethods=dns-01"
```

gerdr 有**自己的** ACME 账户（`~/.gerdr/remote/acme/account.json`），和同一台机器上的 herdr-web 不是同一个：已经有一条
CAA 覆盖到这个名字（比如挂在上一级）时，要**再加一条** issue，不然 gerdr 签不下证书。`gerdr remote status` 打出 accounturi。
CAA 不配一切照常工作，只是中继多了一个后门，没有任何症状。

中继那边的 SNI 路由（Traefik 的 `HostSNI` 规则之类）也要放行 gerdr 的名字 —— 漏了的表现是「注册成功、隧道已连上、
手机打不开」，gerdr 这边一个字都不报。

## 两条路两套凭据（和 herdr-web 最大的不同）

herdr-web 只有一套设备凭据；gerdr 本来就有一把令牌（`~/.gerdr/token`），mac App、`gerdr session`、agent 自己都靠它，
而且它是**agent 读得到的明文**。所以：

| | 本机口（127.0.0.1:7870） | 远程口（中继隧道） |
|---|---|---|
| 认什么 | 令牌（cookie / Bearer） | **只认**设备 cookie（`gerdr_dev`，配对码或 passkey 换来的） |
| 令牌 | 认 | **不认**，`/auth?token=` 也 403 |
| 出配对码 | 能 | 403 |
| 加 passkey | 409（origin 不是 RPID） | 能（要 step-up） |
| 没认证时拿到什么 | 401 | 配对页；`/_ui/`、`/_esm/`、`/api/`、`/ws` 一律 401 |

判据是**请求落在哪个监听上**（`auth.MarkRemote`，只有隧道上那个 http.Server 盖章），不看源 IP、不看 Host：隧道里的
源地址是中继说的，Host 是客户端写的。

为什么远程不认令牌：令牌被 prompt injection 读走是日常风险；它要是在远程也好使，读走就等于整个互联网上的一个 shell。
远程那条路上的凭据只在设备的 HttpOnly cookie 里，磁盘上只有哈希。

为什么没认证只给一页自带的配对页、不给 React 界面：界面源码是 agent 能改的东西，`/_esm/` 是一个会往磁盘里缓存任意
npm 包的代理 —— 都不该对没配过对的人开放。

## 配对码只在本机出

和 herdr-web §11 同一个理由：配对码造的是一份**不随创造者一起被撤销**的凭据，远程能出码的话，手机被人拿去一次就能
留下一台你踢不掉的设备。所以 `/api/remote/pair-code` 只在本机口上，`gerdr remote pair` 也是调它。

`gerdr remote …` **不在** `sysenv.SafeGerdrCLI` 的自动放行里（那里只有 session / ui）：agent 跑一下 `gerdr remote pair`
就能给外面的人出一个码，每次都要过人的眼。agent 拿令牌直接 curl 那个口也一样要审批 —— 这一条挡不住
`bypassPermissions` 模式下的 agent，能挡住的只有「别让它悄悄地做」。

## 远程起不来不拦启动

herdr-web 配错了宁可拒绝启动（远程就是它的全部）。gerdr 的主界面在本机，mac App 起不来就什么都用不了，所以：证书签不下来、
没 join 过、中继连不上，都只记在 `remote.Status` 里（设置 →「远程访问」、`gerdr remote status`）。第一次签证书要等 DNS
传播（一两分钟），在后台等，不拖启动。

## 配置能写在文件里

服务配置别处一律只走环境变量，这块例外：`~/.gerdr/remote/remote.json`（0600，`gerdr remote join` 写）。mac App 从 Finder
起，拿不到 shell 里 export 的变量。环境变量照样能给、盖过文件。文件里的 DNS 凭据按 `GERDR_<名字>` 补进进程环境，
GERDR_* 本来就不往 agent 的环境里传（`sysenv.ChildEnv`）；acme 把脱了前缀的那份（lego 要读光秃秃的名字）在建完 provider
之后立刻撤掉，不然它会被之后起的 agent 继承。

## 照搬过来时要守住的几条（都在 herdr-web 踩过）

- **中继协议一个字节都不能改**：`relay.signed` 那串（`herdr-relay/1\n…`）、握手三句 JSON、子流头（两字节长度 + `{"src"}`）。
  `internal/relay/relay_test.go` 的假中继钉着格式；真的兼容性只有连上真中继才验得到。
- `relay.srcConn` 把 yamux 的超时错误换成 `os.ErrDeadlineExceeded`：不换的话 crypto/tls 把它当永久错误记住，**隧道上的
  WebSocket 连上就断**，普通请求一切正常。
- step-up 判 `PasskeyAt` 不判 `VerifiedAt`：配对会把 VerifiedAt 设成当下，按它判的话刚拿码进来的人天然新鲜，可以给自己留一把 passkey。
- 「全部踢掉」连 passkey 一起清（`auth.RevokeEverything`）：passkey 登录不要求认证，只清设备的话留着的 passkey 立刻换回一份新凭据。
- passkey 能不能用按**请求的 Host** 算（`passkeyOK`），本机口上那几个口 409，界面上不画按钮。
- `NotAllowedError` 不等于取消：700ms 内回来的照旧报出来（`lib/passkey.ts`、配对页里同一段）。
- 配对页进去用 `location.replace("/")`：SameSite=Strict 的 cookie 在「从别的 app 点链接进来」那一下不带，页面拿到的是配对页，
  whoami（同源 fetch）会带上 cookie 认出来，再发一次同源导航才进得去。
- 设备 cookie 一律 `Secure`：远程那条路一定是 https。

## 没做的

- 端到端（真 herdr-relay + 真证书 + 手机）只能部署之后验：假中继的测试管协议格式，不管 Traefik 路由、CAA、证书链。
- herdr-web 的命令行客户端那套（device flow 批准）、App 换路直连、CT 日志巡检、审计日志、令牌轮换。
