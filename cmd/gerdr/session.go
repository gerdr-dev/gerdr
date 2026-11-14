package main

// gerdr session …：给 gerdr 里跑着的 agent 用的命令行 —— 一个会话里的 agent 可以列出别的会话、开新会话、
// 给它们派活、等它们跑完、读它们说了什么（和 herdr 的 `herdr agent …` 是同一个用途，内置的 gerdr skill 教 agent 怎么用）。
//
// 找服务：GERDR_DIR（gerdr 起 agent 时塞进去的）下的 server.json（端口）和 token。
// 认自己：claude 给命令的环境里有 CLAUDE_CODE_SESSION_ID、codex 有 CODEX_THREAD_ID，拿它们问服务端换 gerdr 会话 id。
// 输出一律是 JSON（agent 读起来不用猜格式）；出错时 stderr 一行 JSON、退出码 1。

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/work"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/server"
	"github.com/gerdr-dev/gerdr/internal/webui"
)

type apiClient struct {
	base  string
	token string
	http  *http.Client
	self  *string // selfID, cached: list() needs it on every call
}

// projectFlag is --project on the session / workspace / tab / pane commands: which project to act in when it can't be told
// from where the command runs (a shell outside gerdr). Inside gerdr it's found by itself (server.ResolveSpace).
var projectFlag string

func addProjectFlag(cmd *cobra.Command) *cobra.Command {
	cmd.PersistentFlags().StringVar(&projectFlag, "project", "", "project to act in (default: the one this agent / terminal is in)")
	return cmd
}

// caller: who's running this command, for the server to work out the project (server.ResolveSpace).
func (c *apiClient) caller() server.Caller {
	return server.Caller{Project: projectFlag, Session: c.selfID(), Term: os.Getenv("GERDR_TERM")}
}

// workProject: the project whose Work directory a path is in (the Work agent's cwd), as the server sees it.
func workProject() func(string) (string, bool) {
	return work.New(config.FromEnv().Dir, nil).WorkProject
}

func newAPIClient() (*apiClient, error) {
	env := config.FromEnv()
	b, err := os.ReadFile(infoPath(env))
	if err != nil {
		return nil, fmt.Errorf(i18n.S("Gerdr 没在运行（找不到 %s）", "Gerdr is not running (%s not found)"), infoPath(env))
	}
	var si serverInfo
	if err := json.Unmarshal(b, &si); err != nil {
		return nil, err
	}
	tok := env.Token
	if tok == "" {
		tb, err := os.ReadFile(filepath.Join(env.Dir, "token"))
		if err != nil {
			return nil, fmt.Errorf(i18n.S("读不到登录令牌：%w", "cannot read the login token: %w"), err)
		}
		tok = strings.TrimSpace(string(tb))
	}
	host := si.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return &apiClient{base: fmt.Sprintf("http://%s:%d", host, si.Port), token: tok, http: &http.Client{Timeout: 3 * time.Minute}}, nil
}

func (c *apiClient) do(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Gerdr", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

type sessInfo struct {
	ID      string `json:"id"`
	Agent   string `json:"agent"`
	Cwd     string `json:"cwd"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Pending int    `json:"pending"`
	Waiting string `json:"waiting"`
	Error   string `json:"error,omitempty"`
	Login   string `json:"login,omitempty"` // Error is the agent's login failing: `gerdr session login` signs it in
	Updated int64  `json:"updatedAt"`
	Tasks   []struct {
		Status string `json:"status"`
	} `json:"tasks"`
	Tags         map[string]string `json:"tags"`
	ReportTo     string            `json:"reportTo"`
	WaitingSince int64             `json:"waitingSince"`
	Turn         *struct {
		StartedAt int64 `json:"startedAt"`
	} `json:"turn"`
	// Host: the other computer it runs on (docs/dev/MULTIHOST.md); nil = this one
	Host *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"host"`
	// inUI：界面上有没有 pane 显示它。和 herdr 一样，「开着的会话」就是界面上看得见的；关掉 pane = 关掉会话
	// （记录留着，能接着聊，只是默认不列、不算）。
	inUI *bool
}

// errNoSession is also what an agent gets for a session open in another herdr-style session (list leaves those out):
// to an agent, other sessions don't exist, same as in herdr.
func errNoSession(ref string) error {
	return fmt.Errorf(i18n.S("没有会话 %q（gerdr session list 看有哪些）", "no session %q (gerdr session list shows what there is)"), ref)
}

func (s sessInfo) view(self string) map[string]any {
	// updatedAt（unix ms）：这个会话最后一次有动静（发消息、agent 输出、审批……）的时间 —— 相当于 herdr agent.list 的 state_change_seq，
	// 拿它就能把「最近动过的」排前面（list 已经按它排好了）
	m := map[string]any{"id": s.ID, "agent": s.Agent, "cwd": s.Cwd, "title": s.Title, "status": s.Status, "updatedAt": s.Updated}
	if s.inUI != nil && !*s.inUI {
		m["closed"] = true // 界面上没有 pane 显示它（被关掉了）；--all 才列出来
	}
	if s.Pending > 0 {
		m["waiting_for_human"] = s.Pending
		m["waiting"] = s.Waiting // 等的是什么，一句话：「回答：下一步做什么？（A / B）」「审批：Write x.txt」
	}
	if s.Pending > 0 && s.WaitingSince > 0 {
		m["waitingSince"] = s.WaitingSince // unix ms: since when it's been waiting for a person
	}
	if s.Turn != nil {
		m["turnStartedAt"] = s.Turn.StartedAt // unix ms: when the running turn started
	}
	if len(s.Tags) > 0 {
		m["tags"] = s.Tags
	}
	if s.ReportTo != "" {
		m["reportTo"] = s.ReportTo
	}
	if s.Host != nil {
		m["host"] = s.Host.ID // runs on another computer (gerdr host list); its paths are that computer's
	}
	if s.Error != "" {
		m["error"] = s.Error
	}
	if s.Login != "" {
		m["login"] = s.Login
	}
	running := 0
	for _, t := range s.Tasks {
		if t.Status == "running" {
			running++
		}
	}
	if running > 0 {
		m["background_tasks"] = running
	}
	if s.ID == self && self != "" {
		m["self"] = true
	}
	return m
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// selfID：调用这条命令的 agent 所在的 gerdr 会话（不在 gerdr 里就是空）。
func (c *apiClient) selfID() string {
	if c.self == nil {
		id := c.lookupSelf()
		c.self = &id
	}
	return *c.self
}

func (c *apiClient) lookupSelf() string {
	q := url.Values{}
	if v := os.Getenv("CLAUDE_CODE_SESSION_ID"); v != "" {
		q.Set("claude", v)
	}
	if v := os.Getenv("CODEX_THREAD_ID"); v != "" {
		q.Set("codex", v)
	}
	if v := os.Getenv("GERDR_NATIVE_SESSION_ID"); v != "" {
		q.Set("native", v)
	}
	if len(q) == 0 {
		return ""
	}
	var s sessInfo
	if c.do("GET", "/api/resolve?"+q.Encode(), nil, &s) != nil {
		return ""
	}
	return s.ID
}

// list: the sessions the caller may see. An agent only sees its own project (server.ResolveSpace: the pane it's in, its Work
// directory, its terminal, --project): sessions in another one are left out, like herdr where each session has its own socket.
// Closed sessions (no pane anywhere) belong to no session and stay visible; opening one puts it next to the caller, in the caller's.
func (c *apiClient) list() ([]sessInfo, error) {
	var h struct {
		Sessions []sessInfo      `json:"sessions"`
		Layout   json.RawMessage `json:"layout"`
	}
	// Only what this needs; a Gerdr from before ?only sends everything, which decodes the same
	if err := c.do("GET", "/api/hello?only=sessions,layout", nil, &h); err != nil {
		return nil, err
	}
	spaces := server.SessionSpaces(h.Layout)
	wp := workProject()
	who := c.caller()
	for _, s := range h.Sessions {
		if s.ID == who.Session {
			who.SessionCwd = s.Cwd
		}
	}
	mine := server.ResolveSpace(h.Layout, who, wp)
	out := h.Sessions[:0]
	for _, s := range h.Sessions {
		sp, open := spaces[s.ID]
		// A Work agent is in no pane but belongs to its project all the same
		owned := open
		if !owned {
			sp, owned = wp(s.Cwd)
		}
		if mine != "" && owned && sp != mine {
			continue
		}
		s.inUI = &open
		out = append(out, s)
	}
	return out, nil
}

// resolve：会话可以写完整 id、id 前缀（至少 4 位）或者标题（完全一样）。
func (c *apiClient) resolve(ref string) (sessInfo, error) {
	list, err := c.list()
	if err != nil {
		return sessInfo{}, err
	}
	var hits []sessInfo
	for _, s := range list {
		if s.ID == ref {
			return s, nil
		}
		if (len(ref) >= 4 && strings.HasPrefix(s.ID, ref)) || s.Title == ref {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return sessInfo{}, errNoSession(ref)
	}
	return sessInfo{}, fmt.Errorf(i18n.S("%q 对应好几个会话，用完整 id", "%q matches several sessions; use the full id"), ref)
}

func (c *apiClient) get(id string) (sessInfo, error) {
	var s sessInfo
	err := c.do("GET", "/api/sessions/"+id, nil, &s)
	return s, err
}

// ---- 读对话 ----

type hubEvent struct {
	Seq        int64             `json:"seq"`
	Kind       string            `json:"kind"`
	MessageID  string            `json:"messageId"`
	Content    []json.RawMessage `json:"content"`
	From       string            `json:"from"`
	Update     json.RawMessage   `json:"update"`
	StopReason string            `json:"stopReason"`
	Error      string            `json:"error"`
	Auto       bool              `json:"auto"`
	Text       string            `json:"text"`
	Request    json.RawMessage   `json:"request"`
	ReqID      string            `json:"reqId"`
	Response   json.RawMessage   `json:"response"`
}

type entry struct {
	Role string `json:"role"` // user / agent / tool / notice / waiting（还在等人）/ answered（等过、已经处理了）
	Text string `json:"text"`
	From string `json:"from,omitempty"`
	seq  int64
}

// transcript 把会话日志读成一串人话：用户说的、agent 说的、调了什么工具、出了什么错。
// 子 agent 的话（_meta.gerdr.subagent）不算进来，和界面上的主对话一致。
func transcript(evs []hubEvent) []entry {
	var out []entry
	push := func(e entry) { out = append(out, e) }
	appendText := func(role, text string, seq int64) {
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Text += text
			return
		}
		push(entry{Role: role, Text: text, seq: seq})
	}
	waitAt := map[string]int{}      // reqId → out 里那一条
	waitDesc := map[string]string{} // reqId → 问的是什么（答过之后换个前缀重写那一条，不用去匹配文字）
	for _, e := range evs {
		switch e.Kind {
		case "user":
			var parts []string
			for _, raw := range e.Content {
				var b struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				_ = json.Unmarshal(raw, &b)
				switch b.Type {
				case "text":
					parts = append(parts, b.Text)
				case "image":
					parts = append(parts, i18n.S("[图片]", "[image]"))
				}
			}
			push(entry{Role: "user", Text: strings.Join(parts, "\n"), From: e.From, seq: e.Seq})
		case "update":
			var u struct {
				Kind    string `json:"sessionUpdate"`
				Content struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Title  string         `json:"title"`
				Status string         `json:"status"`
				Meta   map[string]any `json:"_meta"`
			}
			_ = json.Unmarshal(e.Update, &u)
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
				appendText("agent", u.Content.Text, e.Seq)
			case "tool_call":
				push(entry{Role: "tool", Text: u.Title, seq: e.Seq})
			}
		case "turn_end":
			if e.Error != "" {
				push(entry{Role: "notice", Text: i18n.S("出错：", "Error: ") + e.Error, seq: e.Seq})
			} else if e.StopReason != "" && e.StopReason != "end_turn" {
				push(entry{Role: "notice", Text: i18n.S("这一轮结束：", "Turn ended: ") + e.StopReason, seq: e.Seq})
			}
		case "notice":
			push(entry{Role: "notice", Text: e.Text, seq: e.Seq})
		case "permission", "ask":
			// 带上问的是什么（DescribeRequest）；答过的后面会改成「答过了」—— 原来一律写「在等人回答问题」，
			// 早就答过的也这么写，读的 agent 以为对方还卡着
			waitAt[e.ReqID] = len(out)
			desc := acpx.DescribeRequest(e.Kind, e.Request)
			waitDesc[e.ReqID] = desc
			push(entry{Role: "waiting", Text: i18n.S("在等人", "Waiting · ") + desc, seq: e.Seq})
		case "permission_done", "ask_done":
			if i, ok := waitAt[e.ReqID]; ok && i < len(out) {
				out[i].Role = "answered"
				out[i].Text = i18n.S("已处理 · ", "Handled · ") + waitDesc[e.ReqID] + answerNote(e.Response)
			}
		}
	}
	for i := range out {
		out[i].Text = strings.TrimSpace(out[i].Text)
	}
	return out
}

func (c *apiClient) events(id string) ([]hubEvent, error) {
	var evs []hubEvent
	err := c.do("GET", "/api/sessions/"+id+"/events", nil, &evs)
	return evs, err
}

// replySince：seq 之后 agent 说的话（最后一轮的回复）。
func replySince(evs []hubEvent, seq int64) string {
	var after []hubEvent
	for _, e := range evs {
		if e.Seq > seq {
			after = append(after, e)
		}
	}
	var parts []string
	for _, en := range transcript(after) {
		if en.Role == "agent" || en.Role == "notice" {
			parts = append(parts, en.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func lastSeq(evs []hubEvent) int64 {
	if len(evs) == 0 {
		return 0
	}
	return evs[len(evs)-1].Seq
}

// wait：等它停下来（不在跑了，或者在等人审批 / 回答）。
func (c *apiClient) wait(id string, timeout time.Duration) (sessInfo, bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		s, err := c.get(id)
		if err != nil {
			return s, false, err
		}
		if s.Status != "running" && s.Status != "starting" {
			return s, true, nil
		}
		if timeout > 0 && time.Now().After(deadline) {
			return s, false, nil
		}
		time.Sleep(time.Second)
	}
}

func waitResult(c *apiClient, id string, since int64, timeout time.Duration) error {
	s, settled, err := c.wait(id, timeout)
	if err != nil {
		return err
	}
	evs, err := c.events(id)
	if err != nil {
		return err
	}
	out := map[string]any{"session": s.view(""), "reply": replySince(evs, since)}
	if !settled {
		out["timed_out"] = true
	}
	if s.Status == "waiting" {
		out["note"] = i18n.S("它在等人审批或回答问题：让用户去 Gerdr 界面上处理", "It is waiting for the user to approve or answer a question: ask the user to handle it in the Gerdr UI")
	}
	printJSON(out)
	return nil
}

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: i18n.S("给 agent 用：列出 / 新开 / 指挥 Gerdr 里的其他会话（输出 JSON）", "For agents: list, create and direct other sessions in Gerdr (JSON output)"),
		Long: i18n.S(`在 Gerdr 里跑着的 agent 用这组命令和别的会话协作：开一个新会话（Claude Code 或 Codex）派活、
等它做完、读它的回复。会话可以用完整 id、id 前缀（至少 4 位）或标题指定。`, `Agents running in Gerdr use these commands to work with other sessions: start a new session (Claude Code or Codex)
and hand it a task, wait for it to finish, read its reply. Refer to a session by full id, id prefix (at least 4 characters) or title.`),
	}
	client := func() *apiClient {
		c, err := newAPIClient()
		if err != nil {
			fail(err)
		}
		return c
	}

	var listAll bool
	var listTags []string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: i18n.S("列出开着的会话（界面上看得见的；self: true 是你自己），最近有动静的在前（updatedAt）。--all 连关掉的一起", "List open sessions (the ones visible in the UI; self: true is you), most recently active first (updatedAt). --all includes closed ones"),
		RunE: func(_ *cobra.Command, _ []string) error {
			c := client()
			self := c.selfID()
			list, err := c.list()
			if err != nil {
				return err
			}
			want, err := parseTags(listTags, false)
			if err != nil {
				return err
			}
			sort.SliceStable(list, func(i, j int) bool { return list[i].Updated > list[j].Updated }) // 最近动过的在前
			out := []map[string]any{}
			for _, s := range list {
				if !listAll && s.inUI != nil && !*s.inUI && s.ID != self {
					continue
				}
				if !hasTags(s.Tags, want) {
					continue
				}
				out = append(out, s.view(self))
			}
			printJSON(out)
			return nil
		},
	}
	listCmd.Flags().BoolVar(&listAll, "all", false, i18n.S("连关掉的会话一起列（closed: true）", "include closed sessions (closed: true)"))
	listCmd.Flags().StringArrayVar(&listTags, "tag", nil, i18n.S("只列带这个标签的：k=v，或者只写 k（有这个键就算）；可以给多个，都要满足", "only sessions with this tag: k=v, or just k (any value); repeat for several, all must match"))
	cmd.AddCommand(listCmd)
	cmd.AddCommand(&cobra.Command{
		Use:   "current",
		Short: i18n.S("你自己所在的会话", "The session you are in"),
		RunE: func(_ *cobra.Command, _ []string) error {
			c := client()
			self := c.selfID()
			if self == "" {
				return errors.New(i18n.S("认不出当前会话（不是在 Gerdr 里跑的 agent？）", "cannot tell which session this is (not an agent running in Gerdr?)"))
			}
			s, err := c.get(self)
			if err != nil {
				return err
			}
			printJSON(s.view(self))
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "agents",
		Short: i18n.S("能开哪些 agent（new --agent 填 id）：内置的 + 用户在设置里建的角色，角色带它的提示词（看它是干什么的），还有可选的模型和思考强度（new --model / --effort 填 id，name 是界面上显示的名字和版本）", "Agents you can start (pass the id to new --agent): built-in ones + roles the user made in Settings, each role with its prompt (so you can see what it's for), and the models and efforts it offers (pass the id to new --model / --effort; name is what the picker shows, with the version)"),
		RunE: func(_ *cobra.Command, _ []string) error {
			var h struct {
				Agents []map[string]any `json:"agents"`
				Config struct {
					Agents []struct {
						ID     string `json:"id"`
						Prompt string `json:"prompt"`
					} `json:"agents"`
				} `json:"config"`
			}
			if err := client().do("GET", "/api/hello?only=agents,config", nil, &h); err != nil {
				return err
			}
			out := []map[string]any{}
			for _, a := range h.Agents {
				v := map[string]any{"id": a["id"], "name": a["name"], "available": a["available"]}
				if b, _ := a["base"].(string); b != "" {
					v["base"] = b
				}
				// What new --model / --effort take, with the names the picker shows ("fable" = "Fable 5.2"):
				// known once this kind of agent has run here
				for _, k := range []string{"models", "efforts"} {
					if x, ok := a[k]; ok {
						v[k] = x
					}
				}
				for _, d := range h.Config.Agents {
					if d.ID == a["id"] && d.Prompt != "" {
						v["prompt"] = d.Prompt
					}
				}
				out = append(out, v)
			}
			printJSON(out)
			return nil
		},
	})

	var nAgent, nCwd, nTitle, nMode, nModel, nEffort, nPrompt, nWorkspace, nReportTo, nHost string
	var nTags []string
	var nWait, nFocus bool
	var nTimeout time.Duration
	newCmd := &cobra.Command{
		Use:   "new",
		Short: i18n.S("开一个新会话（默认在你旁边分屏打开，不抢焦点）", "Start a new session (opens in a split next to you by default, without taking focus)"),
		RunE: func(_ *cobra.Command, _ []string) error {
			c := client()
			self := c.selfID()
			cwd := nCwd
			if nHost != "" {
				// Another computer: this one's working directory means nothing there; its home by default, ~ is its home
				var err error
				if cwd, err = c.hostCwd(nHost, cwd); err != nil {
					return err
				}
			} else {
				if cwd == "" {
					cwd, _ = os.Getwd()
				}
				if abs, err := filepath.Abs(cwd); err == nil {
					cwd = abs
				}
			}
			body := map[string]any{"agent": nAgent, "cwd": cwd, "title": nTitle}
			if nHost != "" {
				body["host"] = nHost
			}
			if nMode != "" {
				body["mode"] = nMode
			}
			// A model / effort the agent doesn't offer is refused with the ones it does (hub checkConfig), not quietly
			// replaced by the default
			config := map[string]string{}
			if nModel != "" {
				config["model"] = nModel
			}
			if nEffort != "" {
				config["effort"] = nEffort
			}
			if len(config) > 0 {
				body["config"] = config
			}
			tags, err := parseTags(nTags, false)
			if err != nil {
				return err
			}
			if len(tags) > 0 {
				body["tags"] = tags
			}
			if nReportTo != "" {
				to, err := c.reportTarget(nReportTo)
				if err != nil {
					return err
				}
				body["reportTo"] = to
			}
			open := map[string]any{"near": self, "focus": nFocus}
			if nWorkspace != "" {
				who := c.caller()
				open["workspace"] = nWorkspace
				open["project"], open["from"], open["term"] = who.Project, who.Session, who.Term
			}
			body["open"] = open
			var s sessInfo
			if err := c.do("POST", "/api/sessions", body, &s); err != nil {
				return err
			}
			if nPrompt == "" {
				printJSON(s.view(self))
				return nil
			}
			if err := c.sendTo(s.ID, nPrompt, self, ""); err != nil {
				return err
			}
			if !nWait {
				s, _ = c.get(s.ID)
				printJSON(s.view(self))
				return nil
			}
			return waitResult(c, s.ID, 0, nTimeout)
		},
	}
	newCmd.Flags().StringVar(&nAgent, "agent", "", i18n.S("claude、codex、native（Gerdr Agent），或者设置里加的角色 / 变体的 id（gerdr session agents）。不给 = 设置里的默认 agent", "claude, codex, native (Gerdr Agent), or the id of a role / variant from Settings (gerdr session agents). Omitted = the default agent in Settings"))
	newCmd.Flags().StringVar(&nWorkspace, "workspace", "", i18n.S("放进这个 Code 工作空间（id、编号或名字；new = 新建一个），作为新标签页；不给 = 开在你旁边", "put it in this Code workspace (id, number or label; new = a new one) as a new tab; omitted = next to you"))
	newCmd.Flags().StringArrayVar(&nTags, "tag", nil, i18n.S("给它打标签 k=v（可以给多个），之后 list --tag 找回来", "tag it k=v (repeatable), to find it again with list --tag"))
	newCmd.Flags().StringVar(&nReportTo, "report-to", "", i18n.S("它每次停下（做完、等人、出错）就发一条回报给这个会话；self = 你自己。用了它就别再 --wait", "send a report to this session each time it stops (done, waiting for a person, error); self = you. Don't --wait as well"))
	newCmd.Flags().StringVar(&nCwd, "cwd", "", i18n.S("工作目录（默认当前目录；--host 时默认那台电脑的家目录，~ 也指那台的）", "working directory (default: current directory; with --host, that computer's home, and ~ is its home)"))
	newCmd.Flags().StringVar(&nHost, "host", "", i18n.S("开在另一台电脑上（gerdr host list 里的 id）；它的文件在那台上", "start it on another computer (an id from gerdr host list); its files are on that computer"))
	newCmd.Flags().StringVar(&nTitle, "title", "", i18n.S("会话标题（之后可以用它指代这个会话）", "session title (you can refer to the session by it later)"))
	newCmd.Flags().StringVar(&nMode, "mode", "", i18n.S("权限模式（claude: default/acceptEdits/plan/auto/bypassPermissions；codex: ask/auto/read-only/full-access；native: default/acceptEdits/plan/bypassPermissions）。不给 = 用户在设置里给这种 agent 定的默认模式（全放开的那种不算）", "permission mode (claude: default/acceptEdits/plan/auto/bypassPermissions; codex: ask/auto/read-only/full-access; native: default/acceptEdits/plan/bypassPermissions). Omitted = the default mode the user set for this agent in Settings (fully permissive ones excepted)"))
	newCmd.Flags().StringVar(&nModel, "model", "", i18n.S("模型：gerdr session agents 里这个 agent 的 models 的 id（比如 claude 的 fable，name 写着它是哪个版本）。不给 = 设置里这种 agent 的默认模型；它没有的会报错并列出有的", "model: an id from this agent's models in gerdr session agents (e.g. claude's fable; its name says which version). Omitted = the default model set for this agent; one it doesn't offer is an error listing the ones it does"))
	newCmd.Flags().StringVar(&nEffort, "effort", "", i18n.S("思考强度：gerdr session agents 里的 efforts 的 id（low / medium / high …）。不给 = 默认", "thinking effort: an id from efforts in gerdr session agents (low / medium / high …). Omitted = the default"))
	newCmd.Flags().StringVar(&nPrompt, "prompt", "", i18n.S("开好之后马上发的第一句话", "first message to send once it's started"))
	newCmd.Flags().BoolVar(&nWait, "wait", false, i18n.S("和 --prompt 一起用：等它做完，输出它的回复", "with --prompt: wait for it to finish and print its reply"))
	newCmd.Flags().DurationVar(&nTimeout, "timeout", 30*time.Minute, i18n.S("--wait 最多等多久", "how long --wait waits at most"))
	newCmd.Flags().BoolVar(&nFocus, "focus", false, i18n.S("打开后把焦点切过去（默认不切，别打断人）", "switch focus to it once opened (off by default, so as not to interrupt the user)"))
	cmd.AddCommand(newCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   i18n.S("tag <会话> <k=v>...", "tag <session> <k=v>..."),
		Short: i18n.S("改会话的标签：k=v 设置，k= 删掉；没提到的不动。输出改完的会话", "Change a session's tags: k=v sets, k= removes; others stay. Prints the session"),
		Args:  cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			tags, err := parseTags(args[1:], true)
			if err != nil {
				return err
			}
			var out sessInfo
			if err := c.do("POST", "/api/sessions/"+s.ID+"/meta", map[string]any{"tags": tags, "from": c.selfID()}, &out); err != nil {
				return err
			}
			printJSON(out.view(c.selfID()))
			return nil
		},
	})
	var rFixed bool
	renameCmd := &cobra.Command{
		Use:   i18n.S("rename <会话> <标题>", "rename <session> <title>"),
		Short: i18n.S("给会话改名（和界面上改名一样，之后不再自动起名；/clear 会把它清掉，--fixed 不会）；标题给 \"\" = 交回自动起名。输出改完的会话", "Rename a session (as renaming it in the UI: no more auto titles; a /clear drops it, unless --fixed); title \"\" = back to auto titles. Prints the session"),
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			if err := c.do("POST", "/api/sessions/"+s.ID+"/rename", map[string]any{"title": args[1], "fixed": rFixed}, nil); err != nil {
				return err
			}
			if s, err = c.resolve(s.ID); err != nil {
				return err
			}
			printJSON(s.view(c.selfID()))
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   i18n.S("cwd <会话> <目录>", "cwd <session> <folder>"),
		Short: i18n.S("把会话换到另一个目录接着聊（项目目录搬家 / 改名之后用；对话、Claude 的会话记录跟着走）。在跑的会话不行。输出改完的会话", "Move a session to another folder and carry on there (after its project folder was moved or renamed; the conversation and Claude's transcript come along). Not while it is running. Prints the session"),
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			dir, err := filepath.Abs(expandHome(args[1]))
			if err != nil {
				return err
			}
			if err := c.do("POST", "/api/sessions/"+s.ID+"/cwd", map[string]any{"cwd": dir}, nil); err != nil {
				return err
			}
			if s, err = c.resolve(s.ID); err != nil {
				return err
			}
			printJSON(s.view(c.selfID()))
			return nil
		},
	})
	renameCmd.Flags().BoolVar(&rFixed, "fixed", false, i18n.S("固定的名字：/clear 之后还在，和 new --title 给的一样（比如一直开着、管很多事的会话）", "a fixed name: kept through /clear, like one given with new --title (e.g. a long-lived conversation that handles many things)"))
	cmd.AddCommand(renameCmd)
	cmd.AddCommand(&cobra.Command{
		Use:   i18n.S("report-to <会话> <目标会话|self|none>", "report-to <session> <target|self|none>"),
		Short: i18n.S("让一个会话每次停下（做完、等人、出错）就回报给目标会话；none = 不再回报", "Make a session report to the target each time it stops (done, waiting for a person, error); none = stop"),
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			to := ""
			if args[1] != "none" {
				if to, err = c.reportTarget(args[1]); err != nil {
					return err
				}
			}
			var out sessInfo
			if err := c.do("POST", "/api/sessions/"+s.ID+"/meta", map[string]any{"reportTo": to, "from": c.selfID()}, &out); err != nil {
				return err
			}
			printJSON(out.view(c.selfID()))
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   i18n.S("fetch <会话> <路径> [存到哪]", "fetch <session> <path> [dest]"),
		Short: i18n.S("把一个会话那台电脑上的文件拿到这台（相对路径按它的目录算）；默认存到当前目录，输出存下的路径", "Copy a file from the computer a session runs on to this one (relative paths are under its directory); saved in the current directory by default, prints where"),
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			if s.Host == nil {
				// Same computer: nothing to copy
				p := args[1]
				if !filepath.IsAbs(p) {
					p = filepath.Join(s.Cwd, p)
				}
				printJSON(map[string]any{"path": p})
				return nil
			}
			dest := filepath.Base(args[1])
			if len(args) == 3 {
				dest = args[2]
			}
			if st, err := os.Stat(dest); err == nil && st.IsDir() {
				dest = filepath.Join(dest, filepath.Base(args[1]))
			}
			p, err := c.fetch(s.Host.ID, args[1], s.Cwd, dest)
			if err != nil {
				return err
			}
			printJSON(map[string]any{"path": p, "from": s.Host.ID})
			return nil
		},
	})

	var sWait bool
	var sTimeout time.Duration
	var sFiles []string
	var sFold string
	sendCmd := &cobra.Command{
		Use:   i18n.S("send <会话> <消息>", "send <session> <message>"),
		Short: i18n.S("给一个会话发消息（它在跑就插话进去）", "Send a message to a session (interjects if it's running)"),
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			self := c.selfID()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			if s.ID == self {
				return errors.New(i18n.S("不能给自己发", "cannot send to yourself"))
			}
			evs, err := c.events(s.ID)
			if err != nil {
				return err
			}
			since := lastSeq(evs)
			files, err := c.attach(s, sFiles)
			if err != nil {
				return err
			}
			if err := c.sendTo(s.ID, args[1], self, sFold, files...); err != nil {
				return err
			}
			// 发给一个关掉的会话：顺手把它开回界面上（开在你旁边、不抢焦点），不然人看不见它在干活。
			if s.inUI != nil && !*s.inUI {
				_ = c.do("POST", "/api/sessions/"+s.ID+"/open", map[string]any{"near": self}, nil)
			}
			if !sWait {
				s, _ = c.get(s.ID)
				printJSON(map[string]any{"sent": true, "session": s.view(self)})
				return nil
			}
			return waitResult(c, s.ID, since, sTimeout)
		},
	}
	sendCmd.Flags().BoolVar(&sWait, "wait", false, i18n.S("等它做完，输出它这一轮的回复", "wait for it to finish and print its reply for this turn"))
	sendCmd.Flags().StringArrayVar(&sFiles, "file", nil, i18n.S("随消息带上这个文件（可以给多个）；对方在另一台电脑上时会先拷过去，它拿到的是那台上的路径", "attach this file (repeatable); for a session on another computer it is copied there first, and it gets the path there"))
	sendCmd.Flags().StringVar(&sFold, "fold", "", i18n.S("在对方的聊天里这条消息折成的那一行（点开看全文）；不给就用第一行。对方收到的总是全文", "the one line this message folds to in its chat (a click shows all of it); default its first line. The agent always gets the whole message"))
	sendCmd.Flags().DurationVar(&sTimeout, "timeout", 30*time.Minute, i18n.S("--wait 最多等多久", "how long --wait waits at most"))
	cmd.AddCommand(sendCmd)

	var wTimeout time.Duration
	var wSince int64
	waitCmd := &cobra.Command{
		Use:   i18n.S("wait <会话>", "wait <session>"),
		Short: i18n.S("等一个会话停下来（跑完，或者在等人审批 / 回答），输出它最后的回复", "Wait for a session to stop (finished, or waiting for the user to approve / answer) and print its last reply"),
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			since := wSince
			if since == 0 {
				// 默认：最后一条用户消息之后 agent 说的
				evs, _ := c.events(s.ID)
				for _, e := range evs {
					if e.Kind == "user" {
						since = e.Seq
					}
				}
			}
			return waitResult(c, s.ID, since, wTimeout)
		},
	}
	waitCmd.Flags().DurationVar(&wTimeout, "timeout", 30*time.Minute, i18n.S("最多等多久", "how long to wait at most"))
	waitCmd.Flags().Int64Var(&wSince, "since", 0, i18n.S("只要这个序号之后的回复（默认：最后一条用户消息之后）", "only replies after this sequence number (default: after the last user message)"))
	cmd.AddCommand(waitCmd)

	var rLast int
	readCmd := &cobra.Command{
		Use:   i18n.S("read <会话>", "read <session>"),
		Short: i18n.S("读一个会话的对话（用户说的、agent 说的、调用的工具）", "Read a session's conversation (what the user said, what the agent said, tools called)"),
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			evs, err := c.events(s.ID)
			if err != nil {
				return err
			}
			t := transcript(evs)
			if rLast > 0 && len(t) > rLast {
				t = t[len(t)-rLast:]
			}
			printJSON(map[string]any{"session": s.view(""), "messages": t})
			return nil
		},
	}
	readCmd.Flags().IntVar(&rLast, "last", 30, i18n.S("只要最后几条（0 = 全部）", "only the last N entries (0 = all)"))
	cmd.AddCommand(readCmd)

	var oFocus bool
	openCmd := &cobra.Command{
		Use:   i18n.S("open <会话>", "open <session>"),
		Short: i18n.S("把一个关掉的会话（closed: true）开回界面上，接着用。默认开在你旁边、不抢焦点", "Reopen a closed session (closed: true) in the UI to keep using it. Opens next to you by default, without taking focus"),
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			if err := c.do("POST", "/api/sessions/"+s.ID+"/open", map[string]any{"near": c.selfID(), "focus": oFocus}, nil); err != nil {
				return err
			}
			printJSON(map[string]any{"opened": true, "id": s.ID})
			return nil
		},
	}
	openCmd.Flags().BoolVar(&oFocus, "focus", false, i18n.S("打开后把焦点切过去", "switch focus to it once opened"))
	cmd.AddCommand(openCmd)

	cmd.AddCommand(&cobra.Command{
		Use:   i18n.S("close <会话>", "close <session>"),
		Short: i18n.S("关掉一个会话（从界面上收掉它的 pane；正在跑的那一轮会停掉，记录留着，open 能开回来；关自己是等这一轮跑完再关）", "Close a session (removes its pane from the UI; a running turn is stopped, the history stays and open brings it back; closing yourself waits for the current turn to finish)"),
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			// 关自己：当场关会掐掉正在跑的这一轮（就是发这条命令的这一轮），收尾的话说不出来、这条命令也等不到返回；
			// 改成让服务端等这一轮跑完再关，这里不等
			if s.ID == c.selfID() {
				if err := c.do("POST", "/api/sessions/"+s.ID+"/close", map[string]any{"afterTurn": true}, nil); err != nil {
					return err
				}
				printJSON(map[string]any{"closed": false, "afterTurn": true, "id": s.ID})
				return nil
			}
			if s.inUI != nil && !*s.inUI {
				printJSON(map[string]any{"closed": true, "id": s.ID})
				return nil
			}
			if err := c.do("POST", "/api/sessions/"+s.ID+"/close", map[string]any{"from": c.selfID()}, nil); err != nil {
				return err
			}
			// 布局归界面管：等开着的界面把 pane 收掉、存好布局。一直没收掉 = 没有界面开着（布局里还挂着它，下次打开界面还在）
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
				if s, err := c.resolve(s.ID); err == nil && s.inUI != nil && !*s.inUI {
					printJSON(map[string]any{"closed": true, "id": s.ID})
					return nil
				}
			}
			return errors.New(i18n.S("界面没把它关掉（Gerdr 的窗口没开着？）", "the UI did not close it (is the Gerdr window open?)"))
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   i18n.S("cancel <会话>", "cancel <session>"),
		Short: i18n.S("打断一个会话正在跑的那一轮", "Interrupt a session's running turn"),
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			if err := c.do("POST", "/api/sessions/"+s.ID+"/cancel", map[string]any{}, nil); err != nil {
				return err
			}
			printJSON(map[string]any{"cancelled": true, "id": s.ID})
			return nil
		},
	})

	var loginCode string
	var loginStatus, loginCancel bool
	login := &cobra.Command{
		Use:   i18n.S("login <会话>", "login <session>"),
		Short: i18n.S("会话的 agent 登录过期了（会话的 login 字段有值）：在后台重新登录", "The session's agent lost its login (the session has a login field): sign it in again in the background"),
		Long: i18n.S(`在 Gerdr 那台电脑上跑这个 agent 自己的登录（claude auth login），打印 {state, url}。
那台电脑的浏览器会打开，人在里面授权完就登好了；人不在那台电脑前时，把 url 给人，
人登录后页面会给一串授权码，用 --code 交回来。登好之后，因为登录失败而出错的会话恢复正常，人重发一次就行。
已经有一个在跑就接着用那一个。`, `Runs the agent's own sign-in (claude auth login) on the Gerdr computer and prints {state, url}.
A browser opens on that computer; once the person authorizes there, it's done. When they aren't at that computer,
give them the url: after signing in, the page shows a code, which goes back with --code. Once signed in, sessions that
failed on the login are back to normal and the person resends. Joins the sign-in already running, if any.`),
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c := client()
			s, err := c.resolve(args[0])
			if err != nil {
				return err
			}
			base := "/api/sessions/" + s.ID + "/login"
			switch {
			case loginCancel:
				err = c.do("POST", base+"/cancel", map[string]any{}, nil)
			case loginCode != "":
				err = c.do("POST", base+"/code", map[string]any{"code": loginCode}, nil)
			}
			if err != nil {
				return err
			}
			var st map[string]any
			if loginStatus || loginCancel || loginCode != "" {
				err = c.do("GET", base, nil, &st)
			} else {
				err = c.do("POST", base, map[string]any{}, &st)
			}
			if err != nil {
				return err
			}
			printJSON(st)
			return nil
		},
	}
	login.Flags().StringVar(&loginCode, "code", "", i18n.S("登录页给的授权码", "the code the sign-in page showed"))
	login.Flags().BoolVar(&loginStatus, "status", false, i18n.S("只看进展，不开始", "only show how it stands, don't start one"))
	login.Flags().BoolVar(&loginCancel, "cancel", false, i18n.S("放弃正在跑的登录", "give up the running sign-in"))
	cmd.AddCommand(login)
	return cmd
}

// hostCwd: a working directory on another computer, absolute there. Empty = its home; ~ = its home too.
func (c *apiClient) hostCwd(host, cwd string) (string, error) {
	var d struct {
		Home string `json:"home"`
	}
	if err := c.do("GET", "/api/fs/dirs?host="+url.QueryEscape(host), nil, &d); err != nil {
		return "", fmt.Errorf(i18n.S("问不到那台电脑（%s）：%w", "couldn't ask that computer (%s): %w"), host, err)
	}
	switch {
	case cwd == "" || cwd == "~":
		return d.Home, nil
	case strings.HasPrefix(cwd, "~/"):
		return d.Home + cwd[1:], nil
	case strings.HasPrefix(cwd, "/"):
		return cwd, nil
	}
	return "", errors.New(i18n.S("--host 时 --cwd 要写绝对路径或 ~/…", "with --host, --cwd must be absolute or ~/…"))
}

// sendTo sends text (and files) to session id. fold: a one-line summary the chat draws the message folded to
// (chatlog.Event.Fold); "" = shown whole.
func (c *apiClient) sendTo(id, text, from, fold string, files ...map[string]string) error {
	content := []map[string]string{{"type": "text", "text": text}}
	content = append(content, files...)
	return c.do("POST", "/api/sessions/"+id+"/send", map[string]any{
		"content": content,
		"id":      uuid.NewString(),
		"from":    from,
		"fold":    strings.TrimSpace(fold),
	}, nil)
}

// attach: files to go with a message to session s, as resource links the agent reads by path. For a session on another
// computer each file is copied there first (POST /api/fs/upload?host=, kept in that computer's Gerdr uploads), and
// the link is the path there: a path on this computer means nothing to it (docs/dev/MULTIHOST.md).
func (c *apiClient) attach(s sessInfo, paths []string) ([]map[string]string, error) {
	var out []map[string]string
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			return nil, fmt.Errorf(i18n.S("%s 是目录：--file 只收文件（目录先打个包）", "%s is a folder: --file takes files (pack the folder first)"), p)
		}
		there := abs
		if s.Host != nil {
			if there, err = c.upload(s.Host.ID, abs); err != nil {
				return nil, fmt.Errorf(i18n.S("把 %s 传到 %s 失败：%w", "copying %s to %s failed: %w"), p, s.Host.ID, err)
			}
		}
		out = append(out, map[string]string{"type": "resource_link", "uri": "file://" + there, "name": filepath.Base(abs)})
	}
	return out, nil
}

// fetch streams a file from another computer (through this Gerdr's /h/<host>/ proxy) into dest; returns dest, absolute.
func (c *apiClient) fetch(host, path, cwd, dest string) (string, error) {
	q := url.Values{"path": {path}, "cwd": {cwd}}
	req, err := http.NewRequest("GET", c.base+"/h/"+url.PathEscape(host)+"/api/fs/raw?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return "", errors.New(e.Error)
	}
	abs, err := filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	tmp := abs + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return abs, os.Rename(tmp, abs)
}

// upload streams a file to another computer through this Gerdr; returns its path there.
func (c *apiClient) upload(host, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	q := url.Values{"host": {host}, "name": {filepath.Base(path)}}
	req, err := http.NewRequest("POST", c.base+"/api/fs/upload?"+q.Encode(), f)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Gerdr", "1")
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := (&http.Client{}).Do(req) // no overall timeout: a big file over the relay takes what it takes
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Path  string `json:"path"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 || out.Path == "" {
		if out.Error == "" {
			out.Error = resp.Status
		}
		return "", errors.New(out.Error)
	}
	return out.Path, nil
}

// fail exits with err as JSON on stderr; a nil err is a no-op, so `fail(c.do(…))` reads as a check.
func fail(err error) {
	if err == nil {
		return
	}
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	fmt.Fprintln(os.Stderr, string(b))
	os.Exit(1)
}

// workCmd: `gerdr work …`, the old name of what is now under `gerdr panel` (run, functions, logs, schedules…), kept
// hidden so agents and panels still using it keep working. There is no "work" concept any more, only panels.
func workCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "work", Short: "Old name of gerdr panel run / functions / logs / schedules / triggers / secret / status", Hidden: true}
	var project string
	cmd.PersistentFlags().StringVar(&project, "project", "", "project id (default: the one the current directory belongs to)")
	cmd.AddCommand(panelFnCmds(func() string {
		if project != "" {
			return project
		}
		wd, _ := os.Getwd()
		return work.New(config.FromEnv().Dir, nil).ProjectOf(wd)
	})...)
	return cmd
}

// panelFnCmds: what runs inside a panel, under `gerdr panel` — its local functions (run, functions, logs, abort), its
// MCP tools, schedules, triggers, secrets and page status. which: the panel / project acted on (--project, else the
// one whose directory the current directory is in, which is where a panel's agent runs).
func panelFnCmds(which func() string) []*cobra.Command {
	cmd := &cobra.Command{}
	var input string
	run := &cobra.Command{
		Use:   "run <file.export> [input JSON]",
		Short: "Run a local function the way a page does: progress and logs on stderr, the result (JSON) on stdout",
		Long: `Run a local function the way a page does, e.g. gerdr panel run okr.summary '{"quarter":"2026Q4"}'.
Progress and console output go to stderr as they happen, the returned value to stdout as JSON; a thrown error
prints its stack and exits 1. Ctrl-C stops the run. --input-file - reads the input from stdin.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			raw := ""
			if len(args) > 1 {
				raw = args[1]
			}
			if input != "" {
				var b []byte
				if input == "-" {
					b, err = io.ReadAll(os.Stdin)
				} else {
					b, err = os.ReadFile(input)
				}
				if err != nil {
					return err
				}
				raw = string(b)
			}
			var in any
			if strings.TrimSpace(raw) != "" {
				if err := json.Unmarshal([]byte(raw), &in); err != nil {
					return fmt.Errorf("input isn't JSON: %w", err)
				}
			}
			return c.workRun(which(), args[0], in)
		},
	}
	run.Flags().StringVar(&input, "input-file", "", "read the input JSON from this file (- = stdin)")
	cmd.AddCommand(run)
	cmd.AddCommand(&cobra.Command{
		Use:   "functions",
		Short: "List the project's callable functions (file.export)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("GET", "/api/work/functions?project="+url.QueryEscape(which()), nil, &out); err != nil {
				return err
			}
			printJSON(out["list"])
			return nil
		},
	})
	var logFn, logID string
	var logN int
	logs := &cobra.Command{
		Use:   "logs",
		Short: "Past runs of the project's functions (newest first), or one run's full log with --id",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			q := url.Values{"project": {which()}}
			if logID != "" {
				q.Set("id", logID)
			}
			if logFn != "" {
				q.Set("fn", logFn)
			}
			q.Set("limit", fmt.Sprint(logN))
			var out map[string]any
			if err := c.do("GET", "/api/work/logs?"+q.Encode(), nil, &out); err != nil {
				return err
			}
			if logID != "" {
				printJSON(out["lines"])
			} else {
				printJSON(out["list"])
			}
			return nil
		},
	}
	logs.Flags().StringVar(&logFn, "fn", "", "only this function (file or file.export)")
	logs.Flags().StringVar(&logID, "id", "", "one run's log: progress, logs, the error with its stack")
	logs.Flags().IntVarP(&logN, "limit", "n", 20, "how many runs")
	cmd.AddCommand(logs)
	cmd.AddCommand(&cobra.Command{
		Use:   "abort <run id>",
		Short: "Stop a running function (the id is in the run's start event, X-Gerdr-Run, or gerdr panel logs)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			return c.do("POST", "/api/work/runs/"+url.PathEscape(args[0])+"/abort", map[string]any{}, nil)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "schedules",
		Short: "The project's schedules (schedules/<id>.json): last run, next time, paused",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("GET", "/api/work/schedules?project="+url.QueryEscape(which()), nil, &out); err != nil {
				return err
			}
			printJSON(out["list"])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "streams",
		Short: "The project's streams (streams/<id>.json: long connections Gerdr keeps open for it): connected or not, messages, runs",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("GET", "/api/work/streams?project="+url.QueryEscape(which()), nil, &out); err != nil {
				return err
			}
			printJSON(out["list"])
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "triggers",
		Short: "The project's triggers (triggers/<id>.json: what runs when an event comes, docs/dev/EVENTS.md): runs and the last one since Gerdr started",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("GET", "/api/work/triggers?project="+url.QueryEscape(which()), nil, &out); err != nil {
				return err
			}
			printJSON(out["list"])
			return nil
		},
	})
	cmd.AddCommand(workMCPCmd(which))
	sched := &cobra.Command{Use: "schedule", Short: "Run one schedule now, or pause / resume it (on this machine)"}
	for _, op := range []string{"run", "pause", "resume"} {
		sched.AddCommand(&cobra.Command{
			Use:   op + " <id>",
			Short: map[string]string{"run": "Run it now, whatever its time", "pause": "Stop running it until resumed", "resume": "Run it on its times again"}[op],
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				c, err := newAPIClient()
				if err != nil {
					return err
				}
				return c.do("POST", "/api/work/schedules/"+url.PathEscape(args[0])+"/"+op+"?project="+url.QueryEscape(which()), map[string]any{}, nil)
			},
		})
	}
	cmd.AddCommand(sched)
	secret := &cobra.Command{Use: "secret", Short: "Keys for ctx.secrets.get (stored in Gerdr's secrets.json, never shown back)"}
	secret.AddCommand(&cobra.Command{
		Use:   "set <name>",
		Short: "Store a key; the value is read from stdin so it stays out of shell history and transcripts",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			v := strings.TrimRight(string(b), "\r\n")
			if v == "" {
				return errors.New("no value on stdin (to delete it: gerdr panel secret rm)")
			}
			return c.do("POST", "/api/work/secrets", map[string]any{"name": args[0], "value": v}, nil)
		},
	})
	secret.AddCommand(&cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a key",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			return c.do("POST", "/api/work/secrets", map[string]any{"name": args[0], "value": ""}, nil)
		},
	})
	secret.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "The stored keys' names",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("GET", "/api/work/secrets", nil, &out); err != nil {
				return err
			}
			printJSON(out["list"])
			return nil
		},
	})
	cmd.AddCommand(secret)
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "The panel's page (ui/): build error and runtime errors from the open page",
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("GET", "/api/work/ui/status?project="+url.QueryEscape(which()), nil, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	})
	return cmd.Commands()
}

// panelCmd: `gerdr panel`, a project's panels (its own and the ones added: work/source.go), the same as
// Settings → Panels. The project is the current directory's (or --project); a panel is named by its name, the project's
// own by the project's id, or any by its whole key ("<project>/<name>").
func panelCmd() *cobra.Command {
	var projectArg string
	which := func() string {
		if projectArg != "" {
			return projectArg
		}
		wd, _ := os.Getwd()
		return work.New(config.FromEnv().Dir, nil).ProjectOf(wd)
	}
	project := func() string { p, _ := work.SplitKey(which()); return p }
	keyOf := func(arg string) string {
		if strings.Contains(arg, "/") || arg == "" || arg == project() {
			if arg == "" {
				return project()
			}
			return arg
		}
		return work.PanelKey(project(), arg)
	}
	client := func() (*apiClient, error) { return newAPIClient() }
	cmd := &cobra.Command{Use: "panel", Short: "A project's panels: list, install from git, add a blank one, update, change the icon, reorder, put a dot / number on its button, remove; and run what's in one (run, functions, logs, schedules, triggers, secret, status)"}
	cmd.PersistentFlags().StringVar(&projectArg, "project", "", "project id, or a panel's key (<project>/<name>) for run / functions / logs …  (default: the one the current directory belongs to)")
	cmd.AddCommand(panelFnCmds(which)...)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "The project's panels (JSON): key, name, config (its ui.json with this person's title / icon / color over it), where it came from",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("GET", "/api/work/panels?project="+url.QueryEscape(project()), nil, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	})
	var name string
	install := &cobra.Command{
		Use:   "install <git url>",
		Short: "Add a panel cloned from a git repository (owner/repo, a GitHub URL, file://…). Its code runs here: functions run commands, schedules start by themselves",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("POST", "/api/work/install", map[string]any{"project": project(), "url": args[0], "name": name}, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	}
	install.Flags().StringVar(&name, "name", "", "the panel's name (default: the repository's)")
	cmd.AddCommand(install)
	cmd.AddCommand(&cobra.Command{
		Use:   "new <name>",
		Short: "Add a blank panel for its agent to build",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("POST", "/api/work/panels", map[string]any{"project": project(), "name": args[0]}, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "update [panel]",
		Short: "Pull the panel's repository; the user's own changes are rebased on top (default: the panel of the current directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			key := which()
			if len(args) == 1 {
				key = keyOf(args[0])
			}
			var out map[string]any
			if err := c.do("POST", "/api/work/update", map[string]any{"project": key}, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "rename <panel> <title>",
		Short: "Rename a panel for this person (kept in its user/ui.json, not shared); its directory, data and ui.json stay as they are",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			return c.do("POST", "/api/work/panels/title", map[string]any{"key": keyOf(args[0]), "title": args[1]}, nil)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "move <panel> <position>",
		Short: "Move a panel's button on the rail, for this person: position 1 is the top (as dragging the button does); `gerdr panel list` shows the order",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 1 {
				return fmt.Errorf("position: a number from 1, got %q", args[1])
			}
			c, err := client()
			if err != nil {
				return err
			}
			return c.do("POST", "/api/work/panels/move", map[string]any{"key": keyOf(args[0]), "to": n - 1}, nil)
		},
	})
	var color string
	icon := &cobra.Command{
		Use:   "icon <panel> [icon]",
		Short: "Set the panel's rail icon for this person (its user/ui.json): a glyph or line icon key (kanban, rocket, orbit…) or sprite:<key> from an add-on; none = the default",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			ic := ""
			if len(args) == 2 {
				ic = args[1]
			}
			return c.do("POST", "/api/work/panels/icon", map[string]any{"key": keyOf(args[0]), "icon": ic, "color": color}, nil)
		},
	}
	icon.Flags().StringVar(&color, "color", "", "a palette colour for a built-in icon (as in Settings → Agent)")
	cmd.AddCommand(icon)
	var badgeAdd bool
	badge := &cobra.Command{
		Use:   "badge <panel> <n|dot|clear>",
		Short: "Put a red dot or a number on a panel's rail button (something new there; no inbox item, no notification). It goes when the user opens the panel. A function does the same with ctx.badge",
		Long: `Put a red dot or a number on a panel's rail button: something new there (a board changed, new messages).
Only a mark: nothing goes to the inbox, nobody is notified. It goes when the user opens the panel, or with clear.

  gerdr panel badge board 3          a number
  gerdr panel badge board dot        a plain dot
  gerdr panel badge board 1 --add    one more on top of what it shows (a dot counts as 0)
  gerdr panel badge board clear      none`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			n := 0
			switch v := strings.ToLower(args[1]); v {
			case "dot", "true":
			case "clear", "none", "0":
				if badgeAdd {
					return errors.New("--add takes a number")
				}
				n = -1
			default:
				if n, err = strconv.Atoi(v); err != nil || n < 0 {
					return fmt.Errorf("%q: want a number, dot or clear", args[1])
				}
			}
			return c.do("POST", "/api/work/badge", map[string]any{"key": keyOf(args[0]), "n": n, "add": badgeAdd}, nil)
		},
	}
	badge.Flags().BoolVar(&badgeAdd, "add", false, "add the number to what the badge shows instead of replacing it")
	cmd.AddCommand(badge)
	cmd.AddCommand(&cobra.Command{
		Use:   "rm <panel>",
		Short: "Remove a panel: it moves to the trash, data and all (the project's id removes its own panel; restore brings a blank one)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("POST", "/api/work/panels/remove", map[string]any{"key": keyOf(args[0])}, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "restore",
		Short: "Give the project its own panel back after it was removed (a blank one)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			return c.do("POST", "/api/work/panels/restore", map[string]any{"project": project()}, nil)
		},
	})
	return cmd
}

// workRun runs a function and follows it: progress and logs to stderr, the result to stdout, an error (with its stack)
// to stderr and exit 1. Ctrl-C aborts the run (it would otherwise go on: runs don't belong to the request).
func (c *apiClient) workRun(project, fn string, input any) error {
	// From inside an agent: say which conversation runs it, so a Work tool's function sees ctx.caller.session even
	// when called from the command line (an agent whose MCP tools didn't load), not only over `gerdr panel mcp`
	b, _ := json.Marshal(map[string]any{"project": project, "fn": fn, "input": input, "from": c.selfID()})
	req, err := http.NewRequest("POST", c.base+"/api/work/run", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Gerdr", "1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := (&http.Client{}).Do(req) // no timeout: a run may take up to an hour
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s", resp.Status)
	}
	id := resp.Header.Get("X-Gerdr-Run")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	go func() {
		if _, ok := <-sig; ok && id != "" {
			fmt.Fprintln(os.Stderr, "stopping…")
			_ = c.do("POST", "/api/work/runs/"+url.PathEscape(id)+"/abort", map[string]any{}, nil)
		}
	}()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var ev struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		var d map[string]any
		_ = json.Unmarshal(ev.Data, &d)
		switch ev.Type {
		case "progress":
			fmt.Fprintf(os.Stderr, "[progress] %s\n", ev.Data)
		case "log":
			fmt.Fprintf(os.Stderr, "[%v] %v\n", d["level"], d["message"])
		case "result":
			printJSON(d["value"])
			return nil
		case "error":
			if st, _ := d["stack"].(string); st != "" {
				fmt.Fprintln(os.Stderr, st)
			}
			return fmt.Errorf("%v", d["message"])
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("the connection closed before the run ended (it may still be running: gerdr panel logs)")
}

// gerdr ui …：给改界面的 agent 用（内置的 gerdr-ui skill）。
func uiCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ui", Short: i18n.S("界面源码在哪、现在有没有改坏（输出 JSON）", "Where the UI source is and whether it's currently broken (JSON output)")}
	sync := &cobra.Command{
		Use:   i18n.S("sync <界面源码目录>", "sync <ui-source-dir>"),
		Short: i18n.S("把一份界面源码当成新版合进 Gerdr 正在用的界面目录（git 合并，开着的窗口热更新，不用重启）", "Merge a copy of the UI source into the UI dir Gerdr is using as a new version (git merge; open windows hot-reload, no restart needed)"),
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			env := config.FromEnv()
			src, _ := filepath.Abs(args[0])
			res, err := webui.SyncFrom(src, env.UI)
			if err != nil {
				return err
			}
			printJSON(map[string]any{"to": env.UI, "result": res})
			return conflictErr(res)
		},
	}
	merge := &cobra.Command{
		Use:   "merge",
		Short: i18n.S("把还没合进来的新版界面合进来；有冲突就留在文件里，改好后 git add -A && git commit", "Merge in a pending new UI version; conflicts are left in the files: fix them, then git add -A && git commit"),
		RunE: func(_ *cobra.Command, _ []string) error {
			env := config.FromEnv()
			res, err := webui.Merge(env.UI)
			if err != nil {
				return err
			}
			printJSON(map[string]any{"dir": env.UI, "result": res})
			return conflictErr(res)
		},
	}
	cmd.AddCommand(merge)
	cmd.AddCommand(sync)
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: i18n.S("界面目录 + 编译错误 + 页面里报上来的运行时错误", "UI dir + build errors + runtime errors reported by the page"),
		RunE: func(_ *cobra.Command, _ []string) error {
			c, err := newAPIClient()
			if err != nil {
				return err
			}
			var out map[string]any
			if err := c.do("GET", "/api/ui/status", nil, &out); err != nil {
				return err
			}
			printJSON(out)
			return nil
		},
	})
	return cmd
}

// conflictErr makes a merge that stopped on conflicts exit non-zero. The result JSON alone said "conflict" while the
// exit code said success, and agents (and `make sync-ui`) took it as synced, leaving the running UI on the old version.
func conflictErr(res *webui.Result) error {
	if res != nil && res.Status == "conflict" {
		if res.Note != "" {
			return errors.New(res.Note)
		}
		return errors.New(i18n.S("有冲突，留在文件里：解决后 git add -A && git commit", "Conflicts are left in the files: resolve them, then git add -A && git commit"))
	}
	return nil
}

// answerNote：人怎么处理的（session read 里「已处理」那条的后半句）。
func answerNote(resp json.RawMessage) string {
	var r struct {
		OptionID  string              `json:"optionId"`
		Answers   map[string][]string `json:"answers"`
		Cancelled bool                `json:"cancelled"`
		Reason    string              `json:"reason"`
	}
	_ = json.Unmarshal(resp, &r)
	switch {
	case r.OptionID != "":
		return " → " + r.OptionID
	case len(r.Answers) > 0:
		var a []string
		for _, v := range r.Answers {
			a = append(a, strings.Join(v, "、"))
		}
		return " → " + strings.Join(a, "；")
	case r.Reason == "replied":
		return i18n.S(" → 没选，在对话里直接回了", " → no option picked, replied in the chat instead")
	case r.Reason == "skipped":
		return i18n.S(" → 跳过了", " → skipped")
	case r.Cancelled:
		return i18n.S(" → 作废（这一轮结束了）", " → void (the turn ended)")
	}
	return ""
}

// parseTags reads k=v arguments. A bare k (no '=') means "has this key" in a filter; in a change (removing) it's
// rejected so a typo doesn't silently delete: removing is k=.
func parseTags(args []string, change bool) (map[string]string, error) {
	out := map[string]string{}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if k == "" || (change && !ok) {
			return nil, fmt.Errorf(i18n.S("标签写成 k=v（删掉写 k=）：%q", "write tags as k=v (k= to remove): %q"), a)
		}
		out[k] = v
	}
	return out, nil
}

// hasTags: every wanted key is set, to the wanted value unless that's "" (any value).
func hasTags(tags, want map[string]string) bool {
	for k, v := range want {
		got, ok := tags[k]
		if !ok || (v != "" && got != v) {
			return false
		}
	}
	return true
}

// reportTarget: a session ref for --report-to; "self" is the caller.
func (c *apiClient) reportTarget(ref string) (string, error) {
	if ref == "self" {
		id := c.selfID()
		if id == "" {
			return "", errors.New(i18n.S("认不出当前会话：--report-to self 只能在 Gerdr 里的 agent 用", "cannot tell which session this is: --report-to self only works for an agent running in Gerdr"))
		}
		return id, nil
	}
	s, err := c.resolve(ref)
	if err != nil {
		return "", err
	}
	return s.ID, nil
}
