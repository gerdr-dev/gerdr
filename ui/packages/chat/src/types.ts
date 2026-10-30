// The shapes the chat works with: the event log (HubEvent, see chatlog/EVENTS.md in the gerdr repo — change both
// sides together), ACP content blocks and requests, and ChatSession, what the chat needs to know about a session.

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
  kind:
    | "user"
    | "update"
    | "turn_start"
    | "turn_end"
    | "permission"
    | "permission_done"
    | "ask"
    | "ask_done"
    | "notice"
    | "task"
    | "steer_state"
    | "shell"
    | "shell_out"
    | "shell_done";
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
  until?: number; // notice retry: when the retry fires (unix ms)
  exit?: number; // shell_done：退出码
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

/** What the chat needs to know about a session (gerdr's SessionSummary has more). Mirrors chatlog.State. */
export type ChatSession = {
  id: string;
  /** Which agent (an id the host knows; shown when the conversation is empty). */
  agent: string;
  /** The engine underneath: claude | codex | native (decides a few engine-specific behaviours). */
  base?: string;
  cwd: string;
  status: SessionStatus;
  error?: string;
  modes?: { availableModes: SessionMode[]; currentModeId: string };
  configOptions?: ConfigOption[];
  commands?: AvailableCommand[];
  /** commands came from a cache (the agent hasn't reported its own yet) */
  commandsCached?: boolean;
  tasks?: AgentTask[];
  turn?: { startedAt: number; outputTokens: number }; // the running turn
  context?: { tokens: number; window: number }; // context usage
};
