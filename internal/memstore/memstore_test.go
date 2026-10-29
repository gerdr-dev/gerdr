package memstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func roots(t *testing.T) Roots {
	base := t.TempDir()
	r := Roots{Native: filepath.Join(base, "gerdr", "native", "memory"), Claude: filepath.Join(base, "claude", "projects"), Codex: filepath.Join(base, "codex", "memories")}
	for _, d := range []string{filepath.Join(r.Native, "global"), filepath.Join(r.Claude, "-w-app", "memory"), r.Codex} {
		os.MkdirAll(d, 0o755)
	}
	return r
}

func TestAllowed(t *testing.T) {
	r := roots(t)
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(r.Native, "global", "escape"))
	for p, ok := range map[string]bool{
		filepath.Join(r.Native, "global", "a.md"):                   true,
		filepath.Join(r.Native, "projects", "x", "new.md"):          true,
		filepath.Join(r.Claude, "-w-app", "memory", "a.md"):         true,
		filepath.Join(r.Claude, "-w-app", "session.jsonl"):          false,
		filepath.Join(r.Claude, "-w-app", "notes.md"):               false, // 不在 memory/ 下
		filepath.Join(r.Codex, "summary.md"):                        true,
		filepath.Join(r.Native, "global", "a.txt"):                  false,
		filepath.Join(r.Native, "global", "..", "..", "..", "x.md"): false,
		filepath.Join(r.Native, "global", "escape", "x.md"):         false, // 符号链接链到别处
		"relative/a.md": false,
	} {
		_, err := Allowed(r, p)
		if (err == nil) != ok {
			t.Errorf("Allowed(%s) = %v, want ok=%v", p, err, ok)
		}
	}
}

func TestListAndDelete(t *testing.T) {
	r := roots(t)
	g := filepath.Join(r.Native, "global")
	os.WriteFile(filepath.Join(g, "lang.md"), []byte("---\nname: lang\ndescription: reply in Chinese\ntype: user\n---\nbody"), 0o644)
	os.WriteFile(filepath.Join(g, "old.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(g, Index), []byte("- [Lang](lang.md) — zh\n- [Old](old.md) — stale\n"), 0o644)
	cm := filepath.Join(r.Claude, "-w-app", "memory")
	os.WriteFile(filepath.Join(cm, "x.md"), []byte("---\nname: x\ndescription: d\nmetadata:\n  type: project\n---\n"), 0o644)
	os.MkdirAll(filepath.Join(r.Claude, "-w-empty", "memory"), 0o755) // 空的不列

	ss := List(r, []string{"/w/app"}, nil)
	byAgent := map[string][]Store{}
	for _, s := range ss {
		byAgent[s.Agent] = append(byAgent[s.Agent], s)
	}
	if n := len(byAgent["native"]); n != 1 || len(byAgent["native"][0].Entries) != 2 || !byAgent["native"][0].Index {
		t.Fatalf("native stores: %+v", byAgent["native"])
	}
	if c := byAgent["claude"]; len(c) != 1 || c[0].Label != "/w/app" || c[0].Entries[0].Type != "project" {
		t.Fatalf("claude stores: %+v", c)
	}
	if len(byAgent["codex"]) != 1 {
		t.Fatal("codex store should be listed even when empty")
	}
	var lang Entry
	for _, e := range byAgent["native"][0].Entries {
		if e.File == "lang.md" {
			lang = e
		}
	}
	if lang.Description != "reply in Chinese" || lang.Type != "user" {
		t.Fatalf("frontmatter: %+v", lang)
	}

	if err := Delete(r, filepath.Join(g, "old.md")); err != nil {
		t.Fatal(err)
	}
	idx, _ := os.ReadFile(filepath.Join(g, Index))
	if strings.Contains(string(idx), "old.md") || !strings.Contains(string(idx), "lang.md") {
		t.Fatalf("index after delete: %q", idx)
	}
	if err := Delete(r, filepath.Join(g, Index)); err == nil {
		t.Fatal("deleting the index should be refused")
	}
}
