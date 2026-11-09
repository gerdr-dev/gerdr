import { DEFAULT_KEYS, IS_MAC, type KeyAction } from "../keys";
import type { ActiveConf } from "../overviewState";

export type Theme = "system" | "light" | "dark";

/**
 * A composer attachment as other devices see it: a reference, never the bytes. `blob` is an image stored on the gerdr
 * machine (POST /api/blobs → /api/blobs/<sha256>), `path` an uploaded video.
 */
export type DraftImg = { mimeType: string; blob?: string; path?: string; name?: string };
/** The composer content shared across devices (hub/draft.go): text, selection [start, end], attachments. */
export type SharedDraft = { text: string; sel?: [number, number]; imgs: DraftImg[] };

export type Config = {
  prefix: string;
  keys: Partial<Record<KeyAction, string[]>>;
  theme: Theme;
  /** 旧版存这儿、所有设备共用；现在字号存各设备本机（State.localFont），这个只当没设过时的初值 */
  fontSize: number;
  defaultAgent: string;
  /** 新会话的默认模式（按 agent id，角色也算）。后端建会话时套（cmd/gerdr 的 cfgDefaults），`gerdr session new` 也吃。 */
  defaultModes: Record<string, string>;
  /** 新会话的默认配置项（按 agent id → 配置项 id → 值，比如 model / effort）。Gerdr Agent 的默认模型不在这儿，在 llm.defaultModel。 */
  defaultConfig?: Record<string, Record<string, string>>;
  notifications: boolean;
  /** 发送：enter（回车发、Shift+回车换行）或 mod+enter（⌘/Ctrl+回车发）。 */
  sendKey: "enter" | "mod+enter";
  /** Per project (layout space id): the Work view's current agent conversation (the chat on its right), shared by all devices. */
  workSessions?: Record<string, string>;
  /** Before projects: the default project's Work conversation. Read as a fallback (lib/work.ts). */
  workSession?: string;
  /** Per project: which agent (engine or role from Settings → Agent) new Work conversations use; missing = workAgent, then defaultAgent. */
  workAgents?: Record<string, string>;
  workAgent?: string;
  keepTabs: number; // 切走之后还留在页面里的 tab 数（展开状态、滚动位置都保住）；1 = 不留
  /** 改文件 / 新建文件的工具调用默认展开 diff。关掉后只剩一行（文件名、+/- 行数），点开再看。 */
  expandEdits?: boolean;
  /** 显示思考：思考过程不收进工具调用那一组，单独成行、默认展开着。关掉就和工具调用折在一起，一行「思考」点开再看。没写 = 开。 */
  showThinking?: boolean;
  /** 点对话里的外部链接先弹确认框（streamdown 的 linkSafety，它默认开着）。关掉就直接打开。没写 = 开（老行为）。 */
  confirmLinks?: boolean;
  /** 实验室：对话用虚拟滚动（只渲染屏幕附近的行）。默认关 —— 往上翻时行卸载再挂载，带图的行高度先 0 后撑开，界面一跳一跳（用户报的）。 */
  virtualList?: boolean;
  /** Labs: following the bottom stops at the start of a reply text that grows taller than the screen, so it can be read
   *  from its start (ChatList's readCap). Off by default. */
  readFromTop?: boolean;
  /** 实验室：有远程设备连着时不让电脑睡（后端起 caffeinate），断开 15 分钟后放手；开着时合上盖子也不睡
   *  （pmset disablesleep，要授权一次，手动时段也算）。后端读这个字段（cmd/gerdr 的 applyAwake）。 */
  keepAwakeRemote?: boolean;
  /** Settings → Remote access: see this computer's screen and click / type on it from another device (components/RemoteScreen.tsx,
   *  internal/screen). Read by the backend at each request (server/screenapi.go). */
  remoteScreen?: boolean;
  /** 实验室：选中即复制（和 herdr 的 ui.copy_on_select 一样），lib/copysel.ts。 */
  copyOnSelect?: boolean;
  /** Labs → Diagnosis: which install of claude / codex to run, when PATH finds a stale or blocked one.
   *  Read by the backend (cmd/gerdr's applyBins). */
  agentBins?: Record<string, string>;
  agents?: AgentDef[];
  /** Per project (layout space id): skills / MCP servers its sessions don't get (ProjectExtDialog). Read by the backend
   *  (cmd/gerdr: ext.Manager.ForProject), whatever the agent's own settings. */
  projects?: Record<string, ProjectConf>;
  /** 输入框里 pin 住的 / 命令（命令名，不带 /）：附图按钮旁边多一个 / 按钮，点开就是这些。存 config 是让手机和电脑共用一份。 */
  pinnedCommands?: string[];
  /** Inbox: put an item in when the gerdr machine runs on battery at or below `percent`. Missing = on at 20%. */
  batteryAlert?: { on: boolean; percent: number };
  /** Inbox: which kinds of item go in (Settings → General → Inbox). Missing field = on, the old behaviour. */
  inbox?: { permission?: boolean; ask?: boolean; error?: boolean };
  /** Gerdr Agent（native）用的模型服务商，后端读（cmd/gerdr 的 cfgLLM），设置 → Agent 里编辑。 */
  llm?: LLMConfig;
  /** What draws the working indicator (Settings → General → Loading 动画; components/AddonSlot.tsx AddonLoader). Missing = the first
   *  "loader" add-on, else gerdr's dots. */
  loader?: LoaderChoice;
  /** Who is in the sidebar's 进行中 / the overview grid, the same on every device (overviewState.ts ActiveConf; lib/overview.ts
   *  writes it). Missing = not yet seeded from a device's old localStorage copy. */
  active?: ActiveConf;
};

/**
 * gerdr's own dots, one of its other built-in indicators (components/Loaders.tsx id), an add-on's "loader" entry, or a
 * sprite (g.slot("sprite")), the last two by slot key "addon:id".
 */
export type LoaderChoice = "dots" | `builtin:${string}` | `loader:${string}` | `sprite:${string}`;

/** config.json 的 llm：Gerdr Agent 直接调的模型接口（OpenAI 兼容或 Anthropic）。模型只写 id 时协议、思考档位按 id 猜（后端 native/config.go）。 */
export type LLMModel = { id: string; name?: string; api?: "chat" | "responses"; efforts?: string[]; window?: number };
/** api: the provider's protocol. Unset = OpenAI-compatible (each model picks Chat Completions / Responses); "anthropic" = Anthropic Messages. */
export type LLMProvider = { id: string; name?: string; api?: "anthropic"; baseUrl: string; apiKey?: string; apiKeyEnv?: string; models: LLMModel[] };
export type LLMConfig = { providers?: LLMProvider[]; defaultModel?: string };

/** What a project switches off, by name. */
export type ProjectConf = { disabledSkills?: string[]; disabledMcp?: string[] };

/** config.json 里的 agents：agent = 一个引擎（base = claude / codex / native，或者外部 ACP agent 的 command）+ 自己的配置。
 *  prompt 是追加的系统提示；skills / mcp 是只给的那几个（按名字），不写 = 照全局设置，空数组 = 一个都不给（后端 hub.AgentDef）。 */
export type AgentDef = {
  id: string;
  name: string;
  base?: "claude" | "codex" | "native";
  command?: string[];
  bin?: string;
  args?: string[];
  env?: Record<string, string>;
  prompt?: string;
  skills?: string[];
  mcp?: string[];
  /** 引擎是 native 时只用这几家模型服务商（llm.providers 的 id）；不写 = 全部 */
  providers?: string[];
  /** a built-in glyph (components/AgentGlyph.tsx); not set = vendor logo for claude / codex, an automatic glyph otherwise */
  icon?: string;
  /** a PALETTE key for the icon (components/AgentGlyph.tsx); not set = its own colours */
  color?: string;
};

export const DEFAULT_CONFIG: Config = {
  prefix: "ctrl+b",
  keys: {},
  theme: "system",
  fontSize: 14,
  defaultAgent: "claude",
  defaultModes: {},
  notifications: true,
  sendKey: "enter",
  keepTabs: 6,
  expandEdits: true,
  showThinking: true,
  virtualList: false,
  readFromTop: false,
  keepAwakeRemote: false,
  copyOnSelect: false,
};

export function mergeConfig(raw: any): Config {
  const c = { ...DEFAULT_CONFIG, ...(raw && typeof raw === "object" ? raw : {}) };
  c.keys = { ...(raw?.keys ?? {}) };
  c.defaultModes = { ...(raw?.defaultModes ?? {}) };
  return c;
}

export function keyFor(c: Config, a: KeyAction): string[] {
  const custom = c.keys[a];
  if (!custom) return DEFAULT_KEYS[a];
  if (IS_MAC || !custom.length) return custom; // [] = deliberately unbound
  // Custom keys are one set for every device (config.json): ⌘ ones set on a mac are the Windows key elsewhere and never reach
  // the page (see PC_EXTRA in keys.ts), so skip them there, and fall back to this platform's defaults if nothing is left
  const usable = custom.filter((k) => !/(^|\+)meta\+/.test(k));
  return usable.length ? usable : DEFAULT_KEYS[a];
}
