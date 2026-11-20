package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// HTTPError：接口回了非 200。Retryable 的（429 / 5xx）在拿到响应头之前自动重试。
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("model API %d: %s", e.Status, oneLine(bodyMessage(e.Body), 600))
}

// bodyMessage 取出错误响应体里给人看的那句话：OpenAI / Anthropic / 多数网关都是 {"error":{"message":…}}，
// 有的是 {"error":"…"} 或 {"message":"…"}。认不出就原样返回（界面上直接显示一坨 JSON，用户读不懂，比如「积分不足」藏在里面）
func bodyMessage(body string) string {
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(body)), &v) != nil {
		return body
	}
	var inner struct {
		Message string `json:"message"`
	}
	if len(v.Error) > 0 && json.Unmarshal(v.Error, &inner) == nil && strings.TrimSpace(inner.Message) != "" {
		return inner.Message
	}
	var str string
	if len(v.Error) > 0 && json.Unmarshal(v.Error, &str) == nil && strings.TrimSpace(str) != "" {
		return str
	}
	if strings.TrimSpace(v.Message) != "" {
		return v.Message
	}
	return body
}

// 重试几次、等多久：网关偶发的 502 / 429 一般几秒就好；再久就交给 hub 的整轮重试（它会发「继续」）。
var retryWaits = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

// OnRetry 在每次自动重试前调（给界面显示一行「在重试」）。nil = 不报。
type retryKey struct{}

func WithRetryNotice(ctx context.Context, f func(err error, attempt int, wait time.Duration)) context.Context {
	return context.WithValue(ctx, retryKey{}, f)
}

var httpClient = &http.Client{Transport: &http.Transport{
	Proxy:       http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
	// Without these a connect / handshake into a dead network waited on forever (the Transport's zero values mean none).
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 5 * time.Minute, // 推理模型第一个字节可能很久才来
	IdleConnTimeout:       90 * time.Second,
	MaxIdleConnsPerHost:   8,
}}

// streamIdle is how long a 200 response may go without a single byte before it counts as dead. A local proxy (a TUN
// one's 198.18.x.x fake IPs, Clash, Surge) keeps our side of the TCP connection alive after the network behind it
// drops, so neither an error nor TCP keepalive ever arrives: a turn sat on "retrying now" for over ten minutes after
// the network was back. 5 minutes like Codex's stream_idle_timeout and the header timeout above: a reasoning model
// can be silent for minutes. A var for tests.
var streamIdle = 5 * time.Minute

// errStreamIdle reads as a dropped stream (streamErr wraps it as "stream disconnected"), so the hub retries the turn.
var errStreamIdle = errors.New("no data from the model API for too long")

// idleBody cancels its request once no byte has come for streamIdle; the blocked Read then fails with errStreamIdle.
type idleBody struct {
	rc     io.ReadCloser
	timer  *time.Timer
	cancel context.CancelFunc
	idle   time.Duration
	fired  atomic.Bool
}

func newIdleBody(rc io.ReadCloser, cancel context.CancelFunc, idle time.Duration) *idleBody {
	b := &idleBody{rc: rc, cancel: cancel, idle: idle}
	b.timer = time.AfterFunc(idle, func() {
		b.fired.Store(true)
		cancel()
	})
	return b
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.timer.Reset(b.idle)
	}
	if err != nil && b.fired.Load() {
		return n, fmt.Errorf("%w (%s)", errStreamIdle, b.idle)
	}
	return n, err
}

func (b *idleBody) Close() error {
	b.timer.Stop()
	err := b.rc.Close()
	b.cancel()
	return err
}

// post 发请求，拿到 200 的响应体（SSE）。网络错误、429、5xx 在拿到响应之前重试。
// session 是会话 id（Request.CacheKey），放进 x-session-id 头：服务商据此把同一段对话的请求串起来。
func post(ctx context.Context, ep Endpoint, path, session string, body any) (io.ReadCloser, error) {
	h := http.Header{}
	if ep.APIKey != "" {
		h.Set("Authorization", "Bearer "+ep.APIKey)
	}
	if session != "" {
		h.Set("X-Session-Id", session)
	}
	return postURL(ctx, strings.TrimRight(ep.BaseURL, "/")+path, h, ep.Headers, body)
}

// postURL is post with the auth headers already chosen (the protocols put the key in different headers);
// extra (the provider's own headers) is applied last so it can override them.
func postURL(ctx context.Context, url string, auth http.Header, extra map[string]string, body any) (io.ReadCloser, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return postBytes(ctx, url, auth, extra, "application/json", buf)
}

// postBytes is postURL for a body already encoded (a multipart upload: OpenAI's image edits), with the same retries.
func postBytes(ctx context.Context, url string, auth http.Header, extra map[string]string, contentType string, buf []byte) (io.ReadCloser, error) {
	var last error
	for attempt := 0; ; attempt++ {
		// rctx: the idle watchdog's handle on this one request (cancelling ctx itself would read as a stop)
		rctx, cancel := context.WithCancel(ctx)
		req, err := http.NewRequestWithContext(rctx, "POST", url, bytes.NewReader(buf))
		if err != nil {
			cancel()
			return nil, err
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Accept", "text/event-stream")
		for k, v := range auth {
			req.Header[k] = v
		}
		for k, v := range extra {
			req.Header.Set(k, v)
		}
		res, err := httpClient.Do(req)
		if err == nil && res.StatusCode == 200 {
			return newIdleBody(res.Body, cancel, streamIdle), nil
		}
		var wait time.Duration
		if err == nil {
			b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
			res.Body.Close()
			cancel()
			last = &HTTPError{Status: res.StatusCode, Body: string(b)}
			if res.StatusCode != 429 && res.StatusCode != 408 && res.StatusCode < 500 {
				return nil, last
			}
			if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 && s <= 60 {
				wait = time.Duration(s) * time.Second
			}
		} else {
			cancel()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = fmt.Errorf("network error: %w", err)
		}
		if attempt >= len(retryWaits) {
			return nil, last
		}
		if wait == 0 {
			wait = retryWaits[attempt]
		}
		if f, ok := ctx.Value(retryKey{}).(func(error, int, time.Duration)); ok && f != nil {
			f(last, attempt+1, wait)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// readSSE 逐条读 `data:` 的内容（跳过 event: / 注释 / 空行），直到 [DONE] 或 EOF。
// 回调返回错误就停。
func readSSE(r io.Reader, on func(data []byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 32<<20) // 一条 output_item.done 可能带着很长的 encrypted_content
	var data bytes.Buffer
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		d := bytes.TrimSpace(data.Bytes())
		data.Reset()
		if string(d) == "[DONE]" {
			return io.EOF
		}
		return on(d)
	}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			if data.Len() > 0 {
				// 有的网关两条 data 之间不空行：前一条先交出去
				if err := flush(); err != nil {
					return err
				}
			}
			data.Write(bytes.TrimPrefix(bytes.TrimPrefix(line, []byte("data:")), []byte(" ")))
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return sc.Err()
}

// streamErr 把读流时的错误包成 hub 认得出要重试的样子（hub.retryable 认 "stream disconnected"）。
func streamErr(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae
	}
	return fmt.Errorf("stream disconnected: %w", err)
}

// APIError：流里报的错（接口已经回了 200，错误在 SSE 里）。原样交出去，不当成断线。
type APIError struct{ Msg string }

func (e *APIError) Error() string { return "model API error: " + oneLine(e.Msg, 600) }

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func dataURL(p Part) string {
	mime := p.MIME
	if mime == "" {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + p.Data
}
