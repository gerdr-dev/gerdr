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

	"github.com/gerdr-dev/gerdr/internal/config"
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

func (s *Service) startLan(ctx context.Context, cfg config.Remote, cf *certFile, h http.Handler) {
	if cfg.LanPort <= 0 {
		return
	}
	addr := fmt.Sprintf(":%d", cfg.LanPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.mu.Lock()
		s.lanErr = "局域网直连口 " + addr + " 起不来：" + err.Error()
		s.mu.Unlock()
		s.log.Warn("remote: lan port", "err", err)
		return
	}
	hs := &http.Server{Handler: lanOnly(h, s.alert), ReadHeaderTimeout: 10 * time.Second}
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		ln.Close()
		return
	}
	s.lanHS, s.lanErr = hs, ""
	s.mu.Unlock()
	tl := tls.NewListener(ln, &tls.Config{GetCertificate: cf.get, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}})
	go func() {
		if err := hs.Serve(tl); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
			s.mu.Lock()
			s.lanErr = "局域网直连口挂了：" + err.Error()
			s.mu.Unlock()
		}
	}()
}

// LanOrigins：报给 App 的候选（`https://192.168.1.5:7878`）。直连口没在听时为空。
// 每次现算：局域网 IP 会变（DHCP、换 Wi-Fi），配死一个地址的表现是「某天开始不直连了，而且没有任何报错」。
func (s *Service) LanOrigins() []string {
	s.mu.Lock()
	on, port := s.lanHS != nil, s.Cfg.LanPort
	s.mu.Unlock()
	if !on {
		return nil
	}
	return lan.Origins(port)
}

// lanOnly：对端不在本地网络里就 403。第一次撞上时吼一声 —— 那说明这个口被暴露到公网了。
func lanOnly(next http.Handler, alert func(string)) http.Handler {
	var once sync.Once
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !lan.PeerIsLocal(r.RemoteAddr) {
			once.Do(func() {
				alert("局域网直连口收到了来自 " + r.RemoteAddr + " 的连接 —— 不是本地地址，已拒绝。这个口大概被暴露到公网了（端口转发 / UPnP / 全局 IPv6）")
			})
			http.Error(w, "这个口只服务本地网络。", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
