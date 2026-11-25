// The keys while a panel hosts Code (core CodeView) and gave its list's order (core useCodeOrder). Its list shows
// conversations by where they stand, not Code's tabs and workspaces, so the keys that mean "the Nth", "the next" or
// "a workspace" went somewhere the list doesn't show: ⇧⌘] stepped through one workspace's tabs, ⌘W landed on the
// next tab of a workspace nobody sees (or on a blank new-conversation page), the workspace keys switched or closed
// workspaces the panel hides (the person, 2026-11-25: 「快捷键应该直接作用到面板上」). Here they go by the list.
// The pane keys (split, ⌘D, focus, zoom…) still act on what is on screen, as in Code.
import type { KeyAction } from "../keys";
import { activeTab, leaves, locateSession } from "../layout";
import { focusedSession, get, hostOrder, markRead, useStore } from "../store";
import { toggleOverview } from "../overview";
import { focusPane } from "./panes";
import { addTabForSession } from "./tabs";

type Run = () => void;

/** As core showInCodeView: its pane when it has one, else a tab for it. */
function show(id: string) {
  toggleOverview(false);
  const loc = locateSession(get().layout, id);
  if (loc) focusPane(loc.pane.id);
  else addTabForSession(id);
  markRead(id);
}

const order = () => hostOrder() ?? [];
const here = () => focusedSession(get().layout);

/** The next / previous row of the list (round), from the conversation in front; the first / last when it isn't listed. */
function step(d: 1 | -1) {
  const ids = order();
  if (!ids.length) return;
  const i = ids.indexOf(here() ?? "\0");
  show(ids[i < 0 ? (d > 0 ? 0 : ids.length - 1) : (i + d + ids.length) % ids.length]);
}

// ⌘E: the conversation shown before this one, and back again (as Code's last tab)
let shown: { now?: string; before?: string } = {};
useStore.subscribe((s) => {
  if (!hostOrder(s)) return;
  const id = focusedSession(s.layout);
  if (id && id !== shown.now) shown = { now: id, before: shown.now };
});
function previous() {
  const id = shown.before;
  if (id && get().sessions[id]) show(id);
}

/**
 * Close what's in front with Code's own close (`close`: its double press for a running agent included), then land on
 * the row next to the closed one in the list, not on the next tab of its workspace (which the list doesn't show, or
 * a blank page Code puts back). A pane closed out of a split leaves the rest of that tab in front, as in Code.
 */
function closeThenLand(close: Run, whole: boolean): Run {
  return () => {
    const ids = order();
    const was = here();
    const i = ids.indexOf(was ?? "\0");
    const before = get().layout;
    const split = !whole && leaves(activeTab(before).root).length > 1;
    close();
    if (get().layout === before) return; // not closed (the first press on a running agent)
    if (split) return;
    const rest = ids.filter((x) => x !== was);
    const next = i < 0 ? rest[0] : (rest[i] ?? rest[i - 1]);
    if (next) show(next);
  };
}

const nothing: Run = () => {};

/** What replaces Code's table entries while hosted; the rest stay Code's. `code` is Code's table, for its closes. */
export function hostedKeys(code: Partial<Record<KeyAction, Run>>, nth: (i: number) => void): Partial<Record<KeyAction, Run>> {
  const digits = (prefix: string, f: (i: number) => void) => Object.fromEntries([1, 2, 3, 4, 5, 6, 7, 8, 9].map((i) => [`${prefix}${i}`, () => f(i)]));
  return {
    nextTab: () => step(1),
    prevTab: () => step(-1),
    lastTab: previous,
    ...digits("tab", nth),
    closePane: closeThenLand(code.closePane!, false),
    closeTab: closeThenLand(code.closeTab!, true),
    // No tab names in the list (they were taken out of Code's rows too)
    renameTab: nothing,
    // No workspaces in the list: switching one only changed what's behind it, closing one closed its tasks' panes
    ...digits("ws", nothing),
    newWorkspace: nothing,
    renameWorkspace: nothing,
    closeWorkspace: nothing,
    workspaces: nothing,
    nextWorkspace: nothing,
    prevWorkspace: nothing,
  } as Partial<Record<KeyAction, Run>>;
}
