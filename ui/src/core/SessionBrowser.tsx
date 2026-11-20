import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { create } from "zustand";
import { AppWindow, Globe, PanelRightClose, SquareArrowOutUpRight, Undo2 } from "lucide-react";
import { BrowserScreen } from "@/components/browser/BrowserScreen";
import { Button } from "@/components/ui/button";
import { loadBrowserTabs, tabMoves, useBrowserOpens, useBrowserTabs, useBrowserWindows } from "@/lib/actions";
import type { BrowserTab } from "@/lib/api";
import { t } from "@/lib/i18n";
import type { FloatFit } from "@/lib/layout";
import { isNative, nativeBrowserWindow, nativeBrowserWindowClose, nativeBrowserWindowTab, nativeNewPathWindow } from "@/lib/native";
import { cn } from "@/lib/utils";

// Agents' pages outside Code's panes (docs/dev/BROWSER-PANE.md). In Code, a page an agent opens comes up as a Browser
// pane beside the agent's pane (lib/actions/browser.ts browserOpened). A panel's agents are in no pane, so their pages
// came up nowhere: the user was asked to sign in "in the Browser pane" and couldn't find one (user report). Here:
//   - PanelBrowser: a column at the right of the whole panel, for every conversation of that panel (asked for: not a
//     tab beside one chat), opened on its own when one of them opens a page;
//   - BrowserWindow: the same page in a window of its own (the column's "open in new window"), App.tsx ?browser=;
//   - SessionBrowser / useSessionBrowser: for a page that wants one conversation's browser in its own layout.

/**
 * The pages a conversation's agent opened (`gerdr browser open`, and the pages those opened), oldest first, and when it
 * last opened one while this UI was up (opened: a time, changes at each open).
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
 * One page with its address bar, back / forward / reload and a ⋯ menu of the project's pages, clicked and typed into
 * as in Code's Browser pane (the App shows the real page). tab: the page to show; onTab: another one picked, or the
 * page moved (it opened a sign-in popup and sits hidden behind it, or the popup closed: followed as a pane is).
 */
function PageBrowser({ project, tab, onTab, extra }: { project: string; tab?: number; onTab: (tab?: number) => void; extra?: ReactNode }) {
  const id = useId();
  const tabRef = useRef(tab);
  tabRef.current = tab;
  const onTabRef = useRef(onTab);
  onTabRef.current = onTab;
  useEffect(
    () =>
      useBrowserTabs.subscribe((s, prev) => {
        const to = tabRef.current && tabMoves(prev.tabs, s.tabs).get(tabRef.current);
        if (to) onTabRef.current(to);
      }),
    [],
  );
  const [url, setURL] = useState<string>();
  // the page's size picked in the ⋯ menu: kept while the panel is open (a panel's browser has no layout entry)
  const [size, setSize] = useState<FloatFit>();
  return (
    <BrowserScreen
      project={project}
      tab={tab}
      lastURL={url}
      paneId={`page-browser${id}`}
      focused
      onShow={onTab}
      onURL={setURL}
      size={size}
      onSize={setSize}
      onFocus={() => {}}
      headClassName="h-10"
      extra={extra}
      Body={Body}
    />
  );
}

/** The browser of one conversation: the page its agent opened last (or one picked here). Fills its parent's height. */
export function SessionBrowser({ session, className }: { session: string; className?: string }) {
  const { pages } = useSessionBrowser(session);
  const last = useBrowserOpens((s) => s[session]);
  const all = useBrowserTabs((s) => s.tabs);
  const [picked, setPicked] = useState<{ tab?: number } | null>(null);
  useEffect(() => setPicked(null), [last?.at]); // the agent opened another: show that one
  const alive = (n?: number) => !!n && all.some((x) => x.tab === n && !x.gone);
  const tab = picked ? picked.tab : alive(last?.tab) ? last!.tab : pages.at(-1)?.tab;
  const project = useStickyProject(tab, pages);
  return (
    <div className={cn("flex h-full min-h-0 flex-col", className)}>
      {project && <PageBrowser project={project} tab={tab} onTab={(n) => setPicked({ tab: n })} />}
    </div>
  );
}

/** The project a view's page is in, kept when the page goes (its address bar then opens new pages there). */
function useStickyProject(tab: number | undefined, pages: BrowserTab[]): string {
  const all = useBrowserTabs((s) => s.tabs);
  const ref = useRef("");
  const cur = all.find((x) => x.tab === tab);
  if (cur?.project) ref.current = cur.project;
  else if (!ref.current && pages[0]?.project) ref.current = pages[0].project;
  return ref.current;
}

// ---- a panel's browser column ----

/** Per panel (its key): the page its column shows and whether the user closed the column; lives while the UI does. */
type Column = { tab?: number; closed: boolean; seen: number };
const useColumns = create<Record<string, Column>>(() => ({}));

/** A panel's column: its state, the pages its conversations opened, the last one opened, the page and project shown. */
function usePanelColumn(panel: string, sessions: Set<string>) {
  const loaded = useBrowserTabs((s) => s.loaded);
  useEffect(() => {
    if (!loaded) void loadBrowserTabs();
  }, [loaded]);
  const all = useBrowserTabs((s) => s.tabs);
  const pages = all.filter((x) => !x.gone && x.session && sessions.has(x.session));
  // the last page one of them opened while this UI was up
  const opens = useBrowserOpens();
  let last: { tab: number; at: number } | undefined;
  for (const id of sessions) {
    const o = opens[id];
    if (o && (!last || o.at > last.at)) last = o;
  }
  // first seen: what was opened before isn't news (it doesn't open the column again)
  const st = useColumns((c) => c[panel]) ?? { closed: false, seen: last?.at ?? 0 };
  const save = (next: Column) => useColumns.setState({ [panel]: next });
  const alive = (n?: number) => !!n && all.some((x) => x.tab === n && !x.gone);
  const tab = st.tab !== undefined && (alive(st.tab) || !pages.length) ? st.tab : pages.at(-1)?.tab;
  const project = useStickyProject(tab, pages);
  const has = !!project && (pages.length > 0 || !!st.tab);
  return { st, save, last, tab, project, has };
}

/**
 * The way back to a closed browser column: a button for the row it sits in (the agent column's header, its folded
 * strip, a strip of its own), so the column doesn't leave a strip of its own beside the chat's (asked for). Nothing
 * while the column is open or has no page.
 */
export function PanelBrowserButton({ panel, sessions, className }: { panel: string; sessions: Set<string>; className?: string }) {
  const { st, save, has } = usePanelColumn(panel, sessions);
  if (!has || !st.closed) return null;
  return (
    <Button variant="ghost" size="icon" className={cn("size-7", className)} title={t("browser.showColumn")} onClick={() => save({ ...st, closed: false })}>
      <Globe className="size-3.5" />
    </Button>
  );
}

/** Whether panel's browser column is closed with a page to show (its PanelBrowserButton is there). */
export function usePanelBrowserClosed(panel: string, sessions: Set<string>): boolean {
  const { st, has } = usePanelColumn(panel, sessions);
  return has && st.closed;
}

/**
 * A column at the right of a panel with the pages its conversations opened: the agent column's and the workers' a page
 * of it started (sessions: their ids). Opens by itself when one of them opens a page, on that page; closed with its ×
 * until the next one. "Open in new window" moves the page to a window of its own (BrowserWindow).
 */
export function PanelBrowser({ panel, sessions, width, edge }: { panel: string; sessions: Set<string>; width?: number; edge?: ReactNode }) {
  const { st, save, last, tab, project, has } = usePanelColumn(panel, sessions);
  const inWindow = useBrowserWindows((w) => !!tab && w.tabs.includes(tab));
  const cur = useBrowserTabs((s) => s.tabs.find((x) => x.tab === tab));
  // one of its agents opened a page: show it, the column coming back if it was closed
  useEffect(() => {
    if (last && last.at > st.seen) save({ tab: last.tab, closed: false, seen: last.at });
  }, [last?.at]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!has) return null;
  // closed: its way back is a PanelBrowserButton in the row beside it (WorkView); a phone's tab shows it anyway
  if (st.closed && width) return null;
  const hide = width && (
    <Button variant="ghost" size="icon" className="size-6 shrink-0" title={t("browser.hideColumn")} onClick={() => save({ ...st, closed: true })}>
      <PanelRightClose className="size-3.5" />
    </Button>
  );
  const box = cn("relative flex min-w-0 flex-col", width ? "max-w-[60%] shrink-0 border-l" : "min-h-0 flex-1");
  // The page moved to a window of its own: a page is in one place, so the column holds a card for it instead of a
  // second view of it (pictures, as the window has the real page)
  if (tab && inWindow)
    return (
      <div className={box} style={{ width }}>
        {edge}
        <div className="flex h-10 shrink-0 items-center gap-1 border-b px-2 text-[12px]">
          <Globe className="mr-1 size-4 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground">{cur?.url}</span>
          {hide}
        </div>
        <div className="grid flex-1 place-items-center bg-muted/40 p-6 text-center">
          <div className="max-w-xs space-y-3">
            <AppWindow className="mx-auto size-8 text-muted-foreground" />
            <div className="text-[13px] text-muted-foreground">{t("browser.inWindow")}</div>
            <div className="flex justify-center gap-2">
              <Button size="sm" variant="outline" onClick={() => nativeBrowserWindow(project, tab)}>
                <AppWindow /> {t("browser.showWindow")}
              </Button>
              <Button size="sm" variant="outline" onClick={() => nativeBrowserWindowClose(tab)}>
                <Undo2 /> {t("browser.backToPanel")}
              </Button>
            </div>
          </div>
        </div>
      </div>
    );
  return (
    // width: a column at the panel's right; none: the whole space (a phone's 浏览器 tab)
    <div className={box} style={{ width }}>
      {edge}
      <PageBrowser
        project={project}
        tab={tab}
        onTab={(n) => save({ ...st, tab: n })}
        extra={
          <>
            {tab && (
              <Button
                variant="ghost"
                size="icon"
                className="size-6 shrink-0"
                title={t("browser.newWindow")}
                onClick={() => {
                  openBrowserWindow(project, tab);
                  // an older core can't say the page is out (no card): the column steps aside as it used to
                  if (!useBrowserWindows.getState().known) save({ ...st, closed: true });
                }}
              >
                <SquareArrowOutUpRight className="size-3.5" />
              </Button>
            )}
            {hide}
          </>
        }
      />
    </div>
  );
}

/**
 * Page tab in a window of its own: the desktop App's core brings forward the window it is in already, or opens one more
 * window of Gerdr on /<project>?browser=<tab> (internal/desktop browserwin.go). A core that doesn't track them (it never
 * answered) gets the plain new window; a plain browser opens that address in a new tab.
 */
export function openBrowserWindow(project: string, tab: number) {
  const path = `/${encodeURIComponent(project)}?browser=${tab}`;
  if (isNative() && useBrowserWindows.getState().known) nativeBrowserWindow(project, tab);
  else if (isNative()) nativeNewPathWindow(project, path);
  else window.open(path, "_blank");
}

/** The page this window shows alone (?browser=<tab>: openBrowserWindow), 0 = a usual window. */
export const browserOnly = Number(new URLSearchParams(location.search).get("browser")) || 0;

/** The whole window as one page's browser (App.tsx, browserOnly). */
export function BrowserWindow({ tab: first }: { tab: number }) {
  const all = useBrowserTabs((s) => s.tabs);
  const loaded = useBrowserTabs((s) => s.loaded);
  useEffect(() => {
    if (!loaded) void loadBrowserTabs();
  }, [loaded]);
  const [tab, setTab] = useState<number | undefined>(first);
  const project = useStickyProject(
    tab,
    all.filter((x) => x.tab === first),
  );
  const cur = all.find((x) => x.tab === tab);
  useEffect(() => {
    if (cur?.url) document.title = cur.url;
  }, [cur?.url]);
  // the core keeps which page this window has (it followed the page to a popup: the new one), so the panel's column
  // shows its card and opening the page again brings this window forward (internal/desktop browserwin.go)
  useEffect(() => nativeBrowserWindowTab(tab ?? 0), [tab]);
  // ⌘W / Ctrl+W closes this window, as a browser's would: in the App the menu's key comes as a command (App.tsx
  // runCommand), in a plain browser (or a Windows App without that menu key) it is a keydown here
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "w") {
        e.preventDefault();
        e.stopPropagation();
        window.close();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, []);
  return (
    <div className="flex h-full flex-col bg-background">
      {/* the App's traffic lights: a strip of their height that drags the window (lib/native.ts nativeTitleBar) */}
      {isNative() && <div className="h-(--titlebar-h) shrink-0 border-b bg-sidebar" style={{ WebkitAppRegion: "drag" } as React.CSSProperties} />}
      {project ? (
        <PageBrowser
          project={project}
          tab={tab}
          onTab={setTab}
          extra={
            // back where it came from: closing the window puts it back (the column shows it again)
            <Button variant="ghost" size="icon" className="size-6 shrink-0" title={t("browser.backToPanel")} onClick={() => window.close()}>
              <Undo2 className="size-3.5" />
            </Button>
          }
        />
      ) : (
        <div className="grid flex-1 place-items-center text-[13px] text-muted-foreground">{loaded ? t("browser.gone") : t("browser.loading")}</div>
      )}
    </div>
  );
}
