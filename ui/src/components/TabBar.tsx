import { PanelLeft, Plus, SquareTerminal, X } from "lucide-react";
import { useRef } from "react";
import { Button } from "@/components/ui/button";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { addTab, closeTab, duplicateTab, tabName } from "@/lib/actions";
import { formatCombo } from "@/lib/keys";
import { activeWs, leaves, type Tab } from "@/lib/layout";
import { applyTabLabel } from "@/lib/addons";
import { get, keyFor, set, setLayout, toggleSidebar, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { cn } from "@/lib/utils";
import { AgentIcon, StatusDot, dotStatus, dotRank } from "./AgentIcon";
import { TabDragLayer, useTabDrag } from "./TabDrag";
import { ConnStatus } from "./ConnStatus";

/** 一个 tab 的汇总状态：里面任何一个会话在等你 → 等你；在跑 → 在跑；…… */
export function tabStatus(tab: Tab, sessions: Record<string, SessionSummary>, unread: Record<string, true>) {
  let rank = 9;
  let status: string | undefined;
  let isUnread = false;
  for (const p of leaves(tab.root)) {
    const s = p.sessionId ? sessions[p.sessionId] : undefined;
    if (!s) continue;
    const r = dotRank(dotStatus(s), !!unread[s.id]);
    if (r < rank) {
      rank = r;
      status = dotStatus(s);
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
  useStore((s) => s.addonVersion);
  const ws = activeWs(layout);
  const barRef = useRef<HTMLDivElement>(null);
  const { drag, onPointerDown } = useTabDrag(ws, barRef);
  const flush = sidebar;
  // The tabs are prefix+1–9 (⌘1–9 went to "active", lib/modHeld.ts): number them once the prefix is pressed
  const numbers = prefixActive;
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
        data-tab-bar
        className={cn("flex min-w-0 flex-1 items-center gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden", flush ? "pr-2" : "px-2")}
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
                    // A size container so the tab can drop its icon / close button when squeezed (see below)
                    "group/tab @container/tab relative flex h-7 w-52 min-w-[44px] shrink cursor-default items-center gap-2 rounded-md px-1.5 text-[12px] select-none @max-[8rem]/tab:gap-1",
                    active ? "bg-accent font-medium text-foreground" : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
                    drag?.tabId === t.id && "opacity-40",
                    // Mark which tab the context menu belongs to while it's open (Radix sets data-state on the trigger)
                    "data-[state=open]:bg-accent data-[state=open]:text-foreground data-[state=open]:ring-1 data-[state=open]:ring-brand/60",
                  )}
                >
                  {/* Crowded (narrow tab): text only, like Chrome — the name is what tells tabs apart, the icon just eats it. */}
                  {(agent || first?.term) && (
                    <span className="flex size-4 shrink-0 items-center justify-center @max-[8rem]/tab:hidden">
                      {agent ? (
                        <AgentIcon agent={agent} className={cn("size-4", !active && "opacity-60")} />
                      ) : (
                        <SquareTerminal className="size-4 shrink-0 text-muted-foreground" />
                      )}
                    </span>
                  )}
                  {/* Names that don't fit fade out at the right edge instead of ending in "…" (like Chrome): an ellipsis costs three
                      characters' room, which is most of a crowded tab. A short name never reaches the fade, so it is always on. */}
                  <span className="min-w-0 flex-1 overflow-hidden whitespace-nowrap [mask-image:linear-gradient(to_right,black_calc(100%-1rem),transparent)]">
                    {label}
                  </span>
                  {/* After the prefix key, the tab's prefix+1–9 number lies on a veil at the tab's left edge — over the icon when there
                      is one, over the start of the name when not (no icon, or crowded). It used to live in the icon's slot and conjure
                      that slot up when missing, which only worked for some tabs and shoved the names over on every ⌘ press. As an
                      overlay it shows on every tab and moves nothing; the name stays plain for plugins / renames. */}
                  {numbers && i < 9 && (
                    <span className="absolute top-1 left-1 flex size-5 items-center justify-center rounded-sm bg-background text-[12px] font-medium text-foreground/80 tabular-nums ring-1 ring-border">
                      {i + 1}
                    </span>
                  )}
                  {/* No dot on an idle tab: StatusDot draws it transparent, which still took 8px + the gap from the name */}
                  {(st.status || st.unread) && <StatusDot status={st.status} unread={st.unread} />}
                  {/* The close button: always on the active tab, on hover for the others (crowded: the active tab's, on hover only).
                      A hidden one takes no room so the name gets it. Showing it can't shift other tabs: tabs are sized by the bar,
                      not by their content (back when they were, inserting it on hover made the whole bar jump). */}
                  <button
                    className={cn(
                      "-mr-1 rounded-sm p-0.5 hover:bg-muted",
                      active
                        ? "block @max-[8rem]/tab:hidden @max-[8rem]/tab:group-hover/tab:block"
                        : "hidden group-hover/tab:block @max-[8rem]/tab:group-hover/tab:hidden",
                    )}
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
      {!sidebar && <ConnStatus />}
      {prefixActive && (
        <div className="shrink-0 rounded-md bg-brand px-2 py-0.5 font-mono text-[11px] text-white" style={{ WebkitAppRegion: "no-drag" } as any}>
          {formatCombo(prefix).replace(/\s.*/, "")} …
        </div>
      )}
      <TabDragLayer />
    </div>
  );
}
