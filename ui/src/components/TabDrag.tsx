import { SquareTerminal } from "lucide-react";
import { useLayoutEffect, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { mergeTab, movePane, moveTab, moveTabToWorkspace, moveWorkspace, placeRow } from "@/lib/actions";
import { activeWs, allLeaves, type Side, type Workspace } from "@/lib/layout";
import { activeIds, addActive } from "@/lib/overview";
import { get, setLayout } from "@/lib/store";
import { cn } from "@/lib/utils";
import { AgentIcon } from "./AgentIcon";
import { t as tr } from "@/lib/i18n"; // `t` is the drop target below
import type { MessageKey } from "@/lib/messages";

/*
 * tab 栏的拖拽：排序 + 合并 + 挪到别的工作空间，一个手势里按松手的位置分：
 *
 * - 在 tab 栏里，落在两个 tab 之间（或某个 tab 左右四分之一）→ 排序，那条缝上亮一根竖线；
 * - 落在别的 tab 正中间那一半 → 整个并进那个 tab（放右边），那个 tab 圈一圈；停住 0.6 秒它就切出来给你看，
 *   接着往下拖进它的某个 pane，就能挑贴在哪一边；
 * - 拖到下面的 pane 上 → 贴到这个 pane 离指针最近的那一边，那半边铺一层预览（和 VS Code 拖编辑器一样）；
 * - 拖到侧栏的别的工作空间上 → 挪过去。
 * - 侧栏（UnifiedSidebar）里拖一行，落在某个分组的两行之间 → 这一行列到那儿（layout.sideRows），那条缝上亮一根横线；
 *   落在分组标题上 → 列到那一组的最后。只改侧栏怎么列，屏幕上的 tab、分屏都不动。
 * - Anything carrying one agent (a sidebar row, a pane header, a tab of one pane) dropped on the sidebar's "active"
 *   section joins it (overview.addActive); nothing moves. While such a drag runs the section shows even when empty.
 * - A sidebar group's title dragged between two groups reorders the workspaces (startWsDrag).
 *
 * 不用 HTML5 原生拖拽：它的拖影是浏览器截的半透明图、拖动中改不了样子，也没法在 pane 区域上算「贴哪边」
 * （dragover 进 xterm / 对话这些子元素会一路被打断）。这里用 pointer 事件 + setPointerCapture 自己画。
 * 被拖的 tab 原地留一个淡的占位，别的 tab 不让位 —— 让位的话指针下面的东西一直在动，落点跟着跳。
 * 松手排完序再用 FLIP 把挪动的 tab 滑过去，被拖的那个从拖影的位置滑进新位置。
 *
 * 合并不会丢东西：会话、终端原样搬过去；拆回来用 pane 菜单里的「移到新 tab」。
 *
 * pane 的标题栏也能拖（startPaneDrag），落点一样，只是搬的是这一个 pane：贴到别的 pane（同一个 tab 里也行）的某一边、
 * 拖进 tab 栏的缝里单独成一个新 tab、拖到别的 tab 上并进去、拖到别的工作空间成为那边的新 tab。
 * tab 里只有这一个 pane 时就当拖整个 tab。两种拖法共用一份状态（模块级），拖影统一由 tab 栏里的 TabDragLayer 画。
 */

type Target =
  /** Insert before the `to`-th tab of wsId (indices after taking the dragged tab out); `line` is the gap to light up. */
  | { kind: "reorder"; wsId: string; to: number; line: Box }
  /** A sidebar row listed in group wsId before its `to`-th row (counted without it): the list only (actions.placeRow). */
  | { kind: "row"; wsId: string; pane: string; to: number; line: Box }
  | { kind: "tab"; tabId: string; rect: Box }
  | { kind: "pane"; tabId: string; paneId: string; side: Side; rect: Box }
  | { kind: "ws"; wsId: string; rect: Box }
  /** The sidebar's "active" section (UnifiedSidebar, data-active-drop): the dragged agent joins it, nothing moves. */
  | { kind: "active"; session: string; rect: Box }
  /** A sidebar group (workspace) put before the `to`-th group of its session, counted without it (actions.moveWorkspace). */
  | { kind: "wsorder"; to: number; line: Box };

type Box = { left: number; top: number; width: number; height: number };

export type TabGhost = { label: string; agent?: string; term?: boolean };

type Drag = {
  tabId: string;
  /** 拖的是 tab 栏里的第几个；拖 pane 时是 -1 */
  from: number;
  /** 拖的是 pane 标题栏时，是哪个 pane */
  paneId?: string;
  x: number;
  y: number;
  offX: number;
  offY: number;
  w: number;
  h: number;
  ghost: TabGhost;
  /** The agent being dragged (a lone pane, or a tab of one pane): only then can it be dropped into "active". */
  session?: string;
  target: Target | null;
  spring?: string;
};

const SPRING_MS = 600;
const GAP = 4; // tab 之间的 gap-1

const box = (r: DOMRect): Box => ({ left: r.left, top: r.top, width: r.width, height: r.height });

// 拖动状态放模块级：tab 栏和 pane 标题栏都能开始拖，拖影 / 预览只在 tab 栏那儿画一份
let current: Drag | null = null;
const subs = new Set<() => void>();
const setDrag = (d: Drag | null) => {
  current = d;
  for (const f of subs) f();
};
const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));
export const useDrag = () => useSyncExternalStore(subscribe, () => current);

// FLIP：松手时记下各 tab 的位置（被拖的那个记拖影的位置），重排后 tab 栏从旧位置滑到新位置
let flipPending: Map<string, number> | null = null;
const tabBar = () => document.querySelector<HTMLElement>("[data-tab-bar]");

export function useTabDrag(ws: Workspace, bar: React.RefObject<HTMLDivElement | null>) {
  const drag = useDrag();
  const order = ws.tabs.map((t) => t.id).join(",");

  useLayoutEffect(() => {
    const prev = flipPending;
    flipPending = null;
    if (!prev || !bar.current) return;
    for (const el of bar.current.querySelectorAll<HTMLElement>("[data-tab-id]")) {
      const old = prev.get(el.dataset.tabId!);
      if (old == null) continue;
      const dx = old - el.getBoundingClientRect().left;
      if (Math.abs(dx) < 1) continue;
      el.animate([{ transform: `translateX(${dx}px)` }, { transform: "none" }], { duration: 200, easing: "cubic-bezier(0.2, 0.8, 0.2, 1)" });
    }
  }, [order]);

  return {
    drag,
    onPointerDown: (e: React.PointerEvent<HTMLElement>, tabId: string, from: number, ghost: TabGhost) => startDrag(e, tabId, from, undefined, ghost),
  };
}

/** 拖 pane 标题栏。tab 里只有它一个就当拖整个 tab。 */
export function startPaneDrag(e: React.PointerEvent<HTMLElement>, paneId: string, ghost: TabGhost) {
  const owner = allLeaves(get().layout).find((x) => x.pane.id === paneId);
  if (!owner) return;
  const only = owner.tab.root.type === "pane";
  startDrag(e, owner.tab.id, only ? owner.ws.tabs.indexOf(owner.tab) : -1, only ? undefined : paneId, ghost);
}

function startDrag(e: React.PointerEvent<HTMLElement>, tabId: string, from: number, paneId: string | undefined, ghost: TabGhost) {
  // 只认鼠标 / 触控笔左键；手指在 tab 栏上划是横向滚动，不抢。点按钮（关闭、⋯ 菜单）不算拖。
  if (e.button !== 0 || e.pointerType === "touch" || (e.target as HTMLElement).closest("button")) return;
  const ws = activeWs(get().layout);
  const el = e.currentTarget;
  const r = el.getBoundingClientRect();
  const header = !el.dataset.tabId; // 从 pane 标题栏开始拖的（不是 tab 栏）
  const sx = e.clientX;
  const sy = e.clientY;
  const pid = e.pointerId;
  const origActive = ws.activeTab;
  let d: Drag | null = null;
  let springTimer: ReturnType<typeof setTimeout> | undefined;
  let shown = origActive; // 现在显示着的 tab（停住切过去之后就变了）

  const update = (next: Drag | null) => {
    d = next;
    setDrag(next);
  };
  // 拖 pane 时：自己并不进自己的 tab、贴不到自己身上
  const self = (tab: string, pane?: string) => (paneId ? pane === paneId || (!pane && tab === tabId) : tab === tabId);

  const showTab = (id: string) => setLayout((l) => ({ ...l, workspaces: l.workspaces.map((w) => (w.id === ws.id ? { ...w, activeTab: id } : w)) }));

  // The workspace the dragged tab / pane lives in: dropping on its own workspace moves nothing. Not always the active one:
  // the desktop sidebar lists every workspace's tabs, so a drag can start in another.
  const srcWs = get().layout.workspaces.find((w) => w.tabs.some((t) => t.id === tabId))?.id ?? ws.id;
  // Started on a sidebar row: the pane it lists. Dropped in the sidebar it only moves in the list (actions.placeRow)
  const sideRow = el.closest<HTMLElement>("[data-side-row]")?.dataset.sideRow;
  // The agent it carries: a pane, or a tab holding just one pane (a split tab is several agents, none of them "the" one)
  const srcTab = allLeaves(get().layout).filter((x) => x.tab.id === tabId);
  const session = (paneId ? srcTab.find((x) => x.pane.id === paneId) : srcTab.length === 1 ? srcTab[0] : undefined)?.pane.sessionId;

  const locate = (x: number, y: number): Target | null => {
    // The tab bar, when it's shown (the desktop sidebar hidden, or the old layout)
    const b = tabBar();
    const br = b?.getBoundingClientRect();
    if (b && br && y < br.bottom + 2) {
      const els = [...b.querySelectorAll<HTMLElement>("[data-tab-id]")];
      if (!els.length) return null;
      const rs = els.map((x) => x.getBoundingClientRect());
      let k = rs.findIndex((r) => x < r.right + GAP / 2);
      if (k < 0) k = rs.length - 1;
      const r = rs[k];
      const f = (x - r.left) / r.width;
      const over = els[k].dataset.tabId!;
      if (f > 0.25 && f < 0.75) {
        if (!self(over)) return { kind: "tab", tabId: over, rect: box(r) };
        if (!paneId) return null; // 拖 tab 时自己身上 = 原地
      }
      const ins = f < 0.5 ? k : k + 1; // 插到第 ins 个前面（按原来的下标）
      if (from >= 0 && (ins === from || ins === from + 1)) return null; // 原地
      const lx = ins < rs.length ? rs[ins].left - GAP / 2 : rs[rs.length - 1].right + GAP / 2;
      // 拖 pane 时 from = -1：to 就是插入的位置
      return {
        kind: "reorder",
        wsId: ws.id,
        to: from >= 0 && ins > from ? ins - 1 : ins,
        line: { left: lx - 1, top: r.top + 2, width: 2, height: r.height - 4 },
      };
    }
    const hit = document.elementFromPoint(x, y) as HTMLElement | null;
    // Onto the sidebar's "active" section: the agent joins it (already in: nothing to do)
    const act = hit?.closest<HTMLElement>("[data-active-drop]");
    if (act) return session && !activeIds(get()).includes(session) ? { kind: "active", session, rect: box(act.getBoundingClientRect()) } : null;
    // A sidebar row dragged between two others (UnifiedSidebar): list it there, in this group or another one
    const list = sideRow ? hit?.closest<HTMLElement>("[data-side-rows]") : null;
    const rows = list ? [...list.querySelectorAll<HTMLElement>("[data-side-row]")] : [];
    if (sideRow && list && rows.length) {
      const rs = rows.map((x) => x.getBoundingClientRect());
      let ins = rs.findIndex((r) => y < r.top + r.height / 2);
      if (ins < 0) ins = rs.length;
      const own = rows.findIndex((x) => x.dataset.sideRow === sideRow);
      if (own >= 0 && (ins === own || ins === own + 1)) return null;
      const ly = ins < rs.length ? rs[ins].top : rs[rs.length - 1].bottom;
      const lr = list.getBoundingClientRect();
      const to = own >= 0 && ins > own ? ins - 1 : ins;
      return { kind: "row", wsId: list.dataset.sideRows!, pane: sideRow, to, line: { left: lr.left + 2, top: ly - 1, width: lr.width - 4, height: 2 } };
    }
    const wsEl = hit?.closest<HTMLElement>("[data-ws-id]");
    // A sidebar row onto a group's title: listed at its end (any group, its own included); a tab moves to another workspace
    if (wsEl) return wsEl.dataset.wsId === srcWs && !sideRow ? null : { kind: "ws", wsId: wsEl.dataset.wsId!, rect: box(wsEl.getBoundingClientRect()) };
    const paneEl = hit?.closest<HTMLElement>("[data-pane-id]");
    if (!paneEl) return null;
    const overPane = paneEl.dataset.paneId!;
    const owner = allLeaves(get().layout).find((x) => x.pane.id === overPane);
    if (!owner || (paneId ? overPane === paneId : owner.tab.id === tabId)) return null; // 自己并不进自己
    const pr = paneEl.getBoundingClientRect();
    const fx = (x - pr.left) / pr.width;
    const fy = (y - pr.top) / pr.height;
    const side = (
      [
        ["left", fx],
        ["right", 1 - fx],
        ["up", fy],
        ["down", 1 - fy],
      ] as [Side, number][]
    ).sort((a, b) => a[1] - b[1])[0][0];
    const half = { ...box(pr) };
    if (side === "left" || side === "right") half.width /= 2;
    else half.height /= 2;
    if (side === "right") half.left += half.width;
    if (side === "down") half.top += half.height;
    return { kind: "pane", tabId: owner.tab.id, paneId: overPane, side, rect: half };
  };

  const move = (ev: PointerEvent) => {
    if (ev.pointerId !== pid) return;
    if (!d) {
      if (Math.hypot(ev.clientX - sx, ev.clientY - sy) < 5) return;
      try {
        el.setPointerCapture(pid);
      } catch {}
      document.body.style.cursor = "grabbing";
      // pane 标题栏里有字，拖的时候别顺手选中一片
      document.body.style.userSelect = "none";
      window.getSelection()?.removeAllRanges();
      // 从 pane 标题栏拖时拖影是 tab 大小的胶囊（不是整条标题栏），指针捏在它中间
      const w = header ? Math.min(r.width, 208) : r.width;
      const h = header ? 28 : r.height;
      const off = header ? { offX: w / 2, offY: h / 2 } : { offX: sx - r.left, offY: sy - r.top };
      d = { tabId, from, paneId, x: sx, y: sy, ...off, w, h, ghost, session, target: null };
    }
    const target = locate(ev.clientX, ev.clientY);
    // 停在别的 tab 中间：过一会儿把它切出来，好接着往下拖进它的 pane
    const springOn = target?.kind === "tab" && target.tabId !== shown ? target.tabId : undefined;
    if (springOn !== d.spring) {
      clearTimeout(springTimer);
      if (springOn)
        springTimer = setTimeout(() => {
          showTab(springOn);
          shown = springOn;
          if (d) update({ ...d, spring: undefined });
        }, SPRING_MS);
    }
    update({ ...d, x: ev.clientX, y: ev.clientY, target, spring: springOn });
  };

  const finish = (commit: boolean) => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
    window.removeEventListener("pointercancel", cancel);
    window.removeEventListener("keydown", key, true);
    clearTimeout(springTimer);
    if (!d) return;
    document.body.style.cursor = "";
    document.body.style.userSelect = "";
    try {
      el.releasePointerCapture(pid);
    } catch {}
    // 拖完松手时浏览器还会补一个 click（同一个 tab 上按下又松开），别让它再切一次 tab
    const eat = (ev: MouseEvent) => (ev.stopPropagation(), ev.preventDefault());
    window.addEventListener("click", eat, true);
    setTimeout(() => window.removeEventListener("click", eat, true), 0);
    const t0 = commit ? d.target : null;
    const t = t0?.kind === "wsorder" ? null : t0; // only a group drag (startWsDrag) has those
    const restore = () => shown !== origActive && get().layout.workspaces.some((w) => w.tabs.some((x) => x.id === origActive)) && showTab(origActive);
    const b = tabBar();
    if (t && b) {
      const m = new Map<string, number>();
      for (const x of b.querySelectorAll<HTMLElement>("[data-tab-id]")) m.set(x.dataset.tabId!, x.getBoundingClientRect().left);
      if (!paneId) m.set(tabId, d.x - d.offX);
      flipPending = m;
    }
    if (!t) restore();
    else if (t.kind === "active") (restore(), addActive([t.session]));
    else if (t.kind === "row") placeRow(t.pane, t.wsId, t.to);
    else if (t.kind === "ws" && sideRow) placeRow(sideRow, t.wsId, Infinity);
    else if (paneId) {
      if (t.kind === "reorder") (restore(), movePane(paneId, { wsId: t.wsId, index: t.to }));
      else if (t.kind === "tab") movePane(paneId, { tabId: t.tabId, side: "right" });
      else if (t.kind === "pane") movePane(paneId, { tabId: t.tabId, paneId: t.paneId, side: t.side });
      else movePane(paneId, { wsId: t.wsId });
    } else if (t.kind === "reorder") {
      restore();
      if (t.wsId === srcWs) moveTab(srcWs, from, t.to);
      else moveTabToWorkspace(tabId, t.wsId, t.to);
    } else if (t.kind === "tab") mergeTab(tabId, t.tabId, undefined, "right");
    else if (t.kind === "pane") mergeTab(tabId, t.tabId, t.paneId, t.side);
    else moveTabToWorkspace(tabId, t.wsId);
    update(null);
  };
  const up = (ev: PointerEvent) => ev.pointerId === pid && finish(true);
  const cancel = (ev: PointerEvent) => ev.pointerId === pid && finish(false);
  const key = (ev: KeyboardEvent) => {
    if (ev.key !== "Escape" || !d) return;
    ev.preventDefault();
    ev.stopPropagation();
    finish(false);
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
  window.addEventListener("pointercancel", cancel);
  window.addEventListener("keydown", key, true);
}

/**
 * Dragging a sidebar group by its title reorders the workspaces of the session (UnifiedSidebar marks each group with
 * data-ws-group). The drop point is the gap nearest the pointer, by each group's vertical middle; a line lights up there.
 * It shares the drag state and TabDragLayer with tab / pane drags, but nothing else: a group can't be merged or split.
 */
export function startWsDrag(e: React.PointerEvent<HTMLElement>, wsId: string, label: string) {
  if (e.button !== 0 || e.pointerType === "touch" || (e.target as HTMLElement).closest("button")) return;
  const el = e.currentTarget;
  const r = el.getBoundingClientRect();
  // Only the groups of the sidebar the drag started in (the phone drawer may hold another copy)
  const scope = el.closest<HTMLElement>("[data-ws-groups]");
  if (!scope) return;
  const sx = e.clientX;
  const sy = e.clientY;
  const pid = e.pointerId;
  let d: Drag | null = null;

  const locate = (y: number): Target | null => {
    const els = [...scope.querySelectorAll<HTMLElement>("[data-ws-group]")];
    const own = els.findIndex((x) => x.dataset.wsGroup === wsId);
    if (own < 0 || els.length < 2) return null;
    const rs = els.map((x) => x.getBoundingClientRect());
    let ins = rs.findIndex((x) => y < x.top + x.height / 2);
    if (ins < 0) ins = rs.length;
    if (ins === own || ins === own + 1) return null;
    // In the gap above the group (the groups are mt-1 apart), or just under the last one
    const ly = ins < rs.length ? rs[ins].top - 2 : rs[rs.length - 1].bottom + 1;
    return { kind: "wsorder", to: ins > own ? ins - 1 : ins, line: { left: r.left + 2, top: ly - 1, width: r.width - 4, height: 2 } };
  };

  const move = (ev: PointerEvent) => {
    if (ev.pointerId !== pid) return;
    if (!d) {
      if (Math.hypot(ev.clientX - sx, ev.clientY - sy) < 5) return;
      try {
        el.setPointerCapture(pid);
      } catch {}
      document.body.style.cursor = "grabbing";
      document.body.style.userSelect = "none";
      d = { tabId: "", from: -1, x: sx, y: sy, offX: sx - r.left, offY: sy - r.top, w: r.width, h: r.height, ghost: { label }, target: null };
    }
    d = { ...d, x: ev.clientX, y: ev.clientY, target: locate(ev.clientY) };
    setDrag(d);
  };
  const finish = (commit: boolean) => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
    window.removeEventListener("pointercancel", cancel);
    window.removeEventListener("keydown", key, true);
    if (!d) return;
    document.body.style.cursor = "";
    document.body.style.userSelect = "";
    try {
      el.releasePointerCapture(pid);
    } catch {}
    // The click the browser adds after the drag would fold / unfold the group
    const eat = (ev: MouseEvent) => (ev.stopPropagation(), ev.preventDefault());
    window.addEventListener("click", eat, true);
    setTimeout(() => window.removeEventListener("click", eat, true), 0);
    const t = commit ? d.target : null;
    if (t?.kind === "wsorder") moveWorkspace(wsId, t.to);
    d = null;
    setDrag(null);
  };
  const up = (ev: PointerEvent) => ev.pointerId === pid && finish(true);
  const cancel = (ev: PointerEvent) => ev.pointerId === pid && finish(false);
  const key = (ev: KeyboardEvent) => {
    if (ev.key !== "Escape" || !d) return;
    ev.preventDefault();
    ev.stopPropagation();
    finish(false);
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
  window.addEventListener("pointercancel", cancel);
  window.addEventListener("keydown", key, true);
}

const SIDE_TEXT: Record<Side, MessageKey> = { left: "tabDrag.left", right: "tabDrag.right", up: "tabDrag.up", down: "tabDrag.down" };

/** 拖动中画在最上层的东西：跟着指针的 tab、排序的竖线、合并 / 分屏的预览。全都不接指针事件。 */
export function TabDragLayer() {
  const drag = useDrag();
  if (!drag) return null;
  const t = drag.target;
  // Reordering in the tab bar keeps the full tab under the pointer; anything else, sidebar rows included, shrinks it to a
  // pill beside the pointer so it doesn't hide where it lands
  const compact = !!t && t.kind !== "reorder";
  const fixed = (b: Box): React.CSSProperties => ({ left: b.left, top: b.top, width: b.width, height: b.height });
  return createPortal(
    <div className="pointer-events-none fixed inset-0 z-[100]">
      {t?.kind === "tab" && (
        <div className="fixed overflow-hidden rounded-md bg-brand/10 ring-2 ring-brand" style={fixed(t.rect)}>
          {/* 停住多久会切过去：底下一条走满 */}
          {drag.spring === t.tabId && (
            <div key={t.tabId} className="absolute bottom-0 left-0 h-0.5 bg-brand" style={{ animation: `gerdr-spring ${SPRING_MS}ms linear forwards` }} />
          )}
        </div>
      )}
      {t?.kind === "pane" && (
        <div
          className="fixed flex items-center justify-center rounded-md border-2 border-brand bg-brand/15 transition-all duration-150 ease-out"
          style={fixed(t.rect)}
        >
          <span className="rounded-md bg-background/90 px-2 py-1 text-[12px] text-foreground shadow-sm">{tr(SIDE_TEXT[t.side])}</span>
        </div>
      )}
      {t?.kind === "active" && <div className="fixed rounded-md bg-brand/10 ring-2 ring-brand" style={fixed(t.rect)} />}
      {t?.kind === "ws" && <div className="fixed rounded-md bg-brand/10 ring-2 ring-brand" style={fixed(t.rect)} />}
      {/* 拖影：在 tab 栏里排序时就是这个 tab 本身（不透明，盖住下面的字）；要合并 / 分屏 / 挪走时缩成指针右下角的小胶囊，
          不然它正好盖住要落的那个 tab / 那半边 pane，看不清落在哪 */}
      <div
        className={cn(
          "fixed flex items-center gap-2 rounded-md bg-popover px-1.5 text-[12px] font-medium text-popover-foreground shadow-lg ring-1 ring-border",
          compact && "max-w-56 shadow-xl",
        )}
        style={
          compact
            ? { left: drag.x + 12, top: drag.y + 14, height: drag.h }
            : { left: drag.x - drag.offX, top: drag.y - drag.offY, width: drag.w, height: drag.h }
        }
      >
        {drag.ghost.agent ? (
          <AgentIcon agent={drag.ghost.agent} className="size-4" />
        ) : (
          drag.ghost.term && <SquareTerminal className="size-4 shrink-0 text-muted-foreground" />
        )}
        <span className="min-w-0 flex-1 truncate">{drag.ghost.label}</span>
      </div>
      {/* 排序的落点竖线画在拖影上面：拖影跟着指针，常常正好压在那条缝上 */}
      {(t?.kind === "reorder" || t?.kind === "row" || t?.kind === "wsorder") && (
        <div className="fixed rounded-full bg-brand shadow-[0_0_0_1px_var(--background)]" style={fixed(t.line)} />
      )}
      <style>{`@keyframes gerdr-spring { from { width: 0 } to { width: 100% } }`}</style>
    </div>,
    document.body,
  );
}
