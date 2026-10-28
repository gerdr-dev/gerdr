//go:build !windows

package term

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTermRoundTrip(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	m := New(os.Environ, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer m.Close()
	dir := t.TempDir()
	tm, err := m.Create(CreateOptions{Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}
	_, out, detach := tm.Attach()
	defer detach()
	if err := tm.Write([]byte("echo gerdr-$((40+2)); pwd\n")); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	deadline := time.After(5 * time.Second)
	for !strings.Contains(got.String(), "gerdr-42") {
		select {
		case b := <-out:
			got.Write(b)
		case <-deadline:
			t.Fatalf("没等到输出：%q", got.String())
		}
	}
	// 重连拿到的重放里也有
	backlog, _, d2 := tm.Attach()
	d2()
	if !strings.Contains(string(backlog), "gerdr-42") {
		t.Fatalf("重放里没有：%q", backlog)
	}
	if d := tm.LiveCwd(); d != "" && !strings.HasSuffix(d, strings.TrimPrefix(dir, "/private")) {
		t.Errorf("LiveCwd = %q, want %q", d, dir)
	}
	_ = tm.Write([]byte("exit 3\n"))
	select {
	case <-tm.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("shell 没退")
	}
	if tm.ExitCode() != 3 {
		t.Errorf("退出码 %d", tm.ExitCode())
	}
}

func TestKillGroup(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	m := New(os.Environ, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tm, err := m.Create(CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = tm.Write([]byte("sleep 100\n"))
	time.Sleep(200 * time.Millisecond)
	if err := m.Kill(tm.ID()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tm.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("kill 之后没退")
	}
	if m.Get(tm.ID()) != nil {
		t.Error("kill 之后还在列表里")
	}
	// 界面靠 -1 区分「被结束的」（留着给重开）和 shell 自己退的（关掉 pane）
	if tm.ExitCode() != -1 {
		t.Errorf("被 kill 的退出码 = %d，要 -1", tm.ExitCode())
	}
}

func TestTrimRing(t *testing.T) {
	b := []byte(strings.Repeat("x", 100) + "\n" + strings.Repeat("y", 100))
	r := trimRing(b, 120)
	if len(r) > 120 || r[0] != 'y' {
		t.Fatalf("%q", r)
	}
}
