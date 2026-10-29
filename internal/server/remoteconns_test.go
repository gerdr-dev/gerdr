package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/auth"
)

// 远程 /ws 连上 / 断开：「正在连接」列表跟着变，能认出是哪台配过对的设备。
func TestRemoteConns(t *testing.T) {
	s, lh, rh := remoteServer(t)
	r := httptest.NewRequest("POST", "http://127.0.0.1:7870/api/remote/pair-code", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+s.Token)
	r.Header.Set("X-Gerdr", "1")
	w := httptest.NewRecorder()
	lh.ServeHTTP(w, r)
	var pc struct{ Code string }
	_ = json.Unmarshal(w.Body.Bytes(), &pc)
	var dev *http.Cookie
	for _, c := range do(rh, "GET", "/?pair="+pc.Code, nil).Result().Cookies() {
		if c.Name == "gerdr_dev" {
			dev = c
		}
	}
	if dev == nil {
		t.Fatal("没配上")
	}

	ws := httptest.NewRequest("GET", "https://"+testHost+"/ws", nil)
	ws.RemoteAddr = "203.0.113.9:1234"
	ws.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1")
	ws.AddCookie(dev)
	release := s.trackRemote(auth.MarkRemote(ws))

	var list []RemoteConn
	w = do(rh, "GET", "/api/remote/connections", nil, cookie(dev))
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Device == "" || list[0].Label == "" || list[0].IP != "203.0.113.9" || list[0].Client != "iPhone · Safari" || list[0].Since == 0 {
		t.Fatalf("连接列表不对：%d %s", w.Code, w.Body)
	}
	release()
	if n := len(s.remoteConns.list()); n != 0 {
		t.Fatalf("断开了还剩 %d 条", n)
	}
}

func TestUABrief(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36 EdgA/130.0":                    "Android · Edge",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0 Mobile/15E148 Safari/604.1": "iPhone · Chrome",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15":                  "Mac · Safari",
		"curl/8.0": "",
	} {
		if got := uaBrief(ua); got != want {
			t.Errorf("%q → %q，要 %q", ua, got, want)
		}
	}
}
