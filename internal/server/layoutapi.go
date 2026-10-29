package server

// Layout control for agents: `gerdr workspace|tab|pane …` (cmd/gerdr/layout.go), shaped after herdr's commands of the same
// names so an agent that knows one knows the other. It exists so the user can say "sort my tabs into workspaces" and have an
// agent do it.
//
// The layout is otherwise the UI's: it edits layout.json and PUTs the whole thing (ui/src/lib/layout.ts has the model). These
// operations edit the same JSON here, save it, and broadcast it exactly like a PUT from another window, so every open window
// takes it the way it takes another window's edit (structure replaced, each window keeps its own view). Doing it on the server
// rather than asking a window to do it means it works with no window open and runs once, not once per window. The JSON is
// edited as generic maps so fields this file doesn't know about survive.
//
// Scope, deliberately:
//   - Only the caller's herdr-style session (space): references to anything in another one don't resolve, and nothing moves
//     across. An unknown caller (the user in a terminal) sees every space but still can't move things across.
//   - Nothing here stops an agent or a terminal. close only removes an empty tab / workspace; closing a conversation is
//     `gerdr session close`, which has its own rules.
//   - Focus is left alone: what each window looks at is that window's (layout.ts View), and an agent shouldn't yank it.

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// LayoutSession is what the layout calls need to know about a conversation shown in a pane.
type LayoutSession struct {
	ID, Title, Agent, Cwd, Status string
}

// LayoutParams are the arguments of every layout method; each uses the few it needs. ID is the thing acted on.
type LayoutParams struct {
	ID           string  `json:"id,omitempty"`
	Workspace    string  `json:"workspace,omitempty"`
	Tab          string  `json:"tab,omitempty"`
	TargetPane   string  `json:"target_pane,omitempty"`
	Split        string  `json:"split,omitempty"` // right / down / left / up: which side of the target the moved thing goes
	Ratio        float64 `json:"ratio,omitempty"` // the moved thing's share of the split, default 0.5
	NewTab       bool    `json:"new_tab,omitempty"`
	NewWorkspace bool    `json:"new_workspace,omitempty"`
	Label        string  `json:"label,omitempty"`
	TabLabel     string  `json:"tab_label,omitempty"`
	Cwd          string  `json:"cwd,omitempty"`
	Position     int     `json:"position,omitempty"` // 1-based place among its siblings; 0 = last
}

type node = map[string]any

type layoutCall struct {
	l        node
	space    string // the caller's space; "" = caller not in a pane: all visible, nothing crosses
	self     string
	sessions map[string]LayoutSession
	changed  bool
}

// ApplyLayout runs one layout method against raw (layout.json). out is the new layout, nil when the method only reads.
func ApplyLayout(raw json.RawMessage, sessions []LayoutSession, from, method string, p LayoutParams) (out json.RawMessage, result any, err error) {
	var l node
	if json.Unmarshal(raw, &l) != nil || len(arr(l, "workspaces")) == 0 {
		return nil, nil, errors.New("no layout yet: open gerdr's window once")
	}
	c := &layoutCall{l: l, self: from, sessions: map[string]LayoutSession{}}
	for _, s := range sessions {
		c.sessions[s.ID] = s
	}
	if from != "" {
		c.space = SessionSpaces(raw)[from]
	}
	result, err = c.run(method, p)
	if err != nil || !c.changed {
		return nil, result, err
	}
	c.normalize()
	out, err = json.Marshal(c.l)
	return out, result, err
}

func (c *layoutCall) run(method string, p LayoutParams) (any, error) {
	switch method {
	case "workspace.list":
		out := []node{}
		for i, w := range c.visibleWs() {
			out = append(out, c.wsView(w, i+1))
		}
		return node{"workspaces": out}, nil
	case "workspace.get":
		w, err := c.workspace(p.ID)
		if err != nil {
			return nil, err
		}
		return node{"workspace": c.wsView(w, c.wsNumber(w)), "tabs": c.tabViews(w)}, nil
	case "workspace.create":
		w, t := c.newWorkspace(p.Label, p.Cwd, c.callerSpace())
		c.placeWs(w, p.Position)
		return node{"workspace": c.wsView(w, c.wsNumber(w)), "tab": c.tabView(w, t, 1), "root_pane": c.paneView(w, t, leaves(asNode(t["root"]))[0])}, nil
	case "workspace.rename":
		w, err := c.workspace(p.ID)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.Label) == "" {
			return nil, errors.New("give the new label")
		}
		w["name"] = strings.TrimSpace(p.Label)
		c.changed = true
		return node{"workspace": c.wsView(w, c.wsNumber(w))}, nil
	case "workspace.move":
		w, err := c.workspace(p.ID)
		if err != nil {
			return nil, err
		}
		if p.Position < 1 {
			return nil, errors.New("give --position (1 = first)")
		}
		c.placeWs(w, p.Position)
		return node{"workspace": c.wsView(w, c.wsNumber(w))}, nil
	case "workspace.close":
		w, err := c.workspace(p.ID)
		if err != nil {
			return nil, err
		}
		for _, t := range tabsOf(w) {
			if err := notEmpty(t, "workspace"); err != nil {
				return nil, err
			}
		}
		peers := 0
		for _, x := range c.wss() {
			if spaceOfWs(x) == spaceOfWs(w) {
				peers++
			}
		}
		if peers == 1 {
			return nil, errors.New("it's the last workspace in this session; can't close it")
		}
		c.removeWs(w)
		return node{"closed": str(w, "id")}, nil

	case "tab.list":
		out := []node{}
		for _, w := range c.visibleWs() {
			if p.Workspace != "" {
				if fw, err := c.workspace(p.Workspace); err != nil {
					return nil, err
				} else if fw["id"] != w["id"] {
					continue
				}
			}
			out = append(out, c.tabViews(w)...)
		}
		return node{"tabs": out}, nil
	case "tab.get":
		w, t, err := c.tab(p.ID)
		if err != nil {
			return nil, err
		}
		return node{"tab": c.tabView(w, t, tabNumber(w, t)), "panes": c.paneViews(w, t)}, nil
	case "tab.create":
		w, err := c.workspaceOrCaller(p.Workspace)
		if err != nil {
			return nil, err
		}
		cwd := p.Cwd
		if cwd == "" {
			cwd = str(w, "cwd")
		}
		t := newEmptyTab(cwd)
		if p.Label != "" {
			t["name"] = p.Label
		}
		c.insertTab(w, t, p.Position)
		return node{"tab": c.tabView(w, t, tabNumber(w, t)), "root_pane": c.paneView(w, t, leaves(asNode(t["root"]))[0])}, nil
	case "tab.rename":
		w, t, err := c.tab(p.ID)
		if err != nil {
			return nil, err
		}
		if l := strings.TrimSpace(p.Label); l != "" {
			t["name"] = l
		} else {
			delete(t, "name") // back to the default label (the folder of its first conversation)
		}
		c.changed = true
		return node{"tab": c.tabView(w, t, tabNumber(w, t))}, nil
	case "tab.close":
		w, t, err := c.tab(p.ID)
		if err != nil {
			return nil, err
		}
		if err := notEmpty(t, "tab"); err != nil {
			return nil, err
		}
		c.removeTab(w, t)
		return node{"closed": str(t, "id")}, nil
	case "tab.move":
		return c.moveTab(p)

	case "pane.list":
		out := []node{}
		for _, w := range c.visibleWs() {
			if p.Workspace != "" {
				if fw, err := c.workspace(p.Workspace); err != nil {
					return nil, err
				} else if fw["id"] != w["id"] {
					continue
				}
			}
			for _, t := range tabsOf(w) {
				out = append(out, c.paneViews(w, t)...)
			}
		}
		return node{"panes": out}, nil
	case "pane.get", "pane.current":
		ref := p.ID
		if method == "pane.current" {
			if c.self == "" {
				return nil, errors.New("can't tell which session you are (not an agent running in gerdr?)")
			}
			ref = c.self
		}
		w, t, pn, err := c.pane(ref)
		if err != nil {
			return nil, err
		}
		return node{"pane": c.paneView(w, t, pn)}, nil
	case "pane.layout":
		var w, t node
		var err error
		if p.Tab != "" {
			w, t, err = c.tab(p.Tab)
		} else {
			ref := p.ID
			if ref == "" {
				ref = c.self
			}
			w, t, _, err = c.pane(ref)
		}
		if err != nil {
			return nil, err
		}
		return c.layoutView(w, t), nil
	case "pane.move":
		return c.movePane(p)
	case "pane.swap":
		_, ta, a, err := c.pane(p.ID)
		if err != nil {
			return nil, err
		}
		_, tb, b, err := c.pane(p.TargetPane)
		if err != nil {
			return nil, err
		}
		if a["id"] == b["id"] {
			return nil, errors.New("that's the same pane")
		}
		// Swap the leaves whole (ids travel with what they show), so a pane id keeps naming the same conversation.
		ac, bc := clone(a), clone(b)
		clear(a)
		clear(b)
		for k, v := range bc {
			a[k] = v
		}
		for k, v := range ac {
			b[k] = v
		}
		dropZoom(ta)
		dropZoom(tb)
		c.changed = true
		return node{"ok": true}, nil
	}
	return nil, fmt.Errorf("unknown layout method %q", method)
}

// ---- moving ----

func (c *layoutCall) movePane(p LayoutParams) (any, error) {
	sw, st, pn, err := c.pane(p.ID)
	if err != nil {
		return nil, err
	}
	alone := asNode(st["root"])["id"] == pn["id"]
	// Out into a tab of its own: a pane that's already alone in its tab just takes its tab along (keeps the tab's label).
	asTab := func() node {
		if alone {
			c.removeTab(sw, st)
			return st
		}
		c.detach(st, pn)
		return node{"id": rid("t"), "root": pn, "focus": pn["id"]}
	}
	switch {
	case p.NewWorkspace:
		label := p.Label
		if label == "" {
			label = c.tabLabel(st)
		}
		t := asTab()
		w, _ := c.newWorkspace(label, str(sw, "cwd"), spaceOfWs(sw))
		w["tabs"] = []any{t}
		w["activeTab"] = t["id"]
		c.placeWs(w, p.Position)
		setTabLabel(t, p.TabLabel)
		return c.moved(pn)
	case p.Tab != "" || p.TargetPane != "":
		dw, dt, target, err := c.destination(p)
		if err != nil {
			return nil, err
		}
		if err := c.sameSpace(sw, dw); err != nil {
			return nil, err
		}
		if target != nil && target["id"] == pn["id"] {
			return nil, errors.New("can't split a pane against itself")
		}
		if dt["id"] == st["id"] && alone {
			return nil, errors.New("it's the only pane in that tab already")
		}
		if alone {
			c.removeTab(sw, st)
		} else {
			c.detach(st, pn)
		}
		if err := attach(dt, target, pn, p.Split, p.Ratio); err != nil {
			return nil, err
		}
		c.changed = true
		return c.moved(pn)
	case p.Workspace != "" || p.NewTab || p.Position > 0:
		dw := sw
		if p.Workspace != "" {
			if dw, err = c.workspace(p.Workspace); err != nil {
				return nil, err
			}
		}
		if err := c.sameSpace(sw, dw); err != nil {
			return nil, err
		}
		t := asTab()
		c.insertTab(dw, t, p.Position)
		setTabLabel(t, p.TabLabel)
		return c.moved(pn)
	}
	return nil, errors.New("say where: --workspace / --new-tab / --new-workspace (a tab of its own), or --tab / --target-pane (split in)")
}

func (c *layoutCall) moveTab(p LayoutParams) (any, error) {
	sw, st, err := c.tab(p.ID)
	if err != nil {
		return nil, err
	}
	switch {
	case p.NewWorkspace:
		label := p.Label
		if label == "" {
			label = c.tabLabel(st)
		}
		c.removeTab(sw, st)
		w, _ := c.newWorkspace(label, str(sw, "cwd"), spaceOfWs(sw))
		w["tabs"] = []any{st}
		w["activeTab"] = st["id"]
		c.placeWs(w, p.Position)
	case p.Tab != "" || p.TargetPane != "":
		// Merge: the whole tab, splits and all, goes beside the target (dragging a tab onto another one in the UI).
		dw, dt, target, err := c.destination(p)
		if err != nil {
			return nil, err
		}
		if err := c.sameSpace(sw, dw); err != nil {
			return nil, err
		}
		if dt["id"] == st["id"] {
			return nil, errors.New("can't merge a tab into itself")
		}
		c.removeTab(sw, st)
		if err := attach(dt, target, asNode(st["root"]), p.Split, p.Ratio); err != nil {
			return nil, err
		}
		c.changed = true
		return node{"tab": c.tabView(dw, dt, tabNumber(dw, dt)), "merged": st["id"]}, nil
	case p.Workspace != "" || p.Position > 0:
		dw := sw
		if p.Workspace != "" {
			if dw, err = c.workspace(p.Workspace); err != nil {
				return nil, err
			}
		}
		if err := c.sameSpace(sw, dw); err != nil {
			return nil, err
		}
		c.removeTab(sw, st)
		c.insertTab(dw, st, p.Position)
	default:
		return nil, errors.New("say where: --workspace / --position (move it), --new-workspace, or --tab / --target-pane (merge into)")
	}
	w, t := c.tabOfID(str(st, "id"))
	return node{"tab": c.tabView(w, t, tabNumber(w, t))}, nil
}

// destination: the tab a split-in goes to, and the pane it splits (nil = beside the whole tab).
func (c *layoutCall) destination(p LayoutParams) (w, t, target node, err error) {
	if p.TargetPane != "" {
		w, t, target, err = c.pane(p.TargetPane)
		if err == nil && p.Tab != "" {
			if _, tt, e := c.tab(p.Tab); e != nil {
				err = e
			} else if tt["id"] != t["id"] {
				err = errors.New("--target-pane isn't in --tab")
			}
		}
		return
	}
	w, t, err = c.tab(p.Tab)
	return
}

func (c *layoutCall) moved(pn node) (any, error) {
	w, t, p, err := c.pane(str(pn, "id"))
	if err != nil {
		return nil, err
	}
	return node{"pane": c.paneView(w, t, p)}, nil
}

func (c *layoutCall) sameSpace(a, b node) error {
	if spaceOfWs(a) != spaceOfWs(b) {
		return errors.New("that's in another session; layout changes can't cross sessions")
	}
	return nil
}

// attach puts sub beside target (a pane in t; nil = the whole tab) on side split, sub taking ratio of the space.
func attach(t, target, sub node, split string, ratio float64) error {
	if split == "" {
		split = "right"
	}
	dir := map[string]string{"right": "row", "left": "row", "down": "col", "up": "col"}[split]
	if dir == "" {
		return fmt.Errorf("--split is right, down, left or up, not %q", split)
	}
	if ratio == 0 {
		ratio = 0.5
	}
	ratio = min(0.9, max(0.1, ratio))
	first := split == "left" || split == "up"
	wrap := func(n node) node {
		s := node{"type": "split", "id": rid("s"), "dir": dir, "a": n, "b": sub, "ratio": 1 - ratio}
		if first {
			s["a"], s["b"], s["ratio"] = sub, n, ratio
		}
		return s
	}
	root := asNode(t["root"])
	if target == nil {
		t["root"] = wrap(root)
	} else if r, ok := replaceNode(root, str(target, "id"), wrap); ok {
		t["root"] = r
	} else {
		return errors.New("target pane isn't in that tab")
	}
	dropZoom(t)
	return nil
}

// detach takes pn out of t, which has other panes; its sibling takes the space.
func (c *layoutCall) detach(t, pn node) {
	t["root"] = removeLeaf(asNode(t["root"]), str(pn, "id"))
	dropZoom(t)
	c.changed = true
}

// ---- tree helpers ----

func leaves(n node) []node {
	if n == nil {
		return nil
	}
	if str(n, "type") == "split" {
		return append(leaves(asNode(n["a"])), leaves(asNode(n["b"]))...)
	}
	return []node{n}
}

func removeLeaf(n node, id string) node {
	if n == nil {
		return nil
	}
	if str(n, "type") != "split" {
		if str(n, "id") == id {
			return nil
		}
		return n
	}
	a, b := removeLeaf(asNode(n["a"]), id), removeLeaf(asNode(n["b"]), id)
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	n["a"], n["b"] = a, b
	return n
}

func replaceNode(n node, id string, fn func(node) node) (node, bool) {
	if n == nil {
		return n, false
	}
	if str(n, "id") == id {
		return fn(n), true
	}
	if str(n, "type") == "split" {
		for _, k := range []string{"a", "b"} {
			if r, ok := replaceNode(asNode(n[k]), id, fn); ok {
				n[k] = r
				return n, true
			}
		}
	}
	return n, false
}

func dropZoom(t node) { delete(t, "zoom") }

// ---- workspaces and tabs ----

func (c *layoutCall) wss() []node { return nodes(c.l["workspaces"]) }

func (c *layoutCall) setWss(ws []node) {
	c.l["workspaces"] = anys(ws)
	c.changed = true
}

func (c *layoutCall) visibleWs() []node {
	var out []node
	for _, w := range c.wss() {
		if c.space == "" || spaceOfWs(w) == c.space {
			out = append(out, w)
		}
	}
	return out
}

func (c *layoutCall) callerSpace() string {
	if c.space == "" {
		return DefaultSpace
	}
	return c.space
}

func spaceOfWs(w node) string {
	if s := str(w, "space"); s != "" {
		return s
	}
	return DefaultSpace
}

func tabsOf(w node) []node { return nodes(w["tabs"]) }

func tabNumber(w, t node) int {
	for i, x := range tabsOf(w) {
		if x["id"] == t["id"] {
			return i + 1
		}
	}
	return 0
}

func (c *layoutCall) wsNumber(w node) int {
	for i, x := range c.visibleWs() {
		if x["id"] == w["id"] {
			return i + 1
		}
	}
	return 0
}

func newEmptyTab(cwd string) node {
	p := node{"type": "pane", "id": rid("p")}
	if cwd != "" {
		p["draft"] = node{"cwd": cwd}
	}
	return node{"id": rid("t"), "root": p, "focus": p["id"]}
}

func (c *layoutCall) newWorkspace(label, cwd, space string) (w, t node) {
	if strings.TrimSpace(label) == "" {
		label = fmt.Sprintf("workspace %d", len(c.visibleWs())+1)
	}
	t = newEmptyTab(cwd)
	w = node{"id": rid("w"), "name": strings.TrimSpace(label), "tabs": []any{t}, "activeTab": t["id"]}
	if cwd != "" {
		w["cwd"] = cwd
	}
	if space != DefaultSpace {
		w["space"] = space
	}
	c.setWss(append(c.wss(), w))
	return w, t
}

// placeWs moves w to position pos (1-based) among its session's workspaces; pos < 1 leaves it where it is (new ones are last).
// Other sessions' workspaces keep their places, like moveWorkspace in ui/src/lib/actions.ts.
func (c *layoutCall) placeWs(w node, pos int) {
	if pos < 1 {
		return
	}
	var rest, peers []node
	for _, x := range c.wss() {
		if x["id"] == w["id"] {
			continue
		}
		rest = append(rest, x)
		if spaceOfWs(x) == spaceOfWs(w) {
			peers = append(peers, x)
		}
	}
	idx := func(n node) int {
		for i, x := range rest {
			if x["id"] == n["id"] {
				return i
			}
		}
		return len(rest)
	}
	at := len(rest)
	if pos-1 < len(peers) {
		at = idx(peers[pos-1])
	} else if len(peers) > 0 {
		at = idx(peers[len(peers)-1]) + 1
	}
	out := append(append(append([]node{}, rest[:at]...), w), rest[at:]...)
	c.setWss(out)
}

func (c *layoutCall) removeWs(w node) {
	var out []node
	for _, x := range c.wss() {
		if x["id"] != w["id"] {
			out = append(out, x)
		}
	}
	c.setWss(out)
}

// insertTab puts t into w at pos (1-based; < 1 = last). A workspace holding nothing but one empty tab (a fresh one, or one
// everything was just moved out of) gives that placeholder up, so sorting tabs into new workspaces doesn't leave blanks behind.
func (c *layoutCall) insertTab(w, t node, pos int) {
	tabs := tabsOf(w)
	if len(tabs) == 1 && tabs[0]["id"] != t["id"] && isEmptyTab(tabs[0]) {
		tabs = nil
	}
	at := len(tabs)
	if pos >= 1 && pos-1 < len(tabs) {
		at = pos - 1
	}
	tabs = append(append(append([]node{}, tabs[:at]...), t), tabs[at:]...)
	w["tabs"] = anys(tabs)
	c.changed = true
}

// removeTab takes t out of w; a workspace left with no tabs gets an empty one (like closing its last tab in the UI).
func (c *layoutCall) removeTab(w, t node) {
	var out []node
	for _, x := range tabsOf(w) {
		if x["id"] != t["id"] {
			out = append(out, x)
		}
	}
	if len(out) == 0 {
		out = []node{newEmptyTab(str(w, "cwd"))}
	}
	w["tabs"] = anys(out)
	c.changed = true
}

func (c *layoutCall) tabOfID(id string) (node, node) {
	for _, w := range c.wss() {
		for _, t := range tabsOf(w) {
			if str(t, "id") == id {
				return w, t
			}
		}
	}
	return nil, nil
}

func isEmptyTab(t node) bool { return notEmpty(t, "") == nil }

func isEmptyPane(p node) bool { return p["sessionId"] == nil && p["term"] == nil && p["sub"] == nil }

// notEmpty refuses closing a tab / workspace that still shows something: that would stop agents or kill terminals.
func notEmpty(t node, what string) error {
	for _, p := range leaves(asNode(t["root"])) {
		if !isEmptyPane(p) {
			return fmt.Errorf("the %s isn't empty: move its panes out first (closing a conversation is `gerdr session close`)", what)
		}
	}
	return nil
}

func setTabLabel(t node, label string) {
	if label = strings.TrimSpace(label); label != "" {
		t["name"] = label
	}
}

// normalize repairs the remembered places (active tab, focused pane, zoom…) that a change left pointing at nothing. They're
// only the fallback for a window with no view of its own (layout.ts withView); each window keeps what it was looking at.
func (c *layoutCall) normalize() {
	ws := c.wss()
	has := func(id any) bool {
		for _, w := range ws {
			if w["id"] == id {
				return true
			}
		}
		return false
	}
	for _, w := range ws {
		tabs := tabsOf(w)
		hasTab := func(id any) bool {
			for _, t := range tabs {
				if t["id"] == id {
					return true
				}
			}
			return false
		}
		if !hasTab(w["activeTab"]) {
			w["activeTab"] = tabs[0]["id"]
		}
		if w["lastTab"] != nil && !hasTab(w["lastTab"]) {
			delete(w, "lastTab")
		}
		for _, t := range tabs {
			ls := leaves(asNode(t["root"]))
			hasPane := func(id any) bool {
				for _, p := range ls {
					if p["id"] == id {
						return true
					}
				}
				return false
			}
			if !hasPane(t["focus"]) {
				t["focus"] = ls[0]["id"]
			}
			if t["zoom"] != nil && !hasPane(t["zoom"]) {
				delete(t, "zoom")
			}
		}
	}
	if !has(c.l["active"]) {
		c.l["active"] = ws[0]["id"]
	}
	if c.l["lastActive"] != nil && !has(c.l["lastActive"]) {
		delete(c.l, "lastActive")
	}
}

// ---- finding things ----

// workspace: by id, number (as listed) or label.
func (c *layoutCall) workspace(ref string) (node, error) {
	vis := c.visibleWs()
	for _, w := range vis {
		if str(w, "id") == ref {
			return w, nil
		}
	}
	if n, err := strconv.Atoi(ref); err == nil && n >= 1 && n <= len(vis) {
		return vis[n-1], nil
	}
	var hits []node
	for _, w := range vis {
		if str(w, "name") == ref {
			hits = append(hits, w)
		}
	}
	return one(hits, ref, "workspace")
}

func (c *layoutCall) workspaceOrCaller(ref string) (node, error) {
	if ref != "" {
		return c.workspace(ref)
	}
	if w, _, _, err := c.pane(c.self); c.self != "" && err == nil {
		return w, nil
	}
	return c.visibleWs()[0], nil
}

// tab: by id or label.
func (c *layoutCall) tab(ref string) (w, t node, err error) {
	if ref == "" {
		return nil, nil, errors.New("which tab? (gerdr tab list)")
	}
	type hit struct{ w, t node }
	var hits []hit
	for _, w := range c.visibleWs() {
		for _, t := range tabsOf(w) {
			if str(t, "id") == ref {
				return w, t, nil
			}
			if c.tabLabel(t) == ref {
				hits = append(hits, hit{w, t})
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0].w, hits[0].t, nil
	case 0:
		return nil, nil, fmt.Errorf("no tab %q (gerdr tab list)", ref)
	}
	return nil, nil, fmt.Errorf("%q names several tabs: use the tab_id", ref)
}

// pane: by pane id, or by the conversation it shows (session id, an id prefix of 4+ characters, or its title).
func (c *layoutCall) pane(ref string) (w, t, p node, err error) {
	if ref == "" {
		return nil, nil, nil, errors.New("which pane? (gerdr pane list)")
	}
	type hit struct{ w, t, p node }
	var bySession []hit
	seen := map[string]bool{}
	for _, w := range c.visibleWs() {
		for _, t := range tabsOf(w) {
			for _, p := range leaves(asNode(t["root"])) {
				if str(p, "id") == ref {
					return w, t, p, nil
				}
				sid := str(p, "sessionId")
				if sid == "" || seen[sid] { // shown twice: the first pane, like the UI's locateSession
					continue
				}
				s := c.sessions[sid]
				if sid == ref || (len(ref) >= 4 && strings.HasPrefix(sid, ref)) || (s.Title != "" && s.Title == ref) {
					seen[sid] = true
					bySession = append(bySession, hit{w, t, p})
				}
			}
		}
	}
	switch len(bySession) {
	case 1:
		h := bySession[0]
		return h.w, h.t, h.p, nil
	case 0:
		return nil, nil, nil, fmt.Errorf("no pane %q (gerdr pane list)", ref)
	}
	return nil, nil, nil, fmt.Errorf("%q matches several conversations: use the pane_id", ref)
}

func one(hits []node, ref, what string) (node, error) {
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return nil, fmt.Errorf("no %s %q (gerdr %s list)", what, ref, what)
	}
	return nil, fmt.Errorf("%q names several %ss: use the %s_id", ref, what, what)
}

// ---- views (field names follow herdr's) ----

// tabLabel is what the tab bar shows: its own name, else the folder of the first thing in it (ui/src/lib/actions.ts tabName).
func (c *layoutCall) tabLabel(t node) string {
	if n := str(t, "name"); n != "" {
		return n
	}
	for _, p := range leaves(asNode(t["root"])) {
		if s, ok := c.sessions[str(p, "sessionId")]; ok && s.Cwd != "" {
			return filepath.Base(s.Cwd)
		}
		if term := asNode(p["term"]); term != nil && str(term, "cwd") != "" {
			return filepath.Base(str(term, "cwd"))
		}
		if d := asNode(p["draft"]); d != nil && str(d, "cwd") != "" {
			return filepath.Base(str(d, "cwd"))
		}
	}
	return "new"
}

func (c *layoutCall) wsView(w node, number int) node {
	panes := 0
	for _, t := range tabsOf(w) {
		panes += len(leaves(asNode(t["root"])))
	}
	v := node{"workspace_id": w["id"], "label": w["name"], "number": number, "tab_count": len(tabsOf(w)), "pane_count": panes}
	if cwd := str(w, "cwd"); cwd != "" {
		v["cwd"] = cwd
	}
	if c.space == "" {
		v["session"] = spaceOfWs(w)
	}
	return v
}

func (c *layoutCall) tabViews(w node) []node {
	out := []node{}
	for i, t := range tabsOf(w) {
		out = append(out, c.tabView(w, t, i+1))
	}
	return out
}

func (c *layoutCall) tabView(w, t node, number int) node {
	ls := leaves(asNode(t["root"]))
	v := node{"tab_id": t["id"], "workspace_id": w["id"], "label": c.tabLabel(t), "number": number, "pane_count": len(ls)}
	if len(ls) > 1 {
		v["split"] = splitExpr(asNode(t["root"]))
	}
	return v
}

func (c *layoutCall) paneViews(w, t node) []node {
	out := []node{}
	for _, p := range leaves(asNode(t["root"])) {
		out = append(out, c.paneView(w, t, p))
	}
	return out
}

func (c *layoutCall) paneView(w, t, p node) node {
	v := node{"pane_id": p["id"], "tab_id": t["id"], "workspace_id": w["id"]}
	switch {
	case str(p, "sessionId") != "":
		sid := str(p, "sessionId")
		v["kind"] = "agent"
		sv := node{"id": sid}
		if s, ok := c.sessions[sid]; ok {
			sv["title"], sv["agent"], sv["status"], sv["cwd"] = s.Title, s.Agent, s.Status, s.Cwd
		}
		v["session"] = sv
		if sid == c.self {
			v["self"] = true
		}
	case p["term"] != nil:
		v["kind"] = "terminal"
		v["cwd"] = asNode(p["term"])["cwd"]
	case p["sub"] != nil:
		v["kind"] = "subagent"
		v["parent_session"] = asNode(p["sub"])["sessionId"]
	default:
		v["kind"] = "empty"
	}
	return v
}

// splitExpr: row(a, b) = a left of b, col(a, b) = a above b.
func splitExpr(n node) string {
	if str(n, "type") != "split" {
		return str(n, "id")
	}
	return fmt.Sprintf("%s(%s, %s)", str(n, "dir"), splitExpr(asNode(n["a"])), splitExpr(asNode(n["b"])))
}

func (c *layoutCall) layoutView(w, t node) node {
	type rect struct{ x, y, w, h float64 }
	panes := []node{}
	splits := []node{}
	round := func(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }
	var walk func(n node, r rect)
	walk = func(n node, r rect) {
		if str(n, "type") != "split" {
			panes = append(panes, node{"pane_id": n["id"], "rect": node{"x": round(r.x), "y": round(r.y), "width": round(r.w), "height": round(r.h)}})
			return
		}
		ratio, _ := n["ratio"].(float64)
		a, b := r, r
		dir := "right"
		if str(n, "dir") == "row" {
			a.w, b.x, b.w = r.w*ratio, r.x+r.w*ratio, r.w*(1-ratio)
		} else {
			dir = "down"
			a.h, b.y, b.h = r.h*ratio, r.y+r.h*ratio, r.h*(1-ratio)
		}
		ids := func(x node) []any {
			var out []any
			for _, l := range leaves(x) {
				out = append(out, l["id"])
			}
			return out
		}
		splits = append(splits, node{"split_id": n["id"], "direction": dir, "ratio": round(ratio), "first": ids(asNode(n["a"])), "second": ids(asNode(n["b"]))})
		walk(asNode(n["a"]), a)
		walk(asNode(n["b"]), b)
	}
	walk(asNode(t["root"]), rect{0, 0, 1, 1})
	return node{"workspace_id": w["id"], "tab_id": t["id"], "split": splitExpr(asNode(t["root"])), "panes": panes, "splits": splits}
}

// ---- JSON plumbing ----

func asNode(v any) node { m, _ := v.(map[string]any); return m }

func str(m node, k string) string { s, _ := m[k].(string); return s }

func arr(m node, k string) []any { a, _ := m[k].([]any); return a }

func nodes(v any) []node {
	a, _ := v.([]any)
	out := make([]node, 0, len(a))
	for _, x := range a {
		if m := asNode(x); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func anys(ns []node) []any {
	out := make([]any, len(ns))
	for i, n := range ns {
		out[i] = n
	}
	return out
}

func clone(m node) node {
	out := make(node, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// rid makes an id the way the UI does (layout.ts rid): a prefix and 7 base-36 characters.
func rid(prefix string) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 7)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(36))
		b[i] = digits[n.Int64()]
	}
	return prefix + string(b)
}

// ---- HTTP ----

var layoutMu sync.Mutex // one layout change at a time (read, edit, save)

func (s *Server) layoutOp(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Method string       `json:"method"`
		From   string       `json:"from"` // the calling agent's gerdr session: scopes everything to its herdr-style session
		Params LayoutParams `json:"params"`
	}
	if err := decode(r, &b); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	layoutMu.Lock()
	defer layoutMu.Unlock()
	var sessions []LayoutSession
	for _, x := range s.Hub.List() {
		sessions = append(sessions, LayoutSession{ID: x.ID, Title: x.Title, Agent: x.Agent, Cwd: x.Cwd, Status: x.Status})
	}
	out, res, err := ApplyLayout(s.Layout.Get(), sessions, b.From, b.Method, b.Params)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if out != nil {
		if err := s.Layout.Set(out); err != nil {
			httpError(w, 500, err.Error())
			return
		}
		// Same message a PUT from a window sends: every window takes the new structure and keeps its own view.
		s.Hub.Broadcast("layout", map[string]any{"data": out, "client": "gerdr-cli"})
	}
	writeJSON(w, res)
}
