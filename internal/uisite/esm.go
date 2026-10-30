package uisite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ESM is the local proxy behind /_esm/: forwards to an esm.sh instance (DefaultESMCDN, or GERDR_ESM_CDN) and caches results on disk.
// 为什么不让浏览器直连 CDN：① 第一次拉过之后离线也能开（agent 本身要联网，但界面不该跟着白屏）；
// ② 国内直连 esm.sh 时快时慢，缓存之后只慢第一次；③ CSP 只用放行 'self'。
//
// esm.sh 返回的模块里 import 的都是以 / 开头的绝对路径（"/react@19.3.1/es2022/react.mjs"），
// 经过代理之后这些路径落在我们自己的根上，所以要改写成 /_esm/... —— 只改 import / export from 里的字符串。
type ESM struct {
	upstream string
	dir      string
	client   *http.Client

	mu       sync.Mutex
	inflight map[string]*fetchCall
}

type fetchCall struct {
	done chan struct{}
	res  *cached
	err  error
}

type cached struct {
	Status      int    `json:"status"`
	ContentType string `json:"contentType"`
	Body        []byte `json:"-"`
}

// DefaultESMCDN is our own esm.sh instance. The public esm.sh goes down now and then (e.g. its build disk filling
// up, 500 on every package not yet cached), which leaves newly added UI packages unloadable; ours is under our control.
const DefaultESMCDN = "https://esm.talizen.com"

func NewESM(upstream, cacheDir string) *ESM {
	if upstream == "" {
		upstream = DefaultESMCDN
	}
	return &ESM{upstream: strings.TrimRight(upstream, "/"), dir: cacheDir, client: &http.Client{Timeout: 60 * time.Second}, inflight: map[string]*fetchCall{}}
}

var esmImportRE = regexp.MustCompile(`((?:\bfrom|\bimport)\s*\(?\s*)(["'])/([^/"'])`)
var esmMapRE = regexp.MustCompile(`(sourceMappingURL=)/([^/])`)

func rewriteESM(b []byte) []byte {
	b = esmImportRE.ReplaceAll(b, []byte("${1}${2}/_esm/${3}"))
	return esmMapRE.ReplaceAll(b, []byte("${1}/_esm/${2}"))
}

func (e *ESM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/_esm")
	key := rest
	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}
	c, err := e.get(key)
	if err != nil {
		http.Error(w, "esm: "+err.Error(), http.StatusBadGateway)
		return
	}
	if c.ContentType != "" {
		w.Header().Set("content-type", c.ContentType)
	}
	if c.Status == http.StatusOK {
		w.Header().Set("cache-control", "public, max-age=31536000, immutable")
	}
	w.WriteHeader(c.Status)
	w.Write(c.Body)
}

func (e *ESM) file(key string) string {
	h := sha256.Sum256([]byte(key))
	s := hex.EncodeToString(h[:])
	return filepath.Join(e.dir, s[:2], s)
}

func (e *ESM) get(key string) (*cached, error) {
	p := e.file(key)
	if b, err := os.ReadFile(p); err == nil {
		if mb, err := os.ReadFile(p + ".json"); err == nil {
			var c cached
			if json.Unmarshal(mb, &c) == nil {
				c.Body = b
				return &c, nil
			}
		}
	}
	e.mu.Lock()
	if f, ok := e.inflight[key]; ok {
		e.mu.Unlock()
		<-f.done
		return f.res, f.err
	}
	f := &fetchCall{done: make(chan struct{})}
	e.inflight[key] = f
	e.mu.Unlock()
	f.res, f.err = e.fetch(key)
	e.mu.Lock()
	delete(e.inflight, key)
	e.mu.Unlock()
	close(f.done)
	if f.err == nil && f.res.Status == http.StatusOK {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		mb, _ := json.Marshal(f.res)
		if os.WriteFile(p+".tmp", f.res.Body, 0o644) == nil && os.Rename(p+".tmp", p) == nil {
			_ = os.WriteFile(p+".json", mb, 0o644)
		}
	}
	return f.res, f.err
}

// fetch：esm.sh 第一次被请求某个包时是现场编译的，偶尔回 5xx（实测 hast-util-to-parse5 第一次 500、
// 立刻再要就好了）。所以 5xx 和网络错误重试几次。
func (e *ESM) fetch(key string) (*cached, error) {
	var c *cached
	var err error
	for i, wait := range []time.Duration{0, time.Second, 3 * time.Second} {
		if i > 0 {
			time.Sleep(wait)
		}
		if c, err = e.fetchOnce(key); err == nil {
			return c, nil
		}
	}
	return nil, err
}

func (e *ESM) fetchOnce(key string) (*cached, error) {
	req, err := http.NewRequest(http.MethodGet, e.upstream+key, nil)
	if err != nil {
		return nil, err
	}
	// esm.sh 按 User-Agent 挑编译目标：固定成一个现代浏览器，缓存才对谁都能用（Safari 16+/Chrome 110+ 都吃 es2022）。
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	ct := resp.Header.Get("content-type")
	if strings.Contains(ct, "javascript") || strings.Contains(ct, "typescript") {
		body = rewriteESM(body)
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%s: %s", key, resp.Status)
	}
	return &cached{Status: resp.StatusCode, ContentType: ct, Body: body}, nil
}
