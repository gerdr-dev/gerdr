package projbrowser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// A minimal DevTools client over --remote-debugging-pipe (JSON messages each followed by a NUL byte; the browser
// reads fd 3 and writes fd 4), like Lightron's internal/cdp. A pipe rather than --remote-debugging-port: no other
// process on the computer can connect and drive a browser that holds the project's logins.

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

func (e *cdpError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

var errClosed = errors.New("the browser closed")

type conn struct {
	w       io.WriteCloser
	r       *bufio.Reader
	wmu     sync.Mutex
	seq     atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan *msg
	onEvent func(*msg)
	closed  chan struct{}
}

// newConn: onEvent runs on the reading goroutine, in order; a handler that calls back into the browser must do it on
// a goroutine of its own.
func newConn(w io.WriteCloser, r io.Reader, onEvent func(*msg)) *conn {
	c := &conn{w: w, r: bufio.NewReaderSize(r, 1<<20), pending: map[int64]chan *msg{}, onEvent: onEvent, closed: make(chan struct{})}
	go c.read()
	return c
}

func (c *conn) read() {
	defer close(c.closed)
	for {
		b, err := c.r.ReadBytes(0)
		if err != nil {
			c.mu.Lock()
			for id, ch := range c.pending {
				ch <- &msg{Error: &cdpError{Message: errClosed.Error()}}
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
		if c.onEvent != nil {
			c.onEvent(&m)
		}
	}
}

// call sends method to session ("" = the browser) and waits for its result.
func (c *conn) call(ctx context.Context, session, method string, params any) (json.RawMessage, error) {
	select {
	case <-c.closed:
		return nil, errClosed
	default:
	}
	id := c.seq.Add(1)
	ch := make(chan *msg, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	req := map[string]any{"id": id, "method": method}
	if raw, ok := params.(json.RawMessage); ok {
		if len(raw) > 0 && string(raw) != "null" {
			req["params"] = raw
		}
	} else if params != nil {
		req["params"] = params
	}
	if session != "" {
		req["sessionId"] = session
	}
	b, err := json.Marshal(req)
	if err == nil {
		c.wmu.Lock()
		_, err = c.w.Write(append(b, 0))
		c.wmu.Unlock()
	}
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, fmt.Errorf("%s: %w", method, m.Error)
		}
		if len(m.Result) == 0 {
			return json.RawMessage("{}"), nil
		}
		return m.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("%s: %w", method, ctx.Err())
	}
}
