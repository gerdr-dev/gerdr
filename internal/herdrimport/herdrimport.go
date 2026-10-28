// Package herdrimport 读出本机 herdr 的布局（工作空间 / tab / 分屏）和每个 pane 里 agent 的会话，
// 给 gerdr 的「从 herdr 导入」用。
//
// 数据全部来自 herdr 自己的命令行（JSON）：workspace list、tab list、pane list，多 pane 的 tab 再问一次 pane layout。
// 会话 id 是 pane list 里的 agent_session —— `herdr integration install claude|codex` 装的 hook 报的，
// 没装 hook 的 agent pane 没有它，那种 pane 导不了对话（列出来标明原因）。
// 只读：不动 herdr 里的任何东西。
package herdrimport

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Pane struct {
	ID        string `json:"id"`
	Agent     string `json:"agent,omitempty"`     // claude / codex；空 = 普通终端
	SessionID string `json:"sessionId,omitempty"` // agent 自己的会话 id（没装 herdr 的 hook 就没有）
	Cwd       string `json:"cwd"`
	Title     string `json:"title,omitempty"`
	Status    string `json:"status,omitempty"`
}

// Node 是一个 tab 里的分屏树：叶子是 pane，内部节点是一刀切成两半（dir: row = 左右、col = 上下）。
type Node struct {
	Pane  string  `json:"pane,omitempty"`
	Dir   string  `json:"dir,omitempty"`
	Ratio float64 `json:"ratio,omitempty"`
	A     *Node   `json:"a,omitempty"`
	B     *Node   `json:"b,omitempty"`
}

type Tab struct {
	ID    string  `json:"id"`
	Label string  `json:"label"` // herdr 里显示的名字（去掉了编号插件加的「1.」「 Ⅱ」）
	Panes []*Pane `json:"panes"`
	Tree  *Node   `json:"tree"`
}

type Workspace struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Tabs  []*Tab `json:"tabs"`
}

type Snapshot struct {
	Workspaces []*Workspace `json:"workspaces"`
}

// herdrBin：herdr 可执行文件。从 Finder 起的 App 的 PATH 只有系统目录，调用前 sysenv.AdoptLoginPath 已经补过。
func herdrBin() (string, error) {
	if p, err := exec.LookPath("herdr"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".local/bin/herdr"), "/opt/homebrew/bin/herdr", "/usr/local/bin/herdr"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("找不到 herdr（没装，或者不在 PATH 里）")
}

func run(ctx context.Context, bin string, out any, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	// 别带着 HERDR_*：gerdr 自己要是从某个 herdr pane 里起的，带着它们会指到那个 pane 的会话上（或者被当成嵌套而拒绝）。
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	b, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return fmt.Errorf("herdr %s：%s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return fmt.Errorf("herdr %s：%w", strings.Join(args, " "), err)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("herdr %s 的输出看不懂：%w", strings.Join(args, " "), err)
	}
	return json.Unmarshal(env.Result, out)
}

var (
	numPrefixRE   = regexp.MustCompile(`^\d+\.`)
	romanSuffixRE = regexp.MustCompile(`\s+[ⅡⅢⅣⅤⅥⅦⅧⅨⅩⅪⅫ]+$`)
)

// cleanLabel：去掉 tab 编号插件加的「1.」前缀和同名序号「 Ⅱ」（gerdr 自己的编号插件会再加一遍）。
// 只剩数字的（herdr 没起名时的默认名「1」）当没有名字，让 gerdr 按会话标题显示。
func cleanLabel(s string) string {
	s = strings.TrimSpace(romanSuffixRE.ReplaceAllString(numPrefixRE.ReplaceAllString(s, ""), ""))
	if strings.Trim(s, "0123456789") == "" {
		return ""
	}
	return s
}

func Scan(ctx context.Context) (*Snapshot, error) {
	bin, err := herdrBin()
	if err != nil {
		return nil, err
	}
	var ws struct {
		Workspaces []struct {
			ID    string `json:"workspace_id"`
			Label string `json:"label"`
			Num   int    `json:"number"`
		} `json:"workspaces"`
	}
	if err := run(ctx, bin, &ws, "workspace", "list"); err != nil {
		return nil, err
	}
	var tabs struct {
		Tabs []struct {
			ID        string `json:"tab_id"`
			Workspace string `json:"workspace_id"`
			Label     string `json:"label"`
			PaneCount int    `json:"pane_count"`
		} `json:"tabs"`
	}
	if err := run(ctx, bin, &tabs, "tab", "list"); err != nil {
		return nil, err
	}
	var panes struct {
		Panes []struct {
			ID      string `json:"pane_id"`
			Tab     string `json:"tab_id"`
			Agent   string `json:"agent"`
			Session *struct {
				Agent string `json:"agent"`
				Kind  string `json:"kind"`
				Value string `json:"value"`
			} `json:"agent_session"`
			Cwd    string `json:"cwd"`
			Title  string `json:"title"`
			Status string `json:"agent_status"`
		} `json:"panes"`
	}
	if err := run(ctx, bin, &panes, "pane", "list"); err != nil {
		return nil, err
	}

	snap := &Snapshot{}
	wsByID := map[string]*Workspace{}
	sort.SliceStable(ws.Workspaces, func(i, j int) bool { return ws.Workspaces[i].Num < ws.Workspaces[j].Num })
	for _, w := range ws.Workspaces {
		x := &Workspace{ID: w.ID, Label: w.Label}
		snap.Workspaces = append(snap.Workspaces, x)
		wsByID[w.ID] = x
	}
	tabByID := map[string]*Tab{}
	multi := map[string]bool{}
	for _, t := range tabs.Tabs {
		w := wsByID[t.Workspace]
		if w == nil {
			continue
		}
		x := &Tab{ID: t.ID, Label: cleanLabel(t.Label), Panes: []*Pane{}}
		w.Tabs = append(w.Tabs, x)
		tabByID[t.ID] = x
		if t.PaneCount > 1 {
			multi[t.ID] = true
		}
	}
	for _, p := range panes.Panes {
		t := tabByID[p.Tab]
		if t == nil {
			continue
		}
		x := &Pane{ID: p.ID, Agent: p.Agent, Cwd: p.Cwd, Title: p.Title, Status: p.Status}
		// kind 实测是 id（不是 path）；只认 id，path 那种对不上 resume 要的东西。
		if p.Session != nil && p.Session.Kind == "id" && p.Session.Value != "" {
			x.SessionID = p.Session.Value
			if x.Agent == "" {
				x.Agent = p.Session.Agent
			}
		}
		t.Panes = append(t.Panes, x)
	}
	for _, w := range snap.Workspaces {
		for _, t := range w.Tabs {
			if len(t.Panes) == 1 {
				t.Tree = &Node{Pane: t.Panes[0].ID}
				continue
			}
			if multi[t.ID] && len(t.Panes) > 0 {
				t.Tree = layoutTree(ctx, bin, t)
			}
		}
	}
	return snap, nil
}

type rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"width"`
	H int `json:"height"`
}

// layoutTree：herdr 的 pane layout 给的是每个 pane 的矩形，按「一刀能把它们分成两边」递归切回一棵二叉树。
// 问不到布局（老版本 herdr）就退成从左到右排开。
func layoutTree(ctx context.Context, bin string, t *Tab) *Node {
	var lay struct {
		Layout struct {
			Panes []struct {
				ID   string `json:"pane_id"`
				Rect rect   `json:"rect"`
			} `json:"panes"`
		} `json:"layout"`
	}
	rects := map[string]rect{}
	if err := run(ctx, bin, &lay, "pane", "layout", "--pane", t.Panes[0].ID); err == nil {
		for _, p := range lay.Layout.Panes {
			rects[p.ID] = p.Rect
		}
	}
	var ids []string
	for _, p := range t.Panes {
		if _, ok := rects[p.ID]; ok {
			ids = append(ids, p.ID)
		}
	}
	if len(ids) != len(t.Panes) {
		ids = nil
		for _, p := range t.Panes {
			ids = append(ids, p.ID)
		}
		return chain(ids)
	}
	if n := cut(ids, rects); n != nil {
		return n
	}
	return chain(ids)
}

func chain(ids []string) *Node {
	if len(ids) == 1 {
		return &Node{Pane: ids[0]}
	}
	return &Node{Dir: "row", Ratio: 1 / float64(len(ids)), A: &Node{Pane: ids[0]}, B: chain(ids[1:])}
}

func cut(ids []string, r map[string]rect) *Node {
	if len(ids) == 1 {
		return &Node{Pane: ids[0]}
	}
	minX, minY, maxX, maxY := 1<<30, 1<<30, 0, 0
	for _, id := range ids {
		x := r[id]
		minX, minY = min(minX, x.X), min(minY, x.Y)
		maxX, maxY = max(maxX, x.X+x.W), max(maxY, x.Y+x.H)
	}
	try := func(vertical bool) *Node {
		// 候选切线：每个 pane 的右边 / 下边
		for _, id := range ids {
			var line int
			if vertical {
				line = r[id].X + r[id].W
				if line >= maxX {
					continue
				}
			} else {
				line = r[id].Y + r[id].H
				if line >= maxY {
					continue
				}
			}
			var a, b []string
			ok := true
			for _, j := range ids {
				x := r[j]
				lo, hi := x.Y, x.Y+x.H
				if vertical {
					lo, hi = x.X, x.X+x.W
				}
				// 分隔线本身可能占一格：容 1
				switch {
				case hi <= line+1:
					a = append(a, j)
				case lo >= line-1:
					b = append(b, j)
				default:
					ok = false
				}
			}
			if !ok || len(a) == 0 || len(b) == 0 {
				continue
			}
			na, nb := cut(a, r), cut(b, r)
			if na == nil || nb == nil {
				continue
			}
			total := maxY - minY
			dir := "col"
			if vertical {
				total, dir = maxX-minX, "row"
			}
			ratio := 0.5
			if total > 0 {
				start := minY
				if vertical {
					start = minX
				}
				ratio = float64(line-start) / float64(total)
			}
			return &Node{Dir: dir, Ratio: ratio, A: na, B: nb}
		}
		return nil
	}
	if n := try(true); n != nil {
		return n
	}
	return try(false)
}
