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
	Send(ctx context.Context, project, id, text string, o SendOptions) (map[string]any, error)
	// Close stops a conversation's agent and takes it off the screen; the record stays and Send opens it again.
	Close(ctx context.Context, id string) error
	// Tag changes a conversation's tags ("" removes a key).
	Tag(id string, tags map[string]string) (map[string]any, error)
	// SessionsChanged waits until one of ids moves — something new in its log (a message, the agent's text as it
	// streams, a tool call) or its status — and returns those that moved; none when timeout passes first. Pushed by the
	// hub, the way the chat follows a conversation: a panel follows conversations without polling them.
	SessionsChanged(ctx context.Context, ids []string, timeout time.Duration) ([]string, error)
	// Pending: what a conversation the panel started waits on a person for (approvals, questions), oldest first, in
	// the shape add-on services get (server/addonpending.go). Answer gives one an answer as the chat's card would:
	// {optionId} for an approval, {answers} / {skip} for a question. Both only for a conversation that panel started
	// (its PanelTag), and Answer never for a call an agent made (the runner checks): an agent must not approve its own.
	Pending(panel, id string) ([]map[string]any, error)
	Answer(panel, id, reqID string, answer json.RawMessage) error
	// Workspaces lists the project's Code workspaces (every project's with all) and the conversations in their panes.
	Workspaces(project string, all bool) []map[string]any
	// Agents: the agents and roles a session can start with (hub.Agents: id, name, base, available, prompt, …).
	Agents() []map[string]any
	// RecentDirs: directories conversations ran in and workspaces point at, most recently used first.
	RecentDirs() []map[string]any
	// Artifacts (docs/dev/ARTIFACTS.md). ArtifactsFolder: the panel's folder for one of its things (key), named name
	// under under/ in its project's artifacts directory: {path, exists, id?}; it may not exist yet.
	ArtifactsFolder(panel, key, name, under string) (map[string]any, error)
	// ArtifactsRename: that folder takes a new name (its thing was renamed), when it exists and the name is free.
	ArtifactsRename(panel, key, name string) (string, error)
	// ArtifactsItems: a folder's records ([] when it doesn't exist). ArtifactsRecord adds one (creating the folder).
	ArtifactsItems(dir string) ([]map[string]any, error)
	ArtifactsRecord(dir string, item map[string]any) (map[string]any, error)
}

// SendOptions is ctx.sessions.send's third argument.
type SendOptions struct {
	// Attachments: as in StartOptions.
	Attachments []string `json:"attachments,omitempty"`
	// Now: go in right away while it runs (hub urgent steer: a running command moves to the background), instead of
	// at its next pause.
	Now bool `json:"now,omitempty"`
	// Fold: a one-line summary the chat shows instead of the message, opened to the whole of it on a click (the
	// text block's _meta.gerdr.fold). For inputs code hands an agent that the person reads past; the agent gets the
	// whole message either way.
	Fold string `json:"fold,omitempty"`
	// Show: shown whole. A message from code otherwise folds to its first line (no Fold given): for what the person
	// should read (a worker's answer passed on, their own words relayed).
	Show bool `json:"show,omitempty"`
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
	// ArtifactsDir: the folder the agent puts what it makes that isn't committed (a task's folder from
	// ctx.artifacts.folder); default: its repository's (docs/dev/ARTIFACTS.md).
	ArtifactsDir string `json:"artifactsDir,omitempty"`
	// SystemFile: a file whose text goes into the agent's system prompt, re-read whenever the agent starts again
	// (hub Record.SystemFile): instructions that stay out of the chat and change without a new session.
	SystemFile string `json:"systemFile,omitempty"`
	// ContinueFrom: a conversation (of this panel's) this new one carries on: its chat is copied above and the first
	// message brings it along to the agent (hub handoff.go), so the person reads one conversation across the switch.
	ContinueFrom string `json:"continueFrom,omitempty"`
	// Fold / Show: the first message (prompt), as SendOptions'.
	Fold string `json:"fold,omitempty"`
	Show bool   `json:"show,omitempty"`
}

var errNoHost = errors.New("not available here: ctx.sessions / ctx.workspaces need Gerdr's server")

// opts reads an options object (or nothing) into v through JSON, the way the server would see it.
func (r *runner) opts(arg jsValue, v any) error {
	if arg.IsNullish() {
		return nil
	}
	m, ok := plain(toPlain(r.toGo(arg))).(map[string]any)
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
		"start": func(arg jsValue) *promise {
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
		// ctx.sessions.pending(id) → what a conversation this panel started waits on a person for, oldest first
		"pending": func(id string) []map[string]any {
			if h == nil {
				r.throw(errNoHost)
			}
			out, err := h.Pending(r.project, id)
			if err != nil {
				r.throw(err)
			}
			return out
		},
		// ctx.sessions.answer(id, reqId, { optionId } | { answers } | { skip }) answers one of them for a person. Not
		// for a call an agent made (a tool, `gerdr panel run` from a conversation): an agent must not approve its own
		// requests, nor another's — only the page, a schedule, a stream or a trigger, acting on a person's word.
		"answer": func(id, reqID string, arg jsValue) {
			if h == nil {
				r.throw(errNoHost)
			}
			if c := callerOf(r.ctx); c != "" {
				r.throw(fmt.Errorf("ctx.sessions.answer: called by conversation %s — an agent can't answer approvals; only a person can (the page, or what a person sent through a stream)", c))
			}
			var a map[string]any
			if err := r.opts(arg, &a); err != nil {
				r.throw(fmt.Errorf("ctx.sessions.answer: %w", err))
			}
			b, _ := json.Marshal(a)
			if err := h.Answer(r.project, id, reqID, b); err != nil {
				r.throw(err)
			}
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
		// ctx.sessions.changed(ids, { timeout? }) → the ids that moved (a new event in the log, or the status), or []
		// after timeout (ms, 60 s at most, default 30 s). Instead of polling get / read: the hub pushes it.
		"changed": func(ids []string, arg jsValue) *promise {
			if h == nil {
				r.throw(errNoHost)
			}
			var o struct {
				Timeout int64 `json:"timeout"`
			}
			if err := r.opts(arg, &o); err != nil {
				r.throw(fmt.Errorf("ctx.sessions.changed: %w", err))
			}
			d := time.Duration(o.Timeout) * time.Millisecond
			if d <= 0 {
				d = 30 * time.Second
			}
			if d > time.Minute {
				d = time.Minute
			}
			moved, err := h.SessionsChanged(r.ctx, ids, d)
			if err != nil {
				return r.rejected(err)
			}
			return r.resolved(moved)
		},
		// ctx.sessions.read(id, { last? }) → [{ role, text, at, from? }] or null: what was said in it, no tool calls. A
		// panel shows a conversation where Gerdr's chat can't (another computer's, synced through the panel's own
		// server), which `reply` (the last turn only) isn't enough for.
		"read": func(id string, arg jsValue) any {
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
		"list": func(arg jsValue) []map[string]any {
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
		"send": func(id, text string, arg jsValue) *promise {
			if h == nil {
				return r.rejected(errNoHost)
			}
			if strings.TrimSpace(text) == "" {
				return r.rejected(errors.New("ctx.sessions.send: empty message"))
			}
			ctx, cancel := context.WithTimeout(r.ctx, 2*time.Minute)
			defer cancel()
			var o SendOptions
			if err := r.opts(arg, &o); err != nil {
				return r.rejected(err)
			}
			s, err := h.Send(ctx, r.project, id, text, o)
			if err != nil {
				return r.rejected(err)
			}
			return r.resolved(s)
		},
		// await ctx.sessions.close(id): stop its agent, take it off the screen; send() later opens it again
		"close": func(id string) *promise {
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
		"tag": func(id string, arg jsValue) map[string]any {
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

// artifactsObject is ctx.artifacts: folders for what a panel's work leaves behind (docs/dev/ARTIFACTS.md).
func (r *runner) artifactsObject() map[string]any {
	h := r.r.Host
	need := func() {
		if h == nil {
			r.throw(errNoHost)
		}
	}
	abs := func(p string) string {
		d, err := expandPath(p)
		if err != nil || !filepath.IsAbs(d) {
			r.throw(fmt.Errorf("ctx.artifacts: need an absolute path: %q", p))
		}
		return d
	}
	return map[string]any{
		// ctx.artifacts.folder({ key, name, under? }) → { path, exists, id? }
		"folder": func(arg jsValue) map[string]any {
			need()
			var o struct {
				Key   string `json:"key"`
				Name  string `json:"name"`
				Under string `json:"under"`
			}
			if err := r.opts(arg, &o); err != nil {
				r.throw(fmt.Errorf("ctx.artifacts.folder: %w", err))
			}
			if o.Key == "" {
				r.throw(errors.New("ctx.artifacts.folder: key is required"))
			}
			if o.Name == "" {
				o.Name = o.Key
			}
			out, err := h.ArtifactsFolder(r.project, o.Key, o.Name, o.Under)
			if err != nil {
				r.throw(err)
			}
			return out
		},
		// ctx.artifacts.rename(key, name) → path ("" when it has no folder)
		"rename": func(key, name string) string {
			need()
			p, err := h.ArtifactsRename(r.project, key, name)
			if err != nil {
				r.throw(err)
			}
			return p
		},
		// ctx.artifacts.items(path) → records
		"items": func(p string) []map[string]any {
			need()
			out, err := h.ArtifactsItems(abs(p))
			if err != nil {
				r.throw(err)
			}
			return out
		},
		// ctx.artifacts.record(path, { kind, label?, url?, path?, text?, supersedes? }) → the record
		"record": func(p string, arg jsValue) map[string]any {
			need()
			var it map[string]any
			if err := r.opts(arg, &it); err != nil {
				r.throw(fmt.Errorf("ctx.artifacts.record: %w", err))
			}
			if it == nil {
				it = map[string]any{}
			}
			if _, ok := it["session"]; !ok {
				if c := callerOf(r.ctx); c != "" {
					it["session"] = c
				}
			}
			out, err := h.ArtifactsRecord(abs(p), it)
			if err != nil {
				r.throw(err)
			}
			return out
		},
	}
}

func (r *runner) workspacesObject() map[string]any {
	return map[string]any{
		// ctx.workspaces.list({ all? }) → this project's Code workspaces (every project's with all)
		"list": func(arg jsValue) []map[string]any {
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
		"read": func(p string, arg jsValue) any {
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
