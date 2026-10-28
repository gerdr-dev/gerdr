import { create } from "zustand";
import { api, clientId, type FileStat } from "./api";
import { DEFAULT_KEYS, type KeyAction } from "./keys";
import { activeTab, defaultLayout, leaves, validLayout, type Layout } from "./layout";
import { isPhone } from "./phone";
import { Transcript } from "./transcript";
import type { AgentInfo, HubEvent, SessionSummary } from "./types";

export type Theme = "system" | "light" | "dark";

export type Config = {
  prefix: string;
  keys: Partial<Record<KeyAction, string[]>>;
  theme: Theme;
  fontSize: number;
  defaultAgent: string;
  /** 新会话的默认模式（按 agent）。 */
  defaultModes: Record<string, string>;
  notifications: boolean;
  /** 发送：enter（回车发、Shift+回车换行）或 mod+enter（⌘/Ctrl+回车发）。 */
  sendKey: "enter" | "mod+enter";
  keepTabs: number; // 切走之后还留在页面里的 tab 数（展开状态、滚动位置都保住）；1 = 不留
  /** 实验室：对话用虚拟滚动（只渲染屏幕附近的行）。默认关 —— 往上翻时行卸载再挂载，带图的行高度先 0 后撑开，界面一跳一跳（用户报的）。 */
  virtualList?: boolean;
  /** 实验室：有远程设备连着时不让电脑睡（后端起 caffeinate），断开 15 分钟后放手。后端读这个字段（cmd/gerdr 的 applyAwake）。 */
  keepAwakeRemote?: boolean;
  /** 实验室：不休眠时合上盖子也不睡（pmset disablesleep，要授权一次）。只在本来就该醒着时生效。 */
  keepAwakeLid?: boolean;
  agents?: AgentDef[];
};

/** config.json 里的 agents：claude / codex 的变体（base），或者外部 ACP agent（command）。 */
export type AgentDef = { id: string; name: string; base?: "claude" | "codex"; command?: string[]; bin?: string; args?: string[]; env?: Record<string, string> };

export const DEFAULT_CONFIG: Config = {
  prefix: "ctrl+b",
  keys: {},
  theme: "system",
  fontSize: 14,
  defaultAgent: "claude",
  defaultModes: {},
  notifications: true,
  sendKey: "enter",
  keepTabs: 6,
  virtualList: false,
  keepAwakeRemote: false,
  keepAwakeLid: false,
};

export function mergeConfig(raw: any): Config {
  const c = { ...DEFAULT_CONFIG, ...(raw && typeof raw === "object" ? raw : {}) };
  c.keys = { ...(raw?.keys ?? {}) };
  c.defaultModes = { ...(raw?.defaultModes ?? {}) };
  return c;
}

export function keyFor(c: Config, a: KeyAction): string[] {
  return c.keys[a] ?? DEFAULT_KEYS[a];
}

type Dialog =
  | { kind: "palette" }
  | { kind: "help" }
  | { kind: "settings"; tab?: string }
  | { kind: "sessions" }
  | { kind: "workspaces" }
  | { kind: "rename"; target: "tab" | "workspace" | "session"; id: string; value: string }
  | { kind: "newWorkspace" }
  | { kind: "importHerdr" }
  | { kind: "resume"; sessionId: string; paneId: string }
  | { kind: "file"; stat: FileStat }
  | null;

type State = {
  ready: boolean;
  authError: boolean;
  connected: boolean;
  keptTabs: string[]; // 现在挂在页面里的 tab（当前那个排第一），App 维护
  version: string;
  home: string;
  pluginsDir: string;
  agents: AgentInfo[];
  sessions: Record<string, SessionSummary>;
  /** 每个会话的对话（只有订阅着的会话有）；tv 是它的版本号，变了就重画。 */
  tv: Record<string, number>;
  layout: Layout;
  config: Config;
  prefixActive: boolean;
  dialog: Dialog;
  sidebar: boolean;
  /** 手机上：侧栏抽屉开着没有（不存盘 —— 每次打开页面都该先看见对话）。 */
  drawer: boolean;
  /** 手机上：面板一览（PaneSwitcher）开着没有。 */
  switcher: boolean;
  unread: Record<string, true>;
  /** 每个 pane 输入框里还没发的字（切 tab 不丢）。 */
  drafts: Record<string, string>;
  pluginVersion: number;
};

export const transcripts = new Map<string, Transcript>();

export function transcript(id: string): Transcript {
  let t = transcripts.get(id);
  if (!t) {
    t = new Transcript();
    transcripts.set(id, t);
  }
  return t;
}

function loadUnread(): Record<string, true> {
  try {
    return JSON.parse(localStorage.getItem("gerdr.unread") ?? "{}");
  } catch {
    return {};
  }
}

function loadSidebar(): boolean {
  try {
    return localStorage.getItem("gerdr.sidebar") !== "0";
  } catch {
    return true;
  }
}

export const useStore = create<State>(() => ({
  ready: false,
  authError: false,
  connected: false,
  keptTabs: [],
  version: "",
  home: "",
  pluginsDir: "",
  agents: [],
  sessions: {},
  tv: {},
  layout: defaultLayout(),
  config: DEFAULT_CONFIG,
  prefixActive: false,
  dialog: null,
  sidebar: loadSidebar(),
  drawer: false,
  switcher: false,
  unread: loadUnread(),
  drafts: {},
  pluginVersion: 0,
}));

export const get = useStore.getState;
export const set = useStore.setState;

// ---- 布局：本地先改，300ms 后写回服务端（别的窗口 / 设备跟着变）----

let layoutTimer: ReturnType<typeof setTimeout> | undefined;

export function setLayout(fn: (l: Layout) => Layout) {
  const prev = get().layout;
  const next = fn(prev);
  if (next === prev) return;
  set({ layout: next });
  clearTimeout(layoutTimer);
  layoutTimer = setTimeout(() => {
    api.putLayout(get().layout, clientId).catch(() => {});
  }, 300);
  afterLayoutChange(prev, next);
}

export function applyRemoteLayout(l: unknown) {
  if (validLayout(l)) {
    const prev = get().layout;
    set({ layout: l });
    afterLayoutChange(prev, l);
  }
}

const layoutListeners = new Set<(prev: Layout, next: Layout) => void>();
export function onLayoutChange(fn: (prev: Layout, next: Layout) => void) {
  layoutListeners.add(fn);
  return () => layoutListeners.delete(fn);
}
function afterLayoutChange(prev: Layout, next: Layout) {
  for (const fn of layoutListeners) fn(prev, next);
}

export function setConfig(patch: Partial<Config>) {
  const next = { ...get().config, ...patch };
  set({ config: next });
  api.putConfig(next, clientId).catch(() => {});
}

// ---- 会话日志 ----

// 子 agent 视图是从同一份日志另读一遍的，所以留着原始事件（新开一个子 agent 面板时从头重放）。
const rawEvents = new Map<string, HubEvent[]>();
const subTranscripts = new Map<string, Transcript>();

export function subTranscript(id: string, sub: string): Transcript {
  const k = `${id}#${sub}`;
  let t = subTranscripts.get(k);
  if (!t) {
    t = new Transcript(sub);
    t.reset(rawEvents.get(id) ?? []);
    subTranscripts.set(k, t);
  }
  return t;
}

export function applyEvents(id: string, events: HubEvent[], reset: boolean) {
  const t = transcript(id);
  if (reset) {
    t.reset(events);
    rawEvents.set(id, [...events]);
  } else {
    const fresh = events.filter((e) => e.seq > t.lastSeq);
    for (const e of fresh) t.apply(e);
    const raw = rawEvents.get(id) ?? [];
    raw.push(...fresh);
    rawEvents.set(id, raw);
    events = fresh;
  }
  for (const [k, st] of subTranscripts) {
    if (!k.startsWith(id + "#")) continue;
    if (reset) st.reset(events);
    else for (const e of events) st.apply(e);
  }
  set((s) => ({ tv: { ...s.tv, [id]: (s.tv[id] ?? 0) + 1 } }));
}

export function upsertSession(s: SessionSummary) {
  set((st) => ({ sessions: { ...st.sessions, [s.id]: s } }));
}

export function removeSession(id: string) {
  transcripts.delete(id);
  rawEvents.delete(id);
  for (const k of subTranscripts.keys()) if (k.startsWith(id + "#")) subTranscripts.delete(k);
  set((st) => {
    const sessions = { ...st.sessions };
    delete sessions[id];
    return { sessions };
  });
}

export function markRead(id: string) {
  if (!get().unread[id]) return;
  set((s) => {
    const unread = { ...s.unread };
    delete unread[id];
    try {
      localStorage.setItem("gerdr.unread", JSON.stringify(unread));
    } catch {}
    return { unread };
  });
}

export function markUnread(id: string) {
  if (get().unread[id]) return;
  set((s) => {
    const unread = { ...s.unread, [id]: true as const };
    try {
      localStorage.setItem("gerdr.unread", JSON.stringify(unread));
    } catch {}
    return { unread };
  });
}

export function toggleSidebar() {
  // 手机上侧栏是抽屉，开合不动桌面那份记忆（gerdr.sidebar）
  if (isPhone()) {
    set({ drawer: !get().drawer, switcher: false });
    return;
  }
  const v = !get().sidebar;
  set({ sidebar: v });
  try {
    localStorage.setItem("gerdr.sidebar", v ? "1" : "0");
  } catch {}
}

/** 屏幕上看得见的会话（当前 tab 里的 pane；放大时只有那一个）。 */
export function visibleSessions(l: Layout): string[] {
  const tab = activeTab(l);
  const ls = leaves(tab.root).filter((p) => !tab.zoom || p.id === tab.zoom);
  // 子 agent 面板看的也是它所属会话的日志。
  return [...new Set(ls.map((p) => p.sessionId ?? p.sub?.sessionId).filter((x): x is string => !!x))];
}

/** 要订阅日志的会话：看得见的 + 留在页面里没卸载的 tab 里的（它们切回来时不用整份重推、组件不用重建）。 */
export function watchedSessions(l: Layout, kept: string[]): string[] {
  const out = new Set(visibleSessions(l));
  const want = new Set(kept);
  for (const ws of l.workspaces)
    for (const t of ws.tabs)
      if (want.has(t.id)) for (const p of leaves(t.root)) if (p.sessionId ?? p.sub?.sessionId) out.add((p.sessionId ?? p.sub?.sessionId)!);
  return [...out];
}

export function focusedSession(l: Layout): string | undefined {
  const tab = activeTab(l);
  return leaves(tab.root).find((p) => p.id === tab.focus)?.sessionId;
}
