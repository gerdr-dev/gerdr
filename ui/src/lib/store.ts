import { create } from "zustand";
import { api, clientId, uuid, type FileStat } from "./api";
import { DEFAULT_KEYS, type KeyAction } from "./keys";
import { activeTab, defaultLayout, leaves, phoneRoot, validLayout, viewOf, withView, type Layout, type View } from "./layout";
import { isPhone } from "./phone";
import { Transcript } from "./transcript";
import type { AgentInfo, ContentBlock, HubEvent, RemoteConn, SessionSummary } from "./types";

export type Theme = "system" | "light" | "dark";

export type Config = {
  prefix: string;
  keys: Partial<Record<KeyAction, string[]>>;
  theme: Theme;
  /** 旧版存这儿、所有设备共用；现在字号存各设备本机（State.localFont），这个只当没设过时的初值 */
  fontSize: number;
  defaultAgent: string;
  /** 新会话的默认模式（按 agent）。 */
  defaultModes: Record<string, string>;
  notifications: boolean;
  /** 发送：enter（回车发、Shift+回车换行）或 mod+enter（⌘/Ctrl+回车发）。 */
  sendKey: "enter" | "mod+enter";
  keepTabs: number; // 切走之后还留在页面里的 tab 数（展开状态、滚动位置都保住）；1 = 不留
  /** 改文件 / 新建文件的工具调用默认展开 diff。关掉后只剩一行（文件名、+/- 行数），点开再看。 */
  expandEdits?: boolean;
  /** 实验室：对话用虚拟滚动（只渲染屏幕附近的行）。默认关 —— 往上翻时行卸载再挂载，带图的行高度先 0 后撑开，界面一跳一跳（用户报的）。 */
  virtualList?: boolean;
  /** 实验室：有远程设备连着时不让电脑睡（后端起 caffeinate），断开 15 分钟后放手；开着时合上盖子也不睡
   *  （pmset disablesleep，要授权一次，手动时段也算）。后端读这个字段（cmd/gerdr 的 applyAwake）。 */
  keepAwakeRemote?: boolean;
  agents?: AgentDef[];
  /** 输入框里 pin 住的 / 命令（命令名，不带 /）：附图按钮旁边多一个 / 按钮，点开就是这些。存 config 是让手机和电脑共用一份。 */
  pinnedCommands?: string[];
  /** Gerdr Agent（native）用的模型服务商，后端读（cmd/gerdr 的 cfgLLM），设置 → Agent 里编辑。 */
  llm?: LLMConfig;
};

/** config.json 的 llm：Gerdr Agent 直接调的模型接口（OpenAI 兼容）。模型只写 id 时协议、思考档位按 id 猜（后端 native/config.go）。 */
export type LLMModel = { id: string; name?: string; api?: "chat" | "responses"; efforts?: string[]; window?: number };
export type LLMProvider = { id: string; name?: string; baseUrl: string; apiKey?: string; apiKeyEnv?: string; models: LLMModel[] };
export type LLMConfig = { providers?: LLMProvider[]; defaultModel?: string };

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
  expandEdits: true,
  virtualList: false,
  keepAwakeRemote: false,
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
  | { kind: "pickFile"; path: string; candidates: string[] }
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
  /** 正在连着的远程界面：设置里「远程访问」tab 上的红点和「正在连接」列表。 */
  remoteConns: RemoteConn[];
  sessions: Record<string, SessionSummary>;
  /** 每个会话的对话（只有订阅着的会话有）；tv 是它的版本号，变了就重画。 */
  tv: Record<string, number>;
  /** 会话日志前面还有没拉的（打开时服务端只推最近一段）：more = 还有，loading = 正在拉。没有这一项 = 已经到头了。 */
  older: Record<string, "more" | "loading">;
  layout: Layout;
  config: Config;
  prefixActive: boolean;
  dialog: Dialog;
  sidebar: boolean;
  /**
   * 字号（px）：每台设备各一份，存本机（localStorage），不进 config —— config 存服务端、所有设备共用，
   * 手机上调大了字，电脑上不该跟着变大。0 = 这台还没设过，用 config.fontSize（以前存在那儿的，老用户设过的照旧生效）。
   */
  localFont: number;
  /** 手机上：侧栏抽屉开着没有（不存盘 —— 每次打开页面都该先看见对话）。 */
  drawer: boolean;
  /** 手机上：面板一览（PaneSwitcher）开着没有。 */
  switcher: boolean;
  unread: Record<string, true>;
  /** 每个 pane 输入框里还没发的字（切 tab 不丢）。 */
  drafts: Record<string, string>;
  /** 每个 pane 输入框里贴了还没发的图：和字一样，tab 不保留时切走输入框会卸载，放组件里就丢了（用户报的）。 */
  draftImgs: Record<string, { data: string; mimeType: string; url: string }[]>;
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

function loadLocalFont(): number {
  try {
    return Number(localStorage.getItem("gerdr.fontSize")) || 0;
  } catch {
    return 0;
  }
}

/** 这台设备用的字号 */
export const fontSizeOf = (s: { localFont: number; config: Config }) => s.localFont || s.config.fontSize;

export function setFontSize(n: number) {
  set({ localFont: n });
  try {
    localStorage.setItem("gerdr.fontSize", String(n));
  } catch {}
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
  remoteConns: [],
  sessions: {},
  tv: {},
  older: {},
  layout: defaultLayout(),
  config: DEFAULT_CONFIG,
  prefixActive: false,
  dialog: null,
  sidebar: loadSidebar(),
  localFont: loadLocalFont(),
  drawer: false,
  switcher: false,
  unread: loadUnread(),
  drafts: {},
  draftImgs: {},
  pluginVersion: 0,
}));

export const get = useStore.getState;
export const set = useStore.setState;

// ---- 布局：本地先改，300ms 后写回服务端（别的窗口 / 设备跟着变）----

let layoutTimer: ReturnType<typeof setTimeout> | undefined;

// 这个端自己的「看的位置」（layout.ts 的 View）：存在本机浏览器里，主机和远程（不同的 origin）天然各一份。
const VIEW_KEY = "gerdr.view";
function saveView(l: Layout) {
  try {
    localStorage.setItem(VIEW_KEY, JSON.stringify(viewOf(l)));
  } catch {}
}
export function loadView(): View | null {
  try {
    const v = JSON.parse(localStorage.getItem(VIEW_KEY) ?? "null");
    return v && typeof v.active === "string" && v.ws && v.tabs ? v : null;
  } catch {
    return null;
  }
}

export function setLayout(fn: (l: Layout) => Layout) {
  const prev = get().layout;
  const next = fn(prev);
  if (next === prev) return;
  set({ layout: next });
  saveView(next);
  clearTimeout(layoutTimer);
  layoutTimer = setTimeout(() => {
    api.putLayout(get().layout, clientId).catch(() => {});
  }, 300);
  afterLayoutChange(prev, next);
}

/** 别的端存的布局（或重连时服务端那份）：结构照收，「看的位置」留自己的 —— 手机上切 tab，主机不跟着动。 */
export function applyRemoteLayout(l: unknown) {
  if (validLayout(l)) {
    const prev = get().layout;
    const next = withView(l, viewOf(prev));
    set({ layout: next });
    afterLayoutChange(prev, next);
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

export function applyEvents(id: string, events: HubEvent[], reset: boolean, older = false) {
  const t = transcript(id);
  if (reset) {
    t.reset(events);
    rawEvents.set(id, [...events]);
    setOlder(id, older ? "more" : undefined);
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

/**
 * 发一条消息，点下去就先在对话最底下画一条半透明的（见 Transcript.sending），日志里那条到了换掉。
 * 失败就拿掉、错误照旧抛给调用方（它负责把字放回输入框）。
 */
export async function sendShown(id: string, content: ContentBlock[], onRetry?: (attempt: number) => void, now?: boolean) {
  const msgId = uuid();
  const t = transcript(id);
  const bump = () => set((s) => ({ tv: { ...s.tv, [id]: (s.tv[id] ?? 0) + 1 } }));
  t.addSending(msgId, content);
  bump();
  try {
    const r = await api.send(id, content, onRetry, now, msgId);
    // 服务端回成功之前已经记下这条、推出来了，正常早就换掉了；推送断了的话重连会整份重推，也会换掉。
    // 万一两样都没赶上，别让一条半透明的永远挂着
    setTimeout(() => {
      if (!t.sending.some((m) => m.id === msgId)) return;
      t.dropSending(msgId);
      bump();
    }, 10_000);
    return r;
  } catch (e) {
    t.dropSending(msgId);
    bump();
    throw e;
  }
}

function setOlder(id: string, v: "more" | "loading" | undefined) {
  set((s) => {
    if (s.older[id] === v) return {};
    const older = { ...s.older };
    if (v) older[id] = v;
    else delete older[id];
    return { older };
  });
}

/**
 * 往上翻到顶了：拉更早的一段接在前面，整份重放（Transcript 只会从头往后放；消息 id 按日志序号起，已有的不变）。
 * 打开会话时服务端只推最近一段（hub/blob.go）—— 远程打开一个几十 MB 的会话，整份推过去传不完。
 */
export async function loadOlder(id: string) {
  const raw = rawEvents.get(id);
  if (get().older[id] !== "more" || !raw?.length) return;
  setOlder(id, "loading");
  let next: "more" | undefined = "more";
  try {
    const r = await api.olderEvents(id, raw[0].seq);
    // 等的这会儿整份重推过（回退、重连接不上）：手里换了一份，这次拉到的接不上，丢掉（older 也由那次重推定了）
    if (rawEvents.get(id) !== raw) return;
    next = r.older ? "more" : undefined;
    const all = [...(r.events ?? []), ...raw];
    rawEvents.set(id, all);
    transcript(id).reset(all);
    for (const [k, st] of subTranscripts) if (k.startsWith(id + "#")) st.reset(all);
    set((s) => ({ tv: { ...s.tv, [id]: (s.tv[id] ?? 0) + 1 } }));
  } catch {
    // 网络不好：留着 more，下次翻到顶再试
  } finally {
    if (get().older[id] === "loading") setOlder(id, next);
  }
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

/** 屏幕上看得见的会话（当前 tab 里的 pane；放大时只有那一个；手机上见 phoneRoot：左右分屏只看得见焦点那边）。 */
export function visibleSessions(l: Layout, phone = false): string[] {
  const tab = activeTab(l);
  const ls = phone ? leaves(phoneRoot(tab)) : leaves(tab.root).filter((p) => !tab.zoom || p.id === tab.zoom);
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
