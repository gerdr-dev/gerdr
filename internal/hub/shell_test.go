package hub

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

func waitShellDone(t *testing.T, s *Session, id string) *Event {
	t.Helper()
	for i := 0; i < 100; i++ {
		for _, e := range s.Events() {
			if e.Kind == EvShellDone && e.MessageID == id {
				return e
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("命令没跑完")
	return nil
}

// `!命令`：在会话目录里跑、输出进日志；跑完的跟着下一条消息带给 agent，带过之后不再带。
func TestShellPending(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := newSession(h, Record{ID: "s1", Cwd: dir})
	id, err := s.Shell("pwd; echo 中文; exit 3", "")
	if err != nil {
		t.Fatal(err)
	}
	d := waitShellDone(t, s, id)
	if d.Exit == nil || *d.Exit != 3 {
		t.Fatalf("退出码 %v", d.Exit)
	}
	s.mu.Lock()
	p := s.pendingShellLocked()
	s.mu.Unlock()
	if len(p) != 1 {
		t.Fatalf("pending %d", len(p))
	}
	txt := p[0].Text.Text
	for _, want := range []string{"<bash-input>pwd; echo 中文; exit 3</bash-input>", dir, "中文", "<bash-stderr>Exit code 3</bash-stderr>"} {
		if !strings.Contains(txt, want) {
			t.Errorf("缺 %q：%s", want, txt)
		}
	}
	raw, _ := json.Marshal(p[0])
	if !isShellBlock(raw) {
		t.Error("isShellBlock 认不出来")
	}
	// 带过一次（日志里有了用户消息）就不再带
	s.emit(&Event{Kind: EvUser, MessageID: "u1"})
	s.mu.Lock()
	p = s.pendingShellLocked()
	s.mu.Unlock()
	if len(p) != 0 {
		t.Fatalf("带过的又带了一遍：%d", len(p))
	}
	got := withShell(nil, []acp.ContentBlock{acp.TextBlock("hi")})
	if len(got) != 1 {
		t.Fatal("没有命令时内容不该变")
	}
}

// 停止：连同它起的子进程一起杀掉，结束标 stopped。
func TestShellStop(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := newSession(h, Record{ID: "s1", Cwd: t.TempDir()})
	id, err := s.Shell("echo start; sleep 30", "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := s.StopShell(id); err != nil {
		t.Fatal(err)
	}
	d := waitShellDone(t, s, id)
	if d.Level != "stopped" {
		t.Fatalf("level %q", d.Level)
	}
	if !strings.Contains(shellContext("x", "", d), "Stopped by the user") {
		t.Error("停止的没交代")
	}
}

// 输出里半个 UTF-8 字符不能被切到两段里（中文成段写进日志时会乱码）。
func TestShellOutTake(t *testing.T) {
	o := &shellOut{}
	b := []byte("ab中")
	o.Write(b[:len(b)-1])
	if got := o.take(false); got != "ab" {
		t.Fatalf("got %q", got)
	}
	o.Write(b[len(b)-1:])
	if got := o.take(true); got != "中" {
		t.Fatalf("got %q", got)
	}
}
