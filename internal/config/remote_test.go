package config

import "testing"

// Relay or direct access; only direct access may put a port in the address.
func TestRemoteWaysIn(t *testing.T) {
	for _, c := range []struct {
		name    string
		r       Remote
		enabled bool
		public  string
	}{
		{"relay", Remote{Host: "g.example.com", RelayURL: "https://relay", LanPort: 7878}, true, "g.example.com"},
		{"direct, Tailscale-style", Remote{Host: "g.example.com", Direct: true, LanPort: 7878}, true, "g.example.com:7878"},
		{"direct behind 443", Remote{Host: "g.example.com", Direct: true, LanPort: 7878, URLPort: 443}, true, "g.example.com"},
		{"direct, another outside port", Remote{Host: "g.example.com", Direct: true, LanPort: 7878, URLPort: 8443}, true, "g.example.com:8443"},
		{"direct without a LAN port", Remote{Host: "g.example.com", Direct: true}, false, "g.example.com"},
		{"relay wins", Remote{Host: "g.example.com", Direct: true, RelayURL: "https://relay", LanPort: 7878}, true, "g.example.com"},
		{"no way in", Remote{Host: "g.example.com", LanPort: 7878}, false, "g.example.com"},
	} {
		if got := c.r.Enabled(); got != c.enabled {
			t.Errorf("%s: Enabled = %v", c.name, got)
		}
		if got := c.r.PublicHost(); got != c.public {
			t.Errorf("%s: PublicHost = %q, want %q", c.name, got, c.public)
		}
	}
}
