package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// sessionRoutes: /api/sessions and the operations on one session (send, answer, rewind, close…). Kept out of
// Handler in server.go, which every new endpoint used to edit, so agents adding unrelated routes collided there.
func (s *Server) sessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions", s.createSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return nil, s.Hub.Delete(r.Context(), sess.ID())
	}))
	mux.HandleFunc("POST /api/sessions/{id}/send", s.sessionOp(s.send))
	mux.HandleFunc("POST /api/sessions/{id}/meta", s.sessionOp(s.meta))
	// 输入框里的 `!命令`：gerdr 在会话目录里直接跑，不经过 agent（hub/shell.go）
	mux.HandleFunc("POST /api/sessions/{id}/shell", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Command string `json:"command"`
			ID      string `json:"id"` // client 生成的 UUID：重发同一条时不跑两遍
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		id, err := sess.Shell(b.Command, b.ID)
		return map[string]any{"id": id}, err
	}))
	mux.HandleFunc("POST /api/sessions/{id}/shell/{run}/stop", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return map[string]any{}, sess.StopShell(r.PathValue("run"))
	}))
	mux.HandleFunc("POST /api/sessions/{id}/cancel", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return nil, sess.Cancel(r.Context())
	}))
	// 全部会话，最近有动静的在前（给外面的程序；界面用的是 /api/hello 那一大包）
	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		list := s.Hub.List()
		sort.SliceStable(list, func(i, j int) bool { return list[i].UpdatedAt > list[j].UpdatedAt })
		out := make([]any, 0, len(list))
		for _, x := range list {
			out = append(out, x)
		}
		// Other computers' sessions after this one's (hosts.go); ?local=1 for this computer's only
		if r.URL.Query().Get("local") != "1" {
			for _, x := range s.hostSessions(r.Context(), 2*time.Second) {
				out = append(out, x)
			}
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("POST /api/sessions/{id}/answer", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			ReqID    string          `json:"reqId"`
			Response json.RawMessage `json:"response"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return nil, sess.Answer(b.ReqID, b.Response)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/mode", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct{ Mode string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return nil, sess.SetMode(r.Context(), b.Mode)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/config", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct{ ID, Value string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return nil, sess.SetConfig(r.Context(), b.ID, b.Value)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/rename", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Title string
			Auto  bool // 插件起的：不算人改的名，人改过名时不生效（hub/title.go）
			Fixed bool // the session's name for good, kept through /clear (hub Record.TitleFixed)
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		if b.Auto {
			sess.SetAutoTitle(b.Title)
		} else {
			sess.Rename(b.Title, b.Fixed)
		}
		return nil, nil
	}))
	// Point the session at another folder (its project folder moved or was renamed): hub Session.SetCwd
	mux.HandleFunc("POST /api/sessions/{id}/cwd", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct{ Cwd string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return sess.Summary(), sess.SetCwd(r.Context(), b.Cwd)
	}))
	// 起标题用的材料（插件 g.sessionTitle）：当前标题、这段上下文（最后一次 /clear 之后）用户说过的话
	mux.HandleFunc("GET /api/sessions/{id}/title", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return sess.TitleState(), nil
	}))
	// 用这个会话的 agent 的账号跑一次小问答（插件的 ctx.summarize）
	mux.HandleFunc("POST /api/sessions/{id}/complete", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct{ Prompt string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		if strings.TrimSpace(b.Prompt) == "" {
			return nil, errors.New(i18n.S("prompt 是空的", "prompt is empty"))
		}
		t, err := s.Hub.Complete(r.Context(), sess.Summary().Agent, b.Prompt)
		return map[string]string{"text": t}, err
	}))
	mux.HandleFunc("POST /api/sessions/{id}/rewind", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			MessageID string `json:"messageId"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return sess.Rewind(r.Context(), b.MessageID)
	}))
	mux.HandleFunc("GET /api/sessions/{id}/events", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return sess.Events(), nil
	}))
	// 界面往上翻到顶：序号 before 之前的再一段（打开会话时 /ws 只推了最近一段，见 hub/blob.go）
	mux.HandleFunc("GET /api/sessions/{id}/events/older", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		before, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		if err != nil {
			return nil, errors.New(i18n.S("before 不对", "invalid before"))
		}
		evs, more := sess.OlderEvents(before)
		return map[string]any{"events": evs, "older": more}, nil
	}))
	mux.HandleFunc("POST /api/sessions/{id}/tasks/{task}/stop", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return map[string]any{}, sess.StopTask(r.Context(), r.PathValue("task"))
	}))
	mux.HandleFunc("POST /api/sessions/{id}/tools/{tool}/background", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		ok, err := sess.BackgroundTool(r.Context(), r.PathValue("tool"))
		return map[string]any{"backgrounded": ok}, err
	}))
	mux.HandleFunc("POST /api/sessions/{id}/allow-rules", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Rules []string `json:"rules"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return map[string]any{}, sess.AllowRules(r.Context(), b.Rules)
	}))
	// Signing the session's agent in again after its login expired (hub/login.go): start (or join the running one),
	// how it stands, the code the sign-in page showed, give up.
	mux.HandleFunc("POST /api/sessions/{id}/login", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return sess.StartLogin()
	}))
	mux.HandleFunc("GET /api/sessions/{id}/login", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return sess.LoginState(), nil
	}))
	mux.HandleFunc("POST /api/sessions/{id}/login/code", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Code string `json:"code"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return map[string]any{}, sess.LoginCode(b.Code)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/login/cancel", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		sess.CancelLogin()
		return map[string]any{}, nil
	}))
	mux.HandleFunc("POST /api/sessions/{id}/escape", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return sess.Escape(ctx)
	}))
	mux.HandleFunc("GET /api/sessions/{id}/files", s.sessionOp(s.searchFiles))
	mux.HandleFunc("POST /api/sessions/{id}/reload", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return nil, sess.Reload(r.Context())
	}))
	mux.HandleFunc("POST /api/sessions/{id}/takeover", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Action string `json:"action"`
		}
		_ = decode(r, &b)
		return nil, sess.Takeover(r.Context(), b.Action)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/warm", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = sess.Warm(ctx) // 不支持的 agent 报错无所谓
		return nil, nil
	}))
	mux.HandleFunc("POST /api/sessions/{id}/withdraw-last", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return sess.WithdrawLast(r.Context())
	}))
	// withdraw：收回指定的一条排队中的插话（对话里「排队中」那行的撤回 / 立即发）
	mux.HandleFunc("POST /api/sessions/{id}/withdraw", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			MessageID string `json:"messageId"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return sess.Withdraw(r.Context(), b.MessageID)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/open", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Near  string `json:"near"`
			Focus bool   `json:"focus"`
		}
		_ = decode(r, &b)
		if err := s.crossSpace(b.Near, sess.ID()); err != nil {
			return nil, err
		}
		s.Hub.Broadcast("open", map[string]any{"id": sess.Summary().ID, "near": b.Near, "focus": b.Focus})
		return nil, nil
	}))
	// close：open 的反面（gerdr session close），见 Hub.CloseUI。afterTurn：会话关自己，等它这一轮跑完再关（Hub.CloseAfterTurn）
	mux.HandleFunc("POST /api/sessions/{id}/close", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			AfterTurn bool   `json:"afterTurn"`
			From      string `json:"from"` // the agent session asking (gerdr session close); empty from the UI
		}
		_ = decode(r, &b)
		if err := s.crossSpace(b.From, sess.ID()); err != nil {
			return nil, err
		}
		if b.AfterTurn {
			now, err := s.Hub.CloseAfterTurn(r.Context(), sess.ID())
			return map[string]any{"closedNow": now}, err
		}
		return nil, s.Hub.CloseUI(r.Context(), sess.ID())
	}))
	// shutdown：界面关 pane / tab 时调，只关 agent 那边、不广播（布局已经是它自己改的）。
	mux.HandleFunc("POST /api/sessions/{id}/shutdown", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return nil, s.Hub.Shutdown(r.Context(), sess.ID())
	}))
	mux.HandleFunc("GET /api/sessions/{id}", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return sess.Summary(), nil
	}))
	// resolve：agent 跑的 `gerdr session current` 拿自己的 agent 会话 id 换 gerdr 会话。
	mux.HandleFunc("GET /api/resolve", func(w http.ResponseWriter, r *http.Request) {
		for _, agent := range []string{"claude", "codex", "native"} {
			if id := r.URL.Query().Get(agent); id != "" {
				if sess := s.Hub.FindByAgent(agent, id); sess != nil {
					writeJSON(w, sess.Summary())
					return
				}
			}
		}
		httpError(w, 404, i18n.S("不是 Gerdr 里的会话", "Not a Gerdr session"))
	})
}

// createOpen：在界面上打开它。Near 是「放在哪个会话旁边」（agent 开的会话放在它自己旁边），
// Focus 要不要把焦点切过去（agent 开的默认不切，别打断人）。界面没开着就什么都不发生。
type createOpen struct {
	Near  string `json:"near"`
	Focus bool   `json:"focus"`
	// Workspace: put it in this Code workspace of the caller's project (id, number or label; "new"), as a
	// new tab, instead of next to Near. Placed by the server, so it works with no window open.
	Workspace string `json:"workspace"`
	Caller
}

// placeCreated puts a session `gerdr session new` started on screen (o non-nil): in a workspace, or next to the
// caller. reqCwd: the directory asked for (placeSession matches workspaces by it). Also for sessions started on
// another computer (hosts.go): the screen is this one's.
func (s *Server) placeCreated(o *createOpen, id, reqCwd, cwd string) error {
	if o == nil {
		return nil
	}
	if o.Workspace != "" {
		c := o.Caller
		if c.Session == "" {
			c.Session = o.Near
		}
		if self, err := s.Hub.Session(c.Session); err == nil {
			c.SessionCwd = self.Summary().Cwd
		}
		space := ResolveSpace(s.Layout.Get(), c, s.workProject())
		if err := s.placeSession(space, id, reqCwd, cwd, o.Workspace); err != nil {
			// The conversation exists either way: say so instead of losing it
			return fmt.Errorf(i18n.S("会话 %s 开好了，但放不进工作空间：%v", "session %s was started but couldn't go in the workspace: %v"), id, err)
		}
		if o.Focus {
			s.Hub.Broadcast("open", map[string]any{"id": id, "focus": true})
		}
		return nil
	}
	s.Hub.Broadcast("open", map[string]any{"id": id, "near": o.Near, "focus": o.Focus})
	return nil
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var o struct {
		hub.CreateOptions
		Open *createOpen `json:"open"`
	}
	if err := decode(r, &o); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	// 带 open 的是 `gerdr session new`（agent 跑的），界面自己开会话不带
	o.CreateOptions.FromCLI = o.Open != nil
	// Started by an agent: that agent's session is its parent (open.near is the caller's own session), so who handed
	// what to whom can be followed any number of levels down
	if o.Open != nil && o.Parent == "" {
		o.Parent = o.Open.Near
	}
	if o.Agent == "" {
		o.Agent = s.defaultAgent() // `gerdr session new` without --agent: what new sessions use in the UI
	}
	if panel, _ := s.panelCaller(o.Open); panel != "" {
		httpError(w, 400, errPanelSessionNew(panel).Error())
		return
	}
	if o.Open != nil && o.ReportTo != "" {
		// The new session isn't anywhere yet: it lands in the caller's project, so the target has to be reachable from there
		if err := s.crossSpace(o.Open.Near, o.ReportTo); err != nil {
			httpError(w, 400, err.Error())
			return
		}
	}
	if o.Open != nil && o.Project == "" {
		// It lands in the caller's project (placeCreated), so that project's skills / MCP apply from the start
		c := o.Open.Caller
		if c.Session == "" {
			c.Session = o.Open.Near
		}
		if self, err := s.Hub.Session(c.Session); err == nil {
			c.SessionCwd = self.Summary().Cwd
		}
		o.Project = ResolveSpace(s.Layout.Get(), c, s.workProject())
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	sess, err := s.Hub.Create(ctx, o.CreateOptions)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	sum := sess.Summary()
	if err := s.placeCreated(o.Open, sum.ID, o.Cwd, sum.Cwd); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	writeJSON(w, sum)
}

// panelCaller: the panel whose conversation (one it started: a board's worker, at any depth) runs `gerdr session new`,
// and that conversation's id; "" when the caller isn't one of a panel's.
//
// Such a conversation can't start a session here: a panel looks after the agents it runs (what they're told, which
// model, how they report, where they show), and one started past it has none of that — a board worker that handed
// its work on with `session new` left a conversation nobody followed, on no board and closed with the old task
// (2026-11-05). It starts agents the panel's way (its functions call ctx.sessions.start, which keeps the chain through
// parent). The one thing a panel can't do is start one on another computer: there `session new --host` is allowed as a
// helper that reports back to the caller (createOnHost), which the caller follows and the panel shows as its wait.
func (s *Server) panelCaller(o *createOpen) (panel, caller string) {
	if o == nil {
		return "", ""
	}
	caller = o.Near
	if caller == "" {
		caller = o.Caller.Session
	}
	sess, err := s.Hub.Session(caller)
	if err != nil {
		return "", ""
	}
	return sess.Summary().Tags[PanelTag], caller
}

func errPanelSessionNew(panel string) error {
	return fmt.Errorf(i18n.S(
		"你是面板「%s」开的对话，不能用 gerdr session new 开新会话：面板要管它开的 agent（交代什么、用哪个模型、怎么汇报、显示在哪）。要开新 agent，照这个面板给你的说明，用它自己的函数或工具。只有在另一台电脑上开、结果回报给你的帮手可以用 gerdr session new --host <电脑> --report-to self。",
		"You are a conversation the panel %q started, so gerdr session new is refused: the panel looks after the agents it runs (what they're told, which model, how they report, where they show). To start another agent, do it the panel's way, through its own functions or tools, as its instructions say. Only a helper on another computer that reports back to you may be started with gerdr session new --host <computer> --report-to self."),
		panel)
}

// meta: `gerdr session tag` / `report-to`, ctx.sessions.tag. Tags: "" removes a key. ReportTo: "" stops reports;
// left out (null) = unchanged.
func (s *Server) meta(r *http.Request, sess *hub.Session) (any, error) {
	var b struct {
		Tags     map[string]string `json:"tags"`
		ReportTo *string           `json:"reportTo"`
		From     string            `json:"from"` // the agent session asking
	}
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	if err := s.crossSpace(b.From, sess.ID()); err != nil {
		return nil, err
	}
	if len(b.Tags) > 0 {
		if err := sess.SetTags(b.Tags); err != nil {
			return nil, err
		}
	}
	if b.ReportTo != nil {
		to := *b.ReportTo
		if to != "" {
			// A session on another computer is checked by the hub (Hub.Elsewhere); one here gets its canonical id
			if t, err := s.Hub.Session(to); err == nil {
				to = t.ID()
				if err := s.crossSpace(sess.ID(), to); err != nil {
					return nil, err
				}
			}
		}
		if err := sess.SetReportTo(to); err != nil {
			return nil, err
		}
	}
	return sess.Summary(), nil
}

func (s *Server) send(r *http.Request, sess *hub.Session) (any, error) {
	var b struct {
		Content []acp.ContentBlock `json:"content"`
		ID      string             `json:"id"`   // client 生成的 UUID：重发同一条时去重
		From    string             `json:"from"` // 别的会话经 gerdr session send 发来的：发送方会话 id
		Now     bool               `json:"now"`  // 在跑时立即插话（截断当前这次生成），默认排到下一个空档
		Fold    string             `json:"fold"` // the chat draws it folded to this line (chatlog.Event.Fold)
	}
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	// Empty content is allowed only when pending `!` output goes with it; the hub decides (Session.SendFrom)
	if err := s.crossSpace(b.From, sess.ID()); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return sess.SendWith(ctx, b.Content, hub.SendOpts{ClientID: b.ID, From: b.From, Urgent: b.Now, Fold: b.Fold})
}

func (s *Server) sessionOp(fn func(*http.Request, *hub.Session) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.Hub.Session(r.PathValue("id"))
		if err != nil {
			httpError(w, 404, err.Error())
			return
		}
		res, err := fn(r, sess)
		var held *hub.HeldError
		if errors.As(err, &held) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "code": "held_elsewhere", "holders": held.Holders})
			return
		}
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if res == nil {
			res = map[string]any{"ok": true}
		}
		writeJSON(w, res)
	}
}
