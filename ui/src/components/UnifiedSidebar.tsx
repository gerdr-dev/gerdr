import {
  ArrowDown,
  ArrowUp,
  Bell,
  BellOff,
  ChevronRight,
  CircleX,
  Columns2,
  Ellipsis,
  Eraser,
  ListPlus,
  FileText,
  FolderOpen,
  GalleryHorizontalEnd,
  MailCheck,
  Pencil,
  Pin,
  PinOff,
  Plus,
  Search,
  SquareDashed,
  Globe,
  SquareTerminal,
  SquareSplitHorizontal,
  X,
  ArrowDownWideNarrow,
  ArrowUpToLine,
  Orbit,
} from "lucide-react";
import { createContext, memo, useContext, useEffect, useRef, useState } from "react";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  addTab,
  basename,
  closePaneById,
  closeTab,
  closeWorkspace,
  focusPane,
  mergeTab,
  movePane,
  moveWorkspace,
  moveWorkspaceToSpace,
  muteWorkspace,
  openTerminal,
  paneToTab,
  pinRow,
  selectWorkspace,
  tabName,
} from "@/lib/actions";
import { applyWorkspaceLabel } from "@/lib/addons";
import {
  activeWs,
  allLeaves,
  currentSpace,
  leaves,
  locateSession,
  sideRows,
  spaceList,
  spaceOf,
  spaceWorkspaces,
  type Layout,
  type PaneLeaf,
  type SideRow,
  type Tab,
  type Workspace,
} from "@/lib/layout";
import {
  activeAllProjects,
  addActive,
  clearActive,
  doneActiveIds,
  restoreWorking,
  workingOutIds,
  focusTile,
  MAX_TILES,
  removeActive,
  setActiveAllProjects,
  toggleOverview,
  useActiveIds,
  useOverviewIds,
} from "@/lib/overview";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { codeHost, get, markRead, set, useStore } from "@/lib/store";
import { showInCodeView } from "@/core/CodeView";
import { codeMenuItems } from "@/lib/codeMenu";
import { whenCaughtUp } from "@/lib/catchup";
import { frozenOrder, useModFrozen, useModShown } from "@/lib/modHeld";
import { BorderFlow, useSlowCatchUp } from "./BorderFlow";
import type { SessionSummary } from "@/lib/types";
import { cn } from "@/lib/utils";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";
import { HostTag } from "./HostTag";
import { Elapsed } from "./Elapsed";
import { ListHeader, SidebarBottom, SidebarEdge, SidebarHeader } from "./SidebarChrome";
import { WsDot } from "./WsDot";
import { TabDragLayer, startPaneDrag, startWsDrag, useDrag } from "./TabDrag";
import { t } from "@/lib/i18n";
import { useWorkProject } from "@/lib/work";
import { usePhone } from "@/lib/phone";
import { usePanelPhoneMenu } from "@/core/phoneMenu";
import { PanelPhoneMenu } from "./PhoneMenu";

/*
 * The sidebar (on a phone, the drawer). One list in place of four things that each listed the same agents:
 * the workspace switcher, the tab bar, the agents list and the to-do view.
 *
 * - Every workspace of the project is a group, all shown at once (no switching), each collapsible. A group lists one row
 *   per pane, each on its own: panes split together on screen are not tied together here. Which group a row sits in is
 *   the sidebar's own (Workspace.rows / pins, lib/layout.ts sideRows); dragging it never touches the split on screen.
 *   In a group, rows kept at the top (pins: dragged there, or the row menu) come first, the rest by latest activity.
 *   Panes shown next to the current one are tinted lightly.
 * - "Active" above the groups replaces both the to-do list and the agents list. Who's in is overviewState.activeIds,
 *   shared with the overview: what needs you or runs joins on its own, anything else can be added by
 *   hand; flat in the agents list's order. Each project chooses (the section's ⋯ menu) whether it lists every project's
 *   agents. Its grid (the overview's, lib/overview.ts) opens over the main area next to this list. The groups below still
 *   hold everything, so these rows show twice: the top is where to look, the groups are where things live.
 *
 * Kept out of SidebarChrome.tsx so the experiment can be dropped (or promoted) by touching a few lines in App.tsx.
 */

/** The slot element, kept in the store for core CodeView to portal into; cleared when the list unmounts. */
const setCodeSlot = (el: HTMLDivElement | null) => {
  if (get().codeSlot !== el) set({ codeSlot: el });
};

const FOLD_KEY = "gerdr.unified.folded";
const ACTIVE_FOLD_KEY = "gerdr.unified.activeFolded";

/** A small per-device setting kept in localStorage (like the folded groups). */
function useLocal<T>(key: string, init: T) {
  const [v, setV] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(key);
      return raw == null ? init : (JSON.parse(raw) as T);
    } catch {
      return init;
    }
  });
  const save = (next: T) => {
    setV(next);
    try {
      localStorage.setItem(key, JSON.stringify(next));
    } catch {}
  };
  return [v, save] as const;
}

function loadFolded(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(FOLD_KEY) ?? "[]");
    return Array.isArray(v) ? v.filter((x) => typeof x === "string") : [];
  } catch {
    return [];
  }
}

/** Collapsed groups, per device (like the sidebar width): which ones you fold depends on the screen. */
function useFolded() {
  const [folded, setFolded] = useState<string[]>(loadFolded);
  const save = (next: string[]) => {
    setFolded(next);
    try {
      localStorage.setItem(FOLD_KEY, JSON.stringify(next));
    } catch {}
  };
  return [folded, save] as const;
}

type Item = { ws: Workspace; tab: Tab; pane: PaneLeaf; split: boolean; s?: SessionSummary };

/** Stuck on you: asking or failed. Finished-and-unread is not counted, it isn't waiting on anything. */
const needsYou = (s: SessionSummary | undefined) => !!s && (s.status === "waiting" || s.status === "error");

function hostOf(u: string) {
  try {
    return new URL(u).host || u;
  } catch {
    return u;
  }
}

/** What a row shows for a pane: an agent, a terminal, a file, a subagent, a browser, or a new conversation not started yet. */
function paneLabel(p: PaneLeaf, s: SessionSummary | undefined): { title: string; sub: string } {
  if (s) return { title: s.title || basename(s.cwd), sub: basename(s.cwd) };
  if (p.term) return { title: t("unifiedSidebar.terminal"), sub: basename(p.term.cwd) };
  if (p.file?.folder) return { title: p.file.session ? t("folder.open") : basename(p.file.path), sub: basename(p.file.path) };
  if (p.file) return { title: basename(p.file.path), sub: t("unifiedSidebar.file") };
  if (p.sub) return { title: p.sub.title || t("unifiedSidebar.subagent"), sub: t("unifiedSidebar.subagent") };
  if (p.browser) return { title: p.browser.url ? hostOf(p.browser.url) : t("browser.tag"), sub: t("browser.tag") };
  return { title: t("unifiedSidebar.newSession"), sub: p.draft?.cwd ? basename(p.draft.cwd) : "" };
}

/**
 * The phone drawer's rows are roomier than the desktop list's: two lines (where it is, how long ago) and at least 44px
 * tall, a finger's minimum. They took that from the phone's old flat pane list (PaneSwitcher), dropped because it listed
 * the same panes as this drawer. `now` drives the "3m" column.
 */
const Roomy = createContext<{ now: number } | null>(null);

/** How long since a session last moved, compact (`3m` `2h` `4d`): the column is a few dozen pixels on a phone. */
function ago(ms: number | undefined, now: number) {
  if (!ms) return "";
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 45) return t("nav.justNow");
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.round(h / 24)}d`;
}

/** Go to a pane: the pin grid would cover it, so that closes first. */
function goPane(paneId: string, sessionId?: string) {
  toggleOverview(false);
  focusPane(paneId);
  if (sessionId) markRead(sessionId);
}

/**
 * Drop the click the browser synthesizes after a tap we already handled on `pointerup`. That click is hit-tested
 * after pointerup, and by then the pick may have closed the drawer (tapping the row you're already on closes it at
 * once), so it landed on whatever was under the finger behind it: an image in the chat opened the lightbox. It follows
 * touchend within a few ms; the timeout only stops a tap that never produces a click from eating a later, real one.
 */
function eatNextClick() {
  const eat = (e: MouseEvent) => (e.preventDefault(), e.stopPropagation(), off());
  const off = () => (window.removeEventListener("click", eat, true), clearTimeout(t));
  window.addEventListener("click", eat, true);
  const t = setTimeout(off, 500);
}

/** The clock the roomy rows' "3m" column goes by, ticking every 15s while `on` (minutes are its smallest step). */
function useNow(on: boolean) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => setNow(Date.now()), 15000);
    return () => clearInterval(t);
  }, [on]);
  return now;
}

/**
 * Code's row for one pane, on its own (core CodeRow): the part every list is made of — the frame's search results, a
 * panel's own grouping (CodeView list="replace"). The same Row CodeList draws in its groups — icon, status, unread,
 * the menu, dragging it onto a pane — minus what belongs to a workspace group (pinning, its place in the group).
 * `where`: shown at its end instead of the folder.
 */
export function PaneRow({ pane: id, where }: { pane: string; where?: string }) {
  const layout = useStore((s) => s.layout);
  const loc = allLeaves(layout).find((x) => x.pane.id === id);
  const sid = loc?.pane.sessionId;
  const s = useStore((st) => (sid ? st.sessions[sid] : undefined));
  const unread = useStore((st) => !!(sid && st.unread[sid]));
  const inActive = useActiveIds()
    .split(",")
    .includes(sid ?? "\0");
  // The phone's grid covers the panes: no row is "the one in front" then (as in CodeList)
  const phone = usePhone();
  const gridOn = useStore((st) => st.overview.on) && phone;
  // A phone's drawer has roomier rows (Roomy), said by the frame through context. A panel's part of the list is
  // another React root portaled in, which that context doesn't reach: its rows came out in the desktop's size, 28px
  // tall under a finger and without the "3m" column. So a row not told says it itself on a phone.
  const told = useContext(Roomy);
  const now = useNow(!told && phone);
  const own = !told && phone ? { now } : null;
  if (!loc) return null;
  const cur = activeWs(layout);
  const here = !gridOn && loc.ws.id === cur.id && loc.tab.id === cur.activeTab;
  const inSplit = leaves(loc.tab.root).length > 1;
  const row = (
    <Row
      tab={loc.tab}
      pane={loc.pane}
      s={s}
      inSplit={inSplit}
      unread={unread}
      inActive={inActive}
      current={here && (!inSplit || loc.tab.focus === id)}
      visible={here && inSplit}
      inActiveTab={loc.tab.id === cur.activeTab}
      where={where}
    />
  );
  return own ? <Roomy.Provider value={own}>{row}</Roomy.Provider> : row;
}

/** A workspace's name as a row says it out of its group: with its project's when that isn't the one on screen. */
function wsLabelOf(layout: Layout, ws: Workspace) {
  const here = currentSpace(layout);
  const i = spaceWorkspaces(layout).indexOf(ws);
  const label = i >= 0 ? applyWorkspaceLabel(ws, i, ws.name) : ws.name;
  return spaceOf(ws) === here ? label : `${spaceList(layout).find((sp) => sp.id === spaceOf(ws))?.name ?? spaceOf(ws)} · ${label}`;
}

/**
 * The list beside Code's panes, as a frame: the header, the phone drawer's search, the scrolling area and the buttons
 * under it. What is listed is a part put into it, and Code has no special place there: Code mode puts in CodeList
 * (Code's default: "active" and the workspaces); a panel that hosts Code (core CodeView) puts in its own part, above
 * CodeList (list="add") or instead of it (list="replace": its own grouping of the same panes, drawn with PaneRow).
 *
 * `className`: the phone drawer, which sets its own width (no resize edge there).
 */
export function UnifiedSidebar({ className }: { className?: string }) {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const sidebarW = useStore((s) => s.sidebarW);
  const phone = usePhone();
  // Unconditional for the same reason as App's overviewOn: a skipped hook crashes when `phone` flips.
  const overviewOn = useStore((s) => s.overview.on);
  const gridOn = phone && overviewOn;
  const roomy = !!className;
  // A panel on screen in the phone drawer: its own items (core/phoneMenu.ts) instead of Code's tabs, when it gives any
  const mode = useStore((s) => s.mode);
  const workKey = useWorkProject();
  const host = useStore((s) => codeHost(s));
  const hosted = !!host;
  // The hosting panel draws the whole list (core CodeView list="replace"): Code's default list isn't put in
  const replaced = host?.list === "replace";
  const panelMenu = usePanelPhoneMenu(roomy && mode === "work" && !hosted ? workKey : null);
  // The drawer's search: not kept, it's "what am I looking for right now" (the drawer remounts on every open)
  const [q, setQ] = useState("");
  // The "3m" column's clock: minutes are its smallest step, a tick every 15s is plenty (statuses come over the socket)
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!roomy) return;
    const t = setInterval(() => setNow(Date.now()), 15000);
    return () => clearInterval(t);
  }, [roomy]);
  const cur = activeWs(layout);
  const curTab = cur.tabs.find((t) => t.id === cur.activeTab);
  const scroller = useRef<HTMLDivElement>(null);

  // Keep the row you're on in sight when it changes from elsewhere (⌘1–9, next-needs-you). A running tab is drawn twice
  // (under "active" and in its group): if either copy is already in view, stay put, or clicking the group's copy jumped
  // the list up to the "active" one. Only when none is visible, scroll to the copy closest to where the list is.
  // Whatever part drew the rows: it goes by data-row-current, which every row (Row) carries.
  useEffect(() => {
    const box = scroller.current;
    if (!box) return;
    const rows = [...box.querySelectorAll<HTMLElement>("[data-row-current]")];
    if (!rows.length) return;
    const view = box.getBoundingClientRect();
    const dist = (el: HTMLElement) => {
      const r = el.getBoundingClientRect();
      return r.top < view.top ? view.top - r.top : r.bottom > view.bottom ? r.bottom - view.bottom : 0;
    };
    if (rows.some((el) => dist(el) === 0)) return;
    rows.reduce((a, b) => (dist(b) < dist(a) ? b : a)).scrollIntoView({ block: "nearest" });
  }, [curTab?.id, curTab?.focus, gridOn]);

  const here = currentSpace(layout);
  // Searching swaps the parts for one flat list of this project's panes that match
  const kw = q.trim().toLowerCase();
  const hits = !kw
    ? []
    : allLeaves(layout)
        .filter((x) => spaceOf(x.ws) === here)
        .filter(({ ws, tab, pane }) => {
          const s = pane.sessionId ? sessions[pane.sessionId] : undefined;
          return [
            s?.title,
            s?.agent,
            s?.cwd,
            wsLabelOf(layout, ws),
            tabName(tab),
            pane.sub?.title,
            pane.term ? `${t("unifiedSidebar.terminal")} ${pane.term.cwd}` : "",
            pane.file?.path,
            pane.browser?.url,
          ]
            .join(" ")
            .toLowerCase()
            .includes(kw);
        });

  return (
    <Roomy.Provider value={roomy ? { now } : null}>
      <div className={cn("relative flex h-full shrink-0 flex-col border-r bg-sidebar", className)} style={className ? undefined : { width: sidebarW }}>
        {!className && <SidebarEdge />}
        {/* Beside the rail the project switcher, mode switch, add-ons and settings are on the rail (Rail.tsx); the phone's
          drawer has no rail and keeps them in its own top and bottom rows */}
        {className ? <SidebarHeader /> : <ListHeader />}
        {panelMenu?.length ? (
          <PanelPhoneMenu items={panelMenu} />
        ) : (
          <>
            {roomy && (
              <div className="relative shrink-0 px-2 pt-2">
                <Search className="pointer-events-none absolute top-1/2 left-4.5 mt-1 size-3.5 -translate-y-1/2 text-muted-foreground" />
                {/* iOS gets 16px: a smaller input makes iOS zoom the page on focus. Not focused on open: the keyboard would cover the list */}
                <input
                  className="h-8 w-full rounded-lg bg-muted/60 pr-2 pl-8 text-[13px] outline-none placeholder:text-muted-foreground/70 focus-visible:ring-2 focus-visible:ring-ring/40 supports-[-webkit-touch-callout:none]:text-[16px]"
                  placeholder={t("unifiedSidebar.filterPlaceholder")}
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                />
              </div>
            )}
            <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
              {kw ? (
                <div className="flex flex-col pt-1.5">
                  {hits.map((x) => (
                    <PaneRow key={x.pane.id} pane={x.pane.id} where={wsLabelOf(layout, x.ws)} />
                  ))}
                  {!hits.length && <p className="px-1 py-6 text-center text-[12px] text-muted-foreground">{t("unifiedSidebar.noMatch")}</p>}
                </div>
              ) : (
                <>
                  {/* The hosting panel's part (core CodeView portals it here) */}
                  {hosted && <div ref={setCodeSlot} data-code-slot />}
                  {/* Code's default list: all there is in Code mode; under the panel's part, or left out, when hosted */}
                  {!replaced && <CodeList />}
                  <div className="mt-1 flex gap-1 px-0.5">
                    <FootButton onClick={() => (toggleOverview(false), addTab())} title={t("unifiedSidebar.newTabHint")}>
                      <Plus className="size-3.5" /> {t("unifiedSidebar.newTab")}
                    </FootButton>
                    <FootButton onClick={() => (toggleOverview(false), openTerminal("tab"))} title={t("unifiedSidebar.terminalHint")}>
                      <SquareTerminal className="size-3.5" /> {t("unifiedSidebar.terminal")}
                    </FootButton>
                    {/* A new workspace is a new group of Code's default list: nothing to show for it without that list */}
                    {!replaced && (
                      <FootButton onClick={() => set({ dialog: { kind: "newWorkspace" } })} title={t("unifiedSidebar.newGroupHint")}>
                        <Plus className="size-3.5" /> {t("unifiedSidebar.newGroup")}
                      </FootButton>
                    )}
                  </div>
                </>
              )}
            </div>
          </>
        )}
        {className && <SidebarBottom sidebarToggle />}
        <TabDragLayer />
      </div>
    </Roomy.Provider>
  );
}

/**
 * Code's default list, a part like any a panel puts into the frame (core CodeList): "active" on top (the agents at
 * work, across workspaces), then the project's workspaces as groups, each row a pane. In Code mode it is the whole
 * list; a panel hosting Code gets it under its own part (list="add"), or leaves it out and groups the same panes its
 * own way (list="replace").
 */
export function CodeList() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const phone = usePhone();
  // Unconditional for the same reason as App's overviewOn: a skipped hook crashes when `phone` flips.
  const overviewOn = useStore((s) => s.overview.on);
  const gridOn = phone && overviewOn;
  const gridFocus = useStore((s) => s.overview.focus);
  useStore((s) => s.addonVersion);
  const tiles = useOverviewIds().split(",").filter(Boolean);
  // Held still while ⌘ is down, so the numbers don't move under ⌘1–9 (lib/modHeld.ts)
  const frozen = useModFrozen();
  const activeIds = frozenOrder(useActiveIds().split(",").filter(Boolean), frozen);
  const numbers = useModShown();
  // Counts for the ⋯ menu; recomputed on every render of the list, which already follows sessions and the overview
  const doneN = useStore((s) => doneActiveIds(s).length);
  const outN = useStore((s) => workingOutIds(s).length);
  const allProjects = useStore((s) => activeAllProjects(s));
  const [folded, setFolded] = useFolded();
  const [activeFolded, setActiveFolded] = useLocal(ACTIVE_FOLD_KEY, false);
  // In the phone drawer (the frame says so): roomier rows and titles
  const roomy = !!useContext(Roomy);
  const workKey = useWorkProject();
  const hosted = useStore((s) => !!codeHost(s));
  const list = spaceWorkspaces(layout);
  // A hosting panel lists its own conversations in its part above (the secretary's workers): not again in the
  // workspace their pane sits in, nor under "active". The workspace's title says how many of them work in its folder.
  const theirs = (id?: string) => hosted && !!id && sessions[id]?.tags?.panel === workKey;
  const groups = sideRows(layout, undefined, sessions);
  // A workspace holding nothing but the panel's conversations (made for a worker's directory, where the person has
  // nothing open) isn't listed at all: it would be a title over "empty" for every repository a worker touched
  const onlyTheirs = new Set<string>();
  if (hosted)
    for (const [id, rows] of groups) {
      const rest = rows.filter((r) => !theirs(r.pane.sessionId));
      if (rows.length && !rest.length) onlyTheirs.add(id);
      groups.set(id, rest);
    }
  const panelLive = hosted ? Object.values(sessions).filter((s) => s.tags?.panel === workKey && (s.status === "running" || needsYou(s))) : [];
  const liveIn = (ws: Workspace) => {
    const dir = ws.cwd?.replace(/\/+$/, "");
    return dir ? panelLive.filter((s) => s.cwd === dir || s.cwd?.startsWith(dir + "/")).map((s) => s.id) : [];
  };
  const cur = activeWs(layout);
  // An agent being dragged: "active" turns into a drop target, shown even when empty
  const dragAgent = useDrag()?.session;

  // Switching into a folded group by keyboard (⌃⌘1–9, a notification) unfolds it: otherwise you can't see where you are
  useEffect(() => {
    if (folded.includes(cur.id)) setFolded(folded.filter((x) => x !== cur.id));
  }, [cur.id]);

  const toggleFold = (id: string) => setFolded(folded.includes(id) ? folded.filter((x) => x !== id) : [...folded, id]);

  // "active" can reach other projects (allProjects): find each one's place
  const active = activeIds.flatMap((id) => {
    const loc = locateSession(layout, id);
    if (!loc || theirs(id)) return [];
    const item: Item = { ws: loc.ws, tab: loc.tab, pane: loc.pane, split: leaves(loc.tab.root).length > 1, s: sessions[id] };
    return [item];
  });
  const gridFocusId = gridFocus && tiles.includes(gridFocus) ? gridFocus : tiles[0];

  const flatRow = (x: Item, top = false, num?: number) => {
    const p = rowProps(x.ws, x.tab, x.pane, x.split);
    const id = x.s?.id;
    return (
      <Row
        key={x.pane.id}
        {...p}
        where={wsLabelOf(layout, x.ws)}
        top={top}
        num={num}
        // In the grid, a row of "active" picks its tile instead of leaving the grid
        current={top && gridOn ? id === gridFocusId : p.current}
        // The drawer closes once the tile has loaded: closing at once showed the tile we came from first (lib/catchup)
        onPick={top && gridOn && id && tiles.includes(id) ? () => (focusTile(id), markRead(id), whenCaughtUp(id, () => set({ drawer: false }))) : undefined}
      />
    );
  };

  const rowProps = (ws: Workspace, tab: Tab, pane: PaneLeaf, inSplit: boolean) => {
    const s = pane.sessionId ? sessions[pane.sessionId] : undefined;
    const here = !gridOn && ws.id === cur.id && tab.id === cur.activeTab;
    return {
      tab,
      pane,
      s,
      inSplit,
      unread: !!(s && unread[s.id]),
      inActive: !!(s && activeIds.includes(s.id)),
      // In a split, the focused pane is the current one; its neighbours get a lighter tint (they're on screen too)
      current: here && (!inSplit || tab.focus === pane.id),
      visible: here && inSplit,
      inActiveTab: tab.id === cur.activeTab,
      groupDir: ws.cwd ? basename(ws.cwd) : undefined,
    };
  };

  return (
    // data-ws-groups: the groups a dragged group title reorders among (TabDrag.tsx startWsDrag)
    <div className="pt-1.5" data-ws-groups>
      {(active.length > 0 || gridOn || allProjects || dragAgent) && (
        // data-active-drop: drop an agent here to put it in (TabDrag.tsx)
        <div data-active-drop className="mb-1 border-b pb-1.5">
          <div
            role="button"
            className="group/act flex h-7 w-full cursor-default items-center gap-1 rounded-md pr-1 pl-1.5 text-[11px] font-medium text-muted-foreground select-none hover:bg-accent/60 hover:text-foreground"
            title={t("unifiedSidebar.activeHint")}
            onClick={() => setActiveFolded(!activeFolded)}
          >
            {/* The margin lines the title up with the rows' titles below (row icon + gap-2 vs chevron + gap-1) */}
            <ChevronRight className={cn("size-3.5 shrink-0 transition-transform", roomy ? "mr-1.5" : "mr-1", !activeFolded && "rotate-90")} />
            <span className="flex-1 truncate text-left">
              {t("unifiedSidebar.active")}
              {allProjects && <span className="font-normal">{t("unifiedSidebar.allProjects")}</span>}
            </span>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button
                  className="flex size-5 items-center justify-center rounded-sm hover:bg-muted hover:text-foreground"
                  title={t("unifiedSidebar.activeSettings")}
                  onClick={(e) => e.stopPropagation()}
                >
                  <Ellipsis className="size-3.5" />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
                <DropdownMenuCheckboxItem checked={allProjects} onCheckedChange={(v) => setActiveAllProjects(!!v)}>
                  {t("unifiedSidebar.showAllProjects")}
                </DropdownMenuCheckboxItem>
                <div className="px-2 pb-1 text-[11px] text-muted-foreground">{t("unifiedSidebar.perProject")}</div>
                <DropdownMenuSeparator />
                <DropdownMenuItem disabled={!outN} onClick={restoreWorking}>
                  <ListPlus /> {outN ? t("unifiedSidebar.restoreWorkingN", { n: outN }) : t("unifiedSidebar.restoreWorking")}
                </DropdownMenuItem>
                <DropdownMenuItem disabled={!doneN} onClick={clearActive}>
                  <Eraser /> {doneN ? t("unifiedSidebar.clearDoneN", { n: doneN }) : t("unifiedSidebar.clearDone")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
            {phone && (
              <button
                className={cn("flex h-5 items-center gap-1 rounded-sm px-1", gridOn ? "bg-brand/15 text-foreground" : "hover:bg-muted hover:text-foreground")}
                title={gridOn ? t("unifiedSidebar.gridOff") : t("unifiedSidebar.gridOn", { n: MAX_TILES })}
                // On a phone the overview sits behind the drawer: close it
                onClick={(e) => (e.stopPropagation(), toggleOverview(!gridOn), set({ drawer: false }))}
              >
                <GalleryHorizontalEnd className={cn("size-3", gridOn && "text-brand")} />
                {t("unifiedSidebar.grid")}
              </button>
            )}
          </div>
          {!activeFolded && <div>{active.map((item, i) => flatRow(item, true, numbers && i < 9 ? i + 1 : undefined))}</div>}
          {/* Only when empty: a row appearing mid-drag would push the rows under the pointer */}
          {dragAgent && !active.length && !activeFolded && (
            <div className="py-1 pl-[26px] text-[12px] text-muted-foreground">{t("unifiedSidebar.dropToActive")}</div>
          )}
        </div>
      )}
      {list.map((ws, wi) =>
        onlyTheirs.has(ws.id) ? null : (
          <Group
            key={ws.id}
            ws={ws}
            index={wi}
            last={wi === list.length - 1}
            folded={folded.includes(ws.id)}
            onFold={() => toggleFold(ws.id)}
            sessions={sessions}
            unread={unread}
            current={ws.id === cur.id}
            rows={groups.get(ws.id) ?? []}
            panelLive={liveIn(ws)}
          >
            {/* One row per pane, wherever it was dragged to (layout.sideRows): the panes of a split aren't tied together here,
                  the ones on screen just light up. data-side-row(s) / data-pinned: drag a row among the pinned ones to pin it there (TabDrag.tsx) */}
            {groups.get(ws.id)?.map((r) => (
              <div key={r.pane.id} data-side-row={r.pane.id} data-pinned={r.pinned || undefined}>
                <Row {...rowProps(r.ws, r.tab, r.pane, leaves(r.tab.root).length > 1)} groupId={ws.id} pinned={!!r.pinned} />
              </div>
            ))}
            {!groups.get(ws.id)?.length && <div className="py-1 pl-[26px] text-[12px] text-muted-foreground">{t("unifiedSidebar.emptyGroup")}</div>}
          </Group>
        ),
      )}
    </div>
  );
}

function FootButton({ onClick, title, children }: { onClick: () => void; title: string; children: React.ReactNode }) {
  return (
    <button
      title={title}
      className="flex h-7 flex-1 items-center justify-center gap-1 rounded-md text-[12px] text-muted-foreground hover:bg-accent hover:text-foreground"
      onClick={onClick}
    >
      {children}
    </button>
  );
}

/**
 * A row's icon, with its ⌘1–9 number laid over it while ⌘ is held: the number fills the icon's own box, so it lines up
 * whatever the row's padding or the icon's size, and nothing on the row moves. See-through, so the icon still shows.
 */
/**
 * A row's icon, with what is laid over it: its ⌘ number, or a small corner mark when the row is kept at the top of its
 * group. On the icon rather than beside it so it costs the title no width (asked for); a line between the kept rows and
 * the rest read as a level the groups themselves don't have.
 */
function IconSlot({ n, pinned, children }: { n?: number; pinned?: boolean; children: React.ReactNode }) {
  return (
    <span className="relative flex shrink-0">
      {children}
      {pinned && !n && (
        <span className="absolute -right-1 -bottom-1 flex size-2.5 items-center justify-center rounded-full bg-sidebar text-muted-foreground">
          <ArrowUpToLine className="size-2" strokeWidth={3} />
        </span>
      )}
      {!!n && (
        <span className="absolute inset-0 flex items-center justify-center rounded-[3px] bg-background/70 text-[10px] leading-none font-semibold text-foreground tabular-nums ring-1 ring-border/70">
          {n}
        </span>
      )}
    </span>
  );
}

function Group({
  ws,
  index,
  last,
  folded,
  onFold,
  sessions,
  unread,
  current,
  rows,
  panelLive,
  children,
}: {
  ws: Workspace;
  index: number;
  last: boolean;
  folded: boolean;
  onFold: () => void;
  sessions: Record<string, SessionSummary>;
  unread: Record<string, true>;
  current: boolean;
  rows: SideRow[];
  /** Conversations of the hosting panel at work in this workspace's folder (listed in the panel's part, not here). */
  panelLive: string[];
  children: React.ReactNode;
}) {
  const layout = useStore((s) => s.layout);
  const others = spaceList(layout).filter((sp) => sp.id !== spaceOf(ws));
  const label = applyWorkspaceLabel(ws, index, ws.name);
  const n = rows.length;
  // Muted (do not disturb): the rows keep their own dots, but the title says nothing about them
  const muted = !!ws.muted;
  const waiting = muted ? 0 : rows.filter((r) => needsYou(r.pane.sessionId ? sessions[r.pane.sessionId] : undefined)).length;
  const edit = () => set({ dialog: { kind: "editWorkspace", id: ws.id } });
  const roomy = !!useContext(Roomy);
  return (
    <div className="mt-1" data-ws-group={ws.id}>
      <ContextMenu>
        <ContextMenuTrigger asChild>
          {/* data-ws-id: dropping a sidebar row here lists it at the end of this group; a tab from the tab bar moves here.
              Dragging the title itself reorders the groups (TabDrag.tsx) */}
          <div
            data-ws-id={ws.id}
            onPointerDown={(e) => startWsDrag(e, ws.id, label)}
            role="button"
            className={cn(
              "group/ws flex cursor-default items-center gap-1 rounded-md pr-1 pl-1.5 text-[12px] select-none hover:bg-accent/60",
              roomy ? "h-9" : "h-7",
              "data-[state=open]:ring-1 data-[state=open]:ring-brand/60",
            )}
            onClick={onFold}
            title={ws.cwd ? ws.cwd : undefined}
          >
            {/* The margin lines the group name up with its rows' titles (row icon + gap-2 vs chevron + gap-1) */}
            <ChevronRight className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", roomy ? "mr-1.5" : "mr-1", !folded && "rotate-90")} />
            <span className={cn("min-w-0 truncate font-semibold", current ? "text-foreground" : "text-foreground/80")}>{label}</span>
            {ws.cwd && basename(ws.cwd) !== label && <span className="min-w-0 truncate text-[11px] text-muted-foreground">{basename(ws.cwd)}</span>}
            {muted && (
              <span title={t("unifiedSidebar.mutedHint")} className="flex shrink-0">
                <BellOff className="size-3 text-muted-foreground" />
              </span>
            )}
            <span className="flex-1" />
            {panelLive.length > 0 && (
              <button
                className="flex h-4 shrink-0 items-center gap-1 rounded-full bg-brand/15 px-1.5 text-[10px] font-medium text-brand hover:bg-brand/25"
                title={t("unifiedSidebar.panelLiveHint", { n: panelLive.length })}
                onClick={(e) => {
                  e.stopPropagation();
                  showInCodeView(panelLive[0]);
                }}
              >
                <Orbit className="size-2.5" />
                {panelLive.length}
              </button>
            )}
            {folded && waiting > 0 ? (
              <span className="shrink-0 rounded-full bg-bad px-1.5 text-[10px] leading-4 font-semibold text-white">
                {t("unifiedSidebar.waitingN", { n: waiting })}
              </span>
            ) : (
              <span className="flex shrink-0 items-center gap-1.5 text-[11px] text-muted-foreground tabular-nums group-hover/ws:hidden">
                {folded && !muted && <WsDot w={ws} sessions={sessions} unread={unread} hideIdle />}
                {n}
              </span>
            )}
            <button
              className="hidden size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground group-hover/ws:flex hover:bg-muted hover:text-foreground"
              title={t("unifiedSidebar.newTabInGroup")}
              onClick={(e) => {
                e.stopPropagation();
                toggleOverview(false);
                addTab({ workspaceId: ws.id, cwd: ws.cwd });
              }}
            >
              <Plus className="size-3.5" />
            </button>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuItem onClick={() => (toggleOverview(false), addTab({ workspaceId: ws.id, cwd: ws.cwd }))}>
            <Plus /> {t("unifiedSidebar.newTab")}
          </ContextMenuItem>
          <ContextMenuItem
            onClick={() => {
              toggleOverview(false);
              selectWorkspace(ws.id);
              openTerminal("tab");
            }}
          >
            <SquareTerminal /> {t("unifiedSidebar.newTerminal")}
          </ContextMenuItem>
          <ContextMenuItem onClick={edit}>
            <Pencil /> {t("unifiedSidebar.editEllipsis")}
          </ContextMenuItem>
          <ContextMenuItem onClick={() => muteWorkspace(ws.id, !muted)} title={t("unifiedSidebar.muteHint")}>
            {muted ? (
              <>
                <Bell /> {t("unifiedSidebar.unmute")}
              </>
            ) : (
              <>
                <BellOff /> {t("unifiedSidebar.mute")}
              </>
            )}
          </ContextMenuItem>
          {/* Reordering on a touch screen, where the title can't be dragged (scrolling and this long-press menu own the finger) */}
          {index > 0 && (
            <ContextMenuItem onClick={() => moveWorkspace(ws.id, index - 1)}>
              <ArrowUp /> {t("unifiedSidebar.moveUp")}
            </ContextMenuItem>
          )}
          {!last && (
            <ContextMenuItem onClick={() => moveWorkspace(ws.id, index + 1)}>
              <ArrowDown /> {t("unifiedSidebar.moveDown")}
            </ContextMenuItem>
          )}
          {others.length > 0 && (
            <ContextMenuSub>
              <ContextMenuSubTrigger>{t("unifiedSidebar.moveToProject")}</ContextMenuSubTrigger>
              <ContextMenuSubContent>
                {others.map((sp) => (
                  <ContextMenuItem key={sp.id} onClick={() => moveWorkspaceToSpace(ws.id, sp.id)}>
                    {sp.name}
                  </ContextMenuItem>
                ))}
              </ContextMenuSubContent>
            </ContextMenuSub>
          )}
          <ContextMenuSeparator />
          <ContextMenuItem variant="destructive" onClick={() => closeWorkspace(ws.id)}>
            <CircleX /> {t("unifiedSidebar.closeGroup")}
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
      {!folded && (
        // Not indented: a row's icon (pl-1) sits under the title's chevron (px-1), as in 进行中; the bold titles show the levels
        <div data-side-rows={ws.id}>{children}</div>
      )}
    </div>
  );
}

// Memoised, and every prop is either stable or changes only for the rows it concerns: switching tabs re-renders the
// sidebar (it shows which row is current), and every row with its menu re-rendered with it (tools/scrolltest/tabrenders.mjs).
// So no callbacks made per render and no "which tab is active" handed to every row: it's read when the menu acts.
const Row = memo(function Row({
  tab,
  pane,
  s,
  inSplit,
  unread,
  inActive,
  top,
  onPick,
  current,
  visible,
  inActiveTab,
  where,
  num,
  groupDir,
  groupId,
  pinned,
}: {
  tab: Tab;
  pane: PaneLeaf;
  s?: SessionSummary;
  inSplit: boolean;
  unread: boolean;
  inActive: boolean;
  /** A row of the "active" section: its ✕ and middle click take it off the section instead of closing the tab. */
  top?: boolean;
  /** Replaces going to the pane (the grid picks a tile). */
  onPick?: () => void;
  current: boolean;
  visible: boolean;
  /** This row's tab is the one on screen. */
  inActiveTab: boolean;
  /** Shown instead of the directory (rows of "active", out of their group). */
  where?: string;
  /** Its ⌘1–9 number, while ⌘ is held (rows of "active"). */
  num?: number;
  /** The folder of the row's workspace: its group's header already says it, so a row there in the same folder doesn't. */
  groupDir?: string;
  /** The group (workspace) listing this row, for keeping it at the top there; unset in "active". */
  groupId?: string;
  /** Kept at the top of its group (layout.sideRows pins) instead of sorting by activity. */
  pinned?: boolean;
}) {
  const label = paneLabel(pane, s);
  const { title } = label;
  const sub = label.sub === groupDir ? "" : label.sub;
  // An unnamed tab is named after its first agent: only a renamed tab adds something worth showing
  const tabLabel = !inSplit && tab.name && tab.name !== title ? tab.name : undefined;
  const close = () => (inSplit ? closePaneById(pane.id) : closeTab(tab.id));
  const onActive = (on: boolean) => s && (on ? addActive([s.id]) : removeActive(s.id));
  const dismiss = top && s ? () => onActive(false) : close;
  // Already in the tab on screen (alone or in its split): nothing to put beside it
  const canBeside = !inActiveTab;
  const openBeside = () => {
    // Into the tab on screen, on its right: a lone tab moves over whole, a pane of a split moves on its own
    const activeTabId = activeWs(get().layout).activeTab;
    toggleOverview(false);
    if (inSplit) movePane(pane.id, { tabId: activeTabId, side: "right" });
    else mergeTab(tab.id, activeTabId, undefined, "right");
  };
  const Icon = pane.term ? SquareTerminal : pane.file?.folder ? FolderOpen : pane.file ? FileText : pane.sub ? Columns2 : pane.browser ? Globe : SquareDashed;
  // Picked and still loading after a moment (the drawer stays open till then, App.tsx): say so on the row itself
  const loading = useSlowCatchUp(current ? s?.id : undefined);
  const roomy = useContext(Roomy);
  const place = where ?? sub;
  const oneLine = !!roomy && !place && !s?.host;
  const time = roomy && (
    <span className="shrink-0 text-[11px] font-normal text-muted-foreground tabular-nums" title={s ? new Date(s.updatedAt).toLocaleString() : ""}>
      {s?.status === "running" && s.turn ? <Elapsed since={s.turn.startedAt} /> : ago(s?.updatedAt, roomy.now)}
    </span>
  );
  const pick = () => {
    if (onPick) return onPick();
    goPane(pane.id, s?.id);
    // The drawer closes when the pane in front changes (App.tsx); tapping the one you're already on changes nothing
    if (current) set({ drawer: false });
  };
  /*
   * In the drawer a finger's tap goes on `pointerup`, not `click` (measured on the old phone pane list): with the
   * keyboard up from the search box, the same tap first drops the keyboard, the page's height changes, the list reflows and the
   * browser sends the click elsewhere or not at all — "the first tap only closes the keyboard". Touch pointers are
   * implicitly captured, so pointerup lands on the row pointerdown did. A hold is the long-press menu, not a tap.
   */
  const down = useRef<{ x: number; y: number; t: number } | null>(null);
  const took = useRef(0);
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div
          role="button"
          data-row-current={current || undefined}
          // Drag onto a pane (split beside it) or onto another group (move the tab there): TabDrag.tsx
          onPointerDown={(e) => {
            down.current = roomy && e.pointerType !== "mouse" ? { x: e.clientX, y: e.clientY, t: e.timeStamp } : null;
            startPaneDrag(e, pane.id, { label: title, agent: s?.agent, term: !!pane.term });
          }}
          onPointerCancel={() => (down.current = null)}
          onPointerUp={(e) => {
            const d = down.current;
            down.current = null;
            // Moved more than 10px = scrolling the list (pointercancel doesn't always come)
            if (!d || e.timeStamp - d.t > 450 || Math.abs(e.clientX - d.x) > 10 || Math.abs(e.clientY - d.y) > 10) return;
            took.current = e.timeStamp;
            pick();
            eatNextClick();
          }}
          onClick={(e) => e.timeStamp - took.current > 800 && pick()}
          onDoubleClick={() => !inSplit && set({ dialog: { kind: "rename", target: "tab", id: tab.id, value: tab.name ?? tabName(tab) } })}
          onMouseDown={(e) => e.button === 1 && e.preventDefault()}
          onAuxClick={(e) => e.button === 1 && (e.preventDefault(), dismiss())}
          className={cn(
            "group relative flex cursor-default items-center gap-2 rounded-md pr-2 pl-1.5 text-[12px] select-none",
            roomy ? "min-h-11 py-1.5" : "h-7",
            current ? "bg-accent" : visible ? "bg-accent/40" : "hover:bg-accent/60",
            "data-[state=open]:ring-1 data-[state=open]:ring-brand/60",
          )}
        >
          {loading && <BorderFlow />}
          <IconSlot n={num} pinned={pinned}>
            {s ? (
              <AgentIcon agent={s.agent} className={cn("shrink-0", roomy ? "size-4" : "size-3.5")} />
            ) : (
              <Icon className={cn("shrink-0 text-muted-foreground", roomy ? "size-4" : "size-3.5")} />
            )}
          </IconSlot>
          {roomy ? (
            // Two lines: what it is, then where it is and how long ago it last moved (running: for how long). With nothing
            // to say where (its group's header already does), one line: a second line holding only the
            // time left the title and time on different lines with the icon between them (reported)
            <span className={cn("min-w-0 flex-1 leading-tight", oneLine && "flex items-center gap-1.5")}>
              <span
                className={cn(
                  "block truncate",
                  oneLine && "min-w-0 flex-1",
                  pane.term && "font-mono text-[11.5px]",
                  !s && !pane.term && "text-muted-foreground",
                )}
              >
                {tabLabel ? (
                  <>
                    {tabLabel} <span className="text-muted-foreground">· {title}</span>
                  </>
                ) : (
                  title
                )}
              </span>
              {oneLine ? (
                time
              ) : (
                <span className="mt-0.5 flex items-center gap-1.5 text-[11px] font-normal text-muted-foreground">
                  <HostTag session={s} />
                  <span className="min-w-0 flex-1 truncate">{place}</span>
                  {time}
                </span>
              )}
            </span>
          ) : (
            <>
              <span className={cn("min-w-0 flex-1 truncate", pane.term && "font-mono text-[11.5px]", !s && !pane.term && "text-muted-foreground")}>
                {tabLabel ? (
                  <>
                    {tabLabel} <span className="text-muted-foreground">· {title}</span>
                  </>
                ) : (
                  title
                )}
              </span>
              <HostTag session={s} className="py-0.5 group-hover:hidden" iconOnly />
              <span className="flex max-w-[40%] shrink-0 items-center text-[11px] text-muted-foreground group-hover:hidden">
                {s?.status === "running" && s.turn ? <Elapsed since={s.turn.startedAt} /> : <span className="truncate">{where ?? sub}</span>}
              </span>
            </>
          )}
          <button
            className="hidden size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground group-hover:flex hover:text-foreground"
            title={top && s ? t("nav.removeActive") : inSplit ? t("unifiedSidebar.closeCell") : t("unifiedSidebar.closeTab")}
            onClick={(e) => (e.stopPropagation(), dismiss())}
          >
            <X className="size-3" />
          </button>
          {/* Always drawn (transparent when there is nothing to show, or no session at all: a file / terminal pane)
              so the right-hand labels line up in one column across every row. */}
          <StatusDot status={dotStatus(s)} unread={unread} />
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        {s &&
          (inActive ? (
            <ContextMenuItem onClick={() => onActive(false)}>
              <PinOff /> {t("nav.removeActive")}
            </ContextMenuItem>
          ) : (
            <ContextMenuItem onClick={() => onActive(true)}>
              <Pin /> {t("unifiedSidebar.addActive")}
            </ContextMenuItem>
          ))}
        {s && unread && (
          <ContextMenuItem onClick={() => markRead(s.id)}>
            <MailCheck /> {t("unifiedSidebar.markRead")}
          </ContextMenuItem>
        )}
        {groupId && (
          <ContextMenuItem onClick={() => pinRow(pane.id, groupId, !pinned)} title={t("unifiedSidebar.pinTopHint")}>
            {pinned ? <ArrowDownWideNarrow /> : <ArrowUpToLine />} {pinned ? t("unifiedSidebar.unpinTop") : t("unifiedSidebar.pinTop")}
          </ContextMenuItem>
        )}
        {canBeside && (
          <ContextMenuItem onClick={openBeside}>
            <SquareSplitHorizontal /> {t("unifiedSidebar.openBeside")}
          </ContextMenuItem>
        )}
        {inSplit && (
          <ContextMenuItem onClick={() => (toggleOverview(false), paneToTab(pane.id))}>
            <Columns2 /> {t("unifiedSidebar.splitToTab")}
          </ContextMenuItem>
        )}
        {!inSplit && (
          <ContextMenuItem onClick={() => set({ dialog: { kind: "rename", target: "tab", id: tab.id, value: tab.name ?? tabName(tab) } })}>
            <Pencil /> {t("unifiedSidebar.renameTab")}
          </ContextMenuItem>
        )}
        {s && (
          <ContextMenuItem onClick={() => set({ dialog: { kind: "rename", target: "session", id: s.id, value: s.title ?? "" } })}>
            <Pencil /> {t("unifiedSidebar.renameSession")}
          </ContextMenuItem>
        )}
        <RowPanelItems pane={pane.id} session={s?.id} />
        <ContextMenuSeparator />
        <ContextMenuItem variant="destructive" onClick={close}>
          <CircleX /> {inSplit ? t("unifiedSidebar.closeCell") : t("unifiedSidebar.closeTab")}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
});

/** The hosting panel's items in a row's right-click menu: asked when the menu opens (see Panes.tsx PanelMenuItems). */
function RowPanelItems({ pane, session }: { pane: string; session?: string }) {
  return codeMenuItems(pane, session).map((it) => (
    <ContextMenuItem key={it.id} disabled={it.disabled} onClick={it.onSelect}>
      {it.icon} {it.label}
    </ContextMenuItem>
  ));
}
