package uisite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Workspace packages: a dependency written "workspace:*" in package.json is a package that lives inside the UI
// directory (packages/<dir>/ with its own package.json naming it) and is served from source like the rest — no
// build, hot reload, editable by the user's agent. gerdr's chat UI is one (packages/chat, @gerdr/chat): the same
// source is built and published to npm for other apps (Annulo), while gerdr imports it by name.
//
// `import "@gerdr/chat"` resolves to packages/chat/src/index.(ts|tsx); "@gerdr/chat/x" to packages/chat/src/x.
// Inside a package, imports are relative; third-party ones resolve through the UI's own package.json like
// everywhere else, so a package's dependencies must also be listed there.

func isWorkspaceDep(v string) bool { return strings.HasPrefix(strings.TrimSpace(v), "workspace:") }

// localPackages: package name → its directory (relative to the UI root, / separated), for the workspace
// dependencies in package.json that are found under packages/.
func localPackages(dir string) map[string]string {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	var p packageJSON
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	want := map[string]bool{}
	for name, v := range p.Dependencies {
		if isWorkspaceDep(v) {
			want[name] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	out := map[string]string{}
	ents, _ := os.ReadDir(filepath.Join(dir, "packages"))
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "packages", e.Name(), "package.json"))
		if err != nil {
			continue
		}
		var meta struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &meta) == nil && want[meta.Name] {
			out[meta.Name] = "packages/" + e.Name()
		}
	}
	return out
}

// localSpec maps a bare import of a workspace package to a root-relative path ("/packages/chat/src/index"),
// or "" when spec isn't one.
func localSpec(local map[string]string, spec string) string {
	pkg, sub := packageName(spec)
	d, ok := local[pkg]
	if !ok {
		return ""
	}
	if sub == "" {
		return "/" + path.Join(d, "src", "index")
	}
	return "/" + path.Join(d, "src", sub)
}

// localKey fingerprints the mapping: compiled modules are cached by their own content, and where a bare import
// resolves to is outside it.
func localKey(local map[string]string) string {
	if len(local) == 0 {
		return ""
	}
	var keys []string
	for k, v := range local {
		keys = append(keys, k+"="+v)
	}
	sort.Strings(keys)
	h := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return hex.EncodeToString(h[:8])
}
