import { createContext, useContext } from "react";

/**
 * Where a panel's overlays go (a Radix Portal's `container`: sheets, dialogs, menus): an element inside its own
 * mount, not document.body. Gerdr keeps a panel mounted but hidden when another panel or Code is shown (its state stays),
 * and only what's inside the mount is hidden with it: a drawer portaled to body stayed on screen over Code and
 * other panels. WorkView's MountRoot provides it around the panel's root; null outside a panel (body, as before).
 */
export const PortalContext = createContext<HTMLElement | null>(null);

/** The element a panel's overlays should be portaled into: `<SheetPrimitive.Portal container={usePortalContainer()}>`. */
export function usePortalContainer(): HTMLElement | null {
  return useContext(PortalContext);
}
