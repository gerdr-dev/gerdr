// Package config：服务本身的配置只从环境变量来（GERDR_*），界面上的偏好（快捷键、主题、
// 外部 agent…）存在 ~/.gerdr/config.json，由界面里的「设置」改。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Env struct {
	Dir        string        // GERDR_DIR，默认 ~/.gerdr
	Host       string        // GERDR_HOST，默认 127.0.0.1
	Port       int           // GERDR_PORT，默认 7870；0 = 随便挑一个空闲口
	Token      string        // GERDR_TOKEN，不给就用 ~/.gerdr/token（没有就生成）
	ClaudeIdle time.Duration // GERDR_CLAUDE_IDLE_MINUTES，默认 15；0 = 永不回收
	UI         string        // GERDR_UI：界面源码目录。不给就用 <Dir>/ui（第一次启动从内嵌的那份装出来）
	ESMCDN     string        // GERDR_ESM_CDN：第三方包的来源，默认 https://esm.sh（要和它同一套 URL 写法）
}

func FromEnv() Env {
	home, _ := os.UserHomeDir()
	e := Env{Dir: filepath.Join(home, ".gerdr"), Host: "127.0.0.1", Port: 7870, ClaudeIdle: 15 * time.Minute}
	if v := os.Getenv("GERDR_DIR"); v != "" {
		e.Dir = v
	}
	if v := os.Getenv("GERDR_HOST"); v != "" {
		e.Host = v
	}
	if v := os.Getenv("GERDR_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			e.Port = n
		}
	}
	e.Token = os.Getenv("GERDR_TOKEN")
	if v := os.Getenv("GERDR_CLAUDE_IDLE_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n <= 0 {
				e.ClaudeIdle = -1
			} else {
				e.ClaudeIdle = time.Duration(n) * time.Minute
			}
		}
	}
	e.UI = os.Getenv("GERDR_UI")
	if e.UI == "" {
		e.UI = filepath.Join(e.Dir, "ui")
	}
	e.ESMCDN = os.Getenv("GERDR_ESM_CDN")
	return e
}

func truthy(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// LoadToken 读（必要时生成）登录令牌。
func LoadToken(dir string) (string, error) {
	p := filepath.Join(dir, "token")
	if b, err := os.ReadFile(p); err == nil {
		if t := strings.TrimSpace(string(b)); len(t) >= 32 {
			return t, nil
		}
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	t := hex.EncodeToString(buf)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return t, os.WriteFile(p, []byte(t+"\n"), 0o600)
}

// Store 是一个 JSON 文件（config.json / layout.json）：服务端不解释大部分字段，原样存、原样给。
type Store struct {
	path string
	mu   sync.Mutex
	data json.RawMessage
	mod  time.Time
}

func NewStore(path, def string) *Store {
	s := &Store{path: path, data: json.RawMessage(def)}
	s.reload()
	return s
}

func (s *Store) reload() bool {
	st, err := os.Stat(s.path)
	if err != nil {
		return false
	}
	if !st.ModTime().After(s.mod) {
		return false
	}
	b, err := os.ReadFile(s.path)
	if err != nil || !json.Valid(b) {
		return false
	}
	s.data, s.mod = b, st.ModTime()
	return true
}

func (s *Store) Get() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reload()
	return s.data
}

func (s *Store) Set(b json.RawMessage) error {
	if !json.Valid(b) {
		return os.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var pretty any
	_ = json.Unmarshal(b, &pretty)
	out, _ := json.MarshalIndent(pretty, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.data = out
	if st, err := os.Stat(s.path); err == nil {
		s.mod = st.ModTime()
	}
	return nil
}

// Watch：文件被手改了（编辑器里改 config.json）就回调。
func (s *Store) Watch(fn func()) {
	go func() {
		for {
			time.Sleep(1500 * time.Millisecond)
			s.mu.Lock()
			changed := s.reload()
			s.mu.Unlock()
			if changed {
				fn()
			}
		}
	}()
}
