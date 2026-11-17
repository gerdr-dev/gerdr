//go:build !windows

package chromeext

func setRegistry(key, value string) error { return nil }
