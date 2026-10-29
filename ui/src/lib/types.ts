// 服务端对象的形状（和 internal/hub 一一对应）。

export type AgentInfo = { id: string; name: string; available: boolean; error?: string; base?: string };

export type SessionMode = { id: string; name: string; description?: string };
export type ConfigSelectOption = { value: string; name: string; description?: string };
export type ConfigOption = {
  type: "select";
  id: string;
  name: string;
  category?: string;
  currentValue: string;
  options: ConfigSelectOption[] | { group: string; name: string; options: ConfigSelectOption[] }[];
};
// _meta.skill：claude 适配器标的（agents/claude 的 acpCommands）—— 是 skill，pin 列表里点了只填进输入框不直接发
export type AvailableCommand = { name: string; description: string; input?: { hint?: string }; _meta?: { skill?: boolean } };

export type SessionStatus = "idle" | "running" | "waiting" | "error" | "starting";

export type SessionSummary = {
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
  /** commands 是借的缓存（这个会话的 agent 进程还没起，用这个 agent 上次报的；可能是旧版写的） */
  commandsCached?: boolean;
  pending: number;
  lastSeq: number;
  tasks?: AgentTask[];
  turn?: { startedAt: number; outputTokens: number }; // 在跑的这一轮
  context?: { tokens: number; window: number }; // 上下文用量
};

/** 后台任务 / 子 agent（acpx.Task）。 */
export type AgentTask = {
  id: string;
  type: string; // local_bash | local_agent | …
  description: string;
  subagent?: string;
  toolCallId?: string;
  parent?: string; // 子 agent 起的任务：那个子 agent 的 toolCallId
  background: boolean;
  status: string; // running | completed | failed | killed …
  progress?: string;
  toolUses?: number;
  summary?: string;
  startedAt: number;
  endedAt?: number;
};

// ACP ContentBlock（只列我们会用到的）
export type ContentBlock =
  | { type: "text"; text: string }
  // blob：推给界面时图片换成了地址（/api/blobs/<sha256>），data 是空的；接口直接回的（撤回、回退）还是带 data 的原样
  | { type: "image"; data: string; mimeType: string; uri?: string; blob?: string }
  | { type: "resource_link"; uri: string; name: string }
  | { type: "resource"; resource: { uri: string; text?: string } };

export type HubEvent = {
  seq: number;
  ts: number;
  kind: "user" | "update" | "turn_start" | "turn_end" | "permission" | "permission_done" | "ask" | "ask_done" | "notice" | "task" | "steer_state";
  task?: AgentTask;
  messageId?: string;
  content?: ContentBlock[];
  steer?: boolean;
  auto?: boolean;
  from?: string;
  durationMs?: number;
  outputTokens?: number;
  update?: any;
  stopReason?: string;
  error?: string;
  reqId?: string;
  request?: any;
  response?: any;
  level?: string;
  text?: string;
};

export type PermissionOption = { optionId: string; name: string; kind: "allow_once" | "allow_always" | "reject_once" | "reject_always" };
export type PermissionRequest = {
  sessionId: string;
  toolCall: { toolCallId: string; title?: string; kind?: string; rawInput?: any; _meta?: any };
  options: PermissionOption[];
};

export type AskQuestion = {
  id: string;
  header?: string;
  question: string;
  options?: { label: string; description?: string }[];
  multiSelect?: boolean;
  allowOther?: boolean;
  secret?: boolean;
};
export type AskRequest = { sessionId: string; toolCallId?: string; questions: AskQuestion[] };

export type Hello = {
  version: string;
  agents: AgentInfo[];
  sessions: SessionSummary[];
  layout: any;
  config: any;
  home: string;
  pluginsDir: string;
  remoteConns?: RemoteConn[];
};

/** 正在连着的远程界面（一条远程 /ws 一条；internal/server/remoteconns.go）。since 是 unix 毫秒。 */
export type RemoteConn = { id: number; device: string; label: string; ip: string; client: string; since: number };

export type PluginBuild = { id: string; path: string; code?: string; error?: string; hash: string; mtime: number };

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

/** 设置 → MCP 页「Gerdr Agent 在用的」一行（后端 native.MCPEntry）。 */
export type NativeMCPEntry = {
  name: string;
  from: "gerdr" | "claude";
  type: "stdio" | "http" | "sse";
  target: string;
  status: "connected" | "failed" | "connecting" | "idle";
  tools?: number;
  error?: string;
};
