// Add-on runtime: the backend bundles each add-on in ~/.gerdr/addons with esbuild into ESM, imported here through a
// Blob URL and handed a Gerdr API object (internal/addons/builtin/gerdr.d.ts — keep the two in step).
//
// An add-on's mistakes must not take the UI down: every hook call is wrapped in try/catch, and errors are kept under
// the add-on's name (Settings → Add-ons shows them).
import * as icons from "lucide-react";
import * as React from "react";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api } from "./api";
import { cn } from "./utils";
import { activeTab, activeWs, leaves, type Layout, type PaneLeaf, type Tab, type Workspace } from "./layout";
import { get, set, useStore, visibleSessions } from "./store";
import type { AddonBuild, AddonService, SessionSummary } from "./types";

type TabLabelFn = (tab: any, ctx: any) => string | void;
type PaneTitleFn = (pane: any, ctx: any) => string | void;
type WsLabelFn = (ws: any, ctx: any) => string | void;
type SessionTitleFn = (session: any, ctx: any) => string | void | Promise<string | void>;
export type AddonCommand = { id: string; title: string; key?: string; run: (ctx: any) => void | Promise<void>; addon: string };

type Reg<T> = { addon: string; fn: T };

// ---- what add-ons draw: slots, composer buttons, context-menu items ----

/** Where an add-on can put a component: the sidebar (above its bottom row), a pane's title bar (props: pane, tab). */
export type SlotName = "sidebar" | "paneTitle";
export type SlotReg = { addon: string; key: string; Component: React.ComponentType<any>; order: number };
export const slots: Record<SlotName, SlotReg[]> = { sidebar: [], paneTitle: [] };
export type ComposerAction = { addon: string; id: string; title: string; icon?: React.ReactNode; run: (ctx: ComposerCtx) => void | Promise<void> };
export type ComposerCtx = { sessionId: string; text: string; insert(text: string): void; setText(text: string): void };
export const composerActions: ComposerAction[] = [];
export type MenuItem = { addon: string; id: string; title: string; when?: (ctx: MenuCtx) => boolean; run: (ctx: MenuCtx) => void | Promise<void> };
export type MenuCtx = { selection: string; sessionId?: string };
export const menuItems: MenuItem[] = [];
type TurnStatFn = (info: any) => { className?: string; title?: string; after?: React.ReactNode } | void;
const turnStats: Reg<TurnStatFn>[] = [];

/**
 * ChatHost.useTurnStat: what add-ons make of a turn's metadata line (g.turnStat). Re-renders every 30s while any
 * add-on decorates, so an answer that depends on the time (a prompt cache running out) follows it.
 */
export function useAddonTurnStat(info: { sessionId: string; ts?: number; ms: number; tokens: number; latest: boolean }) {
  useStore((s) => s.addonVersion);
  const agent = useStore((s) => s.sessions[info.sessionId]?.agent ?? "");
  const [now, setNow] = React.useState(Date.now);
  const on = turnStats.length > 0;
  React.useEffect(() => {
    if (!on) return;
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, [on]);
  if (!on) return undefined;
  const classes: string[] = [];
  const after: React.ReactNode[] = [];
  let title: string | undefined;
  for (const r of turnStats) {
    try {
      const d = r.fn({ ...info, agent, now });
      if (!d) continue;
      if (d.className) classes.push(d.className);
      if (d.title) title = d.title;
      if (d.after != null) after.push(React.createElement(React.Fragment, { key: r.addon }, d.after));
    } catch (e) {
      fail(r.addon, e);
    }
  }
  return { timeClassName: classes.join(" ") || undefined, title, after: after.length ? after : undefined };
}

// JSX in add-ons compiles to these two (internal/addons build: JSXFactory / JSXFragment). Not gerdr.h: the desktop
// shell owns window.gerdr.
(globalThis as any).__gerdrH = React.createElement;
(globalThis as any).__gerdrFragment = React.Fragment;

const tabLabels: Reg<TabLabelFn>[] = [];
const paneTitles: Reg<PaneTitleFn>[] = [];
const wsLabels: Reg<WsLabelFn>[] = [];
const sessionTitles: Reg<SessionTitleFn>[] = [];
export const addonCommands: AddonCommand[] = [];
const listeners: { addon: string; event: string; fn: (e: any) => void }[] = [];
// g.service.on: messages / status changes from an add-on's own backend process
const serviceListeners: { addon: string; event: "message" | "status"; fn: (e: any) => void }[] = [];
const serviceStates = new Map<string, AddonService>();

/** An installed add-on as the backend lists it, plus whether its UI half loaded in this window. */
export type AddonStatus = AddonBuild & { loaded: boolean };
export let addonStatus: AddonStatus[] = [];

const disposers = new Map<string, () => void>();

(globalThis as any).defineAddon = (p: unknown) => p;
(globalThis as any).definePlugin = (p: unknown) => p; // the old name, still in add-ons migrated from plugins

function bump() {
  set((s) => ({ addonVersion: s.addonVersion + 1 }));
}

function remove<T extends { addon: string }>(arr: T[], addon: string) {
  for (let i = arr.length - 1; i >= 0; i--) if (arr[i].addon === addon) arr.splice(i, 1);
}

export function fail(id: string, err: unknown) {
  const msg = err instanceof Error ? `${err.message}` : String(err);
  console.error(`[gerdr addon ${id}]`, err);
  const st = addonStatus.find((p) => p.id === id);
  if (st) st.error = msg;
}

// ---- read-only snapshots handed to add-ons ----

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

/** The read-only views add-ons get of a pane / tab (the paneTitle slot's props). */
export const paneSnapshot = (p: PaneLeaf) => paneInfo(p);
export const tabSnapshot = (t: Tab) => tabInfo(t, tabNameFn(t));

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
      fail(r.addon, e);
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
      fail(r.addon, e);
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
      fail(r.addon, e);
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
      fail(l.addon, e);
    }
  }
}

/** A session changed → add-on events (turnEnd / needsInput / sessionCreated). sync.ts calls it on every push. */
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

// A turn ended → ask add-ons for a session title. Several chain in folder-name order (ctx.title is what the previous
// gave); a name given goes to
// the backend (auto: ignored once a person renamed it; the backend's own naming steps aside for this context, see
// internal/hub/title.go). Every open window runs this: the backend runs the same summarize only once, so the name
// doesn't flip between windows.
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
      fail(r.addon, e);
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

// ---- loading ----

function makeApi(id: string, hash: string) {
  const own = <T>(arr: Reg<T>[], fn: T) => {
    const r = { addon: id, fn };
    arr.push(r);
    bump();
    return () => {
      const i = arr.indexOf(r);
      if (i >= 0) arr.splice(i, 1);
      bump();
    };
  };
  // Kept under the old prefix: what an add-on stored when it was a plugin is still there.
  const storeKey = (k: string) => `gerdr.plugin.${id}.${k}`;
  return {
    version: get().version,
    id,
    asset: (path: string) => assetURL(id, path, hash),
    tabLabel: (fn: TabLabelFn) => own(tabLabels, fn),
    paneTitle: (fn: PaneTitleFn) => own(paneTitles, fn),
    workspaceLabel: (fn: WsLabelFn) => own(wsLabels, fn),
    sessionTitle: (fn: SessionTitleFn) => own(sessionTitles, fn),
    command: (c: Omit<AddonCommand, "addon">) => {
      const cmd = { ...c, addon: id };
      addonCommands.push(cmd);
      bump();
      return () => {
        const i = addonCommands.indexOf(cmd);
        if (i >= 0) addonCommands.splice(i, 1);
        bump();
      };
    },
    on: (event: string, fn: (e: any) => void) => {
      const l = { addon: id, event, fn };
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
        await api.addonMessage(id, data);
      },
      on: (event: "message" | "status", fn: (e: any) => void) => {
        const l = { addon: id, event, fn };
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
      // Drafts are per pane: write into the panes showing this session (the focused one first, usually the only one)
      insertText: (sid: string, text: string) => {
        const l = get().layout;
        const panes = l.workspaces.flatMap((w) => w.tabs.flatMap((t) => leaves(t.root))).filter((p) => p.sessionId === sid);
        const focus = activeTab(l).focus;
        const p = panes.find((x) => x.id === focus) ?? panes[0];
        if (p) set((s) => ({ drafts: { ...s.drafts, [p.id]: (s.drafts[p.id] ?? "") + text } }));
      },
    },
    // React and gerdr's own building blocks, so an add-on's UI looks like the rest without bundling its own copy
    React,
    ui: { Button, Popover, PopoverTrigger, PopoverContent, Tooltip, TooltipTrigger, TooltipContent, Switch, cn },
    icons,
    slot: (name: SlotName, Component: React.ComponentType<any>, o?: { order?: number }) => {
      if (!slots[name]) throw new Error(`没有这个插槽：${name}`);
      const r: SlotReg = { addon: id, key: `${id}:${slots[name].length}:${Math.random().toString(36).slice(2, 6)}`, Component, order: o?.order ?? 0 };
      slots[name].push(r);
      slots[name].sort((a, b) => a.order - b.order || a.addon.localeCompare(b.addon));
      bump();
      return () => {
        const i = slots[name].indexOf(r);
        if (i >= 0) slots[name].splice(i, 1);
        bump();
      };
    },
    composerAction: (a: Omit<ComposerAction, "addon">) => {
      const r = { ...a, addon: id };
      composerActions.push(r);
      bump();
      return () => {
        const i = composerActions.indexOf(r);
        if (i >= 0) composerActions.splice(i, 1);
        bump();
      };
    },
    contextMenu: (m: Omit<MenuItem, "addon">) => {
      const r = { ...m, addon: id };
      menuItems.push(r);
      return () => {
        const i = menuItems.indexOf(r);
        if (i >= 0) menuItems.splice(i, 1);
      };
    },
    turnStat: (fn: TurnStatFn) => own(turnStats, fn),
    // Read on every call: a value changed in Settings → Add-ons reaches the add-on without reloading it
    settings: {
      get: (key: string) => {
        const a = latestBuild.get(id);
        const v = a?.settingValues?.[key];
        return v !== undefined ? v : a?.settings?.find((s) => s.key === key)?.default;
      },
    },
    usage: (force?: boolean) => api.usage(force),
    stats: (days?: number) => api.dayStats(days),
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
  const c = addonCommands.find((x) => x.id === id);
  if (!c) return;
  try {
    await c.run({ state: stateSnapshot() });
  } catch (e) {
    fail(c.addon, e);
    import("./notify").then((n) => n.notify(`扩展 ${c.addon} 出错：${e}`, { level: "error" }));
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
  remove(addonCommands, id);
  remove(listeners, id);
  remove(serviceListeners, id);
  for (const k of Object.keys(slots) as SlotName[]) remove(slots[k], id);
  remove(composerActions, id);
  remove(turnStats, id);
  remove(menuItems, id);
}

function emitService(id: string, event: "message" | "status", e: unknown) {
  for (const l of serviceListeners.filter((x) => x.addon === id && x.event === event)) {
    try {
      l.fn(e);
    } catch (err) {
      fail(id, err);
    }
  }
}

export function serviceStatusChanged(id: string | undefined, st: AddonService | undefined) {
  if (!id || !st) return;
  if (st.state === "stopped") serviceStates.delete(id);
  else serviceStates.set(id, st);
  const p = addonStatus.find((x) => x.id === id);
  if (p) p.service = serviceStates.get(id);
  emitService(id, "status", st);
  bump();
}

export function serviceMessage(id: string | undefined, data: unknown) {
  if (id) emitService(id, "message", data);
}

let loadedHashes = new Map<string, string>();
let latestBuild = new Map<string, AddonBuild>();
let loading: Promise<void> | null = null;

/** The URL of a file in an add-on's folder. The hash makes a reloaded add-on fetch its files again. */
export function assetURL(id: string, path: string, hash = "") {
  const rel = path
    .replace(/^\.?\//, "")
    .split("/")
    .map(encodeURIComponent)
    .join("/");
  return `/api/addons/${encodeURIComponent(id)}/files/${rel}${hash ? `?v=${hash}` : ""}`;
}

export function reloadAddons(): Promise<void> {
  if (loading) return loading.then(() => reloadAddons());
  loading = doReload().finally(() => (loading = null));
  return loading;
}

async function doReload() {
  let builds: AddonBuild[];
  try {
    builds = await api.addons();
  } catch {
    return;
  }
  const seen = new Set<string>();
  const status: AddonStatus[] = [];
  serviceStates.clear();
  for (const b of builds) if (b.service) serviceStates.set(b.id, b.service);
  // Before any setup runs: g.settings.get in setup must already see this build's values
  latestBuild = new Map(builds.map((b) => [b.id, b]));
  for (const b of builds) {
    seen.add(b.id);
    const { code: _code, ...meta } = b;
    if (loadedHashes.get(b.id) === b.hash) {
      const prev = addonStatus.find((p) => p.id === b.id);
      status.push({ ...meta, loaded: prev?.loaded ?? true, error: prev?.error ?? b.error });
      continue;
    }
    unload(b.id);
    loadedHashes.set(b.id, b.hash);
    // No code: switched off, broken, or only a service (addon.json) — nothing to load in the UI
    const st: AddonStatus = { ...meta, loaded: false };
    status.push(st);
    if (b.error || !b.code) continue;
    const url = URL.createObjectURL(new Blob([b.code], { type: "text/javascript" }));
    try {
      const mod = await import(/* @vite-ignore */ url);
      const addon = mod.default ?? mod.addon ?? mod.plugin;
      if (!addon || typeof addon.setup !== "function") throw new Error("扩展要 export default defineAddon({ setup(g) { … } })");
      const d = addon.setup(makeApi(b.id, b.hash));
      if (typeof d === "function") disposers.set(b.id, d);
      st.loaded = true;
    } catch (e) {
      st.error = e instanceof Error ? e.message : String(e);
      console.error(`[gerdr addon ${b.id}]`, e);
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
  addonStatus = status;
  bump();
}
