package work

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeHost struct {
	started []StartOptions
	project string
}

func (f *fakeHost) StartSession(_ context.Context, project string, o StartOptions) (map[string]any, error) {
	f.started = append(f.started, o)
	f.project = project
	return map[string]any{"id": "s1", "cwd": o.Cwd, "status": "running"}, nil
}
func (f *fakeHost) Session(id string) (map[string]any, error) {
	if id != "s1" {
		return nil, os.ErrNotExist
	}
	return map[string]any{"id": "s1", "status": "idle", "reply": "done"}, nil
}
func (f *fakeHost) Sessions(cwd string) []map[string]any {
	return []map[string]any{{"id": "s1", "cwd": cwd}}
}
func (f *fakeHost) Send(_ context.Context, id, text string) (map[string]any, error) {
	return map[string]any{"id": id, "sent": text}, nil
}
func (f *fakeHost) Workspaces(project string, all bool) []map[string]any {
	return []map[string]any{{"id": "w1", "project": project, "all": all}}
}
func (f *fakeHost) RecentDirs() []map[string]any {
	return []map[string]any{{"path": "/r", "source": "session"}}
}

func TestSessionsAndWorkspaces(t *testing.T) {
	r, _ := newProject(t, map[string]string{"t.ts": `
export async function go(input, ctx) {
  const s = await ctx.sessions.start({ cwd: '~/proj', prompt: 'do it', title: 'Task 1' })
  let noCwd = ''
  await ctx.sessions.start({ prompt: 'x' }).catch((e) => (noCwd = e.message))
  let rel = ''
  await ctx.sessions.start({ cwd: 'proj' }).catch((e) => (rel = e.message))
  return {
    s, noCwd, rel,
    got: ctx.sessions.get('s1'), missing: ctx.sessions.get('zz'),
    sent: await ctx.sessions.send('s1', 'more'),
    ws: ctx.workspaces.list({ all: true }), recent: ctx.dirs.recent(),
  }
}`})
	h := &fakeHost{}
	r.Host = h
	res, _, err := run(t, r, "t.go", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	home, _ := os.UserHomeDir()
	if len(h.started) != 1 || h.started[0].Cwd != filepath.Join(home, "proj") || h.started[0].Prompt != "do it" || h.started[0].Title != "Task 1" || h.project != "default" {
		t.Errorf("started %+v in project %q", h.started, h.project)
	}
	if !strings.Contains(m["noCwd"].(string), "cwd is required") || !strings.Contains(m["rel"].(string), "absolute path") {
		t.Errorf("errors: %q / %q", m["noCwd"], m["rel"])
	}
	if m["got"].(map[string]any)["reply"] != "done" || m["missing"] != nil {
		t.Errorf("get: %v / %v", m["got"], m["missing"])
	}
	if m["sent"].(map[string]any)["sent"] != "more" {
		t.Errorf("send: %v", m["sent"])
	}
	if w := m["ws"].([]any)[0].(map[string]any); w["all"] != true {
		t.Errorf("workspaces: %v", w)
	}

	// Without a host (the CLI's Runner) it says so rather than doing nothing
	r.Host = nil
	_, _, err = run(t, r, "t.go", nil)
	if err == nil || !strings.Contains(err.Error(), "not available here") {
		t.Errorf("no host: err = %v", err)
	}
}

func TestDirs(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "repo", ".git"), 0o755)
	os.MkdirAll(filepath.Join(root, "Plain"), 0o755)
	os.MkdirAll(filepath.Join(root, ".hidden"), 0o755)
	os.WriteFile(filepath.Join(root, "repo", "README.md"), []byte("# Repo\n\nWhat it is. 中文"), 0o644)
	r, _ := newProject(t, map[string]string{"t.ts": `
export function go(input, ctx) {
  let missing = ''
  try { ctx.dirs.list(input.root + '/nope') } catch (e) { missing = e.message }
  return {
    list: ctx.dirs.list(input.root),
    stat: ctx.dirs.stat(input.root + '/repo'), none: ctx.dirs.stat(input.root + '/nope'),
    readme: ctx.dirs.read(input.root + '/repo/README.md'),
    cut: ctx.dirs.read(input.root + '/repo/README.md', { max: 20 }),
    absent: ctx.dirs.read(input.root + '/repo/NOPE.md'),
    missing,
  }
}`})
	res, _, err := run(t, r, "t.go", map[string]any{"root": root})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	list := m["list"].([]any)
	if len(list) != 2 || list[0].(map[string]any)["name"] != "Plain" || list[1].(map[string]any)["git"] != true {
		t.Errorf("list: %v", list)
	}
	if st := m["stat"].(map[string]any); st["dir"] != true || st["git"] != true || m["none"] != nil {
		t.Errorf("stat: %v / %v", m["stat"], m["none"])
	}
	if m["readme"] != "# Repo\n\nWhat it is. 中文" || m["absent"] != nil {
		t.Errorf("read: %q / %v", m["readme"], m["absent"])
	}
	// 20 bytes ends inside 中: cut back to whole characters
	if c := m["cut"].(string); c != "# Repo\n\nWhat it is. " {
		t.Errorf("cut: %q", c)
	}
	if !strings.Contains(m["missing"].(string), "no such file") {
		t.Errorf("missing dir: %q", m["missing"])
	}
}
