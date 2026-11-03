package uisite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, s := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A mounted UI compiles into the host's graph: its own files under @m/<name>/, @gerdr/core and the host's workspace
// packages resolving to the host's files, its packages in the one import map.
func TestMountSharesTheHostGraph(t *testing.T) {
	host, work := t.TempDir(), t.TempDir()
	writeFiles(t, host, map[string]string{
		"package.json":               `{"dependencies":{"react":"19.3.1","react-dom":"19.3.1","react-router":"7.18.5","react-refresh":"0.19.0","@gerdr/chat":"workspace:*"}}`,
		"packages/chat/package.json": `{"name":"@gerdr/chat"}`,
		"packages/chat/src/index.ts": `export const chat = 1;`,
		"src/index.css":              `:root { --a: 1; }`,
		"src/core/index.ts":          `export const core = 1;`,
		"src/pages/index.tsx":        `export default function P() { return null }`,
	})
	writeFiles(t, work, map[string]string{
		"package.json":        `{"dependencies":{"react":"18.0.0","clsx":"2.1.1"}}`,
		"ui.json":             `{"title":"Board","chat":false}`,
		"src/index.css":       ":root { --background: red; }\n.dark { --background: black; }\n.okr { color: red; }\n@media (max-width: 700px) { body .x { color: blue; } }\n@theme inline { --color-x: var(--x); }",
		"src/lib/x.ts":        `export const x = 1; export const p = __GERDR_PROJECT__; export const q = decodeURIComponent(location.pathname.split("/")[2] || "default");`,
		"src/layout.tsx":      `import { Outlet } from "react-router"; export default function L() { return <Outlet /> }`,
		"src/pages/index.tsx": `import clsx from "clsx"; import { core } from "@gerdr/core"; import { chat } from "@gerdr/chat"; import { x } from "@/lib/x"; export default function P() { return clsx(core, chat, x) }`,
		"src/pages/board.tsx": `import { x } from "../lib/x"; export default function B() { return x }`,
		"src/cards.tsx":       `export function TaskCard() { return null }`,
	})
	r := New(host, t.TempDir(), nil)
	r.Mount("lb", work)
	r.Mount("lb~看板", work) // a panel with a non-ASCII name: its local imports must still be linked
	b := r.ensure()
	if b.fatal != "" {
		t.Fatalf("host fatal: %s", b.fatal)
	}
	if code := b.served["@m/lb~看板/src/pages/board.tsx"].code; strings.Contains(code, depPlaceholder) || !strings.Contains(code, "/_ui/m/@m/lb~看板/src/lib/x.ts?v=") {
		t.Errorf("non-ASCII mount not linked:\n%s", code)
	}
	if code := b.served["@m/lb~看板/src/lib/x.ts"].code; !strings.Contains(code, `p = "lb/看板"`) {
		t.Errorf("a panel's __GERDR_PROJECT__ is its key:\n%s", code)
	}
	mi := b.mounts["lb"]
	if mi.Error != "" || mi.Entry == "" || mi.Cards == "" {
		t.Fatalf("mount: %+v", mi)
	}
	if mi.Config["chat"] != false {
		t.Errorf("config %v", mi.Config)
	}
	for _, p := range []string{"@m/lb/src/pages/index.tsx", "@m/lb/src/lib/x.ts", "@m/lb/src/layout.tsx", "src/core/index.ts", "packages/chat/src/index.ts", "@m/lb/@entry.js"} {
		if _, ok := b.served[p]; !ok {
			t.Errorf("%s not served", p)
		}
	}
	if code := b.served["@m/lb/src/pages/index.tsx"].code; !strings.Contains(code, "/_ui/m/src/core/index.ts?v=") || !strings.Contains(code, "/_ui/m/@m/lb/src/lib/x.ts?v=") {
		t.Errorf("links:\n%s", code)
	}
	if code := b.served["@m/lb/src/lib/x.ts"].code; !strings.Contains(code, `p = "lb"`) || !strings.Contains(code, `q = "lb"`) {
		t.Errorf("__GERDR_PROJECT__ not defined:\n%s", code)
	}
	if !strings.Contains(b.importMap["react"], "react@19.3.1") {
		t.Errorf("react should be the host's: %s", b.importMap["react"])
	}
	if !strings.Contains(b.importMap["clsx"], "clsx@2.1.1") {
		t.Errorf("clsx from the mount's package.json: %v", b.importMap)
	}
	if m := b.manifest["@m/lb/src/pages/index.tsx"]; !m.Lazy {
		t.Error("a mount's modules are loaded when it's shown, not preloaded")
	}
	sel := `[data-gerdr-mount="lb"]`
	for _, want := range []string{"@scope (" + sel + ")", ":scope {", ":where(.dark) :scope {", "@theme inline {", "@media (max-width: 700px) {\n@scope"} {
		if !strings.Contains(b.css, want) {
			t.Errorf("css lacks %q:\n%s", want, b.css)
		}
	}
	if strings.Contains(b.css, "@scope ("+sel+") {\n@theme") {
		t.Error("@theme must stay global")
	}
}

// A mount's broken file is the mount's error: the host page builds and isn't overlaid.
func TestMountErrorStaysInTheMount(t *testing.T) {
	host, work := t.TempDir(), t.TempDir()
	writeFiles(t, host, map[string]string{
		"package.json":        `{"dependencies":{"react":"19.3.1","react-dom":"19.3.1","react-router":"7.18.5","react-refresh":"0.19.0"}}`,
		"src/pages/index.tsx": `export default function P() { return null }`,
	})
	writeFiles(t, work, map[string]string{
		"package.json":        `{"dependencies":{}}`,
		"src/pages/index.tsx": `import x from "left-pad"; export default function P() { return <div> }`,
	})
	r := New(host, t.TempDir(), nil)
	r.Mount("lb", work)
	b := r.ensure()
	if b.fatal != "" {
		t.Fatalf("host fatal: %s", b.fatal)
	}
	if b.mounts["lb"].Error == "" || b.mounts["lb"].Entry != "" {
		t.Fatalf("mount: %+v", b.mounts["lb"])
	}
	// Fixing it: a hot update that remounts only the mount
	writeFiles(t, work, map[string]string{"src/pages/index.tsx": `export default function P() { return null }`})
	r.mu.Lock()
	r.cur.sig = ""
	r.mu.Unlock()
	prev := r.cur
	next := r.ensure()
	u := diff(prev, next)
	if u == nil || u.Type != "update" || u.Boundaries == nil || len(u.Remount) != 1 || u.Remount[0] != "lb" {
		t.Fatalf("update %+v", u)
	}
	if next.mounts["lb"].Entry == "" {
		t.Fatalf("fixed mount has no entry: %+v", next.mounts["lb"])
	}
}

func TestScopeSelectors(t *testing.T) {
	for in, want := range map[string]string{
		":root":            ":scope",
		"html body .x":     ":scope body .x",
		".dark .card, a":   ":where(.dark) :scope .card, a",
		"[data-x='a,b'] p": "[data-x='a,b'] p",
	} {
		if got := scopeSelectors(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
