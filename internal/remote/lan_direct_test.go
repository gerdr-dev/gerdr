package remote

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pires/go-proxyproto"

	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/config"
)

func testCert(t *testing.T) *certFile {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gerdr.test"}, DNSNames: []string{"gerdr.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	dir := t.TempDir()
	cp, kp := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	os.WriteFile(cp, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(kp, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	cf, err := loadCert(cp, kp)
	if err != nil {
		t.Fatal(err)
	}
	return cf
}
func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// get sends one request to the LAN port from loopback, with a PROXY header claiming 203.0.113.7 if proxy is set.
func lanGet(t *testing.T, port int, proxy bool) (int, string) {
	t.Helper()
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if proxy {
		h := proxyproto.HeaderProxyFromAddrs(2, &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 5555}, c.RemoteAddr())
		if _, err := h.WriteTo(c); err != nil {
			t.Fatal(err)
		}
	}
	tl := tls.Client(c, &tls.Config{ServerName: "gerdr.test", InsecureSkipVerify: true})
	io.WriteString(tl, "GET / HTTP/1.1\r\nHost: gerdr.test\r\nConnection: close\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(tl), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func startTestLan(t *testing.T, cfg config.Remote) int {
	s := &Service{log: slog.New(slog.NewTextHandler(io.Discard, nil)), Gate: auth.NewGate(), cfg: cfg}
	cfg.LanPort = freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); s.stop() })
	s.startLan(ctx, cfg, testCert(t), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.RemoteAddr) }))
	return cfg.LanPort
}

// Direct access: any peer, and a PROXY header from this machine (frpc) gives the client's real address.
func TestLanDirect(t *testing.T) {
	port := startTestLan(t, config.Remote{Host: "gerdr.test", Direct: true, LanPort: 1})
	if code, body := lanGet(t, port, false); code != 200 || !strings.HasPrefix(body, "127.0.0.1:") {
		t.Errorf("no header: %d %q", code, body)
	}
	if code, body := lanGet(t, port, true); code != 200 || !strings.HasPrefix(body, "203.0.113.7:") {
		t.Errorf("PROXY header from loopback: %d %q, want the address from the header", code, body)
	}
}

// Beside the relay the LAN port keeps its gate: a PROXY header naming a public address doesn't get through it.
func TestLanBesideRelayKeepsGate(t *testing.T) {
	port := startTestLan(t, config.Remote{Host: "gerdr.test", RelayURL: "https://relay", LanPort: 1})
	if code, _ := lanGet(t, port, false); code != 200 {
		t.Errorf("loopback: %d", code)
	}
	if code, _ := lanGet(t, port, true); code != 403 {
		t.Errorf("public address via PROXY header beside the relay: %d, want 403", code)
	}
}
