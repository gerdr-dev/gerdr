package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A stand-in for `claude auth login` without a TTY: prints the URL, prompts without a newline, reads the code.
const fakeLogin = `#!/bin/sh
[ "$1 $2" = "auth login" ] || exit 9
echo "Opening browser to sign in…"
echo "If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&state=x"
printf "Paste code here if prompted > "
read code
[ "$code" = "abc#def" ] && { echo "Login successful."; exit 0; }
echo "Invalid code"; exit 1
`

func startFake(t *testing.T) (*LoginRun, chan LoginState) {
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte(fakeLogin), 0o755); err != nil {
		t.Fatal(err)
	}
	ended := make(chan LoginState, 1)
	l, err := StartLogin(Options{Bin: bin}, func(st LoginState) { ended <- st })
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for l.State().URL == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := l.State().URL; got != "https://claude.com/cai/oauth/authorize?code=true&state=x" {
		t.Fatalf("URL = %q", got)
	}
	return l, ended
}

func TestLoginRunCode(t *testing.T) {
	l, ended := startFake(t)
	if err := l.Code(" abc#def\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case st := <-ended:
		if st.State != "done" {
			t.Fatalf("state = %+v", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("didn't end")
	}
	if l.Code("again") == nil {
		t.Error("a code after the end should fail")
	}
}

func TestLoginRunWrongCodeAndCancel(t *testing.T) {
	l, ended := startFake(t)
	_ = l.Code("nope")
	if st := <-ended; st.State != "failed" || st.Output == "" {
		t.Fatalf("state = %+v", st)
	}
	l, ended = startFake(t)
	l.Cancel()
	if st := <-ended; st.State != "failed" {
		t.Fatalf("state = %+v", st)
	}
}
