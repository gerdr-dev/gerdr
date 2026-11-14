package remote

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/pires/go-proxyproto"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// The forward port: remote access through a tunnel of one's own (frp and the like) instead of the dialhome relay.
// docs/dev/REMOTE.md "Your own tunnel (frp)". It is the relay path with another way in:
//
//   - **Same certificate, same handler, same "remote" stamp** as the relay (h is server.RemoteHandler): TLS ends
//     here, so the tunnel only carries ciphertext, and the Host whitelist, device cookie and passkeys all apply.
//     The tunnel must pass TLS through (frp `type = "https"` or `"tcp"`, never the https2http plugin).
//   - **127.0.0.1 only**: the tunnel's client (frpc) runs on this machine. Not the LAN port: frpc dials from
//     loopback, which that port's "local network only" gate would wave through and never alert on.
//   - **A PROXY protocol header is required** (frpc `transport.proxyProtocolVersion = "v2"`). Without it every
//     request comes from 127.0.0.1: the per-IP rate limits on pairing codes and passkeys would count the whole
//     internet as one client, and the device list would show no addresses. A connection without the header is
//     dropped, and Status.ForwardErr says what to add to frpc.

func (s *Service) startForward(ctx context.Context, cfg config.Remote, cf *certFile, h http.Handler) {
	if cfg.ForwardPort <= 0 {
		return
	}
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.ForwardPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.setFwdErr(fmt.Sprintf(i18n.S("转发口 %s 起不来：%v", "the forward port %s won't start: %v"), addr, err))
		s.log.Warn("remote: forward port", "err", err)
		return
	}
	pl := &proxyproto.Listener{
		Listener:          ln,
		ConnPolicy:        func(proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) { return proxyproto.REQUIRE, nil },
		ReadHeaderTimeout: 10 * time.Second,
	}
	hs := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.forwardSeen()
			h.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
		// A missing PROXY header surfaces as a TLS handshake error, which http.Server only logs: catch it there
		ErrorLog: log.New(fwdLog{s}, "", 0),
	}
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		ln.Close()
		return
	}
	s.fwdHS, s.fwdErr = hs, ""
	s.mu.Unlock()
	tl := tls.NewListener(pl, &tls.Config{GetCertificate: cf.get, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}})
	go func() {
		if err := hs.Serve(tl); err != nil && !errors.Is(err, http.ErrServerClosed) && ctx.Err() == nil {
			s.setFwdErr(i18n.S("转发口挂了：", "the forward port went down: ") + err.Error())
		}
	}()
}

func (s *Service) setFwdErr(e string) {
	s.mu.Lock()
	s.fwdErr = e
	s.mu.Unlock()
}

// forwardSeen: a request made it through, so the tunnel works: drop a "no PROXY header" left from before frpc was
// set up right.
func (s *Service) forwardSeen() {
	s.mu.Lock()
	s.fwdErr, s.fwdLast = "", time.Now()
	s.mu.Unlock()
}

var errNoProxyHeader = i18n.S(
	"转发口收到了不带 PROXY protocol 头的连接，已断开：frpc 那条代理里加上 transport.proxyProtocolVersion = \"v2\"",
	"The forward port got a connection without a PROXY protocol header and dropped it: add transport.proxyProtocolVersion = \"v2\" to that proxy in frpc")

type fwdLog struct{ s *Service }

func (l fwdLog) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(proxyproto.ErrNoProxyProtocol.Error())) {
		l.s.setFwdErr(errNoProxyHeader)
	} else {
		l.s.log.Debug("remote: forward port: " + string(bytes.TrimSpace(p)))
	}
	return len(p), nil
}

// FrpcSnippet: the frpc.toml proxy for the forward port, as `gerdr remote forward` prints it (the settings page
// draws the same text itself). type "https" routes by SNI and passes TLS through; the PROXY header is required.
func FrpcSnippet(host string, port int) string {
	return fmt.Sprintf(`[[proxies]]
name = "gerdr"
type = "https"
customDomains = [%q]
localIP = "127.0.0.1"
localPort = %d
transport.proxyProtocolVersion = "v2"
`, host, port)
}
