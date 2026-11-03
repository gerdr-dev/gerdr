import { api, uuid } from "../api";
import { Transcript } from "../transcript";
import type { ContentBlock, HubEvent } from "../types";
import { get, set } from "../store";

export const transcripts = new Map<string, Transcript>();

export function transcript(id: string): Transcript {
  let t = transcripts.get(id);
  if (!t) {
    t = new Transcript(undefined, id);
    transcripts.set(id, t);
  }
  return t;
}

// ---- 会话日志 ----

// 子 agent 视图是从同一份日志另读一遍的，所以留着原始事件（新开一个子 agent 面板时从头重放）。
const rawEvents = new Map<string, HubEvent[]>();
const subTranscripts = new Map<string, Transcript>();

export function subTranscript(id: string, sub: string): Transcript {
  const k = `${id}#${sub}`;
  let t = subTranscripts.get(k);
  if (!t) {
    t = new Transcript(sub, id);
    t.reset(rawEvents.get(id) ?? []);
    subTranscripts.set(k, t);
  }
  return t;
}

export function applyEvents(id: string, events: HubEvent[], reset: boolean, older = false) {
  const t = transcript(id);
  if (reset) {
    t.reset(events);
    rawEvents.set(id, [...events]);
    setOlder(id, older ? "more" : undefined);
  } else {
    const fresh = events.filter((e) => e.seq > t.lastSeq);
    for (const e of fresh) t.apply(e);
    const raw = rawEvents.get(id) ?? [];
    raw.push(...fresh);
    rawEvents.set(id, raw);
    events = fresh;
  }
  for (const [k, st] of subTranscripts) {
    if (!k.startsWith(id + "#")) continue;
    if (reset) st.reset(events);
    else for (const e of events) st.apply(e);
  }
  set((s) => ({ tv: { ...s.tv, [id]: (s.tv[id] ?? 0) + 1 } }));
}

/**
 * /replay: play the last turn of a chat back as if the agent were writing it now, in this window only (no agent, no
 * server). Asked for to try how the chat scrolls while output streams in without waiting on a model: cut the log at the
 * user message of the latest turn with some length to it, feed the rest back a few characters at a time (about 150 a
 * second), and put the full log back at the end. Stops
 * early (and restores) when anything real arrives meanwhile. Returns false when there's nothing to replay.
 */
const replays = new Map<string, () => void>();
export function replayLastTurn(id: string, opts: { chars?: number; every?: number; at?: string } = {}): boolean {
  replays.get(id)?.();
  const raw = rawEvents.get(id);
  if (!raw) return false;
  const isUser = (e: HubEvent) => e.kind === "user" || (e.kind === "update" && (e.update as any)?.sessionUpdate === "user_message_chunk");
  const textOf = (e: HubEvent): string | undefined => {
    const u = e.kind === "update" ? (e.update as any) : undefined;
    return (u?.sessionUpdate === "agent_message_chunk" || u?.sessionUpdate === "agent_thought_chunk") && u.content?.type === "text"
      ? u.content.text
      : undefined;
  };
  // The latest turn with a screen or so of text (a two-line answer is over before you can scroll), else the last one
  let cut = -1;
  let n = 0;
  for (let i = raw.length - 1; i >= 0; i--) {
    if (!isUser(raw[i])) {
      n += textOf(raw[i])?.length ?? 0;
      continue;
    }
    if (cut < 0 && i < raw.length - 1) cut = i;
    if (n >= 400) {
      cut = i;
      break;
    }
    n = 0;
  }
  // A given turn instead (tools/scrolltest): the user message containing `at`
  if (opts.at) cut = raw.findIndex((e) => isUser(e) && JSON.stringify((e as any).content ?? (e as any).update?.content ?? "").includes(opts.at!));
  if (cut < 0) return false;
  const chars = opts.chars ?? 6;
  const pieces: HubEvent[] = [];
  for (const e of raw.slice(cut + 1)) {
    const text = textOf(e);
    if (text === undefined) {
      pieces.push(e);
      continue;
    }
    const u = (e as any).update;
    // Same seq for every piece: message ids come from it, and the transcript's lastSeq never passes the real log's
    for (let k = 0; k < text.length; k += chars) pieces.push({ ...e, update: { ...u, content: { ...u.content, text: text.slice(k, k + chars) } } } as HubEvent);
  }
  const t = transcript(id);
  const bump = () => set((s) => ({ tv: { ...s.tv, [id]: (s.tv[id] ?? 0) + 1 } }));
  const len = raw.length;
  let i = 0;
  const stop = () => {
    clearInterval(timer);
    replays.delete(id);
    // Something real came in (a reset replaced the array, or a live event was added): show the real log
    t.reset(rawEvents.get(id) ?? raw);
    bump();
  };
  const timer = window.setInterval(() => {
    if (rawEvents.get(id) !== raw || raw.length !== len || i >= pieces.length) return stop();
    t.apply(pieces[i++]);
    bump();
  }, opts.every ?? 40);
  replays.set(id, stop);
  t.reset(raw.slice(0, cut + 1));
  bump();
  return true;
}

// Checks the push connection (sync.ts registers it; it imports this file, so not the other way round).
let probeConn: () => Promise<boolean> = async () => true;
export function setConnProbe(f: () => Promise<boolean>) {
  probeConn = f;
}

/**
 * 发一条消息，点下去就先在对话最底下画一条半透明的（见 Transcript.sending），日志里那条到了换掉。
 * 失败就拿掉、错误照旧抛给调用方（它负责把字放回输入框）。
 */
export async function sendShown(id: string, content: ContentBlock[], onRetry?: (attempt: number) => void, now?: boolean) {
  const msgId = uuid();
  const t = transcript(id);
  const bump = () => set((s) => ({ tv: { ...s.tv, [id]: (s.tv[id] ?? 0) + 1 } }));
  t.addSending(msgId, content);
  bump();
  try {
    const r = await api.send(id, content, onRetry, now, msgId);
    // The server logs and pushes the message before answering, so the logged copy normally replaced the bubble
    // already. Still missing a moment later means the push connection is likely dead while looking open: check it
    // (a dead one reconnects, and the full re-push replaces the bubble). Dropping the bubble on a timer instead made
    // a delivered message look lost: seen on a phone over remote, the message and the reply reached the server, the
    // phone showed neither, the bubble vanished after 10s and the user sent it again. Give the bubble up only when
    // the connection answers and the copy still isn't there, so it doesn't hang half transparent for good.
    const pending = () => t.sending.some((m) => m.id === msgId);
    const check = async (tries: number) => {
      if (!pending()) return;
      if ((await probeConn()) || tries <= 0) {
        if (!pending()) return;
        t.dropSending(msgId);
        bump();
        return;
      }
      setTimeout(() => void check(tries - 1), 10_000);
    };
    setTimeout(() => void check(5), 2_000);
    return r;
  } catch (e) {
    t.dropSending(msgId);
    bump();
    throw e;
  }
}

function setOlder(id: string, v: "more" | "loading" | undefined) {
  set((s) => {
    if (s.older[id] === v) return {};
    const older = { ...s.older };
    if (v) older[id] = v;
    else delete older[id];
    return { older };
  });
}

/**
 * 往上翻到顶了：拉更早的一段接在前面，整份重放（Transcript 只会从头往后放；消息 id 按日志序号起，已有的不变）。
 * 打开会话时服务端只推最近一段（hub/blob.go）—— 远程打开一个几十 MB 的会话，整份推过去传不完。
 */
export async function loadOlder(id: string) {
  const raw = rawEvents.get(id);
  if (get().older[id] !== "more" || !raw?.length) return;
  setOlder(id, "loading");
  let next: "more" | undefined = "more";
  try {
    const r = await api.olderEvents(id, raw[0].seq);
    // 等的这会儿整份重推过（回退、重连接不上）：手里换了一份，这次拉到的接不上，丢掉（older 也由那次重推定了）
    if (rawEvents.get(id) !== raw) return;
    next = r.older ? "more" : undefined;
    const all = [...(r.events ?? []), ...raw];
    rawEvents.set(id, all);
    transcript(id).reset(all);
    for (const [k, st] of subTranscripts) if (k.startsWith(id + "#")) st.reset(all);
    set((s) => ({ tv: { ...s.tv, [id]: (s.tv[id] ?? 0) + 1 } }));
  } catch {
    // 网络不好：留着 more，下次翻到顶再试
  } finally {
    if (get().older[id] === "loading") setOlder(id, next);
  }
}

export function removeSession(id: string) {
  transcripts.delete(id);
  rawEvents.delete(id);
  for (const k of subTranscripts.keys()) if (k.startsWith(id + "#")) subTranscripts.delete(k);
  set((st) => {
    const sessions = { ...st.sessions };
    delete sessions[id];
    return { sessions };
  });
}
