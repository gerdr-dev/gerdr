package claude

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/sysenv"
)

// Signing in from Gerdr, without a terminal: `claude auth login` with pipes instead of a TTY still works. It opens the
// browser on this computer (whose localhost callback finishes the login by itself) and also prints a URL whose
// redirect shows a code, then reads that code from stdin ("Paste code here if prompted >"). So the page shows the
// URL and a box for the code: someone on a phone, or whose browser didn't open, signs in there and pastes the code.
// It exits 0 once the credentials are saved; a running claude process picks them up on its next request.

// LoginState: how a sign-in started by StartLogin stands.
type LoginState struct {
	State  string `json:"state"`            // running | done | failed
	URL    string `json:"url,omitempty"`    // where to sign in, once claude printed it
	Error  string `json:"error,omitempty"`  // failed: why
	Output string `json:"output,omitempty"` // the end of what claude printed (shown when it fails)
}

type LoginRun struct {
	mu     sync.Mutex
	st     LoginState
	out    strings.Builder
	stdin  io.WriteCloser
	cancel context.CancelFunc
	done   chan struct{}
}

// loginTimeout: someone who walked away doesn't leave a claude waiting for a code forever.
const loginTimeout = 15 * time.Minute

var urlRe = regexp.MustCompile(`https://\S+`)

// StartLogin runs `claude auth login` for this instance (opts.Bin, opts.Env: CLAUDE_CONFIG_DIR picks the account).
// onDone is called once it ends, with the final state.
func StartLogin(opts Options, onDone func(LoginState)) (*LoginRun, error) {
	a := &Agent{opts: opts}
	bin, err := a.bin()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	cmd := exec.CommandContext(ctx, bin, "auth", "login")
	cmd.Dir = os.TempDir()
	cmd.Env = append(sysenv.ChildEnv(), opts.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	l := &LoginRun{st: LoginState{State: "running"}, stdin: stdin, cancel: cancel, done: make(chan struct{})}
	go l.read(pr)
	go func() {
		err := cmd.Wait()
		_ = pw.Close()
		cancel()
		l.mu.Lock()
		switch {
		case err == nil:
			l.st.State = "done"
		case ctx.Err() == context.DeadlineExceeded:
			l.st.State, l.st.Error = "failed", i18n.S("登录超时了，再试一次", "Signing in timed out; try again")
		case ctx.Err() != nil:
			l.st.State, l.st.Error = "failed", i18n.S("登录取消了", "Signing in was cancelled")
		default:
			l.st.State, l.st.Error = "failed", err.Error()
		}
		l.st.Output = tail(l.out.String(), 2000)
		st := l.st
		l.mu.Unlock()
		close(l.done)
		if onDone != nil {
			onDone(st)
		}
	}()
	return l, nil
}

// read collects the output (the code prompt has no newline, so by chunks, not lines) and picks the URL out.
// Of the URLs claude prints, the one after "visit:" is the one that works anywhere (it redirects to a page that
// shows the code); it's the only one printed today, the last one wins in case that changes.
func (l *LoginRun) read(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			l.mu.Lock()
			l.out.Write(buf[:n])
			if us := urlRe.FindAllString(l.out.String(), -1); len(us) > 0 {
				l.st.URL = us[len(us)-1]
			}
			l.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (l *LoginRun) State() LoginState {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.st
	if st.State == "running" {
		st.Output = ""
	}
	return st
}

// Code hands claude the code the sign-in page showed.
func (l *LoginRun) Code(code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New(i18n.S("授权码是空的", "The code is empty"))
	}
	select {
	case <-l.done:
		return errors.New(i18n.S("这次登录已经结束了", "This sign-in has already ended"))
	default:
	}
	_, err := io.WriteString(l.stdin, code+"\n")
	return err
}

func (l *LoginRun) Cancel()               { l.cancel() }
func (l *LoginRun) Done() <-chan struct{} { return l.done }

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}
