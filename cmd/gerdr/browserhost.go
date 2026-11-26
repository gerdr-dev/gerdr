package main

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/gerdr-dev/gerdr/internal/chromeext"
	"github.com/gerdr-dev/gerdr/internal/config"
)

// runBrowserHost: Chrome started this program for the Gerdr browser extension. Relay it to the running Gerdr,
// found the way the CLI finds it (server.json and the token in the data dir), again before every reconnect.
func runBrowserHost(origin string) error {
	log, closeLog := browserHostLog()
	defer closeLog()
	return chromeext.RunHost(origin, func() (string, http.Header, error) {
		c, err := newAPIClient()
		if err != nil {
			return "", nil, err
		}
		h := http.Header{}
		h.Set("Authorization", "Bearer "+c.token)
		return "ws" + strings.TrimPrefix(c.base, "http") + "/api/browser/ext", h, nil
	}, log)
}

// browserHostLog is <data dir>/logs/browser-host.log, shared by every host Chrome starts (one per connected profile),
// kept under ~2 MB by moving it to .1. Its own file: the host is a separate process from Gerdr, and gerdr.log is
// Gerdr's. Nothing to write to (no data dir yet): a logger that drops everything.
func browserHostLog() (*slog.Logger, func()) {
	dir := filepath.Join(config.FromEnv().Dir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, func() {}
	}
	p := filepath.Join(dir, "browser-host.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 2<<20 {
		os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, func() {}
	}
	return slog.New(slog.NewTextHandler(f, nil)), func() { f.Close() }
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
