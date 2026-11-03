// The bridge between this Work project and gerdr around it.
//
// Don't edit this file: gerdr upgrades it, and an edited copy is left alone from then on, so it misses what later
// versions add (an old one doesn't follow the sidebar's width: the menu stops lining up and can't be resized). Put your
// own helpers in a file of their own.
//
// - run(fn, input, { onProgress, onLog, signal }): call a local function (local/<file>.ts, "file.export"). Logic and data
//   access (commands, APIs, MCP) live there, not in the page: the page only draws. The run streams its progress and
//   console output while it goes; it doesn't stop when the page closes (signal / abort() stop it).
// - functions(): what can be called.
// - ask(text): hand a task to the Work agent on the right of the page (gerdr sends it as a message;
//   it queues if the agent is busy); resolves with the conversation's id. openChat(id) shows that conversation.
// - openSession(id): show a coding-agent conversation a local function started (ctx.sessions.start), in Code.
// - showSession(id, { label, actions }): show such a conversation in a panel over this page instead, with gerdr's own
//   chat and input box (images, files), plus a line and buttons of the page's (onSessionPanel hears the clicks);
//   hideSession() closes it.
// - secret(name, { label, note }): ask the user for a key (a password, a token) in gerdr's own dialog; it's saved for
//   local functions' ctx.secrets.get and never passes through this page. secretNames() lists the saved names.
// - The page follows gerdr's light / dark theme by itself (see the listener at the bottom).
// - The page is part of gerdr's own page (a React root of its own next to gerdr's, not a frame): gerdr's controls are on
//   its strip left of it, nothing is drawn over this page. For gerdr's chat and more, import from "@gerdr/core"
//   (src/gerdr-core.d.ts).

declare const __GERDR_PROJECT__: string | undefined;

/** The project this Work site belongs to (gerdr compiles it in; a site once served at /work/<project>/ read the address). */
export const project = typeof __GERDR_PROJECT__ === "string" ? __GERDR_PROJECT__ : decodeURIComponent(location.pathname.split("/")[2] || "default");

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

let askSeq = 0;
const asking = new Map<number, { resolve: (session: string) => void; reject: (e: Error) => void }>();

/** Hands a task to the Work agent on the right (queued if it's busy); resolves with that conversation's id once sent. */
export function ask(text: string): Promise<string> {
  const id = ++askSeq;
  return new Promise((resolve, reject) => {
    asking.set(id, { resolve, reject });
    window.parent.postMessage({ type: "gerdr:ask", id, text }, location.origin);
  });
}

/** Shows a Work agent conversation (e.g. the one ask() resolved with) on the right; on a phone switches to its tab. */
export function openChat(session: string) {
  window.parent.postMessage({ type: "gerdr:openChat", session }, location.origin);
}

/** Shows a coding-agent conversation a local function started (ctx.sessions.start): switches to Code, at its pane. */
export function openSession(session: string) {
  window.parent.postMessage({ type: "gerdr:openSession", session }, location.origin);
}

/** jump: Gerdr scrolls the chat to the card with that ref (a Work tool's gerdrCard) instead of telling the page. */
export type SessionAction = { id: string; label: string; kind?: "primary" | "danger"; confirm?: string; jump?: string };

/**
 * Shows a conversation (e.g. a worker from ctx.sessions.start) in a panel over this page: the whole chat, and its
 * input box takes images and files. `label` is a line above the chat ("t11 · needs you"); `actions` are buttons next
 * to it (`confirm`: a second click needed, the button showing that text). Calling it again for the shown conversation
 * updates them. Clicks and the panel closing come back through onSessionPanel.
 */
/** A thing waiting on the user, one line under the bar: jump scrolls the chat to that card, action tells the page. */
export type SessionItem = { text: string; jump?: string; action?: string };

export function showSession(session: string, opts: { label?: string; actions?: SessionAction[]; items?: SessionItem[] } = {}) {
  window.parent.postMessage({ type: "gerdr:showSession", session, ...opts }, location.origin);
}

// An Esc nothing on the page used goes to gerdr: it closes the panel showSession opened (a drawer over the page).
// A page's own dialogs that close on Esc should preventDefault it, as React UI libraries do.
window.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && !e.defaultPrevented) window.parent.postMessage({ type: "gerdr:escape" }, location.origin);
});

/** Closes the panel showSession opened. */
export function hideSession() {
  window.parent.postMessage({ type: "gerdr:hideSession" }, location.origin);
}

/** What happens to the panel: a button pressed (`action` = its id), or it closed (`action` undefined). Returns an unsubscribe. */
export function onSessionPanel(fn: (e: { session: string; action?: string }) => void) {
  const f = (e: MessageEvent) => {
    if (e.source !== window.parent) return;
    const m = e.data ?? {};
    if (m.type === "gerdr:sessionAction" && typeof m.action === "string") fn({ session: m.session, action: m.action });
    else if (m.type === "gerdr:sessionHidden") fn({ session: m.session });
  };
  window.addEventListener("message", f);
  return () => window.removeEventListener("message", f);
}

let secretSeq = 0;
const secretAsks = new Map<number, { resolve: (saved: boolean) => void; reject: (e: Error) => void }>();

/**
 * Asks the user for a secret in gerdr's dialog and saves it as `name` (what ctx.secrets.get(name) reads). Resolves
 * true when saved, false when they cancelled. The page never sees the value.
 */
export function secret(name: string, opts: { label?: string; note?: string } = {}): Promise<boolean> {
  const id = ++secretSeq;
  return new Promise((resolve, reject) => {
    secretAsks.set(id, { resolve, reject });
    window.parent.postMessage({ type: "gerdr:secret", id, name, label: opts.label, note: opts.note }, location.origin);
  });
}

/** The names of the saved secrets (never their values). */
export async function secretNames(): Promise<string[]> {
  const res = await fetch("/api/work/secrets", { credentials: "same-origin", headers: { "X-Gerdr": "1" } });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data?.error || res.statusText);
  return data.list ?? [];
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
    // For sites made before gerdr moved its controls onto a strip of its own: their menu is w-(--gerdr-sidebar-w) and
    // leaves --gerdr-menu-bottom free for a settings row gerdr used to lay over it. gerdr now sends a fixed width and 0.
    document.documentElement.style.setProperty("--gerdr-sidebar-w", `${e.data.width}px`);
    if (typeof e.data.bottom === "number") document.documentElement.style.setProperty("--gerdr-menu-bottom", `${e.data.bottom}px`);
  } else if (e.data?.type === "gerdr:secretDone") {
    const p = secretAsks.get(e.data.id);
    if (!p) return;
    secretAsks.delete(e.data.id);
    if (e.data.error) p.reject(new Error(e.data.error));
    else p.resolve(!!e.data.saved);
  } else if (e.data?.type === "gerdr:asked") {
    const p = asking.get(e.data.id);
    if (!p) return;
    asking.delete(e.data.id);
    if (e.data.error) p.reject(new Error(e.data.error));
    else p.resolve(e.data.session);
  }
});
window.parent.postMessage({ type: "gerdr:ready" }, location.origin);
