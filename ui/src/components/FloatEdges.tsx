import { useState } from "react";
import { FLOAT_EDGES, type FloatEdge } from "@/lib/layout";
import { cn } from "@/lib/utils";

/**
 * A floating box's resize edges (layout.FLOAT_EDGES), drawn inside the box (`relative` or `absolute`). While one is
 * held the whole box gets a brand outline (asked for: on a tablet nothing showed the resize had started; a thick bar
 * on the held side alone was too heavy, and idle grips weren't wanted).
 * Shared by a floating Browser pane in Code (Panes.tsx FloatPane) and a panel's floating browser (core/SessionBrowser.tsx).
 */
export function FloatEdges({ onDown }: { onDown: (e: React.PointerEvent<HTMLElement>, edge: FloatEdge, cursor: string) => void }) {
  const [held, setHeld] = useState<FloatEdge | null>(null);
  const press = (e: React.PointerEvent<HTMLElement>, g: FloatEdge) => {
    if (e.button !== 0) return;
    setHeld(g);
    const end = () => {
      setHeld(null);
      window.removeEventListener("pointerup", end);
      window.removeEventListener("pointercancel", end);
    };
    window.addEventListener("pointerup", end);
    window.addEventListener("pointercancel", end);
    onDown(e, g, getComputedStyle(e.currentTarget).cursor);
  };
  return (
    <>
      {/* while an edge is held: one outline round the whole box says the resize has started. z-20: above the box's
          "release to minimize" cover (z-10), which in a panel's floating browser fills the box edge to edge */}
      {held && <div className="pointer-events-none absolute inset-0 z-20 rounded-md ring-2 ring-brand ring-inset" />}
      {FLOAT_EDGES.map(({ e: g, cls }, i) => (
        <div key={i} className={cn("absolute touch-none", cls)} onPointerDown={(e) => press(e, g)} />
      ))}
    </>
  );
}
