package version

import (
	"os"
	"sync"
	"time"
)

// Installed is when the running binary was put on this machine: its file's modification time, read once. Installing
// (make install, the App's self-update, a fresh App) writes the file anew, so it says how old what's running is — the
// version string alone (git describe) doesn't, and a build from a dirty tree looks the same for days. Zero if unknown.
var Installed = sync.OnceValue(func() time.Time {
	exe, err := os.Executable()
	if err != nil {
		return time.Time{}
	}
	st, err := os.Stat(exe)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
})
