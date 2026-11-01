package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The lines are shaped like a real transcript (2.1.x) around a Monitor waking the agent.
func TestLastTaskNotification(t *testing.T) {
	now := time.Now().UTC()
	ts := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }
	note := `{"type":"user","uuid":"%s","timestamp":"%s","origin":{"kind":"task-notification","producer":"session-task"},"message":{"role":"user","content":"<task-notification>\n<task-id>b1c9</task-id>\n<summary>Monitor event: \"suite\"</summary>\n<event>== fling.mjs\nok</event>\n</task-notification>"}}`
	toolResult := `{"type":"user","uuid":"r1","timestamp":"` + ts(-time.Second) + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"hi"}]}}`
	dir := t.TempDir()
	write := func(lines ...string) string {
		p := filepath.Join(dir, "s.jsonl")
		s := ""
		for _, l := range lines {
			s += l + "\n"
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	since := now.Add(-10 * time.Second)

	uuid, text := lastTaskNotification(write(toolResult, fmt.Sprintf(note, "n1", ts(0))), since)
	if uuid != "n1" || wakeEventRe.FindStringSubmatch(text)[1] != "== fling.mjs\nok" || wakeTaskRe.FindStringSubmatch(text)[1] != "b1c9" {
		t.Fatalf("fresh notification: %q %q", uuid, text)
	}
	// Something said after it: it woke an earlier turn, not this one.
	if uuid, _ := lastTaskNotification(write(fmt.Sprintf(note, "n1", ts(-2*time.Second)), toolResult), since); uuid != "" {
		t.Fatalf("followed by a tool result: %q", uuid)
	}
	// Too old: this turn's notification isn't written yet.
	if uuid, _ := lastTaskNotification(write(fmt.Sprintf(note, "n1", ts(-time.Minute))), since); uuid != "" {
		t.Fatalf("stale: %q", uuid)
	}
}
