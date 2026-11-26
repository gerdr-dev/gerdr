// The keys while a panel hosts Code (core CodeView) and gave its list's order (core useCodeOrder). Its list shows
// conversations by where they stand, not Code's tabs and workspaces, so the keys that mean "the Nth", "the next" or
// "a workspace" went somewhere the list doesn't show: ⇧⌘] stepped through one workspace's tabs, ⌘W landed on the
// next tab of a workspace nobody sees (or on a blank new-conversation page), the workspace keys switched or closed
// workspaces the panel hides (the person, 2026-11-25: 「快捷键应该直接作用到面板上」). Here they go by the list.
// The pane keys (split, ⌘D, focus, zoom…) still act on what is on screen, as in Code.
import type { KeyAction } from "../keys";
import { codeCloseFallback } from "../codeMenu";
import { activeTab, leaves, locateSession } from "../layout";
import { focusedSession, get, hostOrder, markRead, useStore } from "../store";
import { toggleOverview } from "../overview";
import { closeFocusedPane, focusPane } from "./panes";
import { addTabForSession, closeTab } from "./tabs";

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
 * ⌘W where the list is tasks, not tabs (the person, 2026-11-25: 任务和窗口的概念不应该暴露出来, and a ⌘W that only
 * moved to the next row meant nothing anyone could tell). It acts only where there is something to close that one
 * can see go:
 *   - a split: that part of it goes, the rest of the tab fills its place (Code's own close);
 *   - a new conversation not spoken to yet (⌘T's, or the new-conversation page): it goes (a panel lists an empty task of
 *     its own only while it's on screen, so ⇧⌘T brings back the same task) and the one shown before it comes back;
 *   - a task alone on screen: Gerdr closes nothing (a task isn't closed; it ends by moving it to 已完成) and hands the key
 *     to the panel (core useCodeCloseFallback): a task board asks where the task goes now (the person, 2026-11-26).
 * Nothing it closes is stopped while working, and there is no second press to confirm.
 */
function close(whole: boolean): Run {
  return () => {
    const l = get().layout;
    const tab = activeTab(l);
    const split = !whole && leaves(tab.root).length > 1;
    const id = here();
    const s = id ? get().sessions[id] : undefined;
    // An agent's conversation with nothing in it yet (no events, no title): what ⌘T opens. A pane with no
    // conversation is the new-conversation page itself
    const empty = !id || (!!s && !s.lastSeq && !s.title);
    if (!split && !empty) return codeCloseFallback(tab.focus, id);
    // The one seen before: the new-conversation page has no conversation, so the last one seen is still "now"
    const back = id ? shown.before : shown.now;
    if (whole) closeTab(undefined, true);
    else closeFocusedPane(true);
    if (split) return;
    const ids = order();
    const to = back && back !== id && get().sessions[back] ? back : ids.find((x) => x !== id);
    if (to) show(to);
  };
}

const nothing: Run = () => {};

/** What replaces Code's table entries while hosted; the rest stay Code's. `nth`: ⌘N's go-to. */
export function hostedKeys(nth: (i: number) => void): Partial<Record<KeyAction, Run>> {
  const digits = (prefix: string, f: (i: number) => void) => Object.fromEntries([1, 2, 3, 4, 5, 6, 7, 8, 9].map((i) => [`${prefix}${i}`, () => f(i)]));
  return {
    nextTab: () => step(1),
    prevTab: () => step(-1),
    lastTab: previous,
    ...digits("tab", nth),
    closePane: close(false),
    closeTab: close(true),
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
