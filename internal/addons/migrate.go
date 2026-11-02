package addons

import (
	"crypto/sha256"
	"fmt"
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
//   - a file older versions seeded there and nobody edited is dropped (see seeded)
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
			if seeded[fmt.Sprintf("%x", sha256.Sum256(b))] {
				continue
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

// seeded holds the sha256 of every plugin file older versions wrote into <root>/plugins on first start
// (duplicate-tab, tab-numbering, _session-title, _notify-when-done). A file still byte-identical to one of them is
// something gerdr put there, not something the user wrote, so it is not carried over: Gerdr ships no add-ons, and
// these live in the store now. An edited copy differs and is migrated like any other.
var seeded = map[string]bool{
	"73c80ddc23ac8684d0e3ef7b15a09626b927ae67deb90afd91fb86f2bec019c7": true, // duplicate-tab.ts
	"9a988f82fbfd978233099008c7d03a5a6316f8df688046c6caf832df4d646747": true, // _session-title.ts
	"10c273610946bbf2d87b4aff6df9662fb8df5a8a14e4f125b1393e2dbf86ee70": true, // tab-numbering.ts
	"e0a4e582cc465b33232a9421cb93207a28eb1057eba1aaf1053ad49427536bb1": true, // _notify-when-done.ts
}

var refRE = regexp.MustCompile(`(/// <reference path=["'])\./gerdr\.d\.ts`)
