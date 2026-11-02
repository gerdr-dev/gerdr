import { toast } from "sonner";
import type { KeyAction } from "../keys";
import { activeTab, activeWs } from "../layout";
import { get, overviewShown, set, setMode, toggleSidebar } from "../store";
import { overviewKey, toggleOverview } from "../overview";
import { activeAt } from "../modHeld";
import { confirmCloseFocusedPane, cyclePane, duplicateFocused, focusDir, newAgentInOverview, splitFocused, swapDir, tabName, toggleZoom } from "./panes";
import { reopenClosed } from "./reopen";
import { attentionList, focusSession, focusedSessionId, stashDraft, stepAttention, stopFocused } from "./sessions";
import { stepSpace } from "./spaces";
import { addTab, closeTab, lastTab, selectTab, stepTab } from "./tabs";
import { openTerminal } from "./terminal";
import { closeWorkspace, selectWorkspaceN, stepWorkspace } from "./workspaces";
import { t } from "../i18n";

// ---- 快捷键动作 ----

export function runAction(a: KeyAction) {
  // A new agent from the overview stays in it, next to the tile it copies (only with no tile to copy does it fall through)
  if (overviewShown() && (a === "duplicateRight" || a === "duplicateDown" || a === "newTab") && newAgentInOverview()) return;
  if (overviewShown() && overviewKey(a)) return;
  const l = get().layout;
  switch (a) {
    case "splitRight":
      return splitFocused("right");
    case "splitDown":
      return splitFocused("down");
    case "duplicateRight":
      return duplicateFocused("right");
    case "duplicateDown":
      return duplicateFocused("down");
    case "splitTerminal":
      return openTerminal("right");
    case "splitTerminalDown":
      return openTerminal("down");
    case "terminalTab":
      return openTerminal("tab");
    case "closePane":
      return confirmCloseFocusedPane();
    case "zoom":
      return toggleZoom();
    case "focusLeft":
      return focusDir("left");
    case "focusDown":
      return focusDir("down");
    case "focusUp":
      return focusDir("up");
    case "focusRight":
      return focusDir("right");
    case "swapLeft":
      return swapDir("left");
    case "swapDown":
      return swapDir("down");
    case "swapUp":
      return swapDir("up");
    case "swapRight":
      return swapDir("right");
    case "nextPane":
      return cyclePane(1);
    case "prevPane":
      return cyclePane(-1);
    case "newTab":
      return addTab();
    case "nextTab":
      return stepTab(1);
    case "prevTab":
      return stepTab(-1);
    case "lastTab":
      return lastTab();
    case "closeTab":
      return closeTab();
    case "reopenClosed":
      return reopenClosed();
    case "renameTab": {
      const t = activeTab(l);
      return set({ dialog: { kind: "rename", target: "tab", id: t.id, value: t.name ?? tabName(t) } });
    }
    case "tab1":
    case "tab2":
    case "tab3":
    case "tab4":
    case "tab5":
    case "tab6":
    case "tab7":
    case "tab8":
    case "tab9":
      return selectTab(Number(a.slice(3)) - 1);
    case "active1":
    case "active2":
    case "active3":
    case "active4":
    case "active5":
    case "active6":
    case "active7":
    case "active8":
    case "active9": {
      // Same as clicking the row: a tile in the grid is focused there, anything else is brought up in its pane / Work
      const id = activeAt(Number(a.slice(6)));
      if (id) focusSession(id);
      return;
    }
    case "ws1":
    case "ws2":
    case "ws3":
    case "ws4":
    case "ws5":
    case "ws6":
    case "ws7":
    case "ws8":
    case "ws9":
      return selectWorkspaceN(Number(a.slice(2)) - 1);
    case "newWorkspace":
      return set({ dialog: { kind: "newWorkspace" } });
    case "renameWorkspace": {
      return set({ dialog: { kind: "editWorkspace", id: activeWs(l).id } });
    }
    case "closeWorkspace":
      return closeWorkspace();
    case "workspaces":
      return set({ dialog: { kind: "workspaces" } });
    case "nextWorkspace":
      return stepWorkspace(1);
    case "prevWorkspace":
      return stepWorkspace(-1);
    case "newSpace":
      return set({ dialog: { kind: "rename", target: "newSpace", id: "", value: "" } });
    case "nextSpace":
      return stepSpace(1);
    case "prevSpace":
      return stepSpace(-1);
    case "sessions":
      return set({ dialog: { kind: "sessions" } });
    case "toggleSidebar":
      return toggleSidebar();
    case "overview":
      if (get().mode === "work") setMode("agents");
      return toggleOverview();
    case "help":
      return set({ dialog: { kind: "help" } });
    case "settings":
      return set({ dialog: { kind: "settings" } });
    case "importHerdr":
      return set({ dialog: { kind: "importHerdr" } });
    case "palette":
      return set({ dialog: { kind: "palette" } });
    case "nextAttention":
      return stepAttention(1);
    case "prevAttention":
      return stepAttention(-1);
    case "focusAttention": {
      const first = attentionList()[0];
      if (first) focusSession(first);
      else toast(t("actionsRunAction.noWaiting"));
      return;
    }
    case "renamePane": {
      const id = focusedSessionId();
      if (!id) return;
      return set({ dialog: { kind: "rename", target: "session", id, value: get().sessions[id]?.title ?? "" } });
    }
    case "stop":
      return stopFocused();
    case "stash":
      return stashDraft();
  }
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
