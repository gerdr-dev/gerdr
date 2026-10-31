// 插件运行时：服务端用 esbuild 把 ~/.gerdr/plugins 里的 TS 编成 ESM，这里用 Blob URL
// import 进来，给它一个 Gerdr API 对象（见 internal/plugins/builtin/gerdr.d.ts —— 两边对齐）。
//
// 插件出错不能把界面拖垮：每个钩子调用都包 try/catch，出错记在插件名下（设置里能看到）。
import { api } from "./api";
import { activeTab, activeWs, leaves, type Layout, type PaneLeaf, type Tab, type Workspace } from "./layout";
import { get, set, useStore, visibleSessions } from "./store";
import type { PluginBuild, PluginService, SessionSummary } from "./types";

type TabLabelFn = (tab: any, ctx: any) => string | void;
type PaneTitleFn = (pane: any, ctx: any) => string | void;
type WsLabelFn = (ws: any, ctx: any) => string | void;
type SessionTitleFn = (session: any, ctx: any) => string | void | Promise<string | void>;
export type PluginCommand = { id: string; title: string; key?: string; run: (ctx: any) => void | Promise<void>; plugin: string };

type Reg<T> = { plugin: string; fn: T };

const tabLabels: Reg<TabLabelFn>[] = [];
const paneTitles: Reg<PaneTitleFn>[] = [];
const wsLabels: Reg<WsLabelFn>[] = [];
const sessionTitles: Reg<SessionTitleFn>[] = [];
export const pluginCommands: PluginCommand[] = [];
const listeners: { plugin: string; event: string; fn: (e: any) => void }[] = [];
// g.service.on: messages / status changes from a plugin's own backend process
const serviceListeners: { plugin: string; event: "message" | "status"; fn: (e: any) => void }[] = [];
const serviceStates = new Map<string, PluginService>();

export type PluginStatus = { id: string; path: string; error?: string; loaded: boolean; service?: PluginService };
export let pluginStatus: PluginStatus[] = [];

const disposers = new Map<string, () => void>();

(globalThis as any).definePlugin = (p: unknown) => p;

function bump() {
  set((s) => ({ pluginVersion: s.pluginVersion + 1 }));
}

function remove<T extends { plugin: string }>(arr: T[], plugin: string) {
  for (let i = arr.length - 1; i >= 0; i--) if (arr[i].plugin === plugin) arr.splice(i, 1);
}

function fail(id: string, err: unknown) {
  const msg = err instanceof Error ? `${err.message}` : String(err);
  console.error(`[gerdr plugin ${id}]`, err);
  const st = pluginStatus.find((p) => p.id === id);
  if (st) st.error = msg;
}

// ---- 给插件看的只读快照 ----

function sessionInfo(s?: SessionSummary) {
  if (!s) return undefined;
  return { id: s.id, agent: s.agent, title: s.title ?? "", cwd: s.cwd, status: s.status };
}

function paneInfo(p: PaneLeaf) {
  const s = p.sessionId ? get().sessions[p.sessionId] : undefined;
  return { id: p.id, sessionId: p.sessionId, session: sessionInfo(s), terminal: p.term ? { ...p.term } : undefined };
}

function tabInfo(t: Tab, name: string) {
  return { id: t.id, name, panes: leaves(t.root).map(paneInfo) };
}

let tabNameFn: (t: Tab) => string = (t) => t.name ?? "";
export function setTabNameFn(fn: (t: Tab) => string) {
  tabNameFn = fn;
}

function wsInfo(w: Workspace) {
  return { id: w.id, name: w.name, tabs: w.tabs.map((t) => tabInfo(t, tabNameFn(t))) };
}

export function stateSnapshot(l: Layout = get().layout) {
  const ws = activeWs(l);
  const tab = activeTab(l);
  const pane = leaves(tab.root).find((p) => p.id === tab.focus);
  return {
    workspaces: l.workspaces.map(wsInfo),
    activeWorkspace: wsInfo(ws),
    activeTab: tabInfo(tab, tabNameFn(tab)),
    activePane: pane ? paneInfo(pane) : undefined,
    sessions: Object.values(get().sessions).map((s) => sessionInfo(s)!),
  };
}

// ---- 钩子的应用（界面渲染时调）----

export function applyTabLabel(tab: Tab, index: number, ws: Workspace, base: string): string {
  let label = base;
  if (!tabLabels.length) return label;
  const info = tabInfo(tab, base);
  const wsi = wsInfo(ws);
  for (const r of tabLabels) {
    try {
      const v = r.fn(info, { index, workspace: wsi, label });
      if (typeof v === "string") label = v;
    } catch (e) {
      fail(r.plugin, e);
    }
  }
  return label;
}

export function applyPaneTitle(pane: PaneLeaf, tab: Tab, base: string): string {
  let label = base;
  if (!paneTitles.length) return label;
  const info = paneInfo(pane);
  const ti = tabInfo(tab, tabNameFn(tab));
  for (const r of paneTitles) {
    try {
      const v = r.fn(info, { label, tab: ti });
      if (typeof v === "string") label = v;
    } catch (e) {
      fail(r.plugin, e);
    }
  }
  return label;
}

export function applyWorkspaceLabel(ws: Workspace, index: number, base: string): string {
  let label = base;
  if (!wsLabels.length) return label;
  const info = wsInfo(ws);
  for (const r of wsLabels) {
    try {
      const v = r.fn(info, { index, label });
      if (typeof v === "string") label = v;
    } catch (e) {
      fail(r.plugin, e);
    }
  }
  return label;
}

export function emit(event: string, payload: any) {
  for (const l of listeners) {
    if (l.event !== event) continue;
    try {
      l.fn(payload);
    } catch (e) {
      fail(l.plugin, e);
    }
  }
}

/** 会话状态变化 → 插件事件（turnEnd / needsInput / sessionCreated）。sync.ts 在收到推送时调。 */
export function emitSessionChange(prev: SessionSummary | undefined, next: SessionSummary) {
  const visible = visibleSessions(get().layout).includes(next.id) && document.visibilityState === "visible";
  const info = sessionInfo(next);
  if (!prev) {
    emit("sessionCreated", { session: info, visible });
    return;
  }
  if (prev.status !== "waiting" && next.status === "waiting") emit("needsInput", { session: info, visible, kind: "permission" });
  if ((prev.status === "running" || prev.status === "waiting") && (next.status === "idle" || next.status === "error")) {
    emit("turnEnd", { session: info, visible, stopReason: next.status === "error" ? "error" : "end_turn", error: next.error });
    if (sessionTitles.length) runSessionTitle(info!);
  }
}

// 一轮结束 → 问插件要不要改会话标题。多个插件按文件名顺序串起来（ctx.title 是前面给出的）；有插件给出了名字就交给
// 后端（auto：人改过名时不生效，这段上下文里后端的默认起名让开，见 internal/hub/title.go）。
// 每个开着的窗口都会跑一遍：后端对同样的 summarize 只真跑一次，起出来的名字一样，不会来回跳。
async function runSessionTitle(info: ReturnType<typeof sessionInfo> & {}) {
  let st: { title: string; customTitle: boolean; prompts: string[] };
  try {
    st = await api.titleState(info.id);
  } catch {
    return;
  }
  if (st.customTitle) return;
  let title = st.title;
  let changed = false;
  const summarize = async (prompt: string) => (await api.complete(info.id, prompt)).text;
  for (const r of [...sessionTitles]) {
    try {
      const v = await r.fn(info, { title, prompts: st.prompts, turn: st.prompts.length, summarize });
      if (typeof v === "string" && v.trim()) {
        title = v.trim();
        changed = true;
      }
    } catch (e) {
      fail(r.plugin, e);
    }
  }
  if (changed) api.rename(info.id, title, true).catch(() => {});
}

// 焦点 pane 变了（点 pane、切 tab / 工作空间都算）→ focusChanged。layout 每次改动都会过这里，
// 所以只比较焦点 pane 的 id，没变就不发；没人听时连快照都不拼。
let lastFocus = "";
useStore.subscribe((s, prev) => {
  if (s.layout === prev.layout) return;
  const id = activeTab(s.layout).focus;
  if (id === lastFocus) return;
  lastFocus = id;
  if (!listeners.some((l) => l.event === "focusChanged")) return;
  const snap = stateSnapshot(s.layout);
  emit("focusChanged", { pane: snap.activePane, tab: snap.activeTab, workspace: snap.activeWorkspace });
});

// ---- 加载 ----

function makeApi(id: string) {
  const own = <T>(arr: Reg<T>[], fn: T) => {
    const r = { plugin: id, fn };
    arr.push(r);
    bump();
    return () => {
      const i = arr.indexOf(r);
      if (i >= 0) arr.splice(i, 1);
      bump();
    };
  };
  const storeKey = (k: string) => `gerdr.plugin.${id}.${k}`;
  return {
    version: get().version,
    tabLabel: (fn: TabLabelFn) => own(tabLabels, fn),
    paneTitle: (fn: PaneTitleFn) => own(paneTitles, fn),
    workspaceLabel: (fn: WsLabelFn) => own(wsLabels, fn),
    sessionTitle: (fn: SessionTitleFn) => own(sessionTitles, fn),
    command: (c: Omit<PluginCommand, "plugin">) => {
      const cmd = { ...c, plugin: id };
      pluginCommands.push(cmd);
      bump();
      return () => {
        const i = pluginCommands.indexOf(cmd);
        if (i >= 0) pluginCommands.splice(i, 1);
        bump();
      };
    },
    on: (event: string, fn: (e: any) => void) => {
      const l = { plugin: id, event, fn };
      listeners.push(l);
      return () => {
        const i = listeners.indexOf(l);
        if (i >= 0) listeners.splice(i, 1);
      };
    },
    notify: (text: string, opts?: any) => import("./notify").then((n) => n.notify(text, opts)),
    state: () => stateSnapshot(),
    service: {
      status: () => serviceStates.get(id),
      send: async (data: unknown) => {
        await api.pluginMessage(id, data);
      },
      on: (event: "message" | "status", fn: (e: any) => void) => {
        const l = { plugin: id, event, fn };
        serviceListeners.push(l);
        return () => {
          const i = serviceListeners.indexOf(l);
          if (i >= 0) serviceListeners.splice(i, 1);
        };
      },
    },
    actions: {
      newTab: async (o?: any) => (await import("./actions")).addTab(o),
      duplicateTab: async (tabId: string, where: "after" | "before") => (await import("./actions")).duplicateTab(tabId, where),
      renameTab: (tabId: string, name: string) => import("./actions").then((a) => a.renameTab(tabId, name)),
      renameWorkspace: (wid: string, name: string) => import("./actions").then((a) => a.renameWorkspace(wid, name)),
      focusPane: (pid: string) => import("./actions").then((a) => a.focusPane(pid)),
      split: async (dir: "right" | "down") => (await import("./actions")).splitFocused(dir),
      send: async (sid: string, text: string) => {
        await (await import("./actions")).send(sid, text);
      },
      runCommand: async (cid: string) => runCommand(cid),
    },
    storage: {
      get: (k: string) => {
        try {
          const v = localStorage.getItem(storeKey(k));
          return v == null ? undefined : JSON.parse(v);
        } catch {
          return undefined;
        }
      },
      set: (k: string, v: unknown) => {
        try {
          localStorage.setItem(storeKey(k), JSON.stringify(v));
        } catch {}
      },
    },
  };
}

export async function runCommand(id: string) {
  const { runBuiltinCommand } = await import("./commands");
  if (runBuiltinCommand(id)) return;
  const c = pluginCommands.find((x) => x.id === id);
  if (!c) return;
  try {
    await c.run({ state: stateSnapshot() });
  } catch (e) {
    fail(c.plugin, e);
    import("./notify").then((n) => n.notify(`插件 ${c.plugin} 出错：${e}`, { level: "error" }));
  }
}

function unload(id: string) {
  const d = disposers.get(id);
  disposers.delete(id);
  try {
    d?.();
  } catch (e) {
    console.error(e);
  }
  remove(tabLabels, id);
  remove(paneTitles, id);
  remove(wsLabels, id);
  remove(sessionTitles, id);
  remove(pluginCommands, id);
  remove(listeners, id);
  remove(serviceListeners, id);
}

function emitService(id: string, event: "message" | "status", e: unknown) {
  for (const l of serviceListeners.filter((x) => x.plugin === id && x.event === event)) {
    try {
      l.fn(e);
    } catch (err) {
      fail(id, err);
    }
  }
}

export function serviceStatusChanged(id: string | undefined, st: PluginService | undefined) {
  if (!id || !st) return;
  if (st.state === "stopped") serviceStates.delete(id);
  else serviceStates.set(id, st);
  const p = pluginStatus.find((x) => x.id === id);
  if (p) p.service = serviceStates.get(id);
  emitService(id, "status", st);
  bump();
}

export function serviceMessage(id: string | undefined, data: unknown) {
  if (id) emitService(id, "message", data);
}

let loadedHashes = new Map<string, string>();
let loading: Promise<void> | null = null;

export function reloadPlugins(): Promise<void> {
  if (loading) return loading.then(() => reloadPlugins());
  loading = doReload().finally(() => (loading = null));
  return loading;
}

async function doReload() {
  let builds: PluginBuild[];
  try {
    builds = await api.plugins();
  } catch {
    return;
  }
  const seen = new Set<string>();
  const status: PluginStatus[] = [];
  serviceStates.clear();
  for (const b of builds) if (b.service) serviceStates.set(b.id, b.service);
  for (const b of builds) {
    seen.add(b.id);
    if (loadedHashes.get(b.id) === b.hash) {
      const prev = pluginStatus.find((p) => p.id === b.id) ?? { id: b.id, path: b.path, loaded: true };
      status.push({ ...prev, service: b.service });
      continue;
    }
    unload(b.id);
    loadedHashes.set(b.id, b.hash);
    // No code and no error: a plugin that is only a service (plugin.json), nothing to load in the UI
    const st: PluginStatus = { id: b.id, path: b.path, loaded: !b.error && !b.code, error: b.error, service: b.service };
    status.push(st);
    if (b.error || !b.code) continue;
    const url = URL.createObjectURL(new Blob([b.code], { type: "text/javascript" }));
    try {
      const mod = await import(/* @vite-ignore */ url);
      const plugin = mod.default ?? mod.plugin;
      if (!plugin || typeof plugin.setup !== "function") throw new Error("插件要 export default definePlugin({ setup(g) { … } })");
      const d = plugin.setup(makeApi(b.id));
      if (typeof d === "function") disposers.set(b.id, d);
      st.loaded = true;
    } catch (e) {
      st.error = e instanceof Error ? e.message : String(e);
      console.error(`[gerdr plugin ${b.id}]`, e);
    } finally {
      URL.revokeObjectURL(url);
    }
  }
  for (const id of [...loadedHashes.keys()]) {
    if (!seen.has(id)) {
      unload(id);
      loadedHashes.delete(id);
    }
  }
  pluginStatus = status;
  bump();
}
