package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/artifacts"
)

func TestArtifactsAPIByDir(t *testing.T) {
	root := t.TempDir()
	s := &Server{Artifacts: artifacts.New(func() string { return root })}
	mux := http.NewServeMux()
	s.artifactsRoutes(mux)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(b)))
		return w
	}
	dir := filepath.Join(root, "p", "x")

	w := call("GET", "/api/artifacts?dir="+url.QueryEscape(dir), nil)
	var info struct {
		Exists bool             `json:"exists"`
		Items  []artifacts.Item `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &info)
	if w.Code != 200 || info.Exists {
		t.Fatalf("before anything: %d %s", w.Code, w.Body)
	}

	// The first record creates the folder
	w = call("POST", "/api/artifacts", map[string]any{"dir": dir, "kind": "link", "url": "https://example.com/pr/1", "label": "PR"})
	if w.Code != 200 {
		t.Fatalf("record: %d %s", w.Code, w.Body)
	}
	if _, err := artifacts.Read(dir); err != nil {
		t.Fatal("no identity after the first record:", err)
	}
	if w = call("POST", "/api/artifacts", map[string]any{"dir": dir, "kind": "file", "path": "missing.png"}); w.Code != 400 {
		t.Fatalf("pinning a missing file: %d", w.Code)
	}
	os.WriteFile(filepath.Join(dir, "a.png"), []byte("x"), 0o644)
	if w = call("POST", "/api/artifacts", map[string]any{"dir": dir, "kind": "file", "path": filepath.Join(dir, "a.png")}); w.Code != 200 {
		t.Fatalf("pin by absolute path: %d %s", w.Code, w.Body)
	}
	w = call("GET", "/api/artifacts?dir="+url.QueryEscape(dir), nil)
	_ = json.Unmarshal(w.Body.Bytes(), &info)
	if !info.Exists || len(info.Items) != 2 || info.Items[1].Path != "a.png" {
		t.Fatalf("after: %s", w.Body)
	}

	// list hides .gerdr; move refuses to overwrite; trash removes
	w = call("GET", "/api/fs/list?path="+url.QueryEscape(dir), nil)
	var l struct {
		Entries []struct {
			Name string `json:"name"`
			Mime string `json:"mime"`
		} `json:"entries"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &l)
	if len(l.Entries) != 2 { // .gerdrignore, a.png
		t.Fatalf("list: %s", w.Body)
	}
	os.WriteFile(filepath.Join(dir, "b.png"), []byte("y"), 0o644)
	if w = call("POST", "/api/fs/move", map[string]string{"from": filepath.Join(dir, "b.png"), "to": filepath.Join(dir, "a.png")}); w.Code != 409 {
		t.Fatalf("move over a file: %d", w.Code)
	}
	if w = call("POST", "/api/fs/move", map[string]string{"from": filepath.Join(dir, "b.png"), "to": filepath.Join(dir, "sub", "c.png")}); w.Code != 200 {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	if w = call("POST", "/api/fs/trash", map[string]string{"path": root}); w.Code != 400 {
		t.Fatalf("trashing the root: %d", w.Code)
	}
}

func TestSpaceName(t *testing.T) {
	raw := json.RawMessage(`{"spaces":[{"id":"default","name":"my"},{"id":"lb"}]}`)
	if SpaceName(raw, "default") != "my" || SpaceName(raw, "lb") != "lb" || SpaceName(raw, "x") != "x" {
		t.Fatal("SpaceName")
	}
}
