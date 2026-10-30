// Zoom animation shared by the panes of a tab (Panes.TabView) and the overview's tiles (Overview.Grid): the zoomed item
// slides from its box to the full area and back, over its neighbours.
import { useEffect, useState } from "react";

export const ZOOM_MS = 220;
export const EASE = "cubic-bezier(0.2, 0.8, 0.2, 1)";

/**
 * Which pane is growing to / shrinking from full size right now (zoom toggled within the last ZOOM_MS), if any.
 * Decided during render (not in an effect) so the transition class lands in the same style change as the new box —
 * a frame later the box has already jumped and there is nothing left to transition.
 */
export function useZoomAnim(zoom: string | undefined, phone: boolean) {
  const [seen, setSeen] = useState(zoom);
  const [anim, setAnim] = useState<string>();
  if (seen !== zoom) {
    setSeen(zoom);
    setAnim(phone || matchMedia("(prefers-reduced-motion: reduce)").matches ? undefined : (zoom ?? seen));
  }
  useEffect(() => {
    if (!anim) return;
    const t = setTimeout(() => setAnim(undefined), ZOOM_MS + 30);
    return () => clearTimeout(t);
  }, [anim, seen]);
  return anim;
}

/**
 * True for ZOOM_MS after `sig` changes: an arrangement changed (a tile pinned / swapped / added / removed) and the
 * items should slide to their new boxes. Decided during render, like useZoomAnim, so the transition lands in the same
 * style change as the new boxes. Changes that follow the pointer (seam drags) or the window keep `sig` the same.
 */
export function useLayoutAnim(sig: string) {
  const [seen, setSeen] = useState(sig);
  const [at, setAt] = useState(0);
  if (seen !== sig) {
    setSeen(sig);
    if (!matchMedia("(prefers-reduced-motion: reduce)").matches) setAt(Date.now());
  }
  useEffect(() => {
    if (!at) return;
    const t = setTimeout(() => setAt(0), ZOOM_MS + 30);
    return () => clearTimeout(t);
  }, [at]);
  return at !== 0;
}

export const BOX_TRANSITION = `left ${ZOOM_MS}ms ${EASE}, top ${ZOOM_MS}ms ${EASE}, width ${ZOOM_MS}ms ${EASE}, height ${ZOOM_MS}ms ${EASE}`;
