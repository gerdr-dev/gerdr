// gerdr add-on API. gerdr overwrites this file with the current version on every start: don't edit it, edit your
// add-on.
//
// An add-on is a folder in the add-on directory (~/.gerdr/addons/<id>/):
//
//   addon.json   optional: {"name": "…", "version": "1.0.0", "description": "…", "icon": "icon.png",
//                           "main": "index.ts", "service": {…}}
//   index.ts     the UI half (or what "main" names): export default defineAddon({...}). It may import other files of
//                the folder; images, sounds and other files it ships are reached with g.asset("icon.png").
//
// Saving takes effect at once, no restart. A folder whose name starts with _ isn't loaded; Settings → Add-ons
// switches one off without renaming it. Add-ons from a store (Settings → Add-ons → Store) are folders like any other;
// editing one is fine, but the next update from the store asks before overwriting it.
//
// The UI half runs in the UI (the same page), and changes UI things: the names of tabs / panes / workspaces, commands
// and keys, session events, notifications.
//
// Services. A UI half runs in every open window (the App, a remote browser, the phone) and in none while no window is
// open. Work that must keep running and exist exactly once — holding a Bluetooth wristband, which accepts one
// connection at a time — goes in a service: addon.json names a process, and gerdr's backend starts it, restarts it
// when it dies, and stops it when the add-on is removed or switched off:
//
//   ~/.gerdr/addons/band/addon.json   {"service": {"command": "wristkit", "args": ["gerdr"], "env": {"K": "V"}}}
//   ~/.gerdr/addons/band/index.ts     (optional) the UI half, talks to the process through g.service
//
// command: a name on PATH (the login shell's, like agents get), an absolute path, or "./bin/x" relative to the add-on.
// The process runs in the add-on's folder, with GERDR_ADDON (its id), GERDR_ADDON_DIR and GERDR_ADDON_DATA set and the
// gerdr command on PATH. Keep files in GERDR_ADDON_DATA, not the add-on's folder: any change there counts as editing
// the add-on and reloads add-ons in every open window, and an update from a store replaces the folder. It talks to
// gerdr in JSON lines over stdin / stdout:
//
//   gerdr → process (stdin)
//     {"type":"hello","version":"…","addon":"band"}      first line of every start
//     {"type":"sessions","sessions":[ServiceSession…]}   the sessions open in the UI: once after hello, then only when
//                                                         a field changes (a turn streaming tokens is not a change)
//     {"type":"message","data":…}                        sent by the UI half, g.service.send(data)
//   process → gerdr (stdout)
//     {"type":"status","text":"band connected"}          shown under the add-on in Settings → Add-ons
//     {"type":"message","data":…}                        to g.service.on("message") in every open window
//
// Other stdout lines go to gerdr's log; stderr goes to ~/.gerdr/logs/addons/<id>.log.
// stdin closing means gerdr is stopping it (quitting, add-on removed): clean up (let go of the device) and exit.
// gerdr waits 3 seconds, then kills the process group. A process that exits is restarted after 1s, backing off to 1min.

export {};

declare global {
  function defineAddon(addon: GerdrAddon): GerdrAddon;
  /** @deprecated the old name, from when add-ons were plugins: use defineAddon. */
  function definePlugin(addon: GerdrAddon): GerdrAddon;

  interface GerdrAddon {
    name?: string;
    /** Return a function to clean up when the add-on is unloaded (edited, removed, switched off). */
    setup(g: Gerdr): void | (() => void);
  }

  /** Undoes a registration. */
  type Dispose = () => void;

  interface Gerdr {
    /** gerdr's version. */
    readonly version: string;
    /** This add-on's id: its folder name. */
    readonly id: string;
    /**
     * A URL for a file of this add-on's folder, for an <img src>, an Audio, a fetch():
     * g.asset("icon.png"), g.asset("sounds/done.wav"). Changes when the add-on is reloaded, so a new version shows.
     */
    asset(path: string): string;

    /**
     * 改 tab 在标签栏上显示的名字。多个扩展按文件夹名顺序串起来：ctx.label 是前面的扩展
     * （或默认规则）给出的名字。返回 undefined 表示不改。tab.name 是用户起的原名。
     */
    tabLabel(fn: (tab: TabInfo, ctx: { index: number; workspace: WorkspaceInfo; label: string }) => string | void): Dispose;
    /** 改 pane 顶上那行标题。默认是会话标题（第一句话 / agent 起的名字）。 */
    paneTitle(fn: (pane: PaneInfo, ctx: { label: string; tab: TabInfo }) => string | void): Dispose;
    /** 改侧栏里工作空间的名字。 */
    workspaceLabel(fn: (ws: WorkspaceInfo, ctx: { index: number; label: string }) => string | void): Dispose;

    /**
     * 给会话起标题（侧栏、pane 顶上那个）。每一轮结束时调；返回字符串就用它，返回 undefined 不改。
     *
     * 不注册的话 gerdr 自己起：每段上下文（两次 /clear 之间）的第 1 句和第 3 句话那一轮结束时，拿这段里说过的话
     * 让模型起一个。扩展给出过名字后，这段上下文里默认的就不再动它（/clear 之后重新算）；人改过名的会话不会调到这里。
     */
    sessionTitle(fn: (session: SessionInfo, ctx: SessionTitleContext) => string | void | Promise<string | void>): Dispose;

    /**
     * 加一条命令：出现在命令面板（prefix+:）里；给了 key 的话还能用快捷键触发。
     * key 的写法和设置里一样："prefix+a"、"prefix+shift+k"、"ctrl+alt+p"。
     */
    command(cmd: { id: string; title: string; key?: string; run(ctx: CommandContext): void | Promise<void> }): Dispose;

    on(event: "turnEnd", fn: (e: SessionEvent & { stopReason: string; error?: string }) => void): Dispose;
    on(event: "needsInput", fn: (e: SessionEvent & { kind: "permission" | "ask" }) => void): Dispose;
    on(event: "sessionCreated", fn: (e: SessionEvent) => void): Dispose;
    /** Today's turns done or cost changed (g.stats()). Coalesced: at most every couple of seconds. */
    on(event: "stats", fn: () => void): Dispose;
    on(event: "focusChanged", fn: (e: { pane?: PaneInfo; tab?: TabInfo; workspace?: WorkspaceInfo }) => void): Dispose;

    /** 弹一条提示。system: true 时用系统通知（mac App / 浏览器通知权限）。 */
    notify(text: string, opts?: { level?: "info" | "error"; system?: boolean }): void;

    /** 当前界面状态的只读快照。 */
    state(): StateSnapshot;

    // ---- drawing ----
    // JSX works in .tsx files (it compiles to gerdr's React.createElement); hooks come from g.React. Style with Tailwind
    // classes and gerdr's color tokens (bg-muted, text-muted-foreground, bg-brand, text-ok / text-warn / text-bad…),
    // so it looks right in light and dark.

    /** gerdr's React: the components an add-on draws are mounted by it, so hooks must come from here. */
    readonly React: GerdrReact;
    /** gerdr's own building blocks (shadcn/ui), so an add-on looks like the rest of the app. */
    readonly ui: {
      Button: Component<{ variant?: "default" | "outline" | "ghost" | "secondary" | "destructive"; size?: "default" | "sm" | "icon" | "icon-sm"; className?: string; title?: string; disabled?: boolean; onClick?: (e: any) => void; children?: any }>;
      Popover: Component<{ open?: boolean; onOpenChange?: (open: boolean) => void; children?: any }>;
      PopoverTrigger: Component<{ asChild?: boolean; children?: any }>;
      PopoverContent: Component<{ side?: "top" | "bottom" | "left" | "right"; align?: "start" | "center" | "end"; className?: string; children?: any }>;
      Tooltip: Component<{ children?: any }>;
      TooltipTrigger: Component<{ asChild?: boolean; children?: any }>;
      TooltipContent: Component<{ side?: "top" | "bottom" | "left" | "right"; children?: any }>;
      Switch: Component<{ checked?: boolean; onCheckedChange?: (v: boolean) => void; disabled?: boolean }>;
      /** Joins class names, later Tailwind classes winning (clsx + tailwind-merge). */
      cn(...classes: any[]): string;
    };
    /** Every lucide-react icon: <g.icons.Timer className="size-3.5" />. */
    readonly icons: Record<string, Component<{ className?: string }>>;

    /**
     * Put a component in one of gerdr's slots. It is re-rendered with gerdr; one that throws is dropped and its error
     * shown in Settings → Add-ons. Several add-ons in one slot: by order, then by add-on id.
     *   "sidebar"    the left sidebar, above its bottom row (full width, no props). The slot pads above and below
     *                once for all blocks: give yours horizontal padding only, or two blocks leave a gap between them
     *   "paneTitle"  every pane's title bar, before its ⋯ menu (props: { pane: PaneInfo; tab: TabInfo }); keep it small
     */
    slot(name: "sidebar", component: Component<{}>, opts?: { order?: number }): Dispose;
    slot(name: "paneTitle", component: Component<{ pane: PaneInfo; tab: TabInfo }>, opts?: { order?: number }): Dispose;

    /** A button in the composer's bottom bar (next to the model menu). icon: an element, e.g. <g.icons.Zap />. */
    composerAction(action: { id: string; title: string; icon?: any; run(ctx: ComposerContext): void | Promise<void> }): Dispose;

    /**
     * An item in the right-click menu (the desktop App and the Android App; a browser keeps its own menu). Shown when
     * when(ctx) is true; without when, only while text is selected.
     */
    contextMenu(item: { id: string; title: string; when?(ctx: MenuContext): boolean; run(ctx: MenuContext): void | Promise<void> }): Dispose;

    // ---- data ----

    /** The accounts' quotas (Claude Code, Codex): what the composer's /usage shows. force: skip the cache. */
    usage(force?: boolean): Promise<UsageInfo[]>;
    /** Turns done and money spent per day, the last `days` (default 1), today last. g.on("stats") says when they change. */
    stats(days?: number): Promise<DayStats[]>;

    /** This add-on's own service (addon.json, see the top of this file). Without one, status() is undefined and send rejects. */
    service: {
      status(): ServiceStatus | undefined;
      /** Writes {"type":"message","data":data} to the process's stdin. Rejects when it isn't running. */
      send(data: unknown): Promise<void>;
      /** {"type":"message","data":…} the process wrote to stdout; fn gets data. */
      on(event: "message", fn: (data: any) => void): Dispose;
      /** The process started, exited, or changed its status line. */
      on(event: "status", fn: (status: ServiceStatus) => void): Dispose;
    };

    actions: {
      newTab(opts?: { workspaceId?: string; cwd?: string; agent?: string; name?: string }): Promise<void>;
      duplicateTab(tabId: string, where: "after" | "before"): Promise<void>;
      renameTab(tabId: string, name: string): void;
      renameWorkspace(workspaceId: string, name: string): void;
      focusPane(paneId: string): void;
      split(direction: "right" | "down"): Promise<void>;
      /** 给某个会话发一句话（它在跑就插进去）。 */
      send(sessionId: string, text: string): Promise<void>;
      runCommand(id: string): Promise<void>;
      /** Append text to a session's composer (what is typed there, not sent). */
      insertText(sessionId: string, text: string): void;
    };

    /** The add-on's own small storage (kept in this browser / App). */
    storage: {
      get<T = unknown>(key: string): T | undefined;
      set(key: string, value: unknown): void;
    };
  }

  type Component<P> = (props: P) => any;

  /** What JSX compiles to (set by gerdr; tsconfig.json points JSX at them). */
  const __gerdrH: any;
  const __gerdrFragment: any;

  interface GerdrReact {
    createElement(type: any, props?: any, ...children: any[]): any;
    Fragment: any;
    useState<T>(initial: T | (() => T)): [T, (v: T | ((prev: T) => T)) => void];
    useEffect(effect: () => void | (() => void), deps?: readonly unknown[]): void;
    useMemo<T>(f: () => T, deps: readonly unknown[]): T;
    useCallback<T extends (...args: any[]) => any>(f: T, deps: readonly unknown[]): T;
    useRef<T>(initial: T): { current: T };
  }

  namespace JSX {
    type Element = any;
    interface IntrinsicElements {
      [tag: string]: any;
    }
    interface ElementChildrenAttribute {
      children: {};
    }
  }

  interface ComposerContext {
    sessionId: string;
    /** What is typed in the composer now. */
    readonly text: string;
    /** Append to what is typed. */
    insert(text: string): void;
    /** Replace what is typed. */
    setText(text: string): void;
  }

  interface MenuContext {
    /** The selected text ("" when nothing is). */
    selection: string;
    /** The session of the pane that was clicked, if any. */
    sessionId?: string;
  }

  interface UsageLimit {
    kind: string;
    /** Shown as is: "5 小时", "本周", "本周 · Opus"… */
    label: string;
    /** 0–100 */
    percent: number;
    /** unix ms; 0 = unknown */
    resetsAt?: number;
    windowMins?: number;
  }

  /** One agent account's quotas. When a refresh failed, data is still the last one fetched and error says why. */
  interface UsageInfo {
    agent: string;
    name: string;
    data?: { plan?: string; limits: UsageLimit[] };
    error?: string;
    /** When data was fetched (unix ms). */
    at?: number;
  }

  interface DayStats {
    /** 2026-10-31, local time */
    date: string;
    /** Turns that ended normally (not cancelled, not failed). */
    done: number;
    /** USD by agent, as the agent reported it: only Claude Code reports money. */
    cost?: Record<string, number>;
  }

  interface ServiceStatus {
    /** exited: died, waiting to be restarted. stopped: the add-on is gone or switched off. */
    state: "starting" | "running" | "exited" | "stopped";
    pid?: number;
    /** The process's own status line ({"type":"status"}). */
    text?: string;
    /** Why it isn't running: command not found, or how it last exited plus the end of its stderr. */
    error?: string;
    restarts?: number;
    /** When it entered this state (unix ms). */
    since: number;
  }

  /** One entry of the "sessions" line a service gets. Means the same as `gerdr session list` (sessions open in the UI). */
  interface ServiceSession {
    id: string;
    agent: string;
    cwd: string;
    title: string;
    status: "idle" | "running" | "waiting" | "error" | "starting";
    /** What it waits for, one line ("回答：…", "审批：Write x.txt"). */
    waiting?: string;
    error?: string;
    /** When status last changed (unix ms): sort by it to put what just happened first. Doesn't move while a turn runs. */
    since: number;
  }

  interface SessionTitleContext {
    /** 当前标题（多个扩展时是前面的扩展给出的）。 */
    title: string;
    /** 这段上下文（最后一次 /clear 之后）用户说过的话，按顺序；斜杠命令不算。 */
    prompts: string[];
    /** prompts.length：这段上下文里用户说到第几句了。 */
    turn: number;
    /**
     * 用这个会话的 agent 的账号跑一次小问答，回模型的原话（claude 用 haiku；codex 用默认模型的低推理档，
     * 一次十几秒）。别每轮都调：花钱，标题老变也不好找。
     */
    summarize(prompt: string): Promise<string>;
  }

  interface CommandContext {
    state: StateSnapshot;
  }

  interface SessionEvent {
    session: SessionInfo;
    /** 这个会话此刻是否在屏幕上看得见（所在 pane 可见、窗口在前台）。 */
    visible: boolean;
  }

  interface SessionInfo {
    id: string;
    agent: string;
    title: string;
    cwd: string;
    status: "idle" | "running" | "waiting" | "error" | "starting";
  }

  interface PaneInfo {
    id: string;
    sessionId?: string;
    session?: SessionInfo;
    /** 终端 pane：服务端的终端 id 和开它时的目录。 */
    terminal?: { id: string; cwd: string };
  }

  interface TabInfo {
    id: string;
    /** 用户起的名字（没起过就是默认名：会话目录名 / agent 名）。 */
    name: string;
    panes: PaneInfo[];
  }

  interface WorkspaceInfo {
    id: string;
    name: string;
    tabs: TabInfo[];
  }

  interface StateSnapshot {
    workspaces: WorkspaceInfo[];
    activeWorkspace?: WorkspaceInfo;
    activeTab?: TabInfo;
    activePane?: PaneInfo;
    sessions: SessionInfo[];
  }
}
