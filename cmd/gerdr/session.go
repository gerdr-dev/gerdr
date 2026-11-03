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
	if s.Error != "" {
		m["error"] = s.Error
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
	if err := c.do("GET", "/api/hello", nil, &h); err != nil {
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
		Short: i18n.S("能开哪些 agent（new --agent 填 id）：内置的 + 用户在设置里建的角色，角色带它的提示词（看它是干什么的）", "Agents you can start (pass the id to new --agent): built-in ones + roles the user made in Settings, each role with its prompt (so you can see what it's for)"),
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
			if err := client().do("GET", "/api/hello", nil, &h); err != nil {
				return err
			}
			out := []map[string]any{}
			for _, a := range h.Agents {
				v := map[string]any{"id": a["id"], "name": a["name"], "available": a["available"]}
				if b, _ := a["base"].(string); b != "" {
					v["base"] = b
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

	var nAgent, nCwd, nTitle, nMode, nPrompt, nWorkspace, nReportTo string
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
			if cwd == "" {
				cwd, _ = os.Getwd()
			}
			if abs, err := filepath.Abs(cwd); err == nil {
				cwd = abs
			}
			body := map[string]any{"agent": nAgent, "cwd": cwd, "title": nTitle}
			if nMode != "" {
				body["mode"] = nMode
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
			if err := c.sendTo(s.ID, nPrompt, self); err != nil {
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
	newCmd.Flags().StringVar(&nCwd, "cwd", "", i18n.S("工作目录（默认当前目录）", "working directory (default: current directory)"))
	newCmd.Flags().StringVar(&nTitle, "title", "", i18n.S("会话标题（之后可以用它指代这个会话）", "session title (you can refer to the session by it later)"))
	newCmd.Flags().StringVar(&nMode, "mode", "", i18n.S("权限模式（claude: default/acceptEdits/plan/auto/bypassPermissions；codex: ask/auto/read-only/full-access；native: default/acceptEdits/plan/bypassPermissions）。不给 = 用户在设置里给这种 agent 定的默认模式（全放开的那种不算）", "permission mode (claude: default/acceptEdits/plan/auto/bypassPermissions; codex: ask/auto/read-only/full-access; native: default/acceptEdits/plan/bypassPermissions). Omitted = the default mode the user set for this agent in Settings (fully permissive ones excepted)"))
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

	var sWait bool
	var sTimeout time.Duration
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
			if err := c.sendTo(s.ID, args[1], self); err != nil {
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
	return cmd
}

func (c *apiClient) sendTo(id, text, from string) error {
	return c.do("POST", "/api/sessions/"+id+"/send", map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
		"id":      uuid.NewString(),
		"from":    from,
	}, nil)
}

func fail(err error) {
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	fmt.Fprintln(os.Stderr, string(b))
	os.Exit(1)
}

// gerdr work …: for the Work agent (see internal/work/builtin/CLAUDE.md). The project is the one whose
// Work directory the current directory is in (that's where the Work agent runs), or --project.
func workCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "work", Short: "The Work view: local functions and the Work site of a project"}
	var project string
	cmd.PersistentFlags().StringVar(&project, "project", "", "project id (default: the one the current directory belongs to)")
	which := func() string {
		if project != "" {
			return project
		}
		wd, _ := os.Getwd()
		return work.New(config.FromEnv().Dir, nil).ProjectOf(wd)
	}
	var input string
	run := &cobra.Command{
		Use:   "run <file.export> [input JSON]",
		Short: "Run a local function the way a page does: progress and logs on stderr, the result (JSON) on stdout",
		Long: `Run a local function the way a page does, e.g. gerdr work run okr.summary '{"quarter":"2026Q4"}'.
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
		Short: "Stop a running function (the id is in the run's start event, X-Gerdr-Run, or gerdr work logs)",
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
				return errors.New("no value on stdin (to delete it: gerdr work secret rm)")
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
		Short: "The project's Work site (ui/): build error and runtime errors from the open page",
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
	return cmd
}

// workRun runs a function and follows it: progress and logs to stderr, the result to stdout, an error (with its stack)
// to stderr and exit 1. Ctrl-C aborts the run (it would otherwise go on: runs don't belong to the request).
func (c *apiClient) workRun(project, fn string, input any) error {
	b, _ := json.Marshal(map[string]any{"project": project, "fn": fn, "input": input})
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
	return errors.New("the connection closed before the run ended (it may still be running: gerdr work logs)")
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
			return nil
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
			return nil
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
