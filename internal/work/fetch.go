package work

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// fetch as in browsers and Annulo's functions:
//
//	const res = await fetch(url, { method, headers, body })
//	const reader = res.body.getReader(); const dec = new TextDecoder()
//	for (;;) { const { done, value } = await reader.read(); if (done) break; buf += dec.decode(value, { stream: true }) }
//
// or text() / json() / arrayBuffer() for the rest at once. Reads are synchronous (one at a time), each returns a settled
// promise. Responses also carry timing { ttfb_ms, total_ms } and truncated.
//
// Unlike Annulo, local and private addresses are allowed: a gerdr function runs for the one user on their own machine and
// can ctx.exec anything anyway; internal company services are a common thing to read.

// maxFetchBody: text() past it is cut and marked truncated; a streamed read past it fails
const maxFetchBody = 10 << 20

// A streamed response waits fetchHeaderWait for its headers, then gives up after fetchIdleWait without data; there's no
// total limit (the run's own applies): SSE APIs talk for minutes but keep sending.
var (
	fetchHeaderWait = 2 * time.Minute
	fetchIdleWait   = time.Minute
	fetchClient     = &http.Client{}
)

type fetchRequest struct {
	URL     string
	Method  string
	Headers map[string]string
	Body    string
}

func (r *runner) request(u string, init map[string]any) fetchRequest {
	req := fetchRequest{URL: u, Method: "GET", Headers: map[string]string{}}
	if m, ok := init["method"].(string); ok && m != "" {
		req.Method = strings.ToUpper(m)
	}
	if hs, ok := init["headers"].(map[string]any); ok {
		for k, v := range hs {
			req.Headers[k] = fmt.Sprint(v)
		}
	}
	switch b := init["body"].(type) {
	case string:
		req.Body = b
	case nil:
	default:
		j, _ := json.Marshal(toPlain(b))
		req.Body = string(j)
	}
	return req
}

type fetchResponse struct {
	Status     int
	StatusText string
	URL        string
	Headers    map[string]string
	TTFBMs     int64
	Stream     io.ReadCloser // streamed body; nil when Body holds it all
	Body       []byte
	TotalMs    int64
	Truncated  bool
}

func newHTTPRequest(ctx context.Context, r fetchRequest) (*http.Request, error) {
	u, err := url.Parse(r.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("only http / https URLs can be fetched: %q", r.URL)
	}
	var body io.Reader
	if r.Body != "" {
		body = strings.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("user-agent") == "" {
		req.Header.Set("user-agent", "gerdr-local-function")
	}
	return req, nil
}

func headersOf(h http.Header) map[string]string {
	out := map[string]string{}
	for k := range h {
		out[strings.ToLower(k)] = h.Get(k)
	}
	return out
}

// doStream returns at the headers; the body is read as the function goes, cut after fetchIdleWait without data.
func doStream(ctx context.Context, r fetchRequest) (*fetchResponse, error) {
	ctx, cancel := context.WithCancel(ctx)
	idle := &idleBody{d: fetchIdleWait, cancel: cancel}
	idle.timer = time.AfterFunc(fetchHeaderWait, idle.expire)
	req, err := newHTTPRequest(ctx, r)
	if err != nil {
		idle.Close()
		return nil, err
	}
	start := time.Now()
	resp, err := fetchClient.Do(req)
	if err != nil {
		idle.Close()
		if idle.expired.Load() {
			return nil, fmt.Errorf("no response after %d seconds", int(fetchHeaderWait.Seconds()))
		}
		return nil, err
	}
	idle.rc = resp.Body
	idle.timer.Reset(fetchIdleWait)
	return &fetchResponse{Status: resp.StatusCode, StatusText: http.StatusText(resp.StatusCode), URL: resp.Request.URL.String(), Headers: headersOf(resp.Header),
		TTFBMs: time.Since(start).Milliseconds(), Stream: idle}, nil
}

// doFull reads the whole response (fetchAll): 90 seconds and 10MB at most; a body cut short still comes back, marked truncated.
func doFull(ctx context.Context, r fetchRequest) (*fetchResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := newHTTPRequest(ctx, r)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	resp, err := fetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	ttfb := time.Since(start)
	b, rerr := io.ReadAll(io.LimitReader(resp.Body, maxFetchBody+1))
	truncated := rerr != nil || len(b) > maxFetchBody
	if len(b) > maxFetchBody {
		b = b[:maxFetchBody]
	}
	return &fetchResponse{Status: resp.StatusCode, StatusText: http.StatusText(resp.StatusCode), URL: resp.Request.URL.String(), Headers: headersOf(resp.Header),
		Body: b, TTFBMs: ttfb.Milliseconds(), TotalMs: time.Since(start).Milliseconds(), Truncated: truncated}, nil
}

// idleBody is a streamed body: each read with data restarts the clock, d without data cancels the request.
type idleBody struct {
	rc      io.ReadCloser
	d       time.Duration
	timer   *time.Timer
	cancel  context.CancelFunc
	expired atomic.Bool
}

func (b *idleBody) expire() {
	b.expired.Store(true)
	b.cancel()
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.timer.Reset(b.d)
	}
	if err != nil && err != io.EOF && b.expired.Load() {
		err = fmt.Errorf("no data for %d seconds, gave up", int(b.d.Seconds()))
	}
	return n, err
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	b.cancel()
	if b.rc != nil {
		return b.rc.Close()
	}
	return nil
}

func (r *runner) fetch(u string, init map[string]any) *goja.Promise {
	start := time.Now()
	resp, err := doStream(r.ctx, r.request(u, init))
	if err != nil {
		return r.rejected(fmt.Errorf("fetch %s failed: %w", u, err))
	}
	return r.resolved(r.response(resp, start))
}

// fetchAll: await ctx.fetchAll([url or { url, method, headers, body }, …]) runs them 4 at a time and returns the
// responses in order; a failed one is { ok: false, status: 0, url, error } and doesn't fail the rest.
func (r *runner) fetchAll(items []any) *goja.Promise {
	reqs := make([]fetchRequest, len(items))
	for i, it := range items {
		switch v := it.(type) {
		case string:
			reqs[i] = r.request(v, nil)
		case map[string]any:
			u, _ := v["url"].(string)
			reqs[i] = r.request(u, v)
		default:
			reqs[i] = r.request(fmt.Sprint(v), nil)
		}
	}
	type res struct {
		resp *fetchResponse
		err  error
	}
	out := make([]res, len(reqs))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, q := range reqs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i].resp, out[i].err = doFull(r.ctx, q)
		}()
	}
	wg.Wait()
	// goja objects can only be made on the goroutine running the JS
	arr := make([]any, len(out))
	for i, o := range out {
		if o.err != nil {
			arr[i] = map[string]any{"ok": false, "status": 0, "url": reqs[i].URL, "error": o.err.Error()}
		} else {
			arr[i] = r.response(o.resp, time.Now())
		}
	}
	return r.resolved(arr)
}

type fetchBody struct {
	rc        io.Reader
	closer    io.Closer
	start     time.Time
	read      int
	done      bool
	truncated bool
	totalMs   int64
}

func newFetchBody(resp *fetchResponse, start time.Time) *fetchBody {
	if resp.Stream != nil {
		return &fetchBody{rc: resp.Stream, closer: resp.Stream, start: start}
	}
	return &fetchBody{rc: bytes.NewReader(resp.Body), start: start, truncated: resp.Truncated, totalMs: resp.TotalMs}
}

func (b *fetchBody) finish() {
	if b.done {
		return
	}
	b.done = true
	if b.totalMs == 0 {
		b.totalMs = time.Since(b.start).Milliseconds()
	}
	if b.closer != nil {
		b.closer.Close()
	}
}

// chunk reads the next piece; nil, nil at the end
func (b *fetchBody) chunk() ([]byte, error) {
	if b.done {
		return nil, nil
	}
	buf := make([]byte, 32<<10)
	for {
		n, err := b.rc.Read(buf)
		if n > 0 {
			b.read += n
			if b.read > maxFetchBody {
				b.finish()
				return nil, fmt.Errorf("the response is larger than %d MB", maxFetchBody>>20)
			}
			return buf[:n], nil
		}
		if err == io.EOF {
			b.finish()
			return nil, nil
		}
		if err != nil {
			b.finish()
			return nil, err
		}
	}
}

// all reads the rest. Past the limit or cut off (timeout) isn't a failure: what was read comes back, marked truncated
func (b *fetchBody) all() []byte {
	if b.done {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(b.rc, int64(maxFetchBody-b.read)+1))
	b.read += len(data)
	if err != nil || b.read > maxFetchBody {
		b.truncated = true
	}
	if b.read > maxFetchBody {
		data = data[:len(data)-(b.read-maxFetchBody)]
	}
	b.finish()
	return data
}

func (r *runner) response(resp *fetchResponse, start time.Time) *goja.Object {
	b := newFetchBody(resp, start)
	r.bodies = append(r.bodies, b)
	vm := r.vm
	o := vm.NewObject()
	timing := vm.NewObject()
	_ = timing.Set("ttfb_ms", resp.TTFBMs)
	_ = timing.DefineAccessorProperty("total_ms", vm.ToValue(func() int64 {
		if !b.done {
			return time.Since(b.start).Milliseconds()
		}
		return b.totalMs
	}), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = o.Set("timing", timing)
	_ = o.DefineAccessorProperty("truncated", vm.ToValue(func() bool { return b.truncated }), nil, goja.FLAG_FALSE, goja.FLAG_TRUE)
	_ = o.Set("status", resp.Status)
	_ = o.Set("statusText", resp.StatusText)
	_ = o.Set("ok", resp.Status >= 200 && resp.Status < 300)
	_ = o.Set("url", resp.URL)
	headers := vm.NewObject()
	_ = headers.Set("get", func(name string) any {
		for k, v := range resp.Headers {
			if strings.EqualFold(k, name) {
				return v
			}
		}
		return nil
	})
	_ = headers.Set("forEach", func(fn goja.Callable) {
		for k, v := range resp.Headers {
			_, _ = fn(goja.Undefined(), vm.ToValue(v), vm.ToValue(k))
		}
	})
	_ = o.Set("headers", headers)

	reader := vm.NewObject()
	_ = reader.Set("read", func() *goja.Promise {
		c, err := b.chunk()
		if err != nil {
			return r.rejected(fmt.Errorf("reading the response from %s failed: %w", resp.URL, err))
		}
		if c == nil {
			return r.resolved(map[string]any{"done": true, "value": goja.Undefined()})
		}
		return r.resolved(map[string]any{"done": false, "value": r.uint8Array(c)})
	})
	_ = reader.Set("cancel", func() *goja.Promise { b.finish(); return r.resolved(goja.Undefined()) })
	_ = reader.Set("releaseLock", func() {})
	body := vm.NewObject()
	_ = body.Set("getReader", func() *goja.Object { return reader })
	_ = o.Set("body", body)

	_ = o.Set("text", func() *goja.Promise { return r.resolved(string(b.all())) })
	_ = o.Set("json", func() *goja.Promise {
		data := b.all()
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return r.rejected(fmt.Errorf("the response isn't JSON (%d): %.200s", resp.Status, data))
		}
		return r.resolved(v)
	})
	_ = o.Set("arrayBuffer", func() *goja.Promise { return r.resolved(vm.NewArrayBuffer(b.all())) })
	return o
}

func (r *runner) uint8Array(data []byte) goja.Value {
	ab := r.vm.NewArrayBuffer(append([]byte(nil), data...))
	if ctor, ok := goja.AssertConstructor(r.vm.Get("Uint8Array")); ok {
		if v, err := ctor(nil, r.vm.ToValue(ab)); err == nil {
			return v
		}
	}
	return r.vm.ToValue(ab)
}

// bytesOf turns a Uint8Array / ArrayBuffer / string into bytes
func (r *runner) bytesOf(v goja.Value) []byte {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	switch x := v.Export().(type) {
	case goja.ArrayBuffer:
		return x.Bytes()
	case string:
		return []byte(x)
	}
	var data []byte
	if err := r.vm.ExportTo(v, &data); err != nil {
		r.throw(errors.New("expected a Uint8Array or ArrayBuffer: " + err.Error()))
	}
	return data
}

// installText adds TextDecoder / TextEncoder (UTF-8 only). decode(x, { stream: true }) keeps a character split across
// chunks for the next call, so streamed Chinese doesn't come out garbled.
func (r *runner) installText() {
	_ = r.vm.Set("TextDecoder", func(call goja.ConstructorCall) *goja.Object {
		var pending []byte
		d := call.This
		_ = d.Set("encoding", "utf-8")
		_ = d.Set("decode", func(v goja.Value, opts map[string]any) string {
			data := append(pending, r.bytesOf(v)...)
			pending = nil
			if stream, _ := opts["stream"].(bool); stream {
				for i := 1; i <= 3 && i <= len(data); i++ {
					if c := data[len(data)-i]; c&0xC0 != 0x80 { // the character's first byte
						if !utf8.FullRune(data[len(data)-i:]) {
							pending = append([]byte(nil), data[len(data)-i:]...)
							data = data[:len(data)-i]
						}
						break
					}
				}
			}
			return strings.ToValidUTF8(string(data), "�")
		})
		return nil
	})
	_ = r.vm.Set("TextEncoder", func(call goja.ConstructorCall) *goja.Object {
		_ = call.This.Set("encoding", "utf-8")
		_ = call.This.Set("encode", func(s string) goja.Value { return r.uint8Array([]byte(s)) })
		return nil
	})
}
