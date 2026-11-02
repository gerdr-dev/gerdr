// ⌘1–9 (Ctrl+1–9 off the mac) go to the Nth agent of the sidebar's "active" section. That list is ordered by who needs you
// first (lib/order.ts), so it moves: going to a finished agent marks it read and drops it down the list, and the next
// ⌘N would land on someone else. So the order is frozen from the moment the modifier goes down until it comes up: the
// numbers you read are the ones the keys use, and ⌘1 then ⌘2 in one hold reach the two rows you saw.
import { useSyncExternalStore } from "react";
import { IS_MAC } from "./keys";
import { activeIds } from "./overviewState";
import { get } from "./store";

type State = {
  /** Show the numbers: the modifier has been held alone for a moment. */
  shown: boolean;
  /** "Active" as it was when the modifier went down; undefined when it's up. */
  frozen?: string[];
};

let state: State = { shown: false };
const subs = new Set<() => void>();
const put = (next: State) => {
  state = next;
  for (const f of subs) f();
};

if (typeof window !== "undefined") {
  const mod = IS_MAC ? "Meta" : "Control";
  let timer: ReturnType<typeof setTimeout> | undefined;
  const off = () => {
    clearTimeout(timer);
    timer = undefined;
    if (state.shown || state.frozen) put({ shown: false });
  };
  const down = (e: KeyboardEvent) => {
    // ⌘1–9 while ⌘ is still down: keep the numbers (and the order) so the next pick is still readable
    if (/^Digit[1-9]$/.test(e.code) && (IS_MAC ? e.metaKey : e.ctrlKey)) return;
    if (e.key !== mod) return off();
    if (e.repeat) return;
    if (e.shiftKey || e.altKey || (IS_MAC ? e.ctrlKey : e.metaKey)) return;
    // Frozen at once (a quick ⌘1 uses the order on screen), shown after a moment so ⌘C / ⌘V don't flash numbers
    put({ shown: false, frozen: activeIds(get()) });
    clearTimeout(timer);
    timer = setTimeout(() => put({ ...state, shown: true }), 250);
  };
  // Capture phase: xterm and the composer stop some keydowns. Blur: the keyup of a ⌘-Tab away never reaches the page.
  window.addEventListener("keydown", down, true);
  window.addEventListener("keyup", (e) => e.key === mod && off(), true);
  window.addEventListener("blur", off);
}

const subscribe = (f: () => void) => (subs.add(f), () => subs.delete(f));

/** Whether to draw the ⌘1–9 numbers. */
export const useModShown = () => useSyncExternalStore(subscribe, () => state.shown);

/** Re-render when the freeze starts / ends (the sidebar's order follows it). */
export const useModFrozen = () => useSyncExternalStore(subscribe, () => state.frozen);

/** `ids` ("active" now) in the frozen order while the modifier is held: the ones still there keep their place, newcomers go last. */
export function frozenOrder(ids: string[], frozen = state.frozen): string[] {
  if (!frozen) return ids;
  const kept = frozen.filter((id) => ids.includes(id));
  return [...kept, ...ids.filter((id) => !kept.includes(id))];
}

/** The agent ⌘N goes to (1-based), in the order the sidebar shows. */
export function activeAt(n: number): string | undefined {
  return frozenOrder(activeIds(get()))[n - 1];
}
