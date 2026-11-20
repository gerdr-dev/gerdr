import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { create } from "zustand";
import { Globe, PanelRight, PanelRightClose, PictureInPicture2, Undo2 } from "lucide-react";
import { BrowserScreen } from "@/components/browser/BrowserScreen";
import { BrowserOutCard, PopOutButton } from "@/components/browser/BrowserOut";
import { Button } from "@/components/ui/button";
import { loadBrowserTabs, tabMoves, useBrowserOpens, useBrowserTabs, useBrowserWindows } from "@/lib/actions";
import type { BrowserTab } from "@/lib/api";
import { t } from "@/lib/i18n";
import { FLOAT_FITS, type FloatFit } from "@/lib/layout";
import { isNative, nativeBrowserWindowTab, nativeFitWindow } from "@/lib/native";
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
function PageBrowser({
  project,
  tab,
  onTab,
  extra,
  fit,
  onHeadPointerDown,
  ownWindow,
}: {
  project: string;
  tab?: number;
  onTab: (tab?: number) => void;
  extra?: ReactNode;
  /** floating (PanelFloat): the page's pictures, laid out at this size */
  fit?: FloatFit;
  onHeadPointerDown?: (e: React.PointerEvent<HTMLElement>) => void;
  /** the page alone in a window (BrowserWindow): a size picked sizes the window to it */
  ownWindow?: boolean;
}) {
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
      onSize={(f) => {
        setSize(f);
        // the window takes the page's width: the page area grows / shrinks by what it is off from the size
        const box = ownWindow && f ? document.querySelector<HTMLElement>("[data-browser-box]") : null;
        if (box && f) nativeFitWindow(FLOAT_FITS[f].w - box.clientWidth);
      }}
      fit={fit}
      onFocus={() => {}}
      headClassName="h-10"
      onHeadPointerDown={onHeadPointerDown}
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
// out: the page moved to a window of its own from here, the column folded meanwhile; outSeen: its window came up (the
// core listed it), so the window going away means it is back (the column opens on it again)
// float: over the panel's corner instead of a column (box: its place, px from the panel's right / top, and width)
type Column = { tab?: number; closed: boolean; seen: number; out?: number; outSeen?: boolean; float?: boolean; box?: FloatSpot };
type FloatSpot = { r: number; t: number; w: number };
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
  let last: { tab: number; at: number; show?: boolean } | undefined;
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
  // A page moved out folds the column (asked for: the card in its place took the room for nothing); its window
  // closing (⌘W, put back) brings the column back on it
  const outIn = useBrowserWindows((w) => !!st.out && w.tabs.includes(st.out));
  useEffect(() => {
    if (!st.out) return;
    if (outIn && !st.outSeen) save({ ...st, outSeen: true });
    else if (!outIn && st.outSeen) save({ ...st, tab: st.out, closed: false, out: undefined, outSeen: false });
  }, [outIn]); // eslint-disable-line react-hooks/exhaustive-deps
  // one of its agents opened a page: show it, the column coming back if it was closed
  useEffect(() => {
    // floating while the agent works alone, docked when it wants the user in it (`gerdr browser show`), as in Code
    if (last && last.at > st.seen) save({ ...st, tab: last.tab, closed: false, seen: last.at, float: !last.show, out: undefined, outSeen: false });
  }, [last?.at]); // eslint-disable-line react-hooks/exhaustive-deps
  if (!has) return null;
  // closed: its way back is a PanelBrowserButton in the row beside it (WorkView); a phone's tab shows it anyway
  if (st.closed && width) return null;
  const hide = width && (
    <Button variant="ghost" size="icon" className="size-6 shrink-0" title={t("browser.hideColumn")} onClick={() => save({ ...st, closed: true })}>
      <PanelRightClose className="size-3.5" />
    </Button>
  );
  // a phone's tab (no width) is always the whole space
  const floating = !!width && !!st.float;
  const toggle = width && (
    <Button
      variant="ghost"
      size="icon"
      className="size-6 shrink-0"
      title={floating ? t("browser.dockColumn") : t("browser.floatColumn")}
      onClick={() => save({ ...st, float: !floating })}
    >
      {floating ? <PanelRight className="size-3.5" /> : <PictureInPicture2 className="size-3.5" />}
    </Button>
  );
  const box = cn("relative flex min-w-0 flex-col", width ? "max-w-[60%] shrink-0 border-l" : "min-h-0 flex-1");
  // The page moved to a window of its own: a page is in one place, so the column holds a card for it instead of a
  // second view of it (pictures, as the window has the real page)
  if (tab && inWindow)
    return (
      <div className={box} style={{ width }}>
        {edge}
        <BrowserOutCard project={project} tab={tab} url={cur?.url} end={hide} />
      </div>
    );
  const extra = (
    <>
      {tab && (
        <PopOutButton
          project={project}
          tab={tab}
          // folded while the page is out (an older core can't say when it's back: then just folded)
          onOut={() => save({ ...st, closed: true, ...(useBrowserWindows.getState().known ? { out: tab, outSeen: false } : {}) })}
        />
      )}
      {toggle}
      {hide}
    </>
  );
  if (floating)
    return (
      <PanelFloat spot={st.box} onSpot={(b) => save({ ...useColumns.getState()[panel], box: b })}>
        {(onMove) => <PageBrowser project={project} tab={tab} onTab={(n) => save({ ...st, tab: n })} fit="pc" onHeadPointerDown={onMove} extra={extra} />}
      </PanelFloat>
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
              <PopOutButton
                project={project}
                tab={tab}
                // folded while the page is out (an older core can't say when it's back: then just folded)
                onOut={() => save({ ...st, closed: true, ...(useBrowserWindows.getState().known ? { out: tab, outSeen: false } : {}) })}
              />
            )}
            {toggle}
            {hide}
          </>
        }
      />
    </div>
  );
}

const FLOAT_W = { min: 280, def: 440, max: 960 };
const FLOAT_HEAD = 41; // the title bar (h-10) and its border
const floatH = (w: number) => ((w - 2) * FLOAT_FITS.pc.h) / FLOAT_FITS.pc.w + FLOAT_HEAD;

/**
 * A panel's browser floating over its top right corner, as a floating Browser pane in Code (Panes.tsx FloatPane): the
 * page's pictures laid out at a desktop's size, dragged by its title bar, resized from its bottom-left corner at the
 * page's shape, kept inside the panel. Positioned against the Work view's body (WorkView: its relative box).
 */
function PanelFloat({
  spot,
  onSpot,
  children,
}: {
  spot?: FloatSpot;
  onSpot: (s: FloatSpot) => void;
  children: (onMove: (e: React.PointerEvent<HTMLElement>) => void) => ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const s = spot ?? { r: 12, t: 12, w: FLOAT_W.def };
  const gesture = (e: React.PointerEvent<HTMLElement>, resize: boolean) => {
    if (e.button !== 0) return;
    // the title bar's own buttons and address bar keep their clicks
    if (!resize && (e.target as Element).closest("button, a, input, textarea, [role=menuitem]")) return;
    const room = (ref.current?.offsetParent as HTMLElement | null)?.getBoundingClientRect();
    if (!room) return;
    e.preventDefault();
    const x0 = e.clientX,
      y0 = e.clientY;
    const fit = (n: FloatSpot): FloatSpot => {
      const w = Math.min(Math.max(FLOAT_W.min, Math.min(n.w, FLOAT_W.max)), room.width);
      return { w, r: Math.min(Math.max(0, n.r), room.width - w), t: Math.min(Math.max(0, n.t), Math.max(0, room.height - floatH(w))) };
    };
    const move = (ev: PointerEvent) => {
      const dx = ev.clientX - x0,
        dy = ev.clientY - y0;
      onSpot(fit(resize ? { ...s, w: s.w - dx } : { ...s, r: s.r - dx, t: s.t + dy }));
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    document.body.style.cursor = resize ? "nesw-resize" : "grabbing";
    document.body.style.userSelect = "none";
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
  };
  return (
    <div
      ref={ref}
      className="absolute z-30 flex max-w-full flex-col overflow-hidden rounded-md border bg-background shadow-2xl"
      style={{ right: s.r, top: s.t, width: s.w, height: floatH(s.w) }}
    >
      {children((e) => gesture(e, false))}
      <div className="absolute bottom-0 left-0 size-3 cursor-nesw-resize touch-none" onPointerDown={(e) => gesture(e, true)} />
    </div>
  );
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
          ownWindow
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
