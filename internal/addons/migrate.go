package addons

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Migrate moves the old plugin directory (<root>/plugins: a .ts file or a folder with plugin.json per plugin) to
// add-ons, once, before the first add-on directory exists:
//
//   - foo.ts → addons/foo/index.ts (its `/// <reference path="./gerdr.d.ts" />` pointed one level up)
//   - _foo.ts / _foo/ (switched off by name) → addons/foo, switched off in the state file
//   - foo/ with plugin.json → addons/foo with addon.json
//
// The old directory is left as plugins.migrated, and plugin-data / logs/plugins move to addon-data / logs/addons.
func Migrate(root string) error {
	old := filepath.Join(root, "plugins")
	dst := filepath.Join(root, "addons")
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	ents, err := os.ReadDir(old)
	if err != nil {
		return nil // nothing to migrate
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	m := New(root, nil)
	var off []string
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "gerdr.d.ts" || name == "tsconfig.json" || name == "README.md" {
			continue
		}
		src := filepath.Join(old, name)
		id, ext := name, ""
		if !e.IsDir() {
			ext = filepath.Ext(name)
			if (ext != ".ts" && ext != ".tsx" && ext != ".js") || strings.HasSuffix(name, ".d.ts") {
				continue
			}
			id = strings.TrimSuffix(name, ext)
		}
		disabled := strings.HasPrefix(id, "_")
		id = strings.TrimLeft(id, "_")
		if !validID(id) {
			continue
		}
		to := filepath.Join(dst, id)
		if _, err := os.Stat(to); err == nil {
			continue // foo.ts and _foo.ts both there: the first one wins
		}
		if e.IsDir() {
			if err := copyTree(src, to); err != nil {
				return err
			}
			if _, err := os.Stat(filepath.Join(to, "plugin.json")); err == nil {
				_ = os.Rename(filepath.Join(to, "plugin.json"), filepath.Join(to, ManifestFile))
			}
		} else {
			b, err := os.ReadFile(src)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(to, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(to, "index"+ext), refRE.ReplaceAll(b, []byte(`${1}../gerdr.d.ts`)), 0o644); err != nil {
				return err
			}
		}
		if disabled {
			off = append(off, id)
		}
	}
	if len(off) > 0 {
		if err := m.updateState(func(st *state) error {
			st.Disabled = append(st.Disabled, off...)
			return nil
		}); err != nil {
			return err
		}
	}
	_ = os.Rename(old, filepath.Join(root, "plugins.migrated"))
	_ = os.Rename(filepath.Join(root, "plugin-data"), filepath.Join(root, "addon-data"))
	_ = os.Rename(filepath.Join(root, "logs", "plugins"), filepath.Join(root, "logs", "addons"))
	return nil
}

var refRE = regexp.MustCompile(`(/// <reference path=["'])\./gerdr\.d\.ts`)
