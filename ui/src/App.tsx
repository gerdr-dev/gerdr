import { KeyRound } from "lucide-react";
import { useEffect, useLayoutEffect, useState } from "react";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { PhoneBar, PhoneConnToast, Sidebar, TabBar } from "./components/Chrome";
import { PaneSwitcher } from "./components/PaneSwitcher";
import { Dialogs } from "./components/Dialogs";
import { GerdrChatProvider } from "./lib/chatHost";
import { AppContextMenu } from "./components/AppContextMenu";
import { TabActive, TabView } from "./components/Panes";
import { WorkView } from "./components/WorkView";
import { OverviewView } from "./components/Overview";
import { watchFollow } from "./lib/overview";
import { tabName } from "./lib/actions";
import { handleKeydown } from "./lib/commands";
import { ACTION_TITLES, setPrefixLabel, toAccelerator, type KeyAction } from "./lib/keys";
import { activeTab, currentSpace, spaceList, type Tab } from "./lib/layout";
import { isPhone, usePhone, useViewportHeight } from "./lib/phone";
import { cn } from "./lib/utils";
import { syncWatch } from "./lib/sync";
import { isNative, nativeAskUpdate, nativeBadge, nativeMenuKeys, nativeSpace, onAppUpdate } from "./lib/native";
import { initAndroid, syncAndroidTheme } from "./lib/android";
import { CliApprove } from "./components/CliApprove";
import { systemNotify } from "./lib/notify";
import { watchInbox } from "./lib/inbox";
import { watchCopyOnSelect } from "./lib/copysel";
import { reloadAddons, setTabNameFn } from "./lib/addons";
import { fontSizeOf, get, keyFor, prefOf, markUnread, set, useStore, visibleSessions } from "./lib/store";
import { boot } from "./lib/sync";
import type { SessionSummary } from "./lib/types";

setTabNameFn(tabName);

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
      for (const cur of Object.values(s.sessions)) {
        const old = before[cur.id];
        if (!old) continue;
        const seen = visible.has(cur.id) && !away;
        const name = cur.title || cur.cwd.split("/").pop() || cur.agent;
        if (old.status !== "waiting" && cur.status === "waiting" && !seen) {
          systemNotify(`在等你 · ${name}`, "需要你批准或回答问题", cur.id);
        }
        if ((old.status === "running" || old.status === "waiting") && (cur.status === "idle" || cur.status === "error") && !seen) {
          markUnread(cur.id);
          systemNotify(cur.status === "error" ? `出错了 · ${name}` : `完成 · ${name}`, cur.error ?? "", cur.id);
        }
      }
      nativeBadge(Object.values(s.sessions).filter((x) => x.status === "waiting").length);
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
  const switcher = useStore((s) => s.switcher);
  const phone = usePhone();
  useTheme();
  useViewportHeight();
  useAttention();

  useEffect(() => {
    boot().then(() => {
      reloadAddons();
      initAndroid();
    });
    const onKey = (e: KeyboardEvent) => handleKeydown(e);
    window.addEventListener("keydown", onKey, true);
    const unwatchCopy = watchCopyOnSelect(); // 实验室「选中即复制」，没开时什么都不做
    const unwatchFollow = watchFollow(); // overview: put agents that start running in by themselves (when switched on)
    const unwatchInbox = watchInbox(); // errors, questions / approvals, low battery → the inbox
    if (isNative()) document.documentElement.classList.add("native");
    // mac App 的菜单和通知点击走这两个入口。
    (window as any).gerdr = {
      runCommand: (id: string) => import("./lib/addons").then((p) => p.runCommand(id)),
      focusSession: (id: string) => import("./lib/actions").then((a) => a.focusSession(id)),
      appUpdate: onAppUpdate, // desktop/main.js pushUpdate
    };
    nativeAskUpdate();
    return () => {
      window.removeEventListener("keydown", onKey, true);
      unwatchCopy();
      unwatchFollow();
      unwatchInbox();
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
  // 抽屉里点了工作空间 / agent（= 换了眼前那个 pane）就收起来：在手机上，去了别处还挡着半个屏没有意义
  const here = `${tab.id}/${tab.focus}`;
  useEffect(() => {
    if (get().drawer) set({ drawer: false });
  }, [here]);
  const dialogOpen = useStore((s) => !!s.dialog);
  useEffect(() => {
    if (dialogOpen) set({ drawer: false });
  }, [dialogOpen]);
  // 转到宽屏（平板横过来）时手机那两块浮层收掉，免得回到手机档时莫名其妙还开着
  useEffect(() => {
    if (!phone) set({ drawer: false, switcher: false });
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
  useEffect(() => syncWatch(), [mode]);
  // The overview covers sidebar and tabs (on a phone too, one tile at a time). Once opened it stays mounted, hidden, so reopening keeps each
  // chat's scroll position, like the kept tabs below.
  const overview = useStore((s) => s.overview.on) && mode !== "work";
  const [overviewMounted, setOverviewMounted] = useState(overview);
  if (overview && !overviewMounted) setOverviewMounted(true);
  const all = new Map<string, Tab>();
  for (const w of layout.workspaces) for (const t of w.tabs) all.set(t.id, t);
  const kept = [tab.id, ...prevKept.filter((id) => id !== tab.id && all.has(id))].slice(0, keep);
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
          <div className="text-[15px] font-medium">需要登录链接</div>
          <div className="text-[13px] text-muted-foreground">
            Gerdr 启动时会打印一个带令牌的链接（<code className="font-mono">http://127.0.0.1:7870/auth?token=…</code>）。在终端里运行{" "}
            <code className="font-mono">gerdr url</code> 再拿一次。
          </div>
        </div>
      </div>
    );
  }
  if (!ready) return <div className="flex h-full items-center justify-center text-[13px] text-muted-foreground">连接中…</div>;

  return (
    <GerdrChatProvider>
      <TooltipProvider delayDuration={400}>
        <div className="relative flex h-full">
          {overviewMounted && (
            <div className={cn("absolute inset-0 z-30", !overview && "pointer-events-none invisible")} aria-hidden={!overview}>
              <OverviewView shown={overview} />
            </div>
          )}
          {/* Work mode has its own top bar with the Code / Work switch, and the Work project draws its own menu */}
          {sidebar && !phone && mode !== "work" && <Sidebar />}
          {/* 手机上侧栏是盖在上面的抽屉（并排放的话对话只剩一百多 px 宽） */}
          {phone && drawer && (
            <div className="fixed inset-0 z-40 flex">
              <Sidebar className="w-[min(18rem,85vw)] shadow-xl" />
              <div className="flex-1 bg-background/50" onClick={() => set({ drawer: false })} />
            </div>
          )}
          <div className="relative flex min-w-0 flex-1 flex-col">
            {/* Work mode covers the agent tabs instead of unmounting them: back in Code mode everything is where it was */}
            {mode === "work" && (
              <div className="absolute inset-0 z-30">
                <WorkView />
              </div>
            )}
            {phone ? <PhoneBar /> : <TabBar />}
            <div className="relative min-h-0 flex-1">
              {/* 按 id 排个固定顺序再画，不能直接用 kept（最近用的排最前）：那样每切一次 tab 顺序就变，React 会在 DOM 里
                挪这些节点，而**挪动 DOM 节点会把里面的滚动位置清零** —— 切走的 tab 一藏起来就回到最顶上（踩过）。
                顺序固定的话切 tab 只改 invisible，节点不动。 */}
              {[...kept].sort().map((id) => {
                const t = all.get(id)!;
                const active = id === tab.id;
                return (
                  <div key={id} className={cn("absolute inset-0", !active && "pointer-events-none invisible")} aria-hidden={!active}>
                    <TabActive.Provider value={active && !overview}>
                      <TabView tab={t} />
                    </TabActive.Provider>
                  </div>
                );
              })}
              {switcher && phone && <PaneSwitcher />}
            </div>
          </div>
        </div>
        <Dialogs />
        <AppContextMenu />
        <CliApprove />
        {/* 手机上底部是输入框和软键盘，toast 放顶上 */}
        <Toaster position={phone ? "top-center" : "bottom-right"} />
        {phone && <PhoneConnToast />}
      </TooltipProvider>
    </GerdrChatProvider>
  );
}
