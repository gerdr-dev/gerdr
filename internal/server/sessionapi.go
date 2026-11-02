package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/hub"
)

// sessionRoutes: /api/sessions and the operations on one session (send, answer, rewind, close…). Kept out of
// Handler in server.go, which every new endpoint used to edit, so agents adding unrelated routes collided there.
func (s *Server) sessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions", s.createSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return nil, s.Hub.Delete(r.Context(), sess.ID())
	}))
	mux.HandleFunc("POST /api/sessions/{id}/send", s.sessionOp(s.send))
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
		writeJSON(w, list)
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
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		if b.Auto {
			sess.SetAutoTitle(b.Title)
		} else {
			sess.Rename(b.Title)
		}
		return nil, nil
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
			return nil, errors.New("prompt 是空的")
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
			return nil, errors.New("before 不对")
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
		httpError(w, 404, "不是 Gerdr 里的会话")
	})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var o struct {
		hub.CreateOptions
		// Open：在界面上打开它。Near 是「放在哪个会话旁边」（agent 开的会话放在它自己旁边），
		// Focus 要不要把焦点切过去（agent 开的默认不切，别打断人）。界面没开着就什么都不发生。
		Open *struct {
			Near  string `json:"near"`
			Focus bool   `json:"focus"`
		} `json:"open"`
	}
	if err := decode(r, &o); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	// 带 open 的是 `gerdr session new`（agent 跑的），界面自己开会话不带
	o.CreateOptions.FromCLI = o.Open != nil
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	sess, err := s.Hub.Create(ctx, o.CreateOptions)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if o.Open != nil {
		s.Hub.Broadcast("open", map[string]any{"id": sess.Summary().ID, "near": o.Open.Near, "focus": o.Open.Focus})
	}
	writeJSON(w, sess.Summary())
}

func (s *Server) send(r *http.Request, sess *hub.Session) (any, error) {
	var b struct {
		Content []acp.ContentBlock `json:"content"`
		ID      string             `json:"id"`   // client 生成的 UUID：重发同一条时去重
		From    string             `json:"from"` // 别的会话经 gerdr session send 发来的：发送方会话 id
		Now     bool               `json:"now"`  // 在跑时立即插话（截断当前这次生成），默认排到下一个空档
	}
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	if len(b.Content) == 0 {
		return nil, errors.New("empty message")
	}
	if err := s.crossSpace(b.From, sess.ID()); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return sess.SendFrom(ctx, b.Content, b.ID, b.From, b.Now)
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
