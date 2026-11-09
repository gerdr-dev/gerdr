//go:build !darwin

package main

// brand / prepareEngine: only macOS needs a copy of the browser that carries Gerdr's identity (engine_darwin.go);
// elsewhere run the browser as installed.
type brand struct{ name, id, icns string }

func prepareEngine(exe, dir string, b brand) (string, error) { return exe, nil }

func makeIcns(png, out string) error { return nil }

func plistString(path, key string) (string, error) { return "", nil }
