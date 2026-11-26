import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { addTab, tabName } from "@/lib/actions";
import { activeWs, allLeaves, currentSpace, findLeaf, spaceOf } from "@/lib/layout";
import { applyTabLabel } from "@/lib/addons";
import { set, useStore } from "@/lib/store";
import { useSwipePages } from "@/lib/phoneSwipe";
import { basename } from "@/lib/actions/shared";
import type { Layout } from "@/lib/layout";
import type { SessionSummary } from "@/lib/types";
import { FadeText } from "./FadeText";
import { StatusDot } from "./AgentIcon";
import { InboxButton } from "./Inbox";
import { DrawerButton } from "./DrawerButton";
import { tabStatus } from "./TabBar";
import { t } from "@/lib/i18n";

/**
 * The phone's stand-in for the tab bar: ☰ (the drawer) · where you are · the inbox · new tab. (An overview button used to
 * sit here, opening a separate screen that paged through the conversations; the main view swipes itself now: PhonePager.)
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
  const tab = ws.tabs.find((t) => t.id === ws.activeTab) ?? ws.tabs[0];
  // The title is the page drawn (PhonePager: the tab held on screen while a switched-to one catches up), so it changes
  // with the page, and the neighbours' titles wait beside it to fade in as a swipe drags their page in (followSwipe)
  const swipe = useSwipePages();
  const cur = pageTitle(layout, sessions, unread, swipe.shown ?? tab.id) ?? { title: tabName(tab), ...tabStatus(tab, sessions, unread) };
  const side = ([-1, 1] as const).map((at) => ({ at, p: pageTitle(layout, sessions, unread, at < 0 ? swipe.prev : swipe.next) }));
  // The phone shows the focused pane only. When that is already an empty one (the new-session screen), + would only
  // stack another identical tab on top of it, so it's off.
  const shown = findLeaf(tab.root, tab.zoom ?? tab.focus);
  const starting = useStore((s) => !!shown && !!s.starting[shown.id]);
  const onNewPage = !!shown && !shown.sessionId && !shown.sub && !shown.term && !shown.file && !shown.browser && !starting;
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
    <div data-phone-titlebar className="flex h-11 shrink-0 items-center gap-1 border-b bg-sidebar px-1.5">
      <DrawerButton>
        {waiting > 0 && (
          <span className="absolute top-1 right-1 flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-bad px-0.5 text-[9px] leading-none font-semibold text-white tabular-nums">
            {waiting}
          </span>
        )}
      </DrawerButton>
      {/* data-titlebar-drag: in a narrow App window the title drags the window instead (index.css); ☰ opens the drawer */}
      <button
        data-titlebar-drag
        className="relative -ml-1.5 flex h-9 min-w-0 flex-1 items-center overflow-hidden rounded-md pr-1.5 pl-0 text-left"
        onClick={openDrawer}
      >
        {/* With a single pane its title bar shows the path instead of the title and dot (Panes.tsx PhonePath), so the dot
            is this session's; with several it sums up the tab */}
        <span data-swipe-title="0" className="flex min-w-0 items-center gap-1.5">
          <FadeText className="min-w-0 text-[13px] font-medium">{cur.title}</FadeText>
          <StatusDot status={cur.status} unread={cur.unread} />
        </span>
        {side.map(
          ({ at, p }) =>
            p && (
              <span
                key={at}
                data-swipe-title={at}
                aria-hidden
                className="pointer-events-none absolute inset-y-0 right-1.5 left-0 flex items-center gap-1.5 opacity-0"
              >
                <FadeText className="min-w-0 text-[13px] font-medium">{p.title}</FadeText>
                <StatusDot status={p.status} unread={p.unread} />
              </span>
            ),
        )}
      </button>
      {/* Square 36px buttons with no gap between them (narrower boxes were tried and felt cramped) */}
      <div className="flex shrink-0 items-center">
        <InboxButton phone />
        <Button variant="ghost" size="icon" className="size-9" aria-label={t("phoneBar.newTab")} disabled={onNewPage} onClick={() => addTab()}>
          <Plus className="size-[18px]" />
        </Button>
      </div>
    </div>
  );
}

/** A page's title and status dot (PhonePager's keys): the tab's label, or the conversation's for one with no tab yet. */
function pageTitle(layout: Layout, sessions: Record<string, SessionSummary>, unread: Record<string, true>, key: string | undefined) {
  if (!key) return undefined;
  if (key.startsWith("s:")) {
    const ss = sessions[key.slice(2)];
    return ss && { title: ss.title || basename(ss.cwd), status: ss.status as string | undefined, unread: !!unread[ss.id] };
  }
  for (const ws of layout.workspaces) {
    const i = ws.tabs.findIndex((t) => t.id === key);
    if (i >= 0) return { title: applyTabLabel(ws.tabs[i], i, ws, tabName(ws.tabs[i])), ...tabStatus(ws.tabs[i], sessions, unread) };
  }
  return undefined;
}
