package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zbysir/gerdr/internal/auth"
	"github.com/zbysir/gerdr/internal/config"
	"github.com/zbysir/gerdr/internal/hub"
	"github.com/zbysir/gerdr/internal/remote"
)

// 带一把（假的）passkey 的远程服务端：device flow 在一把都没有时直接拒。
func remoteServerWithPasskey(t *testing.T) (*Server, http.Handler, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	rd := config.RemoteDir(dir)
	_ = os.MkdirAll(rd, 0o700)
	if err := os.WriteFile(filepath.Join(rd, "passkeys.json"), []byte(`{"handle":"aGFuZGxl","keys":[{"id":"k1","label":"test"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Remote{RelayURL: "https://relay.example.com", Host: testHost, DeviceTTL: 24 * time.Hour, ReauthAfter: 24 * time.Hour,
		AndroidCerts: []string{config.OfficialAndroidCert}}
	rm, err := remote.Open(dir, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if rm.Passkeys.Count() != 1 {
		t.Fatalf("假 passkey 没读进来：%d", rm.Passkeys.Count())
	}
	s := &Server{Token: "local-token-local-token-local-token", Remote: rm}
	h := s.Handler()
	return s, h, RemoteHandler(h)
}

func TestAssetLinks(t *testing.T) {
	_, _, rh := remoteServerWithPasskey(t)
	w := do(rh, "GET", "/.well-known/assetlinks.json", nil)
	if w.Code != 200 {
		t.Fatalf("没认证也该拿得到 assetlinks：%d %s", w.Code, w.Body)
	}
	var out []struct {
		Relation []string
		Target   struct {
			Namespace   string
			PackageName string   `json:"package_name"`
			Certs       []string `json:"sha256_cert_fingerprints"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out) != 1 {
		t.Fatalf("格式不对：%v %s", err, w.Body)
	}
	tg := out[0].Target
	if tg.PackageName != config.AndroidPackage || len(tg.Certs) != 1 || tg.Certs[0] != config.OfficialAndroidCert ||
		out[0].Relation[0] != "delegate_permission/common.get_login_creds" {
		t.Fatalf("内容不对：%s", w.Body)
	}
}

func TestAndroidOrigin(t *testing.T) {
	o := auth.AndroidOrigin(config.OfficialAndroidCert)
	if !strings.HasPrefix(o, "android:apk-key-hash:") || len(o) != len("android:apk-key-hash:")+43 {
		t.Fatalf("origin 不对：%q", o)
	}
	if auth.AndroidOrigin("zz:zz") != "" {
		t.Fatal("认不出的指纹该给空")
	}
}

// device flow：App 发起（不认证）→ 远程批准要先过 passkey（stepup）→ 本机口凭令牌能批 → App 领走凭据、拿它进得去。
func TestCLIFlow(t *testing.T) {
	s, lh, rh := remoteServerWithPasskey(t)
	ua := hdr("User-Agent", "Mozilla/5.0 (Linux; Android 15) Chrome/130 GerdrApp/0.1.0")
	w := do(rh, "POST", "/api/remote/cli/start", body("{}")(), csrf, ua)
	var st struct{ ID, Poll, Code, URL string }
	_ = json.Unmarshal(w.Body.Bytes(), &st)
	if w.Code != 200 || st.Code == "" || st.URL != "https://"+testHost+"/?cli" {
		t.Fatalf("发起：%d %s", w.Code, w.Body)
	}
	if w := do(rh, "POST", "/api/remote/cli/start", body("{}")()); w.Code != 403 {
		t.Fatalf("没带 X-Gerdr 的发起该 403，得到 %d", w.Code)
	}
	poll := func() map[string]any {
		w := do(rh, "POST", "/api/remote/cli/poll", body(`{"id":"`+st.ID+`","poll":"`+st.Poll+`"}`)(), csrf)
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	if m := poll(); m["state"] != "pending" {
		t.Fatalf("还没批该 pending：%v", m)
	}
	if w := do(rh, "POST", "/api/remote/cli/approve", body(`{"code":"`+st.Code+`"}`)(), csrf); w.Code != 401 {
		t.Fatalf("没认证不能批，得到 %d", w.Code)
	}

	// 远程一台配过对、但最近没刷过 passkey 的设备：要 stepup
	code, _ := s.Remote.Auth.MintCode()
	_, tok, _ := s.Remote.Auth.Redeem(code, "iPhone · Safari", "")
	c := cookie(&http.Cookie{Name: auth.CookieName, Value: tok})
	if w := do(rh, "POST", "/api/remote/cli/peek", body(`{"code":"`+st.Code+`"}`)(), c, csrf); w.Code != 200 || !strings.Contains(w.Body.String(), "Android · App") {
		t.Fatalf("peek 该看到发起方：%d %s", w.Code, w.Body)
	}
	if w := do(rh, "POST", "/api/remote/cli/approve", body(`{"code":"`+st.Code+`"}`)(), c, csrf); w.Code != 403 || !strings.Contains(w.Body.String(), "stepup") {
		t.Fatalf("远程批准没过 passkey 该 stepup，得到 %d %s", w.Code, w.Body)
	}

	// 本机口（令牌）能直接批
	r := httptest.NewRequest("POST", "http://127.0.0.1:7870/api/remote/cli/approve", strings.NewReader(`{"code":"`+st.Code+`"}`))
	r.Header.Set("Authorization", "Bearer "+s.Token)
	r.Header.Set("X-Gerdr", "1")
	lw := httptest.NewRecorder()
	lh.ServeHTTP(lw, r)
	if lw.Code != 200 {
		t.Fatalf("本机批准：%d %s", lw.Code, lw.Body)
	}
	m := poll()
	tokNew, _ := m["token"].(string)
	if m["state"] != "approved" || tokNew == "" {
		t.Fatalf("批了该领到凭据：%v", m)
	}
	if m := poll(); m["state"] != nil {
		t.Fatalf("领过一次就该没了：%v", m)
	}
	if w := do(rh, "GET", "/api/remote/whoami", nil, cookie(&http.Cookie{Name: auth.CookieName, Value: tokNew})); !strings.Contains(w.Body.String(), `"authed":true`) {
		t.Fatalf("新凭据该进得去：%s", w.Body)
	}

	// 带着不认的 cookie 发起 = 401（App 据此退成新设备）
	if w := do(rh, "POST", "/api/remote/cli/start", body("{}")(), csrf, cookie(&http.Cookie{Name: auth.CookieName, Value: "nope"})); w.Code != 401 {
		t.Fatalf("不认的 cookie 发起该 401，得到 %d", w.Code)
	}
}

func TestCLINoPasskey(t *testing.T) {
	_, _, rh := remoteServer(t)
	if w := do(rh, "POST", "/api/remote/cli/start", body("{}")(), csrf); w.Code != 409 || !strings.Contains(w.Body.String(), "nopasskey") {
		t.Fatalf("一把 passkey 都没有该 409 nopasskey，得到 %d %s", w.Code, w.Body)
	}
}

// 提示的口径：变成「等你」、在跑 → 空闲。第一拍（不带 since）只对底。
func TestAppNotices(t *testing.T) {
	s := &Server{}
	w := &s.appWatch
	w.last = map[string]watchState{}
	w.bell = make(chan struct{})
	w.once.Do(func() {}) // 不起订阅那条循环：这里直接喂摘要
	sum := func(st string) hub.Summary {
		return hub.Summary{Record: hub.Record{ID: "s1", Agent: "claude", Cwd: "/x/proj"}, Status: st}
	}
	w.last["s1"] = watchState{status: hub.StIdle}
	s.onSession(sum(hub.StRunning))
	s.onSession(sum(hub.StWaiting))
	s.onSession(sum(hub.StRunning))
	s.onSession(sum(hub.StRunning)) // 状态没变：不算
	s.onSession(sum(hub.StIdle))
	if len(w.notices) != 2 || w.notices[0].Status != "waiting" || w.notices[1].Status != "done" || w.notices[1].Title != "proj" {
		t.Fatalf("提示不对：%+v", w.notices)
	}
	if w.ver != 4 {
		t.Fatalf("一览版本该是 4（四次真变化），得到 %d", w.ver)
	}

	get := func(q string) map[string]any {
		r := httptest.NewRequest("GET", "/api/app/notices"+q, nil)
		rw := httptest.NewRecorder()
		s.appNotices(rw, r)
		var m map[string]any
		_ = json.Unmarshal(rw.Body.Bytes(), &m)
		return m
	}
	if m := get(""); m["seq"] != float64(2) || len(m["notices"].([]any)) != 0 {
		t.Fatalf("第一拍只对底：%v", m)
	}
	if m := get("?since=1&wait=0"); len(m["notices"].([]any)) != 1 {
		t.Fatalf("since=1 该拿到后一条：%v", m)
	}
	// 挂着等：来了新提示当场回
	done := make(chan map[string]any)
	go func() { done <- get("?since=2&wait=5") }()
	time.Sleep(50 * time.Millisecond)
	s.onSession(sum(hub.StWaiting))
	select {
	case m := <-done:
		if m["seq"] != float64(3) {
			t.Fatalf("醒来该拿到第 3 条：%v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("新提示来了长轮询没醒")
	}
}

func TestLastAgentText(t *testing.T) {
	ev := func(kind, u string) *hub.Event { return &hub.Event{Kind: kind, Update: json.RawMessage(u)} }
	chunk := func(s string) *hub.Event {
		return ev(hub.EvUpdate, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"`+s+`"}}`)
	}
	evs := []*hub.Event{
		{Kind: hub.EvTurnStart}, chunk("先看看"), ev(hub.EvUpdate, `{"sessionUpdate":"tool_call"}`),
		chunk("改好了，"), chunk("测试都过了。"), {Kind: hub.EvTurnEnd},
	}
	if got := lastAgentText(evs); got != "改好了，测试都过了。" {
		t.Fatalf("得到 %q", got)
	}
}
