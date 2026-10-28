import { Keyboard, LayoutList, Menu, PanelLeft, Plus, Settings, SquareTerminal, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { addTab, basename, closeTab, closeWorkspace, duplicateTab, focusSession, moveWorkspace, selectWorkspace, tabName } from "@/lib/actions";
import { formatCombo } from "@/lib/keys";
import { activeWs, leaves, type Tab, type Workspace } from "@/lib/layout";
import { applyTabLabel, applyWorkspaceLabel } from "@/lib/plugins";
import { focusedSession, get, keyFor, set, setLayout, toggleSidebar, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { compareSessions } from "@/lib/order";
import { cn } from "@/lib/utils";
import { AgentIcon, StatusDot } from "./AgentIcon";
import { fmtElapsed } from "./chat/StatusLine";
import { TabDragLayer, useTabDrag } from "./TabDrag";
import { UsageMeter } from "./UsageMeter";
import { isAndroid } from "@/lib/android";
import { useRoute } from "./AndroidSettings";

/** 一个 tab 的汇总状态：里面任何一个会话在等你 → 等你；在跑 → 在跑；…… */
export function tabStatus(tab: Tab, sessions: Record<string, SessionSummary>, unread: Record<string, true>) {
  let rank = 9;
  let status: string | undefined;
  let isUnread = false;
  for (const p of leaves(tab.root)) {
    const s = p.sessionId ? sessions[p.sessionId] : undefined;
    if (!s) continue;
    const r = s.status === "waiting" ? 0 : s.status === "error" ? 1 : s.status === "running" ? 2 : unread[s.id] ? 3 : 9;
    if (r < rank) {
      rank = r;
      status = s.status;
      isUnread = !!unread[s.id];
    }
  }
  return { status, unread: isUnread };
}

export function TabBar() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const sidebar = useStore((s) => s.sidebar);
  const prefixActive = useStore((s) => s.prefixActive);
  const prefix = useStore((s) => s.config.prefix);
  useStore((s) => s.pluginVersion);
  const ws = activeWs(layout);
  const barRef = useRef<HTMLDivElement>(null);
  const { drag, onPointerDown } = useTabDrag(ws, barRef);
  const flush = sidebar;
  return (
    // 胶囊式标签：当前的是一块亮一点的圆角底（bg-accent），别的不画底、悬停时浮起一块淡的。
    // 原来是 Chrome 那种（当前标签和下面内容同色、连成一片），可深色下栏底和内容底色只差一点（0.14 / 0.165），
    // 看不出哪个是当前的；加粗 + 压暗别的、顶上加品牌色细线都试过，还是不明显 / 不好看（用户报的），所以改成有底色的胶囊。
    // 对齐：侧栏开着时栏左边留 6px、标签 px-1.5，图标落在 12px —— 和下面 pane 标题栏（px-3 + 16px 图标 + gap-2）上下对齐。
    // 侧栏收起时左边是展开侧栏的按钮，本来就对不齐，照旧留空。
    <div className="flex h-10 shrink-0 items-center gap-1 border-b bg-sidebar px-1.5" style={{ WebkitAppRegion: "drag" } as any}>
      {!sidebar && (
        <Button variant="ghost" size="icon" className="size-7" onClick={toggleSidebar} style={{ WebkitAppRegion: "no-drag" } as any}>
          <PanelLeft className="size-4" />
        </Button>
      )}
      <div
        ref={barRef}
        className={cn("flex min-w-0 flex-1 items-center gap-1 overflow-x-auto [scrollbar-width:none]", flush ? "pr-2" : "px-2")}
        style={{ WebkitAppRegion: "no-drag" } as any}
      >
        {ws.tabs.map((t, i) => {
          const active = t.id === ws.activeTab;
          const st = tabStatus(t, sessions, unread);
          const label = applyTabLabel(t, i, ws, tabName(t));
          // 图标和 pane 标题栏的一样：会话是 agent 图标，终端是终端图标（终端原来没图标，标签文字就对到了标题栏的图标上）
          const first = leaves(t.root).find((p) => p.sessionId || p.term);
          const agent = first?.sessionId ? sessions[first.sessionId]?.agent : undefined;
          return (
            <ContextMenu key={t.id}>
              <ContextMenuTrigger asChild>
                <div
                  data-tab-id={t.id}
                  // 拖动排序 / 合并 / 挪到别的工作空间，见 TabDrag.tsx
                  onPointerDown={(e) => onPointerDown(e, t.id, i, { label, agent, term: !!first?.term })}
                  onClick={() =>
                    setLayout((l) => ({
                      ...l,
                      workspaces: l.workspaces.map((w) =>
                        w.id === ws.id ? { ...w, activeTab: t.id, lastTab: w.activeTab === t.id ? w.lastTab : w.activeTab } : w,
                      ),
                    }))
                  }
                  onDoubleClick={() => set({ dialog: { kind: "rename", target: "tab", id: t.id, value: t.name ?? tabName(t) } })}
                  // 中键关（照浏览器）。mousedown 里先拦掉，不然 mac 上中键按下会进「自动滚动」
                  onMouseDown={(e) => e.button === 1 && e.preventDefault()}
                  onAuxClick={(e) => {
                    if (e.button !== 1) return;
                    e.preventDefault();
                    closeTab(t.id);
                  }}
                  className={cn(
                    "group/tab flex h-7 w-52 min-w-[88px] shrink cursor-default items-center gap-2 rounded-md px-1.5 text-[12px] select-none",
                    active ? "bg-accent font-medium text-foreground" : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
                    drag?.tabId === t.id && "opacity-40",
                  )}
                >
                  {agent ? (
                    <AgentIcon agent={agent} className={cn("size-4", !active && "opacity-60")} />
                  ) : (
                    first?.term && <SquareTerminal className="size-4 shrink-0 text-muted-foreground" />
                  )}
                  <span className="min-w-0 flex-1 truncate">{label}</span>
                  <StatusDot status={st.status} unread={st.unread} />
                  {/* 关闭键一直占着位置（照 Chrome）：当前 tab 一直显示，别的 hover 才显示。原来是 hover 时才插进来，
                      tab 当场变宽、后面的 tab 全跟着挪 —— 鼠标划过 tab 栏就是一路闪动（用户报的） */}
                  <button
                    className={cn("-mr-1 rounded-sm p-0.5 hover:bg-muted", active ? "visible" : "invisible group-hover/tab:visible")}
                    onClick={(e) => {
                      e.stopPropagation();
                      closeTab(t.id);
                    }}
                  >
                    <X className="size-3" />
                  </button>
                </div>
              </ContextMenuTrigger>
              <ContextMenuContent>
                <ContextMenuItem onClick={() => set({ dialog: { kind: "rename", target: "tab", id: t.id, value: t.name ?? tabName(t) } })}>
                  重命名
                </ContextMenuItem>
                <ContextMenuItem onClick={() => duplicateTab(t.id, "after")}>复制到后面</ContextMenuItem>
                <ContextMenuItem onClick={() => duplicateTab(t.id, "before")}>复制到前面</ContextMenuItem>
                <ContextMenuSeparator />
                <ContextMenuItem variant="destructive" onClick={() => closeTab(t.id)}>
                  关闭
                </ContextMenuItem>
              </ContextMenuContent>
            </ContextMenu>
          );
        })}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" className="size-7 shrink-0" onClick={() => addTab()}>
              <Plus className="size-4" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>新建 tab（{formatCombo(keyFor(get().config, "newTab")[0] ?? "")}）</TooltipContent>
        </Tooltip>
      </div>
      {prefixActive && (
        <div className="shrink-0 rounded-md bg-brand px-2 py-0.5 font-mono text-[11px] text-white" style={{ WebkitAppRegion: "no-drag" } as any}>
          {formatCombo(prefix).replace(/\s.*/, "")} …
        </div>
      )}
      <TabDragLayer drag={drag} />
    </div>
  );
}

/**
 * 手机上代替 tab 栏的那一行：☰（侧栏抽屉）· 当前在哪（点开面板一览）· 面板一览 · 新 tab。
 *
 * 手机上一次只看一个 pane，tab 栏那一排小标签既点不准也放不下，所以换 tab / 换 pane 全交给面板一览（PaneSwitcher）；
 * 这一行只说「我现在在哪」和「有几个在等我」—— 后者是角标，因为它在别的 tab 里时屏幕上没有任何别的地方看得见。
 */
export function PhoneBar() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const switcher = useStore((s) => s.switcher);
  const connected = useStore((s) => s.connected);
  useStore((s) => s.pluginVersion);
  const ws = activeWs(layout);
  const wi = layout.workspaces.indexOf(ws);
  const ti = ws.tabs.findIndex((t) => t.id === ws.activeTab);
  const tab = ws.tabs[ti] ?? ws.tabs[0];
  const st = tabStatus(tab, sessions, unread);
  const waiting = Object.values(sessions).filter((s) => s.status === "waiting").length;
  const openSwitcher = () => {
    // 先把键盘收掉：键盘占着半个屏时，点面板里第一下只会收键盘
    (document.activeElement as HTMLElement | null)?.blur?.();
    set({ switcher: !switcher, drawer: false });
  };
  return (
    <div className="flex h-11 shrink-0 items-center gap-1 border-b bg-sidebar px-1.5">
      <Button variant="ghost" size="icon" className="size-9" aria-label="侧栏" onClick={() => set({ drawer: true, switcher: false })}>
        <Menu className="size-[18px]" />
      </Button>
      <button className="flex h-9 min-w-0 flex-1 items-center gap-1.5 rounded-md px-1.5 text-left" onClick={openSwitcher}>
        <span className="min-w-0 truncate text-[13px] font-medium">{applyTabLabel(tab, ti, ws, tabName(tab))}</span>
        {/* 只有一个 pane 时不画：下面 pane 标题栏那个点就是同一个会话，一行里三个点太乱。好几个 pane 时它是整个 tab 的汇总，留着 */}
        {leaves(tab.root).length > 1 && <StatusDot status={st.status} unread={st.unread} />}
        <span className="min-w-0 shrink-[2] truncate text-[11px] text-muted-foreground">{applyWorkspaceLabel(ws, wi, ws.name)}</span>
        {!connected && <span className="shrink-0 text-[11px] text-bad">离线</span>}
      </button>
      <Button variant={switcher ? "secondary" : "ghost"} size="icon" className="relative size-9" aria-label="面板一览" onClick={openSwitcher}>
        <LayoutList className="size-[18px]" />
        {waiting > 0 && (
          <span className="absolute top-1 right-1 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-bad px-0.5 text-[9px] leading-none font-semibold text-white tabular-nums">
            {waiting}
          </span>
        )}
      </Button>
      <Button variant="ghost" size="icon" className="size-9" aria-label="新建 tab" onClick={() => addTab()}>
        <Plus className="size-[18px]" />
      </Button>
    </div>
  );
}

function RouteLabel() {
  const r = useRoute();
  return (
    <span title={r.detail} className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">
      <span className={cn("size-1.5 rounded-full", r.via === "lan" ? "bg-ok" : "bg-brand")} />
      {r.via === "lan" ? "局域网" : "中继"}
    </span>
  );
}

export function Sidebar({ className }: { className?: string }) {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const connected = useStore((s) => s.connected);
  useStore((s) => s.pluginVersion);
  const [drag, setDrag] = useState<number | null>(null);
  return (
    <div className={cn("flex h-full w-56 shrink-0 flex-col border-r bg-sidebar", className)}>
      <div className="flex h-10 shrink-0 items-center gap-1 border-b px-2" style={{ WebkitAppRegion: "drag" } as any}>
        <div className="native-traffic-light-gap" />
        <span className="flex-1 text-[12px] font-semibold tracking-wide text-muted-foreground">gerdr</span>
        {!connected && <span className="text-[11px] text-bad">离线</span>}
        {/* 安卓 App 里走哪条路（详情在设置 → 通用 → 安卓 App）。原来是顶栏上的一个绿 / 蓝点，和状态点挤在一起分不清，挪到侧栏里写成字 */}
        {connected && isAndroid() && <RouteLabel />}
        <Button variant="ghost" size="icon" className="size-7" onClick={toggleSidebar} style={{ WebkitAppRegion: "no-drag" } as any}>
          <PanelLeft className="size-4" />
        </Button>
      </div>
      <div className="max-h-[45%] shrink-0 overflow-y-auto px-1.5 py-2">
        {layout.workspaces.map((w, wi) => (
          <WorkspaceItem
            key={w.id}
            w={w}
            index={wi}
            active={w.id === layout.active}
            sessions={sessions}
            unread={unread}
            onDragStart={() => setDrag(wi)}
            onDrop={() => {
              if (drag != null && drag !== wi) moveWorkspace(drag, wi);
              setDrag(null);
            }}
          />
        ))}
        <button
          className="mt-1 flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-[12px] text-muted-foreground hover:bg-accent"
          onClick={() => set({ dialog: { kind: "newWorkspace" } })}
        >
          <Plus className="size-3.5" /> 新工作空间
        </button>
      </div>
      <AgentList />
      <UsageMeter />
      <div className="flex shrink-0 items-center gap-1 border-t p-1.5">
        <Button
          variant="ghost"
          size="sm"
          className="h-7 flex-1 justify-start gap-2 text-[12px] text-muted-foreground"
          onClick={() => set({ dialog: { kind: "settings" } })}
        >
          <Settings className="size-3.5" /> 设置
        </Button>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" className="size-7 text-muted-foreground" onClick={() => set({ dialog: { kind: "help" } })}>
              <Keyboard className="size-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>快捷键</TooltipContent>
        </Tooltip>
      </div>
    </div>
  );
}

/**
 * 侧栏下半截：所有开着的 agent（= 界面上有 pane 显示的会话），跨工作空间，和 herdr 侧栏的 agents 一样。
 * 排序见 lib/order.ts（和手机上的面板一览共用）：在等你的 → 出错的 → 跑完没看的 → 在跑的 → 闲着的。
 */
function AgentList() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const focused = focusedSession(layout);
  const rows: { s: SessionSummary; where: string }[] = [];
  const seen = new Set<string>();
  layout.workspaces.forEach((w, wi) =>
    w.tabs.forEach((t, ti) => {
      for (const p of leaves(t.root)) {
        const s = p.sessionId ? sessions[p.sessionId] : undefined;
        if (!s || seen.has(s.id)) continue;
        seen.add(s.id);
        rows.push({ s, where: `${applyWorkspaceLabel(w, wi, w.name)} · ${ti + 1}.${tabName(t)}` });
      }
    }),
  );
  // 和手机上的面板一览同一套顺序（lib/order.ts）；分不出来时保持界面上的先后（sort 是稳定的）
  rows.sort((a, b) => compareSessions(a.s, b.s, unread));
  return (
    <div className="flex min-h-0 flex-1 flex-col border-t">
      <div className="flex shrink-0 items-center px-3.5 pt-2 pb-1 text-[11px] font-medium text-muted-foreground">
        <span className="flex-1">agents</span>
        <span className="tabular-nums">{rows.length}</span>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 pb-2">
        {rows.length === 0 && <div className="px-2 py-1 text-[12px] text-muted-foreground">没有开着的会话</div>}
        {rows.map(({ s, where }) => (
          <button
            key={s.id}
            className={cn("flex w-full items-start gap-2 rounded-md px-2 py-1 text-left hover:bg-accent/60", s.id === focused && "bg-accent")}
            onClick={() => focusSession(s.id)}
          >
            <AgentIcon agent={s.agent} className="mt-0.5 size-3.5 shrink-0" />
            <div className="min-w-0 flex-1">
              <div className="truncate text-[12px] leading-5">{s.title || basename(s.cwd)}</div>
              <div className="flex text-[11px] leading-4 text-muted-foreground">
                <span className="min-w-0 truncate">{where}</span>
                {s.status === "running" && s.turn && <Elapsed since={s.turn.startedAt} />}
              </div>
            </div>
            <StatusDot status={s.status} unread={!!unread[s.id]} className="mt-1.5" />
          </button>
        ))}
      </div>
    </div>
  );
}

// 在跑的这一轮跑了多久（和对话里状态行的用时同一个数）。单独一个组件自己每秒走，整张列表不跟着每秒重画。
function Elapsed({ since }: { since: number }) {
  const [, tick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => tick((x) => x + 1), 1000);
    return () => clearInterval(t);
  }, []);
  return <span className="shrink-0 tabular-nums">&nbsp;· {fmtElapsed(Date.now() - since)}</span>;
}

function WorkspaceItem({
  w,
  index,
  active,
  sessions,
  unread,
  onDragStart,
  onDrop,
}: {
  w: Workspace;
  index: number;
  active: boolean;
  sessions: Record<string, SessionSummary>;
  unread: Record<string, true>;
  onDragStart: () => void;
  onDrop: () => void;
}) {
  const label = applyWorkspaceLabel(w, index, w.name);
  return (
    <div className="mb-1" data-ws-id={w.id} draggable onDragStart={onDragStart} onDragOver={(e) => e.preventDefault()} onDrop={onDrop}>
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <button
            className={cn(
              "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px]",
              active ? "bg-accent font-medium" : "hover:bg-accent/60",
            )}
            onClick={() => selectWorkspace(w.id)}
            onDoubleClick={() => set({ dialog: { kind: "rename", target: "workspace", id: w.id, value: w.name } })}
          >
            <span className="w-4 shrink-0 text-center font-mono text-[11px] text-muted-foreground">{index + 1}</span>
            <span className="min-w-0 flex-1 truncate">{label}</span>
            <WsDot w={w} sessions={sessions} unread={unread} />
          </button>
        </ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuItem onClick={() => set({ dialog: { kind: "rename", target: "workspace", id: w.id, value: w.name } })}>重命名</ContextMenuItem>
          <ContextMenuItem onClick={() => addTab({ workspaceId: w.id })}>新建 tab</ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem variant="destructive" onClick={() => closeWorkspace(w.id)}>
            关闭工作空间
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
    </div>
  );
}

/** idle：没状态时也占着位置（透明的点；侧栏里靠它和别的行对齐）。传 hideIdle 就不画（面板一览的标签里，占位只是一块空白） */
export function WsDot({
  w,
  sessions,
  unread,
  hideIdle,
}: {
  w: Workspace;
  sessions: Record<string, SessionSummary>;
  unread: Record<string, true>;
  hideIdle?: boolean;
}) {
  let best: { status?: string; unread: boolean } = { unread: false };
  let rank = 9;
  for (const t of w.tabs) {
    const st = tabStatus(t, sessions, unread);
    const r = st.status === "waiting" ? 0 : st.status === "error" ? 1 : st.status === "running" ? 2 : st.unread ? 3 : 9;
    if (r < rank) {
      rank = r;
      best = st;
    }
  }
  if (hideIdle && rank === 9) return null;
  return <StatusDot status={best.status} unread={best.unread} />;
}
