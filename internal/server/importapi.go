package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/herdrimport"
	"github.com/gerdr-dev/gerdr/internal/hub"
)

// 「从 herdr 导入」：herdr 的工作空间 / tab / 分屏原样搬过来，每个 agent pane 接上它的会话（resume，历史全在）。
// 布局是前端的（layout.json 服务端不解释），所以这里只负责把会话建好，返回「哪个位置放哪个会话」，前端拼布局。

type importPane struct {
	*herdrimport.Pane
	Existing string `json:"existing,omitempty"` // 已经在 gerdr 里的会话（之前导入过 / 在 gerdr 里开的）
	Skip     string `json:"skip,omitempty"`     // 导不了的原因
}

func (s *Server) herdrScan(ctx context.Context) (*herdrimport.Snapshot, map[string]*importPane, error) {
	snap, err := herdrimport.Scan(ctx)
	if err != nil {
		return nil, nil, err
	}
	agents := map[string]bool{}
	for _, a := range s.Hub.Agents() {
		if ok, _ := a["available"].(bool); ok {
			id, _ := a["id"].(string)
			agents[id] = true
		}
	}
	info := map[string]*importPane{}
	for _, w := range snap.Workspaces {
		for _, t := range w.Tabs {
			for _, p := range t.Panes {
				ip := &importPane{Pane: p}
				switch {
				case p.Agent == "":
					ip.Skip = "普通终端（gerdr 只接 agent 会话）"
				case p.Agent != "claude" && p.Agent != "codex":
					ip.Skip = p.Agent + " 还不支持"
				case !agents[p.Agent]:
					ip.Skip = p.Agent + " 在这台机器上不可用"
				case p.SessionID == "":
					ip.Skip = "herdr 没记下它的会话 id（装了 herdr integration 之后重开过的 agent 才有）"
				default:
					if sess := s.Hub.FindByAgent(p.Agent, p.SessionID); sess != nil {
						ip.Existing = sess.Summary().ID
					}
				}
				info[p.ID] = ip
			}
		}
	}
	return snap, info, nil
}

func (s *Server) importRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/import/herdr", func(w http.ResponseWriter, r *http.Request) {
		snap, info, err := s.herdrScan(r.Context())
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		type tab struct {
			Label string            `json:"label"`
			Panes []*importPane     `json:"panes"`
			Tree  *herdrimport.Node `json:"tree"`
		}
		type ws struct {
			ID    string `json:"id"`
			Label string `json:"label"`
			Tabs  []tab  `json:"tabs"`
		}
		var out []ws
		for _, x := range snap.Workspaces {
			o := ws{ID: x.ID, Label: x.Label, Tabs: []tab{}}
			for _, t := range x.Tabs {
				tt := tab{Label: t.Label, Tree: t.Tree, Panes: []*importPane{}}
				for _, p := range t.Panes {
					tt.Panes = append(tt.Panes, info[p.ID])
				}
				o.Tabs = append(o.Tabs, tt)
			}
			out = append(out, o)
		}
		writeJSON(w, map[string]any{"workspaces": out})
	})

	mux.HandleFunc("POST /api/import/herdr", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Workspaces []string `json:"workspaces"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		want := map[string]bool{}
		for _, id := range b.Workspaces {
			want[id] = true
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		snap, info, err := s.herdrScan(ctx)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		// 并发接会话（claude 的是读会话记录重放，codex 是 thread/resume）；同一个会话出现在两个 pane 里只接一次。
		var mu sync.Mutex
		got := map[string]string{} // herdr pane → gerdr 会话
		var errs []string
		imported, reused := 0, 0
		byKey := map[string][]string{}
		for _, x := range snap.Workspaces {
			if !want[x.ID] {
				continue
			}
			for _, t := range x.Tabs {
				for _, p := range t.Panes {
					ip := info[p.ID]
					if ip.Skip != "" {
						continue
					}
					if ip.Existing != "" {
						got[p.ID] = ip.Existing
						reused++
						continue
					}
					k := p.Agent + ":" + p.SessionID
					byKey[k] = append(byKey[k], p.ID)
				}
			}
		}
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, ids := range byKey {
			p := info[ids[0]]
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				sess, err := s.Hub.Create(ctx, hub.CreateOptions{Agent: p.Agent, Cwd: p.Cwd, Import: p.SessionID, Title: p.Title})
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					msg := err.Error()
					// codex 一个 thread 同一时间只许一个进程写：herdr 里那个 codex 还开着就接不了。
					if strings.Contains(msg, "active writer") {
						msg = "这个 Codex 会话在 herdr 里还开着，先在 herdr 里退出它再导入"
					}
					errs = append(errs, fmt.Sprintf("「%s」：%s", p.Title, msg))
					return
				}
				imported++
				for _, id := range ids {
					got[id] = sess.Summary().ID
				}
			}()
		}
		wg.Wait()

		// 布局：把树里的 pane 换成 gerdr 会话 id，没接上的叶子删掉（另一半顶上来）。
		var prune func(n *herdrimport.Node) map[string]any
		prune = func(n *herdrimport.Node) map[string]any {
			if n == nil {
				return nil
			}
			if n.Pane != "" {
				if id := got[n.Pane]; id != "" {
					return map[string]any{"sessionId": id, "cwd": info[n.Pane].Cwd}
				}
				return nil
			}
			a, b := prune(n.A), prune(n.B)
			if a == nil {
				return b
			}
			if b == nil {
				return a
			}
			return map[string]any{"dir": n.Dir, "ratio": n.Ratio, "a": a, "b": b}
		}
		var out []map[string]any
		for _, x := range snap.Workspaces {
			if !want[x.ID] {
				continue
			}
			var tabs []map[string]any
			for _, t := range x.Tabs {
				if tree := prune(t.Tree); tree != nil {
					tabs = append(tabs, map[string]any{"label": t.Label, "tree": tree})
				}
			}
			if len(tabs) > 0 {
				out = append(out, map[string]any{"label": x.Label, "tabs": tabs})
			}
		}
		writeJSON(w, map[string]any{"workspaces": out, "imported": imported, "reused": reused, "errors": errs})
	})
}
