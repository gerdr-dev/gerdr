import { toast } from "sonner";
import type { KeyAction } from "../keys";
import { activeTab, activeWs } from "../layout";
import { get, set, setMode, toggleSidebar } from "../store";
import { closeTile, focusTileDir, stepTile, swapTileDir, toggleOverview, toggleTileZoom } from "../overview";
import { activeAt } from "../modHeld";
import { confirmCloseFocusedPane, cyclePane, duplicateFocused, focusDir, newAgentInOverview, splitFocused, swapDir, tabName, toggleZoom } from "./panes";
import { reopenClosed } from "./reopen";
import { attentionList, focusSession, focusedSessionId, stashDraft, stepAttention, stopFocused } from "./sessions";
import { stepSpace } from "./spaces";
import { addTab, closeTab, lastTab, selectTab, stepTab } from "./tabs";
import { navBack, navForward } from "./nav";
import { openTerminal } from "./terminal";
import { closeWorkspace, selectWorkspaceN, stepWorkspace } from "./workspaces";
import { currentView, type ViewName } from "./views";
import { t } from "../i18n";
import { isPhone } from "../phone";

// ---- 快捷键动作 ----
//
// A key action is an intent ("close", "focus left", "new tab"); what it does is up to the view on screen (views.ts):
// the overview closes / moves tiles, Work closes the conversation shown over its page, Code acts on the panes. So:
//   1. the view's own table, if it has the action;
//   2. else APP: the same in every view (dialogs, the session keys, which ask the view for its session);
//   3. else it's one of Code's that this view covers: step aside to Code and stop there, without running it. The
//      layout under a cover isn't what the user is looking at, so acting on it (closing "the focused pane", splitting
//      it) would change things out of sight; a second press, now in Code, does it.
// The tables are typed so that every action is either in APP or in CODE: a new one can't be forgotten.

type Run = () => void;

type Digit = 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9;

/** tab1…tab9 and the like: one entry per digit. */
const numbered = <P extends string>(prefix: P, f: (i: number) => void) =>
  Object.fromEntries([1, 2, 3, 4, 5, 6, 7, 8, 9].map((i) => [`${prefix}${i}`, () => f(i)])) as Record<`${P}${Digit}`, Run>;

const APP = {
  sessions: () => set({ dialog: { kind: "sessions" } }),
  overview: () => {
    if (get().mode === "work") setMode("agents");
    toggleOverview();
  },
  help: () => set({ dialog: { kind: "help" } }),
  settings: () => set({ dialog: { kind: "settings" } }),
  importHerdr: () => set({ dialog: { kind: "importHerdr" } }),
  palette: () => set({ dialog: { kind: "palette" } }),
  // Going to a session: focusSession knows the views (a tile in the overview, the Work chat, else its pane in Code)
  nextAttention: () => stepAttention(1),
  prevAttention: () => stepAttention(-1),
  focusAttention: () => {
    const first = attentionList()[0];
    if (first) focusSession(first);
    else toast(t("actionsRunAction.noWaiting"));
  },
  // Same as clicking the row in the list
  ...numbered("active", (i) => {
    const id = activeAt(i);
    if (id) focusSession(id);
  }),
  // The session keys: on the view's session (the focused pane's, tile's, or the Work chat's)
  renamePane: () => {
    const id = focusedSessionId();
    if (id) set({ dialog: { kind: "rename", target: "session", id, value: get().sessions[id]?.title ?? "" } });
  },
  stop: () => stopFocused(),
  stash: () => stashDraft(),
} satisfies Partial<Record<KeyAction, Run>>;

/** Code: the panes, tabs, workspaces and projects. */
const CODE: Record<Exclude<KeyAction, keyof typeof APP>, Run> = {
  splitRight: () => splitFocused("right"),
  splitDown: () => splitFocused("down"),
  duplicateRight: () => duplicateFocused("right"),
  duplicateDown: () => duplicateFocused("down"),
  splitTerminal: () => openTerminal("right"),
  splitTerminalDown: () => openTerminal("down"),
  terminalTab: () => openTerminal("tab"),
  closePane: () => confirmCloseFocusedPane(),
  zoom: () => toggleZoom(),
  focusLeft: () => focusDir("left"),
  focusDown: () => focusDir("down"),
  focusUp: () => focusDir("up"),
  focusRight: () => focusDir("right"),
  swapLeft: () => swapDir("left"),
  swapDown: () => swapDir("down"),
  swapUp: () => swapDir("up"),
  swapRight: () => swapDir("right"),
  nextPane: () => cyclePane(1),
  prevPane: () => cyclePane(-1),
  newTab: () => addTab(),
  nextTab: () => stepTab(1),
  prevTab: () => stepTab(-1),
  lastTab: () => lastTab(),
  closeTab: () => closeTab(),
  reopenClosed: () => reopenClosed(),
  renameTab: () => {
    const tab = activeTab(get().layout);
    set({ dialog: { kind: "rename", target: "tab", id: tab.id, value: tab.name ?? tabName(tab) } });
  },
  ...numbered("tab", (i) => selectTab(i - 1)),
  ...numbered("ws", (i) => selectWorkspaceN(i - 1)),
  newWorkspace: () => set({ dialog: { kind: "newWorkspace" } }),
  renameWorkspace: () => set({ dialog: { kind: "editWorkspace", id: activeWs(get().layout).id } }),
  closeWorkspace: () => closeWorkspace(),
  workspaces: () => set({ dialog: { kind: "workspaces" } }),
  nextWorkspace: () => stepWorkspace(1),
  prevWorkspace: () => stepWorkspace(-1),
  newSpace: () => set({ dialog: { kind: "rename", target: "newSpace", id: "", value: "" } }),
  // nav.ts goes back to the place itself, Code or Work, and does nothing under the overview
  navBack: () => navBack(),
  navForward: () => navForward(),
  toggleSidebar: () => toggleSidebar(),
  nextSpace: () => stepSpace(1),
  prevSpace: () => stepSpace(-1),
};

/** The overview: pane keys act on the tiles. A new agent opens next to the focused tile, staying in the grid. */
const newAgent = () => newAgentInOverview() || toggleOverview(false);
const OVERVIEW: Partial<Record<KeyAction, Run>> = {
  closePane: () => closeTile(), // the tile leaves the grid, the agent keeps running
  zoom: () => toggleTileZoom(),
  focusLeft: () => focusTileDir("left"),
  focusDown: () => focusTileDir("down"),
  focusUp: () => focusTileDir("up"),
  focusRight: () => focusTileDir("right"),
  swapLeft: () => swapTileDir("left"),
  swapDown: () => swapTileDir("down"),
  swapUp: () => swapTileDir("up"),
  swapRight: () => swapTileDir("right"),
  nextPane: () => stepTile(1),
  prevPane: () => stepTile(-1),
  newTab: newAgent,
  duplicateRight: newAgent,
  duplicateDown: newAgent,
};

/** Work: the site and its agent are per project, so the project keys work here too; ⌘W closes the conversation the page shows over itself. */
const WORK: Partial<Record<KeyAction, Run>> = {
  closePane: () => set({ workPeek: null }),
  newSpace: CODE.newSpace,
  nextSpace: CODE.nextSpace,
  prevSpace: CODE.prevSpace,
  navBack: CODE.navBack,
  navForward: CODE.navForward,
  // The phone's drawer (projects, Code / Work) opens over Work too; the desktop's session list is Code's
  toggleSidebar: () => (isPhone() ? toggleSidebar() : setMode("agents")),
};

const TABLES: Record<ViewName, Partial<Record<KeyAction, Run>>> = { code: CODE, overview: OVERVIEW, work: WORK };

export function runAction(a: KeyAction) {
  const view = currentView();
  const run = TABLES[view.name][a] ?? (APP as Partial<Record<KeyAction, Run>>)[a];
  if (run) return run();
  view.leave();
}

/**
 * After an overlay closes (store.dialog, the Lightbox), hand focus back to the focused pane: its composer / terminal
 * listens for gerdr:refocus. Radix restores focus to the dialog's trigger (often a sidebar button) when it unmounts
 * after its exit animation, so wait until no dialog is left in the DOM. Gives up if another one opens meanwhile.
 */
export function refocusAfterDialog() {
  const start = performance.now();
  const tick = () => {
    if (get().dialog) return;
    if (document.querySelector("[role=dialog]") && performance.now() - start < 1000) return void requestAnimationFrame(tick);
    window.dispatchEvent(new Event("gerdr:refocus"));
  };
  requestAnimationFrame(tick);
}
