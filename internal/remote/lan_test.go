package remote

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 直连口只服务本地网络：公网地址连上来（端口转发 / 全局 IPv6）一个字节都不给。
func TestLanOnly(t *testing.T) {
	alerts := 0
	h := lanOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), func(string) { alerts++ })
	for addr, want := range map[string]int{"192.168.1.20:5555": 204, "[fe80::1]:1": 204, "203.0.113.9:1": 403, "[2001:db8::1]:1": 403} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = addr
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s: 得到 %d，该是 %d", addr, w.Code, want)
		}
	}
	if alerts != 1 {
		t.Errorf("公网连上来只吼一次，吼了 %d 次", alerts)
	}
}
