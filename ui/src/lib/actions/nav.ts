// Back / forward through where focus has been, like a browser's history: the mouse's side buttons (watchNavButtons) and
// the navBack / navForward commands. A place is a pane (it carries its tab and workspace with it), so jumping there
// brings back the workspace, tab and split you were looking at. Kept in memory per window, like lastTab: it's "where I
// just was", not layout, and another device moving its own focus doesn't touch it (that's its view, not ours).
import { activeTab, allLeaves } from "../layout";
import { isNative } from "../native";
import { get, overviewShown, useStore } from "../store";
import { focusPane } from "./panes";

const MAX = 100;
const back: string[] = [];
const forward: string[] = [];
let cur = activeTab(get().layout).focus;
let jumping = false;

useStore.subscribe((s, p) => {
  if (s.layout === p.layout) return;
  const id = activeTab(s.layout).focus;
  if (id === cur) return;
  if (!jumping) {
    if (cur && back[back.length - 1] !== cur) back.push(cur);
    if (back.length > MAX) back.shift();
    forward.length = 0;
  }
  cur = id;
});

// Moves one step along `from`, putting where we are onto `to`. Places that are gone (pane closed, here or on another
// device) are dropped on the way, so one press never lands on nothing.
function step(from: string[], to: string[]) {
  // The overview and the Work view cover the tabs: a jump under them would be invisible and only lose the place
  if (overviewShown() || get().mode === "work") return;
  const live = new Set(allLeaves(get().layout).map((x) => x.pane.id));
  while (from.length) {
    const id = from.pop()!;
    if (!live.has(id) || id === cur) continue;
    to.push(cur);
    jumping = true;
    try {
      focusPane(id);
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
    if (e.button === 3) navBack();
    else navForward();
  };
  window.addEventListener("mousedown", onDown, true);
  window.addEventListener("mouseup", onUp, true);
  // The listeners above never see a press over an iframe (the Work site, a PDF in the file viewer): the browser goes
  // back through the window's joint history instead. In the desktop App that history starts with the shell's
  // "Starting…" page (desktop/main.js shows it until the backend is up), so the window landed there and stayed black.
  // So keep one entry of ours on top: a back that gets past the iframe's own history lands on it (same document, a
  // popstate), and it's put straight back. In Code mode that press counts as a back here. Not in a browser tab: there
  // it would trap the toolbar's back button, and the page before Gerdr is the user's own.
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
    if (onPop) window.removeEventListener("popstate", onPop);
  };
}
