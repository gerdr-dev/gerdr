package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/work"
)

// workHost is what a Work page's local functions reach through ctx.sessions / ctx.workspaces / ctx.dirs.recent
// (work.Host): the hub and the layout, called in-process. A project here is a layout space (the Work project's id is
// its space's id).
type workHost struct{ s *Server }

var _ work.Host = workHost{}

func (h workHost) StartSession(ctx context.Context, project string, o work.StartOptions) (map[string]any, error) {
	s := h.s
	agent := o.Agent
	if agent == "" {
		var c struct {
			DefaultAgent string `json:"defaultAgent"`
		}
		if s.Config != nil {
			_ = json.Unmarshal(s.Config.Get(), &c)
		}
		agent = c.DefaultAgent
		if agent == "" {
			agent = "claude"
		}
	}
	// FromCLI: started by code an agent may have written, so a permissive default mode isn't applied on its own (the
	// same rule as `gerdr session new`); an explicit mode still is.
	sess, err := s.Hub.Create(ctx, hub.CreateOptions{Agent: agent, Cwd: o.Cwd, Mode: o.Mode, Title: o.Title, FromCLI: true})
	if err != nil {
		return nil, err
	}
	id := sess.ID()
	if err := s.placeSession(project, id, o.Cwd, sess.Summary().Cwd, o.Workspace); err != nil {
		// The conversation exists either way; say where it couldn't go rather than lose it
		s.Log.Warn("work: place session", "id", id, "err", err)
	}
	if o.Focus {
		s.Hub.Broadcast("open", map[string]any{"id": id, "focus": true})
	}
	if strings.TrimSpace(o.Prompt) != "" {
		if _, err := sess.SendFrom(ctx, []acp.ContentBlock{acp.TextBlock(o.Prompt)}, "", "", false); err != nil {
			return nil, fmt.Errorf("opened %s but the first message didn't go: %w", id, err)
		}
	}
	return h.view(sess.Summary(), nil), nil
}

func (h workHost) Session(id string) (map[string]any, error) {
	sess, err := h.s.Hub.Session(id)
	if err != nil {
		return nil, err
	}
	return h.view(sess.Summary(), sess.Events()), nil
}

func (h workHost) Sessions(cwd string) []map[string]any {
	list := h.s.Hub.List()
	sort.SliceStable(list, func(i, j int) bool { return list[i].UpdatedAt > list[j].UpdatedAt })
	out := []map[string]any{}
	for _, x := range list {
		if cwd == "" || x.Cwd == cwd {
			out = append(out, h.view(x, nil))
		}
	}
	return out
}

func (h workHost) Send(ctx context.Context, id, text string) (map[string]any, error) {
	sess, err := h.s.Hub.Session(id)
	if err != nil {
		return nil, err
	}
	if _, err := sess.SendFrom(ctx, []acp.ContentBlock{acp.TextBlock(text)}, "", "", false); err != nil {
		return nil, err
	}
	return h.view(sess.Summary(), nil), nil
}

func (h workHost) Workspaces(project string, all bool) []map[string]any {
	var l node
	if json.Unmarshal(h.s.Layout.Get(), &l) != nil {
		return []map[string]any{}
	}
	byID := map[string]hub.Summary{}
	for _, x := range h.s.Hub.List() {
		byID[x.ID] = x
	}
	c := &layoutCall{l: l}
	out := []map[string]any{}
	for _, w := range c.wss() {
		if !all && spaceOfWs(w) != spaceOr(project) {
			continue
		}
		sessions := []map[string]any{}
		for _, t := range tabsOf(w) {
			for _, p := range leaves(asNode(t["root"])) {
				if x, ok := byID[str(p, "sessionId")]; ok {
					sessions = append(sessions, h.view(x, nil))
				}
			}
		}
		out = append(out, map[string]any{"id": str(w, "id"), "name": str(w, "name"), "cwd": str(w, "cwd"), "project": spaceOfWs(w), "sessions": sessions})
	}
	return out
}

func (h workHost) RecentDirs() []map[string]any {
	last := map[string]int64{}
	src := map[string]string{}
	for _, x := range h.s.Hub.List() {
		if x.Cwd == "" {
			continue
		}
		if wp := h.s.workProject(); wp != nil {
			if _, ok := wp(x.Cwd); ok {
				continue // a Work agent's conversation: gerdr's own directory, not a project
			}
		}
		if x.UpdatedAt > last[x.Cwd] {
			last[x.Cwd] = x.UpdatedAt
		}
		src[x.Cwd] = "session"
	}
	var l node
	if json.Unmarshal(h.s.Layout.Get(), &l) == nil {
		for _, w := range (&layoutCall{l: l}).wss() {
			if c := str(w, "cwd"); c != "" {
				if _, ok := src[c]; !ok {
					src[c] = "workspace"
				}
			}
		}
	}
	out := []map[string]any{}
	for p, s := range src {
		out = append(out, map[string]any{"path": p, "lastUsed": last[p], "source": s})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i]["lastUsed"].(int64), out[j]["lastUsed"].(int64)
		if a != b {
			return a > b
		}
		return out[i]["path"].(string) < out[j]["path"].(string)
	})
	return out
}

// view is a session as a function sees it. With the log, reply is what the agent said since the last message to it.
func (h workHost) view(x hub.Summary, evs []*hub.Event) map[string]any {
	v := map[string]any{
		"id": x.ID, "title": x.Title, "agent": x.Agent, "cwd": x.Cwd, "status": x.Status,
		"createdAt": x.CreatedAt, "updatedAt": x.UpdatedAt,
	}
	if x.Waiting != "" {
		v["waiting"] = x.Waiting
	}
	if x.Error != "" {
		v["error"] = x.Error
	}
	if evs != nil {
		v["reply"] = lastReply(evs)
	}
	return v
}

// lastReply: the agent's text after the last user message, plus how a turn ended when it didn't end normally — the
// same as `gerdr session wait` prints (cmd/gerdr/session.go replySince). Subagents' text isn't the agent's answer.
func lastReply(evs []*hub.Event) string {
	from := 0
	for i, e := range evs {
		if e.Kind == hub.EvUser {
			from = i + 1
		}
	}
	var parts []string
	var cur strings.Builder
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			parts = append(parts, t)
		}
		cur.Reset()
	}
	for _, e := range evs[from:] {
		switch e.Kind {
		case hub.EvUpdate:
			var u struct {
				Kind    string `json:"sessionUpdate"`
				Content struct {
					Text string `json:"text"`
				} `json:"content"`
				Meta map[string]any `json:"_meta"`
			}
			if json.Unmarshal(e.Update, &u) != nil {
				continue
			}
			if g, ok := u.Meta["gerdr"].(map[string]any); ok {
				if _, sub := g["subagent"]; sub {
					continue
				}
				if _, child := g["parentToolCallId"]; child {
					continue
				}
			}
			switch u.Kind {
			case "agent_message_chunk":
				cur.WriteString(u.Content.Text)
			case "tool_call":
				flush() // text before and after a tool call are separate paragraphs
			}
		case hub.EvTurnEnd:
			flush()
			if e.Error != "" {
				parts = append(parts, "出错："+e.Error)
			} else if e.StopReason != "" && e.StopReason != "end_turn" {
				parts = append(parts, "这一轮结束："+e.StopReason)
			}
		}
	}
	flush()
	return strings.Join(parts, "\n\n")
}

func spaceOr(project string) string {
	if project == "" {
		return DefaultSpace
	}
	return project
}

// placeSession puts a conversation started by a Work function into its project's Code layout, so it's in the window
// (and kept alive, see layout.ts) whether or not a window is open now: a new tab in workspace wsID, else in the
// workspace whose directory is cwd (or the closest one above it), else in a new workspace for cwd. Tabs stay
// where the user is looking: the active tab doesn't change. real is cwd with symlinks resolved (what the hub keeps:
// /tmp/x is /private/tmp/x on macOS); a workspace's directory is matched against both, as typed in the UI either way.
func (s *Server) placeSession(project, id, cwd, real, wsID string) error {
	layoutMu.Lock()
	defer layoutMu.Unlock()
	raw := s.Layout.Get()
	var l node
	if json.Unmarshal(raw, &l) != nil || len(arr(l, "workspaces")) == 0 {
		return errors.New("no layout yet: open gerdr's window once")
	}
	space := spaceOr(project)
	c := &layoutCall{l: l, space: space}
	var w node
	best := -1
	for _, x := range c.wss() {
		if spaceOfWs(x) != space {
			continue
		}
		if wsID != "" {
			if str(x, "id") == wsID {
				w = x
				break
			}
			continue
		}
		wc := strings.TrimSuffix(str(x, "cwd"), "/")
		if wc == "" {
			continue
		}
		if wc == cwd || wc == real {
			w = x
			break
		}
		if (strings.HasPrefix(cwd, wc+"/") || strings.HasPrefix(real, wc+"/")) && len(wc) > best {
			w, best = x, len(wc)
		}
	}
	if w == nil {
		if wsID != "" {
			return fmt.Errorf("no workspace %q in project %q", wsID, space)
		}
		w, _ = c.newWorkspace(filepath.Base(cwd), cwd, space)
	}
	p := node{"type": "pane", "id": rid("p"), "sessionId": id}
	t := node{"id": rid("t"), "root": p, "focus": p["id"]}
	c.insertTab(w, t, 0)
	c.normalize()
	b, err := json.Marshal(c.l)
	if err != nil {
		return err
	}
	out := json.RawMessage(b) // not []byte: that would go out to the windows as a base64 string, which they drop
	if err := s.Layout.Set(out); err != nil {
		return err
	}
	if s.Hub != nil {
		s.Hub.Broadcast("layout", map[string]any{"data": out, "client": "gerdr-work"})
	}
	return nil
}
