import {
  Check,
  ChevronsUpDown,
  Command,
  Keyboard,
  ClipboardCheck,
  Loader2,
  LayoutList,
  Menu,
  PanelLeft,
  Plus,
  Settings,
  SquareTerminal,
  X,
} from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { addTab, closeSpace, closeTab, duplicateTab, focusedSessionId, switchSpace, tabName } from "@/lib/actions";
import { formatCombo, IS_MAC } from "@/lib/keys";
import {
  activeWs,
  allLeaves,
  currentSpace,
  leaves,
  spaceList,
  spaceOf,
  spaceWorkspaces,
  type Layout,
  type Space,
  type Tab,
  type Workspace,
} from "@/lib/layout";
import { applyTabLabel, applyWorkspaceLabel } from "@/lib/addons";
import { ModeSwitch } from "@/components/WorkView";
import { get, keyFor, set, setLayout, setSidebarW, SIDEBAR_W, toggleSidebar, useStore } from "@/lib/store";
import type { SessionSummary } from "@/lib/types";
import { MAX_TILES, showInOverview, toggleOverview } from "@/lib/overview";
import { cn } from "@/lib/utils";
import { FadeText } from "./FadeText";
import { AgentIcon, StatusDot, dotStatus, dotRank } from "./AgentIcon";
import { fmtElapsed } from "./chat/StatusLine";
import { TabDragLayer, useTabDrag } from "./TabDrag";
import { AddonSlot } from "./AddonSlot";
import { isAndroid } from "@/lib/android";
import { reconnectNow } from "@/lib/sync";
import { toast } from "sonner";
import { askConfirm } from "@/lib/confirm";
import { isNative, nativeNewWindow } from "@/lib/native";
import { useRoute } from "./AndroidSettings";
import { usePhone } from "@/lib/phone";
import { InboxButton } from "./Inbox";
import { PaneSwitcher } from "./PaneSwitcher";
import { Popover, PopoverAnchor } from "@/components/ui/popover";

/** 一个 tab 的汇总状态：里面任何一个会话在等你 → 等你；在跑 → 在跑；…… */
/**
 * True while the tab shortcuts' modifier (⌘ on mac, Ctrl elsewhere) is held on its own. Waits a moment before turning on
 * so a quick ⌘C / ⌘V doesn't flash numbers across the tab bar. Listens in the capture phase because xterm and the
 * composer stop some keydowns; drops on blur since the keyup of a ⌘-Tab away never reaches the page.
 */
function useModHeld() {
  const [held, setHeld] = useState(false);
  useEffect(() => {
    const mod = IS_MAC ? "Meta" : "Control";
    let timer: ReturnType<typeof setTimeout> | undefined;
    const off = () => {
      clearTimeout(timer);
      timer = undefined;
      setHeld(false);
    };
    const down = (e: KeyboardEvent) => {
      // ⌘1–9 switches tabs while ⌘ is still down: keep the numbers up so the next pick is still readable.
      if (/^[1-9]$/.test(e.key) && (IS_MAC ? e.metaKey : e.ctrlKey)) return;
      if (e.key !== mod) return off();
      if (e.repeat) return;
      if (e.shiftKey || e.altKey || (IS_MAC ? e.ctrlKey : e.metaKey)) return;
      clearTimeout(timer);
      timer = setTimeout(() => setHeld(true), 250);
    };
    const up = (e: KeyboardEvent) => e.key === mod && off();
    window.addEventListener("keydown", down, true);
    window.addEventListener("keyup", up, true);
    window.addEventListener("blur", off);
    return () => {
      off();
      window.removeEventListener("keydown", down, true);
      window.removeEventListener("keyup", up, true);
      window.removeEventListener("blur", off);
    };
  }, []);
  return held;
}

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
  const numbers = useModHeld();
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
                  {/* Holding ⌘ (Ctrl elsewhere) lays the tab's ⌘1–9 number on a veil at the tab's left edge — over the icon when there
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
  useStore((s) => s.addonVersion);
  const ws = activeWs(layout);
  const wi = spaceWorkspaces(layout).indexOf(ws);
  const ti = ws.tabs.findIndex((t) => t.id === ws.activeTab);
  const tab = ws.tabs[ti] ?? ws.tabs[0];
  const st = tabStatus(tab, sessions, unread);
  // Waiting in this session (the switcher it opens only lists this session's panes); other sessions' show on the sidebar's session switcher
  const space = currentSpace(layout);
  const waiting = new Set(
    allLeaves(layout)
      .filter((x) => spaceOf(x.ws) === space && x.pane.sessionId && sessions[x.pane.sessionId]?.status === "waiting")
      .map((x) => x.pane.sessionId),
  ).size;
  const openSwitcher = () => {
    // A tap on these buttons while the panel is open first counts as a tap outside it (the popover closes itself on
    // pointerdown); toggling on the click that follows would open it straight back
    if (!switcher && Date.now() - switcherClosedAt < 400) return;
    // 先把键盘收掉：键盘占着半个屏时，点面板里第一下只会收键盘
    (document.activeElement as HTMLElement | null)?.blur?.();
    set({ switcher: !switcher, drawer: false });
  };
  return (
    // The pane switcher is a popover anchored on the whole bar (same component as the inbox, for its animation); the
    // store's `switcher` drives it because the pane title bar's 1/2 count opens it too
    <Popover
      open={switcher}
      onOpenChange={(v) => {
        if (!v) switcherClosedAt = Date.now();
        set({ switcher: v });
      }}
      modal
    >
      <PopoverAnchor asChild>
        <div className="flex h-11 shrink-0 items-center gap-1 border-b bg-sidebar px-1.5">
          <Button variant="ghost" size="icon" className="size-9" aria-label="侧栏" onClick={() => set({ drawer: true, switcher: false })}>
            <Menu className="size-[18px]" />
          </Button>
          {/* Lined up with the pane title bar below (Panes.tsx: pl-4, 16px icon, gap-2): both icons are centred at 24px and both
          titles start at 40px — 6px padding + 36px button + 4px gap − 6px pulled back (asked for) */}
          <button className="-ml-1.5 flex h-9 min-w-0 flex-1 items-center gap-1.5 rounded-md pr-1.5 pl-0 text-left" onClick={openSwitcher}>
            <FadeText className="min-w-0 text-[13px] font-medium">{applyTabLabel(tab, ti, ws, tabName(tab))}</FadeText>
            {/* 只有一个 pane 时不画：下面 pane 标题栏那个点就是同一个会话，一行里三个点太乱。好几个 pane 时它是整个 tab 的汇总，留着 */}
            {leaves(tab.root).length > 1 && <StatusDot status={st.status} unread={st.unread} />}
            {/* The workspace never gives way to a long tab name (asked for): it is the one place on a phone that says which one you're in */}
            <FadeText className="max-w-[40%] shrink-0 text-[11px] text-muted-foreground">{applyWorkspaceLabel(ws, wi, ws.name)}</FadeText>
          </button>
          <Button variant={switcher ? "secondary" : "ghost"} size="icon" className="relative size-9" aria-label="面板一览" onClick={openSwitcher}>
            <LayoutList className="size-[18px]" />
            {waiting > 0 && (
              <span className="absolute top-1 right-1 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-bad px-0.5 text-[9px] leading-none font-semibold text-white tabular-nums">
                {waiting}
              </span>
            )}
          </Button>
          <OverviewButton />
          <InboxButton phone />
          <Button variant="ghost" size="icon" className="size-9" aria-label="新建 tab" onClick={() => addTab()}>
            <Plus className="size-[18px]" />
          </Button>
        </div>
      </PopoverAnchor>
      <PaneSwitcher />
    </Popover>
  );
}

let switcherClosedAt = 0;

/**
 * The phone bar's overview button. Tap opens the overview; long-press (or right-click) puts the pane you're on into it
 * and goes to that tile. Our own timer rather than relying on contextmenu: iOS Safari never fires it on a long-press.
 */
function OverviewButton() {
  const timer = useRef<number | undefined>(undefined);
  const held = useRef(false);
  const hold = () => {
    held.current = true;
    const id = focusedSessionId();
    if (!id) return toggleOverview(true); // a terminal or an empty pane: nothing to put in
    if (!showInOverview(id)) toast(`网格最多放 ${MAX_TILES} 个，先从进行中移出几个`);
  };
  const cancel = () => clearTimeout(timer.current);
  return (
    <Button
      variant="ghost"
      size="icon"
      // No text selection / iOS callout from the long-press
      className="size-9 select-none [-webkit-touch-callout:none]"
      aria-label="进行中（长按：把当前面板加进去）"
      onPointerDown={() => {
        held.current = false;
        cancel();
        timer.current = window.setTimeout(hold, 500);
      }}
      onPointerUp={cancel}
      onPointerLeave={cancel}
      onPointerCancel={cancel}
      onContextMenu={(e) => {
        e.preventDefault();
        cancel();
        if (!held.current) hold();
      }}
      onClick={() => {
        // The click that ends a long-press already did its thing
        if (held.current) return void (held.current = false);
        toggleOverview(true);
      }}
    >
      <ClipboardCheck className="size-[18px]" />
    </Button>
  );
}

/**
 * The connection status while disconnected from the gerdr backend. Used to be a bare red "offline", which read as "broken" while
 * sync.ts was in fact retrying with backoff (a backend restart takes a few seconds): now it says it's reconnecting, counts down
 * to the next try, and offers to try now. Blips under a second (sleep / wake, a dropped stale socket) show nothing.
 */
export function ConnStatus() {
  const connected = useStore((s) => s.connected);
  const conn = useStore((s) => s.conn);
  const [shown, setShown] = useState(false);
  const [, tick] = useState(0);
  useEffect(() => {
    setShown(false);
    if (connected) return;
    const t = setTimeout(() => setShown(true), 1000);
    return () => clearTimeout(t);
  }, [connected]);
  useEffect(() => {
    if (connected || conn.state !== "waiting") return;
    const t = setInterval(() => tick((x) => x + 1), 250);
    return () => clearInterval(t);
  }, [connected, conn]);
  const ever = useStore((s) => s.everConnected);
  const retrying = useStore((s) => s.connRetrying);
  // The first attempt after opening: the page shows its own loading state, a "reconnecting" here would be wrong and redundant
  if (connected || !shown || (!ever && !retrying)) return null;
  const left = conn.state === "waiting" ? conn.at - Date.now() : 0;
  // A wait under a second would flash a "1 s" countdown between attempts: show it as still connecting
  if (left < 1000)
    return (
      <span
        className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground"
        title={ever ? "和 Gerdr 后台的连接断了，正在重连" : "正在连接 Gerdr 后台"}
      >
        <Loader2 className="size-3 animate-spin" />
        {ever ? "重连中…" : "连接中…"}
      </span>
    );
  return (
    <span
      className="flex shrink-0 items-center gap-1 text-[11px] text-warn tabular-nums"
      title={`${ever ? "和 Gerdr 后台的连接断了" : "连不上 Gerdr 后台"}，${Math.ceil(left / 1000)} 秒后自动重试`}
    >
      {ever ? "断开" : "连不上"} · {Math.ceil(left / 1000)}s
      <button className="rounded-sm px-1 text-foreground hover:bg-accent" style={{ WebkitAppRegion: "no-drag" } as any} onClick={reconnectNow}>
        {ever ? "重连" : "重试"}
      </button>
    </span>
  );
}

/**
 * The phone's connection status: a toast at the top instead of ConnStatus in the top bar. The bar has no room for it (it got
 * squeezed out by the tab name) and Work mode covers it entirely, so a dropped connection showed nowhere. Same timing as
 * ConnStatus (blips under a second show nothing); the toast stays until the connection is back, then goes away.
 */
export function PhoneConnToast() {
  const connected = useStore((s) => s.connected);
  const conn = useStore((s) => s.conn);
  const [shown, setShown] = useState(false);
  const [, tick] = useState(0);
  useEffect(() => {
    setShown(false);
    if (connected) return;
    const t = setTimeout(() => setShown(true), 1000);
    return () => clearTimeout(t);
  }, [connected]);
  useEffect(() => {
    if (connected || conn.state !== "waiting") return;
    const t = setInterval(() => tick((x) => x + 1), 250);
    return () => clearInterval(t);
  });
  const ever = useStore((s) => s.everConnected);
  const retrying = useStore((s) => s.connRetrying);
  // Same as ConnStatus: nothing during the first attempt after opening (the page is showing it's loading)
  const on = shown && !connected && (ever || retrying);
  const left = conn.state === "waiting" ? Math.ceil((conn.at - Date.now()) / 1000) : 0;
  // A wait under a second would flash a "1 s" countdown between attempts: show it as still connecting
  const text = ever
    ? left > 1
      ? `和 Gerdr 的连接断了，${left} 秒后重连`
      : "正在重连 Gerdr…"
    : left > 1
      ? `连不上 Gerdr，${left} 秒后重试`
      : "正在连接 Gerdr…";
  useEffect(() => {
    if (!on) return;
    toast.loading(text, {
      id: "conn",
      duration: Infinity,
      dismissible: false,
      // preventDefault: sonner closes a toast on its action by default, which would flash it away and back
      action: left > 1 ? { label: ever ? "重连" : "重试", onClick: (e) => (e.preventDefault(), reconnectNow()) } : undefined,
    });
  }, [on, text, left > 1]);
  useEffect(() => {
    if (on) return;
    toast.dismiss("conn");
  }, [on]);
  useEffect(() => () => void toast.dismiss("conn"), []);
  return null;
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

/**
 * The bottom of the left column in all three modes: add-ons' sidebar blocks (g.slot("sidebar"): the quota meter,
 * today's numbers…) over the settings row. Shared so the add-ons are in sight whichever mode is open (asked for).
 */
export function SidebarBottom({ sidebarToggle }: { sidebarToggle?: boolean }) {
  return (
    <>
      {/* The vertical padding is given once here, not by each block: two blocks each padding themselves left a wide gap.
          bg-sidebar: in Work mode this is laid over the site's menu */}
      <AddonSlot name="sidebar" className="shrink-0 border-t bg-sidebar py-1 empty:hidden" />
      <SidebarFooter sidebarToggle={sidebarToggle} />
    </>
  );
}

/**
 * The top row of the left column, the same in all three modes (asked for): the Code sidebar, the overview's list, and
 * Work mode's top bar. Project first, the Code / overview / Work switch at the right, so switching modes moves nothing.
 * Only these two: the connection status used to sit here too and squeezed the project name to "def…" (it's in the
 * bottom row now, SidebarFooter).
 */
export function SidebarHeader({ className, style }: { className?: string; style?: React.CSSProperties }) {
  // As tall as the phone's top bar (PhoneBar, h-11): the drawer slides in beside it, and the two bottom borders should line up
  const phone = usePhone();
  return (
    <div
      className={cn("flex shrink-0 items-center gap-1 border-b px-1.5", phone ? "h-11" : "h-10", className)}
      style={{ WebkitAppRegion: "drag", ...style } as any}
    >
      <div className="native-traffic-light-gap" />
      {/* The session switcher takes the title's place (asked for). Only the button is no-drag, so the rest of the bar still moves the window */}
      <div className="flex min-w-16 flex-1">
        <SpaceSwitcher />
      </div>
      <ModeSwitch />
    </div>
  );
}

/**
 * The bottom row of the left column, the same in all three modes (asked for: settings always at the bottom): the Code
 * sidebar, the overview's list, and over the bottom of the Work site's menu (WorkView). `sidebarToggle`: the Code
 * sidebar and the overview's list (one setting) can be hidden; Work mode's menu can't.
 */
export function SidebarFooter({ sidebarToggle }: { sidebarToggle?: boolean }) {
  const connected = useStore((s) => s.connected);
  const doctorBad = useStore((s) => s.doctor?.level === "bad");
  const phone = usePhone();
  return (
    <div className="flex h-10 shrink-0 items-center gap-1 border-t bg-sidebar px-1.5">
      <Button
        variant="ghost"
        size="sm"
        className="h-7 shrink-0 justify-start gap-2 text-[12px] text-muted-foreground"
        onClick={() => set({ dialog: { kind: "settings", tab: doctorBad ? "labs" : undefined } })}
      >
        <Settings className="size-3.5" /> 设置
        {/* Labs → Diagnosis found something broken (agents missing PATH dirs, a blocked codex…) */}
        {doctorBad && (
          <span className="ml-auto flex items-center gap-1 text-[11px] text-bad" title="诊断发现了问题：设置 → 实验室 → 诊断">
            <span className="size-1.5 rounded-full bg-bad" /> 有问题
          </span>
        )}
      </Button>
      {/* Status lives down here, where there's room: the connection to the backend when it's lost, and on Android which route
          the app takes (details in Settings → General → Android app) */}
      <div className="flex min-w-0 flex-1 items-center justify-end gap-2 overflow-hidden">
        <ConnStatus />
        {connected && isAndroid() && <RouteLabel />}
      </div>
      {/* On a phone the inbox is in the top bar (PhoneBar); this row is in the drawer there */}
      {!phone && <InboxButton />}
      {/* A phone has no ⌘K and no keyboard to need a shortcut list: the slot opens the palette instead, the only way
          to reach add-on commands there. The drawer closes so the palette isn't under it on the way back. */}
      {phone ? (
        <Button
          variant="ghost"
          size="icon"
          className="size-7 text-muted-foreground"
          aria-label="命令面板"
          onClick={() => set({ drawer: false, dialog: { kind: "palette" } })}
        >
          <Command className="size-3.5" />
        </Button>
      ) : (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" className="size-7 text-muted-foreground" onClick={() => set({ dialog: { kind: "help" } })}>
              <Keyboard className="size-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>快捷键</TooltipContent>
        </Tooltip>
      )}
      {sidebarToggle && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon" className="size-7 text-muted-foreground" onClick={toggleSidebar}>
              <PanelLeft className="size-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent>隐藏侧栏</TooltipContent>
        </Tooltip>
      )}
    </div>
  );
}

/**
 * A column resize handle's look. touch-none: without it a touch drag that moves fast is taken over by the browser as a
 * pan (pointercancel), so on a phone or tablet only a slow drag got anywhere. The callers make it wider for fingers.
 */
export const COL_EDGE = "z-20 cursor-col-resize touch-none bg-clip-content transition-colors hover:bg-brand/50";

/**
 * Drag on a column resize handle: onDx gets the distance from where it started, at most once a frame (each call
 * re-renders the sidebar or the chat column; per pointer event that's several times a frame on a phone). Captured, so a
 * frame next to the handle (Work mode's site) can't swallow the moves.
 */
export function startColResize(e: React.PointerEvent, onDx: (dx: number) => void, axis: "x" | "y" = "x") {
  e.preventDefault();
  const el = e.currentTarget as HTMLElement;
  el.setPointerCapture(e.pointerId);
  const pos = (ev: { clientX: number; clientY: number }) => (axis === "x" ? ev.clientX : ev.clientY);
  const x0 = pos(e);
  let x = x0;
  let raf = 0;
  const move = (ev: PointerEvent) => {
    x = pos(ev);
    raf ||= requestAnimationFrame(() => {
      raf = 0;
      onDx(x - x0);
    });
  };
  const end = () => {
    if (raf) (cancelAnimationFrame(raf), onDx(x - x0));
    raf = 0;
    for (const t of ["pointerup", "pointercancel", "lostpointercapture"]) el.removeEventListener(t, end);
    el.removeEventListener("pointermove", move);
    document.body.style.cursor = "";
  };
  document.body.style.cursor = axis === "x" ? "col-resize" : "row-resize";
  el.addEventListener("pointermove", move);
  for (const t of ["pointerup", "pointercancel", "lostpointercapture"]) el.addEventListener(t, end);
}

/**
 * The sidebar's right edge: drag to resize it (asked for), double-click for the default width. Also on the overview's
 * list, which shares the width so switching between them doesn't jump.
 */
export function SidebarEdge() {
  const start = (e: React.PointerEvent) => {
    const w0 = get().sidebarW;
    startColResize(e, (dx) => setSidebarW(w0 + dx));
  };
  return (
    <div
      className={cn("absolute top-0 -right-1 bottom-0 w-2 px-[3px] pointer-coarse:-right-3 pointer-coarse:w-6 pointer-coarse:px-[11px]", COL_EDGE)}
      onPointerDown={start}
      onDoubleClick={() => setSidebarW(SIDEBAR_W)}
      title="拖动调宽窄，双击还原"
      style={{ WebkitAppRegion: "no-drag" } as any}
    />
  );
}

export function Elapsed({ since }: { since: number }) {
  const [, tick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => tick((x) => x + 1), 1000);
    return () => clearInterval(t);
  }, []);
  return <span className="shrink-0 tabular-nums">&nbsp;· {fmtElapsed(Date.now() - since)}</span>;
}

/** A session's overall state for the switcher: the most urgent of its workspaces' (same ranking as WsDot). */
function spaceStatus(l: Layout, sp: Space, sessions: Record<string, SessionSummary>, unread: Record<string, true>) {
  let best: { status?: string; unread: boolean } = { unread: false };
  let rank = 9;
  for (const w of spaceWorkspaces(l, sp.id))
    for (const t of w.tabs) {
      const st = tabStatus(t, sessions, unread);
      const r = dotRank(st.status, st.unread);
      if (r < rank) {
        rank = r;
        best = st;
      }
    }
  return rank === 9 ? undefined : best;
}

/**
 * Which herdr-style session the sidebar shows (layout.ts Space). One line above the workspaces: the current one's name, and a dot when
 * another session has something for you — its agents are out of the lists below, so this is the only place that tells you.
 */
async function askCloseSpace(sp: Space) {
  const n = spaceWorkspaces(get().layout, sp.id).length;
  const ok = await askConfirm({
    title: `关闭项目「${sp.name}」？`,
    description: `连同它的 ${n} 个工作空间一起关掉，里面的 agent 会停掉（会话记录留着，能再打开）。项目的工作空间布局和 Work 都留在它的目录里：以后再建一个同名的项目，就都回来了。`,
    action: "关闭",
    destructive: true,
  });
  if (ok) closeSpace(sp.id);
}

const newSpaceDialog = () => set({ dialog: { kind: "rename", target: "newSpace", id: "", value: "" } });

/**
 * The session switcher in the sidebar header. When every session's name fits on one line they sit side by side like tabs
 * (one click to switch, asked for); when they don't, it folds into a dropdown (SpaceMenu). Fitting is measured on an invisible
 * copy of the row, re-measured when the header resizes or a name changes.
 */
export function SpaceSwitcher() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const spaces = spaceList(layout);
  const here = currentSpace(layout);
  const box = useRef<HTMLDivElement>(null);
  const ruler = useRef<HTMLDivElement>(null);
  const [fits, setFits] = useState(true);
  const names = spaces.map((s) => s.name).join("\0");
  useLayoutEffect(() => {
    const el = box.current;
    const r = ruler.current;
    if (!el || !r) return;
    const check = () => setFits(r.scrollWidth <= el.clientWidth);
    check();
    const ro = new ResizeObserver(check);
    ro.observe(el);
    return () => ro.disconnect();
  }, [names]);
  const chip = (sp: Space, live: boolean) => {
    const st = sp.id === here ? undefined : spaceStatus(layout, sp, sessions, unread);
    return (
      <button
        key={sp.id}
        tabIndex={live ? undefined : -1}
        className={cn(
          "flex h-6 max-w-28 shrink-0 items-center gap-1 rounded-md px-1.5 text-[12px] font-semibold whitespace-nowrap",
          sp.id === here ? "bg-accent text-foreground" : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
          // Mark which session the context menu belongs to while it's open
          "data-[state=open]:ring-1 data-[state=open]:ring-brand/60",
        )}
        style={{ WebkitAppRegion: "no-drag" } as any}
        onClick={live ? () => switchSpace(sp.id) : undefined}
      >
        <span className="min-w-0 truncate">{sp.name}</span>
        {st && <StatusDot status={st.status} unread={st.unread} />}
      </button>
    );
  };
  const plus = (live: boolean) => (
    <button
      tabIndex={live ? undefined : -1}
      title="新项目（每个项目有自己的工作空间、agent 和 Work，互相看不见）"
      className="flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-accent/60 hover:text-foreground"
      style={{ WebkitAppRegion: "no-drag" } as any}
      onClick={live ? newSpaceDialog : undefined}
    >
      <Plus className="size-3.5" />
    </button>
  );
  return (
    <div ref={box} className="relative flex min-w-0 flex-1 items-center">
      <div ref={ruler} aria-hidden className="pointer-events-none invisible absolute top-0 left-0 flex w-max items-center gap-0.5">
        {spaces.map((sp) => chip(sp, false))}
        {plus(false)}
      </div>
      {fits ? (
        <div className="-ml-1 flex min-w-0 items-center gap-0.5">
          {spaces.map((sp) => (
            <ContextMenu key={sp.id}>
              <ContextMenuTrigger asChild>{chip(sp, true)}</ContextMenuTrigger>
              <ContextMenuContent>
                {isNative() && <ContextMenuItem onClick={() => nativeNewWindow(sp.id)}>在新窗口打开</ContextMenuItem>}
                {isNative() && spaces.length > 1 && <ContextMenuSeparator />}
                {spaces.length > 1 && (
                  <ContextMenuItem variant="destructive" onClick={() => askCloseSpace(sp)}>
                    关闭项目
                  </ContextMenuItem>
                )}
              </ContextMenuContent>
            </ContextMenu>
          ))}
          {plus(true)}
        </div>
      ) : (
        <SpaceMenu />
      )}
    </div>
  );
}

/** The folded form of the switcher: the current session's name, a dot when another one needs you, everything else in a menu. */
function SpaceMenu() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  const spaces = spaceList(layout);
  const here = currentSpace(layout);
  const cur = spaces.find((s) => s.id === here)!;
  const elsewhere = spaces
    .filter((s) => s.id !== here)
    .map((s) => spaceStatus(layout, s, sessions, unread))
    .filter((x) => x)
    .sort((a, b) => +(b!.status === "waiting") - +(a!.status === "waiting"))[0];
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          className="-ml-1 flex h-7 max-w-full min-w-0 items-center gap-1.5 rounded-md px-1.5 text-left text-[12px] font-semibold text-muted-foreground hover:bg-accent/60 hover:text-foreground data-[state=open]:bg-accent"
          style={{ WebkitAppRegion: "no-drag" } as any}
        >
          <span className="min-w-0 truncate text-foreground">{cur.name}</span>
          {elsewhere && <StatusDot status={elsewhere.status} unread={elsewhere.unread} />}
          <ChevronsUpDown className="size-3 shrink-0" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-52">
        <DropdownMenuLabel className="text-[11px] font-normal text-muted-foreground">每个项目的 agent 互相看不见</DropdownMenuLabel>
        {spaces.map((sp) => {
          const st = spaceStatus(layout, sp, sessions, unread);
          return (
            <DropdownMenuItem key={sp.id} onClick={() => switchSpace(sp.id)}>
              <Check className={cn("size-3.5", sp.id !== here && "invisible")} />
              <span className="min-w-0 flex-1 truncate">{sp.name}</span>
              <span className="text-[11px] text-muted-foreground tabular-nums">{spaceWorkspaces(layout, sp.id).length}</span>
              <StatusDot status={st?.status} unread={!!st?.unread} />
            </DropdownMenuItem>
          );
        })}
        <DropdownMenuSeparator />
        {isNative() && <DropdownMenuItem onClick={() => nativeNewWindow(cur.id)}>在新窗口打开「{cur.name}」</DropdownMenuItem>}
        <DropdownMenuItem onClick={newSpaceDialog}>
          <Plus className="size-3.5" /> 新项目
        </DropdownMenuItem>
        {spaces.length > 1 && (
          <DropdownMenuItem variant="destructive" onClick={() => askCloseSpace(cur)}>
            关闭「{cur.name}」
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
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
    const r = dotRank(st.status, st.unread);
    if (r < rank) {
      rank = r;
      best = st;
    }
  }
  if (hideIdle && rank === 9) return null;
  return <StatusDot status={best.status} unread={best.unread} />;
}
