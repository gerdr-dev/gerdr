package main

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gerdr-dev/gerdr/internal/chromeext"
)

// runBrowserHost: Chrome started this program for the Gerdr browser extension. Relay it to the running Gerdr,
// found the way the CLI finds it (server.json and the token in the data dir), again before every reconnect.
func runBrowserHost(origin string) error {
	return chromeext.RunHost(origin, func() (string, http.Header, error) {
		c, err := newAPIClient()
		if err != nil {
			return "", nil, err
		}
		h := http.Header{}
		h.Set("Authorization", "Bearer "+c.token)
		return "ws" + strings.TrimPrefix(c.base, "http") + "/api/browser/ext", h, nil
	})
}

// registerBrowserHost points Chrome's native host manifest at this program, every start (the App moved, an update
// changed the path). Only the default data dir does it, so a `make dev` instance doesn't take the extension from
// the Gerdr in use; GERDR_BROWSER_HOST=1 makes a dev instance take it (through a launcher that sets its GERDR_DIR,
// since Chrome starts the host with no environment of ours). `go run` binaries are skipped: they vanish.
func registerBrowserHost(dir string, log *slog.Logger) {
	if os.Getenv("GERDR_DIR") != "" && os.Getenv("GERDR_BROWSER_HOST") != "1" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	if strings.Contains(exe, string(filepath.Separator)+"go-build") {
		return
	}
	if os.Getenv("GERDR_DIR") != "" && runtime.GOOS != "windows" {
		launcher := filepath.Join(dir, "browser-host")
		script := "#!/bin/sh\nGERDR_DIR='" + dir + "' exec '" + exe + "' \"$@\"\n"
		if err := os.WriteFile(launcher, []byte(script), 0o755); err != nil {
			log.Warn("browser host launcher", "err", err)
			return
		}
		exe = launcher
	}
	if err := chromeext.Register(exe, dir); err != nil {
		log.Warn("register browser native host", "err", err)
	}
}
