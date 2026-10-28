import type { ContentBlock, ExtSettings, ExtView, Hello, PluginBuild, SessionSummary } from "./types";

export class ApiError extends Error {
  status: number;
  /** 远程那条路上的细分原因：reason="stepup"（再加 passkey 前先验一次）、need="pair" | "passkey"（见 docs/dev/REMOTE.md） */
  reason?: string;
  need?: string;
  constructor(status: number, message: string, data?: { reason?: string; need?: string }) {
    super(message);
    this.status = status;
    this.reason = data?.reason;
    this.need = data?.need;
  }
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
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || res.statusText, typeof data === "object" ? data : undefined);
  return data as T;
}

// 网络层的失败（断网、服务端正在重启）：fetch 直接抛 TypeError；反代 / 系统代理会给 502–504。
// 这些值得重试；4xx 和业务错误不重试。
export function isNetworkError(e: unknown) {
  return e instanceof TypeError || (e instanceof ApiError && (e.status === 502 || e.status === 503 || e.status === 504));
}

export function uuid(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return "10000000-1000-4000-8000-100000000000".replace(/[018]/g, (c) => (+c ^ (Math.random() * 16) >> (+c / 4)).toString(16));
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * 发消息：网络不好时自动重发（最多一分钟左右）。每次带同一个 id，服务端认得出「已经收到过了，
 * 只是响应丢了」，不会变成两条。onRetry 给界面说一声「在重试」。
 */
async function sendWithRetry(id: string, content: ContentBlock[], onRetry?: (attempt: number) => void) {
  const msgId = uuid();
  const waits = [500, 1000, 2000, 4000, 8000, 15000, 30000];
  for (let i = 0; ; i++) {
    try {
      return await req<{ messageId: string; steered: boolean }>("POST", `/api/sessions/${id}/send`, { content, id: msgId });
    } catch (e) {
      if (!isNetworkError(e) || i >= waits.length) throw e;
      onRetry?.(i + 1);
      await sleep(waits[i]);
    }
  }
}

export type HerdrPane = { id: string; agent?: string; sessionId?: string; cwd: string; title?: string; existing?: string; skip?: string };
export type HerdrWorkspace = { id: string; label: string; tabs: { label: string; panes: HerdrPane[] }[] };

export const api = {
  stats: (webpid?: number) => req<any>("GET", `/api/system/stats${webpid ? `?webpid=${webpid}` : ""}`),
  herdrScan: () => req<{ workspaces: HerdrWorkspace[] }>("GET", "/api/import/herdr"),
  herdrImport: (workspaces: string[]) =>
    req<{ workspaces: { label: string; tabs: { label: string; tree: import("./actions").ImportNode }[] }[]; imported: number; reused: number; errors?: string[] }>(
      "POST",
      "/api/import/herdr",
      { workspaces },
    ),
  hello: () => req<Hello>("GET", "/api/hello"),
  plugins: () => req<PluginBuild[]>("GET", "/api/plugins"),
  putLayout: (data: unknown, client: string) => req("PUT", "/api/layout", { data, client }),
  putConfig: (data: unknown, client: string) => req("PUT", "/api/config", { data, client }),
  createSession: (o: { agent: string; cwd: string; mode?: string; config?: Record<string, string>; import?: string; title?: string }) =>
    req<SessionSummary>("POST", "/api/sessions", o),
  deleteSession: (id: string) => req("DELETE", `/api/sessions/${id}`),
  send: sendWithRetry,
  cancel: (id: string) => req("POST", `/api/sessions/${id}/cancel`, {}),
  searchFiles: (id: string, q: string) => req<{ files: string[] }>("GET", `/api/sessions/${id}/files?q=${encodeURIComponent(q)}`),
  warm: (id: string) => req("POST", `/api/sessions/${id}/warm`, {}),
  withdrawLast: (id: string) => req<{ content: ContentBlock[]; withdrawn?: number }>("POST", `/api/sessions/${id}/withdraw-last`, {}),
  escape: (id: string) => req<{ content: ContentBlock[]; rolledBack?: boolean; withdrawn?: number }>("POST", `/api/sessions/${id}/escape`, {}),
  answer: (id: string, reqId: string, response: unknown) => req("POST", `/api/sessions/${id}/answer`, { reqId, response }),
  setMode: (id: string, mode: string) => req("POST", `/api/sessions/${id}/mode`, { mode }),
  setConfig: (id: string, cid: string, value: string) => req("POST", `/api/sessions/${id}/config`, { id: cid, value }),
  rename: (id: string, title: string) => req("POST", `/api/sessions/${id}/rename`, { title }),
  rewind: (id: string, messageId: string, restoreFiles: boolean) =>
    req<{ content: ContentBlock[]; restoredFrom?: string; safetyCommit?: string }>("POST", `/api/sessions/${id}/rewind`, { messageId, restoreFiles }),
  rewindPreview: (id: string, messageId: string) =>
    req<{ files: string[] | null }>("GET", `/api/sessions/${id}/rewind-preview?messageId=${encodeURIComponent(messageId)}`),
  importable: (agent: string, cwd: string) =>
    req<{ sessionId: string; cwd: string; title?: string; updatedAt?: string }[]>(
      "GET",
      `/api/importable?agent=${encodeURIComponent(agent)}&cwd=${encodeURIComponent(cwd)}`,
    ),
  stopTask: (id: string, taskId: string) => req("POST", `/api/sessions/${id}/tasks/${encodeURIComponent(taskId)}/stop`, {}),
  backgroundTool: (id: string, toolCallId: string) =>
    req<{ backgrounded: boolean }>("POST", `/api/sessions/${id}/tools/${encodeURIComponent(toolCallId)}/background`, {}),
  allowRules: (id: string, rules: string[]) => req("POST", `/api/sessions/${id}/allow-rules`, { rules }),
  ext: () => req<ExtView>("GET", "/api/ext"),
  putExt: (settings: ExtSettings) => req<ExtView>("PUT", "/api/ext/settings", settings),
  readSkill: (name: string) => req<{ content: string }>("GET", `/api/ext/skills/${encodeURIComponent(name)}`),
  writeSkill: (name: string, content: string) => req<ExtView>("PUT", `/api/ext/skills/${encodeURIComponent(name)}`, { content }),
  deleteSkill: (name: string) => req<ExtView>("DELETE", `/api/ext/skills/${encodeURIComponent(name)}`),
  importSkill: (dir: string) => req<ExtView>("POST", "/api/ext/skills/import", { dir }),
  dirs: (path: string) =>
    req<{ path: string; parent: string; dirs: string[]; home: string; git: boolean }>("GET", `/api/fs/dirs?path=${encodeURIComponent(path)}`),
  fileStat: (path: string, cwd?: string) => req<FileStat>("GET", `/api/fs/stat?path=${encodeURIComponent(path)}&cwd=${encodeURIComponent(cwd ?? "")}`),

  // 远程访问（设置 →「远程访问」；passkey 的两段 ceremony 在 lib/passkey.ts）
  remoteStatus: () => req<{ status: RemoteStatus; remote: boolean }>("GET", "/api/remote/status"),
  remoteWhoami: () => req<{ remote: boolean; authed: boolean; need?: string; deviceId?: string; passkeys: number; passkeyAvailable: boolean }>("GET", "/api/remote/whoami"),
  /** 出配对码：只有本机口能调（远程那一侧服务端直接 403） */
  remotePairCode: () => req<{ code: string; expires: string; url: string; qr?: string }>("POST", "/api/remote/pair-code", {}),
  remoteDevices: () => req<{ devices: RemoteDevice[]; me: string }>("GET", "/api/remote/devices"),
  remoteRevoke: (id: string) => req<{ revoked: number; label: string }>("DELETE", `/api/remote/devices/${encodeURIComponent(id)}`),
  /** 全部踢掉：设备 + passkey 一起（只清设备的话，留着的 passkey 能立刻换回一份新凭据） */
  remoteRevokeAll: () => req<{ revoked: number; passkeys: number }>("DELETE", "/api/remote/devices"),
  remotePasskeys: () => req<{ passkeys: PasskeyInfo[]; available: boolean; url: string }>("GET", "/api/remote/passkeys"),
  remoteDeletePasskey: (id: string) => req("DELETE", `/api/remote/passkeys/${encodeURIComponent(id)}`),
  remoteUnlock: () => req("POST", "/api/remote/unlock", {}),
  remoteLogout: () => req("POST", "/api/remote/logout", {}),
  /** passkey ceremony 的原始两步，只给 lib/passkey.ts 用 */
  passkeyStep: <T>(action: "register" | "login", step: "begin" | "finish", body?: unknown, ceremony?: string) =>
    req<T>("POST", `/api/remote/passkey/${action}/${step}${ceremony ? `?c=${encodeURIComponent(ceremony)}` : ""}`, body ?? {}),
};

export type RemoteStatus = {
  enabled: boolean;
  host?: string;
  url?: string;
  relayURL?: string;
  state?: string;
  err?: string;
  certExpires?: string;
  ttlDays: number;
  reauthHours: number;
  locked: boolean;
};
export type RemoteDevice = { id: string; label: string; created: string; lastSeen: string; lastIp: string; expires: string };
export type PasskeyInfo = { id: string; label: string; created: string; lastUsed: string };

export type FileStat = { path: string; name: string; dir: boolean; size: number; mtime: number; mime: string };

/** 文件本身的地址（img / video 的 src 直接用）。path 已经是 fileStat 解析过的绝对路径。 */
export const fileRawURL = (path: string, download = false) => `/api/fs/raw?path=${encodeURIComponent(path)}${download ? "&download=1" : ""}`;

// 这个浏览器标签页的 id：布局 / 配置广播回来时认出「是我自己改的」，不重复应用。
export const clientId = Math.random().toString(36).slice(2);
