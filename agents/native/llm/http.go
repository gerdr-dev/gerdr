package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPError：接口回了非 200。Retryable 的（429 / 5xx）在拿到响应头之前自动重试。
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("model API %d: %s", e.Status, oneLine(e.Body, 600))
}

// 重试几次、等多久：网关偶发的 502 / 429 一般几秒就好；再久就交给 hub 的整轮重试（它会发「继续」）。
var retryWaits = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

// OnRetry 在每次自动重试前调（给界面显示一行「在重试」）。nil = 不报。
type retryKey struct{}

func WithRetryNotice(ctx context.Context, f func(err error, attempt int, wait time.Duration)) context.Context {
	return context.WithValue(ctx, retryKey{}, f)
}

var httpClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	ResponseHeaderTimeout: 5 * time.Minute, // 推理模型第一个字节可能很久才来
	IdleConnTimeout:       90 * time.Second,
	MaxIdleConnsPerHost:   8,
}}

// post 发请求，拿到 200 的响应体（SSE）。网络错误、429、5xx 在拿到响应之前重试。
func post(ctx context.Context, ep Endpoint, path string, body any) (io.ReadCloser, error) {
	h := http.Header{}
	if ep.APIKey != "" {
		h.Set("Authorization", "Bearer "+ep.APIKey)
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
	var last error
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		for k, v := range auth {
			req.Header[k] = v
		}
		for k, v := range extra {
			req.Header.Set(k, v)
		}
		res, err := httpClient.Do(req)
		var wait time.Duration
		if err == nil {
			if res.StatusCode == 200 {
				return res.Body, nil
			}
			b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
			res.Body.Close()
			last = &HTTPError{Status: res.StatusCode, Body: string(b)}
			if res.StatusCode != 429 && res.StatusCode != 408 && res.StatusCode < 500 {
				return nil, last
			}
			if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 && s <= 60 {
				wait = time.Duration(s) * time.Second
			}
		} else {
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
