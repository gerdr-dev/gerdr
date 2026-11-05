// 服务端对象的形状（和 internal/hub 一一对应）。

/** options：这种 agent 上次报的模式 / 配置项（后端 optCache），编辑「新会话默认」时画下拉框用；没开过会话的没有。 */
export type AgentInfo = {
  id: string;
  name: string;
  available: boolean;
  error?: string;
  base?: string;
  /** the built-in glyph the role picked (components/AgentGlyph.tsx); none = vendor logo / automatic */
  icon?: string;
  /** a PALETTE key for the icon; none = its own colours */
  color?: string;
  options?: { modes?: { availableModes: SessionMode[]; currentModeId: string }; config?: ConfigOption[] };
};

// The chat's types (event log, content blocks, requests…) live in the chat package; re-exported here for the rest of gerdr.
export * from "@gerdr/chat/types";
import type { AgentTask, AvailableCommand, ChatSession, ConfigOption, SessionMode, SessionStatus } from "@gerdr/chat/types";

export type SessionSummary = ChatSession & {
  id: string;
  agent: string;
  /** Labels (gerdr session --tag, ctx.sessions.start tags). `panel`: the panel (Work key) that started it, its owner. */
  tags?: Record<string, string>;
  /** Another computer this one drives (docs/dev/MULTIHOST.md): the session runs there, its id is "<host>:<id>". Unset = here. */
  host?: { id: string; name: string };
  /** agent 实际是哪一种（变体的底子：claude / codex） */
  base?: string;
  /** The git branch checked out where the agent works (workdir, else cwd) */
  branch?: string;
  agentSessionId: string;
  /** Where the session was started: what it is grouped by, and where a new session "like this one" starts */
  cwd: string;
  /** Where the agent works now, when it moved itself away from cwd (Claude's EnterWorktree) */
  workdir?: string;
  title?: string;
  customTitle?: boolean;
  createdAt: number;
  updatedAt: number;
  mode?: string;
  config?: Record<string, string>;
  status: SessionStatus;
  error?: string;
  /** Inline only from a host on an older Gerdr: the server now sends optsKey + modeId + optValues (useSessionOptions) */
  modes?: { availableModes: SessionMode[]; currentModeId: string };
  configOptions?: ConfigOption[];
  /** The mode / option lists, fetched by this key from /api/options/<key> (lib/optionSets.ts) */
  optsKey?: string;
  /** The current mode */
  modeId?: string;
  /** Each option's current value, by option id */
  optValues?: Record<string, string | boolean>;
  commands?: AvailableCommand[];
  /** The server sends the list as this key (fetched on demand from /api/commands/<key>, see useCommandSet), not inline */
  commandsKey?: string;
  /** commands 是借的缓存（这个会话的 agent 进程还没起，用这个 agent 上次报的；可能是旧版写的） */
  commandsCached?: boolean;
  pending: number;
  lastSeq: number;
  tasks?: AgentTask[];
  turn?: { startedAt: number; outputTokens: number }; // 在跑的这一轮
  context?: { tokens: number; window: number }; // 上下文用量
};

export type Hello = {
  version: string;
  /** When the running backend was installed (unix ms); missing from older backends. */
  installedAt?: number;
  agents: AgentInfo[];
  sessions: SessionSummary[];
  layout: any;
  config: any;
  /** Sessions finished unseen, shared by every device (server/unread.go); missing from older backends. */
  unread?: Record<string, boolean>;
  home: string;
  addonsDir: string;
  remoteConns?: RemoteConn[];
  /** Other computers this one drives (server/hosts.go); missing from older backends. */
  hosts?: HostInfo[];
};

/** A paired computer (internal/peer Info). online: the last request to it went through. */
export type HostInfo = { id: string; name: string; url: string; addedAt: number; online: boolean; error?: string };

/** 正在连着的远程界面（一条远程 /ws 一条；internal/server/remoteconns.go）。since 是 unix 毫秒。 */
export type RemoteConn = { id: number; device: string; label: string; ip: string; client: string; since: number };

/** An installed add-on (internal/addons Addon): its manifest, where it came from, and its UI half built. */
export type AddonBuild = {
  id: string;
  dir: string;
  path?: string;
  name: string;
  version?: string;
  description?: string;
  icon?: string;
  hasService?: boolean;
  /** addon.json "settings", and the values set (only those changed from the default). */
  settings?: AddonSetting[];
  settingValues?: Record<string, unknown>;
  /** addon.json "i18n": its texts in other languages, by locale. Already applied by api.ts (lib/i18n.ts localizeAddon). */
  i18n?: Record<string, AddonText>;
  disabled?: boolean;
  /** Installed from a store; modified: its files changed since (an update asks before overwriting). */
  source?: { store: string; version?: string; hash: string; at: number };
  modified?: boolean;
  code?: string;
  error?: string;
  hash: string;
  mtime: number;
  service?: AddonService;
};
/** One entry of addon.json "settings" (internal/addons SettingSpec). */
export type AddonSetting = {
  key: string;
  title?: string;
  description?: string;
  type: "number" | "string" | "text" | "boolean" | "select" | "color" | "secret" | "link" | "action" | "icon";
  default?: unknown;
  /**
   * "link": the page its button opens (the service's status "links"[key] wins). "action": a button running the add-on's
   * g.settings.onAction(key) handler (preview, test). label: the button's text.
   */
  url?: string;
  label?: string;
  min?: number;
  max?: number;
  unit?: string;
  /** select: its choices. With optionsFrom the backend works them out from what is installed (internal/addons resolve). */
  options?: { value: unknown; label?: string }[];
  optionsFrom?: { events?: string; reply?: boolean };
  /** select: never empty; the backend already falls back to the first option when nothing (valid) is picked. */
  required?: boolean;
};
/** One add-on a store offers (internal/addons StoreAddon). */
export type StoreAddon = {
  id: string;
  path: string;
  name: string;
  version?: string;
  description?: string;
  icon?: string;
  readme?: string;
  screenshots?: string[];
  hasService?: boolean;
  hash: string;
  /** addon.json "i18n", and README.<locale>.md files by locale. Already applied by api.ts. */
  i18n?: Record<string, AddonText>;
  readmes?: Record<string, string>;
};
/** addon.json's texts in one language (internal/addons ManifestText): what's left out keeps the base text. */
export type AddonText = {
  name?: string;
  description?: string;
  settings?: Record<string, { title?: string; description?: string; unit?: string; label?: string; options?: Record<string, string> }>;
};
/** A store (internal/addons StoreView): a GitHub repository, or a local folder while writing one. */
export type AddonStore = { source: string; local?: boolean; default?: boolean; fetchedAt?: number; error?: string; addons: StoreAddon[] };
/** An add-on's backend process (addon.json "service"; internal/addons/service.go). */
export type AddonService = {
  state: "starting" | "running" | "exited" | "stopped";
  pid?: number;
  text?: string;
  /** "link" settings' addresses by key ({"type":"status","links"}): pages on a port only the service knows. */
  links?: Record<string, string>;
  error?: string;
  restarts?: number;
  since: number;
};

// ---- skill / MCP（internal/ext）----
export type ExtSource = "agent" | "both" | "gerdr";
export interface MCPServer {
  name: string;
  type?: "stdio" | "http" | "sse" | "";
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  url?: string;
  headers?: Record<string, string>;
  enabled: boolean;
}
export interface ExtSettings {
  mcpSource: ExtSource;
  skillSource: ExtSource;
  mcp: MCPServer[];
  disabledSkills: string[];
}
export interface ExtSkill {
  name: string;
  description: string;
  dir: string;
  enabled: boolean;
  origin: string;
}
export interface ExtView {
  settings: ExtSettings;
  skills: ExtSkill[];
  builtin: ExtSkill[];
  agent: {
    claudeSkills: ExtSkill[];
    claudePlugins: { id: string; path: string }[];
    claudeMCP: MCPServer[];
    codexSkills: ExtSkill[];
    codexMCP: MCPServer[];
  };
}

/** 服务端的一个终端（/api/terms） */
export type TermInfo = { id: string; cwd: string; shell: string; pid: number; exited: boolean; code: number; created: number };

/** 设置 →「记忆」：一家 agent 的一个记忆目录（后端 internal/memstore）。 */
export type MemoryEntry = { file: string; path: string; name?: string; description?: string; type?: string; size: number; modTime: number };
export type MemoryStore = {
  id: string;
  agent: "native" | "claude" | "codex";
  scope: "global" | "project";
  label: string;
  dir: string;
  index: boolean;
  entries: MemoryEntry[];
  modTime: number;
};
