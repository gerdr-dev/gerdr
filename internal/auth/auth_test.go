package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T, c Config) *Store {
	t.Helper()
	if c.Dir == "" {
		c.Dir = t.TempDir()
	}
	s, err := New(c)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func withCookie(token string) *http.Request {
	r := httptest.NewRequest("GET", "https://gerdr.example.com/", nil)
	r.RemoteAddr = "203.0.113.5:4444"
	if token != "" {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	}
	return r
}

// 配对码用一次就废；换来的令牌能认、落盘只有哈希。
func TestRedeemOnceAndHashOnly(t *testing.T) {
	s := newStore(t, Config{TTL: 24 * time.Hour})
	code, _ := s.MintCode()
	dev, token, err := s.Redeem(strings.ToLower(code[:4])+"-"+code[4:], "iPhone Safari", "203.0.113.5")
	if err != nil {
		t.Fatalf("手输的大小写 / 连字符该认：%v", err)
	}
	if _, _, err := s.Redeem(code, "", ""); err == nil {
		t.Fatal("同一个码第二次不该还能用")
	}
	id := s.Authenticate(withCookie(token))
	if id == nil || id.Device.ID != dev.ID {
		t.Fatal("换来的 cookie 认不出来")
	}
	if s.Authenticate(withCookie("nope")) != nil || s.Authenticate(withCookie("")) != nil {
		t.Fatal("乱给的 cookie 不该认")
	}
	b, _ := os.ReadFile(filepath.Join(s.cfg.Dir, "devices.json"))
	if strings.Contains(string(b), token) {
		t.Fatal("devices.json 里出现了令牌明文 —— 被 agent 读走就是一份能远程进来的凭据")
	}
}

// 远程那条路只认设备 cookie：Bearer（本机令牌那种）一律不认。
func TestNoBearer(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()
	_, token, _ := s.Redeem(code, "", "")
	r := withCookie("")
	r.Header.Set("Authorization", "Bearer "+token)
	if s.Authenticate(r) != nil {
		t.Fatal("远程那条路不该认 Authorization 头")
	}
}

func TestExpiredCode(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()
	s.now = func() time.Time { return time.Now().Add(CodeTTL + time.Second) }
	if _, _, err := s.Redeem(code, "", ""); err == nil {
		t.Fatal("过期的码不该能用")
	}
}

func TestRevoke(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()
	dev, token, _ := s.Redeem(code, "", "")
	if _, ok := s.Revoke(dev.ID[:4]); !ok {
		t.Fatal("ID 前四位就该能踢")
	}
	if s.Authenticate(withCookie(token)) != nil {
		t.Fatal("踢掉之后下一个请求就该不认")
	}
	// 重启之后也一样（踢掉的那份写进盘里了）
	s2 := newStore(t, Config{Dir: s.cfg.Dir})
	if s2.Authenticate(withCookie(token)) != nil {
		t.Fatal("重启之后踢掉的设备又回来了")
	}
}

func TestSlidingExpiry(t *testing.T) {
	s := newStore(t, Config{TTL: time.Hour})
	code, _ := s.MintCode()
	_, token, _ := s.Redeem(code, "", "")
	base := time.Now()
	s.now = func() time.Time { return base.Add(50 * time.Minute) }
	if s.Authenticate(withCookie(token)) == nil {
		t.Fatal("没过期就该认")
	}
	s.now = func() time.Time { return base.Add(100 * time.Minute) }
	if s.Authenticate(withCookie(token)) == nil {
		t.Fatal("用过一次就该续期（滑动过期）")
	}
	s.now = func() time.Time { return base.Add(200 * time.Minute) }
	if s.Authenticate(withCookie(token)) != nil {
		t.Fatal("一个 TTL 没用过就该过期")
	}
}

func TestCookieAlwaysSecure(t *testing.T) {
	s := newStore(t, Config{})
	w := httptest.NewRecorder()
	s.IssueCookie(w, "x")
	c := w.Result().Cookies()[0]
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("远程设备 cookie 要 Secure + HttpOnly + SameSite=Strict，得到 %+v", c)
	}
}

// 配对码进来的设备是临时的：到点就掉、用了也不续；过一次 passkey 转正。
func TestPairTempDevice(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s, err := New(Config{Dir: t.TempDir(), TTL: 90 * 24 * time.Hour, PairTTL: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	code, _ := s.MintCode()
	d, tok, err := s.Redeem(code, "Android · Edge", "")
	if err != nil || !d.Temp || !d.Expires.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("配对码进来该是 30 分钟的临时设备：%+v %v", d, err)
	}
	now = now.Add(20 * time.Minute)
	if s.lookup(tok, "") == nil {
		t.Fatal("20 分钟时还该能用")
	}
	now = now.Add(11 * time.Minute) // 用过一次也不续：31 分钟时掉
	if s.lookup(tok, "") != nil {
		t.Fatal("31 分钟时该掉了（临时设备不续期）")
	}

	code, _ = s.MintCode()
	d, tok, _ = s.Redeem(code, "Android · App", "")
	s.MarkVerified(d.ID) // 过了一次 passkey
	now = now.Add(2 * time.Hour)
	got := s.lookup(tok, "")
	if got == nil || got.Temp {
		t.Fatalf("过了 passkey 该转正：%+v", got)
	}
	// passkey 直接登录的新设备不是临时的
	if nd, _ := s.NewDevice("iPhone", "", "passkey"); nd.Temp {
		t.Fatal("passkey 登录的新设备不该是临时的")
	}
}
