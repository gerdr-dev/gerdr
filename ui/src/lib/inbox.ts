import { create } from "zustand";
import { api, type PendingReq } from "./api";
import { isPhone } from "./phone";
import { get as getApp, useStore, visibleSessions } from "./store";
import type { SessionSummary } from "./types";

/**
 * The inbox: things that need the user, kept until they dismiss them (asked for: items only go away with their ×).
 * Approvals / questions (from GET /api/pending), agents that ended in an error, and the gerdr machine's battery running
 * low. Per device (localStorage), like the unread dots: dismissing on the phone shouldn't clear the desktop's.
 */
export type InboxItem = {
  /** req:<reqId> | err:<session>:<at> | bat:<at> */
  id: string;
  kind: "permission" | "ask" | "error" | "battery";
  at: number;
  sessionId?: string;
  /** Who it's about: the session's name, or the battery level */
  title: string;
  body?: string;
  reqId?: string;
  request?: any;
};

type InboxState = {
  items: InboxItem[];
  /** Something came in since the inbox was last opened: the red dot. */
  fresh: boolean;
  /** reqIds still waiting (last /api/pending): the others are answered, their item shows that and drops its buttons.
   *  null = not fetched yet (don't call everything answered meanwhile). */
  live: Record<string, true> | null;
};

const KEY = "gerdr.inbox";
const MAX = 100;

function load(): Pick<InboxState, "items" | "fresh"> {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? "null");
    if (v && Array.isArray(v.items)) return { items: v.items, fresh: !!v.fresh };
  } catch {}
  return { items: [], fresh: false };
}

export const useInbox = create<InboxState>(() => ({ ...load(), live: null }));
const get = useInbox.getState;

function save() {
  const { items, fresh } = get();
  try {
    localStorage.setItem(KEY, JSON.stringify({ items, fresh }));
  } catch {}
}

// Dismissed requests that may still be pending: without this the next /api/pending would put them straight back
const GONE = "gerdr.inbox.gone";
function gone(): string[] {
  try {
    return JSON.parse(localStorage.getItem(GONE) ?? "[]");
  } catch {
    return [];
  }
}

function add(items: InboxItem[], quiet: boolean) {
  if (!items.length) return;
  useInbox.setState((s) => ({ items: [...items, ...s.items].slice(0, MAX), fresh: s.fresh || !quiet }));
  save();
}

export function dismiss(id: string) {
  const it = get().items.find((x) => x.id === id);
  useInbox.setState((s) => ({ items: s.items.filter((x) => x.id !== id) }));
  if (it?.reqId)
    try {
      localStorage.setItem(GONE, JSON.stringify([it.reqId, ...gone()].slice(0, 300)));
    } catch {}
  save();
}

export function clearAll() {
  for (const it of get().items) dismiss(it.id);
}

/** Opened: the red dot goes. */
export function markSeen() {
  if (!get().fresh) return;
  useInbox.setState({ fresh: false });
  save();
}

export const sessionName = (s: Pick<SessionSummary, "title" | "cwd" | "agent">) => s.title || s.cwd.split("/").pop() || s.agent;

/** On screen and the window has focus: the user already sees it, no need for the red dot (it still goes in the list). */
function seen(id: string) {
  if (document.visibilityState !== "visible" || !document.hasFocus()) return false;
  return visibleSessions(getApp().layout, isPhone()).includes(id);
}

/** Settings → Inbox: the user can keep a kind out (missing = on). Battery has its own switch (batteryAlert). */
const wanted = (kind: "permission" | "ask" | "error") => getApp().config.inbox?.[kind] !== false;

// ---- approvals / questions ----

let pendingTimer: ReturnType<typeof setTimeout> | undefined;
function refreshPending() {
  clearTimeout(pendingTimer);
  pendingTimer = setTimeout(async () => {
    let reqs: PendingReq[];
    try {
      reqs = await api.pending();
    } catch {
      return;
    }
    const have = new Set(
      get()
        .items.map((x) => x.reqId)
        .filter(Boolean),
    );
    const skip = new Set(gone());
    const fresh = reqs.filter((r) => !have.has(r.reqId) && !skip.has(r.reqId) && wanted(r.kind));
    useInbox.setState({ live: Object.fromEntries(reqs.map((r) => [r.reqId, true as const])) });
    add(
      fresh.reverse().map((r) => ({
        id: `req:${r.reqId}`,
        kind: r.kind,
        at: r.at || Date.now(),
        sessionId: r.sessionId,
        title: sessionName(getApp().sessions[r.sessionId] ?? r),
        body: r.summary,
        reqId: r.reqId,
        request: r.request,
      })),
      fresh.every((r) => seen(r.sessionId)),
    );
  }, 200);
}

// What /api/pending depends on: a change here (a request came in or got answered) means fetch it again
const pendingKey = (ss: Record<string, SessionSummary>) =>
  Object.values(ss)
    .filter((s) => s.pending > 0 || s.status === "waiting")
    .map((s) => `${s.id}:${s.pending}`)
    .sort()
    .join(",");

// ---- low battery ----

const BAT = "gerdr.inbox.batFired";
async function checkBattery() {
  const cfg = getApp().config.batteryAlert ?? { on: true, percent: 20 };
  if (!cfg.on || !getApp().connected) return;
  let b;
  try {
    b = await api.battery();
  } catch {
    return;
  }
  const low = b.onBattery && b.percent >= 0 && b.percent <= cfg.percent;
  const fired = localStorage.getItem(BAT) === "1";
  // Once per discharge: armed again after it's plugged in or back above the line
  if (!low) {
    if (fired) localStorage.removeItem(BAT);
    return;
  }
  if (fired) return;
  localStorage.setItem(BAT, "1");
  const at = Date.now();
  add([{ id: `bat:${at}`, kind: "battery", at, title: `电量 ${b.percent}%`, body: "运行 gerdr 的电脑在用电池，快没电了" }], false);
}

/** Start feeding the inbox (once, from App). */
export function watchInbox() {
  let prev = getApp().sessions;
  let key = pendingKey(prev);
  let wasConnected = getApp().connected;
  const unsub = useStore.subscribe((s) => {
    if (s.connected !== wasConnected) {
      wasConnected = s.connected;
      if (s.connected) refreshPending();
    }
    if (s.sessions === prev) return;
    const before = prev;
    prev = s.sessions;
    const k = pendingKey(s.sessions);
    if (k !== key) {
      key = k;
      refreshPending();
    }
    const errs: InboxItem[] = [];
    for (const cur of Object.values(s.sessions)) {
      const old = before[cur.id];
      if (!old || cur.status !== "error" || old.status === "error" || !wanted("error")) continue;
      if (old.status !== "running" && old.status !== "waiting") continue;
      errs.push({ id: `err:${cur.id}:${Date.now()}`, kind: "error", at: Date.now(), sessionId: cur.id, title: sessionName(cur), body: cur.error });
    }
    add(
      errs,
      errs.every((e) => seen(e.sessionId!)),
    );
  });
  refreshPending();
  checkBattery();
  const timer = setInterval(checkBattery, 60_000);
  return () => {
    unsub();
    clearInterval(timer);
  };
}
