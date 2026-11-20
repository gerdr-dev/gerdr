// Talking to the Gerdr on this Mac (127.0.0.1:7870) and keeping what the panes show: the session list, the
// transcripts of the sessions on screen, the option lists (model / mode / effort). One /ws for everything, as the
// web UI does (ui/src/lib/sync.ts).
import { signal, batch, platform } from "nsh";
import type { Signal } from "nsh";
import { connect } from "websocket";
import { readFile } from "fs/promises";
import { homedir } from "os";

export const BASE = "127.0.0.1:7870";

export type Session = {
  id: string;
  title?: string;
  cwd: string;
  agent: string;
  status: "idle" | "running" | "waiting" | "error" | "starting";
  updatedAt: number;
  optsKey?: string;
  modeId?: string;
  optValues?: Record<string, string>;
  project?: string;
  branch?: string;
  context?: { tokens: number; window: number };
  turn?: { startedAt: number; outputTokens: number };
};

export type Part =
  | { kind: "user"; key: string; text: string; images: string[]; ts: number; steer?: boolean }
  | { kind: "stat"; key: string; ms: number; tokens: number; ts: number }
  | { kind: "text"; key: string; text: string }
  | { kind: "thought"; key: string; text: string }
  | { kind: "tool"; key: string; title: string; status: string; toolKind?: string; seen: number; ended?: number };

export type ConfigOpt = { id: string; name: string; category?: string; options: { value: string; name: string }[] };
export type Options = { modes: { id: string; name: string }[]; config: ConfigOpt[] };

export const token = signal("");
export const conn = signal("connecting");
export const sessions = signal<Session[]>([]);
/** Finished turns nobody has looked at yet (the server's set, pushed whole as "unread") */
export const unread = signal<Record<string, true>>({});
const transcripts = new Map<string, Signal<Part[]>>();
const optionSets = new Map<string, Signal<Options | null>>();

export async function api(method: string, path: string, body?: unknown) {
  const r = await fetch(`http://${BASE}${path}`, {
    method,
    headers: { Authorization: `Bearer ${token()}`, "X-Gerdr": "1", "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status}`);
  const t = await r.text();
  return t ? JSON.parse(t) : null;
}

export function transcript(id: string): Signal<Part[]> {
  let s = transcripts.get(id);
  if (!s) transcripts.set(id, (s = signal<Part[]>([])));
  return s;
}

export function options(key: string | undefined): Options | null {
  if (!key) return null;
  let s = optionSets.get(key);
  if (!s) {
    optionSets.set(key, (s = signal<Options | null>(null)));
    const sig = s;
    api("GET", `/api/options/${key}`).then((o) => {
      const flat = (opts: any[]): { value: string; name: string }[] =>
        opts.flatMap((o) => (o.options ? flat(o.options) : [{ value: o.value, name: o.name }]));
      sig.set({ modes: o.modes ?? [], config: (o.config ?? []).map((c: any) => ({ ...c, options: flat(c.options) })) });
    });
  }
  return s();
}

// ---- transcript: the hub's event log, cut down to user / text / thought / tool rows ----

const chunkAt = new Map<string, Map<string, number>>();
const toolAt = new Map<string, Map<string, number>>();

async function blobURL(path: string): Promise<string> {
  const r = await fetch(`http://${BASE}${path}`, { headers: { Authorization: `Bearer ${token()}` } });
  const b = new Uint8Array(await r.arrayBuffer());
  const url = `data:${r.headers.get("content-type") ?? "image/png"};base64,${Buffer.from(b).toString("base64")}`;
  return url;
}

function apply(id: string, evs: any[], reset: boolean) {
  const sig = transcript(id);
  const list = reset ? [] : sig().slice();
  if (reset || !chunkAt.has(id)) (chunkAt.set(id, new Map()), toolAt.set(id, new Map()));
  const chunks = chunkAt.get(id)!;
  const tools = toolAt.get(id)!;
  let last = "";
  for (const e of evs) {
    if (e.kind === "user") {
      const content = e.content ?? [];
      const text = content
        .filter((c: any) => c.type === "text")
        .map((c: any) => c.text)
        .join("\n");
      const imgs = content.filter((c: any) => c.type === "image");
      const part: Part = { kind: "user", key: `u${e.seq}`, ts: e.ts, steer: !!e.steer, text, images: imgs.map((c: any) => (c.data ? `data:${c.mimeType};base64,${c.data}` : "")) };
      list.push(part);
      const at = list.length - 1;
      imgs.forEach((c: any, i: number) => {
        const path = c.data ? null : c.uri?.startsWith("/") ? c.uri : c.blob ? (c.blob.startsWith("/") ? c.blob : `/api/blobs/${c.blob}`) : null;
        if (!path) return;
        blobURL(path).then((url) => {
          const cur = sig().slice();
          const p = cur[at];
          if (p?.kind !== "user") return;
          const images = p.images.slice();
          images[i] = url;
          cur[at] = { ...p, images };
          sig.set(cur);
        });
      });
      last = "";
    } else if (e.kind === "turn_end") {
      // Claude Code's line under a finished turn: "用时 1m 5s · ↓ 2.2k tokens"
      if (!e.error && e.durationMs > 1000) list.push({ kind: "stat", key: `s${e.seq}`, ms: e.durationMs, tokens: e.outputTokens ?? 0, ts: e.ts });
      for (let i = 0; i < list.length; i++) {
        const p = list[i];
        if (p.kind === "tool" && !p.ended) list[i] = { ...p, ended: e.ts, status: p.status === "failed" ? "failed" : "completed" };
      }
      last = "";
    } else if (e.kind === "update") {
      const u = e.update ?? {};
      const su = u.sessionUpdate;
      if ((su === "agent_message_chunk" || su === "agent_thought_chunk") && u.content?.type === "text") {
        const kind = su === "agent_message_chunk" ? "text" : "thought";
        const k = `${kind}:${u.messageId ?? ""}`;
        const i = chunks.get(k);
        if (i !== undefined && (u.messageId || last === k) && list[i]) {
          const p = list[i] as any;
          list[i] = { ...p, text: p.text + u.content.text };
        } else {
          list.push({ kind, key: `${kind[0]}${e.seq}`, text: u.content.text });
          chunks.set(k, list.length - 1);
        }
        last = k;
      } else if (su === "tool_call") {
        list.push({ kind: "tool", key: `c${e.seq}`, title: u.title ?? u.kind ?? "tool", status: u.status ?? "pending", toolKind: u.kind, seen: e.ts ?? Date.now() });
        tools.set(u.toolCallId, list.length - 1);
        last = "";
      } else if (su === "tool_call_update") {
        const i = tools.get(u.toolCallId);
        if (i !== undefined && list[i]?.kind === "tool") {
          const p = list[i] as any;
          const status = u.status ?? p.status;
          list[i] = { ...p, status, title: u.title ?? p.title, ended: status === "completed" || status === "failed" ? (e.ts ?? Date.now()) : p.ended };
        }
      }
    }
  }
  sig.set(list);
}

// ---- the socket ----

let sock: any = null;
let watched: string[] = [];

export function watch(ids: string[]) {
  const next = [...new Set(ids)].sort();
  if (next.join() === watched.join()) return;
  const fresh = next.filter((id) => !watched.includes(id));
  watched = next;
  for (const id of fresh) transcript(id).set([]);
  sock?.write(JSON.stringify({ t: "watch", ids: next, since: {} })).catch?.(() => {});
}

function upsert(s: Session) {
  const list = sessions();
  const i = list.findIndex((x) => x.id === s.id);
  const next = i < 0 ? [s, ...list] : list.map((x) => (x.id === s.id ? { ...x, ...s } : x));
  sessions.set(next);
}

async function run() {
  conn.set("connecting");
  sock = await connect(`ws://${BASE}/ws`, { headers: { Authorization: `Bearer ${token()}` } });
  conn.set("connected");
  const mine = sock;
  // The server drops a connection it hears nothing from (server/ws.go readWait): ping as the web UI does
  const beat = setInterval(() => (sock === mine ? mine.write(JSON.stringify({ t: "ping" })).catch(() => {}) : clearInterval(beat)), 15000);
  if (watched.length) sock.write(JSON.stringify({ t: "watch", ids: watched, since: {} }));
  for (let raw; (raw = await sock.read()) !== null; ) {
    let msgs: any;
    const size = typeof raw === "string" ? raw.length : raw.byteLength;
    if (size > 500_000) console.log(`ws frame ${(size / 1e6).toFixed(1)} MB`);
    try {
      msgs = JSON.parse(typeof raw === "string" ? raw : new TextDecoder().decode(raw));
    } catch {
      continue;
    }
    if (!Array.isArray(msgs)) msgs = [msgs];
    batch(() => {
      for (const m of msgs) {
        if (m.t === "events" && watched.includes(m.id)) {
          const evs = m.events ?? [];
          if (m.reset) {
            raws.set(m.id, [...evs]);
            olderFlag(m.id).set(!!m.older);
          } else raws.get(m.id)?.push(...evs);
          apply(m.id, evs, !!m.reset);
        }
        else if (m.t === "session" && m.session) upsert(m.session);
        else if (m.t === "session_removed") sessions.set(sessions().filter((s) => s.id !== m.id));
        else if (m.t === "unread") {
          const u: Record<string, true> = {};
          for (const [k, v] of Object.entries(m.data?.data ?? {})) if (v) u[k] = true;
          unread.set(u);
        }
      }
    });
  }
  console.log(`ws closed ${sock.closeCode} ${sock.closeReason}`);
  conn.set(`closed ${sock.closeCode ?? ""}`);
  sock = null;
  setTimeout(loop, 1000);
}

function loop() {
  run().catch((e) => {
    console.log(`ws error: ${e?.code ?? ""} ${e?.message ?? e}`);
    conn.set("reconnecting");
    sock = null;
    setTimeout(loop, 2000);
  });
}

let started = false;
export async function start() {
  // Once per VM: a hot update renders App again, and a second socket loop would fight the first over one socket
  if (started) return;
  started = true;
  // On the Mac the token file is right there; a phone gets it over USB from tools/token-helper.py (adb reverse)
  if (platform.os === "android" || platform.os === "ios") token.set((await (await fetch("http://127.0.0.1:7899/token")).text()).trim());
  else token.set((await readFile(`${homedir()}/.gerdr/token`, "utf8")).trim());
  const list: Session[] = await api("GET", "/api/sessions");
  sessions.set(list);
  loop();
}

// ---- older history ----

// Opening a session, /ws sends only its last stretch (~256 KB, from a turn's start: hub/blob.go TailBudget), with
// `older` when there is more; the rest comes a stretch at a time from /events/older as the reader nears the top.
// Without it a session showed nothing before its last /compact (reported).
const raws = new Map<string, any[]>();
const olders = new Map<string, Signal<boolean>>();
const olderFlag = (id: string) => {
  let s = olders.get(id);
  if (!s) olders.set(id, (s = signal(false)));
  return s;
};
/** More history before what is shown */
export const hasOlder = (id: string) => olderFlag(id)();
const loadingOlder = new Set<string>();
/** Fetches the stretch before the first event shown and replays the whole log (rows keep their keys: seqs). */
export async function loadOlder(id: string) {
  const raw = raws.get(id);
  if (!raw?.length || !olderFlag(id)() || loadingOlder.has(id)) return;
  loadingOlder.add(id);
  try {
    const r = await api("GET", `/api/sessions/${id}/events/older?before=${raw[0].seq}`);
    const now = raws.get(id);
    if (now !== raw) return; // reset meanwhile
    raw.unshift(...(r.events ?? []));
    olderFlag(id).set(!!r.older);
    apply(id, raw, true);
  } finally {
    loadingOlder.delete(id);
  }
}

// ---- actions ----

export function send(id: string, text: string, images: { data: string; mimeType: string }[]) {
  const content: any[] = images.map((i) => ({ type: "image", data: i.data, mimeType: i.mimeType }));
  if (text) content.push({ type: "text", text });
  return api("POST", `/api/sessions/${id}/send`, { content, id: `nsh-${Date.now()}-${Math.random().toString(36).slice(2, 8)}` });
}

export const setConfig = (id: string, cid: string, value: string) => api("POST", `/api/sessions/${id}/config`, { id: cid, value });
export const setMode = (id: string, mode: string) => api("POST", `/api/sessions/${id}/mode`, { mode });
export const cancel = (id: string) => api("POST", `/api/sessions/${id}/cancel`, {});
