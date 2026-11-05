package work

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeHost struct {
	started []StartOptions
	project string
	closed  []string
	tagged  map[string]string
	listTag map[string]string
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
func (f *fakeHost) Sessions(cwd string, tags map[string]string) []map[string]any {
	f.listTag = tags
	return []map[string]any{{"id": "s1", "cwd": cwd}}
}
func (f *fakeHost) Close(_ context.Context, id string) error {
	f.closed = append(f.closed, id)
	return nil
}
func (f *fakeHost) Tag(id string, tags map[string]string) (map[string]any, error) {
	f.tagged = tags
	return map[string]any{"id": id, "tags": tags}, nil
}
func (f *fakeHost) Send(_ context.Context, _, id, text string, _ []string, _ bool) (map[string]any, error) {
	return map[string]any{"id": id, "sent": text}, nil
}
func (f *fakeHost) Workspaces(project string, all bool) []map[string]any {
	return []map[string]any{{"id": "w1", "project": project, "all": all}}
}
func (f *fakeHost) Agents() []map[string]any {
	return []map[string]any{{"id": "claude", "name": "Claude Code", "available": true}, {"id": "design", "name": "设计", "base": "claude", "available": true, "prompt": "UI design"}}
}
func (f *fakeHost) RecentDirs() []map[string]any {
	return []map[string]any{{"path": "/r", "source": "session"}}
}

func TestSessionsAndWorkspaces(t *testing.T) {
	r, _ := newProject(t, map[string]string{"t.ts": `
export async function go(input, ctx) {
  const s = await ctx.sessions.start({ cwd: '~/proj', prompt: 'do it', title: 'Task 1', tags: { mr: 'a!1' }, reportTo: 'work' })
  ctx.sessions.list({ tags: { mr: '' } })
  ctx.sessions.tag('s1', { mr: '', task: 't9' })
  await ctx.sessions.close('s1')
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
	if o := h.started[0]; o.Tags["mr"] != "a!1" || o.ReportTo != "work" {
		t.Errorf("tags / reportTo not passed on: %+v", o)
	}
	if v, ok := h.listTag["mr"]; !ok || v != "" {
		t.Errorf("list tags: %v", h.listTag)
	}
	if v, ok := h.tagged["mr"]; !ok || v != "" || h.tagged["task"] != "t9" {
		t.Errorf("tag: %v", h.tagged)
	}
	if len(h.closed) != 1 || h.closed[0] != "s1" {
		t.Errorf("close: %v", h.closed)
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

// A session a function starts gets the calling session as its parent (a router running `gerdr panel run …` starts
// workers that point back at it), unless the function names one.
func TestStartParentIsCaller(t *testing.T) {
	r, _ := newProject(t, map[string]string{
		"w.ts": `export async function go(input: any, ctx: any) {
  await ctx.sessions.start({ cwd: "~", prompt: "a" });
  await ctx.sessions.start({ cwd: "~", prompt: "b", parent: "other" });
}`,
	})
	f := &fakeHost{}
	r.Host = f
	rn := r.StartAs("", "w.go", nil, "router1")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := rn.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.started) != 2 || f.started[0].Parent != "router1" || f.started[1].Parent != "other" {
		t.Fatalf("parents: %+v", f.started)
	}
}
