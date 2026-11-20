package desktop

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// The hot half of the desktop App (docs/dev/SHELL.md): this package's Go code and shell/main.js, the main-process
// script desktop/main.js (the frozen loader) runs. Both ship inside the gerdr binary, so a backend update carries them.
//
// HotID tells two binaries' hot halves apart: a hash of the sources themselves, embedded here, so nothing in the build
// has to compute and pass it. A backend update whose HotID differs from the running core's needs the App started again
// to take effect (app.go applyUpdate); one with the same HotID only restarts the backend, as before.

//go:embed shell/main.js
var shellJS []byte

//go:embed *.go el/*.go shell/main.js
var hotSrc embed.FS

var hotID = sync.OnceValue(func() string {
	var names []string
	_ = fs.WalkDir(hotSrc, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, p)
		}
		return nil
	})
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, _ := hotSrc.ReadFile(n)
		h.Write([]byte(n + "\n"))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
})

// HotID: what this binary's core and shell script are (see above).
func HotID() string { return hotID() }

// shellScript writes the embedded main-process script under the shell's directory and returns its path:
// <data>/shell/<HotID>/main.js, so two Apps (or a core and the one it handed over to) never share a half-written file.
// GERDR_SHELL_JS runs a script from the repository instead (development: edit, reopen the dev shell).
func shellScript(data string) string {
	if p := os.Getenv("GERDR_SHELL_JS"); p != "" {
		return p
	}
	dir := filepath.Join(data, "shell", HotID())
	p := filepath.Join(dir, "main.js")
	if b, err := os.ReadFile(p); err == nil && string(b) == string(shellJS) {
		return p
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("shell script: %v", err)
		return ""
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, shellJS, 0o644); err != nil || os.Rename(tmp, p) != nil {
		log.Printf("shell script: can't write %s: %v", p, err)
		return ""
	}
	// older versions' scripts: only the one in use and this one are kept
	if names, err := os.ReadDir(filepath.Join(data, "shell")); err == nil && len(names) > 4 {
		for _, n := range names {
			if n.Name() != HotID() {
				_ = os.RemoveAll(filepath.Join(data, "shell", n.Name()))
			}
		}
	}
	return p
}

// hotIDOf asks another gerdr binary for its HotID; "" when it can't say (then it is treated as the same). Asked again
// only when the file changed: the idle poll asks every 15 s while a window stays in front.
func hotIDOf(bin string) string {
	st, err := os.Stat(bin)
	if bin == "" || err != nil {
		return ""
	}
	key := fmt.Sprintf("%s:%d:%d", bin, st.ModTime().UnixNano(), st.Size())
	hotMu.Lock()
	defer hotMu.Unlock()
	if v, ok := hotSeen[key]; ok {
		return v
	}
	v := askHotID(bin)
	hotSeen[key] = v
	return v
}

var (
	hotMu   sync.Mutex
	hotSeen = map[string]string{}
)

func askHotID(bin string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "shell-core", "-hot-id")
	hideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return ra == rb
}
