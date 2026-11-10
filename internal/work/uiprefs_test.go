package work

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A rename that older Gerdr wrote into an installed panel's ui/ui.json moves to user/ui.json, so an update that also
// changes ui/ui.json neither stashes it nor loses it; a new rename goes straight to user/.
func TestUIPrefs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	git := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=a@b", "-c", "user.name=a"}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	src := filepath.Join(t.TempDir(), "board")
	os.MkdirAll(filepath.Join(src, "ui"), 0o755)
	os.WriteFile(filepath.Join(src, "ui", "ui.json"), []byte("{\n  \"title\": \"Board\",\n  \"icon\": \"orbit\",\n  \"chat\": false\n}\n"), 0o644)
	os.WriteFile(filepath.Join(src, "ui", "package.json"), []byte(`{"dependencies":{}}`), 0o644)
	git(src, "init", "-q")
	git(src, "add", ".")
	git(src, "commit", "-qm", "x")

	r := New(t.TempDir(), nil)
	key, err := r.Install(context.Background(), "p", "file://"+src, "")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := r.Dir(key)
	// What older Gerdr did: the rename and icon written into the shared file
	os.WriteFile(filepath.Join(dir, "ui", "ui.json"), []byte("{\n  \"title\": \"团队\",\n  \"icon\": \"users\",\n  \"chat\": false\n}\n"), 0o644)
	// Upstream changes the same file
	os.WriteFile(filepath.Join(src, "ui", "ui.json"), []byte("{\n  \"title\": \"Board\",\n  \"icon\": \"orbit\"\n}\n"), 0o644)
	git(src, "commit", "-qam", "y")
	if _, err := r.Update(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	cfg, err := r.UIConfig(key)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["title"] != "团队" || cfg["icon"] != "users" || cfg["chat"] != nil {
		t.Errorf("after update: %v", cfg)
	}
	uiChanged := func() bool {
		out, _ := exec.Command("git", "-C", dir, "status", "--porcelain", "ui/ui.json").Output()
		return len(out) > 0
	}
	if uiChanged() {
		t.Error("ui/ui.json still changed after lifting the rename out")
	}

	if err := r.SetUIPrefs(key, map[string]string{"title": "Mine", "color": ""}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = r.UIConfig(key)
	if cfg["title"] != "Mine" || cfg["icon"] != "users" || cfg["color"] != "" {
		t.Errorf("after rename: %v", cfg)
	}
	if uiChanged() {
		t.Error("a rename changed the panel's own files")
	}

	// Anything else changed in ui/ui.json is someone working on the panel: left alone
	own := []byte("{\n  \"title\": \"New\",\n  \"favicon\": \"x.png\"\n}\n")
	os.WriteFile(filepath.Join(dir, "ui", "ui.json"), own, 0o644)
	liftUIPrefs(dir)
	if b, _ := os.ReadFile(filepath.Join(dir, "ui", "ui.json")); string(b) != string(own) {
		t.Error("an author's ui.json edit was taken back")
	}
}
