import {
  ChevronRight,
  CircleX,
  Columns2,
  FileText,
  LayoutGrid,
  MailCheck,
  Pencil,
  Pin,
  PinOff,
  Plus,
  SquareDashed,
  SquareTerminal,
  SquareSplitHorizontal,
  X,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
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
  moveWorkspaceToSpace,
  openTerminal,
  paneToTab,
  selectWorkspace,
  tabName,
} from "@/lib/actions";
import { applyWorkspaceLabel } from "@/lib/addons";
import { activeWs, leaves, locateSession, spaceList, spaceOf, spaceWorkspaces, type PaneLeaf, type Tab, type Workspace } from "@/lib/layout";
import { addTiles, focusTile, MAX_TILES, removeTile, toggleOverview, useOverviewIds } from "@/lib/overview";
import { compareSessions, sessionRank } from "@/lib/order";
import { markRead, set, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { cn } from "@/lib/utils";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";
import { Elapsed, SidebarBottom, SidebarEdge, SidebarHeader, WsDot } from "./Chrome";
import { TabDragLayer, startPaneDrag } from "./TabDrag";

/*
 * Labs → "新侧栏" (config.unifiedSidebar), desktop only. One list in place of four things that each listed the same agents:
 * the workspace switcher, the tab bar, the agents list and the to-do view.
 *
 * - Every workspace of the project is a group, all shown at once (no switching), each collapsible. A group lists its tabs,
 *   one row per pane: a tab split in several panes shows its rows joined by a bar, so each agent's state stays visible.
 * - Pinned agents sit on top. "Pin" is the to-do list under another name (same storage, lib/overview.ts), and its grid
 *   opens over the main area, next to this list, instead of covering the whole window with a list of its own.
 * - The status filter (needs you / running) flattens the groups to the rows that match.
 * - "Active" above the groups: what needs you, runs, or was touched in the last ACTIVE_HOURS, flat in the old agents list's
 *   order (lib/order.ts: needs you, then running, then idle, newest first). The groups below still hold everything, so these
 *   rows show twice: the top is where to look, the groups are where things live.
 *
 * Kept out of Chrome.tsx so the experiment can be dropped (or promoted) by touching a few lines in App.tsx.
 */

type Filter = "all" | "you" | "run";

const FOLD_KEY = "gerdr.unified.folded";
const ACTIVE_FOLD_KEY = "gerdr.unified.activeFolded";
/** An idle, read session stays in "active" this long after its last activity. */
const ACTIVE_HOURS = 6;

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

/** The agents list's order (lib/order.ts); panes without an agent (terminals, files, new conversations) after the idle ones. Stable on ties. */
function compareItems(a: Item, b: Item, unread: Record<string, true>): number {
  if (a.s && b.s) return compareSessions(a.s, b.s, unread);
  return (a.s ? sessionRank(a.s, unread) : 5) - (b.s ? sessionRank(b.s, unread) : 5);
}

const needsYou = (s: SessionSummary | undefined, unread: Record<string, true>) => !!s && (s.status === "waiting" || s.status === "error" || !!unread[s.id]);

/** What a row shows for a pane: an agent, a terminal, a file, a subagent, or a new conversation not started yet. */
function paneLabel(p: PaneLeaf, s: SessionSummary | undefined): { title: string; sub: string } {
  if (s) return { title: s.title || basename(s.cwd), sub: basename(s.cwd) };
  if (p.term) return { title: "终端", sub: basename(p.term.cwd) };
  if (p.file) return { title: basename(p.file.path), sub: "文件" };
  if (p.sub) return { title: p.sub.title || "子 agent", sub: "子 agent" };
  return { title: "新会话", sub: p.draft?.cwd ? basename(p.draft.cwd) : "" };
}

/** Go to a pane: the pin grid would cover it, so that closes first. */
function goPane(paneId: string, sessionId?: string) {
  toggleOverview(false);
  focusPane(paneId);
  if (sessionId) markRead(sessionId);
}

function pin(id: string) {
  addTiles([id]);
  if (!useStore.getState().overview.ids.includes(id)) toast(`最多 Pin ${MAX_TILES} 个，先取消几个`);
}

export function UnifiedSidebar() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const sidebarW = useStore((s) => s.sidebarW);
  const gridOn = useStore((s) => s.overview.on);
  const gridFocus = useStore((s) => s.overview.focus);
  useStore((s) => s.addonVersion);
  const pinned = useOverviewIds().split(",").filter(Boolean);
  const pinnedSet = new Set(pinned);
  const [filter, setFilter] = useState<Filter>("all");
  const [folded, setFolded] = useFolded();
  const [activeFolded, setActiveFolded] = useLocal(ACTIVE_FOLD_KEY, false);
  const list = spaceWorkspaces(layout);
  const cur = activeWs(layout);
  const curTab = cur.tabs.find((t) => t.id === cur.activeTab);
  const scroller = useRef<HTMLDivElement>(null);

  // Switching into a folded group by keyboard (⌃⌘1–9, a notification) unfolds it: otherwise you can't see where you are
  useEffect(() => {
    if (folded.includes(cur.id)) setFolded(folded.filter((x) => x !== cur.id));
  }, [cur.id]);
  // Keep the row you're on in sight when it changes from elsewhere (⌘1–9, next-needs-you)
  useEffect(() => {
    scroller.current?.querySelector<HTMLElement>("[data-row-current]")?.scrollIntoView({ block: "nearest" });
  }, [curTab?.id, curTab?.focus, gridOn]);

  const toggleFold = (id: string) => setFolded(folded.includes(id) ? folded.filter((x) => x !== id) : [...folded, id]);

  // Every pane of this project, for the filter and its counts
  const all: Item[] = [];
  for (const ws of list)
    for (const tab of ws.tabs) {
      const ps = leaves(tab.root);
      for (const pane of ps) all.push({ ws, tab, pane, split: ps.length > 1, s: pane.sessionId ? sessions[pane.sessionId] : undefined });
    }
  const youN = all.filter((x) => needsYou(x.s, unread)).length;
  const runN = all.filter((x) => x.s?.status === "running").length;
  const byPriority = (xs: Item[]) => [...xs].sort((a, b) => compareItems(a, b, unread));
  const matches = byPriority(all.filter((x) => (filter === "you" ? needsYou(x.s, unread) : x.s?.status === "running")));
  const now = Date.now();
  const active = byPriority(all.filter((x) => !!x.s && (sessionRank(x.s, unread) < 4 || now - x.s.updatedAt < ACTIVE_HOURS * 36e5)));
  const flatRow = (x: Item) => (
    <Row key={x.pane.id} {...rowProps(x.ws, x.tab, x.pane, x.split)} where={applyWorkspaceLabel(x.ws, list.indexOf(x.ws), x.ws.name)} />
  );

  const rowProps = (ws: Workspace, tab: Tab, pane: PaneLeaf, inSplit: boolean) => {
    const s = pane.sessionId ? sessions[pane.sessionId] : undefined;
    const here = !gridOn && ws.id === cur.id && tab.id === cur.activeTab;
    return {
      tab,
      pane,
      s,
      inSplit,
      unread: !!(s && unread[s.id]),
      pinned: !!(s && pinnedSet.has(s.id)),
      // In a split, the focused pane is the current one; its neighbours get a lighter tint (they're on screen too)
      current: here && (!inSplit || tab.focus === pane.id),
      visible: here && inSplit,
      activeTabId: cur.activeTab,
    };
  };

  return (
    <div className="relative flex h-full shrink-0 flex-col border-r bg-sidebar" style={{ width: sidebarW }}>
      <SidebarEdge />
      <SidebarHeader />
      <div className="flex shrink-0 items-center gap-0.5 overflow-hidden px-2 pt-2 pb-1 text-[11px]">
        <FilterChip on={filter === "all"} onClick={() => setFilter("all")}>
          全部 <span className="tabular-nums opacity-70">{all.length}</span>
        </FilterChip>
        <FilterChip on={filter === "you"} onClick={() => setFilter(filter === "you" ? "all" : "you")} tone={youN ? "bad" : undefined}>
          等你 <span className="tabular-nums opacity-70">{youN}</span>
        </FilterChip>
        <FilterChip on={filter === "run"} onClick={() => setFilter(filter === "run" ? "all" : "run")}>
          运行中 <span className="tabular-nums opacity-70">{runN}</span>
        </FilterChip>
      </div>
      <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
        <PinSection ids={pinned} gridOn={gridOn} gridFocus={gridFocus} sessions={sessions} unread={unread} />
        {filter !== "all" ? (
          <div className="pt-1">
            {matches.length === 0 && <div className="px-2 py-1 text-[12px] text-muted-foreground">{filter === "you" ? "没有在等你的" : "没有在跑的"}</div>}
            {matches.map(flatRow)}
          </div>
        ) : (
          <>
            {active.length > 0 && (
              <div className="mb-1 border-b pb-1.5">
                <button
                  className="flex h-7 w-full items-center gap-1 rounded-md px-1 text-[11px] font-medium text-muted-foreground hover:bg-accent/60 hover:text-foreground"
                  title={`等你、在跑、没看的，和 ${ACTIVE_HOURS} 小时内动过的；按该不该先看排`}
                  onClick={() => setActiveFolded(!activeFolded)}
                >
                  <ChevronRight className={cn("size-3.5 shrink-0 transition-transform", !activeFolded && "rotate-90")} />
                  <span className="flex-1 text-left">进行中</span>
                  <span className="tabular-nums">{active.length}</span>
                </button>
                {!activeFolded && active.map(flatRow)}
              </div>
            )}
            {list.map((ws, wi) => (
              <Group
                key={ws.id}
                ws={ws}
                index={wi}
                folded={folded.includes(ws.id)}
                onFold={() => toggleFold(ws.id)}
                sessions={sessions}
                unread={unread}
                current={ws.id === cur.id}
              >
                {ws.tabs.map((tab) => {
                  const ps = leaves(tab.root);
                  if (ps.length === 1) return <Row key={tab.id} {...rowProps(ws, tab, ps[0], false)} />;
                  return (
                    // A split tab: its panes as rows joined by a bar on the left
                    <div
                      key={tab.id}
                      className="relative my-0.5 ml-3 before:absolute before:top-1.5 before:bottom-1.5 before:left-0 before:w-0.5 before:rounded-full before:bg-brand/40"
                    >
                      {ps.map((p) => (
                        <Row key={p.id} {...rowProps(ws, tab, p, true)} />
                      ))}
                    </div>
                  );
                })}
                {ws.tabs.length === 0 && <div className="py-1 pl-7 text-[12px] text-muted-foreground">空的</div>}
              </Group>
            ))}
          </>
        )}
        <div className="mt-1 flex gap-1 px-0.5">
          <FootButton onClick={() => (toggleOverview(false), addTab())} title="在当前工作空间新开一个 tab">
            <Plus className="size-3.5" /> 新 tab
          </FootButton>
          <FootButton onClick={() => (toggleOverview(false), openTerminal("tab"))} title="在当前工作空间开一个终端">
            <SquareTerminal className="size-3.5" /> 终端
          </FootButton>
          <FootButton onClick={() => set({ dialog: { kind: "newWorkspace" } })} title="新工作空间（一个分组）">
            <Plus className="size-3.5" /> 分组
          </FootButton>
        </div>
      </div>
      <SidebarBottom sidebarToggle />
      <TabDragLayer />
    </div>
  );
}

function FilterChip({ on, tone, onClick, children }: { on: boolean; tone?: "bad"; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      className={cn(
        "flex h-6 shrink-0 items-center gap-0.5 rounded-md px-1.5 whitespace-nowrap",
        on ? "bg-foreground text-background" : "text-muted-foreground hover:bg-accent hover:text-foreground",
        !on && tone === "bad" && "text-bad",
      )}
      onClick={onClick}
    >
      {children}
    </button>
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

/** Pinned agents on top, from every project (like the to-do list was). The header opens / closes their grid. */
function PinSection({
  ids,
  gridOn,
  gridFocus,
  sessions,
  unread,
}: {
  ids: string[];
  gridOn: boolean;
  gridFocus?: string;
  sessions: Record<string, SessionSummary>;
  unread: Record<string, true>;
}) {
  const layout = useStore((s) => s.layout);
  if (!ids.length && !gridOn) return null;
  const focus = gridFocus && ids.includes(gridFocus) ? gridFocus : ids[0];
  return (
    <div className="mb-1 border-b pb-1.5">
      <button
        className={cn(
          "flex w-full items-center gap-1.5 rounded-md px-2 py-1 text-[11px] font-medium",
          gridOn ? "bg-brand/15 text-foreground" : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
        )}
        title={gridOn ? "关掉网格，回到 tab" : "把 Pin 的 agent 摆成网格一起看"}
        onClick={() => toggleOverview(!gridOn)}
      >
        <Pin className="size-3" />
        <span className="flex-1 text-left">Pin</span>
        <LayoutGrid className="size-3" />
        <span>{gridOn ? "网格中" : "网格"}</span>
      </button>
      {ids.length === 0 && <div className="px-2 py-1 text-[12px] text-muted-foreground">右键一行 → Pin</div>}
      {ids.map((id) => {
        const s = sessions[id];
        if (!s) return null;
        const loc = locateSession(layout, id);
        const current = gridOn && id === focus;
        return (
          <ContextMenu key={id}>
            <ContextMenuTrigger asChild>
              <div
                role="button"
                data-row-current={current || undefined}
                className={cn(
                  "group flex h-7 cursor-default items-center gap-2 rounded-md px-2 text-[12px] select-none hover:bg-accent/60",
                  current && "bg-accent font-medium",
                  "data-[state=open]:ring-1 data-[state=open]:ring-brand/60",
                )}
                onClick={() => {
                  if (gridOn) return (focusTile(id), markRead(id));
                  if (loc) goPane(loc.pane.id, id);
                }}
              >
                <AgentIcon agent={s.agent} className="size-3.5 shrink-0" />
                <span className="min-w-0 flex-1 truncate">{s.title || basename(s.cwd)}</span>
                <span className="max-w-[40%] shrink-0 truncate text-[11px] text-muted-foreground group-hover:hidden">{loc ? loc.ws.name : "Work"}</span>
                <button
                  className="hidden size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground group-hover:flex hover:text-foreground"
                  title="取消 Pin"
                  onClick={(e) => (e.stopPropagation(), removeTile(id))}
                >
                  <X className="size-3" />
                </button>
                <StatusDot status={dotStatus(s)} unread={!!unread[id]} />
              </div>
            </ContextMenuTrigger>
            <ContextMenuContent>
              {loc && (
                <ContextMenuItem onClick={() => goPane(loc.pane.id, id)}>
                  <ChevronRight /> 去它的 tab
                </ContextMenuItem>
              )}
              <ContextMenuItem onClick={() => removeTile(id)}>
                <PinOff /> 取消 Pin
              </ContextMenuItem>
            </ContextMenuContent>
          </ContextMenu>
        );
      })}
    </div>
  );
}

function Group({
  ws,
  index,
  folded,
  onFold,
  sessions,
  unread,
  current,
  children,
}: {
  ws: Workspace;
  index: number;
  folded: boolean;
  onFold: () => void;
  sessions: Record<string, SessionSummary>;
  unread: Record<string, true>;
  current: boolean;
  children: React.ReactNode;
}) {
  const layout = useStore((s) => s.layout);
  const others = spaceList(layout).filter((sp) => sp.id !== spaceOf(ws));
  const label = applyWorkspaceLabel(ws, index, ws.name);
  const n = ws.tabs.reduce((k, t) => k + leaves(t.root).length, 0);
  const waiting = ws.tabs.reduce((k, t) => k + leaves(t.root).filter((p) => needsYou(p.sessionId ? sessions[p.sessionId] : undefined, unread)).length, 0);
  const rename = () => set({ dialog: { kind: "rename", target: "workspace", id: ws.id, value: ws.name } });
  return (
    <div className="mt-1">
      <ContextMenu>
        <ContextMenuTrigger asChild>
          {/* data-ws-id: dropping a dragged row here moves its tab into this workspace (TabDrag.tsx) */}
          <div
            data-ws-id={ws.id}
            role="button"
            className={cn(
              "group/ws flex h-7 cursor-default items-center gap-1 rounded-md px-1 text-[12px] select-none hover:bg-accent/60",
              "data-[state=open]:ring-1 data-[state=open]:ring-brand/60",
            )}
            onClick={onFold}
            onDoubleClick={rename}
            title={ws.cwd ? ws.cwd : undefined}
          >
            <ChevronRight className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", !folded && "rotate-90")} />
            <span className={cn("min-w-0 truncate font-semibold", current ? "text-foreground" : "text-foreground/80")}>{label}</span>
            {ws.cwd && basename(ws.cwd) !== label && <span className="min-w-0 truncate text-[11px] text-muted-foreground">{basename(ws.cwd)}</span>}
            <span className="flex-1" />
            {folded && waiting > 0 ? (
              <span className="shrink-0 rounded-full bg-bad px-1.5 text-[10px] leading-4 font-semibold text-white">{waiting} 等你</span>
            ) : (
              <span className="flex shrink-0 items-center gap-1.5 text-[11px] text-muted-foreground tabular-nums group-hover/ws:hidden">
                {folded && <WsDot w={ws} sessions={sessions} unread={unread} hideIdle />}
                {n}
              </span>
            )}
            <button
              className="hidden size-5 shrink-0 items-center justify-center rounded-sm text-muted-foreground group-hover/ws:flex hover:bg-muted hover:text-foreground"
              title="在这个分组里新开 tab"
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
            <Plus /> 新 tab
          </ContextMenuItem>
          <ContextMenuItem
            onClick={() => {
              toggleOverview(false);
              selectWorkspace(ws.id);
              openTerminal("tab");
            }}
          >
            <SquareTerminal /> 新终端
          </ContextMenuItem>
          <ContextMenuItem onClick={rename}>
            <Pencil /> 重命名
          </ContextMenuItem>
          {others.length > 0 && (
            <ContextMenuSub>
              <ContextMenuSubTrigger>移到项目</ContextMenuSubTrigger>
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
            <CircleX /> 关闭分组
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
      {!folded && <div className="pl-2">{children}</div>}
    </div>
  );
}

function Row({
  tab,
  pane,
  s,
  inSplit,
  unread,
  pinned,
  current,
  visible,
  activeTabId,
  where,
}: {
  tab: Tab;
  pane: PaneLeaf;
  s?: SessionSummary;
  inSplit: boolean;
  unread: boolean;
  pinned: boolean;
  current: boolean;
  visible: boolean;
  activeTabId: string;
  /** Shown instead of the directory (the filtered list, where groups are gone). */
  where?: string;
}) {
  const { title, sub } = paneLabel(pane, s);
  // An unnamed tab is named after its first agent: only a renamed tab adds something worth showing
  const tabLabel = !inSplit && tab.name && tab.name !== title ? tab.name : undefined;
  const close = () => (inSplit ? closePaneById(pane.id) : closeTab(tab.id));
  // Already in the tab on screen (alone or in its split): nothing to put beside it
  const canBeside = tab.id !== activeTabId;
  const openBeside = () => {
    // Into the tab on screen, on its right: a lone tab moves over whole, a pane of a split moves on its own
    toggleOverview(false);
    if (inSplit) movePane(pane.id, { tabId: activeTabId, side: "right" });
    else mergeTab(tab.id, activeTabId, undefined, "right");
  };
  const Icon = pane.term ? SquareTerminal : pane.file ? FileText : pane.sub ? Columns2 : SquareDashed;
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div
          role="button"
          data-row-current={current || undefined}
          // Drag onto a pane (split beside it) or onto another group (move the tab there): TabDrag.tsx
          onPointerDown={(e) => startPaneDrag(e, pane.id, { label: title, agent: s?.agent, term: !!pane.term })}
          onClick={() => goPane(pane.id, s?.id)}
          onDoubleClick={() => !inSplit && set({ dialog: { kind: "rename", target: "tab", id: tab.id, value: tab.name ?? tabName(tab) } })}
          onMouseDown={(e) => e.button === 1 && e.preventDefault()}
          onAuxClick={(e) => e.button === 1 && (e.preventDefault(), close())}
          className={cn(
            "group flex h-7 cursor-default items-center gap-2 rounded-md px-2 text-[12px] select-none",
            current ? "bg-accent font-medium" : visible ? "bg-accent/40" : "hover:bg-accent/60",
            inSplit && "pl-2.5",
            "data-[state=open]:ring-1 data-[state=open]:ring-brand/60",
          )}
        >
          {s ? <AgentIcon agent={s.agent} className="size-3.5 shrink-0" /> : <Icon className="size-3.5 shrink-0 text-muted-foreground" />}
          <span className={cn("min-w-0 flex-1 truncate", pane.term && "font-mono text-[11.5px]", !s && !pane.term && "text-muted-foreground")}>
            {tabLabel ? (
              <>
                {tabLabel} <span className="text-muted-foreground">· {title}</span>
              </>
            ) : (
              title
            )}
          </span>
          {pinned && <Pin className="size-3 shrink-0 text-muted-foreground" />}
          <span className="flex max-w-[40%] shrink-0 items-center text-[11px] text-muted-foreground group-hover:hidden">
            {s?.status === "running" && s.turn ? <Elapsed since={s.turn.startedAt} /> : <span className="truncate">{where ?? sub}</span>}
          </span>
          <button
            className="hidden size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground group-hover:flex hover:text-foreground"
            title={inSplit ? "关掉这一格" : "关闭 tab"}
            onClick={(e) => (e.stopPropagation(), close())}
          >
            <X className="size-3" />
          </button>
          {s && <StatusDot status={dotStatus(s)} unread={unread} />}
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        {s &&
          (pinned ? (
            <ContextMenuItem onClick={() => removeTile(s.id)}>
              <PinOff /> 取消 Pin
            </ContextMenuItem>
          ) : (
            <ContextMenuItem onClick={() => pin(s.id)}>
              <Pin /> Pin
            </ContextMenuItem>
          ))}
        {s && unread && (
          <ContextMenuItem onClick={() => markRead(s.id)}>
            <MailCheck /> 标为已读
          </ContextMenuItem>
        )}
        {canBeside && (
          <ContextMenuItem onClick={openBeside}>
            <SquareSplitHorizontal /> 在右侧分屏打开
          </ContextMenuItem>
        )}
        {inSplit && (
          <ContextMenuItem onClick={() => (toggleOverview(false), paneToTab(pane.id))}>
            <Columns2 /> 拆成单独的 tab
          </ContextMenuItem>
        )}
        {!inSplit && (
          <ContextMenuItem onClick={() => set({ dialog: { kind: "rename", target: "tab", id: tab.id, value: tab.name ?? tabName(tab) } })}>
            <Pencil /> 重命名 tab
          </ContextMenuItem>
        )}
        {s && (
          <ContextMenuItem onClick={() => set({ dialog: { kind: "rename", target: "session", id: s.id, value: s.title ?? "" } })}>
            <Pencil /> 重命名会话
          </ContextMenuItem>
        )}
        <ContextMenuSeparator />
        <ContextMenuItem variant="destructive" onClick={close}>
          <CircleX /> {inSplit ? "关掉这一格" : "关闭 tab"}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}
