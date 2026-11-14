package lan

import (
	"testing"
	"time"
)

func TestPeerIsLocal(t *testing.T) {
	for addr, want := range map[string]bool{
		"192.168.1.5:5000":        true,
		"10.0.0.2:1":              true,
		"172.16.3.4:1":            true,
		"127.0.0.1:1":             true,
		"[::1]:1":                 true,
		"[fe80::1]:1":             true,
		"[::ffff:192.168.1.5]:80": true,
		"[fd00::1]:1":             true,
		"8.8.8.8:53":              false,
		"100.64.0.1:1":            false, // CGNAT 不算本地
		"[2001:db8::1]:1":         false,
		"garbage":                 false,
	} {
		if got := PeerIsLocal(addr); got != want {
			t.Errorf("PeerIsLocal(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestOriginsOff(t *testing.T) {
	if o := Origins(0, true); o != nil {
		t.Fatalf("port 0 应该没有候选, got %v", o)
	}
}

func TestTailnetInterface(t *testing.T) {
	for name, want := range map[string]bool{"utun4": true, "tailscale0": true, "Tailscale": true, "en0": false, "pdp_ip0": false} {
		if got := isTunnel(name); got != want {
			t.Errorf("isTunnel(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestViaTailnet(t *testing.T) {
	mu.Lock()
	cached, at = []Addr{
		{Name: "en0", Address: "100.70.0.9"}, // a carrier's CGNAT address on the real NIC
		{Name: "utun4", Address: "100.101.2.3", Virtual: true, Tailnet: true},
	}, time.Now()
	mu.Unlock()
	defer func() { mu.Lock(); cached = nil; mu.Unlock() }()

	for _, c := range []struct {
		peer, local string
		want        bool
	}{
		{"100.88.1.1:5000", "100.101.2.3:7878", true},
		{"100.88.1.1:5000", "100.70.0.9:7878", false},                // came in on the carrier address
		{"100.88.1.1:5000", "100.99.9.9:7878", false},                // not one of ours
		{"8.8.8.8:5000", "100.101.2.3:7878", false},                  // peer outside 100.64/10
		{"100.88.1.1:5000", "", false},                               // no local address known
		{"[::ffff:100.88.1.1]:1", "[::ffff:100.101.2.3]:7878", true}, // dual-stack socket
	} {
		if got := ViaTailnet(c.peer, c.local); got != c.want {
			t.Errorf("ViaTailnet(%q, %q) = %v, want %v", c.peer, c.local, got, c.want)
		}
	}
	if o := Origins(7878, true); len(o) != 1 || o[0] != "https://100.101.2.3:7878" {
		t.Errorf("Origins with tailnet = %v", o)
	}
	if o := Origins(7878, false); len(o) != 0 {
		t.Errorf("Origins without tailnet = %v", o)
	}
}
