package addons

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFolderAddon(t *testing.T) {
	root := t.TempDir()
	m := New(root, slog.Default())
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(m.Dir(), "hello")
	write(t, filepath.Join(d, "addon.json"), `{"name":"Hello","version":"1.0.0","icon":"icon.svg"}`)
	write(t, filepath.Join(d, "index.ts"), `/// <reference path="../gerdr.d.ts" />
import { greet } from "./lib";
export default defineAddon({ setup(g) { g.notify(greet(g.asset("icon.svg"))); } });`)
	write(t, filepath.Join(d, "lib.ts"), `export const greet = (s: string): string => "hi " + s;`)
	write(t, filepath.Join(m.Dir(), "loose.ts"), `export default 1;`) // top-level files aren't add-ons any more
	list := m.List()
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	a := list[0]
	if a.Error != "" || !strings.Contains(a.Code, "hi ") || a.Name != "Hello" || a.Version != "1.0.0" || a.Icon != "icon.svg" {
		t.Fatalf("addon = %+v", a)
	}
	if p, err := m.File("hello", "icon.svg"); err != nil || p != filepath.Join(d, "icon.svg") {
		t.Fatal(p, err)
	}
	for _, bad := range []string{"../x", "/etc/passwd", "a/../../x"} {
		if _, err := m.File("hello", bad); err == nil {
			t.Fatalf("File(%q) allowed", bad)
		}
	}

	// Switched off: still listed, nothing built, hash changes (the UI unloads it)
	if err := m.SetEnabled("hello", false); err != nil {
		t.Fatal(err)
	}
	off := m.List()[0]
	if !off.Disabled || off.Code != "" || off.Hash == a.Hash {
		t.Fatalf("off = %+v", off)
	}
}

func TestTypeDeclarationsCompile(t *testing.T) {
	b, err := builtin.ReadFile("builtin/gerdr.d.ts")
	if err != nil || !strings.Contains(string(b), "defineAddon") {
		t.Fatal("gerdr.d.ts missing defineAddon", err)
	}
}

func TestMigrate(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "plugins")
	write(t, filepath.Join(old, "gerdr.d.ts"), "// old")
	write(t, filepath.Join(old, "duplicate-tab.ts"), `/// <reference path="./gerdr.d.ts" />
export default definePlugin({ setup() {} });`)
	write(t, filepath.Join(old, "_notify.ts"), `export default definePlugin({ setup() {} });`)
	// What an old gerdr seeded, untouched: not carried over (renamed, as a user switching it on/off would)
	seed, err := os.ReadFile(filepath.Join("testdata", "seeded-duplicate-tab.ts"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(old, "_dup.ts"), string(seed))
	write(t, filepath.Join(old, "band", "plugin.json"), `{"service":{"command":"true"}}`)
	write(t, filepath.Join(root, "plugin-data", "band", "x"), "kept")
	if err := Migrate(root); err != nil {
		t.Fatal(err)
	}
	m := New(root, slog.Default())
	_ = m.Install()
	got := map[string]*Addon{}
	for _, a := range m.List() {
		got[a.ID] = a
	}
	if a := got["duplicate-tab"]; a == nil || a.Error != "" || a.Code == "" {
		t.Fatalf("duplicate-tab = %+v", a)
	}
	if b, _ := os.ReadFile(filepath.Join(m.Dir(), "duplicate-tab", "index.ts")); !strings.Contains(string(b), `path="../gerdr.d.ts"`) {
		t.Fatalf("reference not rewritten: %s", b)
	}
	if a := got["notify"]; a == nil || !a.Disabled {
		t.Fatalf("notify = %+v", a)
	}
	if a := got["dup"]; a != nil {
		t.Fatalf("seeded plugin migrated: %+v", a)
	}
	if a := got["band"]; a == nil || !a.HasService {
		t.Fatalf("band = %+v", a)
	}
	if _, err := os.Stat(filepath.Join(root, "addon-data", "band", "x")); err != nil {
		t.Fatal("data not moved")
	}
	if _, err := os.Stat(filepath.Join(root, "plugins.migrated")); err != nil {
		t.Fatal("old dir not kept")
	}
	// Once: a second run changes nothing
	if err := Migrate(root); err != nil {
		t.Fatal(err)
	}
}

func TestLocalStore(t *testing.T) {
	root := t.TempDir()
	store := t.TempDir()
	write(t, filepath.Join(store, "README.md"), "# store")
	write(t, filepath.Join(store, "tabs", "addon.json"), `{"name":"Tabs","version":"0.1.0","description":"d"}`)
	write(t, filepath.Join(store, "tabs", "index.ts"), `export default defineAddon({ setup() {} });`)
	write(t, filepath.Join(store, "tabs", "README.md"), "# Tabs")
	write(t, filepath.Join(store, "addons", "deep", "addon.json"), `{}`)
	write(t, filepath.Join(store, "addons", "deep", "index.ts"), `export default defineAddon({ setup() {} });`)
	write(t, filepath.Join(store, "notanaddon", "x.ts"), ``)

	m := New(root, slog.Default())
	ctx := context.Background()
	src, err := m.AddStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	var view StoreView
	for _, v := range m.Stores() {
		if v.Source == src {
			view = v
		}
	}
	if len(m.Stores()) != 2 || !view.Local || len(view.Addons) != 2 || view.Addons[0].ID != "deep" || view.Addons[1].Readme != "# Tabs" {
		t.Fatalf("stores = %+v", m.Stores())
	}

	if err := m.InstallFromStore(src, "tabs", false); err != nil {
		t.Fatal(err)
	}
	a := m.List()[0]
	if a.ID != "tabs" || a.Source == nil || a.Source.Store != src || a.Modified || a.Code == "" {
		t.Fatalf("installed = %+v", a)
	}
	// Updating an unchanged copy just works; a changed one needs force
	write(t, filepath.Join(store, "tabs", "index.ts"), `export default defineAddon({ setup() { console.log(2) } });`)
	if err := m.InstallFromStore(src, "tabs", false); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.Dir(), "tabs", "mine.ts"), "// local edit")
	if !m.List()[0].Modified {
		t.Fatal("local edit not noticed")
	}
	if err := m.InstallFromStore(src, "tabs", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
	if err := m.InstallFromStore(src, "tabs", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.Dir(), "tabs", "mine.ts")); err == nil {
		t.Fatal("forced install kept the old folder's files")
	}
	// A folder someone made by hand is never overwritten unasked
	write(t, filepath.Join(m.Dir(), "deep", "index.ts"), `export default defineAddon({ setup() {} });`)
	if err := m.InstallFromStore(src, "deep", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}

	if err := m.Uninstall("tabs"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.readState().Installed["tabs"]; ok {
		t.Fatal("install record left behind")
	}
	if err := m.RemoveStore(src); err != nil || len(m.Stores()) != 1 {
		t.Fatal(err, m.Stores())
	}
	if err := m.RemoveStore(DefaultStore); err != nil || len(m.Stores()) != 0 {
		t.Fatal("removing the default store didn't stick", err)
	}
}

func TestNormalizeSource(t *testing.T) {
	for in, want := range map[string]string{
		"gerdr-dev/gerdr-addon":                    "gerdr-dev/gerdr-addon",
		"https://github.com/gerdr-dev/gerdr-addon": "gerdr-dev/gerdr-addon",
		"github.com/a/b.git":                       "a/b",
		"a/b#v1.2":                                 "a/b#v1.2",
	} {
		if got, err := NormalizeSource(in); err != nil || got != want {
			t.Errorf("%q → %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "a", "a/b/c", "/no/such/dir"} {
		if _, err := NormalizeSource(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSettings(t *testing.T) {
	root := t.TempDir()
	m := New(root, slog.Default())
	d := filepath.Join(m.Dir(), "clock")
	write(t, filepath.Join(d, "addon.json"), `{"settings":[{"key":"ttl","type":"number","default":60,"min":1,"max":1440},{"key":"preview","type":"action","default":"x"}]}`)
	write(t, filepath.Join(d, "index.ts"), `export default defineAddon({ setup() {} });`)
	if err := m.SetSettings("clock", map[string]any{"ttl": 30.0}); err != nil {
		t.Fatal(err)
	}
	a := m.List()[0]
	if len(a.Settings) != 2 || a.SettingValues["ttl"] != 30.0 {
		t.Fatalf("addon = %+v", a)
	}
	if err := m.SetSettings("clock", map[string]any{"ttl": 0.0}); err == nil {
		t.Fatal("out of range accepted")
	}
	if err := m.SetSettings("clock", map[string]any{"nope": 1.0}); err == nil {
		t.Fatal("unknown key accepted")
	}
	// An "action" is a button: no value to set, and its default never reaches the add-on's values
	if err := m.SetSettings("clock", map[string]any{"preview": "y"}); err == nil {
		t.Fatal("value for an action accepted")
	}
	if v, ok := m.SettingsOf("clock")["preview"]; ok {
		t.Fatalf("action has a value: %v", v)
	}
	if err := m.SetSettings("clock", map[string]any{"ttl": nil}); err != nil || m.List()[0].SettingValues != nil {
		t.Fatal("reset to default failed", err)
	}
}

// A select whose options are the chat add-ons installed: filled in from their "emits", never empty when required,
// and back to the first one when the picked add-on is removed.
func TestSettingsOptionsFrom(t *testing.T) {
	root := t.TempDir()
	m := New(root, slog.Default())
	write(t, filepath.Join(m.Dir(), "ask", "addon.json"), `{"settings":[{"key":"chat","type":"select","required":true,"optionsFrom":{"events":"*/dm","reply":true}}]}`)
	write(t, filepath.Join(m.Dir(), "ask", "index.ts"), `export default defineAddon({ setup() {} });`)
	opts := func() ([]SettingOption, any) {
		for _, a := range m.Describe() {
			if a.ID == "ask" {
				return a.Settings[0].Options, m.SettingsOf("ask")["chat"]
			}
		}
		t.Fatal("ask not listed")
		return nil, nil
	}
	if o, v := opts(); len(o) != 0 || v != nil {
		t.Fatalf("no chat add-on: options %v, value %v", o, v)
	}
	write(t, filepath.Join(m.Dir(), "slack", "addon.json"), `{"name":"Slack","emits":[{"kind":"dm","reply":"slack.send"},{"kind":"mention","reply":"slack.send"}]}`)
	write(t, filepath.Join(m.Dir(), "feishu", "addon.json"), `{"name":"飞书","emits":[{"kind":"dm","reply":"feishu.send"}]}`)
	write(t, filepath.Join(m.Dir(), "gitlab", "addon.json"), `{"emits":[{"kind":"dm"}]}`) // can't be answered: left out
	o, v := opts()
	if len(o) != 2 || v != o[0].Value {
		t.Fatalf("options %+v, value %v", o, v)
	}
	labels := map[any]string{o[0].Value: o[0].Label, o[1].Value: o[1].Label}
	if labels["slack"] != "Slack" || labels["feishu"] != "飞书" {
		t.Fatalf("labels %v", labels)
	}
	if err := m.SetSettings("ask", map[string]any{"chat": "gitlab"}); err == nil {
		t.Fatal("a value that isn't an option accepted")
	}
	if err := m.SetSettings("ask", map[string]any{"chat": "feishu"}); err != nil {
		t.Fatal(err)
	}
	if _, v := opts(); v != "feishu" {
		t.Fatalf("picked feishu, got %v", v)
	}
	// Its add-on switched off: the first remaining option, without forgetting the pick
	if err := m.SetEnabled("feishu", false); err != nil {
		t.Fatal(err)
	}
	if _, v := opts(); v != "slack" {
		t.Fatalf("feishu off: got %v", v)
	}
	if err := m.SetEnabled("feishu", true); err != nil {
		t.Fatal(err)
	}
	if _, v := opts(); v != "feishu" {
		t.Fatalf("feishu back: got %v", v)
	}
}

// addon.json's i18n reaches both listings untouched (the UI picks the language), and README.<locale>.md files are
// read for the store by locale, next to README.md.
func TestI18n(t *testing.T) {
	root := t.TempDir()
	m := New(root, slog.Default())
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"Pixel","settings":[{"key":"size","type":"select","options":[{"value":"s","label":"Small"}]}],
"i18n":{"zh":{"name":"像素","settings":{"size":{"title":"尺寸","options":{"s":"小"}}}}}}`
	d := filepath.Join(m.Dir(), "pixel")
	write(t, filepath.Join(d, "addon.json"), manifest)
	write(t, filepath.Join(d, "index.ts"), `export default defineAddon({ setup() {} });`)
	a := m.List()[0]
	if a.Name != "Pixel" || a.I18n["zh"].Name != "像素" || a.I18n["zh"].Settings["size"].Options["s"] != "小" {
		t.Fatalf("installed i18n = %+v", a.I18n)
	}

	store := filepath.Join(root, "store", "pixel")
	write(t, filepath.Join(store, "addon.json"), manifest)
	write(t, filepath.Join(store, "README.md"), "base")
	write(t, filepath.Join(store, "README.zh.md"), "中文")
	write(t, filepath.Join(store, "README.zh-CN.md"), "简体")
	write(t, filepath.Join(store, "README.old.md"), "not a locale")
	list := scanStore(filepath.Join(root, "store"))
	if len(list) != 1 {
		t.Fatalf("store = %+v", list)
	}
	s := list[0]
	if s.Readme != "base" || s.I18n["zh"].Name != "像素" || len(s.Readmes) != 2 || s.Readmes["zh"] != "中文" || s.Readmes["zh-CN"] != "简体" {
		t.Fatalf("store addon = %+v", s)
	}
}
