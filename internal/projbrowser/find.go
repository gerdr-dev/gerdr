package projbrowser

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// findBrowser: the Chromium browser installed here, in the order Lightron looks (Chrome, Edge, Brave, Chromium), for
// when there is no desktop App (with one, location.go runs the App's own program). GERDR_BROWSER_EXE overrides it.
func findBrowser() (string, error) {
	if p := os.Getenv("GERDR_BROWSER_EXE"); p != "" {
		return p, nil
	}
	var tries []string
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		for _, name := range []string{"Google Chrome", "Microsoft Edge", "Brave Browser", "Chromium"} {
			for _, root := range []string{"/Applications", filepath.Join(home, "Applications")} {
				tries = append(tries, filepath.Join(root, name+".app", "Contents", "MacOS", name))
			}
		}
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			root := os.Getenv(env)
			if root == "" {
				continue
			}
			tries = append(tries,
				filepath.Join(root, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(root, `Microsoft\Edge\Application\msedge.exe`),
				filepath.Join(root, `BraveSoftware\Brave-Browser\Application\brave.exe`),
				filepath.Join(root, `Chromium\Application\chrome.exe`))
		}
	default:
		for _, name := range []string{"google-chrome", "google-chrome-stable", "microsoft-edge", "brave-browser", "chromium", "chromium-browser"} {
			if p, err := exec.LookPath(name); err == nil {
				tries = append(tries, p)
			}
		}
	}
	for _, p := range tries {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New(i18n.S("这台电脑上没找到 Chrome / Edge / Brave / Chromium：项目浏览器要用其中一个",
		"No Chrome, Edge, Brave or Chromium on this computer: the project browser runs on one of them"))
}
