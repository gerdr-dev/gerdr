import { useEffect, useRef, useState } from "react";
import { useStore } from "./store";

// Past this, draw the cached (stale) transcript anyway rather than leave a tap looking ignored on a slow connection.
const HOLD_MS = 500;

/**
 * What to draw while switching from one view to another (a tab, a phone overview tile): `want`, unless one of its
 * sessions was just subscribed to and its catch-up hasn't come yet (store.catchingUp). Then the view that was on screen
 * stays up until it has, or for HOLD_MS at most: drawing the cached transcript first and the catch-up a moment later
 * flashes the old state (seen on a phone over the remote link, where the round trip is long). `canHold` says whether
 * the old view still exists to be drawn (a closed tab can't be).
 */
export function useCaughtUp(want: string | undefined, sessions: string[], canHold: (key: string) => boolean): string | undefined {
  const catching = useStore((s) => sessions.some((id) => s.catchingUp[id]));
  const drawn = useRef(want);
  const [late, setLate] = useState<string>();
  useEffect(() => {
    if (!catching || !want) return;
    const t = setTimeout(() => setLate(want), HOLD_MS);
    return () => {
      clearTimeout(t);
      setLate(undefined);
    };
  }, [catching, want]);
  const prev = drawn.current;
  const out = catching && late !== want && prev !== undefined && prev !== want && canHold(prev) ? prev : want;
  useEffect(() => {
    drawn.current = out;
  });
  return out;
}
