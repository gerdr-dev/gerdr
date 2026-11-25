import { KeyRound } from "lucide-react";
import { memo, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ConnToast } from "./components/ConnStatus";
import { PhoneBar as PhoneBarView } from "./components/PhoneBar";
import { TabBar as TabBarView } from "./components/TabBar";
import { Dialogs as DialogsView } from "./components/Dialogs";
import { GerdrChatProvider } from "./lib/chatHost";
import { AppContextMenu as AppContextMenuView } from "./components/AppContextMenu";
import { TabActive, TabView as TabViewFn } from "./components/Panes";
import { WorkView as WorkViewFn } from "./components/WorkView";
import { OverviewView as OverviewViewFn } from "./components/Overview";
import { UnifiedSidebar as UnifiedSidebarView } from "./components/UnifiedSidebar";
import { Rail as RailView, RailCorner } from "./components/Rail";
import { tabName } from "./lib/actions";
import { handleKeydown, handleKeyup, releaseHeld } from "./lib/commands";
import { ACTION_TITLES, setPrefixLabel, toAccelerator, type KeyAction } from "./lib/keys";
import { activeTab, currentSpace, leaves, mutedSession, spaceList, type Tab } from "./lib/layout";
import { HOLD_MS, useCaughtUp } from "./lib/catchup";
import { isPhone, usePhone, useViewportHeight } from "./lib/phone";
import { cn } from "./lib/utils";
import { syncWatch } from "./lib/sync";
import { isNative, nativeAskUpdate, nativeBadge, nativeMenuKeys, nativeSpace, onAppUpdate } from "./lib/native";
import { initAndroid, syncAndroidTheme } from "./lib/android";
import { CliApprove as CliApproveView } from "./components/CliApprove";
import { KeepDevicePrompt } from "./components/KeepDevice";
import { systemNotify } from "./lib/notify";
import { watchInbox } from "./lib/inbox";
import { watchBadges } from "./lib/badges";
import { watchCopyOnSelect } from "./lib/copysel";
import { watchNavButtons } from "./lib/actions/nav";
import { reloadAddons, setTabNameFn } from "./lib/addons";
import { AddonSlot as AddonSlotView } from "./components/AddonSlot";
import { focusedSession, fontSizeOf, get, hostsCode, keyFor, prefOf, markUnread, set, useStore, visibleSessions } from "./lib/store";
import { boot } from "./lib/sync";
import { BrowserWindow, browserOnly } from "./core/SessionBrowser";
import type { SessionSummary } from "./lib/types";
import { t } from "./lib/i18n";

setTabNameFn(tabName);

// App subscribes to the layout, so every tab switch re-renders it. Unmemoised, that re-rendered everything below too:
// the rail, every closed dialog, and every kept tab down to its composer and tooltips, 2221 component renders per switch
// (tools/scrolltest/tabrenders.mjs; switching took 330-450ms with the CPU 4x slower, tabswitch.mjs). Each of these reads
// what it needs from the store itself and takes no props or stable ones, so memo lets them render only when that changes.
const Rail = memo(RailView);
const UnifiedSidebar = memo(UnifiedSidebarView);
const TabBar = memo(TabBarView);
const PhoneBar = memo(PhoneBarView);
const Dialogs = memo(DialogsView);
const AppContextMenu = memo(AppContextMenuView);
const CliApprove = memo(CliApproveView);
const AddonSlot = memo(AddonSlotView);
const WorkView = memo(WorkViewFn);
const OverviewView = memo(OverviewViewFn);
// A tab's object only changes when that tab does (selectTabIn keeps the others), so kept tabs left alone stay put
const TabView = memo(TabViewFn);

function useTheme() {
  const theme = useStore((s) => prefOf(s, "theme"));
  const font = useStore(fontSizeOf);
  // Layout effect, not a plain effect: .dark has to be on <html> before the first frame is painted, or that frame
  // comes out light (a white flash on a dark phone, right as the Android app's loading cover fades out)
  useLayoutEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const apply = () => {
      const dark = theme === "dark" || (theme === "system" && mq.matches);
      document.documentElement.classList.toggle("dark", dark);
      document.documentElement.style.colorScheme = dark ? "dark" : "light";
      syncAndroidTheme(); // 安卓 App 的系统栏跟着换底色（要在 .dark 落下之后读）
    };
    apply();
    mq.addEventListener("change", apply);
    return () => mq.removeEventListener("change", apply);
  }, [theme]);
  useEffect(() => {
    document.documentElement.style.setProperty("--gerdr-font", `${font}px`);
  }, [font]);
}

/** 会话状态变化 → 未读标记、系统通知、Dock 角标。 */
function useAttention() {
  useEffect(() => {
    let prev: Record<string, SessionSummary> = get().sessions;
    return useStore.subscribe((s) => {
      if (s.sessions === prev) return;
      const before = prev;
      prev = s.sessions;
      // 手机上左右分屏只画焦点那一边，另一边看不见，不能算「看着呢」
      const visible = new Set(visibleSessions(s.layout, isPhone()));
      const away = document.visibilityState !== "visible" || !document.hasFocus();
      // Approvals, questions and errors that go in the inbox get their system notification from there (lib/inbox.ts),
      // with the actual command / question; these generic ones are only for kinds the user kept out of the inbox
      const inbox = s.config.inbox ?? {};
      // (a session waiting doesn't say on what: only skip when the inbox takes both kinds, a duplicate beats a miss)
      const inboxWaits = inbox.permission !== false && inbox.ask !== false;
      const inboxErrors = inbox.error !== false;
      for (const cur of Object.values(s.sessions)) {
        const old = before[cur.id];
        // A push replaces one summary and keeps the others' objects: skip the unchanged ones before anything costly
        // (mutedSession walks every pane; per session on every push it was most of a phone's JS for a pushed summary).
        if (!old || old === cur) continue;
        const seen = visible.has(cur.id) && !away;
        // Do not disturb (its own, or its group's): the row still turns unread, but no system notification
        const quiet = seen || !!cur.muted || mutedSession(s.layout, cur.id);
        const name = cur.title || cur.cwd.split("/").pop() || cur.agent;
        if (old.status !== "waiting" && cur.status === "waiting" && !quiet && !inboxWaits) {
          systemNotify(t("app.notifyWaiting", { name }), t("app.notifyWaitingBody"), cur.id);
        }
        // quiet: the turn was only a /clear the person just typed, or they stopped it — no unread mark, no notification
        const ended = (old.status === "running" || old.status === "waiting") && (cur.status === "idle" || cur.status === "error");
        if (ended && !seen && !(cur.status === "idle" && cur.quiet)) {
          markUnread(cur.id);
          if (quiet) continue;
          if (cur.status === "idle") systemNotify(t("app.notifyDone", { name }), "", cur.id);
          else if (!inboxErrors) systemNotify(t("app.notifyError", { name }), cur.error ?? "", cur.id);
        }
      }
      nativeBadge(Object.values(s.sessions).filter((x) => x.status === "waiting" && !x.muted && !mutedSession(s.layout, x.id)).length);
    });
  }, []);
}

export default function App() {
  const ready = useStore((s) => s.ready);
  const authError = useStore((s) => s.authError);
  const layout = useStore((s) => s.layout);
  const sidebar = useStore((s) => s.sidebar);
  const prefix = useStore((s) => s.config.prefix);
  const drawer = useStore((s) => s.drawer);
  const phone = usePhone();
  useTheme();
  // html's own height, not --vvh: a custom property on html restyles the whole page (device.ts)
  useViewportHeight({ height: true });
  useAttention();

  useEffect(() => {
    boot().then(() => {
      reloadAddons();
      initAndroid();
      // ?settings=remote: a handoff from the phone App landed here to add a passkey (server enterHandoff)
      const q = new URLSearchParams(location.search);
      if (q.get("settings") === "remote") {
        q.delete("settings");
        history.replaceState(null, "", location.pathname + (q.size ? `?${q}` : "") + location.hash);
        set({ dialog: { kind: "settings", tab: "remote" } });
      }
    });
    // A window showing one browser page (browserOnly) shares the layout with the others, so Gerdr's shortcuts there
    // would close / split / switch panes the person can't see: none, only its own ⌘W (BrowserWindow)
    const onKey = (e: KeyboardEvent) => !browserOnly && handleKeydown(e);
    window.addEventListener("keydown", onKey, true);
    // Held add-on commands end on their key's keyup; switching away (⌘Tab) eats that keyup, so blur ends them too
    window.addEventListener("keyup", handleKeyup, true);
    window.addEventListener("blur", releaseHeld);
    // The page itself never scrolls sideways (everything lives in its own boxes), but the browser scrolls it anyway to
    // reveal an element focused while it is still off the right edge: a drawer that takes focus while it slides in
    // (whose box counts as overflow once a transformed ancestor contains it, as the App's title-bar strip does in
    // WorkView). Everything then jumps left and slides back as the drawer arrives. Undo it at once; vertical is left
    // alone, as iOS scrolls the document for the keyboard.
    const unscrollX = () => window.scrollX && window.scrollTo(0, window.scrollY);
    window.addEventListener("scroll", unscrollX);
    const unwatchCopy = watchCopyOnSelect(); // 实验室「选中即复制」，没开时什么都不做
    const unwatchInbox = watchInbox(); // errors, questions / approvals, low battery → the inbox
    const unwatchBadges = watchBadges(); // panels' dots / numbers on their rail buttons
    const unwatchNav = watchNavButtons(); // the mouse's back / forward buttons → where focus was before
    if (isNative()) document.documentElement.classList.add("native");
    // mac App 的菜单和通知点击走这两个入口。
    (window as any).gerdr = {
      // a window showing one browser page (browserOnly) has no panes: ⌘W (the menu's close pane / tab) closes it
      runCommand: (id: string) =>
        browserOnly ? (id === "closePane" || id === "closeTab") && window.close() : import("./lib/addons").then((p) => p.runCommand(id)),
      focusSession: (id: string) => import("./lib/actions").then((a) => a.focusSession(id)),
      appUpdate: onAppUpdate, // desktop/main.js pushUpdate
    };
    nativeAskUpdate();
    return () => {
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("keyup", handleKeyup, true);
      window.removeEventListener("blur", releaseHeld);
      window.removeEventListener("scroll", unscrollX);
      unwatchCopy();
      unwatchInbox();
      unwatchBadges();
      unwatchNav();
    };
  }, []);

  useEffect(() => setPrefixLabel(prefix), [prefix]);

  // App 菜单上的键跟着设置走：在设置里把 ⌘D 删了 / 改了，菜单不跟着的话按 ⌘D 还是会从菜单那边分屏
  const keys = useStore((s) => s.config.keys);
  useEffect(() => {
    if (!isNative()) return;
    const cfg = get().config;
    const out: Record<string, string | null> = {};
    for (const a of Object.keys(ACTION_TITLES) as KeyAction[]) out[a] = keyFor(cfg, a).map(toAccelerator).find(Boolean) ?? null;
    nativeMenuKeys(out);
  }, [keys]);

  const tab = activeTab(layout);
  // Picking a project / agent in the drawer (= the pane in front changed) closes it: on a phone it would only cover half
  // the screen. Not before that session has loaded or caught up, though (lib/catchup): closing at once uncovered the
  // session we came from, which the new one then replaced a moment later (reported). The picked row lights up meanwhile.
  const here = `${tab.id}/${tab.focus}`;
  const hereSession = focusedSession(layout);
  const hereCatching = useStore((s) => !!hereSession && !!s.catchingUp[hereSession]);
  const closeDrawer = useRef(false);
  useEffect(() => {
    if (get().drawer) closeDrawer.current = true;
  }, [here]);
  useEffect(() => {
    if (!closeDrawer.current) return;
    const close = () => {
      closeDrawer.current = false;
      if (get().drawer) set({ drawer: false });
    };
    if (!hereCatching) return close();
    const t = setTimeout(close, HOLD_MS);
    return () => clearTimeout(t);
  }, [here, hereCatching]);
  const dialogOpen = useStore((s) => !!s.dialog);
  useEffect(() => {
    if (dialogOpen) set({ drawer: false });
  }, [dialogOpen]);
  // Going wide (a tablet turned sideways) closes the phone drawer, so it isn't oddly still open when back to phone width
  useEffect(() => {
    if (!phone) set({ drawer: false });
  }, [phone]);
  // Which (herdr-style) session this window shows: in the title when there's more than one (tells the App's windows apart),
  // in the address (/<project id>, so a reload or a window opened for a project stays on it), and told to the App shell
  const spaceId = useStore((s) => currentSpace(s.layout));
  const spaceName = useStore((s) => spaceList(s.layout).find((x) => x.id === spaceId)?.name ?? spaceId);
  const many = useStore((s) => spaceList(s.layout).length > 1);
  const space = { id: spaceId, name: spaceName, many };
  useEffect(() => {
    document.title = space.many ? `${tabName(tab)} · ${space.name} · Gerdr` : `${tabName(tab)} · Gerdr`;
  }, [tab, space.name, space.many]);
  useEffect(() => {
    if (!ready) return; // the layout isn't loaded yet: don't overwrite the project this window was opened with
    const u = new URL(location.href);
    const path = "/" + encodeURIComponent(space.id);
    if (u.pathname !== path || u.searchParams.has("space")) {
      u.pathname = path;
      u.searchParams.delete("space");
      history.replaceState(history.state, "", u);
    }
    nativeSpace(space.id, space.name);
  }, [ready, space.id, space.name]);

  // 切走的 tab 不卸载，藏起来（最近用过的几个，keepTabs 个）：展开状态、滚动位置、没发出去的字都还在。
  // 藏用 visibility 不用 display:none（后者把滚动位置丢了）。占多少内存在设置 →「资源」里看。
  const keep = Math.max(
    1,
    useStore((s) => prefOf(s, "keepTabs") ?? 6),
  );
  const prevKept = useStore((s) => s.keptTabs);
  const mode = useStore((s) => s.mode);
  const workKept = useStore((s) => s.workKept);
  useEffect(() => syncWatch(), [mode]);
  // The phone-only overview (PhoneOverview) covers the tabs one tile at a time. Once opened it stays mounted, hidden,
  // so reopening keeps each chat's scroll position, like the kept tabs below. Desktop uses the sidebar's Active list directly.
  // Read unconditionally: `phone && useStore(...)` skipped the hook on wide screens, so a phone crossing the width
  // (rotating, split screen, the WebView resizing on resume) changed the hook count and crashed with React #311.
  const overviewOn = useStore((s) => s.overview.on);
  // A panel that hosts Code (core CodeView) is shown: Code's layout is on screen, the panel's page stays under it
  const hosted = useStore((s) => hostsCode(s));
  const codeOn = mode !== "work" || hosted;
  const overview = phone && overviewOn && codeOn;
  const unified = !phone;
  const [overviewMounted, setOverviewMounted] = useState(overview);
  if (overview && !overviewMounted) setOverviewMounted(true);
  const all = new Map<string, Tab>();
  for (const w of layout.workspaces) for (const t of w.tabs) all.set(t.id, t);
  // Switched to a tab that wasn't kept: its transcripts are cached but behind until the catch-up arrives. Keep the
  // tab we came from on screen till then (lib/catchup), else the old state draws first and the new one flashes in.
  // It stays mounted meanwhile, over the keepTabs count if need be (with keepTabs = 1 it would unmount at once).
  const tabSessions = leaves(tab.root)
    .map((p) => p.sessionId ?? p.sub?.sessionId)
    .filter((x): x is string => !!x);
  const shownTab = useCaughtUp(tab.id, tabSessions, (id) => all.has(id) && prevKept.includes(id));
  const held = shownTab && shownTab !== tab.id ? [shownTab] : [];
  const kept = [tab.id, ...held, ...prevKept.filter((id) => id !== tab.id && id !== shownTab && all.has(id))].slice(0, keep + held.length);
  useEffect(() => {
    if (kept.join() !== prevKept.join()) {
      set({ keptTabs: kept });
      syncWatch();
    }
  });

  if (authError) {
    // 远程那条路上凭据掉了（被踢、过期、要重验 passkey）：整页刷新，服务端会给配对页
    if (location.protocol === "https:") {
      location.reload();
      return null;
    }
    return (
      <div className="flex h-full items-center justify-center p-6">
        <div className="max-w-md space-y-3 text-center">
          <KeyRound className="mx-auto size-8 text-muted-foreground" />
          <div className="text-[15px] font-medium">{t("app.loginTitle")}</div>
          <div className="text-[13px] text-muted-foreground">
            {t("app.loginBefore")}
            <code className="font-mono">http://127.0.0.1:7870/auth?token=…</code>
            {t("app.loginMiddle")} <code className="font-mono">gerdr url</code> {t("app.loginAfter")}
          </div>
        </div>
      </div>
    );
  }
  if (!ready) return <LoadingScreen text={t("app.connecting")} />;
  // a window opened for one page of a project's browser (a panel's "open in new window")
  if (browserOnly) return <BrowserWindow tab={browserOnly} />;

  return (
    <GerdrChatProvider>
      <TooltipProvider delayDuration={400}>
        <div className="relative flex h-full">
          {overviewMounted && !unified && (
            <div className={cn("absolute inset-0 z-30", !overview && "pointer-events-none invisible")} aria-hidden={!overview}>
              <OverviewView shown={overview} />
            </div>
          )}
          {/* gerdr's strip on the left in every mode (projects, Code / Work, add-ons, inbox, settings); Code mode's list
              beside it. Work mode has no list: the rest is the Work site's, then its agent */}
          {!phone && <Rail />}
          {!phone && <RailCorner />}
          {sidebar && !phone && codeOn && <UnifiedSidebar />}
          {/* 手机上侧栏是盖在上面的抽屉（并排放的话对话只剩一百多 px 宽） */}
          {phone && drawer && (
            <div role="dialog" aria-modal="true" className="fixed inset-0 z-40 flex">
              <UnifiedSidebar className="w-[min(20rem,88vw)] shadow-xl" />
              <div className="flex-1 bg-background/50" onClick={() => set({ drawer: false })} />
            </div>
          )}
          <div className="relative flex min-w-0 flex-1 flex-col">
            {/* Work mode covers the agent tabs instead of unmounting them: back in Code mode everything is where it was.
                The same the other way: once opened the Work view stays mounted, only hidden in Code mode — unmounting it
                would throw away the Work page's state, everything the user had open there (reported, back when it was a frame). */}
            {workKept && (
              // Hosting Code: the page stays mounted and counts as shown (its hooks run, its sidebar part is portaled
              // into Code's list), but it must not cover the panes. Its own overlays opt back in with `visible`.
              <div className={cn("absolute inset-0 z-30", codeOn && "pointer-events-none invisible")} aria-hidden={codeOn}>
                <TabActive.Provider value={mode === "work" && !hosted}>
                  <WorkView />
                </TabActive.Provider>
              </div>
            )}
            {/* The unified sidebar lists the tabs itself; with it hidden the tab bar comes back (and its show-sidebar button) */}
            {phone ? <PhoneBar /> : !(unified && sidebar) && <TabBar />}
            <div className="relative min-h-0 flex-1">
              {/* 按 id 排个固定顺序再画，不能直接用 kept（最近用的排最前）：那样每切一次 tab 顺序就变，React 会在 DOM 里
                挪这些节点，而**挪动 DOM 节点会把里面的滚动位置清零** —— 切走的 tab 一藏起来就回到最顶上（踩过）。
                顺序固定的话切 tab 只改 invisible，节点不动。 */}
              {[...kept].sort().map((id) => {
                const t = all.get(id)!;
                const active = id === shownTab;
                return (
                  <div key={id} className={cn("absolute inset-0", !active && "pointer-events-none invisible")} aria-hidden={!active}>
                    <TabActive.Provider value={active && !overview}>
                      <TabView tab={t} />
                    </TabActive.Provider>
                  </div>
                );
              })}
            </div>
          </div>
        </div>
        <Dialogs />
        {/* Add-ons' dialogs / drawers (g.slot("overlay")): always mounted, whatever the sidebar or mode is doing */}
        <AddonSlot name="overlay" />
        <AppContextMenu />
        <CliApprove />
        {/* A device just paired with a code: keep it (passkey / Face ID) or use it just for now */}
        <KeepDevicePrompt />
        {/* Top centre everywhere: every pane's composer is at the bottom (and the phone's keyboard), so a bottom-corner toast
            covered the rightmost pane's input box (user report). The top is pane title bars, fine to cover for a moment.
            40px from the top, the tab bar's bottom border (h-10, TabBar.tsx): the card hangs right under the bar instead
            of floating half over the bar and half over the chat (user's pick, after trying 24 / 34 / 44). */}
        <Toaster position="top-center" offset={{ top: 40 }} />
        {/* A lost connection: a toast everywhere now; the bottom row that showed it on wider screens became the rail */}
        <ConnToast />
      </TooltipProvider>
    </GerdrChatProvider>
  );
}

// LoadingScreen: the "connecting" screen, drawn exactly like the page's own loading screen (loadingHTML in
// internal/uisite/html.go) that it replaces: same box, spinner, font and colours (system colours, since that one shows
// before any theme), so mounting only swaps the line of text. The spinner's phase follows the page clock like that
// one's, so it carries on at the same angle instead of restarting. Keep the two in step.
function LoadingScreen({ text }: { text: string }) {
  const [delay] = useState(() => -(performance.now() % 800));
  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 12,
        font: "13px/1.5 system-ui,-apple-system,sans-serif",
        color: "CanvasText",
        background: "Canvas",
      }}
    >
      <style>{"@keyframes __gerdr_spin{to{transform:rotate(360deg)}}"}</style>
      <div
        style={{
          width: 22,
          height: 22,
          boxSizing: "border-box",
          borderRadius: "50%",
          border: "2px solid color-mix(in srgb,CanvasText 15%,transparent)",
          borderTopColor: "color-mix(in srgb,CanvasText 60%,transparent)",
          animation: `__gerdr_spin .8s linear ${delay}ms infinite`,
        }}
      />
      <div style={{ opacity: 0.6 }}>{text}</div>
    </div>
  );
}
