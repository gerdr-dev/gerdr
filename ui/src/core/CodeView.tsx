import { type ReactNode, useContext, useEffect } from "react";
import { createPortal } from "react-dom";
import { currentSpace } from "@/lib/layout";
import { setCodeHost, useStore } from "@/lib/store";
import { PanelKeyContext } from "./visible";

/**
 * Code inside a panel: while the panel that renders this is the one shown, Gerdr shows Code's own layout (its list of
 * workspaces, tabs, split panes, terminals) in the panel's place, and draws `sidebar` at the top of that list. So a
 * panel can be "Code plus something" (a secretary and its workers above the person's own conversations) without
 * copying Code, and the built-in Code can become a panel that renders only this.
 *
 * It is the same layout Code mode shows, not a second one: a second copy of the panes would mean two terminals on one
 * PTY and two composers per conversation. The rest of the panel's page isn't drawn while this is mounted; overlays it
 * opens through usePortalContainer still are when they set `visible pointer-events-auto` on themselves.
 */
export function CodeView({ sidebar }: { sidebar?: ReactNode }) {
  const key = useContext(PanelKeyContext);
  useEffect(() => {
    if (!key) return;
    setCodeHost(key, true);
    return () => setCodeHost(key, false);
  }, [key]);
  // The slot is only there while this panel is the one shown (UnifiedSidebar): another hosting panel kept mounted,
  // hidden, must not draw into it
  const slot = useStore((s) => {
    if (!key || s.mode !== "work") return null;
    const space = currentSpace(s.layout);
    const panel = s.workPanels[space];
    return (panel ? `${space}/${panel}` : space) === key ? s.codeSlot : null;
  });
  return slot && sidebar ? createPortal(sidebar, slot) : null;
}
