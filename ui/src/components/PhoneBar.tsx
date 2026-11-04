import { ClipboardCheck, Menu, Plus } from "lucide-react";
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
import { tabStatus } from "./TabBar";
import { t } from "@/lib/i18n";

/**
 * The phone's stand-in for the tab bar: ☰ (the drawer) · where you are · the overview · the inbox · new tab.
 *
 * A phone shows one pane at a time and a row of small tabs can neither be hit nor fit, so switching tabs / panes is the
 * drawer's job (UnifiedSidebar, roomier rows and a search box on touch); both ☰ and the title open it. This row only
 * says where you are and how many are waiting on you — a badge on ☰, as nothing else on screen shows one waiting in
 * another tab. (There used to be a separate flat pane list here, PaneSwitcher; it listed the same panes as the drawer.)
 */
export function PhoneBar() {
  const layout = useStore((s) => s.layout);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  useStore((s) => s.addonVersion);
  const ws = activeWs(layout);
  const wi = spaceWorkspaces(layout).indexOf(ws);
  const ti = ws.tabs.findIndex((t) => t.id === ws.activeTab);
  const tab = ws.tabs[ti] ?? ws.tabs[0];
  const st = tabStatus(tab, sessions, unread);
  // Waiting in this project (the drawer lists this project's panes); other projects' show on the drawer's project switcher
  const space = currentSpace(layout);
  const waiting = new Set(
    allLeaves(layout)
      .filter((x) => spaceOf(x.ws) === space && !x.ws.muted && x.pane.sessionId && sessions[x.pane.sessionId]?.status === "waiting")
      .map((x) => x.pane.sessionId),
  ).size;
  const openDrawer = () => {
    // Drop the keyboard first: with it up, the first tap in the drawer would only close the keyboard
    (document.activeElement as HTMLElement | null)?.blur?.();
    set({ drawer: true });
  };
  return (
    <div className="flex h-11 shrink-0 items-center gap-1 border-b bg-sidebar px-1.5">
      <Button variant="ghost" size="icon" className="relative size-9" aria-label={t("phoneBar.sidebar")} onClick={openDrawer}>
        <Menu className="size-[18px]" />
        {waiting > 0 && (
          <span className="absolute top-1 right-1 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-bad px-0.5 text-[9px] leading-none font-semibold text-white tabular-nums">
            {waiting}
          </span>
        )}
      </Button>
      {/* Lined up with the pane title bar below (Panes.tsx: pl-4, 16px icon, gap-2): both icons are centred at 24px and both
          titles start at 40px — 6px padding + 36px button + 4px gap − 6px pulled back (asked for) */}
      <button className="-ml-1.5 flex h-9 min-w-0 flex-1 items-center gap-1.5 rounded-md pr-1.5 pl-0 text-left" onClick={openDrawer}>
        <FadeText className="min-w-0 text-[13px] font-medium">{applyTabLabel(tab, ti, ws, tabName(tab))}</FadeText>
        {/* Not drawn with a single pane: the pane title bar's dot below is the same session. With several it sums up the tab */}
        {leaves(tab.root).length > 1 && <StatusDot status={st.status} unread={st.unread} />}
        {/* The workspace never gives way to a long tab name (asked for): it is the one place on a phone that says which one you're in */}
        <FadeText className="max-w-[40%] shrink-0 text-[11px] text-muted-foreground">{applyWorkspaceLabel(ws, wi, ws.name)}</FadeText>
      </button>
      <OverviewButton />
      <InboxButton phone />
      <Button variant="ghost" size="icon" className="size-9" aria-label={t("phoneBar.newTab")} onClick={() => addTab()}>
        <Plus className="size-[18px]" />
      </Button>
    </div>
  );
}

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
    if (!showInOverview(id)) toast(t("phoneBar.gridFull", { n: MAX_TILES }));
  };
  const cancel = () => clearTimeout(timer.current);
  return (
    <Button
      variant="ghost"
      size="icon"
      // No text selection / iOS callout from the long-press
      className="size-9 select-none [-webkit-touch-callout:none]"
      aria-label={t("phoneBar.active")}
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
