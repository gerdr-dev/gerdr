package hub

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAgyActivityReadsLocalWALIndex(t *testing.T) {
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 unavailable")
	}
	home := t.TempDir()
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	const parent = "3125d54f-32ee-42e8-9f34-90297249a37b"
	query := "PRAGMA journal_mode=WAL;" +
		"CREATE TABLE conversation_summaries (conversation_id TEXT, parent_conversation_id TEXT, killed INTEGER, not_fully_idle INTEGER);" +
		"INSERT INTO conversation_summaries VALUES ('child', '" + parent + "', 0, 1);"
	if out, err := exec.Command(sqlite, "-init", os.DevNull, filepath.Join(root, "conversation_summaries.db"), query).CombinedOutput(); err != nil {
		t.Fatalf("prepare index: %v: %s", err, out)
	}
	probe := agyActivityProbe(AgentDef{Env: map[string]string{"HOME": home}}, nil)
	if !probe(parent) || probe("64a3f97c-73f9-4da4-8d2e-dde4bffce632") {
		t.Fatal("index did not isolate the parent with active work")
	}
}

func TestAgyChildShellWhileIndexIsIdle(t *testing.T) {
	brain := t.TempDir()
	const parent = "3125d54f-32ee-42e8-9f34-90297249a37b"
	const child = "64a3f97c-73f9-4da4-8d2e-dde4bffce632"
	children := filepath.Join(brain, parent, ".system_generated", "subagents")
	data := filepath.Join(brain, child, ".system_generated")
	logs := filepath.Join(data, "logs")
	step := filepath.Join(data, "steps", "4")
	for _, dir := range []string{children, logs, step} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(children, child+".json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"step_index":4,"status":"RUNNING","created_at":%q,"content":"Tool is running as a background task with task id: %s/task-1"}`, time.Now().Format(time.RFC3339), child)
	if err := os.WriteFile(filepath.Join(logs, "transcript.jsonl"), []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !agyChildHasPendingShell(brain, parent) {
		t.Fatal("missed the shell still running in the child")
	}
	if err := os.WriteFile(filepath.Join(step, "output.txt"), []byte("Command exited with code 0."), 0600); err != nil {
		t.Fatal(err)
	}
	if agyChildHasPendingShell(brain, parent) {
		t.Fatal("a completed shell left the child marked running")
	}
}
