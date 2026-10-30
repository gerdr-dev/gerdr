// The bridge between this Work project and gerdr around it.
//
// - run(fn, input, { onProgress, onLog, signal }): call a local function (local/<file>.ts, "file.export"). Logic and data
//   access (commands, APIs, MCP) live there, not in the page: the page only draws. The run streams its progress and
//   console output while it goes; it doesn't stop when the page closes (signal / abort() stop it).
// - functions(): what can be called.
// - ask(text): hand a task to the Work agent on the right of the page (gerdr sends it as a message;
//   it queues if the agent is busy).
// - The page follows gerdr's light / dark theme by itself (see the listener at the bottom).
// - The menu's width is --gerdr-sidebar-w, which follows gerdr's sidebar: the user resizes them together by dragging the
//   menu's right edge (gerdr draws that handle over this page). Keep using the variable for the menu width.
//   Below md the page is too narrow for a side menu (layout.tsx shows tabs instead); gerdr then lays nothing over it.

/** The project this Work site belongs to: it's served at /work/<project>/. */
export const project = decodeURIComponent(location.pathname.split("/")[2] || "default");

export type RunEvent = { type: "start" | "progress" | "log" | "result" | "error"; data: any };

export type RunOptions = {
  /** What the function passes to ctx.progress(…), as it happens. */
  onProgress?: (data: any) => void;
  /** console.log / ctx.log lines: { level, message }. */
  onLog?: (line: { level: string; message: string }) => void;
  /** The run's id, as soon as it starts (for abort(id) from elsewhere). */
  onStart?: (id: string) => void;
  /** Aborting it stops the run (closing the page doesn't). */
  signal?: AbortSignal;
};

/** An error thrown by the function: message for people, stack (file and line) for fixing it. */
export class RunError extends Error {
  constructor(
    message: string,
    public stack?: string,
    public runId?: string,
  ) {
    super(message);
  }
}

export async function run<T = any>(fn: string, input?: unknown, opts: RunOptions = {}): Promise<T> {
  const res = await fetch("/api/work/run", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", Accept: "text/event-stream", "X-Gerdr": "1" },
    body: JSON.stringify({ project, fn, input }),
  });
  if (!res.ok || !res.body) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data?.error || res.statusText);
  }
  const id = res.headers.get("X-Gerdr-Run") ?? undefined;
  const stop = () => id && abort(id);
  opts.signal?.addEventListener("abort", stop, { once: true });
  try {
    return await follow<T>(res.body, id, opts);
  } finally {
    opts.signal?.removeEventListener("abort", stop);
  }
}

/** Picks a run up again by id (after a reload), from its first event. */
export async function attach<T = any>(id: string, opts: RunOptions = {}): Promise<T> {
  const res = await fetch(`/api/work/runs/${encodeURIComponent(id)}`, { credentials: "same-origin", headers: { Accept: "text/event-stream", "X-Gerdr": "1" } });
  if (!res.ok || !res.body) throw new Error((await res.json().catch(() => ({})))?.error || res.statusText);
  return follow<T>(res.body, id, opts);
}

/** Stops a run. */
export async function abort(id: string) {
  await fetch(`/api/work/runs/${encodeURIComponent(id)}/abort`, { method: "POST", credentials: "same-origin", headers: { "X-Gerdr": "1" } });
}

/** The callable functions: [{ name: "file.export", file }]. */
export async function functions(): Promise<{ name: string; file: string }[]> {
  const res = await fetch(`/api/work/functions?project=${encodeURIComponent(project)}`, { credentials: "same-origin", headers: { "X-Gerdr": "1" } });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data?.error || res.statusText);
  return data.list;
}

async function follow<T>(body: ReadableStream<Uint8Array>, id: string | undefined, opts: RunOptions): Promise<T> {
  const reader = body.getReader();
  const dec = new TextDecoder();
  let buf = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let i;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const chunk = buf.slice(0, i);
      buf = buf.slice(i + 2);
      if (!chunk.startsWith("data: ")) continue;
      const ev = JSON.parse(chunk.slice(6)) as RunEvent;
      if (ev.type === "start") opts.onStart?.(ev.data.id);
      else if (ev.type === "progress") opts.onProgress?.(ev.data);
      else if (ev.type === "log") opts.onLog?.(ev.data);
      else if (ev.type === "result") return ev.data.value as T;
      else if (ev.type === "error") throw new RunError(ev.data.message, ev.data.stack, id);
    }
  }
  throw new RunError("the connection closed before the run ended (it may still be running)", undefined, id);
}

export function ask(text: string) {
  window.parent.postMessage({ type: "gerdr:ask", text }, location.origin);
}

/** Opens gerdr's settings (e.g. "mcp" to sign in to an MCP server). */
export function openSettings(tab?: string) {
  window.parent.postMessage({ type: "gerdr:settings", tab }, location.origin);
}

window.addEventListener("message", (e) => {
  if (e.origin !== location.origin) return;
  if (e.data?.type === "gerdr:theme") {
    document.documentElement.classList.toggle("dark", !!e.data.dark);
    document.documentElement.style.colorScheme = e.data.dark ? "dark" : "light";
  } else if (e.data?.type === "gerdr:sidebar" && typeof e.data.width === "number") {
    document.documentElement.style.setProperty("--gerdr-sidebar-w", `${e.data.width}px`);
  }
});
window.parent.postMessage({ type: "gerdr:ready" }, location.origin);
