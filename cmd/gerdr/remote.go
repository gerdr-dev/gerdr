package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zbysir/dialhome/relay"

	"github.com/gerdr-dev/gerdr/internal/acme"
	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/lan"
	"github.com/gerdr-dev/gerdr/internal/qr"
	"github.com/gerdr-dev/gerdr/internal/remote"
)

// remoteCmd：远程访问（云端中继 + 配对码 + passkey）。设计见 docs/dev/REMOTE.md。
//
// 这几条命令**不在** sysenv.SafeGerdrCLI 的自动放行名单里（那里只有 session / ui）：agent 跑
// `gerdr remote pair` 就能给外面的人出一个配对码，必须每次都过人的眼。
func remoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: i18n.S("远程访问：手机 / 别的电脑经云端中继打开这台机器上的 Gerdr（配对码 + passkey）", "Remote access: open this machine's Gerdr from a phone or another computer via a cloud relay (pairing code + passkey)"),
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: i18n.S("远程访问现在怎么样（地址、中继连没连上、证书）", "Show remote access status (address, relay connection, certificate)"),
		RunE: func(*cobra.Command, []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			var out struct{ Status remote.Status }
			if err := c.do("GET", "/api/remote/status", nil, &out); err != nil {
				return err
			}
			st := out.Status
			if !st.Enabled {
				fmt.Println(i18n.S("  没开远程访问。开法：设置 → 远程访问；或者 gerdr remote join <中继地址> <主机名> <邀请码>（云端中继），或者 gerdr remote direct <主机名>（直接访问：Tailscale / frp / 端口转发）", "  Remote access is off. Turn it on in Settings → Remote access, or run gerdr remote join <relay-url> <host> <invite-code> (cloud relay) or gerdr remote direct <host> (direct access: Tailscale / frp / a port forward)"))
				return nil
			}
			fmt.Printf(i18n.S("  地址：%s\n", "  Address: %s\n"), st.URL)
			if st.RelayURL != "" {
				fmt.Printf(i18n.S("  中继：%s\n", "  Relay:   %s\n"), st.RelayURL)
			}
			if len(st.TailnetAddrs) > 0 {
				fmt.Printf(i18n.S("  这台 Mac 的 Tailscale 地址：%s\n", "  This Mac's Tailscale address: %s\n"), strings.Join(st.TailnetAddrs, ", "))
			}
			if st.State != "" {
				fmt.Printf(i18n.S("  状态：%s\n", "  State:   %s\n"), st.State)
			}
			if st.Err != "" {
				fmt.Printf("  ⚠️  %s\n", st.Err)
			}
			if st.CertExpires != nil {
				fmt.Printf(i18n.S("  证书：%s 到期\n", "  Certificate: expires %s\n"), st.CertExpires.Format("2006-01-02"))
			}
			if a := remote.AccountURI(config.FromEnv().Dir); a != "" {
				fmt.Printf(i18n.S("  ACME 账户（CAA 的 accounturi）：%s\n", "  ACME account (CAA accounturi): %s\n"), a)
			}
			if st.Locked {
				fmt.Println(i18n.S("  ⚠️  失败次数太多，已暂停接受新设备：gerdr remote unlock 解开", "  ⚠️  Too many failed attempts; new devices are paused. Run gerdr remote unlock to resume"))
			}
			return nil
		},
	})
	cmd.AddCommand(joinCmd())
	cmd.AddCommand(directCmd())
	cmd.AddCommand(&cobra.Command{
		Use:   "pair",
		Short: i18n.S("出一个配对码（5 分钟、一次性）和二维码：新设备扫一下就进来了", "Show a one-time pairing code (valid 5 minutes) and QR code: scan it on the new device to get in"),
		RunE: func(*cobra.Command, []string) error {
			c, err := newAPIClient()
			if err != nil {
				return fmt.Errorf(i18n.S("%w\n  配对码只在内存里，要 Gerdr 在跑才出得来", "%w\n  Pairing codes live in memory only; Gerdr must be running to issue one"), err)
			}
			var out struct {
				Code    string
				Expires time.Time
				URL     string
			}
			if err := c.do("POST", "/api/remote/pair-code", nil, &out); err != nil {
				return err
			}
			fmt.Println()
			for _, l := range qr.Render(out.URL) {
				fmt.Println("    " + l)
			}
			base := strings.TrimSuffix(strings.SplitN(out.URL, "?", 2)[0], "/")
			fmt.Printf(i18n.S("\n  扫码，或者打开 %s 输入配对码：\n\n      %s %s\n\n  %s 前有效，用一次就废。\n\n", "\n  Scan the code, or open %s and enter the pairing code:\n\n      %s %s\n\n  Valid until %s, single use.\n\n"),
				base, out.Code[:4], out.Code[4:], out.Expires.Format("15:04"))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "devices",
		Short: i18n.S("列出配过对的设备", "List paired devices"),
		RunE: func(*cobra.Command, []string) error {
			devs, err := remoteDevices()
			if err != nil {
				return err
			}
			if len(devs) == 0 {
				fmt.Println(i18n.S("  还没有配过对的设备。", "  No paired devices yet."))
				return nil
			}
			for _, d := range devs {
				fmt.Printf(i18n.S("  %s  %-22s 最后 %s（%s）\n", "  %s  %-22s last seen %s (%s)\n"), d.ID, d.Label, d.LastSeen.Local().Format("01-02 15:04"), d.LastIP)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   i18n.S("revoke <设备 id|all>", "revoke <device-id|all>"),
		Short: i18n.S("踢掉一台设备（all = 全部设备 + 全部 passkey）", "Revoke a device (all = every device + every passkey)"),
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			arg := args[0]
			if c, err := newAPIClient(); err == nil {
				var out struct {
					Revoked, Passkeys int
					Label             string
				}
				path := "/api/remote/devices/" + url.PathEscape(arg)
				if arg == "all" {
					path = "/api/remote/devices"
				}
				if err := c.do("DELETE", path, nil, &out); err != nil {
					return err
				}
				fmt.Println("  " + revokedMsg(arg, out.Revoked, out.Passkeys, out.Label))
				return nil
			}
			// 服务没在跑：直接改文件（凭据文件只有服务在跑时才被它独占）
			s, err := offlineRemote()
			if err != nil {
				return err
			}
			if arg == "all" {
				n, k := auth.RevokeEverything(s.Auth, s.Passkeys)
				fmt.Println("  " + revokedMsg(arg, n, k, ""))
				return nil
			}
			label, ok := s.Auth.Revoke(arg)
			if !ok {
				return fmt.Errorf(i18n.S("没有这台设备：%s", "no such device: %s"), arg)
			}
			fmt.Println("  " + revokedMsg(arg, 1, 0, label))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   i18n.S("reverify <设备 id>", "reverify <device-id>"),
		Short: i18n.S("让一台设备下次访问时重新验证（passkey / 浏览器批准 / App 的 Face ID），不踢掉它", "Make a device re-verify on its next request (passkey / browser approval / the App's Face ID) without revoking it"),
		Long: i18n.S("让一台设备下次访问时重新验证，不踢掉它：凭据、passkey、App 密钥都留着，只是要再证明一次是本人。\n"+
			"只在注册过 passkey 时有用（没有 passkey 就没有「重新验证」这回事）。设备 id 见 gerdr remote devices。",
			"Make a device re-verify on its next request without revoking it: its credential, passkeys and App key stay, it\n"+
				"just has to prove it's you again. Only matters once a passkey is registered (otherwise there's no re-verification).\n"+
				"Device ids: gerdr remote devices."),
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id := args[0]
			var out struct {
				Label    string
				Passkeys int
			}
			if c, err := newAPIClient(); err == nil {
				if err := c.do("POST", "/api/remote/devices/"+url.PathEscape(id)+"/reverify", nil, &out); err != nil {
					return err
				}
			} else {
				s, err := offlineRemote()
				if err != nil {
					return err
				}
				label, ok := s.Auth.RequireReverify(id)
				if !ok {
					return fmt.Errorf(i18n.S("没有这台设备：%s", "no such device: %s"), id)
				}
				out.Label, out.Passkeys = label, s.Passkeys.Count()
			}
			fmt.Printf(i18n.S("  %s 下次访问时要重新验证\n", "  %s will re-verify on its next request\n"), out.Label)
			if out.Passkeys == 0 {
				fmt.Println(i18n.S("  （还没注册 passkey，所以眼下不会要求它验证）", "  (no passkey registered yet, so it won't be asked for now)"))
			}
			return nil
		},
	})
	pk := &cobra.Command{
		Use:   "passkeys",
		Short: i18n.S("列出注册过的 passkey", "List registered passkeys"),
		RunE: func(*cobra.Command, []string) error {
			s, err := offlineRemote()
			if err != nil {
				return err
			}
			keys := s.Passkeys.List()
			if len(keys) == 0 {
				fmt.Println(i18n.S("  还没有注册过 passkey。在远程打开的 Gerdr 里：设置 → 远程访问 → 加一把 passkey。", "  No passkeys registered yet. In Gerdr opened remotely: Settings → Remote access → Add a passkey."))
			}
			for _, k := range keys {
				fmt.Printf(i18n.S("  %s  %-22s 添加于 %s，最后用 %s\n", "  %s  %-22s added %s, last used %s\n"), k.ID, k.Label, k.Created.Local().Format("2006-01-02"), k.LastUsed.Local().Format("01-02 15:04"))
			}
			return nil
		},
	}
	pk.AddCommand(&cobra.Command{
		Use:   "revoke <id|all>",
		Short: i18n.S("删掉一把 passkey（all = 全部）", "Delete a passkey (all = every one)"),
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			// 服务在跑时它内存里那份才算数，得走它；它没有「只清 passkey」的口 —— 要急停就该连设备一起踢
			if c, err := newAPIClient(); err == nil {
				if args[0] == "all" {
					return errors.New(i18n.S("Gerdr 在跑：要全删请用 gerdr remote revoke all（连设备一起踢），或者先停掉 Gerdr", "Gerdr is running: to delete them all use gerdr remote revoke all (revokes devices too), or stop Gerdr first"))
				}
				return c.do("DELETE", "/api/remote/passkeys/"+url.PathEscape(args[0]), nil, nil)
			}
			s, err := offlineRemote()
			if err != nil {
				return err
			}
			if args[0] == "all" {
				n := s.Passkeys.DeleteAll()
				if i18n.IsZH() {
					fmt.Printf("  删掉了 %d 把 passkey\n", n)
				} else {
					fmt.Printf("  Deleted %d %s\n", n, plural(n, "passkey", "passkeys"))
				}
				return nil
			}
			if _, ok := s.Passkeys.Delete(args[0]); !ok {
				return fmt.Errorf(i18n.S("没有这把 passkey：%s", "no such passkey: %s"), args[0])
			}
			return nil
		},
	})
	cmd.AddCommand(pk)
	cmd.AddCommand(&cobra.Command{
		Use:   "unlock",
		Short: i18n.S("解开全局熔断（有人猛猜配对码之后，新设备会被暂停接受）", "Lift the global lockout (after repeated wrong pairing codes, new devices are paused)"),
		RunE: func(*cobra.Command, []string) error {
			c, err := newAPIClient()
			if err != nil {
				return fmt.Errorf(i18n.S("%w（熔断只在内存里，Gerdr 没在跑就没有熔断）", "%w (the lockout lives in memory only; if Gerdr is not running there is none)"), err)
			}
			return c.do("POST", "/api/remote/unlock", nil, nil)
		},
	})
	return cmd
}

func revokedMsg(arg string, n, keys int, label string) string {
	if !i18n.IsZH() {
		if arg != "all" {
			return "Revoked " + label
		}
		if keys == 0 {
			return fmt.Sprintf("Revoked %d %s", n, plural(n, "device", "devices"))
		}
		return fmt.Sprintf("Revoked %d %s and deleted %d %s (an emergency stop has to remove the factor too, or a passkey login could get fresh credentials right away)", n, plural(n, "device", "devices"), keys, plural(keys, "passkey", "passkeys"))
	}
	if arg != "all" {
		return "踢掉了 " + label
	}
	if keys == 0 {
		return fmt.Sprintf("踢掉了 %d 台设备", n)
	}
	// passkey 那半句要说出来：人是冲着踢设备来的，passkey 一起没了必须当场告诉他
	return fmt.Sprintf("踢掉了 %d 台设备，并删掉了 %d 把 passkey（急停要连因子一起拿掉，否则 passkey 登录能立刻换回一份新凭据）", n, keys)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func remoteDevices() ([]auth.Device, error) {
	if c, err := newAPIClient(); err == nil {
		var out struct{ Devices []auth.Device }
		return out.Devices, c.do("GET", "/api/remote/devices", nil, &out)
	}
	s, err := offlineRemote()
	if err != nil {
		return nil, err
	}
	return s.Auth.Devices(), nil
}

// offlineRemote 服务没在跑时直接读写凭据文件。
func offlineRemote() (*remote.Service, error) {
	env := config.FromEnv()
	cfg, err := config.LoadRemote(env.Dir)
	if err != nil {
		return nil, err
	}
	return remote.Open(env.Dir, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// joinCmd：`gerdr remote join` —— 拿中继那边出的邀请码，把这台机器注册上去，配置写进 remote.json。
//
// **先核配置、再用邀请码**：邀请码一次性、30 分钟过期，用掉了才发现配置不对就得回 VPS 再出一个。
// 证书来源（--dns / --cert）可以不给：注册完到设置 → 远程访问 → 证书里填，不用重启。
// certOpts: the certificate flags join and forward share (--dns / --env / --email / --staging or --cert / --key).
type certOpts struct {
	dns, email, cert, key string
	tokens                []string
	staging               bool
}

func (o *certOpts) bind(c *cobra.Command) {
	c.Flags().StringVar(&o.dns, "dns", "", i18n.S("DNS 服务商（走 DNS-01 自己签证书）：", "DNS provider (issue the certificate yourself via DNS-01): ")+strings.Join(acme.Providers(), " / "))
	c.Flags().StringArrayVar(&o.tokens, "env", nil, i18n.S("DNS 服务商的凭据，名字=值（比如 CLOUDFLARE_DNS_API_TOKEN=…），可以给多个", "DNS provider credential as NAME=value (e.g. CLOUDFLARE_DNS_API_TOKEN=…), repeatable"))
	c.Flags().StringVar(&o.email, "email", "", i18n.S("ACME 账号邮箱（可空；给了能收到到期提醒）", "ACME account email (optional; enables expiry reminders)"))
	c.Flags().BoolVar(&o.staging, "staging", false, i18n.S("用 Let's Encrypt 的测试环境（调试用：正式环境同一个域名一周只给 5 张）", "use Let's Encrypt staging (for debugging: production allows only 5 certificates per domain per week)"))
	c.Flags().StringVar(&o.cert, "cert", "", i18n.S("不自己签：证书文件（fullchain）", "bring your own: certificate file (fullchain)"))
	c.Flags().StringVar(&o.key, "key", "", i18n.S("不自己签：私钥文件", "bring your own: private key file"))
}

// apply writes the flags into f and checks the provider name (before an invite code gets spent).
func (o *certOpts) apply(f *config.RemoteFile) error {
	if o.dns != "" {
		f.ACMEDNS, f.TLSCert, f.TLSKey = o.dns, "", ""
	}
	if o.cert != "" || o.key != "" {
		f.TLSCert, f.TLSKey, f.ACMEDNS = o.cert, o.key, ""
	}
	if o.email != "" {
		f.ACMEEmail = o.email
	}
	if o.staging {
		f.ACMEStaging = true
	}
	for _, kv := range o.tokens {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return fmt.Errorf(i18n.S("--env 要写成 名字=值（给的是 %q）", "--env must be NAME=value (got %q)"), kv)
		}
		if f.Env == nil {
			f.Env = map[string]string{}
		}
		f.Env[strings.TrimPrefix(k, "GERDR_")] = v
	}
	if f.ACMEDNS != "" && acme.EnvHint(f.ACMEDNS) == "" {
		return fmt.Errorf(i18n.S("不认识的 DNS 服务商 %q，编译进来的有：%s", "unknown DNS provider %q; built-in ones: %s"), f.ACMEDNS, strings.Join(acme.Providers(), " / "))
	}
	return nil
}

// reloadRunning asks a running Gerdr to read remote.json again; says which way it went.
func reloadRunning() string {
	if c, err := newAPIClient(); err == nil {
		if err := c.do("POST", "/api/remote/reload", nil, nil); err == nil {
			return i18n.S("正在跑的 Gerdr 已经换上了", "the running Gerdr has picked it up")
		}
	}
	return i18n.S("重启 Gerdr 之后生效（mac App 退出再开）", "it takes effect after Gerdr restarts (quit and reopen the mac App)")
}

func joinCmd() *cobra.Command {
	var o certOpts
	c := &cobra.Command{
		Use:   i18n.S("join <中继地址> <主机名> <邀请码>", "join <relay-url> <host> <invite-code>"),
		Short: i18n.S("用邀请码把这台机器注册到云端中继（dialhome）上（邀请码在中继的网页后台里出，或者 VPS 上 dialhome invite <主机名>）", "Register this machine with a cloud relay (dialhome) using an invite code (get one from the relay's web admin, or run dialhome invite <host> on the VPS)"),
		Long: i18n.S("注册到中继，并把配置写进 <数据目录>/remote/remote.json（0600）；Gerdr 在跑就当场生效，没在跑就下次启动生效。\n"+
			"也可以不敲命令：设置 → 远程访问里填同样三样。\n"+
			"证书可以一起给：--dns 让 Gerdr 走 DNS-01 自己签（推荐），或者 --cert / --key 指两个文件；\n"+
			"也可以不给，注册完在 App 的设置 → 远程访问 → 证书里选服务商、填凭据。\n\n"+
			"DNS 服务商的凭据用 --env 给（可以给多个），名字照 lego 的写法、不带 GERDR_ 前缀：\n\n"+
			"  gerdr remote join https://relay.example.com gerdr.example.com <邀请码> \\\n"+
			"    --dns cloudflare --env CLOUDFLARE_DNS_API_TOKEN=…\n\n"+
			"编译进来的 DNS 服务商：",
			"Registers with the relay and writes the config to <data-dir>/remote/remote.json (0600); takes effect at once if Gerdr is running, otherwise on next start.\n"+
				"You can skip the command: enter the same three things in Settings → Remote access.\n"+
				"You can pass a certificate source too: --dns lets Gerdr issue its own via DNS-01 (recommended), or --cert / --key point to two files;\n"+
				"or leave it out and pick a provider and enter credentials later in the App under Settings → Remote access → Certificate.\n\n"+
				"Pass DNS provider credentials with --env (repeatable), named as in lego, without the GERDR_ prefix:\n\n"+
				"  gerdr remote join https://relay.example.com gerdr.example.com <invite-code> \\\n"+
				"    --dns cloudflare --env CLOUDFLARE_DNS_API_TOKEN=…\n\n"+
				"Built-in DNS providers: ") + strings.Join(acme.Providers(), " / "),
		Args: cobra.ExactArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			env := config.FromEnv()
			host, err := relay.CleanHost(args[1])
			if err != nil {
				return err
			}
			relayURL := strings.TrimRight(args[0], "/")
			f, err := config.ReadRemoteFile(env.Dir)
			if err != nil {
				return err
			}
			f.RelayURL, f.Host = relayURL, host
			// 证书来源可以不在这儿给（之后在设置 → 远程访问 → 证书里填）；给了就先核一下名字，邀请码还没用掉
			if err := o.apply(&f); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := remote.Join(ctx, env.Dir, relayURL, host, args[2]); err != nil {
				return err
			}
			if err := config.WriteRemoteFile(env.Dir, f); err != nil {
				return fmt.Errorf(i18n.S("注册好了，但配置没写进去：%w", "registered, but writing the config failed: %w"), err)
			}
			// gerdr 在跑就让它当场重读，不用重启
			applied := reloadRunning()
			fmt.Printf(i18n.S("\n  ✓ 注册好了：%s → %s\n  配置写在 %s/remote.json，%s。\n", "\n  ✓ Registered: %s → %s\n  Config written to %s/remote.json; %s.\n"),
				host, relayURL, config.RemoteDir(env.Dir), applied)
			printRelayDNS(env.Dir, host)
			return nil
		},
	}
	o.bind(c)
	return c
}

// directCmd: direct access instead of the relay (config.Remote.Direct, docs/dev/REMOTE.md "Direct access"). Gerdr only
// serves the LAN port with the domain's certificate; how phones get there is the person's own network.
func directCmd() *cobra.Command {
	var o certOpts
	var urlPort int
	c := &cobra.Command{
		Use: i18n.S("direct <主机名>", "direct <host>"),
		Short: i18n.S("直接访问，不走中继：Gerdr 在局域网直连口上对外，手机怎么连过来（Tailscale / frp / 端口转发）是你自己的网络",
			"Direct access, no relay: Gerdr serves on the LAN port, and how phones get there (Tailscale / frp / a port forward) is your own network"),
		Long: i18n.S("把配置写进 <数据目录>/remote/remote.json（去掉中继地址）；Gerdr 在跑就当场生效。\n"+
			"Gerdr 在局域网直连口（默认 7878，remote.json 的 lanPort）上用这个域名的证书对外，规则和中继那条路一样（配对码 / passkey）。\n"+
			"证书照样走 DNS-01，不需要公网连得到这台 Mac；参数和 join 一样。\n\n"+
			"手机打开的地址：\n"+
			"  Tailscale：A 记录指向这台 Mac 的 Tailscale 地址，地址是 https://<主机名>:7878（--url-port 不用给）\n"+
			"  frp / 路由器端口转发，对外是 443：A 记录指向那台公网机器，--url-port 443，地址就是 https://<主机名>\n"+
			"frp 用 type = \"https\" 或 \"tcp\" 原样转到 127.0.0.1:7878（别用 https2http 插件）；\n"+
			"加上 transport.proxyProtocolVersion = \"v2\" 的话，Gerdr 能看到真实来源 IP（按 IP 的限速才分得开人）。\n\n"+
			"  gerdr remote direct gerdr.example.com --dns cloudflare --env CLOUDFLARE_DNS_API_TOKEN=…",
			"Writes the config to <data-dir>/remote/remote.json (dropping any relay address); takes effect at once if Gerdr is running.\n"+
				"Gerdr serves the LAN port (default 7878, lanPort in remote.json) with this domain's certificate, under the same rules as the relay path (pairing codes / passkeys).\n"+
				"The certificate still comes through DNS-01, so this Mac needn't be reachable from the internet; flags are the same as join.\n\n"+
				"The address phones open:\n"+
				"  Tailscale: the A record points at this Mac's Tailscale address, the address is https://<host>:7878 (no --url-port)\n"+
				"  frp / a router's port forward facing the internet on 443: the A record points at that public machine, --url-port 443, the address is https://<host>\n"+
				"frp: type = \"https\" or \"tcp\" passed through to 127.0.0.1:7878 (not the https2http plugin);\n"+
				"with transport.proxyProtocolVersion = \"v2\" Gerdr sees the real client IP (so per-IP rate limits tell people apart).\n\n"+
				"  gerdr remote direct gerdr.example.com --dns cloudflare --env CLOUDFLARE_DNS_API_TOKEN=…"),
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			env := config.FromEnv()
			host, err := relay.CleanHost(args[0])
			if err != nil {
				return err
			}
			if urlPort < 0 || urlPort > 65535 {
				return fmt.Errorf(i18n.S("--url-port %d 不对", "--url-port %d is out of range"), urlPort)
			}
			f, err := config.ReadRemoteFile(env.Dir)
			if err != nil {
				return err
			}
			if f.Host != "" && f.Host != host {
				return fmt.Errorf(i18n.S("已经连着 %s 了：先在设置 → 远程访问里移除（设备和 passkey 绑在那个名字上）", "already set up for %s: remove it in Settings → Remote access first (devices and passkeys are bound to that name)"), f.Host)
			}
			if f.LanPort != nil && *f.LanPort <= 0 {
				return errors.New(i18n.S("remote.json 里 lanPort 是 0：直接访问走的就是局域网直连口，先把它开起来", "lanPort is 0 in remote.json, and direct access goes through the LAN port: turn it on first"))
			}
			f.RelayURL, f.Host, f.Direct, f.URLPort = "", host, true, urlPort
			if err := o.apply(&f); err != nil {
				return err
			}
			if err := config.WriteRemoteFile(env.Dir, f); err != nil {
				return err
			}
			r, _ := config.LoadRemote(env.Dir)
			fmt.Printf(i18n.S("\n  ✓ 配好了：https://%s（直接访问，Gerdr 在 %d 端口上听）\n  配置写在 %s/remote.json，%s。\n", "\n  ✓ Set up: https://%s (direct access, Gerdr listens on port %d)\n  Config written to %s/remote.json; %s.\n"),
				r.PublicHost(), r.LanPort, config.RemoteDir(env.Dir), reloadRunning())
			if ts := lan.TailnetAddrs(); len(ts) > 0 {
				fmt.Printf(i18n.S("  这台 Mac 的 Tailscale 地址：%s（用 Tailscale 的话 A 记录指向它）\n", "  This Mac's Tailscale address: %s (with Tailscale, point the A record at it)\n"), strings.Join(ts, " / "))
			}
			fmt.Println()
			return nil
		},
	}
	c.Flags().IntVar(&urlPort, "url-port", 0, i18n.S("手机打开的地址里的端口：不给 = 局域网直连口（Tailscale）；443 = 不带端口（frp / 端口转发对外是 443）", "port in the address phones open: none = the LAN port (Tailscale); 443 = no port (frp / a port forward facing the internet on 443)"))
	o.bind(c)
	return c
}

func printRelayDNS(dir, host string) {
	zone := host
	if i := strings.IndexByte(host, '.'); i > 0 {
		zone = host[i+1:]
	}
	acct := remote.AccountURI(dir)
	if acct == "" {
		acct = i18n.S("<这台机器的 ACME 账户：Gerdr 签下第一张证书之后 gerdr remote status 里有>", "<this machine's ACME account: shown in gerdr remote status once Gerdr issues its first certificate>")
	}
	fmt.Printf(i18n.S(`
  最后去 DNS 上加几条（一次性）：

       %s     A    <中继 VPS 的 IP>
       CAA  0 issue "letsencrypt.org; accounturi=%s; validationmethods=dns-01"

     A 记录：手机连的是这个名字，中继按 SNI 把它转给这台机器（中继那边的 SNI 路由也要放行这个名字）。
     CAA 挡的是「中继占着这个 IP 去自己签一张你的证书」—— 中继能签证书就能做中间人（docs/dev/REMOTE.md）。
     挂在哪一级：已经有一条覆盖 %s 的 CAA（比如挂在 %s 上）的话，在那儿**再加一条** issue（每台机器的
     ACME 账户各一条）；单独挂在 %s 上也行，但它用的是通配 A 记录时得同时给它一条显式的 A 记录 ——
     名字上只要有任何记录，通配就不再替它生效。

`, `
  Last, add a few DNS records (one time):

       %s     A    <relay VPS IP>
       CAA  0 issue "letsencrypt.org; accounturi=%s; validationmethods=dns-01"

     A record: phones connect to this name and the relay forwards it to this machine by SNI (the relay's SNI routing must allow this name too).
     CAA stops "the relay, holding this IP, issuing a certificate for you itself" — a relay that can get a certificate can play man-in-the-middle (docs/dev/REMOTE.md).
     Where to put it: if a CAA already covers %s (e.g. on %s), **add another** issue record there (one per machine's
     ACME account); putting it on %s alone also works, but if that name relies on a wildcard A record, give it an explicit A record too —
     once a name has any record of its own, the wildcard no longer applies to it.

`), host, acct, host, zone, host)
}
