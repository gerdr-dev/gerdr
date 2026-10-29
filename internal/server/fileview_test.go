package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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

// agent 写短了的相对路径（`chat/a.tsx`）：唯一一个对得上就打开它，好几个就给候选，`..` 和绝对路径不猜。
func TestFileStatSuffix(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"ui/src/chat/a.tsx", "x/b.go", "y/b.go"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	s := &Server{}
	stat := func(p string) (int, string) {
		r := httptest.NewRequest("GET", "/api/fs/stat?path="+url.QueryEscape(p)+"&cwd="+url.QueryEscape(dir), nil)
		w := httptest.NewRecorder()
		s.fileStat(w, r)
		return w.Code, w.Body.String()
	}
	if code, body := stat("chat/a.tsx"); code != 200 || !strings.Contains(body, filepath.Join(dir, "ui/src/chat/a.tsx")) {
		t.Errorf("唯一匹配：%d %s", code, body)
	}
	if code, body := stat("b.go"); code != 404 || !strings.Contains(body, "candidates") || !strings.Contains(body, filepath.Join(dir, "y/b.go")) {
		t.Errorf("多个匹配：%d %s", code, body)
	}
	if code, body := stat("hat/a.tsx"); code != 404 || strings.Contains(body, "candidates") {
		t.Errorf("只按整段目录名对，不按半截：%d %s", code, body)
	}
	if got := suffixMatches("../a.tsx", dir); got != nil {
		t.Errorf("带 .. 的不猜：%v", got)
	}
}

// Bare names from inline code: present (directly or by suffix) count, identifiers that only look like files don't.
func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"README.md", "ui/src/chat/Composer.tsx", "x/b.go", "y/b.go"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	body, _ := json.Marshal(map[string]any{"cwd": dir, "paths": []string{"README.md", "Composer.tsx", "b.go", "acpx.GUIHint", "Node.js"}})
	w := httptest.NewRecorder()
	(&Server{}).fileExists(w, httptest.NewRequest("POST", "/api/fs/exists", bytes.NewReader(body)))
	var got struct{ Exists map[string]bool }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(w.Body.String())
	}
	want := map[string]bool{"README.md": true, "Composer.tsx": true, "b.go": true}
	if !reflect.DeepEqual(got.Exists, want) {
		t.Errorf("got %v, want %v", got.Exists, want)
	}
}
