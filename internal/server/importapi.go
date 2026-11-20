package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/desktopimport"
	"github.com/gerdr-dev/gerdr/internal/herdrimport"
	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/i18n"
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
					ip.Skip = i18n.S("普通终端（Gerdr 只接 agent 会话）", "Plain terminal (Gerdr only takes agent sessions)")
				case p.Agent != "claude" && p.Agent != "codex":
					ip.Skip = p.Agent + i18n.S(" 还不支持", " isn't supported yet")
				case !agents[p.Agent]:
					ip.Skip = p.Agent + i18n.S(" 在这台机器上不可用", " isn't available on this computer")
				case p.SessionID == "":
					ip.Skip = i18n.S("herdr 没记下它的会话 id（装了 herdr integration 之后重开过的 agent 才有）", "herdr has no session id for it (only agents restarted after installing the herdr integration have one)")
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
	s.desktopImportRoutes(mux)
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
						msg = i18n.S("这个 Codex 会话在 herdr 里还开着，先在 herdr 里退出它再导入", "This Codex session is still open in herdr; quit it there first, then import")
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

// "Import from Claude Desktop": the Desktop App's Code-tab conversations are plain Claude Code sessions
// (internal/desktopimport), so each is taken over with a resume, like the resume list's. They go into the caller's
// project: one in a real project folder into that folder's workspace (as `gerdr session new` places), the ones in the
// Desktop App's scratch folders together into a workspace named "Claude Desktop" (one workspace per scratch folder
// would be a row of meaningless names).

type desktopSession struct {
	desktopimport.Session
	Existing string `json:"existing,omitempty"` // already a Gerdr session (imported before)
	Skip     string `json:"skip,omitempty"`     // why it can't be imported
}

// DesktopWorkspace is the workspace the Desktop App's scratch-folder conversations are put in.
const DesktopWorkspace = "Claude Desktop"

func (s *Server) desktopScan() ([]desktopSession, error) {
	list, err := desktopimport.Scan(desktopimport.Dir(), "")
	if err != nil {
		return nil, err
	}
	out := make([]desktopSession, 0, len(list))
	for _, x := range list {
		d := desktopSession{Session: x}
		switch x.Missing {
		case "transcript":
			d.Skip = i18n.S("找不到它的会话记录（~/.claude/projects 里没有）", "Its transcript isn't there (not in ~/.claude/projects)")
		case "cwd":
			d.Skip = i18n.S("它的文件夹已经不在了：", "Its folder is gone: ") + x.Cwd
		}
		if sess := s.Hub.FindByAgent("claude", x.ID); sess != nil {
			d.Existing = sess.Summary().ID
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *Server) desktopImportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/import/claude-desktop", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.desktopScan()
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"dir": desktopimport.Dir(), "sessions": list})
	})

	mux.HandleFunc("POST /api/import/claude-desktop", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			IDs []string `json:"ids"` // Claude Code session ids (the scan's id)
			Caller
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		list, err := s.desktopScan()
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		byID := map[string]desktopSession{}
		for _, x := range list {
			byID[x.ID] = x
		}
		c := b.Caller
		if c.Session != "" {
			if self, err := s.Hub.Session(c.Session); err == nil {
				c.SessionCwd = self.Summary().Cwd
			}
		}
		project := ResolveSpace(s.Layout.Get(), c, s.workProject())

		type result struct {
			ID      string `json:"id"`                // the Claude Code session id asked for
			Title   string `json:"title,omitempty"`   // its title
			Session string `json:"session,omitempty"` // the Gerdr session it is now
			Reused  bool   `json:"reused,omitempty"`  // was a Gerdr session already
			Error   string `json:"error,omitempty"`
		}
		results := make([]result, len(b.IDs))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		// Taking one over replays its transcript: a few at a time
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for i, id := range b.IDs {
			x, ok := byID[id]
			res := &results[i]
			res.ID, res.Title = id, x.Title
			switch {
			case !ok:
				res.Error = i18n.S("Claude Desktop 里没有这个会话", "No such conversation in Claude Desktop")
				continue
			case x.Existing != "":
				res.Session, res.Reused = x.Existing, true
				continue
			case x.Skip != "":
				res.Error = x.Skip
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				sess, err := s.Hub.Create(ctx, hub.CreateOptions{Agent: "claude", Cwd: x.Cwd, Import: x.ID, Title: x.Title, Project: project})
				if err != nil {
					res.Error = err.Error()
					return
				}
				res.Session = sess.Summary().ID
			}()
		}
		wg.Wait()

		// On screen, in the order asked (the scan's: newest first, each new tab goes first, so put the oldest in first)
		var placeErr string
		for i := len(results) - 1; i >= 0; i-- {
			res := results[i]
			if res.Session == "" || res.Reused {
				continue
			}
			x := byID[res.ID]
			sum, err := s.Hub.Session(res.Session)
			if err != nil {
				continue
			}
			real := sum.Summary().Cwd
			err = s.placeSessionWith(project, res.Session, func(c *layoutCall, space string) (node, error) {
				if !x.Scratch {
					return pickWorkspace(c, space, x.Cwd, real, "")
				}
				for _, w := range c.visibleWs() {
					if str(w, "name") == DesktopWorkspace {
						return w, nil
					}
				}
				w, _ := c.newWorkspace(DesktopWorkspace, "", space)
				return w, nil
			})
			if err != nil {
				placeErr = err.Error()
			}
		}
		out := map[string]any{"results": results}
		if placeErr != "" {
			// The conversations are in Gerdr either way (the sessions list); only putting them in a tab failed
			out["placeError"] = placeErr
		}
		writeJSON(w, out)
	})
}
