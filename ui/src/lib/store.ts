// The app's state (zustand) and the small actions on it. The rest lives in store/, one file per area so agents editing
// different parts don't collide: config.ts (the Config type, defaults), prefs.ts (per-device settings), layoutSync.ts
// (layout write-back), transcripts.ts (session logs). They are re-exported here, so importers keep using "./store".
import { create } from "zustand";
import { api, clientId, type DoctorReport, type FileStat } from "./api";
import { activeTab, currentSpace, defaultLayout, leaves, phoneRoot, type Layout } from "./layout";
import type { AppUpdateStatus } from "./native";
import { loadOverview, overviewFocus, overviewIds, phonePages, type Overview } from "./overviewState";
import { isPhone } from "./phone";
import type { AgentInfo, HostInfo, RemoteConn, SessionSummary } from "./types";
import { DEFAULT_CONFIG, type Config, type SharedDraft } from "./store/config";
import { type DevicePrefs, loadDevice, loadLocalFont, loadMode, loadSidebar, loadSidebarW, loadSpaceModes, loadUnread } from "./store/prefs";

export * from "./store/config";
export * from "./store/prefs";
export * from "./store/layoutSync";
export * from "./store/transcripts";

type Dialog =
  | { kind: "palette" }
  | { kind: "help" }
  /** host: another computer's screen (docs/dev/MULTIHOST.md); unset = this one */
  | { kind: "screen"; host?: string }
  | { kind: "settings"; tab?: string }
  | { kind: "sessions" }
  | { kind: "workspaces" }
  // space = a herdr-style session (layout.ts Space); newSpace reuses the rename dialog to name a new one (id unused)
  | { kind: "rename"; target: "tab" | "session" | "space" | "newSpace"; id: string; value: string }
  | { kind: "newWorkspace" }
  | { kind: "editWorkspace"; id: string }
  | { kind: "importHerdr" }
  /** host: the agent's records on another computer (docs/dev/MULTIHOST.md) */
  | { kind: "importAgent"; agent: string; cwd: string; paneId: string; host?: string }
  | { kind: "resume"; sessionId: string; paneId: string }
  /** carry a conversation on on another computer (MoveSessionDialog) */
  | { kind: "moveSession"; sessionId: string; paneId: string }
  | { kind: "file"; stat: FileStat }
  | { kind: "pickFile"; path: string; candidates: string[]; beside?: boolean; host?: string }
  | null;

type State = {
  ready: boolean;
  authError: boolean;
  connected: boolean;
  /**
   * What the connection is doing while not connected (sync.ts), so the UI can say "reconnecting" instead of a bare "offline":
   * connecting = an attempt is in flight; waiting = backing off, next attempt at `at` (ms). Meaningless while connected.
   */
  conn: { state: "connecting" } | { state: "waiting"; at: number };
  /**
   * The WebSocket has opened at least once since the page loaded. Before that the status says "connecting", not
   * "reconnecting", and the very first attempt shows nothing (the page is already showing its own loading state).
   */
  everConnected: boolean;
  /** Some connection attempt has failed or dropped since the page loaded: we're past the first try. */
  connRetrying: boolean;
  keptTabs: string[]; // 现在挂在页面里的 tab（当前那个排第一），App 维护
  version: string;
  /** When the running backend was installed (unix ms; 0 = unknown): the version string alone doesn't say how old it is. */
  installedAt: number;
  /** The desktop App's update status (desktop/main.js updateStatus); null in a browser / before the shell answered. */
  appUpdate: AppUpdateStatus | null;
  home: string;
  addonsDir: string;
  agents: AgentInfo[];
  /** 正在连着的远程界面：设置里「远程访问」tab 上的红点和「正在连接」列表。 */
  remoteConns: RemoteConn[];
  /** Other computers this one drives (Settings → Computers, docs/dev/MULTIHOST.md). */
  hosts: HostInfo[];
  /** Latest diagnosis (Labs → Diagnosis); a "bad" one puts a red dot by Settings in the sidebar. */
  doctor: DoctorReport | null;
  sessions: Record<string, SessionSummary>;
  /** 每个会话的对话（只有订阅着的会话有）；tv 是它的版本号，变了就重画。 */
  tv: Record<string, number>;
  /**
   * Sessions just subscribed to whose log has more than we hold (nothing cached, or the cache is behind), waiting for
   * the server's catch-up frame (sync.ts syncWatch). Switching to one keeps the old view up until it's in (useCaughtUp).
   */
  catchingUp: Record<string, true>;
  /** 会话日志前面还有没拉的（打开时服务端只推最近一段）：more = 还有，loading = 正在拉。没有这一项 = 已经到头了。 */
  older: Record<string, "more" | "loading">;
  layout: Layout;
  config: Config;
  prefixActive: boolean;
  dialog: Dialog;
  sidebar: boolean;
  /** Sidebar width in px (drag its right edge; the overview's list follows). Per device, in localStorage. */
  sidebarW: number;
  /** Which view fills the main area: the agent tabs, or the Work dashboard (WorkView). Per device, in localStorage. */
  mode: "agents" | "work";
  /**
   * The Work view has been opened in this page: from then on it stays mounted (hidden in Code mode) and its chat stays
   * watched, so switching back keeps the Work site's state (it's an iframe: unmounting reloads it) and the chat's.
   */
  workKept: boolean;
  /**
   * A conversation the Work page asked to show over itself (showSession in the site's gerdr.ts), e.g. a worker it
   * started: the real chat with the real composer (images, files), without making it the project's Work agent.
   */
  workPeek: WorkPeek | null;
  /** The all-agents overview (lib/overview.ts): covers the sidebar and the tabs while on. Per device, in localStorage. */
  overview: Overview;
  /**
   * 字号（px）：每台设备各一份，存本机（localStorage），不进 config —— config 存服务端、所有设备共用，
   * 手机上调大了字，电脑上不该跟着变大。0 = 这台还没设过，用 config.fontSize（以前存在那儿的，老用户设过的照旧生效）。
   */
  localFont: number;
  /** This device's own values for the settings in DEVICE_KEYS (localStorage); read them with prefOf. */
  device: DevicePrefs;
  /** 手机上：侧栏抽屉开着没有（不存盘 —— 每次打开页面都该先看见对话）。 */
  drawer: boolean;
  unread: Record<string, true>;
  /** 每个 pane 输入框里还没发的字（切 tab 不丢）。 */
  drafts: Record<string, string>;
  /** 每个 pane 输入框里贴了还没发的图：和字一样，tab 不保留时切走输入框会卸载，放组件里就丢了（用户报的）。 */
  draftImgs: Record<string, { data: string; mimeType: string; url: string; path?: string; name?: string; blob?: string }[]>;
  /**
   * Session id → the composer content every device agrees on (sync.ts pushDraft / the server's "draft" frames). The
   * composer copies it into its pane's draft when it changes, and pushes its own edits here.
   */
  sharedDrafts: Record<string, SharedDraft>;
  /**
   * The one message put aside with ⌘S (actions.stashDraft): text and images, not tied to a pane, so it can be taken back
   * out in any other session or agent. Memory only, like drafts.
   */
  stash: { text: string; imgs: State["draftImgs"][string] } | null;
  /** Bumped when add-ons load / unload or their state changes: what reads them re-renders. */
  addonVersion: number;
  /** Bumped on "addon_stores": the store tab fetches again. */
  addonStoresVersion: number;
  /** Bumped on "ext" (gerdr's skills / MCP changed outside the Settings page): what lists them fetches again. */
  extVersion: number;
  /** 正在直接开会话的 pane（⌘D 复制）：开好之前别画空面板（挑 agent 那一屏闪一下再变成对话，用户不想看见）。不存盘。 */
  starting: Record<string, true>;
};

export const useStore = create<State>(() => ({
  ready: false,
  authError: false,
  connected: false,
  conn: { state: "connecting" },
  everConnected: false,
  connRetrying: false,
  keptTabs: [],
  version: "",
  installedAt: 0,
  appUpdate: null,
  home: "",
  addonsDir: "",
  agents: [],
  remoteConns: [],
  hosts: [],
  doctor: null,
  sessions: {},
  tv: {},
  catchingUp: {},
  older: {},
  layout: defaultLayout(),
  config: DEFAULT_CONFIG,
  device: loadDevice(),
  prefixActive: false,
  dialog: null,
  sidebar: loadSidebar(),
  sidebarW: loadSidebarW(),
  mode: loadMode(),
  workKept: loadMode() === "work",
  workPeek: null,
  overview: loadOverview(),
  localFont: loadLocalFont(),
  drawer: false,
  unread: loadUnread(),
  drafts: {},
  draftImgs: {},
  sharedDrafts: {},
  stash: null,
  addonVersion: 0,
  addonStoresVersion: 0,
  extVersion: 0,
  starting: {},
}));

export const get = useStore.getState;

/** 给 streamdown 的 linkSafety：在组件里调（订阅了 config，切开关后已渲染的消息也跟着变）。 */
export const useLinkSafety = () => ({ enabled: useStore((s) => s.config.confirmLinks !== false) });
export const set = useStore.setState;

/** Close whatever store.dialog overlay is open (the dialogs rendered by components/Dialogs.tsx). */
export const closeDialog = () => set({ dialog: null });

export function setConfig(patch: Partial<Config>) {
  const next = { ...get().config, ...patch };
  set({ config: next });
  api.putConfig(next, clientId).catch(() => {});
}

export function upsertSession(s: SessionSummary) {
  set((st) => ({ sessions: { ...st.sessions, [s.id]: s } }));
}

/**
 * Unread is one set for every device, kept by the backend (server/unread.go): changes go there and come back to every
 * client as an "unread" frame (sync.ts). `server` is false against a backend from before that (no `unread` in hello),
 * which keeps the old per-device copy in localStorage until it restarts onto a new binary.
 */
export const unreadSync = { server: false };

/** The set as the backend sent it (hello, "unread" frames). */
export function gotUnread(raw: unknown) {
  if (!raw || typeof raw !== "object") return;
  const unread: Record<string, true> = {};
  for (const [k, v] of Object.entries(raw)) if (v) unread[k] = true;
  set({ unread });
}

function saveUnread(id: string, on: boolean, unread: Record<string, true>) {
  if (unreadSync.server) return void api.setUnread(id, on, clientId).catch(() => {});
  try {
    localStorage.setItem("gerdr.unread", JSON.stringify(unread));
  } catch {}
}

export function markRead(id: string) {
  if (!get().unread[id]) return;
  set((s) => {
    const unread = { ...s.unread };
    delete unread[id];
    saveUnread(id, false, unread);
    return { unread };
  });
}

export function markUnread(id: string) {
  if (get().unread[id]) return;
  set((s) => {
    const unread = { ...s.unread, [id]: true as const };
    saveUnread(id, true, unread);
    return { unread };
  });
}

export function setMode(mode: "agents" | "work") {
  set(modePatch(mode));
  saveMode(currentSpace(get().layout), mode);
}

function modePatch(mode: "agents" | "work"): Partial<State> {
  return mode === "work" ? { mode, drawer: false, workKept: true } : { mode, drawer: false };
}

function saveMode(space: string, mode: "agents" | "work") {
  try {
    localStorage.setItem("gerdr.mode", mode);
    localStorage.setItem("gerdr.spaceMode", JSON.stringify({ ...loadSpaceModes(), [space]: mode }));
  } catch {}
}

/**
 * The state patch for landing in `space` (setLayout, when the current project changes): each project keeps the Code / Work
 * mode it was left in, so switching projects doesn't drag the other one's mode along. A project never seen on this device
 * keeps whatever mode is on now.
 */
export function enterSpaceMode(space: string): Partial<State> {
  const mode = loadSpaceModes()[space] ?? get().mode;
  saveMode(space, mode);
  return mode === get().mode ? {} : modePatch(mode);
}

export function toggleSidebar() {
  // 手机上侧栏是抽屉，开合不动桌面那份记忆（gerdr.sidebar）
  if (isPhone()) {
    set({ drawer: !get().drawer });
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
  // The overview covers the tabs. Only the focused tile counts as looked at, the
  // others are just in view: one that finishes there still gets the unread dot (asked for). They're all watched, see below.
  if (overviewShown()) {
    const f = overviewFocus({ ...get(), layout: l });
    return f ? [f] : [];
  }
  const tab = activeTab(l);
  const ls = phone ? leaves(phoneRoot(tab)) : leaves(tab.root).filter((p) => !tab.zoom || p.id === tab.zoom);
  // 子 agent 面板看的也是它所属会话的日志。
  return [...new Set(ls.map((p) => p.sessionId ?? p.sub?.sessionId).filter((x): x is string => !!x))];
}

/** The overview is on screen (it covers everything but the Work view). */
export const overviewShown = () => get().overview.on && get().mode !== "work";

/** 要订阅日志的会话：看得见的 + 留在页面里没卸载的 tab 里的（它们切回来时不用整份重推、组件不用重建）。 */
/**
 * What a Work page asked showSession to show: the conversation, and optionally a line about it (e.g. "t11 · needs
 * you") and buttons; a click goes back to that project's page as gerdr:sessionAction. Gerdr gives them no meaning.
 */
export type WorkPeek = {
  id: string;
  project: string;
  label?: string;
  actions?: { id: string; label: string; kind?: "primary" | "danger"; confirm?: string }[];
};

export function watchedSessions(l: Layout, kept: string[]): string[] {
  const out = new Set(visibleSessions(l));
  // The Work view's chat isn't in the layout; it's watched while the Work view is mounted (shown, or kept hidden).
  if (get().mode === "work" || get().workKept) {
    const space = currentSpace(l);
    const c = get().config;
    const work = c.workSessions?.[space] ?? (space === "default" ? c.workSession : undefined);
    if (work) out.add(work);
    const peek = get().workPeek;
    if (peek) out.add(peek.id);
  }
  // The tabs under the overview stay watched (kept), so closing it doesn't reload them
  // On a phone only the shown tile and its two neighbours (the pages a swipe drags in) are mounted (OverviewPhone.tsx):
  // don't stream the rest
  if (overviewShown()) for (const id of isPhone() ? phonePages({ ...get(), layout: l }) : overviewIds({ ...get(), layout: l })) out.add(id);
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
