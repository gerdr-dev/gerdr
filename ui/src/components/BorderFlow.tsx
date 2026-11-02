import { useEffect, useId, useState } from "react";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

// A wait shorter than this shows nothing: a flash of a loader on a switch that's over at once reads as a glitch.
const SLOW_MS = 200;

/** Session `id` has been waiting for its catch-up (store.catchingUp, lib/catchup) for longer than SLOW_MS. */
export function useSlowCatchUp(id: string | undefined): boolean {
  const catching = useStore((s) => !!id && !!s.catchingUp[id]);
  const [slow, setSlow] = useState(false);
  useEffect(() => {
    if (!catching) {
      setSlow(false);
      return;
    }
    const t = setTimeout(() => setSlow(true), SLOW_MS);
    return () => clearTimeout(t);
  }, [catching]);
  return catching && slow;
}

// The segment's length at full size, in pathLength units (100 = once round), so it is the same share of any border. It
// lengthens in from nothing as it starts (index.css), so it doesn't pop up on the 200ms mark.
const LENGTH = 14;

/**
 * The "loading" mark of a picked control that's waiting (ui/AGENTS.md, "Switching to something that loads"), drawn
 * over the parent, which must be `relative`; corners follow `--radius-md`. `color` is a CSS colour, the brand by default.
 * - `line` (a control with no border of its own): a segment of `color` running round it.
 * - `gap` (a control with a 1px border): this draws that border itself in `color` (pass the control's border colour),
 *   with a gap running round it, so the border stays and the motion shows in it. The control drops its
 *   own border while this is up. A mask cuts the gap, so nothing has to match the background behind the border.
 */
export function BorderFlow({ mode = "line", color = "var(--brand)", className }: { mode?: "line" | "gap"; color?: string; className?: string }) {
  const mask = useId();
  const w = mode === "gap" ? 1 : 1.5;
  const frame = { x: w / 2, y: w / 2, pathLength: 100, style: { "--bf-w": `${w}px` } as React.CSSProperties };
  const dash = (stroke: string) => <rect {...frame} className="bf-dash" stroke={stroke} style={{ ...frame.style, "--len": LENGTH } as React.CSSProperties} />;
  return (
    <svg aria-hidden className={cn("border-flow pointer-events-none absolute inset-0 size-full", className)}>
      {mode === "line" ? (
        dash(color)
      ) : (
        <>
          <mask id={mask}>
            <rect {...frame} stroke="white" />
            {dash("black")}
          </mask>
          <rect {...frame} stroke={color} mask={`url(#${mask})`} />
        </>
      )}
    </svg>
  );
}
