package relay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

// fakeRelay 是 herdr-relay 那一侧最小的样子：握手三句话 + yamux 客户端 + 每条子流先写头。
// 协议要和 herdr-web 的 internal/relay/server.go 一字不差 —— 这里钉的就是那几处格式。
type fakeRelay struct {
	srv  *httptest.Server
	sess chan *yamux.Session
}

func newFakeRelay(t *testing.T) *fakeRelay {
	f := &fakeRelay{sess: make(chan *yamux.Session, 1)}
	up := websocket.Upgrader{}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != TunnelPath {
			http.NotFound(w, r)
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		nonce := make([]byte, 32)
		_, _ = rand.Read(nonce)
		_ = ws.WriteJSON(challenge{Nonce: nonce})
		var h hello
		if ws.ReadJSON(&h) != nil {
			return
		}
		domain := strings.Split(strings.TrimPrefix(f.srv.URL, "https://"), ":")[0]
		ok := len(h.Key) == ed25519.PublicKeySize && ed25519.Verify(h.Key, signed(domain, h.Host, nonce), h.Sig)
		if !ok {
			_ = ws.WriteJSON(verdict{Err: "签名不对"})
			return
		}
		_ = ws.WriteJSON(verdict{OK: true})
		s, err := yamux.Client(newWSStream(ws), nil)
		if err != nil {
			return
		}
		f.sess <- s
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func writeHeader(w io.Writer, h streamHeader) error {
	b, _ := json.Marshal(h)
	var n [2]byte
	binary.BigEndian.PutUint16(n[:], uint16(len(b)))
	_, err := w.Write(append(n[:], b...))
	return err
}

func TestTunnel(t *testing.T) {
	f := newFakeRelay(t)
	dir := t.TempDir()
	cfg := HostConfig{
		URL: f.srv.URL, Host: "gerdr.example.com", Dir: dir,
		TLS: f.srv.Client().Transport.(*http.Transport).TLSClientConfig,
		Log: func(string, ...any) {},
	}
	if _, err := Start(cfg); err == nil {
		t.Fatal("没 join 过（没有身份私钥）就该起不来")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Join(ctx, cfg, "邀请码"); err != nil {
		t.Fatalf("Join: %v", err)
	}
	<-f.sess // join 那一次的连接

	tn, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Close()
	var sess *yamux.Session
	select {
	case sess = <-f.sess:
	case <-time.After(5 * time.Second):
		t.Fatal("隧道没连上")
	}
	st, err := sess.Open()
	if err != nil {
		t.Fatal(err)
	}
	_ = writeHeader(st, streamHeader{Src: "198.51.100.7:5555"})
	_, _ = st.Write([]byte("ping"))

	c, err := tn.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.RemoteAddr().(*net.TCPAddr).IP.String(); got != "198.51.100.7" {
		t.Errorf("RemoteAddr 该是中继报的手机地址（限速按它算），得到 %s", got)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("子流里的字节没原样过来：%q %v", buf, err)
	}
	if tn.State() != "已连上" {
		t.Errorf("状态：%s", tn.State())
	}
}

// 头坏了的子流：给一个绝不会被当成本机的地址，而不是 127.0.0.1。
func TestParseAddrFallback(t *testing.T) {
	if a := parseAddr("garbage"); a.(*net.TCPAddr).IP.IsLoopback() {
		t.Fatal("坏头不能落成 127.0.0.1")
	}
}
