import { switchSpace } from "@/lib/actions/spaces";
import { currentSpace, spaceList } from "@/lib/layout";
import { codeLayout, get, set, setMode, setWorkPanel } from "@/lib/store";
import { panelOfKey, spaceOfKey } from "@/lib/work";

/**
 * Every conversation has an owner. One a panel started (ctx.sessions.start) carries the tag panel=<its Work key>, and
 * bringing it up from anywhere (a notification, the inbox, the running list, the palette) goes to that panel, which
 * shows it its own way (a board opens the task's drawer: core onShowSession). The rest belong to Code and open in a
 * pane. Panels don't know Code is there; Code is just where the conversations nobody else owns live (and is to become
 * a panel of its own, built in).
 */

type Handler = (session: string) => void;
const handlers = new Map<string, Handler>();
const pending = new Map<string, { session: string; timer: ReturnType<typeof setTimeout> }>();

/** How long a panel just switched to gets to mount and say how it shows a conversation, before gerdr's own drawer does. */
const WAIT_MS = 4000;

/** The panel that owns a conversation, if one does. */
export function ownerOf(sessionId: string): string | undefined {
  return get().sessions[sessionId]?.tags?.panel || undefined;
}

/** core onShowSession: the panel `key` shows conversations it owns itself. The last one registered wins; call the result to drop it. */
export function onShowSession(key: string, fn: Handler): () => void {
  handlers.set(key, fn);
  const p = pending.get(key);
  if (p) {
    clearTimeout(p.timer);
    pending.delete(key);
    fn(p.session);
  }
  return () => {
    if (handlers.get(key) === fn) handlers.delete(key);
  };
}

/** Switch to the panel that owns the conversation and have it shown there. */
export function showInPanel(key: string, session: string) {
  const space = spaceOfKey(key);
  const l = codeLayout();
  if (currentSpace(l) !== space && spaceList(l).some((s) => s.id === space)) switchSpace(space);
  setWorkPanel(space, panelOfKey(key));
  setMode("work");
  const h = handlers.get(key);
  if (h) return h(session);
  // Not mounted yet (first time shown) or it doesn't handle it: wait a moment, then gerdr's drawer over the panel
  const prev = pending.get(key);
  if (prev) clearTimeout(prev.timer);
  pending.set(key, {
    session,
    timer: setTimeout(() => {
      pending.delete(key);
      set({ workPeek: { id: session, project: key } });
    }, WAIT_MS),
  });
}

/**
 * An inbox item a panel put there without a conversation (ctx.inbox.set, no `session`: a to-do the panel took in from
 * outside, a step only the user can do). Opening it went to the panel and stopped there: the window stayed on its own
 * project when the item was another's, and the panel never learned which item to show, so the person had to find it
 * again (2026-11-27). Now gerdr switches to the panel and hands it the item's id (core onShowInboxItem).
 */
const itemHandlers = new Map<string, Handler>();
const pendingItems = new Map<string, { id: string; timer: ReturnType<typeof setTimeout> }>();

/** core onShowInboxItem: the panel `key` opens its own inbox items (by the id it gave ctx.inbox.set). */
export function onShowInboxItem(key: string, fn: Handler): () => void {
  itemHandlers.set(key, fn);
  const p = pendingItems.get(key);
  if (p) {
    clearTimeout(p.timer);
    pendingItems.delete(key);
    fn(p.id);
  }
  return () => {
    if (itemHandlers.get(key) === fn) itemHandlers.delete(key);
  };
}

/** Switch to the panel `key` (its project first) and have it open its item `id`. */
export function showInboxItem(key: string, id: string) {
  const space = spaceOfKey(key);
  const l = codeLayout();
  if (currentSpace(l) !== space && spaceList(l).some((s) => s.id === space)) switchSpace(space);
  setWorkPanel(space, panelOfKey(key));
  setMode("work");
  const h = itemHandlers.get(key);
  if (h) return h(id);
  // Not mounted yet: held a while for it. A panel without a handler just shows itself, as before
  const prev = pendingItems.get(key);
  if (prev) clearTimeout(prev.timer);
  pendingItems.set(key, { id, timer: setTimeout(() => pendingItems.delete(key), 15_000) });
}
