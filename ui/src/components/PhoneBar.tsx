import { ClipboardCheck, LayoutList, Menu, Plus } from "lucide-react";
import { useRef } from "react";
import { Button } from "@/components/ui/button";
import { addTab, focusedSessionId, tabName } from "@/lib/actions";
import { activeWs, allLeaves, currentSpace, leaves, spaceOf, spaceWorkspaces } from "@/lib/layout";
import { applyTabLabel, applyWorkspaceLabel } from "@/lib/addons";
import { set, useStore } from "@/lib/store";
import { MAX_TILES, showInOverview, toggleOverview } from "@/lib/overview";
import { FadeText } from "./FadeText";
import { StatusDot } from "./AgentIcon";
import { toast } from "sonner";
import { InboxButton } from "./Inbox";
import { PaneSwitcher } from "./PaneSwitcher";
import { Popover, PopoverAnchor } from "@/components/ui/popover";
import { tabStatus } from "./TabBar";

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
 * A long-press that swaps the screen leaves the finger down on whatever was drawn under it, and the system's own
 * long-press (Android: select the word, show copy / share) then lands on that. Refuse any selection until the finger lifts.
 */
function blockSelectionUntilLift() {
  const block = (e: Event) => e.preventDefault();
  const lift = () => {
    document.removeEventListener("selectstart", block, true);
    window.removeEventListener("pointerup", lift, true);
    window.removeEventListener("pointercancel", lift, true);
  };
  document.addEventListener("selectstart", block, true);
  window.addEventListener("pointerup", lift, true);
  window.addEventListener("pointercancel", lift, true);
  window.getSelection()?.removeAllRanges();
}

/**
 * The phone bar's overview button. Tap opens the overview; long-press (or right-click) puts the pane you're on into it
 * and goes to that tile. Our own timer rather than relying on contextmenu: iOS Safari never fires it on a long-press.
 */
function OverviewButton() {
  const timer = useRef<number | undefined>(undefined);
  const held = useRef(false);
  const hold = () => {
    held.current = true;
    blockSelectionUntilLift();
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
