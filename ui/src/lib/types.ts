// 服务端对象的形状（和 internal/hub 一一对应）。

/** options：这种 agent 上次报的模式 / 配置项（后端 optCache），编辑「新会话默认」时画下拉框用；没开过会话的没有。 */
export type AgentInfo = {
  id: string;
  name: string;
  available: boolean;
  error?: string;
  base?: string;
  options?: { modes?: { availableModes: SessionMode[]; currentModeId: string }; config?: ConfigOption[] };
};

// The chat's types (event log, content blocks, requests…) live in the chat package; re-exported here for the rest of gerdr.
export * from "@gerdr/chat/types";
import type { AgentTask, AvailableCommand, ChatSession, ConfigOption, SessionMode, SessionStatus } from "@gerdr/chat/types";

export type SessionSummary = ChatSession & {
  id: string;
  agent: string;
  /** agent 实际是哪一种（变体的底子：claude / codex） */
  base?: string;
  /** cwd 所在 git 仓库的当前分支 */
  branch?: string;
  agentSessionId: string;
  cwd: string;
  title?: string;
  customTitle?: boolean;
  createdAt: number;
  updatedAt: number;
  mode?: string;
  config?: Record<string, string>;
  status: SessionStatus;
  error?: string;
  modes?: { availableModes: SessionMode[]; currentModeId: string };
  configOptions?: ConfigOption[];
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
  home: string;
  addonsDir: string;
  remoteConns?: RemoteConn[];
};

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
  type: "number" | "string" | "boolean" | "select";
  default?: unknown;
  min?: number;
  max?: number;
  unit?: string;
  options?: { value: unknown; label?: string }[];
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
};
/** A store (internal/addons StoreView): a GitHub repository, or a local folder while writing one. */
export type AddonStore = { source: string; local?: boolean; default?: boolean; fetchedAt?: number; error?: string; addons: StoreAddon[] };
/** An add-on's backend process (addon.json "service"; internal/addons/service.go). */
export type AddonService = {
  state: "starting" | "running" | "exited" | "stopped";
  pid?: number;
  text?: string;
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
