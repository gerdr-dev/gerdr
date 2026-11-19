import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { BrowserScreen } from "@/components/browser/BrowserScreen";
import { loadBrowserTabs, tabMoves, useBrowserOpens, useBrowserTabs } from "@/lib/actions";
import type { BrowserTab } from "@/lib/api";
import { cn } from "@/lib/utils";

// A conversation's browser on a panel's page (docs/dev/BROWSER-PANE.md). In Code, a page an agent opens comes up as a
// Browser pane beside the agent's pane (lib/actions/browser.ts browserOpened); a panel's agent is in no pane, so its
// pages came up nowhere: the user was asked to sign in "in the Browser pane" and couldn't find one (user report).
// A panel puts this beside the conversation instead (a tab of its drawer) and turns to it when the agent opens a page.

/**
 * The pages a conversation's agent opened (`gerdr browser open`, and the pages those opened), oldest first, and when it
 * last opened one while this UI was up (opened: a time, changes at each open): turn to the browser then.
 */
export function useSessionBrowser(session: string | undefined): { pages: BrowserTab[]; opened?: number } {
  const tabs = useBrowserTabs((s) => s.tabs);
  const loaded = useBrowserTabs((s) => s.loaded);
  const opened = useBrowserOpens((s) => (session ? s[session]?.at : undefined));
  useEffect(() => {
    if (!loaded) void loadBrowserTabs();
  }, [loaded]);
  const pages = useMemo(() => (session ? tabs.filter((x) => x.session === session && !x.gone) : []), [tabs, session]);
  return { pages, opened };
}

const Body = ({ children }: { children: ReactNode }) => <div className="relative min-h-0 flex-1">{children}</div>;

/**
 * The browser of one conversation: the page its agent opened last, with an address bar, back / forward / reload and
 * a ⋯ menu of the project's pages, clicked and typed into as in Code's Browser pane (the App shows the real page).
 * Fills its parent's height. A page that opens another (a sign-in popup) is followed there and back, as in a pane.
 */
export function SessionBrowser({ session, className }: { session: string; className?: string }) {
  const id = useId();
  const { pages } = useSessionBrowser(session);
  const last = useBrowserOpens((s) => s[session]);
  const all = useBrowserTabs((s) => s.tabs);
  const alive = (t?: number) => !!t && all.some((x) => x.tab === t && !x.gone);
  // The page shown: picked here (the ⋯ menu, a page followed) or else the agent's last
  const [picked, setPicked] = useState<{ tab?: number } | null>(null);
  useEffect(() => setPicked(null), [last?.at]); // the agent opened another: show that one
  const tab = picked ? picked.tab : alive(last?.tab) ? last!.tab : pages.at(-1)?.tab;
  const project = useRef("");
  const cur = all.find((x) => x.tab === tab);
  if (cur?.project) project.current = cur.project;
  else if (!project.current && pages[0]?.project) project.current = pages[0].project;
  // follow the page to the one it opened and back (lib/actions/browser.ts followNewTabs, for panes)
  const tabRef = useRef(tab);
  tabRef.current = tab;
  useEffect(
    () =>
      useBrowserTabs.subscribe((s, prev) => {
        const to = tabRef.current && tabMoves(prev.tabs, s.tabs).get(tabRef.current);
        if (to) setPicked({ tab: to });
      }),
    [],
  );
  const [url, setURL] = useState<string>();
  return (
    <div className={cn("flex h-full min-h-0 flex-col", className)}>
      {project.current ? (
        <BrowserScreen
          project={project.current}
          tab={tab}
          lastURL={url}
          paneId={`panel-browser${id}`}
          focused
          onShow={(t) => setPicked({ tab: t })}
          onURL={setURL}
          onFocus={() => {}}
          headClassName="h-9"
          Body={Body}
        />
      ) : null}
    </div>
  );
}
