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

**配对码进来的设备只是临时的**（`auth.Device.Temp`）：从配对那一刻起 30 分钟到期（`GERDR_PAIR_TTL_MINUTES` /
remote.json 的 `pairTTLMinutes`，0 = 和别的设备一样），用了也不续期；在那台设备上过一次 passkey（登录或注册一把，
`MarkVerified`）才转正、按设备 TTL 滑动。配对码只负责「让新设备进门」，长期凭据要绑在 passkey 上 —— 码被拍走、
临时借来的设备忘了退，口子最多半小时。passkey 登录和「批准 App 登录」签出来的设备不是临时的（那两条当场过了 passkey 或本机批准）。
改之前配的设备不受影响。

## 远程起不来不拦启动

herdr-web 配错了宁可拒绝启动（远程就是它的全部）。gerdr 的主界面在本机，mac App 起不来就什么都用不了，所以：证书签不下来、
没 join 过、中继连不上，都只记在 `remote.Status` 里（设置 →「远程访问」、`gerdr remote status`）。第一次签证书要等 DNS
传播（一两分钟），在后台等，不拖启动。

## 配置能写在文件里

服务配置别处一律只走环境变量，这块例外：`~/.gerdr/remote/remote.json`（0600，`gerdr remote join` 写）。mac App 从 Finder
起，拿不到 shell 里 export 的变量。环境变量照样能给、盖过文件。文件里的 DNS 凭据按 `GERDR_<名字>` 补进进程环境，
GERDR_* 本来就不往 agent 的环境里传（`sysenv.ChildEnv`）；acme 把脱了前缀的那份（lego 要读光秃秃的名字）在建完 provider
之后立刻撤掉，不然它会被之后起的 agent 继承。

证书来源（DNS 服务商 + 凭据，或两个证书文件）在设置 →「远程访问」→「证书」里填（`/api/remote/cert`，**只在本机口**：
里面有 DNS 密钥），存进 remote.json，`remote.SetCert` 把远程那条路当场重来，不用重启。读回去只给凭据的名字不给值，
表单留空 = 沿用；换服务商时别家的凭据从文件里删掉。所以 `join` 不再强制要 `--dns` / `--cert`。
服务商的表单字段和「凭据去哪儿拿」在 `acme.providers` 一张表里（`acme_test.go` 钉着它和 `dnsNamespaces` 对得上）。

**坑：写在 `~/.zshenv` / `.zshrc` 里的 `GERDR_*` App 看不到**（从访达起不读 shell 配置；`sysenv.AdoptLoginPath`
只捞 PATH）。终端里起的 gerdr 看得到、App 里看不到，表现是「明明配了，App 里还说缺证书」—— 用设置页那一处。

## 照搬过来时要守住的几条（都在 herdr-web 踩过）

- **中继协议一个字节都不能改**：`relay.signed` 那串（`herdr-relay/1\n…`）、握手三句 JSON、子流头（两字节长度 + `{"src"}`）。
  `internal/relay/relay_test.go` 的假中继钉着格式；真的兼容性只有连上真中继才验得到。
- `relay.srcConn` 把 yamux 的超时错误换成 `os.ErrDeadlineExceeded`：不换的话 crypto/tls 把它当永久错误记住，**隧道上的
  WebSocket 连上就断**，普通请求一切正常。
- step-up 判 `PasskeyAt` 不判 `VerifiedAt`：配对会把 VerifiedAt 设成当下，按它判的话刚拿码进来的人天然新鲜，可以给自己留一把 passkey。
- step-up 只能用已有 passkey 做的话，**新手机永远加不上 passkey**：它上面一把都没有，系统只弹「未发现任何通行密钥」
  （实测 Android Edge）。所以还有一条「到电脑上批准」（`auth/approve.go`）：远程设备发起请求，本机设置页（和一条通知）
  显示请求 + 四位核对码，点批准 → `Store.GrantStepUp` 给那台设备写 PasskeyAt。批准和待批列表只在本机口 —— 远程能批的话，
  偷来的 cookie 自己批自己。批准之后手机上还要人再点一下才注册：WebAuthn 的 create 要用户手势，轮询到了直接调会被拒。
- 「全部踢掉」连 passkey 一起清（`auth.RevokeEverything`）：passkey 登录不要求认证，只清设备的话留着的 passkey 立刻换回一份新凭据。
- passkey 能不能用按**请求的 Host** 算（`passkeyOK`），本机口上那几个口 409，界面上不画按钮。
- `NotAllowedError` 不等于取消：700ms 内回来的照旧报出来（`lib/passkey.ts`、配对页里同一段）。
- 配对页进去用 `location.replace("/")`：SameSite=Strict 的 cookie 在「从别的 app 点链接进来」那一下不带，页面拿到的是配对页，
  whoami（同源 fetch）会带上 cookie 认出来，再发一次同源导航才进得去。
- 设备 cookie 一律 `Secure`：远程那条路一定是 https。
- 限速要管住**并发**：`Gate.Check` 只在请求进来时看一眼、失败是验完才记，同一时刻打进来的一批猜测会全部通过、各猜一次
  （herdr-web 那份照搬过来就有这个洞）。所以 `gateCheck` 先 `Enter`（同一个 IP 同时只一次在验，拿不到直接 429，不排队），
  验的那几毫秒再占一个全局名额（`MaxInflight`，换着 IP 并发也有上限；退避的睡眠不占，不然被罚的 IP 睡着就把名额占满了）。
  熔断前最多大约 `GlobalTrip + MaxInflight` 次猜测，对 31⁸ 的码空间可以忽略。`TestRemotePairConcurrentGuesses` 钉着。

## 没做的

- 端到端（真 herdr-relay + 真证书 + 手机）只能部署之后验：假中继的测试管协议格式，不管 Traefik 路由、CAA、证书链。
- CT 日志巡检、审计日志、令牌轮换。
- 安卓 App（局域网直连、App 里刷 passkey、device flow 退路、后台通知）见 [ANDROID.md](ANDROID.md)。
