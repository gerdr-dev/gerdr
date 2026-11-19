package addons

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckManifestFindsSilentMistakes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "icon.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	mf := `{
  "name": "x", "icon": "icon.svg", "screenshots": ["shot.png"], "descripton": "typo",
  "settings": [
    {"key": "provider", "title": "P", "type": "select", "option": [{"value": "a"}]},
    {"key": "mode", "type": "select", "default": "c", "options": [{"value": "a"}, {"value": "b"}, {"value": "a"}]},
    {"key": "token", "type": "password"},
    {"key": "ttl", "type": "number", "default": "60", "min": 10, "max": 5},
    {"key": "on", "type": "boolean", "default": "true", "unit": "s"},
    {"key": "tint", "type": "color", "default": "red"},
    {"key": "open", "type": "link", "url": "file:///etc", "default": 1},
    {"key": "ttl", "type": "string", "defualt": ""},
    {"title": "no key", "type": "string"}
  ],
  "i18n": {"en": {"settings": {
    "mode": {"options": {"a": "A", "z": "Z"}},
    "on": {"unit": "sec"},
    "provder": {"title": "Provider"}
  }}}
}`
	got := map[string]bool{}
	for _, p := range CheckManifest([]byte(mf), dir) {
		got[p.Path+": "+p.Problem] = true
	}
	want := []string{
		`: unknown field "descripton": it is ignored (did you mean "description"?)`,
		`screenshots[0]: no file "shot.png"`,
		`settings[0] (provider): unknown field "option": it is ignored (did you mean "options"?)`,
		`settings[0] (provider): a select needs options`,
		`settings[1] (mode).options[2]: value "a" is there twice`,
		`settings[1] (mode).default: "c" is not one of the options' values`,
		`settings[2] (token).type: "password" is not a setting type`,
		`settings[3] (ttl).default: should be a number, not "60"`,
		`settings[3] (ttl): min is above max`,
		`settings[4] (on).default: should be true or false`,
		`settings[4] (on).unit: only a number uses unit`,
		`settings[5] (tint).default: should be a "#rrggbb" string`,
		`settings[6] (open).url: only http(s) addresses open`,
		`settings[6] (open).default: a link has no value`,
		`settings[7] (ttl): key "ttl" is used by an earlier setting too`,
		`settings[7] (ttl): unknown field "defualt": it is ignored (did you mean "default"?)`,
		`settings[8]: has no key`,
		`i18n.en.settings.mode.options.z: "z" is not one of the option values`,
		`i18n.en.settings.on.unit: only a number shows a unit`,
		`i18n.en.settings.provder: there is no setting "provder" (did you mean "provider"?)`,
	}
	for _, w := range want {
		found := false
		for g := range got {
			if strings.HasPrefix(g, w) {
				found, got[g] = true, false
				break
			}
		}
		if !found {
			t.Errorf("missing problem %q", w)
		}
	}
	for g, unclaimed := range got {
		if unclaimed {
			t.Errorf("unexpected problem %q", g)
		}
	}
}

func TestCheckManifestCleanAndBroken(t *testing.T) {
	clean := `{"name": "ok", "settings": [
    {"key": "style", "type": "select", "default": "", "options": [{"value": "dot"}]},
    {"key": "chat", "type": "select", "required": true, "optionsFrom": {"events": "*/dm", "reply": true}},
    {"key": "n", "type": "number", "default": 5, "min": 1, "max": 10, "unit": "min"},
    {"key": "c", "type": "color", "default": "#e5569b"},
    {"key": "i", "type": "icon", "default": "Coffee"},
    {"key": "go", "type": "action", "label": "Run"}
  ], "service": {"command": "./bin/x", "pending": true}, "i18n": {"en": {"settings": {"go": {"label": "Run"}}}}}`
	if ps := CheckManifest([]byte(clean), ""); len(ps) != 0 {
		t.Fatalf("clean manifest: %v", ps)
	}
	ps := CheckManifest([]byte(`{"name": "x",}`), "")
	if len(ps) != 1 || !strings.HasPrefix(ps[0].Problem, "not valid JSON") {
		t.Fatalf("broken JSON: %v", ps)
	}
	ps = CheckManifest([]byte(`{"name": 3, "settings": [{"key": "a", "type": "strin"}]}`), "")
	if len(ps) != 2 || ps[0].Path != "name" || !strings.Contains(ps[1].Problem, `did you mean "string"?`) {
		t.Fatalf("type error then the rest: %v", ps)
	}
}

func TestCheckFolders(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a", "group/b", "group/deep/c", ".hidden/d"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, d, ManifestFile), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := CheckFolders(root)
	if len(got) != 2 || filepath.Base(got[0]) != "a" || filepath.Base(got[1]) != "b" {
		t.Fatalf("folders: %v", got)
	}
	if got := CheckFolders(filepath.Join(root, "a")); len(got) != 1 {
		t.Fatalf("a folder with addon.json is itself: %v", got)
	}
}
