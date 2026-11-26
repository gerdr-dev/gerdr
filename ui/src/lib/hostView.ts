import { currentSpace } from "./layout";
import { codeLayout, codeShown, get, hasPanelLayout, panelsApart, setMode, setWorkPanel, showLayout, useStore } from "./store";
import { panelOfKey, spaceOfKey } from "./work";

// Code mode and each panel drawing its own Code list (core CodeView list="replace") have panes of their own, stored and
// held apart (store/layoutSync.ts). This puts the right one on screen: Code's in Code mode, the panel's own while that
// panel is shown. Nothing here, or anywhere, moves a pane from one to the other: whose a conversation is comes from the
// conversation (tags.panel). The first version (2026-11-26) kept a panel's workspaces inside Code's layout and moved
// panes between the views by what each device knew of the panels; a device that hadn't opened the panel moved them one
// way, the one showing it moved them back, and in the tug a conversation just started there lost its pane.
// docs/dev/HOSTED-CODE.md

type State = ReturnType<typeof get>;

/** Whether panel `key` shows panes of its own: it draws the whole list (CodeView list="replace"), or already has some. */
const ownPanes = (s: Pick<State, "codeHosts">, key: string) => panelsApart() && (s.codeHosts[key]?.list === "replace" || hasPanelLayout(key));

/**
 * Whose layout a conversation's pane goes in: "" = Code's, a panel key = that panel's own. A panel's conversation
 * (tags.panel) when that panel has panes of its own; the rest, and those of a panel that shows its conversations in
 * Code ("open in Code"), Code's.
 */
export function ownerHost(session: string, s: Pick<State, "sessions" | "codeHosts"> = get()): string {
  const k = s.sessions[session]?.tags?.panel;
  return k && ownPanes(s, k) ? k : "";
}

/** The layout the screen shows: "" = Code's (Code mode, or a panel adding to Code's list), a panel key; null = a panel page that isn't Code. */
function shownOwner(s: State): string | null {
  if (s.mode !== "work") return "";
  const space = currentSpace(codeLayout(s));
  const panel = s.workPanels[space];
  const key = panel ? `${space}/${panel}` : space;
  if (!s.codeHosts[key]) return null;
  return s.codeHosts[key].list === "replace" && panelsApart() ? key : "";
}

/** Show `owner`'s layout on screen: Code mode, or that panel. For bringing up a conversation of the other side. */
export function showOwner(owner: string) {
  if (!owner) {
    // Code's: Code mode, unless what's on screen already shows Code's layout (a panel adding to Code's list)
    if (get().layoutOwner !== "" || !codeShown()) setMode("agents");
  } else {
    setWorkPanel(spaceOfKey(owner), panelOfKey(owner));
    if (get().mode !== "work") setMode("work");
  }
  // At once, not on the next change: the caller goes on to act on the layout now on screen
  showLayout(owner);
}

function sync(s: State) {
  // Another project came up (the rail, a notification) while a panel's own panes were on screen: they are that
  // panel's project's, so Code's comes back first and the new project's view is worked out from there
  if (s.layoutOwner && spaceOfKey(s.layoutOwner) !== currentSpace(codeLayout(s))) {
    showLayout("");
    s = get();
  }
  const want = shownOwner(s);
  if (want !== null && want !== s.layoutOwner) showLayout(want);
}

/** Start keeping the right layout on screen (once, at start-up, after the layouts are in). */
export function startHostViews() {
  useStore.subscribe(sync);
  sync(get());
}
