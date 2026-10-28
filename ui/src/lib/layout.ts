// 布局模型：工作空间 → tab → pane 树（二叉分割）。纯函数，全部返回新对象。
// 结构和 herdr 一样（workspace / tab / pane），快捷键语义也照着它。

/**
 * sub：子 agent 的只读面板（不占 sessionId —— 那样「定位到会话」「关 pane 停掉这一轮」都会认错）。
 * term：终端。id 是服务端的终端（shell 跑在 gerdr 那边，关页面不断）；cwd 是开它时的目录，gerdr 重启过、
 * 终端没了的时候拿它重开一个。
 */
export type PaneLeaf = {
  type: "pane";
  id: string;
  sessionId?: string;
  draft?: { agent?: string; cwd?: string };
  sub?: { sessionId: string; toolCallId: string; title?: string };
  term?: { id: string; cwd: string };
};
export type PaneSplit = { type: "split"; id: string; dir: "row" | "col"; ratio: number; a: PaneNode; b: PaneNode };
export type PaneNode = PaneLeaf | PaneSplit;

export type Tab = { id: string; name?: string; root: PaneNode; focus: string; zoom?: string };
export type Workspace = { id: string; name: string; cwd?: string; tabs: Tab[]; activeTab: string; lastTab?: string };
export type Layout = { version: 1; workspaces: Workspace[]; active: string; lastActive?: string };

export const rid = (p: string) => `${p}${Math.random().toString(36).slice(2, 9)}`;

export function newPane(draft?: PaneLeaf["draft"], sessionId?: string): PaneLeaf {
  return { type: "pane", id: rid("p"), draft, sessionId };
}

export function newTab(draft?: PaneLeaf["draft"], sessionId?: string, name?: string): Tab {
  const p = newPane(draft, sessionId);
  return { id: rid("t"), root: p, focus: p.id, name };
}

export function newWorkspace(name: string, cwd?: string): Workspace {
  const t = newTab(cwd ? { cwd } : undefined);
  return { id: rid("w"), name, cwd, tabs: [t], activeTab: t.id };
}

export function defaultLayout(): Layout {
  const w = newWorkspace("default");
  return { version: 1, workspaces: [w], active: w.id };
}

export function validLayout(x: any): x is Layout {
  return x && x.version === 1 && Array.isArray(x.workspaces) && x.workspaces.length > 0;
}

// ---- 查找 ----

export function leaves(n: PaneNode): PaneLeaf[] {
  return n.type === "pane" ? [n] : [...leaves(n.a), ...leaves(n.b)];
}

export function findLeaf(n: PaneNode, id: string): PaneLeaf | undefined {
  return leaves(n).find((l) => l.id === id);
}

export function activeWs(l: Layout): Workspace {
  return l.workspaces.find((w) => w.id === l.active) ?? l.workspaces[0];
}

export function activeTab(l: Layout): Tab {
  const w = activeWs(l);
  return w.tabs.find((t) => t.id === w.activeTab) ?? w.tabs[0];
}

export function allLeaves(l: Layout): { ws: Workspace; tab: Tab; pane: PaneLeaf }[] {
  const out: { ws: Workspace; tab: Tab; pane: PaneLeaf }[] = [];
  for (const ws of l.workspaces) for (const tab of ws.tabs) for (const pane of leaves(tab.root)) out.push({ ws, tab, pane });
  return out;
}

/** 某个会话在布局里的位置（第一个）。 */
export function locateSession(l: Layout, sessionId: string) {
  return allLeaves(l).find((x) => x.pane.sessionId === sessionId);
}

// ---- 改树 ----

function mapNode(n: PaneNode, fn: (n: PaneNode) => PaneNode): PaneNode {
  const m = fn(n);
  if (m !== n) return m;
  if (n.type === "split") {
    const a = mapNode(n.a, fn);
    const b = mapNode(n.b, fn);
    if (a !== n.a || b !== n.b) return { ...n, a, b };
  }
  return n;
}

export function updateTab(l: Layout, tabId: string, fn: (t: Tab) => Tab): Layout {
  return {
    ...l,
    workspaces: l.workspaces.map((w) => (w.tabs.some((t) => t.id === tabId) ? { ...w, tabs: w.tabs.map((t) => (t.id === tabId ? fn(t) : t)) } : w)),
  };
}

export function updateWs(l: Layout, wsId: string, fn: (w: Workspace) => Workspace): Layout {
  return { ...l, workspaces: l.workspaces.map((w) => (w.id === wsId ? fn(w) : w)) };
}

export function setPane(l: Layout, paneId: string, patch: Partial<PaneLeaf>): Layout {
  return {
    ...l,
    workspaces: l.workspaces.map((w) => ({
      ...w,
      tabs: w.tabs.map((t) => ({ ...t, root: mapNode(t.root, (n) => (n.type === "pane" && n.id === paneId ? { ...n, ...patch } : n)) })),
    })),
  };
}

export function split(tab: Tab, paneId: string, dir: "row" | "col", fresh: PaneLeaf): Tab {
  const root = mapNode(tab.root, (n) => (n.type === "pane" && n.id === paneId ? { type: "split", id: rid("s"), dir, ratio: 0.5, a: n, b: fresh } : n));
  return { ...tab, root, focus: fresh.id, zoom: undefined };
}

/** 关掉一个 pane；返回 null 表示 tab 空了。 */
export function closePane(tab: Tab, paneId: string): Tab | null {
  if (tab.root.type === "pane") return tab.root.id === paneId ? null : tab;
  const rm = (n: PaneNode): PaneNode | null => {
    if (n.type === "pane") return n.id === paneId ? null : n;
    const a = rm(n.a);
    const b = rm(n.b);
    if (!a) return b;
    if (!b) return a;
    return a === n.a && b === n.b ? n : { ...n, a, b };
  };
  const root = rm(tab.root);
  if (!root) return null;
  const ls = leaves(root);
  const focus = ls.some((x) => x.id === tab.focus) ? tab.focus : ls[0].id;
  return { ...tab, root, focus, zoom: tab.zoom === paneId ? undefined : tab.zoom };
}

export function setRatio(tab: Tab, splitId: string, ratio: number): Tab {
  const r = Math.min(0.9, Math.max(0.1, ratio));
  return { ...tab, root: mapNode(tab.root, (n) => (n.type === "split" && n.id === splitId ? { ...n, ratio: r } : n)) };
}

// ---- 几何（方向键找邻居）----

export type Rect = { x: number; y: number; w: number; h: number };

export function rects(n: PaneNode, r: Rect = { x: 0, y: 0, w: 1, h: 1 }, out = new Map<string, Rect>()): Map<string, Rect> {
  if (n.type === "pane") {
    out.set(n.id, r);
    return out;
  }
  if (n.dir === "row") {
    rects(n.a, { ...r, w: r.w * n.ratio }, out);
    rects(n.b, { ...r, x: r.x + r.w * n.ratio, w: r.w * (1 - n.ratio) }, out);
  } else {
    rects(n.a, { ...r, h: r.h * n.ratio }, out);
    rects(n.b, { ...r, y: r.y + r.h * n.ratio, h: r.h * (1 - n.ratio) }, out);
  }
  return out;
}

export function neighbor(tab: Tab, dir: "left" | "right" | "up" | "down"): string | undefined {
  const rs = rects(tab.root);
  const cur = rs.get(tab.focus);
  if (!cur) return;
  const cx = cur.x + cur.w / 2;
  const cy = cur.y + cur.h / 2;
  let best: string | undefined;
  let bestScore = Infinity;
  const eps = 1e-6;
  for (const [id, r] of rs) {
    if (id === tab.focus) continue;
    let ok = false;
    let dist = 0;
    let overlap = 0;
    switch (dir) {
      case "left":
        ok = r.x + r.w <= cur.x + eps;
        dist = cur.x - (r.x + r.w);
        overlap = Math.min(cur.y + cur.h, r.y + r.h) - Math.max(cur.y, r.y);
        break;
      case "right":
        ok = r.x >= cur.x + cur.w - eps;
        dist = r.x - (cur.x + cur.w);
        overlap = Math.min(cur.y + cur.h, r.y + r.h) - Math.max(cur.y, r.y);
        break;
      case "up":
        ok = r.y + r.h <= cur.y + eps;
        dist = cur.y - (r.y + r.h);
        overlap = Math.min(cur.x + cur.w, r.x + r.w) - Math.max(cur.x, r.x);
        break;
      case "down":
        ok = r.y >= cur.y + cur.h - eps;
        dist = r.y - (cur.y + cur.h);
        overlap = Math.min(cur.x + cur.w, r.x + r.w) - Math.max(cur.x, r.x);
        break;
    }
    if (!ok || overlap <= eps) continue;
    // 先比距离，再比和当前 pane 中心的偏离（和 tmux 的直觉一致）。
    const off = dir === "left" || dir === "right" ? Math.abs(r.y + r.h / 2 - cy) : Math.abs(r.x + r.w / 2 - cx);
    const score = dist * 10 + off;
    if (score < bestScore) {
      bestScore = score;
      best = id;
    }
  }
  return best;
}

/** 和某个方向的邻居交换位置（交换的是 pane 里装的东西，几何不动）。 */
export function swap(tab: Tab, dir: "left" | "right" | "up" | "down"): Tab {
  const other = neighbor(tab, dir);
  if (!other) return tab;
  const a = findLeaf(tab.root, tab.focus)!;
  const b = findLeaf(tab.root, other)!;
  const root = mapNode(tab.root, (n) => {
    if (n.type !== "pane") return n;
    if (n.id === a.id) return { ...b, id: a.id };
    if (n.id === b.id) return { ...a, id: b.id };
    return n;
  });
  return { ...tab, root, focus: b.id };
}

export function cycle(tab: Tab, delta: number): Tab {
  const ls = leaves(tab.root);
  const i = ls.findIndex((x) => x.id === tab.focus);
  const next = ls[(i + delta + ls.length) % ls.length];
  return { ...tab, focus: next.id, zoom: tab.zoom ? next.id : undefined };
}

/**
 * 「看的位置」：在哪个工作空间、每个工作空间看哪个 tab、每个 tab 焦点在哪个 pane / 放大了哪个。
 * 每个端各看各的（主机上看着 A，手机上切到 B，主机不跟着动 —— 用户要的功能），所以收到别的端存的布局时
 * 结构照收、这一份留自己的（withView），自己的那份存在本机浏览器里（store.ts 的 gerdr.view）。
 * 服务端存的布局里也带着这些字段（最后一个存的端的），只给第一次打开、本地什么都没有的端当起点。
 */
export type View = {
  active: string;
  lastActive?: string;
  ws: Record<string, { activeTab: string; lastTab?: string }>;
  tabs: Record<string, { focus: string; zoom?: string }>;
};

export function viewOf(l: Layout): View {
  const v: View = { active: l.active, lastActive: l.lastActive, ws: {}, tabs: {} };
  for (const w of l.workspaces) {
    v.ws[w.id] = { activeTab: w.activeTab, lastTab: w.lastTab };
    for (const t of w.tabs) v.tabs[t.id] = { focus: t.focus, zoom: t.zoom };
  }
  return v;
}

/** 把 v 套到 l 上：v 里说的东西在 l 里还在才用，不在了（被别的端关掉了）就用 l 自己的。没变的部分原样返回同一个对象。 */
export function withView(l: Layout, v: View | null | undefined): Layout {
  if (!v) return l;
  const hasWs = (id?: string) => !!id && l.workspaces.some((w) => w.id === id);
  let changed = false;
  const workspaces = l.workspaces.map((w) => {
    const hasTab = (id?: string) => !!id && w.tabs.some((t) => t.id === id);
    let wsChanged = false;
    const tabs = w.tabs.map((t) => {
      const tv = v.tabs?.[t.id];
      if (!tv) return t;
      const focus = findLeaf(t.root, tv.focus) ? tv.focus : t.focus;
      const zoom = tv.zoom && findLeaf(t.root, tv.zoom) ? tv.zoom : undefined;
      if (focus === t.focus && zoom === t.zoom) return t;
      wsChanged = true;
      return { ...t, focus, zoom };
    });
    const wv = v.ws?.[w.id];
    const activeTab = wv && hasTab(wv.activeTab) ? wv.activeTab : w.activeTab;
    const lastTab = wv ? (hasTab(wv.lastTab) ? wv.lastTab : undefined) : w.lastTab;
    if (!wsChanged && activeTab === w.activeTab && lastTab === w.lastTab) return w;
    changed = true;
    return { ...w, tabs, activeTab, lastTab };
  });
  const active = hasWs(v.active) ? v.active : l.active;
  const lastActive = hasWs(v.lastActive) ? v.lastActive : l.lastActive;
  if (!changed && active === l.active && lastActive === l.lastActive) return l;
  return { ...l, workspaces, active, lastActive };
}
