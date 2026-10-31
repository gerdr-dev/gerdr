//go:build !darwin

package server

// footprintKB: only macOS has one that differs from RSS in a way that matters (footprint_darwin.go).
func footprintKB(int) (int64, bool) { return 0, false }
