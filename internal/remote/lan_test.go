package remote

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/config"
)

// 直连口只服务本地网络：公网地址连上来（端口转发 / 全局 IPv6）一个字节都不给。
// A 100.64/10 peer without a tailnet address on our side (no LocalAddr here) stays out too, and so does an IPv6
// peer that didn't come in on one of our global IPv6 addresses.
func TestLanOnly(t *testing.T) {
	alerts := 0
	h := lanOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), true, true, func(string) { alerts++ })
	for addr, want := range map[string]int{"192.168.1.20:5555": 204, "[fe80::1]:1": 204, "203.0.113.9:1": 403, "[2001:db8::1]:1": 403, "100.88.1.1:1": 403} {
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

// portName: the names the relay hands to this host's port proxy (dialhome's portParent), the ones that get the
// wildcard certificate.
func TestPortName(t *testing.T) {
	for sni, want := range map[string]bool{
		"8765.gerdr.example.com": true, "8765.GERDR.example.com": true, "gerdr.example.com": false,
		"a1.gerdr.example.com": false, "1.2.gerdr.example.com": false, "8765.other.example.com": false, ".gerdr.example.com": false,
	} {
		if got := portName(sni, "gerdr.example.com"); got != want {
			t.Errorf("portName(%q) = %v, want %v", sni, got, want)
		}
	}
}

// Passkey origins never include port subdomains: a proxied page (the agent's) could otherwise ask for a passkey
// with the host as RP ID, under a prompt that looks normal, and trade it for a device (docs/dev/REMOTE.md
// "Local ports through the relay").
func TestPasskeyOriginsExcludePortSubdomains(t *testing.T) {
	_, origins := relyingParty(config.Remote{RelayURL: "https://relay.example.com", Host: "gerdr.example.com"})
	for _, o := range origins {
		if portName(strings.TrimPrefix(o, "https://"), "gerdr.example.com") || strings.HasPrefix(o, "https://*") {
			t.Fatalf("passkey origin %q is a port subdomain", o)
		}
	}
}
