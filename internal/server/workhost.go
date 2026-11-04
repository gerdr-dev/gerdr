package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/i18n"
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
		agent = s.defaultAgent()
	}
	reportTo, err := h.target(project, o.ReportTo)
	if err != nil {
		return nil, err
	}
	// FromCLI: started by code an agent may have written, so a permissive default mode isn't applied on its own (the
	// same rule as `gerdr session new`); an explicit mode still is.
	// The Work's skills go with every session it starts: the worker reads the user's way of working as it needs it
	var skills []string
	if dir, err := s.workDir(project); err == nil {
		skills = []string{filepath.Join(dir, work.SkillsDir)}
	}
	// Its owner: the panel that started it (PanelTag). Bringing it up from anywhere (a notification, the inbox, the
	// running list) goes to that panel, which shows it its own way; it isn't a Code conversation.
	tags := map[string]string{}
	for k, v := range o.Tags {
		tags[k] = v
	}
	if _, ok := tags[PanelTag]; !ok {
		tags[PanelTag] = keyOr(project)
	}
	var config map[string]string
	for k, v := range map[string]string{"model": o.Model, "effort": o.Effort} {
		if v != "" {
			if config == nil {
				config = map[string]string{}
			}
			config[k] = v
		}
	}
	sess, err := s.Hub.Create(ctx, hub.CreateOptions{Agent: agent, Cwd: o.Cwd, Mode: o.Mode, Config: config, Parent: o.Parent, Title: o.Title, Tags: tags, ReportTo: reportTo, SkillDirs: skills, FromCLI: true})
	if err != nil {
		return nil, err
	}
	id := sess.ID()
	// A conversation in the Work directory itself is one of the Work's own chats (the Work view lists those by cwd):
	// it goes in no Code workspace, where it would make a "work" workspace and a tab the user never asked for.
	// workspace "none": the page that started it is where it's followed (a board's worker, shown with the page's own
	// SessionChat), so no tab of it appears in Code; going to it from there opens one then.
	if s.inWorkDir(project, sess.Summary().Cwd) || o.Workspace == "none" {
		return h.view(sess.Summary(), nil), s.sendFirst(ctx, sess, o)
	}
	if err := s.placeSession(project, id, o.Cwd, sess.Summary().Cwd, o.Workspace); err != nil {
		// The conversation exists either way; say where it couldn't go rather than lose it
		s.Log.Warn("work: place session", "id", id, "err", err)
	}
	if o.Focus {
		s.Hub.Broadcast("open", map[string]any{"id": id, "focus": true})
	}
	if err := s.sendFirst(ctx, sess, o); err != nil {
		return nil, err
	}
	return h.view(sess.Summary(), nil), nil
}

// sendFirst sends a started session its first message (the prompt, with attachments), if there is one.
func (s *Server) sendFirst(ctx context.Context, sess *hub.Session, o work.StartOptions) error {
	if strings.TrimSpace(o.Prompt) == "" && len(o.Attachments) == 0 {
		return nil
	}
	blocks, err := withAttachments(o.Prompt, o.Attachments)
	if err == nil {
		_, err = sess.SendFrom(ctx, blocks, "", "", false)
	}
	if err != nil {
		return fmt.Errorf("opened %s but the first message didn't go: %w", sess.ID(), err)
	}
	return nil
}

// inWorkDir: cwd is the project's Work directory (symlinks resolved on both sides).
func (s *Server) inWorkDir(project, cwd string) bool {
	d, err := s.workDir(project)
	if err != nil || cwd == "" {
		return false
	}
	if r, err := filepath.EvalSymlinks(d); err == nil {
		d = r
	}
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = r
	}
	return filepath.Clean(cwd) == filepath.Clean(d)
}

func (h workHost) Session(id string) (map[string]any, error) {
	sess, err := h.s.Hub.Session(id)
	if err != nil {
		return nil, err
	}
	return h.view(sess.Summary(), sess.Events()), nil
}

func (h workHost) Sessions(cwd string, tags map[string]string) []map[string]any {
	list := h.s.Hub.List()
	sort.SliceStable(list, func(i, j int) bool { return list[i].UpdatedAt > list[j].UpdatedAt })
	out := []map[string]any{}
	for _, x := range list {
		if (cwd == "" || x.Cwd == cwd) && hub.HasTags(x.Tags, tags) {
			out = append(out, h.view(x, nil))
		}
	}
	return out
}

// Close is `gerdr session close`: the agent side stops, the record stays and a later Send opens it again.
func (h workHost) Close(ctx context.Context, id string) error {
	sess, err := h.s.Hub.Session(id)
	if err != nil {
		return err
	}
	return h.s.Hub.CloseUI(ctx, sess.ID())
}

func (h workHost) Tag(id string, tags map[string]string) (map[string]any, error) {
	sess, err := h.s.Hub.Session(id)
	if err != nil {
		return nil, err
	}
	if err := sess.SetTags(tags); err != nil {
		return nil, err
	}
	return h.view(sess.Summary(), nil), nil
}

// target resolves "work" (the project's Work agent conversation) for reportTo / send; other ids pass through.
func (h workHost) target(project, id string) (string, error) {
	if id != "work" {
		return id, nil
	}
	if w := h.s.workSession(project); w != "" {
		return w, nil
	}
	return "", errors.New(i18n.S("这个项目的 Work 助手还没有对话：先在 Work 里打开它", "this project's Work agent has no conversation yet: open it in Work first"))
}

func (h workHost) Send(ctx context.Context, project, id, text string, attachments []string, now bool) (map[string]any, error) {
	id, err := h.target(project, id)
	if err != nil {
		return nil, err
	}
	sess, err := h.s.Hub.Session(id)
	if err != nil {
		return nil, err
	}
	blocks, err := withAttachments(text, attachments)
	if err != nil {
		return nil, err
	}
	if _, err := sess.SendFrom(ctx, blocks, "", "", now); err != nil {
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

func (h workHost) Agents() []map[string]any { return h.s.Hub.Agents() }

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
	if x.WaitingSince != 0 {
		v["waitingSince"] = x.WaitingSince
	}
	if x.Turn != nil {
		v["turnStartedAt"] = x.Turn.StartedAt
	}
	v["tags"] = map[string]string{}
	if len(x.Tags) > 0 {
		v["tags"] = x.Tags
	}
	if x.ReportTo != "" {
		v["reportTo"] = x.ReportTo
	}
	if x.Parent != "" {
		v["parent"] = x.Parent
	}
	if x.ByPerson {
		v["byPerson"] = true // the last turn was a person typing in its chat: they saw the reply (hub.Summary.ByPerson)
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
				parts = append(parts, i18n.S("出错：", "Error: ")+e.Error)
			} else if e.StopReason != "" && e.StopReason != "end_turn" {
				parts = append(parts, i18n.S("这一轮结束：", "Turn ended: ")+e.StopReason)
			}
		}
	}
	flush()
	return strings.Join(parts, "\n\n")
}

// PanelTag is the tag naming the panel (its Work key) a conversation was started by (ctx.sessions.start).
const PanelTag = "panel"

// keyOr: a Work key, "" meaning the default project's own Work.
func keyOr(key string) string {
	if key == "" {
		return DefaultSpace
	}
	return key
}

// spaceOr: the layout space of a Work key. A panel (work.SplitKey "<project>/<panel>") is in its project's space: its
// conversations go into that project's Code workspaces.
func spaceOr(project string) string {
	project, _, _ = strings.Cut(project, "/")
	if project == "" {
		return DefaultSpace
	}
	return project
}

// defaultAgent: Settings' default agent (config.json defaultAgent), else claude.
func (s *Server) defaultAgent() string {
	var c struct {
		DefaultAgent string `json:"defaultAgent"`
	}
	if s.Config != nil {
		_ = json.Unmarshal(s.Config.Get(), &c)
	}
	if c.DefaultAgent == "" {
		return "claude"
	}
	return c.DefaultAgent
}

// workSession: the project's current Work agent conversation (config.json workSessions, kept by the UI), "" if none.
func (s *Server) workSession(project string) string {
	var c struct {
		WorkSessions map[string]string `json:"workSessions"`
		WorkSession  string            `json:"workSession"` // before projects: the default project's
	}
	if s.Config != nil {
		_ = json.Unmarshal(s.Config.Get(), &c)
	}
	sp := spaceOr(project)
	if id := c.WorkSessions[sp]; id != "" {
		return id
	}
	if sp == DefaultSpace {
		return c.WorkSession
	}
	return ""
}

// placeSession puts a conversation started by a Work function into its project's Code layout, so it's in the window
// (and kept alive, see layout.ts) whether or not a window is open now: a new tab in workspace wsID, else in the
// workspace whose directory is cwd (or the closest one above it), else in a new workspace for cwd. wsID "new" always
// makes a new one (the user picked that over the match). Tabs stay
// where the user is looking: the active tab doesn't change. real is cwd with symlinks resolved (what the hub keeps:
// /tmp/x is /private/tmp/x on macOS); a workspace's directory is matched against both, as typed in the UI either way.
func (s *Server) placeSession(project, id, cwd, real, wsID string) error {
	layoutMu.Lock()
	defer layoutMu.Unlock()
	raw := s.Layout.Get()
	var l node
	if json.Unmarshal(raw, &l) != nil || len(arr(l, "workspaces")) == 0 {
		return errors.New("no layout yet: open Gerdr's window once")
	}
	space := spaceOr(project)
	c := &layoutCall{l: l, space: space}
	var w node
	best := -1
	for _, x := range c.wss() {
		if spaceOfWs(x) != space || wsID == "new" {
			continue
		}
		if wsID != "" {
			break // looked up below, by id, number or label
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
	if wsID != "" && wsID != "new" {
		var err error
		if w, err = c.workspace(wsID); err != nil {
			return fmt.Errorf("%w (project %q)", err, space)
		}
	}
	if w == nil {
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

// maxInlineImage: a bigger image goes as a file link instead (the agent reads it with its tools), as from the composer.
const maxInlineImage = 10 << 20

// withAttachments is a message the way the composer builds one (packages/chat Composer.send): the attachments first,
// an image as the image itself, anything else as a file:// link, then the text.
func withAttachments(text string, paths []string) ([]acp.ContentBlock, error) {
	var out []acp.ContentBlock
	for _, p := range paths {
		if strings.HasPrefix(p, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, p[2:])
			}
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("attachment %q: an absolute path on this machine (POST /api/fs/upload gives one)", p)
		}
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			return nil, fmt.Errorf("attachment %q: no such file", p)
		}
		mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(p)))
		if i := strings.IndexByte(mt, ';'); i >= 0 {
			mt = mt[:i]
		}
		switch mt {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
			if st.Size() <= maxInlineImage {
				b, err := os.ReadFile(p)
				if err != nil {
					return nil, err
				}
				out = append(out, acp.ImageBlock(base64.StdEncoding.EncodeToString(b), mt))
				continue
			}
		}
		out = append(out, acp.ResourceLinkBlock(filepath.Base(p), "file://"+p))
	}
	if strings.TrimSpace(text) != "" {
		out = append(out, acp.TextBlock(text))
	}
	return out, nil
}

func (s *Server) workDir(project string) (string, error) {
	if s.Work == nil {
		return "", errors.New("no Work runner")
	}
	return s.Work.Dir(project)
}
