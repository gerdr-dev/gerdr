import type { ReactNode } from "react";
import { currentSpace } from "./layout";
import { get } from "./store";

/**
 * Menu items a panel that hosts Code (core CodeView) adds for a pane: in the pane's own ⋯ menu and in its row's
 * right-click menu. For what the panel knows about a conversation and Code doesn't (a task's status: 移到待验收).
 * The ⋯ menu is the one place a phone has for it: no hover, no drag, no right click there.
 */
export type CodeMenuItem = {
  id: string;
  label: string;
  icon?: ReactNode;
  disabled?: boolean;
  onSelect?: () => void;
  /**
   * A second level: picking the item unfolds these under it, in place (as the pane menu's own 更多 does: on a phone a
   * submenu to the side has no room and is cut off). For a list of choices that would make the menu long (which
   * agent, of seven).
   */
  children?: CodeMenuItem[];
};
export type CodeMenuFn = (row: { pane: string; session?: string }) => CodeMenuItem[];

const byPanel = new Map<string, CodeMenuFn>();

/** core useCodeRowMenu: panel `key`'s items; null takes them away. */
export function setCodeMenu(key: string, fn: CodeMenuFn | null) {
  if (fn) byPanel.set(key, fn);
  else byPanel.delete(key);
}

/** The hosting panel's items for a pane, while that panel is the one shown; none otherwise (Code mode, another panel). */
export function codeMenuItems(pane: string, session?: string): CodeMenuItem[] {
  const s = get();
  if (s.mode !== "work") return [];
  const space = currentSpace(s.layout);
  const panel = s.workPanels[space];
  const key = panel ? `${space}/${panel}` : space;
  if (!s.codeHosts[key]) return [];
  try {
    return byPanel.get(key)?.({ pane, session }) ?? [];
  } catch {
    // A panel's function throwing mustn't take the pane's menu down with it
    return [];
  }
}

/**
 * A tag a hosting panel puts after a pane's title: what the panel knows about the conversation in it (a task's number
 * and status). Data, not an element: Gerdr draws the chip and, with `items`, the menu it opens (the way to change it,
 * one tap from the pane on a phone too).
 */
export type CodePaneTag = { label: string; icon?: ReactNode; tone?: "warn" | "ok" | "brand"; title?: string; items?: CodeMenuItem[] };
export type CodePaneTagFn = (row: { pane: string; session?: string }) => CodePaneTag | null | undefined;

const tagByPanel = new Map<string, CodePaneTagFn>();
let tagVersion = 0;
const tagSubs = new Set<() => void>();

/** core useCodePaneTag: panel `key`'s tag; null takes it away. Called again whenever what it would answer may have changed. */
export function setCodePaneTag(key: string, fn: CodePaneTagFn | null) {
  if (fn) tagByPanel.set(key, fn);
  else tagByPanel.delete(key);
  tagVersion++;
  for (const f of tagSubs) f();
}

export const subscribePaneTags = (f: () => void) => (tagSubs.add(f), () => void tagSubs.delete(f));
export const paneTagVersion = () => tagVersion;

/** The hosting panel's tag for a pane, while that panel is the one shown. */
export function codePaneTag(pane: string, session?: string): CodePaneTag | null {
  const s = get();
  if (s.mode !== "work") return null;
  const space = currentSpace(s.layout);
  const panel = s.workPanels[space];
  const key = panel ? `${space}/${panel}` : space;
  if (!s.codeHosts[key]) return null;
  try {
    return tagByPanel.get(key)?.({ pane, session }) ?? null;
  } catch {
    return null;
  }
}
