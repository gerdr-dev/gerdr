package uisite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspacePackage(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	write("package.json", `{"dependencies":{"react":"19.3.1","@gerdr/chat":"workspace:*"}}`)
	write("packages/chat/package.json", `{"name":"@gerdr/chat"}`)
	write("packages/chat/src/index.ts", `export { Chat } from "./Chat";`)
	write("packages/chat/src/Chat.tsx", `import { useState } from "react"; export function Chat() { useState(0); return null }`)
	write("packages/chat/src/extra.ts", `export const x = 1;`)
	write("src/pages/index.tsx", `import { Chat } from "@gerdr/chat"; import { x } from "@gerdr/chat/extra"; export default function P() { return x ? <Chat /> : null }`)

	local := localPackages(dir)
	if local["@gerdr/chat"] != "packages/chat" {
		t.Fatalf("local %v", local)
	}
	var c moduleCache
	c.dir = t.TempDir()
	g := c.buildGraph(dir, []string{"src/pages/index.tsx"})
	if len(g.errs) > 0 {
		t.Fatal(g.errs)
	}
	for _, want := range []string{"packages/chat/src/index.ts", "packages/chat/src/Chat.tsx", "packages/chat/src/extra.ts"} {
		if g.mods[want] == nil {
			t.Errorf("%s not in the graph: %v", want, keys(g.mods))
		}
	}
	deps, _ := readPackageJSON(dir)
	if _, ok := deps["@gerdr/chat"]; ok {
		t.Error("a workspace package must not go to esm.sh")
	}
	if b := g.mods["packages/chat/src/Chat.tsx"].Bare; strings.Join(b, ",") != "react" {
		t.Errorf("bare imports inside the package: %v", b)
	}
}

func keys(m map[string]*compiled) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
