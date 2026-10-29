package native

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/zbysir/gerdr/internal/agents/native/llm"
	"github.com/zbysir/gerdr/internal/ext"
	"github.com/zbysir/gerdr/internal/sysenv"
)

// MCP 客户端：只做 agent 要用的 —— initialize、tools/list、tools/call。三种传输：
//   - stdio：起子进程，按行 JSON-RPC；
//   - http（streamable HTTP）：每个请求一次 POST，响应可能是 JSON，也可能是一段 SSE；带上 Mcp-Session-Id；
//   - sse（老式）：GET 一条长连接，先收到 endpoint 事件（往哪 POST），之后响应都从这条长连接回来。
//
// 服务器从哪来照 gerdr 设置里 MCP 的来源（ext.Settings.MCPSource）：「agent 自己的」当成 Claude Code 配的
// （~/.claude.json、项目的 .mcp.json —— 这个 agent 没有自己的 MCP 配置，用户的 MCP 基本都配在那儿），再加 gerdr 自己那份。
//
// 进程共用：同一份配置（名字 + 命令 / 地址）的服务器整个 agent 只起一个，所有会话共用；起不来的 1 分钟内不再试。
// 工具名是 mcp__<服务器>__<工具>（和 Claude Code 一样），审批按工具记（「本会话都允许」= tool:<名字>）；
// 服务器标了 readOnlyHint 的工具不问。

const mcpProtocol = "2025-06-18"

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations *struct {
		ReadOnlyHint bool   `json:"readOnlyHint"`
		Title        string `json:"title"`
	} `json:"annotations"`
}

type mcpClient struct {
	name  string
	conn  mcpConn
	mu    sync.Mutex
	tools []mcpTool
}

type mcpConn interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify(ctx context.Context, method string, params any) error
	close()
}

type mcpPool struct {
	mu       sync.Mutex
	clients  map[string]*mcpClient
	failed   map[string]mcpFailure
	starting map[string]chan struct{}
}

type mcpFailure struct {
	err error
	at  time.Time
}

func mcpKey(s ext.MCPServer) string {
	b, _ := json.Marshal([]any{s.Name, s.Type, s.Command, s.Args, s.Env, s.URL, s.Headers})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// mcpServers：这个会话要用的 MCP 服务器（按设置的来源）。
func mcpServers(m *ext.Manager, cwd string) []ext.MCPServer {
	src := ext.SourceAgent
	var own []ext.MCPServer
	if m != nil {
		st := m.Settings()
		src = st.MCPSource
		if src.UsesGerdr() {
			for _, s := range st.MCP {
				if s.Enabled {
					own = append(own, s)
				}
			}
		}
	}
	var out []ext.MCPServer
	seen := map[string]bool{}
	add := func(ss []ext.MCPServer) {
		for _, s := range ss {
			if !seen[s.Name] {
				seen[s.Name] = true
				out = append(out, s)
			}
		}
	}
	add(own)
	if src != ext.SourceGerdr {
		add(ext.ClaudeMCP(cwd))
	}
	return out
}

// get 把这些服务器都接上（并发，最多等 timeout），返回接上了的和没接上的错误。
func (p *mcpPool) get(ctx context.Context, servers []ext.MCPServer, timeout time.Duration) ([]*mcpClient, map[string]error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var out []*mcpClient
	errs := map[string]error{}
	for _, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := p.client(ctx, s)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[s.Name] = err
				return
			}
			out = append(out, c)
		}()
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, errs
}

func (p *mcpPool) client(ctx context.Context, s ext.MCPServer) (*mcpClient, error) {
	key := mcpKey(s)
	for {
		p.mu.Lock()
		if p.clients == nil {
			p.clients, p.failed, p.starting = map[string]*mcpClient{}, map[string]mcpFailure{}, map[string]chan struct{}{}
		}
		if c := p.clients[key]; c != nil {
			p.mu.Unlock()
			return c, nil
		}
		if f, ok := p.failed[key]; ok && time.Since(f.at) < time.Minute {
			p.mu.Unlock()
			return nil, f.err
		}
		if ch := p.starting[key]; ch != nil {
			p.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		ch := make(chan struct{})
		p.starting[key] = ch
		p.mu.Unlock()

		// 起服务器不跟着这次请求的 ctx 走：等不及先返回，服务器照样起完，下一轮就有了
		c, err := startMCP(context.Background(), s)
		p.mu.Lock()
		delete(p.starting, key)
		if err != nil {
			p.failed[key] = mcpFailure{err, time.Now()}
		} else {
			p.clients[key] = c
			delete(p.failed, key)
		}
		p.mu.Unlock()
		close(ch)
		return c, err
	}
}

// drop：一个服务器断了（进程退出 / 连接坏了），下次用到时重起。
func (p *mcpPool) drop(c *mcpClient) {
	p.mu.Lock()
	for k, v := range p.clients {
		if v == c {
			delete(p.clients, k)
		}
	}
	p.mu.Unlock()
	c.conn.close()
}

func (p *mcpPool) closeAll() {
	p.mu.Lock()
	cs := p.clients
	p.clients = nil
	p.mu.Unlock()
	for _, c := range cs {
		c.conn.close()
	}
}

func startMCP(ctx context.Context, s ext.MCPServer) (*mcpClient, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var conn mcpConn
	var err error
	switch {
	case s.Type == "sse":
		conn, err = dialSSE(ctx, expand(s.URL), expandMap(s.Headers))
	case s.Remote():
		conn = &httpConn{url: expand(s.URL), headers: expandMap(s.Headers)}
	default:
		conn, err = startStdio(s)
	}
	if err != nil {
		return nil, fmt.Errorf("MCP %s: %w", s.Name, err)
	}
	c := &mcpClient{name: s.Name, conn: conn}
	if _, err := conn.call(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocol, "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "gerdr", "version": "1"},
	}); err != nil {
		conn.close()
		return nil, fmt.Errorf("MCP %s initialize: %w", s.Name, err)
	}
	_ = conn.notify(ctx, "notifications/initialized", map[string]any{})
	cursor := ""
	for i := 0; i < 20; i++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := conn.call(ctx, "tools/list", params)
		if err != nil {
			conn.close()
			return nil, fmt.Errorf("MCP %s tools/list: %w", s.Name, err)
		}
		var res struct {
			Tools      []mcpTool `json:"tools"`
			NextCursor string    `json:"nextCursor"`
		}
		_ = json.Unmarshal(raw, &res)
		c.tools = append(c.tools, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	return c, nil
}

// ${VAR} / $VAR 展开（Claude Code 的 MCP 配置也认）。
func expand(s string) string { return os.ExpandEnv(s) }

func expandMap(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = expand(v)
	}
	return out
}

// ---- 变成 agent 的工具 ----

var badToolChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

func mcpToolName(server, name string) string {
	n := "mcp__" + badToolChars.ReplaceAllString(server, "_") + "__" + badToolChars.ReplaceAllString(name, "_")
	if len(n) > 64 { // 两家接口都限制工具名 64 个字符
		h := sha256.Sum256([]byte(n))
		n = n[:55] + "_" + hex.EncodeToString(h[:4])
	}
	return n
}

func (a *Agent) mcpTools(c *mcpClient) []*tool {
	var out []*tool
	for _, mt := range c.tools {
		params := mt.InputSchema
		if params == nil {
			params = obj(map[string]any{})
		}
		if params["type"] == nil {
			params["type"] = "object"
		}
		if params["type"] == "object" && params["properties"] == nil {
			params["properties"] = map[string]any{}
		}
		desc := mt.Description
		if len(desc) > 2000 {
			desc = desc[:2000] + "…"
		}
		desc = "[MCP server " + c.name + "] " + desc
		title := c.name + " · " + mt.Name
		if mt.Annotations != nil && mt.Annotations.Title != "" {
			title = c.name + " · " + mt.Annotations.Title
		}
		toolName := mt.Name
		out = append(out, &tool{
			name: mcpToolName(c.name, mt.Name), desc: desc, params: params,
			readOnly: mt.Annotations != nil && mt.Annotations.ReadOnlyHint,
			show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
				return title, acp.ToolKindOther, nil
			},
			run: func(cl *call, args map[string]any) result {
				if args == nil {
					args = map[string]any{}
				}
				raw, err := c.conn.call(cl.ctx, "tools/call", map[string]any{"name": toolName, "arguments": args})
				if err != nil {
					if cl.ctx.Err() != nil {
						return result{}
					}
					var dead *mcpDead
					if errors.As(err, &dead) {
						a.mcp.drop(c)
					}
					return errResult("MCP call failed: %v", err)
				}
				return mcpResult(raw, cl.r.model.seesImages())
			},
		})
	}
	return out
}

func mcpResult(raw json.RawMessage, images bool) result {
	var res struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
			Resource *struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"resource"`
		} `json:"content"`
		StructuredContent any  `json:"structuredContent"`
		IsError           bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return result{text: string(raw)}
	}
	var texts []string
	var imgs []llm.Part
	for _, c := range res.Content {
		switch c.Type {
		case "text":
			texts = append(texts, c.Text)
		case "image":
			if images {
				imgs = append(imgs, llm.Part{Type: "image", MIME: c.MimeType, Data: c.Data})
			} else {
				texts = append(texts, "[image omitted: the current model cannot view images]")
			}
		case "resource":
			if c.Resource != nil {
				texts = append(texts, fmt.Sprintf("<resource uri=%q>\n%s\n</resource>", c.Resource.URI, c.Resource.Text))
			}
		}
	}
	if len(texts) == 0 && res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		texts = append(texts, string(b))
	}
	text := headTail(strings.Join(texts, "\n"), outMax)
	if text == "" {
		text = "(empty result)"
	}
	return result{text: text, images: imgs, err: res.IsError}
}

// ---- JSON-RPC 的公共部分 ----

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// mcpDead：连接已经坏了（进程退出），这个客户端要扔掉重起。
type mcpDead struct{ err error }

func (e *mcpDead) Error() string { return "MCP server connection closed: " + e.err.Error() }

// rpcMux：按 id 配对请求和响应（stdio 和老式 SSE 共用）。
type rpcMux struct {
	next    atomic.Int64
	mu      sync.Mutex
	waiting map[int64]chan rpcMsg
	dead    error
}

func (m *rpcMux) register() (int64, chan rpcMsg, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dead != nil {
		return 0, nil, &mcpDead{m.dead}
	}
	if m.waiting == nil {
		m.waiting = map[int64]chan rpcMsg{}
	}
	id := m.next.Add(1)
	ch := make(chan rpcMsg, 1)
	m.waiting[id] = ch
	return id, ch, nil
}

func (m *rpcMux) forget(id int64) {
	m.mu.Lock()
	delete(m.waiting, id)
	m.mu.Unlock()
}

func (m *rpcMux) deliver(msg rpcMsg) {
	var id int64
	if json.Unmarshal(msg.ID, &id) != nil {
		return
	}
	m.mu.Lock()
	ch := m.waiting[id]
	delete(m.waiting, id)
	m.mu.Unlock()
	if ch != nil {
		ch <- msg
	}
}

func (m *rpcMux) fail(err error) {
	m.mu.Lock()
	m.dead = err
	w := m.waiting
	m.waiting = nil
	m.mu.Unlock()
	for _, ch := range w {
		close(ch)
	}
}

func (m *rpcMux) wait(ctx context.Context, id int64, ch chan rpcMsg) (json.RawMessage, error) {
	select {
	case msg, ok := <-ch:
		if !ok {
			m.mu.Lock()
			err := m.dead
			m.mu.Unlock()
			return nil, &mcpDead{err}
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("%s (code %d)", msg.Error.Message, msg.Error.Code)
		}
		return msg.Result, nil
	case <-ctx.Done():
		m.forget(id)
		return nil, ctx.Err()
	}
}

func request(id int64, method string, params any) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return b
}

// 服务器发来的请求（roots/list、sampling……）我们都不支持：回 method not found，别让它干等。
func unsupported(msg rpcMsg) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
	return b
}

// ---- stdio ----

type stdioConn struct {
	cmd *exec.Cmd
	mu  sync.Mutex // 写 stdin
	in  io.WriteCloser
	mux rpcMux
}

func startStdio(s ext.MCPServer) (*stdioConn, error) {
	if s.Command == "" {
		return nil, errors.New("no command")
	}
	sysenv.AdoptLoginPath()
	args := make([]string, len(s.Args))
	for i, a := range s.Args {
		args[i] = expand(a)
	}
	cmd := exec.Command(expand(s.Command), args...)
	var env []string
	for k, v := range s.Env {
		env = append(env, k+"="+expand(v))
	}
	cmd.Env = sysenv.ChildEnv(env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr tailWriter
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &stdioConn{cmd: cmd, in: in}
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
		for sc.Scan() {
			var msg rpcMsg
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue // 有的服务器往 stdout 打日志
			}
			switch {
			case msg.Method != "" && len(msg.ID) > 0:
				c.write(unsupported(msg))
			case msg.Method == "":
				c.mux.deliver(msg)
			}
		}
		werr := cmd.Wait()
		if t := strings.TrimSpace(stderr.String()); t != "" {
			werr = fmt.Errorf("%v: %s", werr, oneLine(t, 400))
		} else if werr == nil {
			werr = errors.New("process exited")
		}
		c.mux.fail(werr)
	}()
	return c, nil
}

func (c *stdioConn) write(b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.in.Write(append(b, '\n'))
	return err
}

func (c *stdioConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id, ch, err := c.mux.register()
	if err != nil {
		return nil, err
	}
	if err := c.write(request(id, method, params)); err != nil {
		c.mux.forget(id)
		return nil, &mcpDead{err}
	}
	return c.mux.wait(ctx, id, ch)
}

func (c *stdioConn) notify(ctx context.Context, method string, params any) error {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	return c.write(b)
}

func (c *stdioConn) close() {
	_ = c.in.Close()
	if c.cmd.Process != nil {
		go func() {
			time.Sleep(2 * time.Second)
			_ = c.cmd.Process.Kill()
		}()
	}
}

type tailWriter struct {
	mu sync.Mutex
	b  []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.b = append(w.b, p...)
	if len(w.b) > 4096 {
		w.b = w.b[len(w.b)-4096:]
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.b)
}

// ---- streamable HTTP ----

type httpConn struct {
	url     string
	headers map[string]string
	next    atomic.Int64
	mu      sync.Mutex
	session string
}

var mcpHTTP = &http.Client{Timeout: 10 * time.Minute}

func (c *httpConn) post(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpProtocol)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	c.mu.Lock()
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	c.mu.Unlock()
	res, err := mcpHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if sid := res.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.session = sid
		c.mu.Unlock()
	}
	if res.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body.Close()
		if res.StatusCode == 401 {
			return nil, fmt.Errorf("HTTP 401: the server needs authorization (OAuth is not supported; put a token in the server's headers)")
		}
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, oneLine(string(b), 300))
	}
	return res, nil
}

func (c *httpConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.next.Add(1)
	res, err := c.post(ctx, request(id, method, params))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	handle := func(msg rpcMsg) (json.RawMessage, bool, error) {
		var got int64
		if msg.Method != "" || json.Unmarshal(msg.ID, &got) != nil || got != id {
			return nil, false, nil
		}
		if msg.Error != nil {
			return nil, true, fmt.Errorf("%s (code %d)", msg.Error.Message, msg.Error.Code)
		}
		return msg.Result, true, nil
	}
	if strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		var out json.RawMessage
		var outErr error
		found := false
		err := readEvents(res.Body, func(event, data string) bool {
			var msg rpcMsg
			if json.Unmarshal([]byte(data), &msg) != nil {
				return true
			}
			r, ok, e := handle(msg)
			if ok {
				out, outErr, found = r, e, true
				return false
			}
			return true
		})
		if found {
			return out, outErr
		}
		if err == nil {
			err = errors.New("stream ended without a response")
		}
		return nil, err
	}
	var msg rpcMsg
	if err := json.NewDecoder(res.Body).Decode(&msg); err != nil {
		return nil, err
	}
	r, ok, e := handle(msg)
	if !ok {
		return nil, errors.New("unexpected response")
	}
	return r, e
}

func (c *httpConn) notify(ctx context.Context, method string, params any) error {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	res, err := c.post(ctx, b)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

func (c *httpConn) close() {}

// readEvents：读 SSE，回调返回 false 就停。
func readEvents(r io.Reader, on func(event, data string) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	event, data := "", ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if data != "" || event != "" {
				if !on(event, strings.TrimSuffix(data, "\n")) {
					return nil
				}
			}
			event, data = "", ""
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data += strings.TrimPrefix(line[5:], " ") + "\n"
		}
	}
	if data != "" {
		on(event, strings.TrimSuffix(data, "\n"))
	}
	return sc.Err()
}

// ---- 老式 SSE ----

type sseConn struct {
	post    string
	headers map[string]string
	cancel  context.CancelFunc
	mux     rpcMux
}

func dialSSE(ctx context.Context, u string, headers map[string]string) (*sseConn, error) {
	lctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(lctx, "GET", u, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if res.StatusCode >= 400 {
		res.Body.Close()
		cancel()
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	c := &sseConn{headers: headers, cancel: cancel}
	endpoint := make(chan string, 1)
	go func() {
		err := readEvents(res.Body, func(event, data string) bool {
			switch event {
			case "endpoint":
				select {
				case endpoint <- data:
				default:
				}
			default:
				var msg rpcMsg
				if json.Unmarshal([]byte(data), &msg) == nil && msg.Method == "" {
					c.mux.deliver(msg)
				}
			}
			return true
		})
		res.Body.Close()
		if err == nil {
			err = errors.New("SSE stream closed")
		}
		c.mux.fail(err)
	}()
	select {
	case ep := <-endpoint:
		base, _ := url.Parse(u)
		if p, err := base.Parse(ep); err == nil {
			c.post = p.String()
		} else {
			c.post = ep
		}
		return c, nil
	case <-ctx.Done():
		cancel()
		return nil, errors.New("no endpoint event from the SSE server")
	}
}

func (c *sseConn) send(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, "POST", c.post, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	res, err := mcpHTTP.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}

func (c *sseConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id, ch, err := c.mux.register()
	if err != nil {
		return nil, err
	}
	if err := c.send(ctx, request(id, method, params)); err != nil {
		c.mux.forget(id)
		return nil, err
	}
	return c.mux.wait(ctx, id, ch)
}

func (c *sseConn) notify(ctx context.Context, method string, params any) error {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	return c.send(ctx, b)
}

func (c *sseConn) close() { c.cancel() }
