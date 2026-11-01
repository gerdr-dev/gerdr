import {
  ChevronRight,
  CircleX,
  Columns2,
  Ellipsis,
  Eraser,
  ListPlus,
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
  focusSession,
  mergeTab,
  movePane,
  moveWorkspaceToSpace,
  openTerminal,
  paneToTab,
  selectWorkspace,
  tabName,
} from "@/lib/actions";
import { applyWorkspaceLabel } from "@/lib/addons";
import {
  activeWs,
  currentSpace,
  leaves,
  locateSession,
  sideRows,
  spaceList,
  spaceOf,
  spaceWorkspaces,
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
  workChats,
} from "@/lib/overview";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { markRead, set, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { cn } from "@/lib/utils";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";
import { Elapsed, SidebarBottom, SidebarEdge, SidebarHeader, WsDot } from "./Chrome";
import { TabDragLayer, startPaneDrag } from "./TabDrag";

/*
 * The sidebar (on a phone, the drawer). One list in place of four things that each listed the same agents:
 * the workspace switcher, the tab bar, the agents list and the to-do view.
 *
 * - Every workspace of the project is a group, all shown at once (no switching), each collapsible. A group lists one row
 *   per pane, each on its own: panes split together on screen are not tied together here. Where a row sits (group and
 *   order) is the sidebar's own (Workspace.rows, lib/layout.ts sideRows); dragging it never touches the split on screen.
 *   Panes shown next to the current one are tinted lightly.
 * - "Active" above the groups replaces both the to-do list and the agents list. Who's in is overviewState.activeIds,
 *   shared with the overview: what needs you or runs joins on its own, anything else can be added by
 *   hand; flat in the agents list's order. Each project chooses (the section's ⋯ menu) whether it lists every project's
 *   agents. Its grid (the overview's, lib/overview.ts) opens over the main area next to this list. The groups below still
 *   hold everything, so these rows show twice: the top is where to look, the groups are where things live.
 *
 * Kept out of Chrome.tsx so the experiment can be dropped (or promoted) by touching a few lines in App.tsx.
 */

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

/** `className`: the phone drawer, which sets its own width (no resize edge there). */
export function UnifiedSidebar({ className }: { className?: string }) {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const sidebarW = useStore((s) => s.sidebarW);
  const gridOn = useStore((s) => s.overview.on);
  const gridFocus = useStore((s) => s.overview.focus);
  useStore((s) => s.addonVersion);
  const tiles = useOverviewIds().split(",").filter(Boolean);
  const activeIds = useActiveIds().split(",").filter(Boolean);
  // Counts for the ⋯ menu; recomputed on every render of the sidebar, which already follows sessions and the overview
  const doneN = useStore((s) => doneActiveIds(s).length);
  const outN = useStore((s) => workingOutIds(s).length);
  const allProjects = useStore((s) => activeAllProjects(s));
  const [folded, setFolded] = useFolded();
  const [activeFolded, setActiveFolded] = useLocal(ACTIVE_FOLD_KEY, false);
  const list = spaceWorkspaces(layout);
  const groups = sideRows(layout);
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

  const spaces = spaceList(layout);
  const here = currentSpace(layout);
  // "active" can reach other projects (allProjects) and Work conversations (no pane): find each one's place
  const active = activeIds.map((id) => {
    const loc = locateSession(layout, id);
    if (!loc) return { id };
    const item: Item = { ws: loc.ws, tab: loc.tab, pane: loc.pane, split: leaves(loc.tab.root).length > 1, s: sessions[id] };
    return { id, item };
  });
  const wsLabel = (ws: Workspace) => {
    const i = list.indexOf(ws);
    const label = i >= 0 ? applyWorkspaceLabel(ws, i, ws.name) : ws.name;
    return spaceOf(ws) === here ? label : `${spaces.find((sp) => sp.id === spaceOf(ws))?.name ?? spaceOf(ws)} · ${label}`;
  };
  const gridFocusId = gridFocus && tiles.includes(gridFocus) ? gridFocus : tiles[0];

  const flatRow = (x: Item, top = false) => {
    const p = rowProps(x.ws, x.tab, x.pane, x.split);
    const id = x.s?.id;
    return (
      <Row
        key={x.pane.id}
        {...p}
        where={wsLabel(x.ws)}
        top={top}
        // In the grid, a row of "active" picks its tile instead of leaving the grid
        current={top && gridOn ? id === gridFocusId : p.current}
        onPick={top && gridOn && id && tiles.includes(id) ? () => (focusTile(id), markRead(id), set({ drawer: false })) : undefined}
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
      onActive: (on: boolean) => s && (on ? addActive([s.id]) : removeActive(s.id)),
      // In a split, the focused pane is the current one; its neighbours get a lighter tint (they're on screen too)
      current: here && (!inSplit || tab.focus === pane.id),
      visible: here && inSplit,
      activeTabId: cur.activeTab,
    };
  };

  return (
    <div className={cn("relative flex h-full shrink-0 flex-col border-r bg-sidebar", className)} style={className ? undefined : { width: sidebarW }}>
      {!className && <SidebarEdge />}
      <SidebarHeader />
      <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
        <div className="pt-1.5">
          {(active.length > 0 || gridOn || allProjects) && (
            <div className="mb-1 border-b pb-1.5">
              <div
                role="button"
                className="group/act flex h-7 w-full cursor-default items-center gap-1 rounded-md px-1 text-[11px] font-medium text-muted-foreground select-none hover:bg-accent/60 hover:text-foreground"
                title="等你、在跑、报错、跑完没看的，和你右键加进来的；按该不该先看排"
                onClick={() => setActiveFolded(!activeFolded)}
              >
                <ChevronRight className={cn("size-3.5 shrink-0 transition-transform", !activeFolded && "rotate-90")} />
                <span className="flex-1 truncate text-left">进行中{allProjects && <span className="font-normal"> · 所有项目</span>}</span>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <button
                      className="flex size-5 items-center justify-center rounded-sm hover:bg-muted hover:text-foreground"
                      title="进行中的设置"
                      onClick={(e) => e.stopPropagation()}
                    >
                      <Ellipsis className="size-3.5" />
                    </button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
                    <DropdownMenuCheckboxItem checked={allProjects} onCheckedChange={(v) => setActiveAllProjects(!!v)}>
                      显示所有项目的 agent
                    </DropdownMenuCheckboxItem>
                    <div className="px-2 pb-1 text-[11px] text-muted-foreground">只改这个项目，别的项目各自设</div>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem disabled={!outN} onClick={restoreWorking}>
                      <ListPlus /> 移入所有在做的{outN ? `（${outN}）` : ""}
                    </DropdownMenuItem>
                    <DropdownMenuItem disabled={!doneN} onClick={clearActive}>
                      <Eraser /> 移出已完成的{doneN ? `（${doneN}）` : ""}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
                <button
                  className={cn("flex h-5 items-center gap-1 rounded-sm px-1", gridOn ? "bg-brand/15 text-foreground" : "hover:bg-muted hover:text-foreground")}
                  title={gridOn ? "关掉网格，回到 tab" : `把进行中的 agent 摆成网格一起看（最多 ${MAX_TILES} 个）`}
                  // On a phone the grid is behind the drawer: close it
                  onClick={(e) => (e.stopPropagation(), toggleOverview(!gridOn), set({ drawer: false }))}
                >
                  <LayoutGrid className={cn("size-3", gridOn && "text-brand")} />
                  网格
                </button>
              </div>
              {!activeFolded &&
                active.map((x) =>
                  x.item ? (
                    flatRow(x.item, true)
                  ) : (
                    <WorkRow key={x.id} id={x.id} current={gridOn && x.id === gridFocusId} grid={gridOn && tiles.includes(x.id)} />
                  ),
                )}
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
              rows={groups.get(ws.id) ?? []}
            >
              {/* One row per pane, wherever it was dragged to (layout.sideRows): the panes of a split aren't tied together here,
                  the ones on screen just light up. data-side-row(s): drag a row between two others to move it (TabDrag.tsx) */}
              {groups.get(ws.id)?.map((r) => (
                <div key={r.pane.id} data-side-row={r.pane.id}>
                  <Row {...rowProps(r.ws, r.tab, r.pane, leaves(r.tab.root).length > 1)} />
                </div>
              ))}
              {!groups.get(ws.id)?.length && <div className="py-1 pl-7 text-[12px] text-muted-foreground">空的</div>}
            </Group>
          ))}
        </div>
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

/** A Work conversation in "active": no pane, so no group or tab; going to it opens Work mode (actions.focusSession). */
function WorkRow({ id, current, grid }: { id: string; current: boolean; grid: boolean }) {
  const s = useStore((st) => st.sessions[id]);
  const unread = useStore((st) => !!st.unread[id]);
  const project = useStore((st) => {
    const p = Object.entries(workChats(st.config, st.layout)).find(([, x]) => x === id)?.[0];
    return spaceList(st.layout).find((sp) => sp.id === p)?.name ?? p;
  });
  if (!s) return null;
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div
          role="button"
          data-row-current={current || undefined}
          className={cn(
            "group flex h-7 cursor-default items-center gap-2 rounded-md px-2 text-[12px] select-none",
            current ? "bg-accent font-medium" : "hover:bg-accent/60",
            "data-[state=open]:ring-1 data-[state=open]:ring-brand/60",
          )}
          onClick={() => (grid ? (focusTile(id), markRead(id)) : focusSession(id))}
          onMouseDown={(e) => e.button === 1 && e.preventDefault()}
          onAuxClick={(e) => e.button === 1 && (e.preventDefault(), removeActive(id))}
        >
          <AgentIcon agent={s.agent} className="size-3.5 shrink-0" />
          <span className="min-w-0 flex-1 truncate">{s.title || basename(s.cwd)}</span>
          <span className="max-w-[40%] shrink-0 truncate text-[11px] text-muted-foreground group-hover:hidden">Work{project ? ` · ${project}` : ""}</span>
          <button
            className="hidden size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground group-hover:flex hover:text-foreground"
            title="移出进行中"
            onClick={(e) => (e.stopPropagation(), removeActive(id))}
          >
            <X className="size-3" />
          </button>
          <StatusDot status={dotStatus(s)} unread={unread} />
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onClick={() => focusSession(id)}>
          <ChevronRight /> 去 Work 模式
        </ContextMenuItem>
        <ContextMenuItem onClick={() => removeActive(id)}>
          <PinOff /> 移出进行中
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
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
  rows,
  children,
}: {
  ws: Workspace;
  index: number;
  folded: boolean;
  onFold: () => void;
  sessions: Record<string, SessionSummary>;
  unread: Record<string, true>;
  current: boolean;
  rows: SideRow[];
  children: React.ReactNode;
}) {
  const layout = useStore((s) => s.layout);
  const others = spaceList(layout).filter((sp) => sp.id !== spaceOf(ws));
  const label = applyWorkspaceLabel(ws, index, ws.name);
  const n = rows.length;
  const waiting = rows.filter((r) => needsYou(r.pane.sessionId ? sessions[r.pane.sessionId] : undefined)).length;
  const edit = () => set({ dialog: { kind: "editWorkspace", id: ws.id } });
  return (
    <div className="mt-1">
      <ContextMenu>
        <ContextMenuTrigger asChild>
          {/* data-ws-id: dropping a sidebar row here lists it at the end of this group; a tab from the tab bar moves here (TabDrag.tsx) */}
          <div
            data-ws-id={ws.id}
            role="button"
            className={cn(
              "group/ws flex h-7 cursor-default items-center gap-1 rounded-md px-1 text-[12px] select-none hover:bg-accent/60",
              "data-[state=open]:ring-1 data-[state=open]:ring-brand/60",
            )}
            onClick={onFold}
            onDoubleClick={edit}
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
          <ContextMenuItem onClick={edit}>
            <Pencil /> 编辑…
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
      {!folded && (
        <div className="pl-2" data-side-rows={ws.id}>
          {children}
        </div>
      )}
    </div>
  );
}

function Row({
  tab,
  pane,
  s,
  inSplit,
  unread,
  inActive,
  onActive,
  top,
  onPick,
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
  inActive: boolean;
  onActive: (on: boolean) => void;
  /** A row of the "active" section: its ✕ and middle click take it off the section instead of closing the tab. */
  top?: boolean;
  /** Replaces going to the pane (the grid picks a tile). */
  onPick?: () => void;
  current: boolean;
  visible: boolean;
  activeTabId: string;
  /** Shown instead of the directory (rows of "active", out of their group). */
  where?: string;
}) {
  const { title, sub } = paneLabel(pane, s);
  // An unnamed tab is named after its first agent: only a renamed tab adds something worth showing
  const tabLabel = !inSplit && tab.name && tab.name !== title ? tab.name : undefined;
  const close = () => (inSplit ? closePaneById(pane.id) : closeTab(tab.id));
  const dismiss = top && s ? () => onActive(false) : close;
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
          onClick={() => (onPick ? onPick() : goPane(pane.id, s?.id))}
          onDoubleClick={() => !inSplit && set({ dialog: { kind: "rename", target: "tab", id: tab.id, value: tab.name ?? tabName(tab) } })}
          onMouseDown={(e) => e.button === 1 && e.preventDefault()}
          onAuxClick={(e) => e.button === 1 && (e.preventDefault(), dismiss())}
          className={cn(
            "group flex h-7 cursor-default items-center gap-2 rounded-md px-2 text-[12px] select-none",
            current ? "bg-accent font-medium" : visible ? "bg-accent/40" : "hover:bg-accent/60",
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
          <span className="flex max-w-[40%] shrink-0 items-center text-[11px] text-muted-foreground group-hover:hidden">
            {s?.status === "running" && s.turn ? <Elapsed since={s.turn.startedAt} /> : <span className="truncate">{where ?? sub}</span>}
          </span>
          <button
            className="hidden size-4 shrink-0 items-center justify-center rounded-sm text-muted-foreground group-hover:flex hover:text-foreground"
            title={top && s ? "移出进行中" : inSplit ? "关掉这一格" : "关闭 tab"}
            onClick={(e) => (e.stopPropagation(), dismiss())}
          >
            <X className="size-3" />
          </button>
          {s && <StatusDot status={dotStatus(s)} unread={unread} />}
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        {s &&
          (inActive ? (
            <ContextMenuItem onClick={() => onActive(false)}>
              <PinOff /> 移出进行中
            </ContextMenuItem>
          ) : (
            <ContextMenuItem onClick={() => onActive(true)}>
              <Pin /> 加入进行中
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
