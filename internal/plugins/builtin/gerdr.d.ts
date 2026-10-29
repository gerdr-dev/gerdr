// gerdr 插件 API。这个文件每次启动都会被 gerdr 覆盖成当前版本 —— 别改它，改你自己的插件。
//
// 一个插件就是插件目录（~/.gerdr/plugins）里的一个 .ts 文件（或者一个带 index.ts 的子目录），
// 默认导出 definePlugin({...})。保存即生效，不用重启。文件名以 _ 开头的不加载。
//
// 插件在界面里跑（和页面同一个环境），能改的是界面上的东西：tab / pane / 工作空间的显示名、
// 加命令和快捷键、听会话事件、弹通知。

export {};

declare global {
  function definePlugin(plugin: GerdrPlugin): GerdrPlugin;

  interface GerdrPlugin {
    name?: string;
    /** 返回一个函数的话，插件被卸载（文件改了 / 删了）时会调它做清理。 */
    setup(g: Gerdr): void | (() => void);
  }

  /** 取消注册。 */
  type Dispose = () => void;

  interface Gerdr {
    readonly version: string;

    /**
     * 改 tab 在标签栏上显示的名字。多个插件按文件名顺序串起来：ctx.label 是前面的插件
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
     * 让模型起一个。插件给出过名字后，这段上下文里默认的就不再动它（/clear 之后重新算）；人改过名的会话不会调到这里。
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
    on(event: "focusChanged", fn: (e: { pane?: PaneInfo; tab?: TabInfo; workspace?: WorkspaceInfo }) => void): Dispose;

    /** 弹一条提示。system: true 时用系统通知（mac App / 浏览器通知权限）。 */
    notify(text: string, opts?: { level?: "info" | "error"; system?: boolean }): void;

    /** 当前界面状态的只读快照。 */
    state(): StateSnapshot;

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
    };

    /** 插件自己的小存储（存在这个浏览器 / App 里）。 */
    storage: {
      get<T = unknown>(key: string): T | undefined;
      set(key: string, value: unknown): void;
    };
  }

  interface SessionTitleContext {
    /** 当前标题（多个插件时是前面的插件给出的）。 */
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
