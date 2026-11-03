// Back / forward through where you've been, like a browser's history: the mouse's side buttons (watchNavButtons) and
// the navBack / navForward commands. A place is a pane in Code mode (it carries its tab and workspace with it, so
// jumping there brings back the workspace, tab and split you were looking at) or a project's Work view. Kept in memory
// per window, like lastTab: it's "where I just was", not layout, and another device moving its own focus doesn't touch
// it (that's its view, not ours).
import { activeTab, allLeaves, currentSpace, spaceOf, type Layout } from "../layout";
import { isNative } from "../native";
import { get, overviewShown, setMode, useStore } from "../store";
import { focusPane } from "./panes";

type Mode = "agents" | "work";
/** pane: the focused one, also kept for Work, where it says which project to come back to (the Work view shows the current one). */
type Place = { mode: Mode; pane: string; space: string };

const MAX = 100;
const back: Place[] = [];
const forward: Place[] = [];
let jumping = false;

const here = (l: Layout, mode: Mode): Place => ({ mode, pane: activeTab(l).focus, space: currentSpace(l) });
// In Work the pane under it isn't seen, so moving it isn't going anywhere; only the project is
const key = (p: Place) => (p.mode === "work" ? `w:${p.space}` : `a:${p.pane}`);
let cur = here(get().layout, get().mode);

useStore.subscribe((s, p) => {
  if (s.layout === p.layout && s.mode === p.mode) return;
  const next = here(s.layout, s.mode);
  if (key(next) === key(cur)) return void (cur = next);
  if (!jumping) {
    if (!back.length || key(back[back.length - 1]) !== key(cur)) back.push(cur);
    if (back.length > MAX) back.shift();
    forward.length = 0;
  }
  cur = next;
});

/** Which pane to focus to get back to `p` (undefined: it's gone). Work only needs its project, so any pane of it will do. */
function target(l: Layout, p: Place): string | undefined {
  const all = allLeaves(l);
  if (all.some((x) => x.pane.id === p.pane)) return p.pane;
  if (p.mode === "work") return all.find((x) => spaceOf(x.ws) === p.space)?.pane.id;
}

// Moves one step along `from`, putting where we are onto `to`. Places that are gone (pane or project closed, here or on
// another device) are dropped on the way, so one press never lands on nothing.
function step(from: Place[], to: Place[]) {
  // The overview covers the tabs: a jump under it would be invisible and only lose the place
  if (overviewShown()) return;
  const l = get().layout;
  while (from.length) {
    const p = from.pop()!;
    const pane = target(l, p);
    if (!pane || key(p) === key(cur)) continue;
    to.push(cur);
    jumping = true;
    try {
      // Pane first: in Work it picks the project the view then shows
      focusPane(pane);
      if (get().mode !== p.mode) setMode(p.mode);
    } finally {
      jumping = false;
    }
    return;
  }
}

export const navBack = () => step(back, forward);
export const navForward = () => step(forward, back);

/**
 * The mouse's back / forward buttons (MouseEvent.button 3 / 4). Caught on window in the capture phase so a terminal pane
 * (xterm eats mouse events) or the chat can't swallow them first, and the browser's own history navigation is cancelled:
 * this is a single page, "back" there would leave it.
 */
export function watchNavButtons() {
  const side = (e: MouseEvent) => e.button === 3 || e.button === 4;
  const onDown = (e: MouseEvent) => {
    if (side(e)) e.preventDefault();
  };
  const onUp = (e: MouseEvent) => {
    if (!side(e)) return;
    e.preventDefault();
    e.stopPropagation();
    const isBack = e.button === 3;
    // In a page of its own (the Work site) its routes come first, like a browser: back undoes its own steps, and only
    // once it has none left does it go on to gerdr's places (to Code…). The Navigation API counts only that frame's
    // entries; the browser's own back would walk the whole window's joint history and could undo another frame's step.
    const nav = e.view && e.view !== window ? (e.view as any).navigation : undefined;
    if (nav && (isBack ? nav.canGoBack : nav.canGoForward)) return void (isBack ? nav.back() : nav.forward());
    if (isBack) navBack();
    else navForward();
  };
  const hook = (w: Window) => {
    w.addEventListener("mousedown", onDown, true);
    w.addEventListener("mouseup", onUp, true);
  };
  hook(window);
  // A press over an iframe goes to the iframe's window, not ours. The same-origin ones (the Work site, text in the file
  // viewer) get the same listeners, again on every page they load (each load is a new window); cross-origin ones and
  // Chrome's PDF viewer can't be reached and are left to the popstate guard below.
  const hookFrame = (f: HTMLIFrameElement) => {
    try {
      if (f.contentWindow && f.contentWindow.location.origin === location.origin) hook(f.contentWindow);
    } catch {}
  };
  const onLoad = (e: Event) => {
    if (e.target instanceof HTMLIFrameElement) hookFrame(e.target);
  };
  document.addEventListener("load", onLoad, true); // not window: a load event never reaches it
  document.querySelectorAll("iframe").forEach(hookFrame);
  // Whatever still gets past (a frame we can't reach) makes the browser go back through the window's joint history,
  // whose first entry in the desktop App is the shell's "Starting…" page (desktop/main.js shows it until the backend is
  // up): the window landed there and stayed black. So keep one entry of ours on top: such a back lands on it (same
  // document, a popstate), it's put straight back and counts as a back here. Not in a browser tab: there it would trap
  // the toolbar's back button, and the page before Gerdr is the user's own.
  let onPop: (() => void) | undefined;
  if (isNative()) {
    history.pushState(history.state, "", location.href);
    onPop = () => {
      history.pushState(history.state, "", location.href);
      navBack();
    };
    window.addEventListener("popstate", onPop);
  }
  return () => {
    window.removeEventListener("mousedown", onDown, true);
    window.removeEventListener("mouseup", onUp, true);
    document.removeEventListener("load", onLoad, true);
    if (onPop) window.removeEventListener("popstate", onPop);
  };
}
