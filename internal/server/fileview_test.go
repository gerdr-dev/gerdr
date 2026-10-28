package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct{ p, cwd, want string }{
		{"/tmp/a.png", "/x", "/tmp/a.png"},
		{"~/a.png", "/x", filepath.Join(home, "a.png")},
		{"docs/a.md", "/repo", "/repo/docs/a.md"},
		{"../b.md", "/repo/sub", "/repo/b.md"},
		{"a.md", "~/repo", filepath.Join(home, "repo/a.md")},
		{"a.md", "", filepath.Join(home, "a.md")},
	}
	for _, c := range cases {
		if got := resolvePath(c.p, c.cwd); got != c.want {
			t.Errorf("resolvePath(%q, %q) = %q, want %q", c.p, c.cwd, got, c.want)
		}
	}
}

// raw 不能把 html 当页面吐（同源脚本），而且要带 Range（视频拖进度条）。
func TestFileRaw(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.html"), []byte("<script>alert(1)</script>"), 0o644)
	os.WriteFile(filepath.Join(dir, "v.mp4"), []byte("0123456789"), 0o644)
	s := &Server{}
	get := func(name string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/fs/raw?path="+url.QueryEscape(name)+"&cwd="+url.QueryEscape(dir), nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.fileRaw(w, r)
		return w
	}
	w := get("x.html", nil)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("html 的 Content-Type = %q，要 text/plain", ct)
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("没有 sandbox：%q", w.Header().Get("Content-Security-Policy"))
	}
	w = get("v.mp4", map[string]string{"Range": "bytes=2-4"})
	if w.Code != http.StatusPartialContent || w.Body.String() != "234" {
		t.Errorf("Range：%d %q", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("mp4 的 Content-Type = %q", ct)
	}
	if w = get("nope.png", nil); w.Code != 404 {
		t.Errorf("不存在的文件：%d", w.Code)
	}
}
