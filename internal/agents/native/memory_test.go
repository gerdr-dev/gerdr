package native

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

func TestMemoryForSharesRepo(t *testing.T) {
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	os.MkdirAll(filepath.Join(repo, "sub", "dir"), 0o755)
	a, b := memoryFor("/x", repo), memoryFor("/x", filepath.Join(repo, "sub", "dir"))
	if a.Project == "" || a.Project != b.Project {
		t.Fatalf("subdirectory should share the repo's memory: %q vs %q", a.Project, b.Project)
	}
	if a.Global != "/x/memory/global" {
		t.Fatalf("global %q", a.Global)
	}
	if !a.inMemory(filepath.Join(a.Project, "x.md")) || a.inMemory(filepath.Join(a.Project, "x.sh")) || a.inMemory(filepath.Join(repo, "x.md")) {
		t.Fatal("inMemory wrong")
	}
}

func TestMemoryInPromptAndNoApproval(t *testing.T) {
	var mem memoryDirs
	e := setup(t, func(n int, msgs []any) reply {
		switch n {
		case 0:
			p := filepath.Join(mem.Project, "prefers-tabs.md")
			w, _ := json.Marshal(map[string]string{"path": p, "content": "---\nname: prefers-tabs\ndescription: x\ntype: feedback\n---\nUse tabs."})
			idx, _ := json.Marshal(map[string]string{"path": filepath.Join(mem.Project, "MEMORY.md"), "content": "- [Prefers tabs](prefers-tabs.md) — use tabs\n"})
			rm := fmt.Sprintf(`{"command":"rm -f %s"}`, filepath.Join(mem.Global, "old.md"))
			return reply{calls: [][2]string{{"write", string(w)}, {"write", string(idx)}, {"bash", rm}}}
		}
		return reply{text: "remembered"}
	})
	mem = memoryFor(e.ag.st.dir, e.cwd)
	os.MkdirAll(mem.Global, 0o755)
	os.WriteFile(filepath.Join(mem.Global, "MEMORY.md"), []byte("- [Speaks Chinese](lang.md) — reply in Chinese\n"), 0o644)
	os.WriteFile(filepath.Join(mem.Global, "old.md"), []byte("stale"), 0o644)
	asked := 0
	e.h.perm = func(r acp.RequestPermissionRequest) string { asked++; return "reject_once" }
	sid := e.newSession(modeDefault)
	if _, err := e.prompt(sid, uuid.NewString(), "remember I prefer tabs"); err != nil {
		t.Fatal(err)
	}
	sys := e.llm.request(0)["messages"].([]any)[0].(map[string]any)["content"].(string)
	for _, want := range []string{"reply in Chinese", mem.Project, `You are the model "fake-model"`} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	if asked != 0 {
		t.Fatalf("memory writes should not ask for approval (asked %d)", asked)
	}
	if b, _ := os.ReadFile(filepath.Join(mem.Project, "prefers-tabs.md")); !strings.Contains(string(b), "Use tabs.") {
		t.Fatal("memory not written")
	}
	if _, err := os.Stat(filepath.Join(mem.Global, "old.md")); err == nil {
		t.Fatal("rm of a memory file should run without approval")
	}
	// 下一轮的提示词里有新的项目索引
	e.prompt(sid, uuid.NewString(), "again")
	sys = e.llm.request(-1)["messages"].([]any)[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "Prefers tabs") {
		t.Fatal("new project index not in the next turn's prompt")
	}
	// 卡片标题
	if st := e.h.toolStatus("call_0_0"); st != acp.ToolCallStatusCompleted {
		t.Fatalf("status %v", st)
	}
}

func TestMemoryRmGuard(t *testing.T) {
	m := memoryDirs{Global: "/m/global", Project: "/m/projects/p"}
	for cmd, want := range map[string]bool{
		"rm /m/global/a.md":                       true,
		"rm -f /m/projects/p/a.md /m/global/b.md": true,
		"rm -rf /m/global":                        false,
		"rm /m/global/*.md":                       false,
		"rm /m/global/a.md && rm ~/x":             false,
		"rm /etc/passwd.md":                       false,
		"rm /m/global/../../etc/x.md":             false,
	} {
		if got := memoryRm(cmd, "/w", m); got != want {
			t.Errorf("memoryRm(%q) = %v, want %v", cmd, got, want)
		}
	}
}
