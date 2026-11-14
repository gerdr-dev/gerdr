import type { AppUpdateStatus } from "./native";
import { localizeAddon } from "./i18n";
import type { LLMModel, LLMProvider } from "./store";
import type { AvailableCommand, ConfigOption, SessionMode } from "@gerdr/chat/types";
import type {
  ContentBlock,
  ExtSettings,
  ExtSkill,
  ExtView,
  Hello,
  HostInfo,
  HubEvent,
  WaitView,
  MemoryStore,
  AddonBuild,
  AddonStore,
  SessionSummary,
  TermInfo,
} from "./types";

/** 正开着某个会话的别的进程（herdr / 终端里的交互式 claude） */
export type Holder = { pid: number; name?: string; status?: string; cwd?: string };

/** 一条用量额度（后端 acpx.UsageLimit）：label 直接显示，percent 0–100，resetsAt unix ms（0 = 不知道） */
export type UsageLimit = { kind: string; label: string; percent: number; resetsAt?: number; windowMins?: number };
/** One finished turn (internal/hub/turns.go). */
export type TurnRecord = {
  ts: number;
  sessionId: string;
  agent: string;
  cwd?: string;
  stopReason: string;
  auto?: boolean;
  durationMs?: number;
  outputTokens?: number;
  costUsd?: number;
  /** One entry per model (acpx.Tokens); input includes cacheRead and cacheWrite. */
  tokens?: { model: string; input?: number; output?: number; cacheRead?: number; cacheWrite?: number }[];
};
/** 一家 agent 账号的额度（后端 hub.UsageInfo）：出错时 data 还是上一次拿到的，at 是它的时间 */
export type UsageInfo = { agent: string; name: string; data?: { plan?: string; limits: UsageLimit[] }; error?: string; at?: number };

/** Settings → Labs → Diagnosis (internal/doctor). */
export type DoctorLevel = "ok" | "info" | "warn" | "bad";
export type DoctorFix = { kind: "reloadPath" | "useBin"; label: string; agent?: string; path?: string };
export type DoctorInstall = { path: string; version?: string; error?: string; blocked?: boolean; current?: boolean };
export type DoctorCheck = {
  id: string;
  group: "path" | "agent" | "tool" | "usage";
  title: string;
  level: DoctorLevel;
  summary: string;
  detail?: string;
  installs?: DoctorInstall[];
  fixes?: DoctorFix[];
};
export type DoctorReport = { at: number; deep: boolean; level: DoctorLevel; checks: DoctorCheck[] };

/** Settings → Remote access → Control this computer (internal/screen): the switch, what macOS has granted, the main display in points. */
export type ScreenStatus = { enabled: boolean; supported: boolean; screen: boolean; input: boolean; width: number; height: number };
/** One action on the remote screen; x / y are fractions (0..1) of the display. */
export type ScreenInput =
  | { kind: "click"; x: number; y: number; button?: "left" | "right"; count?: number }
  | { kind: "scroll"; x: number; y: number; dy: number }
  | { kind: "text"; text: string }
  | { kind: "key"; key: string; mods?: ("cmd" | "shift" | "opt" | "ctrl")[] };

export class ApiError extends Error {
  status: number;
  /** 远程那条路上的细分原因：reason="stepup"（再加 passkey 前先验一次）、need="pair" | "passkey"（见 docs/dev/REMOTE.md） */
  reason?: string;
  need?: string;
  /** 业务上的细分：code="held_elsewhere"（会话还在别处开着，holders 是占着它的进程） */
  code?: string;
  holders?: Holder[];
  /** fs/stat：短写的相对路径对上了好几个文件，这是它们的绝对路径 */
  candidates?: string[];
  constructor(status: number, message: string, data?: { reason?: string; need?: string; code?: string; holders?: Holder[]; candidates?: string[] }) {
    super(message);
    this.status = status;
    this.reason = data?.reason;
    this.need = data?.need;
    this.code = data?.code;
    this.holders = data?.holders;
    this.candidates = data?.candidates;
  }
}

/**
 * Called when a request comes back 401 with `need` (remote path: the device cookie is gone, or it's due for
 * re-verification with a passkey). The WebSocket stays open through that, so without this the page went on showing
 * "not paired" toasts and never offered the passkey; sync.ts turns it into authError → reload → the pair page.
 * Not for /h/<host>/… : that 401 is another computer's, and reloading here would change nothing.
 */
let authLost: (() => void) | undefined;
export function onAuthLost(f: () => void) {
  authLost = f;
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    // X-Gerdr：服务端拿它挡 CSRF（跨站页面发不出自定义头）。
    headers: body !== undefined ? { "Content-Type": "application/json", "X-Gerdr": "1" } : { "X-Gerdr": "1" },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let data: any = undefined;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = text;
  }
  if (!res.ok) {
    if (res.status === 401 && data?.need && !path.startsWith("/h/")) authLost?.();
    throw new ApiError(res.status, (data && data.error) || res.statusText, typeof data === "object" ? data : undefined);
  }
  return data as T;
}

// 网络层的失败（断网、服务端正在重启）：fetch 直接抛 TypeError；反代 / 系统代理会给 502–504。
// 这些值得重试；4xx 和业务错误不重试。
export function isNetworkError(e: unknown) {
  return e instanceof TypeError || (e instanceof ApiError && (e.status === 502 || e.status === 503 || e.status === 504));
}

export function uuid(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return "10000000-1000-4000-8000-100000000000".replace(/[018]/g, (c) => (+c ^ ((Math.random() * 16) >> (+c / 4))).toString(16));
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * 发消息：网络不好时自动重发（最多一分钟左右）。每次带同一个 id，服务端认得出「已经收到过了，
 * 只是响应丢了」，不会变成两条。onRetry 给界面说一声「在重试」。
 */
// now：agent 在跑时立即插话（截断当前这次生成）；默认排到下一个空档（当前这步做完）再塞进去，和 Claude Code 的输入框一样。
async function sendWithRetry(id: string, content: ContentBlock[], onRetry?: (attempt: number) => void, now?: boolean, msgId = uuid()) {
  const waits = [500, 1000, 2000, 4000, 8000, 15000, 30000];
  for (let i = 0; ; i++) {
    try {
      return await req<{ messageId: string; steered: boolean }>("POST", `/api/sessions/${id}/send`, { content, id: msgId, now: now || undefined });
    } catch (e) {
      if (!isNetworkError(e) || i >= waits.length) throw e;
      onRetry?.(i + 1);
      await sleep(waits[i]);
    }
  }
}

export type HerdrPane = { id: string; agent?: string; sessionId?: string; cwd: string; title?: string; existing?: string; skip?: string };
export type HerdrWorkspace = { id: string; label: string; tabs: { label: string; panes: HerdrPane[] }[] };

/** 不让电脑睡的状态（internal/awake.Status）。时间是 unix 毫秒，0 = 没有，-1 = 无限期。 */
export type AwakeStatus = {
  available: boolean;
  awake: boolean;
  auto: boolean;
  remote: number;
  graceUntil: number;
  manualUntil: number;
  /** Lid closed too: authorized yet / on right now / why it couldn't turn on / let go on low battery */
  lidReady: boolean;
  lidOn: boolean;
  lidErr: string;
  lowBattery: boolean;
  /** 现在的电量和电源；percent -1 = 没电池（台式机） */
  battery: { percent: number; onBattery: boolean; charging: boolean };
};

/** One approval / question waiting on someone (hub.PendingReq). */
/** A panel's inbox item (internal/work/inbox.go InboxItem). */
export type PanelInboxItem = { panel: string; id: string; title: string; body?: string; session?: string; at: number };

export type PendingReq = {
  sessionId: string;
  title?: string;
  agent: string;
  cwd: string;
  reqId: string;
  kind: "permission" | "ask";
  request: any;
  summary: string;
  at: number;
};

/** 等批准的一次 App 登录（auth.CLIRequest） */
export type CliRequest = { label: string; ip: string; local: boolean; device: string; deviceLabel?: string; expires: string };

export const api = {
  usage: (force?: boolean) => req<UsageInfo[]>("GET", `/api/usage${force ? "?force=1" : ""}`),
  /** The turns that ended in [since, until) (unix ms), oldest first (internal/hub/turns.go). */
  turns: (since: number, until?: number) => req<TurnRecord[]>("GET", `/api/turns?since=${since}${until ? `&until=${until}` : ""}`),
  // Events (docs/dev/EVENTS.md): add-ons' g.events
  events: (q: { since?: number; on?: string; key?: string; limit?: number } = {}) => {
    const p = new URLSearchParams();
    for (const [k, v] of Object.entries(q)) if (v != null && v !== "") p.set(k, String(v));
    return req<{ list: unknown[] }>("GET", `/api/events?${p}`).then((r) => r.list);
  },
  emitEvent: (e: Record<string, unknown>) => req<{ event: unknown; fresh: boolean }>("POST", "/api/events", e),
  stats: (webpid?: number) => req<any>("GET", `/api/system/stats${webpid ? `?webpid=${webpid}` : ""}`),
  herdrScan: () => req<{ workspaces: HerdrWorkspace[] }>("GET", "/api/import/herdr"),
  herdrImport: (workspaces: string[]) =>
    req<{
      workspaces: { label: string; tabs: { label: string; tree: import("./actions").ImportNode }[] }[];
      imported: number;
      reused: number;
      errors?: string[];
    }>("POST", "/api/import/herdr", { workspaces }),
  hello: () => req<Hello>("GET", "/api/hello"),
  /** A key for Work local functions' ctx.secrets.get ("" deletes it); the value is never served back. */
  setWorkSecret: (name: string, value: string) => req("POST", "/api/work/secrets", { name, value }),
  /** Where the project's Work came from (a git repository or not), and replacing it: work/source.go. */
  workSource: (project: string) =>
    req<{ git: boolean; remote?: string; branch?: string; dirty?: boolean }>("GET", `/api/work/source?project=${encodeURIComponent(project)}`),
  /** A project's panels beyond its Work: install one from a repository, add a blank one, remove one (keys "<project>/<name>"). */
  installPanel: (project: string, url: string, name?: string) => req<{ key: string }>("POST", "/api/work/install", { project, url, name }),
  newPanel: (project: string, name: string) => req<{ key: string }>("POST", "/api/work/panels", { project, name }),
  /** A panel's rail icon in its ui.json: the same kinds as an agent's (IconPicker), and a colour. */
  renamePanel: (key: string, title: string) => req<{ ok: boolean }>("POST", "/api/work/panels/title", { key, title }),
  setPanelIcon: (key: string, icon: string, color: string) => req<{ ok: boolean }>("POST", "/api/work/panels/icon", { key, icon, color }),
  /** A project's panels (its own Work first, unless removed), with their ui.json (the user's rename / icon over it) and where they came from. */
  panels: (project: string) =>
    req<{
      panels: { key: string; name: string; config: Record<string, unknown>; source: { git: boolean; remote?: string; dirty?: boolean } }[];
      ownRemoved: boolean;
    }>("GET", `/api/work/panels?project=${encodeURIComponent(project)}`),
  /** Put a panel at its 0-based place on the rail (the person's order, work/order.go). */
  movePanel: (key: string, to: number) => req<{ ok: boolean }>("POST", "/api/work/panels/move", { key, to }),
  unremovePanel: (key: string, trash: string) => req<{ ok: boolean }>("POST", "/api/work/panels/unremove", { key, trash }),
  restoreWork: (project: string) => req<{ ok: boolean }>("POST", "/api/work/panels/restore", { project }),
  removePanel: (key: string) => req<{ trash: string }>("POST", "/api/work/panels/remove", { key }),
  updateWork: (project: string) => req<{ output: string }>("POST", "/api/work/update", { project }),
  // In the interface's language: addon.json's i18n is applied here, once, so every place that shows a name reads it
  addons: () => req<AddonBuild[]>("GET", "/api/addons").then((l) => l.map(localizeAddon)),
  addonMessage: (id: string, data: unknown) => req("POST", `/api/addons/${encodeURIComponent(id)}/message`, { data }),
  /** g.fetch: the backend makes the request, so the target needs no CORS (internal/server/addonfetch.go) */
  addonFetch: (id: string, b: { url: string; method?: string; headers?: Record<string, string>; body?: string; binary?: boolean }) =>
    req<{ status: number; statusText: string; headers: Record<string, string>; body: string; base64?: boolean }>(
      "POST",
      `/api/addons/${encodeURIComponent(id)}/fetch`,
      b,
    ),
  setAddonEnabled: (id: string, enabled: boolean) => req("PUT", `/api/addons/${encodeURIComponent(id)}/enabled`, { enabled }),
  /** null = back to the default */
  setAddonSettings: (id: string, values: Record<string, unknown>) => req("PUT", `/api/addons/${encodeURIComponent(id)}/settings`, { values }),
  uninstallAddon: (id: string) => req("DELETE", `/api/addons/${encodeURIComponent(id)}`),
  addonStores: () => req<AddonStore[]>("GET", "/api/addon-stores").then((l) => l.map((st) => ({ ...st, addons: st.addons.map(localizeAddon) }))),
  addAddonStore: (source: string) => req<{ source: string }>("POST", "/api/addon-stores", { source }),
  removeAddonStore: (source: string) => req("DELETE", `/api/addon-stores?source=${encodeURIComponent(source)}`),
  refreshAddonStore: (source: string) => req("POST", "/api/addon-stores/refresh", { source }),
  /** 409 with code "conflict": the folder isn't from a store, or was changed after installing; force overwrites. */
  installAddon: (source: string, id: string, force = false) => req("POST", "/api/addon-stores/install", { source, id, force }),
  putLayout: (data: unknown, client: string) => req("PUT", "/api/layout", { data, client }),
  putConfig: (data: unknown, client: string) => req("PUT", "/api/config", { data, client }),
  setUnread: (id: string, unread: boolean, client: string) => req("POST", "/api/unread", { id, unread, client }),
  // Gerdr Agent 的模型设置：拉服务商的模型列表、拿一个模型试说一句（服务商参数是页面上的草稿，不用先保存）
  llmModels: (p: LLMProvider) => req<{ models: string[] }>("POST", "/api/llm/models", p),
  // 设置 →「记忆」：三家 agent 的长期记忆，只能读写它们记忆目录里的 .md
  memory: () => req<{ stores: MemoryStore[] }>("GET", "/api/memory"),
  memoryRead: (path: string) => req<{ content: string }>("GET", `/api/memory/file?path=${encodeURIComponent(path)}`),
  memoryWrite: (path: string, content: string) => req("PUT", "/api/memory/file", { path, content }),
  memoryDelete: (path: string) => req("DELETE", `/api/memory/file?path=${encodeURIComponent(path)}`),
  llmTest: (provider: LLMProvider, model: LLMModel) => req<{ reply: string; api: string }>("POST", "/api/llm/test", { provider, model }),
  doctor: () => req<DoctorReport | null>("GET", "/api/doctor"),
  /** deep = also run every claude / codex install found (can pop macOS's "malware blocked" dialog for a blocked one) */
  doctorRun: (deep: boolean) => req<DoctorReport>("POST", "/api/doctor/run", { deep }),
  doctorFix: (f: DoctorFix) => req<DoctorReport>("POST", "/api/doctor/fix", f),
  awake: () => req<AwakeStatus>("GET", "/api/awake"),
  battery: () => req<AwakeStatus["battery"]>("GET", "/api/battery"),
  /** Every approval / question waiting on someone, across all sessions, oldest first (hub.PendingReq). */
  pending: () => req<PendingReq[]>("GET", "/api/pending"),
  /** What panels say needs the user (ctx.inbox.set; server/inbox.go). */
  inbox: () => req<PanelInboxItem[]>("GET", "/api/inbox"),
  setAwake: (b: { minutes?: number; until?: number; forever?: boolean }) => req<AwakeStatus>("PUT", "/api/awake", b),
  setupLid: () => req<AwakeStatus>("POST", "/api/awake/lid-setup", {}),
  /** host: another computer's screen, through this one (server/hosts.go) */
  screen: (host?: string) => req<ScreenStatus>("GET", `/api/screen${hostQ(host)}`),
  screenInput: (i: ScreenInput, host?: string) => req("POST", `/api/screen/input${hostQ(host)}`, i),
  /** Local only: shows macOS's permission prompts on this computer and opens the pane still missing. */
  screenRequestAccess: () => req<ScreenStatus>("POST", "/api/screen/request-access", {}),
  /** continueFrom: another agent's session whose conversation the new one carries on (hub/handoff.go) */
  /** host: start it on that computer (server/hosts.go) */
  createSession: (o: {
    agent: string;
    cwd: string;
    mode?: string;
    config?: Record<string, string>;
    import?: string;
    title?: string;
    continueFrom?: string;
    host?: string;
    /** the project (layout space) it's started in: its skills / MCP apply from the first turn (projectOf in lib/layout.ts) */
    project?: string;
  }) => req<SessionSummary>("POST", "/api/sessions", o),
  /** input：建好就敲进去的字（对话里「在新终端里运行」） */
  /** host: the shell runs on that computer; its id comes back as "<host>:<id>" and everything else routes by it */
  termCreate: (o: { cwd?: string; from?: string; cols?: number; rows?: number; input?: string; host?: string }) => req<TermInfo>("POST", "/api/terms", o),
  /** All live and recently exited terminal processes, used to identify what is holding an update open. */
  terms: () => req<TermInfo[]>("GET", "/api/terms"),
  /** 往终端里敲字（对话里「在当前终端里运行」） */
  termInput: (id: string, data: string) => req("POST", `/api/terms/${encodeURIComponent(id)}/input`, { data }),
  /** liveCwd：顺带问 shell 现在在哪个目录（mac 上要跑一次 lsof，别每次都问） */
  termInfo: (id: string, liveCwd?: boolean) => req<TermInfo>("GET", `/api/terms/${encodeURIComponent(id)}${liveCwd ? "?cwd=1" : ""}`),
  termKill: (id: string) => req("DELETE", `/api/terms/${encodeURIComponent(id)}`),
  send: sendWithRetry,
  /** Stores a composer attachment the agent can't take inline (a video) on the gerdr machine; returns its absolute path. */
  /** host: onto another computer, for its agent (the answer is a path there) */
  upload: async (f: File, host?: string) => {
    const res = await fetch(`/api/fs/upload?name=${encodeURIComponent(f.name)}${hostQ(host, "&")}`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "X-Gerdr": "1" },
      body: f,
    });
    const data = await res.json().catch(() => undefined);
    if (!res.ok) throw new ApiError(res.status, data?.error || res.statusText, data);
    return data as { path: string };
  },
  // ---- Artifacts folders (docs/dev/ARTIFACTS.md, server/artifactsapi.go). host: the folder is on another computer ----
  /** Where a session's artifacts go (it may not exist yet) and its records; or a folder given by path. */
  artifacts: (by: { session: string } | { dir: string }, host?: string) =>
    req<ArtifactsInfo>(
      "GET",
      `/api/artifacts?${"session" in by ? `session=${encodeURIComponent(by.session)}` : `dir=${encodeURIComponent(by.dir)}`}${hostQ(host, "&")}`,
    ),
  recordArtifact: (by: { session: string } | { dir: string }, item: Partial<ArtifactItem>, host?: string) =>
    req<{ dir: string; item: ArtifactItem }>("POST", `/api/artifacts${hostQ(host)}`, { ...by, ...item }),
  /** One level of a directory, Gerdr's own files left out */
  fsList: (path: string, host?: string) => req<{ path: string; entries: FsEntry[] }>("GET", `/api/fs/list?path=${encodeURIComponent(path)}${hostQ(host, "&")}`),
  fsMkdir: (path: string, host?: string) => req<{ path: string }>("POST", `/api/fs/mkdir${hostQ(host)}`, { path }),
  fsMove: (from: string, to: string, host?: string) => req<{ path: string }>("POST", `/api/fs/move${hostQ(host)}`, { from, to }),
  fsTrash: (path: string, host?: string) => req("POST", `/api/fs/trash${hostQ(host)}`, { path }),
  /** The version of a file an editor read (sha256), to save it back only if nobody changed it since */
  fileHash: (path: string, host?: string) =>
    req<FileStat & { sha256?: string }>("GET", `/api/fs/stat?path=${encodeURIComponent(path)}&hash=1${hostQ(host, "&")}`),
  /**
   * Writes a file. ifMatch: the sha256 it was read at ("none": it must not exist yet, "*": overwrite). A 412 means
   * it changed meanwhile (ApiError status 412); the answer is its new sha256.
   */
  fsPut: async (path: string, body: BodyInit, ifMatch: string, host?: string) => {
    const res = await fetch(`/api/fs/file?path=${encodeURIComponent(path)}&ifMatch=${encodeURIComponent(ifMatch)}${hostQ(host, "&")}`, {
      method: "PUT",
      credentials: "same-origin",
      headers: { "X-Gerdr": "1" },
      body,
    });
    const data = await res.json().catch(() => undefined);
    if (!res.ok) throw new ApiError(res.status, data?.error || res.statusText, data);
    return data as { path: string; sha256: string };
  },
  /** Stores a pasted image (base64) on the gerdr machine so other devices' composers can show it; returns /api/blobs/<sha256>. */
  saveBlob: (data: string) => req<{ url: string }>("POST", "/api/blobs", { data }),
  /** 输入框里的 `!命令`：gerdr 在会话目录里直接跑，不经过 agent；输出进对话、跟着下一条消息带给 agent */
  shell: (id: string, command: string) => req<{ id: string }>("POST", `/api/sessions/${id}/shell`, { command, id: uuid() }),
  stopShell: (id: string, run: string) => req("POST", `/api/sessions/${id}/shell/${run}/stop`, {}),
  cancel: (id: string) => req("POST", `/api/sessions/${id}/cancel`, {}),
  /** 关掉 agent 那边（claude 进程当场收掉），记录留着；下次发话自动接回来 */
  shutdown: (id: string) => req("POST", `/api/sessions/${id}/shutdown`, {}),
  reload: (id: string) => req("POST", `/api/sessions/${id}/reload`, {}),
  takeover: (id: string, action: "fork" | "kill") => req("POST", `/api/sessions/${id}/takeover`, { action }),
  searchFiles: (id: string, q: string) => req<{ files: string[] }>("GET", `/api/sessions/${id}/files?q=${encodeURIComponent(q)}`),
  warm: (id: string) => req("POST", `/api/sessions/${id}/warm`, {}),
  withdraw: (id: string, messageId: string) => req<{ content: ContentBlock[]; withdrawn?: number }>("POST", `/api/sessions/${id}/withdraw`, { messageId }),
  withdrawLast: (id: string) => req<{ content: ContentBlock[]; withdrawn?: number }>("POST", `/api/sessions/${id}/withdraw-last`, {}),
  /** Something the UI saw take too long, into gerdr.log (server/uiapi.go): only numbers, in ms */
  uiSlow: (what: string, session: string, ms: Record<string, number>) => req("POST", "/api/ui/slow", { what, session, ms }),
  escape: (id: string) => req<{ content: ContentBlock[]; rolledBack?: boolean; withdrawn?: number }>("POST", `/api/sessions/${id}/escape`, {}),
  answer: (id: string, reqId: string, response: unknown) => req("POST", `/api/sessions/${id}/answer`, { reqId, response }),
  setMode: (id: string, mode: string) => req("POST", `/api/sessions/${id}/mode`, { mode }),
  setConfig: (id: string, cid: string, value: string) => req("POST", `/api/sessions/${id}/config`, { id: cid, value }),
  /** auto：插件起的名（不算人改的，人改过名时不生效）。 */
  rename: (id: string, title: string, auto?: boolean) => req("POST", `/api/sessions/${id}/rename`, { title, auto }),
  /** 起标题的材料：当前标题、这段上下文（最后一次 /clear 之后）用户说过的话。 */
  titleState: (id: string) => req<{ title: string; customTitle: boolean; prompts: string[] }>("GET", `/api/sessions/${id}/title`),
  /** 用这个会话的 agent 的账号跑一次小问答（claude 用 haiku，codex 用默认模型的低推理档）。 */
  complete: (id: string, prompt: string) => req<{ text: string }>("POST", `/api/sessions/${id}/complete`, { prompt }),
  /** 序号 before 之前的再一段日志（打开会话时只推了最近一段，往上翻到顶来拉）；older = 还有更早的。 */
  olderEvents: (id: string, before: number) => req<{ events: HubEvent[]; older: boolean }>("GET", `/api/sessions/${id}/events/older?before=${before}`),
  rewind: (id: string, messageId: string) => req<{ content: ContentBlock[] }>("POST", `/api/sessions/${id}/rewind`, { messageId }),
  importable: (agent: string, cwd: string, host?: string) =>
    req<{ sessionId: string; cwd: string; title?: string; updatedAt?: string }[]>(
      "GET",
      `/api/importable?agent=${encodeURIComponent(agent)}&cwd=${encodeURIComponent(cwd)}${hostQ(host, "&")}`,
    ),
  stopTask: (id: string, taskId: string) => req("POST", `/api/sessions/${id}/tasks/${encodeURIComponent(taskId)}/stop`, {}),
  // What sessions wait for (`gerdr wait`, internal/waits): every one with how it stands; drop one
  waits: () => req<{ list: WaitView[] }>("GET", "/api/waits"),
  deleteWait: (id: string) => req("DELETE", `/api/waits/${encodeURIComponent(id)}`),
  backgroundTool: (id: string, toolCallId: string) =>
    req<{ backgrounded: boolean }>("POST", `/api/sessions/${id}/tools/${encodeURIComponent(toolCallId)}/background`, {}),
  // Signing the session's agent in again after its login expired (hub/login.go)
  loginStart: (id: string) => req<LoginState>("POST", `/api/sessions/${id}/login`, {}),
  loginState: (id: string) => req<LoginState>("GET", `/api/sessions/${id}/login`),
  loginCode: (id: string, code: string) => req("POST", `/api/sessions/${id}/login/code`, { code }),
  loginCancel: (id: string) => req("POST", `/api/sessions/${id}/login/cancel`, {}),
  allowRules: (id: string, rules: string[]) => req("POST", `/api/sessions/${id}/allow-rules`, { rules }),
  ext: () => req<ExtView>("GET", "/api/ext"),
  putExt: (settings: ExtSettings) => req<ExtView>("PUT", "/api/ext/settings", settings),
  readSkill: (name: string) => req<{ content: string }>("GET", `/api/ext/skills/${encodeURIComponent(name)}`),
  writeSkill: (name: string, content: string) => req<ExtView>("PUT", `/api/ext/skills/${encodeURIComponent(name)}`, { content }),
  deleteSkill: (name: string) => req<ExtView>("DELETE", `/api/ext/skills/${encodeURIComponent(name)}`),
  importSkill: (dir: string) => req<ExtView>("POST", "/api/ext/skills/import", { dir }),
  // A Gerdr project's own skills (<GERDR_DIR>/projects/<id>/skills): only that project's sessions get them
  projectSkills: (project: string) => req<{ skills: ExtSkill[] }>("GET", `/api/projects/${encodeURIComponent(project)}/skills`),
  readProjectSkill: (project: string, name: string) =>
    req<{ content: string }>("GET", `/api/projects/${encodeURIComponent(project)}/skills/${encodeURIComponent(name)}`),
  writeProjectSkill: (project: string, name: string, content: string) =>
    req<{ skills: ExtSkill[] }>("PUT", `/api/projects/${encodeURIComponent(project)}/skills/${encodeURIComponent(name)}`, { content }),
  deleteProjectSkill: (project: string, name: string) =>
    req<{ skills: ExtSkill[] }>("DELETE", `/api/projects/${encodeURIComponent(project)}/skills/${encodeURIComponent(name)}`),
  // MCP OAuth (Settings → MCP): sign-in state of every remote server; refresh = probe them all now
  mcpAuth: (refresh?: boolean) => req<MCPAuth[]>("GET", `/api/mcp/auth${refresh ? "?refresh=1" : ""}`),
  mcpAuthStart: (name: string, url: string) => req<{ url: string }>("POST", `/api/mcp/auth/${encodeURIComponent(name)}/start`, { url }),
  /** Reconnect one MCP server: gerdr's own connections (tools = what it offers now) and every live Claude Code process (agents). */
  mcpReconnect: (name: string, url: string) =>
    req<{ tools?: number; error?: string; agents?: number }>("POST", `/api/mcp/${encodeURIComponent(name)}/reconnect`, { url }),
  mcpAuthLogout: (name: string) => req("DELETE", `/api/mcp/auth/${encodeURIComponent(name)}`),
  // Work view: run a local function (~/.gerdr/work/local/<file>.ts, "file.export")
  /** A project's saved Code layout (its workspaces), {} when it has none: re-creating a closed project brings it back. */
  projectLayout: (name: string) =>
    req<{ workspaces?: import("./layout").Workspace[]; active?: string }>("GET", `/api/projects/${encodeURIComponent(name)}/layout`),
  /** Delete a closed project: its directory moves to <GERDR_DIR>/trash/projects/ (where it went is `trash`). */
  deleteProject: (name: string) => req<{ trash: string }>("DELETE", `/api/projects/${encodeURIComponent(name)}`),
  /** Undo deleteProject: `trash` (what it returned) moves back. */
  restoreProject: (name: string, trash: string) => req("POST", `/api/projects/${encodeURIComponent(name)}/restore`, { trash }),
  work: (project: string) => req<{ dir: string }>("GET", `/api/work?project=${encodeURIComponent(project)}`),
  /** Panels' badges on their rail buttons by panel key: >0 a number, 0 a dot (server/badge.go). */
  badges: () => req<Record<string, number>>("GET", "/api/work/badges"),
  /** n: >0 a number, 0 a dot, <0 none. */
  setBadge: (key: string, n: number) => req("POST", "/api/work/badge", { key, n }),
  workRun: <T = any>(fn: string, input?: unknown, project?: string) =>
    req<{ result: T }>("POST", "/api/work/run", { fn, input, project }).then((r) => r.result),
  /** host: browse another computer's folders (the request is proxied there, server/hosts.go) */
  dirs: (path: string, host?: string) =>
    req<{ path: string; parent: string; dirs: string[]; home: string; git: boolean }>(
      "GET",
      `/api/fs/dirs?path=${encodeURIComponent(path)}${host ? `&host=${encodeURIComponent(host)}` : ""}`,
    ),
  /** Create a folder (and its parents) for the path picker; absolute or ~ paths only. */
  mkdir: (path: string, host?: string) => req<{ path: string }>("POST", `/api/fs/mkdir${host ? `?host=${encodeURIComponent(host)}` : ""}`, { path }),
  /** Other computers this one drives (docs/dev/MULTIHOST.md) */
  hosts: () => req<HostInfo[]>("GET", "/api/hosts"),
  /** invite: what the other computer gave (its "https://name/?peer=CODE") */
  addHost: (invite: string, name?: string) => req<HostInfo>("POST", "/api/hosts", { invite, name }),
  removeHost: (id: string) => req("DELETE", `/api/hosts/${encodeURIComponent(id)}`),
  renameHost: (id: string, name: string) => req("POST", `/api/hosts/${encodeURIComponent(id)}/rename`, { name }),
  /** On the computer to be driven: a one-off invite for another Gerdr to control this one */
  peerInvite: () => req<{ code: string; expires: string; url: string }>("POST", "/api/remote/pair-code", { peer: true }),
  /** host: a file on another computer (docs/dev/MULTIHOST.md); the stat comes back marked with it */
  fileStat: (path: string, cwd?: string, host?: string) =>
    req<FileStat>("GET", `/api/fs/stat?path=${encodeURIComponent(path)}&cwd=${encodeURIComponent(cwd ?? "")}${hostQ(host, "&")}`).then((s) =>
      host ? { ...s, host } : s,
    ),
  /** A cookie-less /f/<token>/<name> link to one file for the system browser, valid 5 minutes (server/filelink.go).
   * For another computer's file the link is on that computer's own address (its /f/ is public there too).
   * origin: the loopback address the request reached, when it came from this machine (the App's http://gerdr/ is
   * unknown to the system browser). */
  fileLink: (path: string, host?: string) => req<{ url: string; origin?: string }>("POST", `/api/fs/link${hostQ(host)}`, { path }),
  fileExists: (paths: string[], cwd?: string, host?: string) =>
    req<{ exists: Record<string, boolean> }>("POST", `/api/fs/exists${hostQ(host)}`, { paths, cwd: cwd ?? "" }),

  // 远程访问（设置 →「远程访问」；passkey 的两段 ceremony 在 lib/passkey.ts）
  remoteStatus: () => req<{ status: RemoteStatus; remote: boolean }>("GET", "/api/remote/status"),
  remoteWhoami: () =>
    req<{ remote: boolean; authed: boolean; need?: string; deviceId?: string; passkeys: number; passkeyAvailable: boolean }>("GET", "/api/remote/whoami"),
  /** 出配对码：只有本机口能调（远程那一侧服务端直接 403） */
  remotePairCode: () => req<{ code: string; expires: string; url: string; qr?: string }>("POST", "/api/remote/pair-code", {}),
  remoteDevices: () => req<{ devices: RemoteDevice[]; me: string }>("GET", "/api/remote/devices"),
  remoteRevoke: (id: string) => req<{ revoked: number; label: string }>("DELETE", `/api/remote/devices/${encodeURIComponent(id)}`),
  remoteReverify: (id: string) => req<{ label: string; passkeys: number }>("POST", `/api/remote/devices/${encodeURIComponent(id)}/reverify`),
  /** 全部踢掉：设备 + passkey 一起（只清设备的话，留着的 passkey 能立刻换回一份新凭据） */
  remoteRevokeAll: () => req<{ revoked: number; passkeys: number }>("DELETE", "/api/remote/devices"),
  remotePasskeys: () => req<{ passkeys: PasskeyInfo[]; available: boolean; url: string }>("GET", "/api/remote/passkeys"),
  remoteDeletePasskey: (id: string) => req("DELETE", `/api/remote/passkeys/${encodeURIComponent(id)}`),
  remoteUnlock: () => req("POST", "/api/remote/unlock", {}),
  service: () => req<ServiceStatus>("GET", "/api/service"),
  commandSet: (key: string) => req<AvailableCommand[]>("GET", `/api/commands/${key}`),
  optionSet: (key: string) => req<{ modes?: SessionMode[]; config?: ConfigOption[] }>("GET", `/api/options/${key}`),
  /** The desktop App's update status as its page last relayed it (null: no App window lately); server/appupdate.go. */
  appUpdate: () => req<{ status: AppUpdateStatus | null; client: string }>("GET", "/api/app-update"),
  reportAppUpdate: (status: AppUpdateStatus) => req<{}>("PUT", "/api/app-update", { client: clientId, status }),
  /** A button in Settings → 版本 pressed off the App: the App's page carries it to the shell. */
  appUpdateOp: (op: "check" | "apply" | "auto", on?: boolean) => req<{}>("POST", "/api/app-update/op", { op, on: !!on }),
  uiStatus: () => req<UIStatus>("GET", "/api/ui/status"),
  /** Settings → Labs → Production React: build its packages before reloading onto them; state "" = ready */
  prepareProdReact: () => req<{ ready: boolean; state: string }>("POST", "/api/ui/prod-react"),
  setService: (on: boolean) => req("POST", "/api/service", { on }),
  /** Settings → "Restart Gerdr": can = something brings it back (service / App), busy = what a restart cuts off */
  restartInfo: () => req<{ can: boolean; busy: Busy }>("GET", "/api/restart"),
  restart: () => req("POST", "/api/restart", {}),
  stepUpRequest: () => req<{ id: string; code: string; expires: string }>("POST", "/api/remote/stepup/request", {}),
  stepUpState: (id: string) => req<{ state: "pending" | "approved" | "expired" }>("GET", `/api/remote/stepup/request/${encodeURIComponent(id)}`),
  stepUpPending: () => req<{ requests: StepUpRequest[] }>("GET", "/api/remote/stepup/pending"),
  stepUpDecide: (id: string, ok: boolean) => req("POST", `/api/remote/stepup/approve/${encodeURIComponent(id)}`, { ok }),
  remoteCert: () => req<RemoteCert>("GET", "/api/remote/cert"),
  remoteSaveCert: (c: RemoteCertInput) => req("POST", "/api/remote/cert", c),
  remoteCheckCAA: () => req<{ missing: boolean }>("GET", "/api/remote/cert/caa"),
  remoteAddCAA: () => req<{ missing: boolean }>("POST", "/api/remote/cert/caa"),
  remoteCheckHost: () => req<HostRecord>("GET", "/api/remote/dns/host"),
  remoteFixHost: () => req<HostRecord>("POST", "/api/remote/dns/host"),
  /** 连上 / 移除远程访问：本机专用，当场生效不用重启 */
  remoteConnect: (relayURL: string, host: string, invite: string) => req("POST", "/api/remote/connect", { relayURL, host, invite }),
  /** Direct access instead of the relay (config.Remote.Direct): urlPort is the port in the address, 0 = the LAN port */
  remoteConnectDirect: (host: string, urlPort: number) => req("POST", "/api/remote/connect", { direct: true, host, urlPort }),
  remoteDisconnect: () => req<{ revoked: number; passkeys: number }>("POST", "/api/remote/disconnect", {}),
  remoteLogout: () => req("POST", "/api/remote/logout", {}),
  // 批准安卓 App 的登录（passkey 的退路，device flow：App 显示码，这边手输）
  cliPeek: (code: string) => req<CliRequest>("POST", "/api/remote/cli/peek", { code }),
  cliApprove: (code: string) => req<CliRequest>("POST", "/api/remote/cli/approve", { code }),
  cliDeny: (code: string) => req("POST", "/api/remote/cli/deny", { code }),
  /** passkey ceremony 的原始两步，只给 lib/passkey.ts 用 */
  passkeyStep: <T>(action: "register" | "login", step: "begin" | "finish", body?: unknown, ceremony?: string) =>
    req<T>("POST", `/api/remote/passkey/${action}/${step}${ceremony ? `?c=${encodeURIComponent(ceremony)}` : ""}`, body ?? {}),
};

export type RemoteStatus = {
  enabled: boolean;
  /** 中继地址 / 主机名是环境变量给的：设置里连接 / 移除改不动 */
  envLocked?: boolean;
  host?: string;
  url?: string;
  relayURL?: string;
  state?: string;
  err?: string;
  certExpires?: string;
  ttlDays: number;
  reauthHours: number;
  /** 配对码进来的设备活几分钟（0 = 和别的设备一样） */
  pairMinutes: number;
  locked: boolean;
  /** 局域网直连口（安卓 App 在家时走它）：在听的端口、起不来的原因、报给 App 的候选地址 */
  lanPort?: number;
  lanErr?: string;
  lanOrigins?: string[];
  /** Direct access is the way in; this Mac's Tailscale addresses (an A-record hint); the LAN port as configured */
  direct?: boolean;
  tailnetAddrs?: string[];
  directPort?: number;
};
/** The UI directory's state (webui.Status): a newer gerdr UI the upgrade skipped over a conflict with local edits. */
export type UIStatus = {
  dir: string;
  git?: { merging?: boolean; conflicts?: string[]; pending_upstream?: boolean; pending_version?: string; pending_conflicts?: string[] };
};
// 后台常驻（launchd）：why 非空 = 这里装不了（不是 mac、调试实例……）；self = 现在答话的就是那个后台服务。
export type Busy = { version: string; turns: number; waiting: number; tasks: number; terms: number };
export type ServiceStatus = { why: string; installed: boolean; pid: number; self: boolean; log: string };
export type StepUpRequest = { id: string; deviceId: string; label: string; code: string; expires: string };
export type DNSProvider = { name: string; label: string; help: string; fields: { env: string; label: string; secret: boolean }[] };
export type RemoteCert = {
  dns: string;
  email: string;
  staging: boolean;
  tlsCert: string;
  tlsKey: string;
  set: string[];
  providers: DNSProvider[];
  accountURI: string;
  caaAuto: boolean;
};
/** The host's A record against the relay's IP (remote.HostRecord). auto: the DNS provider can write it. */
export type HostRecord = { want: string; current: string[] | null; missing: boolean; auto: boolean };
export type RemoteCertInput = { dns: string; email: string; staging: boolean; env: Record<string, string>; tlsCert: string; tlsKey: string };
/** temp：配对码进来的临时设备，expires 定死、不续期；在那台设备上过一次 passkey 就转正 */
export type RemoteDevice = {
  id: string;
  label: string;
  created: string;
  lastSeen: string;
  lastIp: string;
  expires: string;
  temp?: boolean;
  /** Another Gerdr driving this one: never asked to re-verify */
  peer?: boolean;
  /** The phone App's key for re-verifying with Face ID / a fingerprint (server auth/appkey.go) */
  appKey?: { added: string } | null;
};
export type PasskeyInfo = { id: string; label: string; created: string; lastUsed: string };

export type MCPAuth = {
  name: string;
  url: string;
  from: "gerdr" | "claude";
  state: "ok" | "needs_auth" | "error";
  // which credentials got in: none needed / the config's own header / Claude Code's login / gerdr's own login
  auth?: "none" | "headers" | "claude" | "gerdr";
  error?: string;
  pending?: boolean;
  hasOwn?: boolean;
  checkedAt: number;
};

/** host: the computer the file is on (set by api.fileStat for another computer's file); unset = this one */
export type FileStat = { path: string; name: string; dir: boolean; size: number; mtime: number; mime: string; host?: string };

/** One record of an artifacts folder (internal/artifacts Item). Never deleted: superseded ones are greyed. */
export type ArtifactItem = {
  id: string;
  kind: "link" | "path" | "text" | "file";
  label?: string;
  url?: string;
  /** path: absolute; file: relative to the folder */
  path?: string;
  text?: string;
  at: number;
  session?: string;
  supersedes?: string;
  superseded?: boolean;
};
export type ArtifactsInfo = { dir: string; exists: boolean; id?: string; items: ArtifactItem[]; root: string };
export type FsEntry = { name: string; dir: boolean; size: number; mtime: number; mime?: string };

/** The computer an id ("<host>:<id>": a session or terminal there) belongs to; undefined = this one (docs/dev/MULTIHOST.md). */
/** A sign-in started from the page (agents/claude/login.go LoginState); state "" = none started */
export type LoginState = { state: "" | "running" | "done" | "failed"; url?: string; error?: string; output?: string };

export const hostOfId = (id?: string) => (id && id.includes(":") ? id.slice(0, id.indexOf(":")) : undefined);

/** ?host= (or &host=) for a request meant for another computer (server/hosts.go proxies it); "" for this one. */
const hostQ = (host?: string, sep = "?") => (host ? `${sep}host=${encodeURIComponent(host)}` : "");

/** 文件本身的地址（img / video 的 src 直接用）。path 已经是 fileStat 解析过的绝对路径。host: another computer's file */
export const fileRawURL = (path: string, download = false, host?: string) =>
  `${host ? `/h/${encodeURIComponent(host)}` : ""}/api/fs/raw?path=${encodeURIComponent(path)}${download ? "&download=1" : ""}`;

// 这个浏览器标签页的 id：布局 / 配置广播回来时认出「是我自己改的」，不重复应用。
export const clientId = Math.random().toString(36).slice(2);
