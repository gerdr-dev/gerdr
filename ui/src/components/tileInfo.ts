// The overview's non-component helpers, shared by its files. Kept out of the .tsx files: a .tsx that exports
// anything but components is no React Refresh boundary, so an edit to it would reload its importers too.
import { tabName } from "@/lib/actions";
import { locateSession, spaceList, spaceOf, type Layout } from "@/lib/layout";
import { workChats } from "@/lib/overview";
import { type WorkConf } from "@/lib/overviewState";
import { useStore } from "@/lib/store";

/** The project a session belongs to: its pane's, or the one whose Work view shows it. */
function projectOf(l: Layout, c: WorkConf, id: string): string | undefined {
  const loc = locateSession(l, id);
  if (loc) return spaceOf(loc.ws);
  return Object.entries(workChats(c, l)).find(([, x]) => x === id)?.[0];
}

/**
 * "tab · workspace · project" of a session's pane, or "Work · project" (the project only when there's more than one).
 * Most specific first: the tab is what tells rows apart, and a narrow row truncates the end.
 */
export function whereOf(l: Layout, c: WorkConf, id: string): string {
  const loc = locateSession(l, id);
  const spaces = spaceList(l);
  if (!loc) {
    const p = projectOf(l, c, id);
    if (!p) return "";
    return spaces.length > 1 ? `Work · ${spaces.find((s) => s.id === p)?.name ?? p}` : "Work";
  }
  // No "3." tab position: the tab bar doesn't show it. An unnamed tab is named after its first agent's title, so for that
  // agent the tab part would only repeat the row's title; leave it out.
  const tab = tabName(loc.tab);
  const parts = tab === useStore.getState().sessions[id]?.title ? [loc.ws.name] : [tab, loc.ws.name];
  if (spaces.length > 1) parts.push(spaces.find((s) => s.id === spaceOf(loc.ws))?.name ?? spaceOf(loc.ws));
  return parts.join(" · ");
}

/** Drag handles here on touch (track): no browser pan; seams wider for a finger (8px is hard to hit). */
export const DRAG_TOUCH = "touch-none";
export const SEAM_COL_COARSE = "pointer-coarse:w-6";
export const SEAM_ROW_COARSE = "pointer-coarse:h-6";

/** Title bar tint for a tile's status, same colours as StatusDot: waiting / error red, running amber, finished unseen green. */
export function statusTint(status: string, unread: boolean) {
  if (status === "waiting" || status === "error") return "bg-bad/15";
  if (status === "running") return "bg-warn/15";
  if (unread) return "bg-ok/15";
  return undefined;
}
