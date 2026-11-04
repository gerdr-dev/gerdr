// gerdr add-on API. gerdr overwrites this file with the current version on every start: don't edit it, edit your
// add-on.
//
// An add-on is a folder in the add-on directory (~/.gerdr/addons/<id>/):
//
//   addon.json   optional: {"name": "…", "version": "1.0.0", "description": "…", "icon": "icon.png",
//                           "screenshots": ["shot.png"], "main": "index.ts", "service": {…}, "settings": [AddonSetting…]}
//                "settings" become a form in Settings → Add-ons; read them with g.settings.get(key):
//                  {"key": "ttl", "title": "缓存时长", "type": "number", "default": 60, "min": 1, "max": 1440, "unit": "分钟"}
//                type: number / string / text (several lines: a list, a table) / boolean / select (with "options": [{"value": 5, "label": "5 分钟"}],
//                or "optionsFrom": {"events": "*/dm", "reply": true} — the event sources installed add-ons declare,
//                value = the source; "required": true = never empty, the first option until one is picked) /
//                color (a "#rrggbb" string, picked from a palette or typed) / secret (a string the form masks:
//                tokens, keys; g.settings.get returns it in the clear) /
//                link (no value: a button opening a page, {"key": "panel", "title": "控制面板", "type": "link",
//                "url": "https://…", "label": "打开"}; a service can give the url at run time, see "links" below)
//                action (no value: a button running your g.settings.onAction(key) handler, to try the values above
//                without leaving the form: {"key": "preview", "title": "试听", "type": "action", "label": "播放"})
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
//     {"type":"hello","version":"…","addon":"band","lang":"zh"}   first line of every start; lang (zh / en) is the
//                                                         computer's language, the one Gerdr's own texts are in
//     {"type":"sessions","sessions":[ServiceSession…]}   the sessions open in the UI: once after hello, then only when
//                                                         a field changes (a turn streaming tokens is not a change)
//     {"type":"message","data":…}                        sent by the UI half, g.service.send(data)
//     {"type":"pending","pending":[ServicePending…]}     only with "service": {…, "pending": true}: what agents wait
//                                                         on a person for (approvals, questions); after the greeting,
//                                                         then whenever it changes. Answered elsewhere = gone from it
//     {"type":"answered","reqId":"…","error":"…"}        how an "answer" went (no error: taken, the agent goes on)
//   process → gerdr (stdout)
//     {"type":"status","text":"band connected"}          shown under the add-on in Settings → Add-ons
//     {"type":"status","text":"…","links":{"panel":"http://127.0.0.1:7858"}}   the addresses of the add-on's "link"
//                                                         settings, by key: pages the process serves on a port only it
//                                                         knows. A status line without "links" keeps the last ones
//     {"type":"message","data":…}                        to g.service.on("message") in every open window
//     {"type":"answer","session":"…","reqId":"…","optionId":"…"}               approve / reject (an option's id)
//     {"type":"answer","session":"…","reqId":"…","answers":{"<question id>":["label"]}}   answer the questions
//     {"type":"answer","session":"…","reqId":"…","skip":true}                  don't answer a question
//                                                         only with "pending": true — that's the person's power to
//                                                         approve commands, given when they install the add-on
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
    /**
     * A turn ended, any session, as the backend logged it (the same record g.turns returns). Unlike turnEnd it carries
     * the turn's facts (cost, tokens, whether the agent started it itself) and comes once per turn per window.
     */
    on(event: "turn", fn: (turn: TurnRecord) => void): Dispose;
    /** A new event (g.events), from any source, once per window. */
    on(event: "event", fn: (e: GerdrEvent) => void): Dispose;
    on(event: "focusChanged", fn: (e: { pane?: PaneInfo; tab?: TabInfo; workspace?: WorkspaceInfo }) => void): Dispose;

    /** 弹一条提示。system: true 时用系统通知（mac App / 浏览器通知权限）。 */
    notify(text: string, opts?: { level?: "info" | "error"; system?: boolean }): void;

    /**
     * The interface's language: "zh" or "en". Fixed while the add-on runs (changing it reloads the page, and the
     * add-on with it), so read it in setup and pick your texts. addon.json's own texts go in its "i18n" field instead.
     */
    locale(): "zh" | "en";

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
      /**
       * A centered modal (Radix Dialog). Render it from the "overlay" slot and open it from a command.
       * showCloseButton={false} drops the ✕. For a drawer use Sheet below.
       */
      Dialog: Component<{ open?: boolean; onOpenChange?: (open: boolean) => void; children?: any }>;
      DialogContent: Component<{ className?: string; showCloseButton?: boolean; children?: any }>;
      DialogHeader: Component<{ className?: string; children?: any }>;
      DialogTitle: Component<{ className?: string; children?: any }>;
      DialogDescription: Component<{ className?: string; children?: any }>;
      DialogFooter: Component<{ className?: string; children?: any }>;
      /** Closes the dialog it is in; asChild to make your own button do it. */
      DialogClose: Component<{ asChild?: boolean; children?: any }>;
      /**
       * A drawer (shadcn Sheet: a Radix Dialog sliding in from an edge), used like Dialog. SheetContent defaults to
       * side="right", 3/4 of the width up to sm:max-w-sm; widen with className="sm:max-w-md". It has no padding of its
       * own besides SheetHeader / SheetFooter (p-4).
       */
      Sheet: Component<{ open?: boolean; onOpenChange?: (open: boolean) => void; children?: any }>;
      SheetContent: Component<{ side?: "top" | "right" | "bottom" | "left"; className?: string; showCloseButton?: boolean; children?: any }>;
      SheetHeader: Component<{ className?: string; children?: any }>;
      SheetTitle: Component<{ className?: string; children?: any }>;
      SheetDescription: Component<{ className?: string; children?: any }>;
      SheetFooter: Component<{ className?: string; children?: any }>;
      SheetClose: Component<{ asChild?: boolean; children?: any }>;
      /** Joins class names, later Tailwind classes winning (clsx + tailwind-merge). */
      cn(...classes: any[]): string;
    };
    /** Every lucide-react icon: <g.icons.Timer className="size-3.5" />. */
    readonly icons: Record<string, Component<{ className?: string }>>;

    /**
     * Put a component in one of gerdr's slots. It is re-rendered with gerdr; one that throws is dropped and its error
     * shown in Settings → Add-ons. Several add-ons in one slot: by order, then by add-on id.
     *   "panel"      a block of your add-on's panel (288px wide, no props). Each add-on with blocks gets one button
     *                on the rail (the strip down the window's left edge), in order (the smallest of your blocks');
     *                hovering or tapping it opens your blocks, stacked, beside the rail; on a phone a sheet from the
     *                drawer's bottom row. On the rail a block stays mounted while its panel is closed. Blocks are
     *                padded above and below once per panel: give yours horizontal padding only.
     *                The button is yours, from the first block that sets each option: opts.title (its tooltip;
     *                default your add-on's name), opts.icon — a lucide icon name ("Gauge"), or a component drawn in a
     *                box of { width, height } px: 32×20 on the rail, 28×16 on a phone, clipped. Use it to show live
     *                data (three quota rings); keep it still and use the theme tokens. Default: your addon.json icon.
     *                opts.tallIcon: true for a button two cells tall on the rail, its box 32×72 (24×24 on a phone),
     *                when your marks need the room.
     *                opts.open: "hover" (default) or "click", for a panel you work in rather than glance at.
     *                Gerdr doesn't put several add-ons behind one button: to show other add-ons' blocks together
     *                with yours, copy their code into your add-on (they're small) and turn them off
     *   "paneTitle"  every pane's title bar, before its ⋯ menu (props: { pane: PaneInfo; tab: TabInfo }); keep it small
     *   "overlay"    mounted once at the app's root and always there (no props, takes no space): where a dialog or
     *                drawer opened by a command lives. A panel can't host one: it's hidden while closed and on a
     *                phone it's only there while the drawer is open. Keep the open state outside the component (a
     *                variable plus a listener) so a command's run() can flip it.
     *   "loader"     replaces the working indicator: the 3×3 dots before a running turn's "Pondering…" line (a row
     *                20px tall), and the one shown while a pane starts an agent. Props: paused (the turn waits on the
     *                user — a permission, a question — so show it's waiting, not working) and className (gerdr's size
     *                and colour for that spot: text-brand running, text-warn waiting; pass it on to keep them). With
     *                no choice made it draws as soon as you're installed (the first registration does); the user can
     *                pick another in Settings → General → Loading 动画. One that throws falls back to the dots.
     *                fallback: gerdr's own dots, if you want to show them sometimes. Just a picture that moves? Make it a sprite instead:
     *                it then also serves as an agent's icon, and only shows where the user puts it
     *   "sprite"     a small picture — an animal, a symbol, a few tiny bars, a number — that gerdr draws nowhere by
     *                itself: the user picks sprites for the places that take one: the working indicator (Settings →
     *                General → Loading 动画) and an agent's icon (Settings → Agent). So a picture set changes nothing
     *                when installed, and one set serves every such place. Register each picture separately (opts.id,
     *                opts.title: "Pirate") so the user can put a different one in each place.
     *                Props: state — "working" / "waiting" (the indicator: running, or waiting on the user) or "idle"
     *                (an agent's icon: always in sight, so keep it still) — and width × height, the box you get, to
     *                scale to: 24×16 in the indicator (not clipped, a hat may stick out), down to 14×14 as an agent's
     *                icon in a list (clipped). Fitting the box is yours: shrink, or draw a simpler version when it's
     *                small. The box carries gerdr's colour for the spot (currentColor: text-brand / text-warn in the
     *                indicator); else use the theme tokens so it reads in light and dark. Don't make it interactive
     *   "settings"   your add-on's settings, laid out by you (Settings → Add-ons → 设置), instead of the generated
     *                form that lists every addon.json setting in order: show a provider's fields only once it is
     *                picked, group them, add a line of help. Keep declaring them in addon.json (storage, defaults,
     *                secrets, translations, your service's settings stay gerdr's) and draw each with <Field name="…" />,
     *                the generated form's own row (title, input, description, reset), so add-ons look alike; around
     *                them draw what you like. values: every setting's value (the default until changed), re-rendered
     *                on change; set(key, value) / set({…}) saves (null: back to the default). One per add-on. While
     *                your add-on is off or this throws, gerdr shows the generated form instead
     */
    slot(name: "loader", component: Component<{ paused: boolean; className?: string; fallback?: any }>, opts?: { order?: number }): Dispose;
    slot(name: "panel", component: Component<{}>, opts?: PanelOpts): Dispose;
    /** The "panel" slot's old name (blocks used to sit in the left column); still works the same. */
    slot(name: "sidebar", component: Component<{}>, opts?: PanelOpts): Dispose;
    slot(name: "sprite", component: Component<SpriteProps>, opts?: { order?: number; id?: string; title?: string }): Dispose;
    slot(name: "overlay", component: Component<{}>, opts?: { order?: number }): Dispose;
    slot(name: "paneTitle", component: Component<{ pane: PaneInfo; tab: TabInfo }>, opts?: { order?: number }): Dispose;
    slot(name: "settings", component: Component<SettingsSlotProps>): Dispose;

    /**
     * Decorate the line under each finished turn ("18:36 · 用时 59s · 4.1k tokens"): a class for the time (color it:
     * "text-ok" / "text-warn" / "text-bad"), its tooltip, and something drawn right after it. Called whenever the line
     * renders, and every 30s while any add-on uses this, so an answer that depends on info.now stays current.
     */
    turnStat(fn: (info: TurnStatInfo) => { className?: string; title?: string; after?: any } | void): Dispose;

    /**
     * Recolor gerdr: override its color tokens, per mode. A value is any CSS color, including relative ones
     * ("oklch(from #e5569b calc(l + 0.1) c h)"); a token left out (or "") keeps gerdr's own. Several add-ons: by
     * add-on id, the later one winning per token. Undone when disposed or the add-on is unloaded.
     * Pass a function to depend on settings: it is called again whenever the add-ons reload, which includes a change
     * in Settings → Add-ons.
     *   g.theme(() => { const c = g.settings.get<string>("color"); return { light: { brand: c }, dark: { brand: c } }; })
     */
    theme(spec: ThemeSpec | (() => ThemeSpec | void)): Dispose;

    /** A button in the composer's bottom bar (next to the model menu). icon: an element, e.g. <g.icons.Zap />. */
    composerAction(action: { id: string; title: string; icon?: any; run(ctx: ComposerContext): void | Promise<void> }): Dispose;

    /**
     * An item in the right-click menu (the desktop App and the Android App; a browser keeps its own menu). Shown when
     * when(ctx) is true; without when, only while text is selected.
     */
    contextMenu(item: { id: string; title: string; when?(ctx: MenuContext): boolean; run(ctx: MenuContext): void | Promise<void> }): Dispose;

    /**
     * A button on each message: beside a user's bubble (on hover; always on a phone) and at the end of a reply's
     * "18:36 · 用时 59s" line. Read it aloud, translate it, send it somewhere: what run() does is yours. One action shows
     * as its icon; with several (yours and other add-ons') they sit behind a "⋯".
     * Gerdr runs it: a spinner while run()'s promise is pending; a second tap aborts ctx.signal (stop what you're doing)
     * and frees the button; a throw is shown as an error. When run() plays sound with g.audio.play(…, { signal:
     * ctx.signal }) the button shows "stop" while it plays. when(msg): which messages get it (default: any with text).
     * icon: a lucide name ("Languages") or an element.
     *   g.messageAction({ id: "read", title: "朗读", icon: "Volume2",
     *     run: (msg, ctx) => g.audio.play(speak(msg.text), { signal: ctx.signal }) });
     */
    messageAction(action: {
      id: string;
      title: string;
      icon?: string | any;
      order?: number;
      when?(msg: MessageInfo): boolean;
      run(msg: MessageInfo, ctx: { signal: AbortSignal; locale: "zh" | "en" }): void | Promise<unknown>;
    }): Dispose;

    /**
     * Gerdr's one sound player, shared by every add-on: a new play() ends the one before (that one resolves false), so
     * sounds never overlap. Give it a clip, or clips in order — an (async) iterable, e.g. an async generator yielding a
     * clip per sentence: the next is asked for as soon as one starts playing, so a long text starts at once and has no
     * gaps. Resolves true when it played to the end, false when stopped (signal, stop(), another play()); rejects when a
     * clip can't be played. Browsers only play sound started by a tap: from a message action's run() it works (Gerdr
     * prepares the player during the tap); started by nothing the user did, it may be refused.
     */
    audio: {
      play(clips: AudioClip | AsyncIterable<AudioClip> | Iterable<AudioClip>, opts?: { signal?: AbortSignal }): Promise<boolean>;
      stop(): void;
    };

    // ---- data ----

    /**
     * An HTTP request made by gerdr's backend, not the page, so the API needs no CORS headers (a plain fetch() from
     * an add-on is cross-origin). Text in, text out: a body that isn't a string is sent as JSON (Content-Type set
     * unless you set it). Like fetch(), an error status resolves (check ok); it rejects only when the server can't be
     * reached, the URL isn't http(s), or 30s pass. Responses over 10 MB are refused.
     * binary: true for an answer that isn't text (audio, images): read it with bytes() / blob(); text is then "".
     */
    fetch(url: string, init?: { method?: string; headers?: Record<string, string>; body?: unknown; binary?: boolean }): Promise<FetchResult>;
    /** The accounts' quotas (Claude Code, Codex): what the composer's /usage shows. force: skip the cache. */
    usage(force?: boolean): Promise<UsageInfo[]>;
    /**
     * The turns that ended in [since, until) (unix ms; until defaults to now), oldest first, every session and agent.
     * Gerdr logs each turn once, window or not; counting is yours: what is "a task", which day it falls on, what to add
     * up. Today's: g.turns({ since: new Date().setHours(0, 0, 0, 0) }). g.on("turn") brings each new one.
     */
    turns(range: { since: number; until?: number }): Promise<TurnRecord[]>;

    /**
     * Events: things that happened outside a conversation (a review comment, a failed pipeline, a chat message), one
     * record shape for every source (Gerdr's docs/dev/EVENTS.md). emit one your add-on found (source defaults to
     * your add-on's id; give a stable id so finding it again doesn't emit it twice); list recent ones; g.on("event")
     * brings each new one. Panels subscribe with triggers/*.json, sessions with `gerdr event watch`, a service with
     * addon.json service.events (they come on its stdin as {"type":"event","event":…}; it emits by printing the same).
     */
    events: {
      emit(e: EventInput): Promise<{ event: GerdrEvent; fresh: boolean }>;
      /** Oldest first. since: unix ms (default: the last 24 hours); on: "source/kind" with * globs; limit: the newest n (default 100). */
      list(q?: { since?: number; on?: string; key?: string; limit?: number }): Promise<GerdrEvent[]>;
    };

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

    /**
     * The values of addon.json "settings" as set in Settings → Add-ons (the default until changed). Stored by gerdr,
     * the same in every window and device. Read it when you need it rather than once in setup: a change made in
     * Settings shows up here without reloading the add-on.
     */
    settings: {
      get<T = unknown>(key: string): T;
      /** Save a setting (or several: set({ a: 1, b: 2 })) as the form would; null goes back to the default. */
      set(key: string, value: unknown): Promise<void>;
      set(values: Record<string, unknown>): Promise<void>;
      /**
       * What an "action" setting's button runs (Settings → Add-ons): try the values the user just set — preview a
       * voice, test a connection. While the promise is pending the button spins, or reads "Stop" once sound you play
       * with g.audio.play(…, { signal: ctx.signal }) starts; tapping it then aborts ctx.signal. A throw shows as an
       * error toast. Sound plays from here: the tap allows it.
       */
      onAction(key: string, run: (ctx: { signal: AbortSignal }) => void | Promise<unknown>): Dispose;
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
    /** What every element takes besides its own props: key, when drawing a list. */
    interface IntrinsicAttributes {
      key?: string | number;
    }
  }

  /** g.slot("panel") options; title / icon / open describe your rail button, the first block that sets one wins. */
  interface PanelOpts {
    order?: number;
    /** A stable id among your blocks; default your n-th. */
    id?: string;
    title?: string;
    /** A lucide icon name, or a component drawn in a box of width × height px (32×20 on the rail, 28×16 on a phone). */
    icon?: string | Component<{ width: number; height: number }>;
    /** A button two cells tall on the rail: the icon component gets 32×72 there, 24×24 on a phone. */
    tallIcon?: boolean;
    /** Open the panel on hover (default) or on click. */
    open?: "hover" | "click";
  }

  /** g.slot("settings"): your own layout of the settings declared in addon.json. */
  interface SettingsSlotProps {
    /** One declared setting, drawn as the generated form draws it: <Field name="apiKey" />. */
    Field: Component<{ name: string }>;
    /** Every declared setting's current value (its default until changed). */
    values: Record<string, unknown>;
    /** Save one setting, or several at once; null goes back to the default. */
    set(key: string, value: unknown): Promise<void>;
    set(values: Record<string, unknown>): Promise<void>;
  }

  /** g.slot("sprite"): where it's drawn now, and the box it gets there. */
  interface SpriteProps {
    state: "idle" | "working" | "waiting";
    width: number;
    height: number;
  }

  interface TurnStatInfo {
    sessionId: string;
    /** claude / codex / … */
    agent: string;
    /** When the turn ended (unix ms); missing on very old turns. */
    ts?: number;
    /** How long it ran (ms). */
    ms: number;
    /** Output tokens. */
    tokens: number;
    /** The newest finished turn of the session (the one the next message continues from). */
    latest: boolean;
    /** The current time (unix ms), refreshed every 30s. */
    now: number;
  }

  interface ThemeSpec {
    light?: ThemeVars;
    dark?: ThemeVars;
  }

  /**
   * gerdr's color tokens (the custom properties of the UI's src/index.css, without "--"). The Tailwind classes follow
   * them: brand → bg-brand / text-brand, muted-foreground → text-muted-foreground…
   */
  type ThemeToken =
    | "brand"
    | "ok"
    | "warn"
    | "bad"
    | "background"
    | "foreground"
    | "card"
    | "card-foreground"
    | "popover"
    | "popover-foreground"
    | "primary"
    | "primary-foreground"
    | "secondary"
    | "secondary-foreground"
    | "muted"
    | "muted-foreground"
    | "accent"
    | "accent-foreground"
    | "destructive"
    | "border"
    | "input"
    | "ring"
    | "sidebar";
  type ThemeVars = Partial<Record<ThemeToken, string | undefined>>;

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

  /** A message, as g.messageAction's when() / run() get it. */
  interface MessageInfo {
    sessionId: string;
    /** The person's message or the agent's reply (a reply is one turn's text: tool calls and thinking left out). */
    role: "user" | "assistant";
    /** Stable for the message. */
    id: string;
    /** As written (Markdown for a reply). */
    readonly markdown: string;
    /** In plain words: the Markdown marks gone, code blocks as "(code)", links as their label. What to read aloud. */
    readonly text: string;
  }

  /** A clip for g.audio.play: bytes or a Blob of audio (mp3, wav, ogg…), or a URL to it (https:, blob:). */
  type AudioClip = Blob | ArrayBuffer | ArrayBufferView | string;

  interface FetchResult {
    ok: boolean;
    status: number;
    statusText: string;
    /** Lower-cased names. */
    headers: Record<string, string>;
    text: string;
    /** JSON.parse(text); throws when it isn't JSON. */
    json<T = any>(): T;
    /** The body as bytes (fetch with binary: true for anything that isn't text). */
    bytes(): Uint8Array;
    /** The body as a Blob typed by its Content-Type: an audio answer goes straight to g.audio.play. */
    blob(): Blob;
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

  /** One finished turn (g.turns, g.on("turn")). */
  /** What g.events.emit takes. */
  interface EventInput {
    /** Stable id for this happening ("gitlab:note:123"): one already seen is dropped. Leave out to always emit. */
    id?: string;
    /** Who it comes from (default: your add-on's id). [A-Za-z0-9._-], no "/". */
    source?: string;
    /** What happened, dotted: "message", "mr.note", "pipeline.failed". */
    kind: string;
    /** What it is about (a channel, group/repo!34): what subscribers narrow by. */
    key?: string;
    /** Who did it, in your source's own identities (a member id, a username): subscribers decide who may ask by it. */
    actor?: string;
    title?: string;
    text?: string;
    url?: string;
    /** Anything else, for code. */
    data?: unknown;
    /**
     * Where an answer goes, when one can: the event kind your service listens for (in service.events) and what it
     * needs besides the text. A subscriber answers with ctx.events.reply(event, text) — your service then gets
     * {kind: reply.kind, data: {...reply.data, text}} — without knowing it's you.
     */
    reply?: { kind: string; data?: unknown };
  }
  interface GerdrEvent extends EventInput {
    id: string;
    source: string;
    /** unix ms */
    at: number;
    /** How many triggers deep it was emitted (a trigger's function emitting another event). */
    depth?: number;
  }

  interface TurnRecord {
    /** When it ended (unix ms). */
    ts: number;
    sessionId: string;
    /** claude / codex / … (a variant like claude-2 as is) */
    agent: string;
    cwd?: string;
    /** end_turn (finished normally) · cancelled · error · max_tokens · refusal … */
    stopReason: string;
    /** The agent started it itself (a background task finished and it reported back): no message from a person. */
    auto?: boolean;
    durationMs?: number;
    outputTokens?: number;
    /** USD, as the agent reported it for this turn. Only Claude Code reports money (its estimate at API prices). */
    costUsd?: number;
    /**
     * What the turn's model calls used, one entry per model. Gerdr Agent only (Claude Code reports costUsd, Codex
     * neither). Tokens, not money: price them yourself.
     */
    tokens?: TurnTokens[];
  }

  /** One model's tokens in a turn (TurnRecord.tokens). */
  interface TurnTokens {
    /** The agent's model key; Gerdr Agent: "provider/model", e.g. "anthropic/claude-opus-4-5". */
    model: string;
    /** Every prompt token, the cached ones included: uncached input = input - cacheRead - cacheWrite. */
    input?: number;
    output?: number;
    cacheRead?: number;
    /** Prompt tokens written to the cache (Anthropic bills them above plain input). */
    cacheWrite?: number;
  }

  interface ServiceStatus {
    /** exited: died, waiting to be restarted. stopped: the add-on is gone or switched off. */
    state: "starting" | "running" | "exited" | "stopped";
    pid?: number;
    /** The process's own status line ({"type":"status"}). */
    text?: string;
    /** The "link" settings' addresses the process reported ({"type":"status","links"}), by setting key. */
    links?: Record<string, string>;
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

  /** One entry of the "pending" line ("service": {"pending": true}): a request waiting on a person. */
  interface ServicePending {
    session: string;
    /** The session's name. */
    title: string;
    agent: string;
    cwd: string;
    /** Answer with this. */
    reqId: string;
    kind: "permission" | "ask";
    /** One line, in the computer's language ("审批：Bash: rm -rf build", "回答：Which database? (pg / mysql)"). */
    summary: string;
    /** When it was asked (unix ms). */
    at: number;
    /** In a do-not-disturb group: Gerdr doesn't notify about it, and an add-on that notifies shouldn't either. */
    muted?: boolean;
    /** permission: what the agent wants to do. input is the tool's arguments as the agent gave them (a command, a path). */
    tool?: { title: string; kind?: string; input?: unknown };
    /** permission: the choices; answer with one's id. kind: allow_once / allow_always / reject_once / reject_always. */
    options?: { id: string; name: string; kind: string }[];
    /**
     * ask: the questions; answer every one by id, with option labels (several only where multiSelect), or your own
     * text where allowOther. secret: the answer is something like a password.
     */
    questions?: {
      id: string;
      header?: string;
      question: string;
      options?: { label: string; description?: string }[];
      multiSelect?: boolean;
      allowOther?: boolean;
      secret?: boolean;
    }[];
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
