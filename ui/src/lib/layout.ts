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
/** space: which session (see Space below) it belongs to; unset = the default one. */
export type Workspace = { id: string; name: string; cwd?: string; tabs: Tab[]; activeTab: string; lastTab?: string; space?: string };
/**
 * Space = herdr's "session": an isolated set of workspaces. The UI shows one at a time (the one the active workspace is in),
 * and agents in different spaces can't see or message each other through `gerdr session …` (cmd/gerdr/session.go,
 * server.SessionSpaces). Called "session" in the UI like herdr; "space" in code because "session" already means a conversation.
 * Workspaces stay in one flat list (everything that walks the whole layout — shutting agents down when no pane shows them,
 * locating a session — keeps working); views filter by space. active：switching back lands on the workspace you left.
 */
export type Space = { id: string; name: string; active?: string };
export type Layout = { version: 1; workspaces: Workspace[]; active: string; lastActive?: string; spaces?: Space[] };

export const DEFAULT_SPACE = "default";

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

// ---- spaces ----

export const spaceOf = (w: Workspace) => w.space || DEFAULT_SPACE;

// A project's id is its name: the address (/<name>) and its Work directory (projects/<name>/work) are named after it, so
// closing a project and creating one with the same name later finds its Work again. Hence no renaming either.
const RESERVED = new Set(["work", "api", "auth", "rescue", "ws", "mcp-oauth", "_ui", "_esm", "projects"]);

/** Why `name` can't be a new project's name ("" = it can). */
export function projectNameError(l: Layout, name: string): string {
  const n = name.trim();
  if (!n) return "起个名字";
  if ([...n].length > 64) return "名字太长了（最多 64 个字）";
  if (/[/\\.\x00-\x1f]/.test(n)) return "名字里不能有 / \\ 和 .（名字会出现在地址和目录名里）";
  if (RESERVED.has(n.toLowerCase())) return `「${n}」是 gerdr 自己用的名字，换一个`;
  // Case-insensitive: on macOS / Windows "abc" and "ABC" would be the same Work directory
  const same = spaceList(l).find((s) => s.id.toLowerCase() === n.toLowerCase());
  if (same) return `已经有叫「${same.id}」的项目了`;
  return "";
}

/** Projects made before ids were names (random ids like s236q0e1) get their name as id; a clash or a bad character gets fixed up. */
export function nameSpaceIds(l: Layout): Layout {
  const spaces = l.spaces ?? [];
  if (spaces.every((s) => s.id === s.name || s.id === DEFAULT_SPACE)) return l;
  const taken = new Set(spaces.filter((s) => s.id === s.name || s.id === DEFAULT_SPACE).map((s) => s.id.toLowerCase()));
  const ids = new Map<string, string>();
  for (const s of spaces) {
    if (s.id === s.name || s.id === DEFAULT_SPACE) continue;
    const base =
      s.name
        .trim()
        .replace(/[/\\.\x00-\x1f]/g, "-")
        .slice(0, 60) || "project";
    let id = RESERVED.has(base.toLowerCase()) ? base + "-1" : base;
    for (let i = 2; taken.has(id.toLowerCase()); i++) id = `${base} ${i}`;
    taken.add(id.toLowerCase());
    ids.set(s.id, id);
  }
  const map = (id?: string) => (id && ids.get(id)) || id;
  return {
    ...l,
    spaces: spaces.map((s) => (ids.has(s.id) ? { ...s, id: ids.get(s.id)!, name: ids.get(s.id)! } : s)),
    workspaces: l.workspaces.map((w) => (w.space && ids.has(w.space) ? { ...w, space: map(w.space) } : w)),
  };
}

/** All spaces, in order: the stored list, plus the default one and any a workspace points at that the list lacks (old / hand-edited layouts). */
export function spaceList(l: Layout): Space[] {
  const out = [...(l.spaces ?? [])];
  const has = (id: string) => out.some((s) => s.id === id);
  if (!has(DEFAULT_SPACE)) out.unshift({ id: DEFAULT_SPACE, name: "default" });
  for (const w of l.workspaces) if (!has(spaceOf(w))) out.push({ id: spaceOf(w), name: spaceOf(w) });
  return out;
}

/** The space being shown: the one the active workspace is in. */
export const currentSpace = (l: Layout) => spaceOf(activeWs(l));

export const spaceWorkspaces = (l: Layout, space = currentSpace(l)) => l.workspaces.filter((w) => spaceOf(w) === space);

// ---- 查找 ----

export function leaves(n: PaneNode): PaneLeaf[] {
  return n.type === "pane" ? [n] : [...leaves(n.a), ...leaves(n.b)];
}

/**
 * 手机上画哪一部分：上下分屏照画，左右分屏只留焦点在的那一边 —— 三四百 px 宽再劈两半，对话和输入框都读不了；
 * 竖着劈两半还各有一整行宽，开着终端看着 agent 跑正合适。只是换个看法，不改布局（布局存服务端、和桌面共用）。
 */
export function phoneRoot(tab: Tab): PaneNode {
  const has = (n: PaneNode): boolean => (n.type === "pane" ? n.id === tab.focus : has(n.a) || has(n.b));
  const walk = (n: PaneNode): PaneNode => {
    if (n.type === "pane") return n;
    if (n.dir === "col") return { ...n, a: walk(n.a), b: walk(n.b) };
    return walk(has(n.b) ? n.b : n.a); // 焦点不在这棵里（坏数据）就取左边
  };
  return walk(tab.root);
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
  // 关的是焦点那个：焦点落到它前一个 pane（排在它左边 / 上面那个，一般就是从它分出来之前的那个），它是第一个才往后。
  // 原来一律回第一个 pane，开了一排分屏、关掉最右边那个时焦点跳到最左边，得再一路切回来（用户报的）。
  let focus = tab.focus;
  if (!ls.some((x) => x.id === focus)) {
    const old = leaves(tab.root);
    const i = old.findIndex((x) => x.id === paneId);
    focus = (old[i - 1] ?? old[i + 1] ?? ls[0]).id;
  }
  return { ...tab, root, focus, zoom: tab.zoom === paneId ? undefined : tab.zoom };
}

/** paneId 那个 pane 的父分割（它是根时没有）。 */
export function parentSplit(n: PaneNode, paneId: string): PaneSplit | undefined {
  if (n.type === "pane") return;
  if (n.a.id === paneId || n.b.id === paneId) return n;
  return parentSplit(n.a, paneId) ?? parentSplit(n.b, paneId);
}

/**
 * 把 leaf 贴回 nodeId 那个节点（pane 或一整个分割）旁边，方向、在哪边、比例照关之前的：撤销关 pane 用，
 * 原来的兄弟还在就能原样摆回去。找不到那个节点返回 null。
 */
export function insertBeside(tab: Tab, nodeId: string, leaf: PaneLeaf, dir: "row" | "col", first: boolean, ratio: number): Tab | null {
  let hit = false;
  const root = mapNode(tab.root, (n) => {
    if (n.id !== nodeId) return n;
    hit = true;
    return { type: "split", id: rid("s"), dir, ratio, a: first ? leaf : n, b: first ? n : leaf };
  });
  return hit ? { ...tab, root, zoom: undefined } : null;
}

export function setRatio(tab: Tab, splitId: string, ratio: number): Tab {
  const r = Math.min(0.9, Math.max(0.1, ratio));
  return { ...tab, root: mapNode(tab.root, (n) => (n.type === "split" && n.id === splitId ? { ...n, ratio: r } : n)) };
}

export type Side = "left" | "right" | "up" | "down";

/**
 * 把一棵子树 node 贴到 paneId 那个 pane 的 side 那边（各占一半）；paneId 不给 = 贴在整个 tab 的那一边。
 * 拖 tab 合并进别的 tab 用：node 是被拖的 tab 的整棵树，原样搬过来，里面的分屏比例都不变。焦点由调用方定。
 */
export function attach(tab: Tab, paneId: string | undefined, side: Side, node: PaneNode): Tab {
  const dir = side === "left" || side === "right" ? "row" : "col";
  const first = side === "left" || side === "up";
  const wrap = (n: PaneNode): PaneNode => ({ type: "split", id: rid("s"), dir, ratio: 0.5, a: first ? node : n, b: first ? n : node });
  const root = paneId ? mapNode(tab.root, (n) => (n.type === "pane" && n.id === paneId ? wrap(n) : n)) : wrap(tab.root);
  return { ...tab, root, zoom: undefined };
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
  l = nameSpaceIds(l); // every layout that comes in passes here (loaded at start / pushed by another device)
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
