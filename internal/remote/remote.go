// Package remote 把远程访问的几块拼起来：设备表 / passkey / 限速（internal/auth）、证书（internal/acme
// 或自己给的文件）、云端中继的隧道（internal/relay），以及隧道上那个 https 服务。
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
	"os"
	"sync"
	"time"

	"github.com/zbysir/gerdr/internal/acme"
	"github.com/zbysir/gerdr/internal/auth"
	"github.com/zbysir/gerdr/internal/config"
	"github.com/zbysir/gerdr/internal/relay"
)

type Service struct {
	Cfg      config.Remote
	Dir      string // <数据目录>/remote
	Auth     *auth.Store
	Passkeys *auth.Passkeys
	Gate     *auth.Gate

	log    *slog.Logger
	acme   *acme.Manager
	certs  *certFile
	tunnel *relay.Tunnel
	hs     *http.Server

	mu  sync.Mutex
	err string // 起不来的原因（证书签不下来、没 join 过……）
}

// Open 准备设备表、passkey 和限速。没开远程也照样打开：设置里要能看设备、踢设备，
// 命令行的 `gerdr remote devices / revoke` 也走这里。
func Open(dataDir string, cfg config.Remote, log *slog.Logger) (*Service, error) {
	dir := config.RemoteDir(dataDir)
	store, err := auth.New(auth.Config{Dir: dir, TTL: cfg.DeviceTTL})
	if err != nil {
		return nil, fmt.Errorf("读设备列表失败: %w", err)
	}
	// RPID 就是中继上的主机名：passkey 只在那个 origin 上能用。没开远程时给空，passkey 那几个口就都是 409。
	pc := auth.PasskeyConfig{Dir: dir, Display: "gerdr"}
	if cfg.Enabled() {
		pc.RPID, pc.Origins = cfg.Host, []string{"https://" + cfg.Host}
	}
	pk, err := auth.NewPasskeys(pc)
	if err != nil {
		return nil, err
	}
	g := auth.NewGate()
	s := &Service{Cfg: cfg, Dir: dir, Auth: store, Passkeys: pk, Gate: g, log: log}
	g.Alert = s.alert
	return s, nil
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
func (s *Service) URL() string {
	if !s.Cfg.Enabled() {
		return ""
	}
	return "https://" + s.Cfg.Host
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
	if !s.Cfg.Enabled() {
		return
	}
	if p := s.Cfg.TLSProblem(); p != "" {
		s.setErr(p)
		return
	}
	go func() {
		certFile, keyFile := s.Cfg.TLSCert, s.Cfg.TLSKey
		if s.Cfg.ACMEDNS != "" {
			s.acme = acme.NewManager(acme.Config{
				Dir: s.Dir, Domains: []string{s.Cfg.Host}, Email: s.Cfg.ACMEEmail,
				DNS: s.Cfg.ACMEDNS, Staging: s.Cfg.ACMEStaging,
			})
			// 第一次签要等 DNS 传播，一两分钟很正常：在后台等，别拖住整个 gerdr 的启动
			if a := s.acme.Ensure(false); a.Err != "" {
				s.setErr("签证书失败：" + a.Err)
				return
			} else if a.Renewed {
				s.log.Info("remote: issued certificate", "host", s.Cfg.Host)
			}
			certFile, keyFile = s.acme.Config().Files()
			go s.renewLoop()
		}
		cf, err := loadCert(certFile, keyFile)
		if err != nil {
			s.setErr("读证书失败：" + err.Error())
			return
		}
		tn, err := relay.Start(relay.HostConfig{
			URL: s.Cfg.RelayURL, Host: s.Cfg.Host, Dir: s.relayDir(),
			Log: func(f string, a ...any) { s.log.Info("remote: " + fmt.Sprintf(f, a...)) },
		})
		if err != nil {
			s.setErr(err.Error())
			return
		}
		s.mu.Lock()
		s.tunnel, s.certs = tn, cf
		s.mu.Unlock()
		s.setErr("")
		// TLS 在这里终止：中继手里只有密文（docs/dev/REMOTE.md「中继能拿到什么」）。
		// 不发 HSTS 之类的头不用操心 —— 安全头由 server.guard 统一加。
		s.hs = &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
		ln := tls.NewListener(tn, &tls.Config{GetCertificate: cf.get, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}})
		if err := s.hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			s.setErr("远程那个口挂了：" + err.Error())
		}
	}()
}

func (s *Service) relayDir() string { return s.Dir + "/relay" }

// renewLoop 半天看一次要不要续。续完不用重启：certFile 按 mtime 热重载。
func (s *Service) renewLoop() {
	for range time.Tick(12 * time.Hour) {
		if a := s.acme.Ensure(false); a.Err != "" {
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
	s.mu.Lock()
	tn := s.tunnel
	s.mu.Unlock()
	if tn != nil {
		tn.Close()
	}
	if s.hs != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.hs.Shutdown(ctx)
	}
	s.Auth.Flush()
}

// Status 是给设置页 / `gerdr remote status` 看的一句话状态。
type Status struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host,omitempty"`
	URL      string `json:"url,omitempty"`
	RelayURL string `json:"relayURL,omitempty"`
	// State：中继隧道的状态（「已连上」「断开了，正在重连：…」）；Err：起不来的原因。
	State string `json:"state,omitempty"`
	Err   string `json:"err,omitempty"`
	// CertExpires：证书到期时间（还没读到证书时为空）。
	CertExpires *time.Time `json:"certExpires,omitempty"`
	TTLDays     int        `json:"ttlDays"`
	ReauthHours int        `json:"reauthHours"`
	Locked      bool       `json:"locked"` // 全局熔断中：不接受新配对，`gerdr remote unlock` 解
}

func (s *Service) Status() Status {
	st := Status{Enabled: s.Cfg.Enabled(), Host: s.Cfg.Host, URL: s.URL(), RelayURL: s.Cfg.RelayURL,
		TTLDays: int(s.Cfg.DeviceTTL / (24 * time.Hour)), ReauthHours: int(s.Cfg.ReauthAfter / time.Hour), Locked: s.Gate.Locked()}
	s.mu.Lock()
	st.Err = s.err
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
	return relay.Join(ctx, relay.HostConfig{URL: relayURL, Host: host, Dir: config.RemoteDir(dataDir) + "/relay"}, invite)
}

// AccountURI：这台机器的 ACME 账户地址，给 CAA 的 accounturi 用。
func AccountURI(dataDir string) string { return acme.AccountURI(config.RemoteDir(dataDir)) }
