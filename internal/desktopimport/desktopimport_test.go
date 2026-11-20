package desktopimport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScan(t *testing.T) {
	dir, projects, proj := t.TempDir(), t.TempDir(), t.TempDir()
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scratch := filepath.Join(dir, "scratch-workspaces", "acc", "org", "scratch-1")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := filepath.Join(dir, "claude-code-sessions", "acc", "org")
	write(filepath.Join(sess, "local_a.json"), `{"cliSessionId":"aaaaaaaa-0000-0000-0000-000000000001","cwd":"`+proj+`","title":" Project one ","lastActivityAt":2000,"model":"claude-opus-5-5"}`)
	write(filepath.Join(sess, "local_b.json"), `{"cliSessionId":"aaaaaaaa-0000-0000-0000-000000000002","cwd":"`+scratch+`","title":"Scratch","createdAt":3000,"isArchived":true}`)
	write(filepath.Join(sess, "local_c.json"), `{"cliSessionId":"aaaaaaaa-0000-0000-0000-000000000003","cwd":"/nope/gone","title":"Gone","lastActivityAt":1000}`)
	write(filepath.Join(sess, "local_d.json"), `{"cliSessionId":"../evil","cwd":"/"}`)
	write(filepath.Join(sess, "scheduled-tasks.json"), `[]`)
	for _, id := range []string{"aaaaaaaa-0000-0000-0000-000000000001", "aaaaaaaa-0000-0000-0000-000000000002", "aaaaaaaa-0000-0000-0000-000000000003"} {
		write(filepath.Join(projects, "-x", id+".jsonl"), "{}\n")
	}
	got, err := Scan(dir, projects)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3: %+v", len(got), got)
	}
	// newest first
	if got[0].Title != "Scratch" || !got[0].Scratch || !got[0].Archived || got[0].Missing != "" {
		t.Errorf("scratch one: %+v", got[0])
	}
	if got[1].Title != "Project one" || got[1].Scratch || got[1].Model != "claude-opus-5-5" || got[1].Missing != "" {
		t.Errorf("project one: %+v", got[1])
	}
	if got[2].Missing != "cwd" {
		t.Errorf("gone folder: %+v", got[2])
	}
	if err := os.Remove(filepath.Join(projects, "-x", "aaaaaaaa-0000-0000-0000-000000000001.jsonl")); err != nil {
		t.Fatal(err)
	}
	got, _ = Scan(dir, projects)
	if got[1].Missing != "transcript" {
		t.Errorf("no transcript: %+v", got[1])
	}
	// No Desktop App
	if got, err := Scan(t.TempDir(), projects); err != nil || len(got) != 0 {
		t.Errorf("empty: %v %v", got, err)
	}
}
