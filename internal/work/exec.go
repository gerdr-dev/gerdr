package work

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/gerdr-dev/gerdr/sysenv"
)

// ctx.exec waits 30 seconds by default, 10 minutes at most; stdout and stderr keep 10MB each, the rest is cut
const (
	execTimeout    = 30 * time.Second
	execMaxTimeout = 10 * time.Minute
	execMaxOutput  = 10 << 20
)

// exec: await ctx.exec('glab', ['mr', 'list', '-F', 'json'], { cwd, input, timeout, env }) → { code, stdout, stderr, truncated }.
// The command and its arguments go apart, no shell in between (for pipes and redirects: ctx.exec('sh', ['-c', script,
// '_', arg…])). A non-zero exit isn't an error, look at code; only a missing command, a timeout or a cwd outside the Work
// directory reject. Commands are found on the login shell's PATH (the App's own is bare when started from the Dock), a
// path with a slash is relative to cwd; cwd defaults to the Work directory and can't leave it; stdin is empty unless
// input is given (so nothing hangs waiting for it); env adds to gerdr's environment (pass ctx.secrets.get values here).
// Same contract as Annulo's ctx.exec.
func (r *runner) exec(name string, args goja.Value, opts goja.Value) *goja.Promise {
	// ctx.exec('ls', { cwd }) too: an object instead of the array is the options
	if o, ok := args.(*goja.Object); ok && o.ClassName() != "Array" && (opts == nil || goja.IsUndefined(opts)) {
		args, opts = nil, args
	}
	var argv []string
	if args != nil && !goja.IsUndefined(args) && !goja.IsNull(args) {
		list, ok := args.Export().([]any)
		if !ok {
			return r.rejected(errors.New("ctx.exec's second argument is the argument array: ctx.exec('cmd', ['arg1', 'arg2'], { … })"))
		}
		for _, a := range list {
			if a == nil {
				return r.rejected(errors.New("ctx.exec's arguments contain null / undefined"))
			}
			argv = append(argv, r.vm.ToValue(a).String())
		}
	}
	var o map[string]any
	if opts != nil && !goja.IsUndefined(opts) && !goja.IsNull(opts) {
		m, ok := toPlain(opts.Export()).(map[string]any)
		if !ok {
			return r.rejected(errors.New("ctx.exec's third argument must be an object: { cwd, input, timeout, env }"))
		}
		o = m
	}

	dir := r.dir
	if cwd, _ := o["cwd"].(string); cwd != "" {
		d := filepath.Join(r.dir, cwd)
		if filepath.IsAbs(cwd) {
			d = filepath.Clean(cwd)
		}
		if rel, err := filepath.Rel(r.dir, d); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return r.rejected(fmt.Errorf("ctx.exec's cwd must be inside the Work directory (relative to it): %s — for another repo pass its path as an argument (git -C <dir>, glab -R <repo>)", cwd))
		}
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			return r.rejected(fmt.Errorf("ctx.exec's cwd isn't a directory: %s", cwd))
		}
		dir = d
	}

	path := name
	if strings.ContainsAny(name, `/\`) {
		if !filepath.IsAbs(name) {
			path = filepath.Join(dir, name)
		}
	} else {
		sysenv.AdoptLoginPath()
		p, err := exec.LookPath(name)
		if err != nil {
			return r.rejected(fmt.Errorf("command not found: %s (not installed on this computer, or not on PATH)", name))
		}
		path = p
	}

	timeout := execTimeout
	if ms := intOf(o["timeout"], 0); ms > 0 {
		timeout = min(time.Duration(ms)*time.Millisecond, execMaxTimeout)
	}
	var env []string
	if m, ok := o["env"].(map[string]any); ok {
		for k, v := range m {
			if v != nil {
				env = append(env, k+"="+r.vm.ToValue(v).String())
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, argv...)
	setProcGroup(cmd)
	cmd.WaitDelay = 2 * time.Second // a grandchild holding the output pipe doesn't keep us waiting
	cmd.Dir = dir
	cmd.Env = sysenv.ChildEnv(env...)
	if in, ok := o["input"]; ok && in != nil {
		cmd.Stdin = strings.NewReader(r.vm.ToValue(in).String())
	}
	stdout, stderr := &capBuffer{max: execMaxOutput}, &capBuffer{max: execMaxOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr

	err := cmd.Run()
	code := 0
	var exitErr *exec.ExitError
	switch {
	case r.ctx.Err() != nil:
		return r.rejected(r.ctx.Err())
	case ctx.Err() != nil:
		return r.rejected(fmt.Errorf("ctx.exec %s didn't finish within %s and was stopped (pass a longer timeout in ms, up to 10 minutes)", name, timeout))
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	case err != nil:
		return r.rejected(fmt.Errorf("ctx.exec %s failed: %w", name, err))
	}
	return r.resolved(map[string]any{
		"code":      code,
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"truncated": stdout.cut || stderr.cut,
	})
}

// capBuffer keeps the first max bytes and drops the rest (still reporting the write as done, so the command neither
// blocks nor fails on a full pipe)
type capBuffer struct {
	bytes.Buffer
	max int
	cut bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room < len(p) {
		b.cut = true
		b.Buffer.Write(p[:max(room, 0)])
		return len(p), nil
	}
	return b.Buffer.Write(p)
}
