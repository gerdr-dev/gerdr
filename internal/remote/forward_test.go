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

// The forward port serves what frpc hands it with the client's real address (from the PROXY header), and drops a
// connection without the header, saying what frpc is missing.
func TestForwardPort(t *testing.T) {
	s := &Service{log: slog.New(slog.NewTextHandler(io.Discard, nil)), Gate: auth.NewGate()}
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startForward(ctx, config.Remote{ForwardPort: port}, testCert(t), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.RemoteAddr)
	}))
	defer s.stop()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	tc := &tls.Config{ServerName: "gerdr.test", InsecureSkipVerify: true}

	get := func(header bool) (string, error) {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return "", err
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		if header {
			h := proxyproto.HeaderProxyFromAddrs(2, &net.TCPAddr{IP: net.ParseIP("203.0.113.7"), Port: 5555}, c.RemoteAddr())
			if _, err := h.WriteTo(c); err != nil {
				return "", err
			}
		}
		tl := tls.Client(c, tc)
		if _, err := io.WriteString(tl, "GET / HTTP/1.1\r\nHost: gerdr.test\r\nConnection: close\r\n\r\n"); err != nil {
			return "", err
		}
		resp, err := http.ReadResponse(bufio.NewReader(tl), nil)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), nil
	}

	if _, err := get(false); err == nil {
		t.Fatal("a connection without a PROXY header was served")
	}
	deadline := time.Now().Add(2 * time.Second)
	for s.Status().ForwardErr == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if e := s.Status().ForwardErr; !strings.Contains(e, "proxyProtocolVersion") {
		t.Errorf("ForwardErr = %q, want the frpc hint", e)
	}

	got, err := get(true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "203.0.113.7:") {
		t.Errorf("RemoteAddr = %q, want the address from the PROXY header", got)
	}
	if st := s.Status(); st.ForwardErr != "" || st.ForwardLast == nil {
		t.Errorf("after a good request: ForwardErr %q, ForwardLast %v", st.ForwardErr, st.ForwardLast)
	}
}
