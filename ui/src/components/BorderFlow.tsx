import { useEffect, useState } from "react";
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

/**
 * The "loading" mark of a button / row that was picked and is waiting (ui/AGENTS.md, "Switching to something that
 * loads"): a brand-coloured segment running round its border. Drawn over the parent, which must be `relative`; the
 * corner radius follows `--radius-md` (index.css `.border-flow`). An SVG stroke rather than a conic gradient: on a long
 * flat row a gradient's segment speeds through the short sides, the dash moves at one speed all the way round.
 */
export function BorderFlow({ className }: { className?: string }) {
  return (
    <svg aria-hidden className={cn("pointer-events-none absolute inset-0 size-full overflow-visible", className)}>
      <rect x="0.75" y="0.75" pathLength={100} className="border-flow" />
    </svg>
  );
}
