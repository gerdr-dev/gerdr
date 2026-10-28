// 急救面板用到的后端接口和数据。只写用得到的字段 —— 完整的类型在 ui/src/lib/types.ts，这里故意不 import 那边：
// 界面目录是会被改的，急救面板不能和它有任何牵连。

export type Agent = { id: string; name: string; available: boolean };

export type Session = {
  id: string;
  agent: string;
  cwd: string;
  title?: string;
  status: "starting" | "idle" | "running" | "waiting" | "error" | string;
  error?: string;
  updatedAt: number;
};

export type Hello = {
  agents: Agent[];
  sessions: Session[];
  config?: { theme?: "system" | "light" | "dark"; defaultAgent?: string; defaultModes?: Record<string, string> };
};

export type UIStatus = {
  dir: string;
  build_error?: string;
  runtime_errors?: { source?: string; message: string; detail?: string }[];
  git?: { merging?: boolean; conflicts?: string[]; uncommitted_files?: number };
};

export type Commit = { sha: string; time: number; subject: string; upstream?: boolean; current?: boolean };

// 会话日志里的一条（hub 的 Event，只列这里画得出来的几种）
export type Ev = {
  seq: number;
  kind: string;
  content?: { type: string; text?: string; name?: string }[];
  update?: {
    sessionUpdate: string;
    content?: { type: string; text?: string };
    toolCallId?: string;
    title?: string;
    kind?: string;
    status?: string;
  };
  reqId?: string;
  request?: any;
  response?: any;
  error?: string;
  level?: string;
  text?: string;
};

export async function req<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: body !== undefined ? { "Content-Type": "application/json", "X-Gerdr": "1" } : { "X-Gerdr": "1" },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let data: any;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {}
  if (!res.ok) throw new Error(data?.error || `${res.status} ${res.statusText}`);
  return data as T;
}

export const uuid = () => (crypto.randomUUID ? crypto.randomUUID() : String(Date.now()) + Math.random().toString(16).slice(2));

export function ago(sec: number) {
  const m = Math.floor((Date.now() / 1000 - sec) / 60);
  if (m < 1) return "刚刚";
  if (m < 60) return `${m} 分钟前`;
  const h = Math.floor(m / 60);
  return h < 24 ? `${h} 小时前` : `${Math.floor(h / 24)} 天前`;
}

// 现在报的错拼成一段文字：左边显示、新开对话时带给 agent
// 运行时错误两份合起来：服务端那份（各个页面报上去的、只算当前这一版）+ 这个页面自己记的（右下角「急救」冒不冒看的就是它）。
// 只看服务端的话，没报上去 / 被当成旧版丢掉的那几条，按钮冒了、面板里却说没报错。
export function errorText(st: UIStatus | null) {
  if (!st) return "";
  const parts: string[] = [];
  if (st.build_error) parts.push("编译错误：\n" + st.build_error);
  const local: { source?: string; message: string; detail?: string }[] = (window as any).__GERDR_UI_ERRORS__ || [];
  const seen = new Set<string>();
  for (const e of [...(st.runtime_errors || []), ...local.filter((e) => e.source !== "build")]) {
    if (seen.has(e.message)) continue;
    seen.add(e.message);
    parts.push(`运行时错误（${e.source || ""}）：${e.message}${e.detail ? "\n" + e.detail : ""}`);
  }
  if (st.git?.merging) parts.push("界面目录正合并到一半，有冲突：" + (st.git.conflicts || []).join(", "));
  return parts.join("\n\n").slice(0, 6000);
}
