//go:build !windows

package addons

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// routeService reports its routes (one refused: not loopback), then drops them when told "drop", exits on "crash".
const routeService = `#!/bin/sh
echo '{"type":"routes","routes":[{"from":"https://relay.example/","to":"http://127.0.0.1:7861/"},{"from":"https://relay.example/v2","to":"http://127.0.0.1:7862"},{"from":"https://other.example","to":"http://10.0.0.1:80"},{"from":"https://picky.example","to":"http://127.0.0.1:7863","agents":["claude"," "]}]}'
while IFS= read -r line; do
  case "$line" in
    *drop*) echo '{"type":"routes","routes":[]}';;
    *crash*) exit 3;;
  esac
done
`

// A service with "routes": true takes over the addresses it names while it runs; one without may not.
func TestServiceRoutes(t *testing.T) {
	dir := t.TempDir()
	for id, spec := range map[string]string{"relay": `"routes":true`, "plain": `"events":["x/*"]`} {
		pdir := filepath.Join(dir, "addons", id)
		_ = os.MkdirAll(pdir, 0o755)
		if err := os.WriteFile(filepath.Join(pdir, "svc.sh"), []byte(routeService), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pdir, "addon.json"), []byte(`{"service":{"command":"./svc.sh",`+spec+`}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := NewServices(New(dir, slog.Default()), slog.Default())
	s.Sync()
	defer s.Close()

	waitFor(t, "routes", func() bool { return s.Rewrite("x", "https://relay.example") != "" })
	for base, want := range map[string]string{
		"https://relay.example":           "http://127.0.0.1:7861",
		"https://relay.example/":          "http://127.0.0.1:7861",
		"HTTPS://Relay.Example/api":       "http://127.0.0.1:7861/api",
		"https://relay.example/v2/":       "http://127.0.0.1:7862", // the longer from wins
		"https://relay.example.com":       "",                      // another host
		"https://other.example":           "",                      // its route wasn't loopback
		"":                                "",
		"https://api.anthropic.com/v1/xx": "",
	} {
		if got := s.Rewrite("x", base); got != want {
			t.Errorf("Rewrite(%q) = %q, want %q", base, got, want)
		}
	}
	// A route naming its agents takes only those
	if got := s.Rewrite("claude", "https://picky.example"); got != "http://127.0.0.1:7863" {
		t.Errorf("picked agent: %q", got)
	}
	if got := s.Rewrite("codex", "https://picky.example"); got != "" {
		t.Errorf("agent not picked was routed to %q", got)
	}
	if st := s.Status("relay"); st == nil || len(st.Routes) != 3 || len(st.Routes[2].Agents) != 1 {
		t.Fatalf("relay status routes = %+v", st)
	}
	if st := s.Status("plain"); st == nil || len(st.Routes) != 0 {
		t.Fatalf("plain got routes without asking: %+v", st)
	}

	// Taking them back, and a crash, both end the routing
	_ = s.Send("relay", []byte(`"drop"`))
	waitFor(t, "routes dropped", func() bool { return s.Rewrite("x", "https://relay.example") == "" })
	_ = s.Send("relay", []byte(`"crash"`))
	waitFor(t, "restarted with routes", func() bool { return s.Rewrite("x", "https://relay.example") != "" })
	_ = s.Send("relay", []byte(`"crash"`))
	waitFor(t, "routes gone with the process", func() bool {
		st := s.Status("relay")
		return st.State != "running" || s.Rewrite("x", "https://relay.example") == ""
	})
}

// {os} / {arch} in a service's command pick the file for this computer.
func TestServiceCommandPlatform(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "addons", "multi")
	_ = os.MkdirAll(filepath.Join(pdir, "bin"), 0o755)
	if err := os.WriteFile(filepath.Join(pdir, "bin", "svc-"+runtime.GOOS+"-"+runtime.GOARCH), []byte("#!/bin/sh\necho '{\"type\":\"status\",\"text\":\"mine\"}'\nread x\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "addon.json"), []byte(`{"service":{"command":"./bin/svc-{os}-{arch}"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServices(New(dir, slog.Default()), slog.Default())
	s.Sync()
	defer s.Close()
	waitFor(t, "the platform's file running", func() bool { st := s.Status("multi"); return st != nil && st.Text == "mine" })
}
