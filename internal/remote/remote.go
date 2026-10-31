// Package remote 把远程访问的几块拼起来：设备表 / passkey / 限速（internal/auth）、证书（internal/acme
// 或自己给的文件）、云端中继的隧道（github.com/zbysir/dialhome/relay），以及隧道上那个 https 服务。
//
// 设计见 docs/dev/REMOTE.md。和 herdr-web 不一样的一条：**远程起不来不拦 gerdr 启动**。herdr-web 的
// 远程就是它的全部，配错了宁可拒绝启动；gerdr 的主界面在本机（mac App 起不来就什么都用不了），所以
// 远程这边出错只记在 Status 里，设置 →「远程访问」和 `gerdr remote status` 看得到。
package remote

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zbysir/dialhome/relay"

	"github.com/gerdr-dev/gerdr/internal/acme"
	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/lan"
)

type Service struct {
	// cfg 在设置里连上 / 移除远程访问时会整份换掉（不重启），读要过 Config() 或者持 mu
	cfg      config.Remote
	Dir      string // <数据目录>/remote
	Auth     *auth.Store
	Passkeys *auth.Passkeys
	Gate     *auth.Gate
	// Approvals：新设备上没有 passkey 可做 step-up 时，到电脑上批准（auth/approve.go）
	Approvals *auth.Approvals

	log *slog.Logger

	mu     sync.Mutex
	h      http.Handler
	cancel context.CancelFunc // 当前这一轮（SetCert 会收掉重来）
	certs  *certFile
	tunnel *relay.Tunnel
	hs     *http.Server
	err    string       // 起不来的原因（证书签不下来、没 join 过……）
	lanHS  *http.Server // 局域网直连口（lan.go）
	lanErr string       // 直连口起不来的原因（端口被占……）；不影响中继那条路
}

// Open 准备设备表、passkey 和限速。没开远程也照样打开：设置里要能看设备、踢设备，
// 命令行的 `gerdr remote devices / revoke` 也走这里。
func Open(dataDir string, cfg config.Remote, log *slog.Logger) (*Service, error) {
	dir := config.RemoteDir(dataDir)
	store, err := auth.New(auth.Config{Dir: dir, TTL: cfg.DeviceTTL, PairTTL: cfg.PairTTL})
	if err != nil {
		return nil, fmt.Errorf("读设备列表失败: %w", err)
	}
	rpid, origins := relyingParty(cfg)
	pk, err := auth.NewPasskeys(auth.PasskeyConfig{Dir: dir, Display: "gerdr", RPID: rpid, Origins: origins})
	if err != nil {
		return nil, err
	}
	g := auth.NewGate()
	s := &Service{cfg: cfg, Dir: dir, Auth: store, Passkeys: pk, Gate: g, Approvals: auth.NewApprovals(), log: log}
	g.Alert = s.alert
	return s, nil
}

// relyingParty：passkey 绑哪个域名。RPID 就是中继上的主机名：passkey 只在那个 origin 上能用。
// 没开远程时给空，passkey 那几个口就都是 409。
func relyingParty(cfg config.Remote) (rpid string, origins []string) {
	if !cfg.Enabled() {
		return "", nil
	}
	origins = []string{"https://" + cfg.Host}
	// Passkeys inside the Android app's WebView carry the signing cert hash as origin (docs/dev/ANDROID.md "passkey")
	for _, fp := range cfg.AndroidCerts {
		origins = append(origins, auth.AndroidOrigins(fp)...)
	}
	return cfg.Host, origins
}

// Config 是现在这份远程配置的拷贝。
func (s *Service) Config() config.Remote {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *Service) alert(msg string) { s.log.Warn("remote: " + msg) }

func (s *Service) setErr(e string) {
	s.mu.Lock()
	s.err = e
	s.mu.Unlock()
	if e != "" {
		s.log.Warn("remote access: " + e)
	}
}

// URL 是远程打开的地址；没开远程时为空。
func (s *Service) URL() string { return urlOf(s.Config()) }

func urlOf(cfg config.Remote) string {
	if !cfg.Enabled() {
		return ""
	}
	return "https://" + cfg.Host
}

// PairURL：扫码进来的链接。码在 query 里，服务端换成 cookie 之后 302 洗掉。
func (s *Service) PairURL(code string) string {
	if u := s.URL(); u != "" {
		return u + "/?pair=" + code
	}
	return ""
}

// Start 在后台把远程那条路拉起来：证书 → 隧道 → https。h 是已经盖过「远程」章的 handler
// （server.RemoteHandler），这里不再包。
func (s *Service) Start(h http.Handler) {
	go s.watchTamper()
	s.mu.Lock()
	s.h = h
	s.mu.Unlock()
	s.run()
}

// SetCert：设置页改了证书来源。只换证书那几项（中继地址、主机名不动 —— 它们是 join 时定的，
// passkey 的 RPID 也绑在主机名上），然后把远程那条路整条重来一遍，不用重启 gerdr。
func (s *Service) SetCert(dns, email string, staging bool, certFile, keyFile string) {
	s.stop()
	s.mu.Lock()
	s.cfg.ACMEDNS, s.cfg.ACMEEmail, s.cfg.ACMEStaging, s.cfg.TLSCert, s.cfg.TLSKey = dns, email, staging, certFile, keyFile
	s.err = ""
	s.mu.Unlock()
	s.run()
}

func (s *Service) run() {
	s.mu.Lock()
	cfg, h := s.cfg, s.h
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()
	if !cfg.Enabled() || h == nil {
		return
	}
	if p := cfg.TLSProblem(); p != "" {
		s.setErr(p)
		return
	}
	go func() {
		certFile, keyFile := cfg.TLSCert, cfg.TLSKey
		if cfg.ACMEDNS != "" {
			am := acme.NewManager(acme.Config{
				Dir: s.Dir, Domains: []string{cfg.Host}, Email: cfg.ACMEEmail,
				DNS: cfg.ACMEDNS, Staging: cfg.ACMEStaging,
			})
			// 第一次签要等 DNS 传播，一两分钟很正常：在后台等，别拖住整个 gerdr 的启动
			a := am.Ensure(false)
			if ctx.Err() != nil {
				return // 等的时候配置又改了：这一轮作废，新的那轮已经起来了
			}
			if a.Err != "" {
				if !strings.HasPrefix(a.Err, "签证书失败") { // 签发那一步的错误自己带着这个开头
					a.Err = "签证书失败：" + a.Err
				}
				s.setErr(a.Err)
				return
			} else if a.Renewed {
				s.log.Info("remote: issued certificate", "host", cfg.Host)
			}
			certFile, keyFile = am.Config().Files()
			go s.renewLoop(ctx, am)
		}
		cf, err := loadCert(certFile, keyFile)
		if err != nil {
			s.setErr("读证书失败：" + err.Error())
			return
		}
		// 直连口在中继之前起：中继连不上（VPS 挂了）时人在家里照样能用
		s.startLan(ctx, cfg, cf, h)
		tn, err := relay.Start(relay.HostConfig{
			URL: cfg.RelayURL, Host: cfg.Host, Dir: s.relayDir(),
			Log: func(f string, a ...any) { s.log.Info("remote: " + fmt.Sprintf(f, a...)) },
		})
		if errors.Is(err, relay.ErrNotJoined) {
			s.setErr(err.Error() + "：先跑一次 gerdr remote join")
			return
		}
		if err != nil {
			s.setErr(err.Error())
			return
		}
		// TLS 在这里终止：中继手里只有密文（docs/dev/REMOTE.md「中继能拿到什么」）。
		// 不发 HSTS 之类的头不用操心 —— 安全头由 server.guard 统一加。
		hs := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
		s.mu.Lock()
		if ctx.Err() != nil {
			s.mu.Unlock()
			tn.Close()
			return
		}
		s.tunnel, s.certs, s.hs, s.err = tn, cf, hs, ""
		s.mu.Unlock()
		ln := tls.NewListener(tn, &tls.Config{GetCertificate: cf.get, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}})
		if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) && ctx.Err() == nil {
			s.setErr("远程那个口挂了：" + err.Error())
		}
	}()
}

// stop 收掉当前这一轮：隧道、https、续期。
func (s *Service) stop() {
	s.mu.Lock()
	tn, hs, lhs, cancel := s.tunnel, s.hs, s.lanHS, s.cancel
	s.tunnel, s.hs, s.certs, s.cancel, s.lanHS, s.lanErr = nil, nil, nil, nil, nil, ""
	s.mu.Unlock()
	if lhs != nil {
		_ = lhs.Close()
	}
	if cancel != nil {
		cancel()
	}
	if tn != nil {
		tn.Close()
	}
	if hs != nil {
		ctx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = hs.Shutdown(ctx)
	}
}

func (s *Service) relayDir() string { return s.Dir + "/relay" }

// renewLoop 半天看一次要不要续。续完不用重启：certFile 按 mtime 热重载。
func (s *Service) renewLoop(ctx context.Context, am *acme.Manager) {
	t := time.NewTicker(12 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if a := am.Ensure(false); a.Err != "" {
			s.alert("证书续期失败（先看 DNS 凭据还在不在）：" + a.Err)
		} else if a.Renewed {
			s.log.Info("remote: certificate renewed")
		}
	}
}

// watchTamper 盯着两个凭据文件：被外部改过就吼一声。它们怕写不怕读 —— 往里加一行就等于
// 给自己发一份能远程进来的凭据，而这台机器上的 agent 是以你的身份跑的，写得动。
func (s *Service) watchTamper() {
	for range time.Tick(30 * time.Second) {
		s.Auth.CheckTampered(s.alert)
		s.Passkeys.CheckTampered(s.alert)
		s.Auth.Flush()
	}
}

func (s *Service) Close() {
	s.stop()
	s.Auth.Flush()
}

// Status 是给设置页 / `gerdr remote status` 看的一句话状态。
type Status struct {
	Enabled bool `json:"enabled"`
	// EnvLocked：中继地址 / 主机名是环境变量给的（盖过 remote.json），设置里连接 / 移除改不动它
	EnvLocked bool   `json:"envLocked,omitempty"`
	Host      string `json:"host,omitempty"`
	URL       string `json:"url,omitempty"`
	RelayURL  string `json:"relayURL,omitempty"`
	// State：中继隧道的状态（「已连上」「断开了，正在重连：…」）；Err：起不来的原因。
	State string `json:"state,omitempty"`
	Err   string `json:"err,omitempty"`
	// CertExpires：证书到期时间（还没读到证书时为空）。
	CertExpires *time.Time `json:"certExpires,omitempty"`
	TTLDays     int        `json:"ttlDays"`
	ReauthHours int        `json:"reauthHours"`
	PairMinutes int        `json:"pairMinutes"` // 配对码进来的设备活几分钟（0 = 和别的一样）
	Locked      bool       `json:"locked"`      // 全局熔断中：不接受新配对，`gerdr remote unlock` 解
	// 局域网直连口：在听的端口（0 = 没开 / 没起来）、起不来的原因、现在报给 App 的候选地址
	LanPort    int      `json:"lanPort,omitempty"`
	LanErr     string   `json:"lanErr,omitempty"`
	LanOrigins []string `json:"lanOrigins,omitempty"`
}

func (s *Service) Status() Status {
	s.mu.Lock()
	cfg := s.cfg
	st := Status{Enabled: cfg.Enabled(), Host: cfg.Host, URL: urlOf(cfg), RelayURL: cfg.RelayURL, EnvLocked: envLocked(),
		TTLDays: int(cfg.DeviceTTL / (24 * time.Hour)), ReauthHours: int(cfg.ReauthAfter / time.Hour), PairMinutes: int(cfg.PairTTL / time.Minute), Locked: s.Gate.Locked()}
	st.Err = s.err
	st.LanErr = s.lanErr
	if s.lanHS != nil {
		st.LanPort = cfg.LanPort
		st.LanOrigins = lan.Origins(cfg.LanPort)
	}
	if s.tunnel != nil {
		st.State = s.tunnel.State()
	} else if st.Enabled && st.Err == "" {
		st.State = "正在准备证书"
	}
	certs := s.certs
	s.mu.Unlock()
	if certs != nil {
		if leaf := certs.leaf(); leaf != nil {
			t := leaf.NotAfter
			st.CertExpires = &t
		}
	}
	return st
}

/* ------------------------------------------------------------------ 证书热重载 */

// certFile 按 mtime 热重载证书：ACME 续期写的是新文件（rename 过去），不重启就要换上。
// 十秒看一次就够 —— 续期提前 30 天做，晚十秒生效没有任何区别。
type certFile struct {
	certPath, keyPath string

	mu      sync.Mutex
	cert    *tls.Certificate
	mod     time.Time
	checked time.Time
}

func loadCert(certPath, keyPath string) (*certFile, error) {
	c := &certFile{certPath: certPath, keyPath: keyPath}
	if err := c.reload(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *certFile) reload() error {
	st, err := os.Stat(c.certPath)
	if err != nil {
		return err
	}
	pair, err := tls.LoadX509KeyPair(c.certPath, c.keyPath)
	if err != nil {
		return err
	}
	if len(pair.Certificate) > 0 {
		pair.Leaf, _ = x509.ParseCertificate(pair.Certificate[0])
	}
	c.cert, c.mod = &pair, st.ModTime()
	return nil
}

func (c *certFile) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.checked) > 10*time.Second {
		c.checked = time.Now()
		if st, err := os.Stat(c.certPath); err == nil && st.ModTime().After(c.mod) {
			// 写到一半被读到会拿到「新证书 + 旧私钥」：失败就继续用旧的，下一轮再试
			_ = c.reload()
		}
	}
	return c.cert, nil
}

func (c *certFile) leaf() *x509.Certificate {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cert == nil {
		return nil
	}
	return c.cert.Leaf
}

// Join 拿邀请码把这台机器注册到中继上（`gerdr remote join`）。
func Join(ctx context.Context, dataDir, relayURL, host, invite string) error {
	// 底下的错误常常只有一个「EOF」「connection refused」，不带上是哪一步、连的谁，人没法判断是地址错了还是码错了
	if err := relay.Join(ctx, relay.HostConfig{URL: relayURL, Host: host, Dir: config.RemoteDir(dataDir) + "/relay"}, invite); err != nil {
		return fmt.Errorf("注册到中继 %s 失败（地址对不对、邀请码是不是给 %s 出的、过没过期）：%w", relayURL, host, err)
	}
	return nil
}

// envLocked：中继地址 / 主机名是环境变量给的。LoadRemote 里环境变量盖过 remote.json，
// 这时候设置里改文件不会生效，干脆不让改，免得「点了连接 / 移除，什么都没变」。
func envLocked() bool {
	return os.Getenv("GERDR_RELAY_URL") != "" || os.Getenv("GERDR_RELAY_HOST") != ""
}

var errEnvLocked = errors.New("中继地址 / 主机名是环境变量 GERDR_RELAY_URL / GERDR_RELAY_HOST 给的，这里改不动：去掉它们、重启 Gerdr 再来")

// Connect 是设置里的「连接」，等于 `gerdr remote join` 不带证书参数：拿邀请码注册到中继、写进 remote.json，
// 然后当场把远程那条路拉起来，不用重启。证书之后在设置的「证书」那一节填（以前配过就沿用）。
//
// 先核参数再用邀请码：邀请码一次性、30 分钟过期，用掉了才发现填错就得回 VPS 再出一个。
func (s *Service) Connect(ctx context.Context, relayURL, host, invite string) error {
	if envLocked() {
		return errEnvLocked
	}
	if cur := s.Config(); cur.Enabled() {
		return fmt.Errorf("已经连着 %s 了：先移除再连", cur.Host)
	}
	host, err := relay.CleanHost(host)
	if err != nil {
		return err
	}
	relayURL = strings.TrimRight(strings.TrimSpace(relayURL), "/")
	if u, err := url.Parse(relayURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("中继地址要写成 https://relay.example.com 这样")
	}
	if invite = strings.TrimSpace(invite); invite == "" {
		return errors.New("邀请码没填：在中继后台（或 VPS 上 dialhome invite " + host + "）出一个")
	}
	f, err := config.ReadRemoteFile(s.dataDir())
	if err != nil {
		return err
	}
	if err := Join(ctx, s.dataDir(), relayURL, host, invite); err != nil {
		return err
	}
	// passkey 绑在域名上：换了名字，旧的那些一把都用不了，留着只会让「已有 N 把」变成假话
	if f.Host != host {
		s.Passkeys.DeleteAll()
	}
	f.RelayURL, f.Host = relayURL, host
	if err := config.WriteRemoteFile(s.dataDir(), f); err != nil {
		return fmt.Errorf("注册好了，但配置没写进去：%w", err)
	}
	return s.Reload()
}

// Disconnect 是设置里的「移除」：收掉隧道，主机名从 remote.json 里去掉，设备和 passkey 全部清掉。
// 中继地址和证书来源留着，下次连接不用重填（ACME 的证书按域名分文件，换名字会另签一张）。
//
// 为什么连凭据一起清：远程这条路没了还留着它们，下次连上时（可能隔了几个月、换了名字）它们突然又好使，
// 那时已经没人记得当初给过谁。中继那边的注册记录这边删不掉（协议里没有注销），不过没有本机的
// 身份私钥签名，别人拿那个名字也连不上来；不想要了在 VPS 上删掉即可。
func (s *Service) Disconnect() (devs, keys int, err error) {
	if envLocked() {
		return 0, 0, errEnvLocked
	}
	f, err := config.ReadRemoteFile(s.dataDir())
	if err != nil {
		return 0, 0, err
	}
	f.Host = ""
	if err := config.WriteRemoteFile(s.dataDir(), f); err != nil {
		return 0, 0, err
	}
	devs, keys = auth.RevokeEverything(s.Auth, s.Passkeys)
	return devs, keys, s.Reload()
}

// Reload 按 remote.json（和环境变量）把远程那条路整条重来：连接 / 移除之后，以及 `gerdr remote join`
// 写完文件通知正在跑的 gerdr。passkey 的 RPID 跟着主机名换。
func (s *Service) Reload() error {
	cfg, err := config.LoadRemote(s.dataDir())
	if err != nil {
		return err
	}
	s.stop()
	rpid, origins := relyingParty(cfg)
	perr := s.Passkeys.SetRP(rpid, origins)
	s.mu.Lock()
	s.cfg, s.err = cfg, ""
	s.mu.Unlock()
	if perr != nil {
		s.setErr(perr.Error())
	}
	s.run()
	return nil
}

// AccountURI：这台机器的 ACME 账户地址，给 CAA 的 accounturi 用。
func AccountURI(dataDir string) string { return acme.AccountURI(config.RemoteDir(dataDir)) }

/* ------------------------------------------------------------------ 设置页改证书 */

// CertConfig 是设置页「证书」那一节：读的时候凭据只给名字（Set），不给值。
type CertConfig struct {
	DNS       string          `json:"dns"`
	Email     string          `json:"email"`
	Staging   bool            `json:"staging"`
	TLSCert   string          `json:"tlsCert"`
	TLSKey    string          `json:"tlsKey"`
	Set       []string        `json:"set"` // 已经配了的凭据变量名（不带 GERDR_）
	Providers []acme.Provider `json:"providers"`
	// AccountURI：CAA 那条记录要的 ACME 账户；签下第一张证书之前是空的。
	AccountURI string `json:"accountURI"`
	// CAAAuto: the provider can write the CAA record itself (AddCAA), so the page offers a button.
	CAAAuto bool `json:"caaAuto"`
}

func (s *Service) dataDir() string { return filepath.Dir(s.Dir) }

func (s *Service) CertConfig() (CertConfig, error) {
	f, err := config.ReadRemoteFile(s.dataDir())
	if err != nil {
		return CertConfig{}, err
	}
	s.mu.Lock()
	c := CertConfig{DNS: s.cfg.ACMEDNS, Email: s.cfg.ACMEEmail, Staging: s.cfg.ACMEStaging, TLSCert: s.cfg.TLSCert, TLSKey: s.cfg.TLSKey}
	s.mu.Unlock()
	c.Set = []string{}
	if p := acme.FindProvider(c.DNS); p != nil {
		for _, fd := range p.Fields {
			if f.Env[fd.Env] != "" || os.Getenv("GERDR_"+fd.Env) != "" {
				c.Set = append(c.Set, fd.Env)
			}
		}
	}
	c.Providers = acme.AllProviders()
	c.AccountURI = acme.AccountURI(s.Dir)
	c.CAAAuto = acme.CAASupported(c.DNS)
	return c, nil
}

// CAA checks for the relay host's CAA record through the configured DNS provider; with apply it also adds it.
// missing=false means an identical record is already there.
func (s *Service) CAA(ctx context.Context, apply bool) (missing bool, err error) {
	cfg := s.Config()
	if !cfg.Enabled() {
		return false, errors.New("还没连上中继：先在上面「连接」")
	}
	return acme.SetCAA(ctx, cfg.ACMEDNS, cfg.Host, acme.AccountURI(s.Dir), apply)
}

// CertInput 是设置页提交的。Env 里空着的项 = 沿用已经存着的那份（表单不回显密钥）。
type CertInput struct {
	DNS     string            `json:"dns"`
	Email   string            `json:"email"`
	Staging bool              `json:"staging"`
	Env     map[string]string `json:"env"`
	TLSCert string            `json:"tlsCert"`
	TLSKey  string            `json:"tlsKey"`
}

// SaveCert 写进 remote.json、补进进程环境，然后把远程那条路重来一遍。
//
// 换了服务商时，别家留在文件里的凭据一起删掉：用不上的密钥不该继续躺在盘上。
func (s *Service) SaveCert(in CertInput) error {
	if !s.Config().Enabled() {
		return errors.New("还没连上中继：先在上面「连接」")
	}
	f, err := config.ReadRemoteFile(s.dataDir())
	if err != nil {
		return err
	}
	if f.Env == nil {
		f.Env = map[string]string{}
	}
	in.DNS = strings.TrimSpace(in.DNS)
	switch {
	case in.DNS != "":
		p := acme.FindProvider(in.DNS)
		if p == nil {
			return fmt.Errorf("不认识的 DNS 服务商 %q", in.DNS)
		}
		keep := map[string]bool{}
		for _, fd := range p.Fields {
			keep[fd.Env] = true
			if v := strings.TrimSpace(in.Env[fd.Env]); v != "" {
				f.Env[fd.Env] = v
			}
			if f.Env[fd.Env] == "" && os.Getenv("GERDR_"+fd.Env) == "" {
				return fmt.Errorf("%s 的「%s」没填", p.Label, fd.Label)
			}
		}
		for k := range f.Env {
			if !keep[k] {
				delete(f.Env, k)
			}
		}
		f.ACMEDNS, f.TLSCert, f.TLSKey = in.DNS, "", ""
	case in.TLSCert != "" && in.TLSKey != "":
		if _, err := loadCert(in.TLSCert, in.TLSKey); err != nil {
			return fmt.Errorf("证书文件读不了：%w", err)
		}
		f.ACMEDNS, f.TLSCert, f.TLSKey, f.Env = "", in.TLSCert, in.TLSKey, nil
	default:
		return errors.New("选一家 DNS 服务商，或者给证书和私钥两个文件")
	}
	f.ACMEEmail, f.ACMEStaging = strings.TrimSpace(in.Email), in.Staging
	if err := config.WriteRemoteFile(s.dataDir(), f); err != nil {
		return err
	}
	// 进程环境里那份也要换成新的（LoadRemote 只补「环境里没有的」，这里是显式改，要盖掉）
	for k, v := range f.Env {
		_ = os.Setenv("GERDR_"+k, v)
	}
	s.SetCert(f.ACMEDNS, f.ACMEEmail, f.ACMEStaging, f.TLSCert, f.TLSKey)
	return nil
}
