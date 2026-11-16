# 远程访问（手机 / 别的电脑）

<sub>[English](remote.md)</sub>

和 [herdr-web](https://github.com/zbysir/herdr-web) 同一套：经云端中继（[dialhome](https://github.com/zbysir/dialhome)）对外（也可以不走中继，见下面「直接访问」），TLS 在这台机器上终止，
中继只转密文。新设备用**配对码**进来（只有坐在机器前的人能出），之后加一把 **passkey**，换新设备就不用再回机器前。
本机的令牌在远程**不好使**。设计和理由见 [docs/dev/REMOTE.md](../dev/REMOTE.md)。

一次性的准备：

```bash
# 在 dialhome 的网页后台（https://relay.example.com/）里给这台机器出邀请码，或者在 VPS 上：
dialhome invite gerdr.example.com

# 这台机器上：注册，顺带配好证书（DNS-01 自己签，这里以 Cloudflare 为例）
gerdr remote join https://relay.example.com gerdr.example.com <邀请码> \
  --dns cloudflare --env CLOUDFLARE_DNS_API_TOKEN=…
```

它会打出要加的 DNS 记录（A 指到中继、一条 CAA），配置写进 `~/.gerdr/remote/remote.json`，重启 Gerdr 生效（mac App 也认）。

然后：

- **配对**：设置 →「远程访问」→ 出一个配对码（或 `gerdr remote pair`），手机扫码就进来了。码 5 分钟、用一次就废。
- **passkey**：在手机上打开的 Gerdr 里，设置 →「远程访问」→ 加一把 passkey。配过对的设备 24 小时没验证要再刷一次。
- **踢设备**：设置里点「踢掉」，或 `gerdr remote devices` / `gerdr remote revoke <id|all>`（all 连 passkey 一起清）。
  轻一点的：`gerdr remote reverify <id>` 不踢掉设备，只让它下次访问时重新验证。
- `gerdr remote status` 看中继连没连上、证书什么时候到期。

也能全用环境变量配（盖过 remote.json）：`GERDR_RELAY_URL`、`GERDR_RELAY_HOST`、`GERDR_ACME_DNS`、`GERDR_<DNS 凭据>`
（比如 `GERDR_CLOUDFLARE_DNS_API_TOKEN`）、`GERDR_ACME_EMAIL`，或者自己给证书 `GERDR_TLS_CERT` / `GERDR_TLS_KEY`；
`GERDR_DEVICE_TTL_DAYS`（默认 90，0 = 永不过期）、`GERDR_REAUTH_HOURS`（默认 24，0 = 不要求）。

## 直接访问（Tailscale、frp、端口转发）代替中继

不走中继：Gerdr 在局域网直连口（7878）上用你域名的证书对外，配对码、passkey 的规则和中继一样。手机怎么连到这台 Mac
是你自己的网络，Gerdr 不关心是哪种。证书照样要（passkey 只在 HTTPS 下能用）；DNS-01 签证书不需要公网连得到这台 Mac。

**用 Tailscale，不要域名**（要配的最少）：

1. Mac 和手机都装 Tailscale，登进同一个 tailnet。在 Tailscale 管理后台的 [DNS 页](https://login.tailscale.com/admin/dns)打开
   *HTTPS Certificates*（MagicDNS 也要开，默认就是开的）。
2. 设置 →「远程访问」→「Tailscale（不要域名）」→ 连接（或 `gerdr remote direct --tailscale`）。Gerdr 用这台 Mac 在 tailnet
   里的名字，证书向 Tailscale 要，它负责续期。
3. 手机开着 Tailscale，打开 `https://<机器名>.<tailnet>.ts.net:7878`，照常配对。

这个名字会出现在公开的证书透明度日志里，所有 Tailscale 的 HTTPS 名字都一样。以后想换成自己的域名，先移除远程访问再重新连（passkey 绑在名字上）。

**用 Tailscale 和自己的域名**（什么都不暴露到公网）：

1. Mac 和手机都装 Tailscale，登进同一个 tailnet。
2. 设置 →「远程访问」→「直接访问」：填域名，「地址里的端口」留空，连接。然后在「证书」里选 DNS 服务商
   （或者 `gerdr remote direct gerdr.example.com --dns cloudflare --env CLOUDFLARE_DNS_API_TOKEN=…`）。
3. 加一条 A 记录：`gerdr.example.com → 这台 Mac 的 Tailscale 地址 100.x.y.z`（设置页和 `gerdr remote status` 里有）。
4. 手机开着 Tailscale，打开 `https://gerdr.example.com:7878`，照常配对。

为什么不需要任何代理：域名本身就指向 tailnet 里的地址，手机上的 Tailscale 是系统级 VPN，浏览器和 App 都连得过去。
Tailscale 关着时这个域名连不上：安卓 App 在家还能走局域网，浏览器不行。地址带端口，是因为 macOS 不让 Gerdr 在具体地址上占 443。

**用 frp 或路由器端口转发**（对外是 443）：

1. 设置 →「远程访问」→「直接访问」：填域名，「地址里的端口」填 `443`（或 `gerdr remote direct gerdr.example.com --url-port 443 …`）。
2. A 记录指向 frps 那台机器（或你的公网 IP），地址就是 `https://gerdr.example.com`。
3. frpc（frps 要有 `vhostHTTPSPort = 443`）：

   ```toml
   [[proxies]]
   name = "gerdr"
   type = "https"                          # 原样转 TLS；别用 https2http 插件
   customDomains = ["gerdr.example.com"]
   localIP = "127.0.0.1"
   localPort = 7878
   transport.proxyProtocolVersion = "v2"   # 可选：Gerdr 能看到真实来源 IP，按 IP 的限速才分得开人
   ```

要知道的：macOS 第一次可能问是否允许 Gerdr 接受传入连接，要允许（不然什么都进不来，也没有报错）。地址带端口时，
安卓 App 里不能直接刷 passkey（安卓只去 443 核对 App），登录改走浏览器批准，或者在 Mac 上「设置 → 远程访问 → 批准 App 登录」输码。

安卓 App 用的是同一份界面，补上浏览器做不到的：在家时局域网直连（origin 不变、TLS 不变）、App 里刷 passkey、后台通知。见 [docs/dev/ANDROID.md](../dev/ANDROID.md)。
[iOS App](../dev/IOS.md) 也一样，只是还没有后台通知（局域网直连要 iOS 17 及以上）。
