import { createElement } from "react";
import { toast } from "sonner";
import { create } from "zustand";
import { api, type PanelInboxItem, type PendingReq } from "./api";
import { systemNotify } from "./notify";
import { currentSpace, locateSession, mutedSession, spaceInitials, spaceList, spaceOf } from "./layout";
import { nativeFocusSession } from "./native";
import { isPhone } from "./phone";
import { codeLayout, codeShown, get as getApp, useStore, visibleSessions, workViewSessions } from "./store";
import type { SessionSummary } from "./types";
import { t } from "./i18n";
import { cn } from "./utils";
import { chatOnScreen } from "./shownChats";

/**
 * The inbox: things that need the user. Approvals / questions (from GET /api/pending) leave by themselves once they're
 * no longer pending; agents that ended in an error and the gerdr machine's battery running low stay until their × is
 * clicked (they have no "done" state). Per device (localStorage), like the unread dots: dismissing on the phone shouldn't clear the desktop's.
 */
export type InboxItem = {
  /** req:<reqId> | err:<session>:<at> | bat:<at> | pan:<panel key>:<its id> */
  id: string;
  /** panel: what a panel says needs the user (ctx.inbox.set): on every device, gone once the panel takes it back */
  kind: "permission" | "ask" | "error" | "battery" | "panel";
  at: number;
  sessionId?: string;
  /** Who it's about: the session's name, or the battery level */
  title: string;
  body?: string;
  reqId?: string;
  request?: any;
  /** kind panel: whose item it is (the panel's key) */
  panel?: string;
};

type InboxState = {
  items: InboxItem[];
  /** Something came in since the inbox was last opened: the red dot. */
  fresh: boolean;
  /** reqIds still waiting (last /api/pending): the others are answered, their item shows that and drops its buttons
   *  until it's removed. null = not fetched yet (don't call everything answered meanwhile). */
  live: Record<string, true> | null;
  /** The last /api/pending as is (every session's, this computer's and the hosts'): what core usePending reads. */
  reqs: PendingReq[];
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

export const useInbox = create<InboxState>(() => ({ ...load(), live: null, reqs: [] }));
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

/** What kind of item it is, as shown in the inbox and its notifications. */
export const kindLabel = (k: InboxItem["kind"]) =>
  ({
    permission: t("inbox.label.permission"),
    ask: t("inbox.label.ask"),
    error: t("inbox.label.error"),
    battery: t("inbox.label.battery"),
    panel: t("inbox.label.panel"),
  })[k];

/** `quiet`: the user is already looking at it (no red dot, no system notification); it still goes in the list. */
function add(items: InboxItem[], quiet: boolean, notify = !quiet) {
  if (!items.length) return;
  useInbox.setState((s) => ({ items: [...items, ...s.items].slice(0, MAX), fresh: s.fresh || !quiet }));
  save();
  // Being looked at here: other windows / devices don't pop a card for it either (closeCards keeps the id a moment,
  // as the item may reach them after this does)
  if (quiet) userClosedMany(items.map(cardId));
  if (!notify) return;
  // A system notification per item, carrying what it actually is (the command, the question, the error), so it can be
  // judged without opening Gerdr. In a browser the session id is the tag, so a newer one from the same session replaces the older.
  for (const it of items) {
    const title = it.kind === "battery" ? it.title : `${kindLabel(it.kind)} · ${it.title}`;
    systemNotify(title, it.body ?? "", it.sessionId);
    toastItem(it, title);
  }
}

// ---- in-app cards ----

/** Inbox item id → its toast's id. One card per session (a newer item replaces its card), one per battery alert. */
const toasts = new Map<string, string>();
const BODY_MAX = 240;
/** Cards being taken down by code (dropCards), not by the user: their onDismiss mustn't close them on other devices. */
const quietly = new Set<string>();

/**
 * The user clicked a card or closed it (×, swipe): the same card goes in every other window and device too, else
 * each one keeps showing what was already dealt with. Card ids match across devices (`inbox:<session>`, or the
 * request's / panel item's id), only the battery one is per device, as its alert is.
 */
function userClosed(card: string) {
  userClosedMany([card]);
}
function userClosedMany(cards: string[]) {
  const ids = [...new Set(cards)].filter((c) => !c.startsWith("inbox:bat:"));
  if (ids.length) import("./sync").then((s) => s.sendCardsClosed(ids));
}

/** An item's card id: one per conversation (a newer item replaces its card), else the item's own. */
const cardId = (it: InboxItem) => `inbox:${it.sessionId ?? it.id}`;

/**
 * Cards closed elsewhere lately, card id → when: an item seen in another window can be announced closed before it
 * reaches this one, which would then pop the card anyway.
 */
const closedAt = new Map<string, number>();
const CLOSED_HOLD = 15_000;

/** Cards closed elsewhere ("cards_closed"): drop them here. The items stay in the inbox until answered. */
export function closeCards(ids: string[]) {
  const now = Date.now();
  for (const [k, at] of closedAt) if (now - at > CLOSED_HOLD) closedAt.delete(k);
  for (const id of ids) closedAt.set(id, now);
  const want = new Set(ids);
  dropCards((k) => !want.has(toasts.get(k)!));
}

/**
 * The in-app card, like the update one (native.ts onAppUpdate): stays until closed, since a system notification is
 * easy to miss and an approval left unseen stalls the agent. It goes by itself once its item does (answered anywhere,
 * ×'d in the panel) and when the inbox is opened (dropCards): it's a pointer to the inbox, not a second copy of it.
 */
function toastItem(it: InboxItem, title: string) {
  const id = cardId(it);
  const closed = closedAt.get(id);
  if (closed && Date.now() - closed < CLOSED_HOLD) return;
  for (const [k, v] of toasts) if (v === id) toasts.delete(k);
  toasts.set(it.id, id);
  quietly.delete(id);
  const body = it.body && it.body.length > BODY_MAX ? it.body.slice(0, BODY_MAX) + "…" : it.body;
  const sid = it.sessionId;
  const open = sid || it.panel ? () => openFromCard(it) : undefined;
  // Several projects (often one window each): the project's chip, as on the rail, says whose it is before reading it
  const l = codeLayout(getApp());
  const space = itemSpace(it);
  const sp = space && spaceList(l).length > 1 ? spaceList(l).find((x) => x.id === space) : undefined;
  toast(title, {
    id,
    duration: Infinity,
    closeButton: true,
    description: body,
    // Below the text, left of the button: the project's chip as on the rail, and its name. Beside the text (sonner's
    // icon slot) it and the button squeezed the text into a narrow column.
    icon: sp ? createElement(SpaceChip, { id: sp.id, name: sp.name }) : undefined,
    // The text takes the first row; the chip and the button go under it, the button right-aligned by sonner's
    // margin-left: auto (as the update card, native.ts onAppUpdate). A grid, not a wrapping flex row: the card is as
    // wide as its text (up to the toaster's --width, centred), and a wrapping flex row's natural width adds the chip
    // and the button to the text's, so a short question left a wide empty band on the right (user report). In the
    // grid the text spans both columns, so the width is max(text, chip + button). Under 600px sonner makes every toast
    // full width; this keeps out of its way there. Sonner's sizes and margins are unlayered CSS, which beats
    // Tailwind's layered utilities: hence the `!`. The centring (inset-x-0 mx-auto) is wide-screen only too: under
    // 600px sonner's toaster is `left: 16px; width: 100%`, i.e. it sticks 16px out past the right edge, and its own
    // toasts only look centred because they sit at its left edge; mx-auto centred the card in that shifted box, so
    // on a phone it hung 16px right, touching the screen's edge (user report).
    classNames: {
      toast: "grid! grid-cols-[1fr_auto] gap-y-3! min-[601px]:w-fit! min-[601px]:min-w-80 max-w-(--width) min-[601px]:inset-x-0 min-[601px]:mx-auto",
      content: "col-span-2",
      icon: "order-1 size-auto! max-w-full m-0!",
      actionButton: "order-2 col-start-2",
    },
    action: open
      ? {
          label: it.kind === "ask" ? t("inbox.goAnswer") : it.kind === "error" ? t("inbox.goLook") : t("inbox.goHandle"),
          // Sonner takes the card down after this without calling onDismiss
          onClick: () => {
            if (toasts.get(it.id) === id) toasts.delete(it.id);
            userClosed(id);
            open();
          },
        }
      : undefined,
    // Also called when code dismisses it (sonner calls onDismiss for toast.dismiss too), hence `quietly`
    onDismiss: () => {
      if (toasts.get(it.id) === id) toasts.delete(it.id);
      if (!quietly.delete(id)) userClosed(id);
    },
  });
}

/**
 * The card's project: its chip as on the rail and its name. This window's own project is lit like the rail's current
 * chip (the card is about what's on screen here); another project's is plain but not dimmed, since that's the one to
 * notice. Live: it follows when the window switches project while the card is up.
 */
function SpaceChip({ id, name }: { id: string; name: string }) {
  const here = useStore((s) => currentSpace(s.layout)) === id;
  return createElement(
    "span",
    { className: "flex min-w-0 items-center gap-1.5 text-[12px] text-foreground" },
    createElement(
      "span",
      {
        className: cn(
          "flex size-5 shrink-0 items-center justify-center rounded border text-[10px] font-semibold",
          here ? "border-foreground bg-foreground text-background" : "border-foreground/40 bg-background text-foreground",
        ),
      },
      spaceInitials(name),
    ),
    createElement("span", { className: "truncate" }, name),
  );
}

/** The project (space) an item belongs to: its conversation's, else its panel's (a panel key is "<space>/<name>"). */
function itemSpace(it: InboxItem): string | undefined {
  if (it.sessionId) {
    const loc = locateSession(getApp().layout, it.sessionId);
    if (loc) return spaceOf(loc.ws);
  }
  // A panel's own conversation has no tab: it's the owning panel's (lib/panelRoute ownerOf; not imported, it pulls in actions)
  const panel = it.panel ?? (it.sessionId ? getApp().sessions[it.sessionId]?.tags?.panel : undefined);
  return panel?.split("/")[0] || undefined;
}

/**
 * A card clicked: another project's conversation goes to the window showing that project, if there is one, instead
 * of turning this window to it (every window shows the cards; the person picked this window for its own project).
 */
function openFromCard(it: InboxItem) {
  const space = itemSpace(it);
  if (it.sessionId && space && space !== currentSpace(getApp().layout) && nativeFocusSession(it.sessionId, space)) return;
  openInboxItem(it);
}

/** Where an item leads: its conversation (a panel's own comes back to the panel, lib/panelRoute), else its panel. */
export function openInboxItem(it: InboxItem) {
  if (it.sessionId) {
    const sid = it.sessionId;
    return void import("./actions").then((a) => a.focusSession(sid));
  }
  if (!it.panel) return;
  const [space, name = ""] = it.panel.split("/");
  import("./store").then((st) => (st.setWorkPanel(space, name), st.setMode("work")));
}

function dropCards(keep?: (itemId: string) => boolean) {
  for (const [k, v] of [...toasts]) {
    if (keep?.(k)) continue;
    toasts.delete(k);
    quietly.add(v);
    toast.dismiss(v);
  }
}

// An item left the list, or its request was answered (it lingers a moment as "已处理"): its card has nothing to offer
useInbox.subscribe((s) => {
  if (!toasts.size) return;
  const byId = new Map(s.items.map((x) => [x.id, x]));
  dropCards((k) => {
    const it = byId.get(k);
    return !!it && !(it.reqId && s.live && !s.live[it.reqId]);
  });
});

/**
 * Its conversation came on screen (opened by hand, not through the card): the card has done its job, it goes. The
 * item stays in the inbox until answered, like one that came in while being looked at.
 */
function dropSeenCards() {
  if (!toasts.size) return;
  const byId = new Map(get().items.map((x) => [x.id, x]));
  const gone: string[] = [];
  dropCards((k) => {
    const sid = byId.get(k)?.sessionId;
    const keep = !sid || !seen(sid);
    if (!keep) gone.push(toasts.get(k)!);
    return keep;
  });
  // Seen here is seen: the same card goes in the other windows too (one window per project: the card stayed up in
  // the project the user came back to)
  userClosedMany(gone);
}

export function dismiss(id: string) {
  const it = get().items.find((x) => x.id === id);
  useInbox.setState((s) => ({ items: s.items.filter((x) => x.id !== id) }));
  // A request or a panel's item may still be there on the server: remembered, so the next fetch doesn't bring it back
  const key = it?.reqId ?? (it?.kind === "panel" ? it.id : undefined);
  if (key)
    try {
      localStorage.setItem(GONE, JSON.stringify([key, ...gone()].slice(0, 300)));
    } catch {}
  save();
}

export function clearAll() {
  for (const it of get().items) dismiss(it.id);
}

/** Opened: the red dot and the cards go. */
export function markSeen() {
  // Handled once: the inbox opened in one window clears the cards in all of them (one person, several projects)
  userClosedMany([...toasts.values()]);
  dropCards();
  if (!get().fresh) return;
  useInbox.setState({ fresh: false });
  save();
}

export const sessionName = (s: Pick<SessionSummary, "title" | "cwd" | "agent">) => s.title || s.cwd.split("/").pop() || s.agent;

/** On screen and the window has focus: the user already sees it, no need for the red dot (it still goes in the list). */
function seen(id: string) {
  // Do not disturb (the conversation's own, or its group's): it still goes in the list, but quietly, like one you're looking at
  if (getApp().sessions[id]?.muted || mutedSession(getApp().layout, id)) return true;
  if (document.visibilityState !== "visible" || !document.hasFocus()) return false;
  // The Work view covers the tabs: what's on screen is its panel's chat and the conversation a page opened beside it
  // (a task's worker on a board). Without this, a question from the very task being read still popped a card.
  // A page's own chat (a board drawing a task's conversation with SessionChat) isn't in either: asked of the element.
  if (chatOnScreen(id)) return true;
  const l = getApp().layout;
  if (!codeShown()) return workViewSessions(l).includes(id);
  return visibleSessions(l, isPhone()).includes(id);
}

/** Settings → Inbox: the user can keep a kind out (missing = on). Battery has its own switch (batteryAlert). */
const wanted = (kind: "permission" | "ask" | "error") => getApp().config.inbox?.[kind] !== false;

// ---- approvals / questions ----

const HANDLED_LINGER = 2000;

let pendingTimer: ReturnType<typeof setTimeout> | undefined;
let primed = false; // the first /api/pending since the page opened has come back
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
    const live: Record<string, true> = Object.fromEntries(reqs.map((r) => [r.reqId, true as const]));
    useInbox.setState({ live, reqs });
    const quiet = fresh.every((r) => seen(r.sessionId));
    // No longer pending (answered here or elsewhere, or the turn ended): nothing left to do with it, so it goes. After
    // a moment showing "已处理", so an answer clicked in the panel visibly lands and the next item doesn't jump up under
    // a second click (which could approve something unread).
    const done = new Set(
      get()
        .items.filter((x) => x.reqId && !live[x.reqId])
        .map((x) => x.id),
    );
    if (done.size)
      setTimeout(() => {
        useInbox.setState((s) => ({ items: s.items.filter((x) => !done.has(x.id)) }));
        save();
      }, HANDLED_LINGER);
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
      quiet,
      // The first fetch after the page opens finds what was already waiting before it: red dot, but no burst of
      // notifications for things that aren't new
      primed && !quiet,
    );
    primed = true;
  }, 200);
}

// What /api/pending depends on: a change here (a request came in or got answered) means fetch it again
const pendingKey = (ss: Record<string, SessionSummary>) =>
  Object.values(ss)
    .filter((s) => s.pending > 0 || s.status === "waiting")
    .map((s) => `${s.id}:${s.pending}`)
    .sort()
    .join(",");

// ---- panels' items ----

/**
 * The panels' list (GET /api/inbox, or the server's "inbox" message) against what the inbox holds: new ones come in
 * (notified like a question, unless already looked at or ×'d here), ones the panel took back leave. Only after the
 * first fetch since the page opened are new ones notified: those were there before it.
 */
let panelPrimed = false;
export function syncPanelInbox(list: PanelInboxItem[]) {
  const ids = new Map(list.map((x) => [`pan:${x.panel}:${x.id}`, x]));
  const held = get().items;
  const stale = held.filter((x) => x.kind === "panel" && !ids.has(x.id));
  if (stale.length) {
    const drop = new Set(stale.map((x) => x.id));
    useInbox.setState((s) => ({ items: s.items.filter((x) => !drop.has(x.id)) }));
    save();
  }
  const have = new Set(held.map((x) => x.id));
  const skip = new Set(gone());
  const fresh: InboxItem[] = [];
  for (const [id, x] of ids)
    if (!have.has(id) && !skip.has(id))
      fresh.push({ id, kind: "panel", at: x.at || Date.now(), panel: x.panel, sessionId: x.session || undefined, title: x.title, body: x.body });
  const quiet = fresh.every((x) => !!x.sessionId && seen(x.sessionId));
  add(fresh.reverse(), quiet, panelPrimed && !quiet);
  panelPrimed = true;
}

function refreshPanelInbox() {
  // Renamed panels first (lib/panelRename.ts): their items come under the new key and mustn't come in as new
  void import("./panelRename").then((p) => p.loadRenamed().then(() => api.inbox().then(syncPanelInbox, () => {})));
}

/** A panel's directory was renamed (lib/panelRename.ts): its items and ×'d ones move to its new key, not notified again. */
export function renamePanelItems(from: string, to: string) {
  const pre = `pan:${from}:`;
  const move = (id: string) => (id.startsWith(pre) ? `pan:${to}:${id.slice(pre.length)}` : id);
  const items = get().items;
  if (items.some((x) => x.id.startsWith(pre))) {
    useInbox.setState({ items: items.map((x) => (x.id.startsWith(pre) ? { ...x, id: move(x.id), panel: to } : x)) });
    save();
  }
  const g = gone();
  if (g.some((id) => id.startsWith(pre)))
    try {
      localStorage.setItem(GONE, JSON.stringify(g.map(move)));
    } catch {}
}

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
  add([{ id: `bat:${at}`, kind: "battery", at, title: t("inbox.batteryTitle", { n: b.percent }), body: t("inbox.batteryBody") }], false);
}

/** Start feeding the inbox (once, from App). */
export function watchInbox() {
  let prev = getApp().sessions;
  let key = pendingKey(prev);
  let wasConnected = getApp().connected;
  let shownLayout = getApp().layout;
  let shownMode = getApp().mode;
  let shownPeek = getApp().workPeek;
  let shownMounts = getApp().mountWatch;
  const unsub = useStore.subscribe((s) => {
    // mountWatch: a page opened or closed a conversation of its own (SessionChat), e.g. a task on a board
    if (s.layout !== shownLayout || s.mode !== shownMode || s.workPeek !== shownPeek || s.mountWatch !== shownMounts) {
      shownLayout = s.layout;
      shownMode = s.mode;
      shownPeek = s.workPeek;
      shownMounts = s.mountWatch;
      dropSeenCards();
    }
    if (s.connected !== wasConnected) {
      wasConnected = s.connected;
      if (s.connected) (refreshPending(), refreshPanelInbox());
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
  refreshPanelInbox();
  checkBattery();
  const timer = setInterval(checkBattery, 60_000);
  // Coming back to a window already showing the conversation counts as opening it too
  window.addEventListener("focus", dropSeenCards);
  return () => {
    unsub();
    clearInterval(timer);
    window.removeEventListener("focus", dropSeenCards);
  };
}
