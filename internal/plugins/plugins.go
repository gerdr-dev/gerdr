// Package plugins 管用户的 TS 插件：扫 ~/.gerdr/plugins、用 esbuild（Go 版，不需要 node）
// 编成浏览器能直接跑的 ESM、文件一变就重编并通知前端。
//
// 插件**在前端跑**（它们改的是界面：tab 名、pane 标题、快捷键、命令），所以这里只负责
// 「把 TS 变成一段 JS」。插件能碰到什么由前端注入的那个 API 对象决定（见 gerdr.d.ts）。
package plugins

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/evanw/esbuild/pkg/api"
)

//go:embed builtin/*
var builtin embed.FS

type Plugin struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
	Hash   string `json:"hash"`
	Mtime  int64  `json:"mtime"`
}

type Manager struct {
	dir string
	log *slog.Logger

	mu      sync.Mutex
	cache   map[string]*Plugin // id → 编好的
	sig     string
	onChange func()
}

func New(dir string, log *slog.Logger) *Manager {
	return &Manager{dir: dir, log: log, cache: map[string]*Plugin{}}
}

// Install 第一次运行时把自带的示例插件和类型声明放进插件目录。已经有的不覆盖（人可能改过），
// 只有 gerdr.d.ts 每次都刷新 —— 它描述的是这个版本的 API。
func (m *Manager) Install() error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	marker := filepath.Join(m.dir, ".installed")
	_, err := os.Stat(marker)
	first := os.IsNotExist(err)
	return fs.WalkDir(builtin, "builtin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := strings.TrimPrefix(p, "builtin/")
		dst := filepath.Join(m.dir, name)
		b, _ := builtin.ReadFile(p)
		always := name == "gerdr.d.ts" || name == "tsconfig.json"
		if !always && !first {
			return nil
		}
		if !always {
			if _, err := os.Stat(dst); err == nil {
				return nil
			}
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
		if first {
			_ = os.WriteFile(marker, []byte(time.Now().Format(time.RFC3339)), 0o644)
		}
		return nil
	})
}

// entries 找插件入口：顶层的 *.ts / *.tsx / *.js，或者子目录里的 index.(ts|tsx|js)。
// 以 . 或 _ 开头的、*.d.ts 跳过。
func (m *Manager) entries() map[string]string {
	out := map[string]string{}
	ents, err := os.ReadDir(m.dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "node_modules" {
			continue
		}
		full := filepath.Join(m.dir, name)
		if e.IsDir() {
			for _, idx := range []string{"index.ts", "index.tsx", "index.js"} {
				if _, err := os.Stat(filepath.Join(full, idx)); err == nil {
					out[name] = filepath.Join(full, idx)
					break
				}
			}
			continue
		}
		if strings.HasSuffix(name, ".d.ts") {
			continue
		}
		for _, ext := range []string{".ts", ".tsx", ".js"} {
			if strings.HasSuffix(name, ext) {
				out[strings.TrimSuffix(name, ext)] = full
			}
		}
	}
	return out
}

// signature：所有插件源文件的 mtime + 大小。插件目录不大，每秒扫一遍成本可以忽略。
func (m *Manager) signature() string {
	var parts []string
	_ = filepath.WalkDir(m.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == "node_modules" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			parts = append(parts, fmt.Sprintf("%s:%d:%d", p, info.ModTime().UnixNano(), info.Size()))
		}
		return nil
	})
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h[:8])
}

// List 返回编好的全部插件（必要时重编）。
func (m *Manager) List() []*Plugin {
	m.mu.Lock()
	defer m.mu.Unlock()
	sig := m.signature()
	if sig != m.sig {
		m.rebuild()
		m.sig = sig
	}
	var out []*Plugin
	for _, p := range m.cache {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Manager) rebuild() {
	m.cache = map[string]*Plugin{}
	for id, path := range m.entries() {
		p := &Plugin{ID: id, Path: path}
		if st, err := os.Stat(path); err == nil {
			p.Mtime = st.ModTime().UnixMilli()
		}
		res := api.Build(api.BuildOptions{
			EntryPoints:      []string{path},
			Bundle:           true,
			Write:            false,
			Format:           api.FormatESModule,
			Platform:         api.PlatformBrowser,
			Target:           api.ES2022,
			Sourcemap:        api.SourceMapInline,
			SourceRoot:       "gerdr-plugin://" + id + "/",
			JSX:              api.JSXTransform,
			JSXFactory:       "gerdr.h",
			LogLevel:         api.LogLevelSilent,
			AbsWorkingDir:    m.dir,
			TsconfigRaw:      `{"compilerOptions":{"target":"es2022","useDefineForClassFields":true}}`,
		})
		if len(res.Errors) > 0 {
			var msgs []string
			for _, e := range res.Errors {
				loc := ""
				if e.Location != nil {
					loc = fmt.Sprintf("%s:%d:%d: ", e.Location.File, e.Location.Line, e.Location.Column)
				}
				msgs = append(msgs, loc+e.Text)
			}
			p.Error = strings.Join(msgs, "\n")
		} else if len(res.OutputFiles) > 0 {
			p.Code = string(res.OutputFiles[0].Contents)
		}
		h := sha256.Sum256([]byte(p.Code + p.Error))
		p.Hash = hex.EncodeToString(h[:8])
		m.cache[id] = p
	}
}

// Watch 每秒看一眼插件目录，变了就回调（前端收到通知后重新拉一次插件）。
func (m *Manager) Watch(onChange func()) {
	go func() {
		m.mu.Lock()
		last := m.signature()
		m.mu.Unlock()
		for {
			time.Sleep(time.Second)
			m.mu.Lock()
			sig := m.signature()
			m.mu.Unlock()
			if sig != last {
				last = sig
				m.log.Info("plugins changed")
				onChange()
			}
		}
	}()
}

func (m *Manager) Dir() string { return m.dir }
