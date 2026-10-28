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
export type AvailableCommand = { name: string; description: string; input?: { hint?: string } };

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
  | { type: "image"; data: string; mimeType: string; uri?: string }
  | { type: "resource_link"; uri: string; name: string }
  | { type: "resource"; resource: { uri: string; text?: string } };

export type HubEvent = {
  seq: number;
  ts: number;
  kind: "user" | "update" | "turn_start" | "turn_end" | "permission" | "permission_done" | "ask" | "ask_done" | "notice" | "task";
  task?: AgentTask;
  messageId?: string;
  content?: ContentBlock[];
  steer?: boolean;
  snapshot?: string;
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
};

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
