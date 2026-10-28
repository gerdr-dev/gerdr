// Package rpcline 是一个按行分帧的双向 JSON-RPC 连接（每行一个 JSON，不带 "jsonrpc" 字段）。
// codex app-server 的 stdio 就是这个格式：两边都能发请求，也都能发通知。
package rpcline

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
)

type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return fmt.Sprintf("rpc error %d: %s (%s)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Handler 处理对端发来的请求（返回结果或错误）和通知（id 为 nil）。
// 通知按收到的顺序串行处理；请求各自起 goroutine（审批这类请求会等人，不能堵住读循环）。
type Handler interface {
	HandleRequest(ctx context.Context, method string, params json.RawMessage) (any, error)
	HandleNotification(method string, params json.RawMessage)
}

type Conn struct {
	w       io.Writer
	wmu     sync.Mutex
	handler Handler
	log     *slog.Logger

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan message
	closed  bool
	done    chan struct{}
	err     error

	notes chan message
}

func New(r io.Reader, w io.Writer, h Handler, log *slog.Logger) *Conn {
	c := &Conn{w: w, handler: h, log: log, pending: map[string]chan message{}, done: make(chan struct{}), notes: make(chan message, 4096)}
	go c.readLoop(r)
	go c.noteLoop()
	return c
}

func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) write(m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(b)
	return err
}

// Call 发请求并等响应。ctx 取消只是不再等，对端那边不会被取消（app-server 没有取消请求这回事）。
func (c *Conn) Call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	if c.closed {
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = errors.New("connection closed")
		}
		return err
	}
	c.nextID++
	id := strconv.FormatInt(c.nextID, 10)
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := c.write(message{ID: json.RawMessage(id), Method: method, Params: raw}); err != nil {
		c.drop(id)
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.drop(id)
		return ctx.Err()
	case <-c.done:
		return fmt.Errorf("connection closed: %w", c.Err())
	}
}

func (c *Conn) Notify(method string, params any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	return c.write(message{Method: method, Params: raw})
}

func (c *Conn) drop(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Conn) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	// 一行可以很大（thread/turns/list、带图的消息）。
	sc.Buffer(make([]byte, 0, 1<<20), 256<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			c.log.Warn("rpcline: bad line", "err", err, "line", truncate(string(line), 300))
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			go c.serve(m)
		case m.Method != "":
			c.notes <- m
		case len(m.ID) > 0:
			key := normID(m.ID)
			c.mu.Lock()
			ch := c.pending[key]
			delete(c.pending, key)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	c.mu.Lock()
	c.closed = true
	c.err = err
	c.mu.Unlock()
	close(c.notes)
	close(c.done)
}

func (c *Conn) noteLoop() {
	for m := range c.notes {
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.log.Error("rpcline: notification handler panic", "method", m.Method, "panic", r)
				}
			}()
			c.handler.HandleNotification(m.Method, m.Params)
		}()
	}
}

func (c *Conn) serve(m message) {
	var resp message
	resp.ID = m.ID
	func() {
		defer func() {
			if r := recover(); r != nil {
				resp.Error = &Error{Code: -32603, Message: fmt.Sprint("panic: ", r)}
			}
		}()
		res, err := c.handler.HandleRequest(context.Background(), m.Method, m.Params)
		if err != nil {
			var re *Error
			if errors.As(err, &re) {
				resp.Error = re
			} else {
				resp.Error = &Error{Code: -32603, Message: err.Error()}
			}
			return
		}
		b, err := json.Marshal(res)
		if err != nil {
			resp.Error = &Error{Code: -32603, Message: err.Error()}
			return
		}
		resp.Result = b
	}()
	if err := c.write(resp); err != nil {
		c.log.Warn("rpcline: write response failed", "err", err)
	}
}

// normID 把数字 id 和字符串 id 统一成一个 map key（我们自己发出去的都是数字）。
func normID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
