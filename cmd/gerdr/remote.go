package main

import (
	"context"
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
		Short: "远程访问：手机 / 别的电脑经云端中继打开这台机器上的 gerdr（配对码 + passkey）",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "远程访问现在怎么样（地址、中继连没连上、证书）",
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
				fmt.Println("  没开远程访问。开法：设置 → 远程访问里填中继地址、主机名和邀请码；或者 gerdr remote join <中继地址> <主机名> <邀请码>")
				return nil
			}
			fmt.Printf("  地址：%s\n  中继：%s\n", st.URL, st.RelayURL)
			if st.State != "" {
				fmt.Printf("  状态：%s\n", st.State)
			}
			if st.Err != "" {
				fmt.Printf("  ⚠️  %s\n", st.Err)
			}
			if st.CertExpires != nil {
				fmt.Printf("  证书：%s 到期\n", st.CertExpires.Format("2006-01-02"))
			}
			if a := remote.AccountURI(config.FromEnv().Dir); a != "" {
				fmt.Printf("  ACME 账户（CAA 的 accounturi）：%s\n", a)
			}
			if st.Locked {
				fmt.Println("  ⚠️  失败次数太多，已暂停接受新设备：gerdr remote unlock 解开")
			}
			return nil
		},
	})
	cmd.AddCommand(joinCmd())
	cmd.AddCommand(&cobra.Command{
		Use:   "pair",
		Short: "出一个配对码（5 分钟、一次性）和二维码：新设备扫一下就进来了",
		RunE: func(*cobra.Command, []string) error {
			c, err := newAPIClient()
			if err != nil {
				return fmt.Errorf("%w\n  配对码只在内存里，要 gerdr 在跑才出得来", err)
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
			fmt.Printf("\n  扫码，或者打开 %s 输入配对码：\n\n      %s %s\n\n  %s 前有效，用一次就废。\n\n",
				base, out.Code[:4], out.Code[4:], out.Expires.Format("15:04"))
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "devices",
		Short: "列出配过对的设备",
		RunE: func(*cobra.Command, []string) error {
			devs, err := remoteDevices()
			if err != nil {
				return err
			}
			if len(devs) == 0 {
				fmt.Println("  还没有配过对的设备。")
				return nil
			}
			for _, d := range devs {
				fmt.Printf("  %s  %-22s 最后 %s（%s）\n", d.ID, d.Label, d.LastSeen.Local().Format("01-02 15:04"), d.LastIP)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "revoke <设备 id|all>",
		Short: "踢掉一台设备（all = 全部设备 + 全部 passkey）",
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
				return fmt.Errorf("没有这台设备：%s", arg)
			}
			fmt.Println("  " + revokedMsg(arg, 1, 0, label))
			return nil
		},
	})
	pk := &cobra.Command{
		Use:   "passkeys",
		Short: "列出注册过的 passkey",
		RunE: func(*cobra.Command, []string) error {
			s, err := offlineRemote()
			if err != nil {
				return err
			}
			keys := s.Passkeys.List()
			if len(keys) == 0 {
				fmt.Println("  还没有注册过 passkey。在远程打开的 gerdr 里：设置 → 远程访问 → 加一把 passkey。")
			}
			for _, k := range keys {
				fmt.Printf("  %s  %-22s 添加于 %s，最后用 %s\n", k.ID, k.Label, k.Created.Local().Format("2006-01-02"), k.LastUsed.Local().Format("01-02 15:04"))
			}
			return nil
		},
	}
	pk.AddCommand(&cobra.Command{
		Use:   "revoke <id|all>",
		Short: "删掉一把 passkey（all = 全部）",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			// 服务在跑时它内存里那份才算数，得走它；它没有「只清 passkey」的口 —— 要急停就该连设备一起踢
			if c, err := newAPIClient(); err == nil {
				if args[0] == "all" {
					return fmt.Errorf("gerdr 在跑：要全删请用 gerdr remote revoke all（连设备一起踢），或者先停掉 gerdr")
				}
				return c.do("DELETE", "/api/remote/passkeys/"+url.PathEscape(args[0]), nil, nil)
			}
			s, err := offlineRemote()
			if err != nil {
				return err
			}
			if args[0] == "all" {
				fmt.Printf("  删掉了 %d 把 passkey\n", s.Passkeys.DeleteAll())
				return nil
			}
			if _, ok := s.Passkeys.Delete(args[0]); !ok {
				return fmt.Errorf("没有这把 passkey：%s", args[0])
			}
			return nil
		},
	})
	cmd.AddCommand(pk)
	cmd.AddCommand(&cobra.Command{
		Use:   "unlock",
		Short: "解开全局熔断（有人猛猜配对码之后，新设备会被暂停接受）",
		RunE: func(*cobra.Command, []string) error {
			c, err := newAPIClient()
			if err != nil {
				return fmt.Errorf("%w（熔断只在内存里，gerdr 没在跑就没有熔断）", err)
			}
			return c.do("POST", "/api/remote/unlock", nil, nil)
		},
	})
	return cmd
}

func revokedMsg(arg string, n, keys int, label string) string {
	if arg != "all" {
		return "踢掉了 " + label
	}
	if keys == 0 {
		return fmt.Sprintf("踢掉了 %d 台设备", n)
	}
	// passkey 那半句要说出来：人是冲着踢设备来的，passkey 一起没了必须当场告诉他
	return fmt.Sprintf("踢掉了 %d 台设备，并删掉了 %d 把 passkey（急停要连因子一起拿掉，否则 passkey 登录能立刻换回一份新凭据）", n, keys)
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
func joinCmd() *cobra.Command {
	var dns, email, cert, key string
	var tokens []string
	var staging bool
	c := &cobra.Command{
		Use:   "join <中继地址> <主机名> <邀请码>",
		Short: "用邀请码把这台机器注册到云端中继（dialhome）上（邀请码在中继的网页后台里出，或者 VPS 上 dialhome invite <主机名>）",
		Long: "注册到中继，并把配置写进 <数据目录>/remote/remote.json（0600）；gerdr 在跑就当场生效，没在跑就下次启动生效。\n" +
			"也可以不敲命令：设置 → 远程访问里填同样三样。\n" +
			"证书可以一起给：--dns 让 gerdr 走 DNS-01 自己签（推荐），或者 --cert / --key 指两个文件；\n" +
			"也可以不给，注册完在 App 的设置 → 远程访问 → 证书里选服务商、填凭据。\n\n" +
			"DNS 服务商的凭据用 --env 给（可以给多个），名字照 lego 的写法、不带 GERDR_ 前缀：\n\n" +
			"  gerdr remote join https://relay.example.com gerdr.example.com <邀请码> \\\n" +
			"    --dns cloudflare --env CLOUDFLARE_DNS_API_TOKEN=…\n\n" +
			"编译进来的 DNS 服务商：" + strings.Join(acme.Providers(), " / "),
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
			if dns != "" {
				f.ACMEDNS, f.TLSCert, f.TLSKey = dns, "", ""
			}
			if cert != "" || key != "" {
				f.TLSCert, f.TLSKey, f.ACMEDNS = cert, key, ""
			}
			if email != "" {
				f.ACMEEmail = email
			}
			if staging {
				f.ACMEStaging = true
			}
			for _, kv := range tokens {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || k == "" {
					return fmt.Errorf("--env 要写成 名字=值（给的是 %q）", kv)
				}
				if f.Env == nil {
					f.Env = map[string]string{}
				}
				f.Env[strings.TrimPrefix(k, "GERDR_")] = v
			}
			// 证书来源可以不在这儿给（之后在设置 → 远程访问 → 证书里填）；给了就先核一下名字，邀请码还没用掉
			r := config.Remote{ACMEDNS: f.ACMEDNS}
			if r.ACMEDNS != "" && acme.EnvHint(r.ACMEDNS) == "" {
				return fmt.Errorf("不认识的 DNS 服务商 %q，编译进来的有：%s", r.ACMEDNS, strings.Join(acme.Providers(), " / "))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := remote.Join(ctx, env.Dir, relayURL, host, args[2]); err != nil {
				return err
			}
			if err := config.WriteRemoteFile(env.Dir, f); err != nil {
				return fmt.Errorf("注册好了，但配置没写进去：%w", err)
			}
			// gerdr 在跑就让它当场重读，不用重启
			applied := "重启 gerdr 之后生效（mac App 退出再开）"
			if c, err := newAPIClient(); err == nil {
				if err := c.do("POST", "/api/remote/reload", nil, nil); err == nil {
					applied = "正在跑的 gerdr 已经换上了"
				}
			}
			fmt.Printf("\n  ✓ 注册好了：%s → %s\n  配置写在 %s/remote.json，%s。\n",
				host, relayURL, config.RemoteDir(env.Dir), applied)
			printRelayDNS(env.Dir, host)
			return nil
		},
	}
	c.Flags().StringVar(&dns, "dns", "", "DNS 服务商（走 DNS-01 自己签证书）："+strings.Join(acme.Providers(), " / "))
	c.Flags().StringArrayVar(&tokens, "env", nil, "DNS 服务商的凭据，名字=值（比如 CLOUDFLARE_DNS_API_TOKEN=…），可以给多个")
	c.Flags().StringVar(&email, "email", "", "ACME 账号邮箱（可空；给了能收到到期提醒）")
	c.Flags().BoolVar(&staging, "staging", false, "用 Let's Encrypt 的测试环境（调试用：正式环境同一个域名一周只给 5 张）")
	c.Flags().StringVar(&cert, "cert", "", "不自己签：证书文件（fullchain）")
	c.Flags().StringVar(&key, "key", "", "不自己签：私钥文件")
	return c
}

func printRelayDNS(dir, host string) {
	zone := host
	if i := strings.IndexByte(host, '.'); i > 0 {
		zone = host[i+1:]
	}
	acct := remote.AccountURI(dir)
	if acct == "" {
		acct = "<这台机器的 ACME 账户：gerdr 签下第一张证书之后 gerdr remote status 里有>"
	}
	fmt.Printf(`
  最后去 DNS 上加几条（一次性）：

       %s     A    <中继 VPS 的 IP>
       CAA  0 issue "letsencrypt.org; accounturi=%s; validationmethods=dns-01"

     A 记录：手机连的是这个名字，中继按 SNI 把它转给这台机器（中继那边的 SNI 路由也要放行这个名字）。
     CAA 挡的是「中继占着这个 IP 去自己签一张你的证书」—— 中继能签证书就能做中间人（docs/dev/REMOTE.md）。
     挂在哪一级：已经有一条覆盖 %s 的 CAA（比如挂在 %s 上）的话，在那儿**再加一条** issue（每台机器的
     ACME 账户各一条）；单独挂在 %s 上也行，但它用的是通配 A 记录时得同时给它一条显式的 A 记录 ——
     名字上只要有任何记录，通配就不再替它生效。

`, host, acct, host, zone, host)
}
