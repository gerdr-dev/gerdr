package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/remote"
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

// 证书配置：远程碰不到（里面有 DNS 密钥）；本机能改，读回来只给名字不给值，换服务商时别家的凭据一起删掉。
func TestRemoteCertConfig(t *testing.T) {
	t.Setenv("GERDR_DO_AUTH_TOKEN", "")
	t.Setenv("GERDR_CLOUDFLARE_DNS_API_TOKEN", "") // SaveCert 会写进程环境：登记一下，测完还原
	s, lh, rh := remoteServer(t)
	if w := do(rh, "GET", "/api/remote/cert", nil); w.Code != 403 {
		t.Fatalf("远程不该能读证书配置，得到 %d", w.Code)
	}
	if w := do(rh, "POST", "/api/remote/cert/caa", nil, csrf, hdr("Origin", "https://"+testHost)); w.Code != 403 {
		t.Fatalf("remote must not spend the DNS credentials on CAA, got %d", w.Code)
	}
	if w := do(rh, "GET", "/api/remote/cert/caa", nil); w.Code != 403 {
		t.Fatalf("remote must not check CAA either, got %d", w.Code)
	}
	local := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:7870/api/remote/cert", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+s.Token)
		r.Header.Set("X-Gerdr", "1")
		w := httptest.NewRecorder()
		lh.ServeHTTP(w, r)
		return w
	}
	if w := local("POST", `{"dns":"cloudflare","env":{}}`); w.Code != 400 {
		t.Fatalf("凭据没填该拒，得到 %d %s", w.Code, w.Body)
	}
	if w := local("POST", `{"dns":"cloudflare","env":{"CLOUDFLARE_DNS_API_TOKEN":"cf-secret-123"}}`); w.Code != 200 {
		t.Fatalf("保存：%d %s", w.Code, w.Body)
	}
	w := local("GET", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "cf-secret-123") || !strings.Contains(w.Body.String(), `"set":["CLOUDFLARE_DNS_API_TOKEN"]`) {
		t.Fatalf("读回来该只有名字：%d %s", w.Code, w.Body)
	}
	if s.Remote.Config().ACMEDNS != "cloudflare" {
		t.Fatalf("配置没生效：%+v", s.Remote.Config())
	}
	// 留空 = 沿用；换一家服务商，cloudflare 的 token 要从文件里删掉
	if w := local("POST", `{"dns":"cloudflare","email":"a@b.c","env":{}}`); w.Code != 200 {
		t.Fatalf("留空沿用：%d %s", w.Code, w.Body)
	}
	if w := local("POST", `{"dns":"digitalocean","env":{"DO_AUTH_TOKEN":"do-1"}}`); w.Code != 200 {
		t.Fatalf("换服务商：%d %s", w.Code, w.Body)
	}
	f, _ := config.ReadRemoteFile(filepath.Dir(s.Remote.Dir))
	if _, ok := f.Env["CLOUDFLARE_DNS_API_TOKEN"]; ok || f.Env["DO_AUTH_TOKEN"] != "do-1" {
		t.Fatalf("换服务商之后文件里的凭据不对：%v", f.Env)
	}
}

// 爆破：进了退避之后同时打一批猜测，只有一个真去验，其余当场 429 —— 不然并发请求能绕过退避和封锁。
func TestRemotePairConcurrentGuesses(t *testing.T) {
	_, _, rh := remoteServer(t)
	guess := func() int { return do(rh, "POST", "/api/remote/pair", body(`{"code":"AAAAAAAA"}`)(), csrf).Code }
	for i := 0; i < 3; i++ { // 前两次不罚，第三次之后下一次要先睡 1 秒
		if c := guess(); c != 401 {
			t.Fatalf("第 %d 次猜错该 401，得到 %d", i+1, c)
		}
	}
	codes := make(chan int, 10)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- guess() }()
	}
	wg.Wait()
	close(codes)
	n := map[int]int{}
	for c := range codes {
		n[c]++
	}
	if n[401] != 1 || n[429] != 9 {
		t.Fatalf("并发 10 次该只有 1 次真去验（401）、9 次 429，得到 %v", n)
	}
}

// 到电脑上批准：远程设备发起，**只有本机能批**（远程能批的话，偷来的 cookie 自己批自己，step-up 就白设了）。
func TestRemoteStepUpApproval(t *testing.T) {
	s, lh, rh := remoteServer(t)
	code, _ := s.Remote.Auth.MintCode()
	dev, token, _ := s.Remote.Auth.Redeem(code, "Android · Edge", "")
	c := cookie(&http.Cookie{Name: "gerdr_dev", Value: token})
	w := do(rh, "POST", "/api/remote/stepup/request", body("{}")(), c, csrf)
	var req struct{ ID, Code string }
	_ = json.Unmarshal(w.Body.Bytes(), &req)
	if w.Code != 200 || req.ID == "" || len(req.Code) != 4 {
		t.Fatalf("发起：%d %s", w.Code, w.Body)
	}
	if w := do(rh, "GET", "/api/remote/stepup/pending", nil, c); w.Code != 403 {
		t.Fatalf("远程不该看得到待批列表，得到 %d", w.Code)
	}
	if w := do(rh, "POST", "/api/remote/stepup/approve/"+req.ID, body(`{"ok":true}`)(), c, csrf); w.Code != 403 {
		t.Fatalf("远程不该能自己批准，得到 %d", w.Code)
	}
	local := func(method, path, b string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:7870"+path, strings.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+s.Token)
		r.Header.Set("X-Gerdr", "1")
		w := httptest.NewRecorder()
		lh.ServeHTTP(w, r)
		return w
	}
	if w := local("GET", "/api/remote/stepup/pending", ""); !strings.Contains(w.Body.String(), req.Code) {
		t.Fatalf("本机该看到请求：%s", w.Body)
	}
	if w := local("POST", "/api/remote/stepup/approve/"+req.ID, `{"ok":true}`); w.Code != 200 {
		t.Fatalf("本机批准：%d %s", w.Code, w.Body)
	}
	if w := do(rh, "GET", "/api/remote/stepup/request/"+req.ID, nil, c); !strings.Contains(w.Body.String(), "approved") {
		t.Fatalf("设备该看到已批准：%s", w.Body)
	}
	for _, d := range s.Remote.Auth.Devices() {
		if d.ID == dev.ID && time.Since(d.PasskeyAt) > time.Minute {
			t.Fatal("批准之后该给这台设备一次 step-up（PasskeyAt）")
		}
	}
}

// 设置里的连接 / 移除：远程那一侧碰不到（远程移除 = 把自己锁在门外）；本机移除后当场关掉远程那条路，
// 设备和 passkey 一起清掉，不用重启。
func TestRemoteDisconnect(t *testing.T) {
	t.Setenv("GERDR_RELAY_URL", "")
	t.Setenv("GERDR_RELAY_HOST", "")
	s, lh, rh := remoteServer(t)
	for _, p := range []string{"/api/remote/connect", "/api/remote/disconnect", "/api/remote/reload"} {
		if w := do(rh, "POST", p, strings.NewReader("{}"), csrf, hdr("Origin", "https://"+testHost)); w.Code != 403 {
			t.Fatalf("远程不该能调 %s，得到 %d", p, w.Code)
		}
	}
	local := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7870"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+s.Token)
		r.Header.Set("X-Gerdr", "1")
		w := httptest.NewRecorder()
		lh.ServeHTTP(w, r)
		return w
	}
	if w := local("/api/remote/connect", `{"relayURL":"https://relay.example.com","host":"other.example.com","invite":"x"}`); w.Code != 400 || !strings.Contains(w.Body.String(), "已经连着") {
		t.Fatalf("连着的时候再连该拒，得到 %d %s", w.Code, w.Body)
	}
	code, _ := s.Remote.Auth.MintCode()
	if _, _, err := s.Remote.Auth.Redeem(code, "test", "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	w := local("/api/remote/disconnect", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"revoked":1`) {
		t.Fatalf("移除：%d %s", w.Code, w.Body)
	}
	if st := s.Remote.Status(); st.Enabled || st.RelayURL != "" && st.Host != "" {
		t.Fatalf("移除后还开着：%+v", st)
	}
	if s.Remote.Passkeys.Available() {
		t.Fatal("移除后 passkey 还绑着旧域名")
	}
	if w := do(rh, "GET", "/", nil); w.Code != 404 {
		t.Fatalf("移除后远程那条路该关掉，得到 %d", w.Code)
	}
}
