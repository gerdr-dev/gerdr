package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/pires/go-proxyproto"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/lan"
)

// 局域网直连口：人在家时，安卓 App 的连接不绕云端中继，直接走这台机器的局域网口。设计照 herdr-web 的
// docs/dev/RELAY.md §4，gerdr 这边的说明在 docs/dev/ANDROID.md「局域网直连」。要点：
//
//   - **不换 origin**。App 里一个本机 CONNECT 代理把 `<远程主机名>:443` 拨到这个口上（探通了才拨），TLS 从 WebView
//     一路到这儿，WebView 按域名验证书 —— 所以这个口给的是**和中继那条路同一张真证书**，Host 也还是那个域名，
//     remoteGuard 的 Host 白名单、设备 cookie、passkey 全部原样适用。
//   - 和中继那条路是**同一个 handler、同一个「远程」章**（h 已经是 server.RemoteHandler 包过的）：不因为在局域网里
//     就放宽任何东西（本机口那种令牌认证不在这条路上）。
//   - **对端必须在本地网络里**（lan.PeerIsLocal），否则一个字节都不服务。绑通配地址只是拓扑上的假设：端口转发、
//     UPnP、全局 IPv6 都能让公网直接连上来。
//   - 浏览器不走这条（没法不换 origin 地换路；换 origin 的坑见 herdr-web RELAY.md §4.4）。
//
// 起不来（端口被占）只记在 Status.LanErr 里，App 探不通会安静地留在中继上。
//
// The same port also serves a phone on this machine's tailnet (Tailscale) from anywhere, unless LanTailnet is off:
// same certificate, same rules, the tailnet only replaces the relay as the way in.
//
// In direct access (config.Remote.Direct) this port is the way in, with no relay beside it, and serves any peer:
// how phones get here (Tailscale, frp, a port forward) is the person's network. The gate goes; the device cookie,
// passkeys and the Host whitelist are what the relay path relies on too, and they stay.
//
// A connection from this machine itself (a tunnel client like frpc) may open with a PROXY protocol header, which
// then gives the client's real address: the per-IP rate limits on pairing codes and passkeys key on it, and
// without it everything through frp is one client, 127.0.0.1. Optional, and only believed from loopback: anyone
// else could write any address into it.

func (s *Service) startLan(ctx context.Context, cfg config.Remote, cf *certFile, h http.Handler) {
	if cfg.LanPort <= 0 {
		return
	}
	addr := fmt.Sprintf(":%d", cfg.LanPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.mu.Lock()
		s.lanErr = fmt.Sprintf(i18n.S("局域网直连口 %s 起不来：%v", "the LAN port %s won't start: %v"), addr, err)
		s.mu.Unlock()
		s.log.Warn("remote: lan port", "err", err)
		return
	}
	handler := h
	if !cfg.DirectOn() {
		handler = lanOnly(h, cfg.LanTailnet, s.alert)
	}
	hs := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		ln.Close()
		return
	}
	s.lanHS, s.lanErr = hs, ""
	s.mu.Unlock()
	pl := &proxyproto.Listener{Listener: ln, ConnPolicy: proxyFromLoopback, ReadHeaderTimeout: 10 * time.Second}
	tl := tls.NewListener(pl, &tls.Config{GetCertificate: cf.get, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}})
	go func() {
		if err := hs.Serve(tl); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
			s.mu.Lock()
			s.lanErr = i18n.S("局域网直连口挂了：", "the LAN port went down: ") + err.Error()
			s.mu.Unlock()
		}
	}()
}

// LanOrigins：报给 App 的候选（`https://192.168.1.5:7878`）。直连口没在听时为空。
// 每次现算：局域网 IP 会变（DHCP、换 Wi-Fi），配死一个地址的表现是「某天开始不直连了，而且没有任何报错」。
func (s *Service) LanOrigins() []string {
	s.mu.Lock()
	on, port, tailnet := s.lanHS != nil, s.cfg.LanPort, s.cfg.LanTailnet
	s.mu.Unlock()
	if !on {
		return nil
	}
	return lan.Origins(port, tailnet)
}

// proxyFromLoopback: read a PROXY header if a local process (frpc) sends one, never from anyone else.
func proxyFromLoopback(o proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
	if a, ok := o.Upstream.(*net.TCPAddr); ok && a.IP.IsLoopback() {
		return proxyproto.USE, nil
	}
	return proxyproto.SKIP, nil
}

// lanOnly：对端不在本地网络里就 403。第一次撞上时吼一声 —— 那说明这个口被暴露到公网了。
// With tailnet on, a tailnet peer that reached our tailnet address counts as local too (lan.ViaTailnet).
func lanOnly(next http.Handler, tailnet bool, alert func(string)) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		local := ""
		if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			local = a.String()
		}
		if !lan.PeerIsLocal(r.RemoteAddr) && !(tailnet && lan.ViaTailnet(r.RemoteAddr, local)) {
			once.Do(func() {
				alert(fmt.Sprintf(i18n.S("局域网直连口收到了来自 %s 的连接 —— 不是本地地址，已拒绝。这个口大概被暴露到公网了（端口转发 / UPnP / 全局 IPv6）",
					"The LAN port got a connection from %s, which isn't a local address, and refused it. The port is probably exposed to the internet (port forwarding / UPnP / global IPv6)."), r.RemoteAddr))
			})
			http.Error(w, i18n.S("这个口只服务本地网络。", "This port only serves the local network."), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
