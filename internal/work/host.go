package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// ctx.sessions / ctx.workspaces / ctx.dirs: what a Work page needs to hand work to agents — pick a directory (the ones
// already used, the Code workspaces, or anywhere on disk), open a conversation there with a first message, then follow
// it. Unlike ctx.fs, which is the function's own data and stays inside the Work directory, these see the whole disk,
// read-only: a function could already do as much with ctx.exec, this just returns structured data.
//
// Sessions and workspaces live in the server (the hub and the layout), so they come through Host; without one (tests,
// the CLI's Runner) those calls fail with a clear message instead of doing nothing.

// Host is the gerdr side of ctx.sessions, ctx.workspaces and ctx.dirs.recent. Results are plain JSON-shaped values.
type Host interface {
	// StartSession opens a conversation and places it in the project's Code layout (a new tab in the workspace for its
	// directory; a new workspace when there's none), then sends the first message if there is one.
	StartSession(ctx context.Context, project string, o StartOptions) (map[string]any, error)
	Session(id string) (map[string]any, error)
	// Conversation: what was said in it, oldest first — the user's messages and the agent's text, no tool calls —
	// the last `last` of them (0 = all). Each { role: user | agent, text, at, from? }.
	Conversation(id string, last int) ([]map[string]any, error)
	// Sessions: every conversation, most recently active first; cwd / tags narrow it (see hub.HasTags).
	Sessions(cwd string, tags map[string]string) []map[string]any
	// Send: id "work" = the project's Work agent conversation, as for StartOptions.ReportTo. attachments as in
	// StartOptions.
	// now: go in right away while it runs (hub urgent steer: a running command moves to the background), instead of
	// at its next pause.
	Send(ctx context.Context, project, id, text string, attachments []string, now bool) (map[string]any, error)
	// Close stops a conversation's agent and takes it off the screen; the record stays and Send opens it again.
	Close(ctx context.Context, id string) error
	// Tag changes a conversation's tags ("" removes a key).
	Tag(id string, tags map[string]string) (map[string]any, error)
	// Workspaces lists the project's Code workspaces (every project's with all) and the conversations in their panes.
	Workspaces(project string, all bool) []map[string]any
	// Agents: the agents and roles a session can start with (hub.Agents: id, name, base, available, prompt, …).
	Agents() []map[string]any
	// RecentDirs: directories conversations ran in and workspaces point at, most recently used first.
	RecentDirs() []map[string]any
}

// StartOptions is ctx.sessions.start's argument.
type StartOptions struct {
	Cwd    string `json:"cwd"`
	Prompt string `json:"prompt,omitempty"`
	Agent  string `json:"agent,omitempty"` // claude / codex / native / a variant's id; "" = the default agent
	Title  string `json:"title,omitempty"` // the tab's name until the agent's own title replaces it
	Mode   string `json:"mode,omitempty"`  // permission mode; "" = the agent's default (never a permissive one)
	// Model / Effort: the agent's own option values (as its model / effort pickers list them); "" = the user's default
	// for that agent. A value the agent doesn't offer is an error naming the ones it does, not silently the default.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Parent: the session that hands this one its work; default: the session that called this function (ctx.caller,
	// e.g. a router running `gerdr panel run tasks.dispatch`), so the chain stays unbroken across a panel.
	Parent    string `json:"parent,omitempty"`
	Workspace string `json:"workspace,omitempty"` // a workspace id from ctx.workspaces.list; "new"; "none" = not in Code; "" = found by cwd
	Focus     bool   `json:"focus,omitempty"`     // switch the open windows to it
	// Tags: labels to find it by later (ctx.sessions.list({ tags })).
	Tags map[string]string `json:"tags,omitempty"`
	// ReportTo: a session told each time this one stops (done, waiting for a person, error); "work" = this project's
	// Work agent conversation.
	ReportTo string `json:"reportTo,omitempty"`
	// Attachments: files on this machine sent with the first message, the way the composer sends them: an image as
	// the image itself, anything else as a file link the agent opens with its tools. A page gets a path for a pasted
	// / dropped / picked file from POST /api/fs/upload.
	Attachments []string `json:"attachments,omitempty"`
}

var errNoHost = errors.New("not available here: ctx.sessions / ctx.workspaces need Gerdr's server")

// opts reads an options object (or nothing) into v through JSON, the way the server would see it.
func (r *runner) opts(arg goja.Value, v any) error {
	if arg == nil || goja.IsUndefined(arg) || goja.IsNull(arg) {
		return nil
	}
	m, ok := plain(toPlain(arg.Export())).(map[string]any)
	if !ok {
		return errors.New("expected an options object")
	}
	b, _ := json.Marshal(m)
	return json.Unmarshal(b, v)
}

func (r *runner) sessionsObject() map[string]any {
	h := r.r.Host
	return map[string]any{
		// await ctx.sessions.start({ cwd, prompt?, agent?, title?, mode?, workspace?, focus? }) → session
		"start": func(arg goja.Value) *goja.Promise {
			if h == nil {
				return r.rejected(errNoHost)
			}
			var o StartOptions
			if err := r.opts(arg, &o); err != nil {
				return r.rejected(fmt.Errorf("ctx.sessions.start: %w", err))
			}
			if strings.TrimSpace(o.Cwd) == "" {
				return r.rejected(errors.New("ctx.sessions.start: cwd is required"))
			}
			cwd, err := expandPath(o.Cwd)
			if err != nil {
				return r.rejected(fmt.Errorf("ctx.sessions.start: %w", err))
			}
			o.Cwd = cwd
			if o.Parent == "" {
				o.Parent = callerOf(r.ctx)
			}
			ctx, cancel := context.WithTimeout(r.ctx, 2*time.Minute)
			defer cancel()
			s, err := h.StartSession(ctx, r.project, o)
			if err != nil {
				return r.rejected(err)
			}
			return r.resolved(s)
		},
		// ctx.sessions.agents() → the agents / roles `start` takes, with what a role is for (its prompt)
		"agents": func() []map[string]any {
			if h == nil {
				r.throw(errNoHost)
			}
			return h.Agents()
		},
		// ctx.sessions.get(id) → session or null
		"get": func(id string) any {
			if h == nil {
				r.throw(errNoHost)
			}
			s, err := h.Session(id)
			if err != nil {
				return nil
			}
			return s
		},
		// ctx.sessions.read(id, { last? }) → [{ role, text, at, from? }] or null: what was said in it, no tool calls. A
		// panel shows a conversation where Gerdr's chat can't (another computer's, synced through the panel's own
		// server), which `reply` (the last turn only) isn't enough for.
		"read": func(id string, arg goja.Value) any {
			if h == nil {
				r.throw(errNoHost)
			}
			var o struct {
				Last int `json:"last"`
			}
			if err := r.opts(arg, &o); err != nil {
				r.throw(fmt.Errorf("ctx.sessions.read: %w", err))
			}
			c, err := h.Conversation(id, o.Last)
			if err != nil {
				return nil
			}
			return c
		},
		// ctx.sessions.list({ cwd? }) → sessions, most recently active first
		"list": func(arg goja.Value) []map[string]any {
			if h == nil {
				r.throw(errNoHost)
			}
			var o struct {
				Cwd  string            `json:"cwd"`
				Tags map[string]string `json:"tags"`
			}
			if err := r.opts(arg, &o); err != nil {
				r.throw(fmt.Errorf("ctx.sessions.list: %w", err))
			}
			cwd := ""
			if o.Cwd != "" {
				var err error
				if cwd, err = expandPath(o.Cwd); err != nil {
					r.throw(err)
				}
			}
			return h.Sessions(cwd, o.Tags)
		},
		// await ctx.sessions.send(id, text) → session. A running one gets it at its next pause, like a queued message.
		// id "work" = this project's Work agent, so a function can hand something to it (it has no session id to use).
		"send": func(id, text string, arg goja.Value) *goja.Promise {
			if h == nil {
				return r.rejected(errNoHost)
			}
			if strings.TrimSpace(text) == "" {
				return r.rejected(errors.New("ctx.sessions.send: empty message"))
			}
			ctx, cancel := context.WithTimeout(r.ctx, 2*time.Minute)
			defer cancel()
			var o struct {
				Attachments []string `json:"attachments"`
				Now         bool     `json:"now"`
			}
			if err := r.opts(arg, &o); err != nil {
				return r.rejected(err)
			}
			s, err := h.Send(ctx, r.project, id, text, o.Attachments, o.Now)
			if err != nil {
				return r.rejected(err)
			}
			return r.resolved(s)
		},
		// await ctx.sessions.close(id): stop its agent, take it off the screen; send() later opens it again
		"close": func(id string) *goja.Promise {
			if h == nil {
				return r.rejected(errNoHost)
			}
			ctx, cancel := context.WithTimeout(r.ctx, time.Minute)
			defer cancel()
			if err := h.Close(ctx, id); err != nil {
				return r.rejected(err)
			}
			return r.resolved(nil)
		},
		// ctx.sessions.tag(id, { key: "value", gone: "" }) → session
		"tag": func(id string, arg goja.Value) map[string]any {
			if h == nil {
				r.throw(errNoHost)
			}
			var tags map[string]string
			if err := r.opts(arg, &tags); err != nil {
				r.throw(fmt.Errorf("ctx.sessions.tag: %w", err))
			}
			s, err := h.Tag(id, tags)
			if err != nil {
				r.throw(err)
			}
			return s
		},
	}
}

func (r *runner) workspacesObject() map[string]any {
	return map[string]any{
		// ctx.workspaces.list({ all? }) → this project's Code workspaces (every project's with all)
		"list": func(arg goja.Value) []map[string]any {
			if r.r.Host == nil {
				r.throw(errNoHost)
			}
			var o struct {
				All bool `json:"all"`
			}
			if err := r.opts(arg, &o); err != nil {
				r.throw(fmt.Errorf("ctx.workspaces.list: %w", err))
			}
			return r.r.Host.Workspaces(r.project, o.All)
		},
	}
}

// dirsReadMax is how much ctx.dirs.read returns at most: enough for a README's opening, not a way to slurp big files.
const dirsReadMax = 1 << 20

func (r *runner) dirsObject() map[string]any {
	return map[string]any{
		"home": func() string { h, _ := os.UserHomeDir(); return h },
		// ctx.dirs.list(path) → [{ name, path, git }]: the folders in it (dot folders skipped), git = a repository
		"list": func(p string) []map[string]any {
			dir, err := expandPath(p)
			if err != nil {
				r.throw(err)
			}
			ents, err := os.ReadDir(dir)
			if err != nil {
				r.throw(err)
			}
			out := []map[string]any{}
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), ".") {
					continue
				}
				full := filepath.Join(dir, e.Name())
				if !e.IsDir() {
					if st, err := os.Stat(full); err != nil || !st.IsDir() { // a symlink to a folder counts
						continue
					}
				}
				_, gerr := os.Stat(filepath.Join(full, ".git"))
				out = append(out, map[string]any{"name": e.Name(), "path": full, "git": gerr == nil})
			}
			sort.Slice(out, func(i, j int) bool {
				return strings.ToLower(out[i]["name"].(string)) < strings.ToLower(out[j]["name"].(string))
			})
			return out
		},
		// ctx.dirs.stat(path) → { path, dir, git } or null when it doesn't exist
		"stat": func(p string) any {
			full, err := expandPath(p)
			if err != nil {
				r.throw(err)
			}
			st, err := os.Stat(full)
			if err != nil {
				return nil
			}
			_, gerr := os.Stat(filepath.Join(full, ".git"))
			return map[string]any{"path": full, "dir": st.IsDir(), "git": st.IsDir() && gerr == nil}
		},
		// ctx.dirs.read(path, { max? }) → the text, cut at max bytes (default 64 KB, at most 1 MB); null when missing
		"read": func(p string, arg goja.Value) any {
			full, err := expandPath(p)
			if err != nil {
				r.throw(err)
			}
			o := struct {
				Max int `json:"max"`
			}{Max: 64 << 10}
			if err := r.opts(arg, &o); err != nil {
				r.throw(fmt.Errorf("ctx.dirs.read: %w", err))
			}
			o.Max = min(max(o.Max, 1), dirsReadMax)
			f, err := os.Open(full)
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				r.throw(err)
			}
			defer f.Close()
			if st, err := f.Stat(); err == nil && st.IsDir() {
				r.throw(fmt.Errorf("%s is a folder: use ctx.dirs.list", p))
			}
			b, err := io.ReadAll(io.LimitReader(f, int64(o.Max)))
			if err != nil {
				r.throw(err)
			}
			for len(b) > 0 && !utf8.Valid(b) { // don't hand back half a character where it was cut
				b = b[:len(b)-1]
			}
			return string(b)
		},
		// ctx.dirs.recent() → [{ path, lastUsed, source: "session" | "workspace" }]
		"recent": func() []map[string]any {
			if r.r.Host == nil {
				r.throw(errNoHost)
			}
			return r.r.Host.RecentDirs()
		},
	}
}

// expandPath takes an absolute or ~ path to a clean absolute one. Relative paths are refused: relative to what would
// be a guess (the Work directory is ctx.fs's business).
func expandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[1:])
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q: give an absolute path or one starting with ~", p)
	}
	return filepath.Clean(p), nil
}
