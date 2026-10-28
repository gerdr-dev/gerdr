package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zbysir/gerdr/internal/config"
	"github.com/zbysir/gerdr/internal/remote"
)

const testHost = "gerdr.example.com"

func remoteServer(t *testing.T) (*Server, http.Handler, http.Handler) {
	t.Helper()
	rm, err := remote.Open(t.TempDir(), config.Remote{RelayURL: "https://relay.example.com", Host: testHost, DeviceTTL: 24 * time.Hour, ReauthAfter: 24 * time.Hour},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Token: "local-token-local-token-local-token", Remote: rm}
	h := s.Handler()
	return s, h, RemoteHandler(h)
}

type reqOpt func(*http.Request)

func hdr(k, v string) reqOpt         { return func(r *http.Request) { r.Header.Set(k, v) } }
func cookie(c *http.Cookie) reqOpt   { return func(r *http.Request) { r.AddCookie(c) } }
func host(h string) reqOpt           { return func(r *http.Request) { r.Host = h } }
func body(s string) func() io.Reader { return func() io.Reader { return strings.NewReader(s) } }

var csrf = hdr("X-Gerdr", "1")

func do(h http.Handler, method, path string, b io.Reader, opts ...reqOpt) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://"+testHost+path, b)
	r.Host = testHost
	r.RemoteAddr = "203.0.113.9:1234"
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// 远程那条路：没配过对只拿得到配对页；本机令牌（cookie / Bearer / /auth）一律不认。
func TestRemoteUnauthed(t *testing.T) {
	s, _, rh := remoteServer(t)

	if w := do(rh, "GET", "/", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "配对码") {
		t.Fatalf("没配对的页面请求该拿到配对页，得到 %d", w.Code)
	}
	if w := do(rh, "GET", "/_ui/m/src/App.tsx", nil); w.Code != 401 {
		t.Fatalf("界面源码不该给没配对的人，得到 %d", w.Code)
	}
	if w := do(rh, "GET", "/_esm/react@19", nil); w.Code != 401 {
		t.Fatalf("esm 代理不该给没配对的人，得到 %d", w.Code)
	}
	w := do(rh, "GET", "/api/hello", nil, hdr("Authorization", "Bearer "+s.Token))
	if w.Code != 401 || !strings.Contains(w.Body.String(), `"need":"pair"`) {
		t.Fatalf("本机令牌在远程不该好使，得到 %d %s", w.Code, w.Body)
	}
	if w := do(rh, "GET", "/api/hello", nil, cookie(&http.Cookie{Name: s.cookieName(), Value: s.Token})); w.Code != 401 {
		t.Fatalf("本机令牌的 cookie 在远程不该好使，得到 %d", w.Code)
	}
	if w := do(rh, "GET", "/auth?token="+s.Token, nil); w.Code != 403 {
		t.Fatalf("远程不该能拿令牌换 cookie，得到 %d", w.Code)
	}
	if w := do(rh, "GET", "/", nil, host("evil.example.com")); w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("Host 不对该 421（DNS rebinding），得到 %d", w.Code)
	}
}

// 配对：码只能在本机出；远程扫码进来换 cookie、302 洗掉地址栏里的码；之后能进，但出不了码。
func TestRemotePairFlow(t *testing.T) {
	s, lh, rh := remoteServer(t)

	if w := do(rh, "POST", "/api/remote/pair-code", body("{}")(), csrf); w.Code != 403 {
		t.Fatalf("远程不该能出配对码，得到 %d", w.Code)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:7870/api/remote/pair-code", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+s.Token)
	r.Header.Set("X-Gerdr", "1")
	w := httptest.NewRecorder()
	lh.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("本机出码：%d %s", w.Code, w.Body)
	}
	var pc struct{ Code, URL, QR string }
	_ = json.Unmarshal(w.Body.Bytes(), &pc)
	if pc.URL != "https://"+testHost+"/?pair="+pc.Code || !strings.HasPrefix(pc.QR, "data:image/png;base64,") {
		t.Fatalf("配对链接 / 二维码不对：%+v", pc)
	}

	w = do(rh, "GET", "/?pair="+pc.Code, nil)
	if w.Code != 302 || strings.Contains(w.Header().Get("Location"), "pair") {
		t.Fatalf("扫码进来该 302 到不带码的地址，得到 %d %q", w.Code, w.Header().Get("Location"))
	}
	var dev *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "gerdr_dev" {
			dev = c
		}
	}
	if dev == nil {
		t.Fatal("没发设备 cookie")
	}
	if w := do(rh, "GET", "/api/remote/devices", nil, cookie(dev)); w.Code != 200 {
		t.Fatalf("配上之后该能进：%d %s", w.Code, w.Body)
	}
	if w := do(rh, "POST", "/api/remote/pair-code", body("{}")(), cookie(dev), csrf); w.Code != 403 {
		t.Fatalf("配过对的远程设备也不该能出码，得到 %d", w.Code)
	}
	if w := do(rh, "POST", "/api/remote/logout", body("{}")(), cookie(dev)); w.Code != 403 {
		t.Fatalf("没有 X-Gerdr 的写请求该挡（CSRF），得到 %d", w.Code)
	}
	// 同一个码第二次：不认
	if w := do(rh, "POST", "/api/remote/pair", body(`{"code":"`+pc.Code+`"}`)(), csrf); w.Code != 401 {
		t.Fatalf("用过的码不该还能配，得到 %d", w.Code)
	}
	// 本机口上的 passkey：origin 不是 RPID，直接 409
	r = httptest.NewRequest("POST", "http://127.0.0.1:7870/api/remote/passkey/register/begin", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+s.Token)
	r.Header.Set("X-Gerdr", "1")
	w = httptest.NewRecorder()
	lh.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("本机口上加 passkey 该 409，得到 %d", w.Code)
	}
}
