package lan

import "testing"

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
	if o := Origins(0); o != nil {
		t.Fatalf("port 0 应该没有候选, got %v", o)
	}
}
