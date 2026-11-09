package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// A minimal Chrome DevTools Protocol client over --remote-debugging-pipe: JSON messages, each followed by a NUL byte,
// Chrome reading fd 3 and writing fd 4. The pipe (not --remote-debugging-port) matters twice: no other local process
// can connect and drive the browser, and Chrome only offers some domains (PWA) to a pipe client.

type msg struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string { return fmt.Sprintf("%d %s", e.Code, e.Message) }

type cdp struct {
	in      *os.File
	out     *bufio.Reader
	wmu     sync.Mutex
	seq     atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan *msg
	onEvent func(*msg)
	closed  chan struct{}
}

func pipeConn(in, out *os.File, onEvent func(*msg)) *cdp {
	c := &cdp{in: in, out: bufio.NewReaderSize(out, 1<<20), pending: map[int64]chan *msg{}, onEvent: onEvent, closed: make(chan struct{})}
	go c.read()
	return c
}

func (c *cdp) read() {
	defer close(c.closed)
	for {
		b, err := c.out.ReadBytes(0)
		if err != nil {
			c.mu.Lock()
			for id, ch := range c.pending {
				ch <- &msg{Error: &cdpError{Message: "browser connection closed"}}
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var m msg
		if json.Unmarshal(b[:len(b)-1], &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- &m
			}
			continue
		}
		// Events are handled in order on this goroutine; handlers that call back into Chrome must do it in their own.
		c.onEvent(&m)
	}
}

func (c *cdp) call(session, method string, params any) (json.RawMessage, error) {
	id := c.seq.Add(1)
	ch := make(chan *msg, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	req := map[string]any{"id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if session != "" {
		req["sessionId"] = session
	}
	b, _ := json.Marshal(req)
	c.wmu.Lock()
	_, err := c.in.Write(append(b, 0))
	c.wmu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, fmt.Errorf("%s: %w", method, m.Error)
		}
		return m.Result, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("%s: timeout", method)
	}
}

// eval runs JS in a page and returns its value (promises awaited).
func (c *cdp) eval(session, js string) (json.RawMessage, error) {
	r, err := c.call(session, "Runtime.evaluate", map[string]any{"expression": js, "awaitPromise": true, "returnByValue": true})
	if err != nil {
		return nil, err
	}
	var v struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	_ = json.Unmarshal(r, &v)
	if e := v.ExceptionDetails; e != nil {
		return nil, fmt.Errorf("js: %s %s", e.Text, e.Exception.Description)
	}
	return v.Result.Value, nil
}
