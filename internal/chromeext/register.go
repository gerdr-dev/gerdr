package chromeext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// HostName is the native host's name; the extension's connectNative uses the same.
const HostName = "com.gerdr.browser"

// Register the native host with this computer's Chromium browsers (Chrome, Edge, Chromium, Brave): a manifest file
// each, plus registry keys on Windows. exe is what Chrome starts (the gerdr program, or a launcher for a non-default
// data dir); dataDir holds the Windows manifest. Written at every start, so a moved or updated App stays right.
// Browsers that aren't installed are skipped.
func Register(exe, dataDir string) error {
	origins := make([]string, 0, len(ExtensionIDs))
	for _, id := range ExtensionIDs {
		origins = append(origins, "chrome-extension://"+id+"/")
	}
	manifest, _ := json.MarshalIndent(map[string]any{
		"name":            HostName,
		"description":     "Gerdr",
		"path":            exe,
		"type":            "stdio",
		"allowed_origins": origins,
	}, "", "  ")
	manifest = append(manifest, '\n')
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	write := func(dir string) error {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		p := filepath.Join(dir, HostName+".json")
		if old, err := os.ReadFile(p); err == nil && string(old) == string(manifest) {
			return nil
		}
		return os.WriteFile(p, manifest, 0o644)
	}
	var firstErr error
	switch runtime.GOOS {
	case "darwin", "linux":
		var roots []string
		if runtime.GOOS == "darwin" {
			base := filepath.Join(home, "Library", "Application Support")
			for _, b := range []string{"Google/Chrome", "Google/Chrome Beta", "Google/Chrome Canary", "Chromium", "Microsoft Edge", "BraveSoftware/Brave-Browser"} {
				roots = append(roots, filepath.Join(base, filepath.FromSlash(b)))
			}
		} else {
			base := filepath.Join(home, ".config")
			for _, b := range []string{"google-chrome", "google-chrome-beta", "chromium", "microsoft-edge", "BraveSoftware/Brave-Browser"} {
				roots = append(roots, filepath.Join(base, filepath.FromSlash(b)))
			}
		}
		for _, root := range roots {
			if st, err := os.Stat(root); err != nil || !st.IsDir() {
				continue
			}
			if err := write(filepath.Join(root, "NativeMessagingHosts")); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	case "windows":
		dir := filepath.Join(dataDir, "browser-extension")
		if err := write(dir); err != nil {
			return err
		}
		file := filepath.Join(dir, HostName+".json")
		for _, key := range []string{`Google\Chrome`, `Microsoft\Edge`, `Chromium`, `BraveSoftware\Brave-Browser`} {
			if err := setRegistry(`Software\`+key+`\NativeMessagingHosts\`+HostName, file); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("registry %s: %w", key, err)
			}
		}
	}
	return firstErr
}
