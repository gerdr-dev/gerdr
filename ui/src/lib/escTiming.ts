// Esc (stop, and take back words the agent hasn't used) timed end to end, reported into gerdr.log when slow.
// Why: a ~2s wait between Esc and the words coming back to the composer was reported, while the server's own log
// said it had answered in under 1s and a copy of the same session took ~270ms. What's left is the page: the key
// press waiting behind other work, the response waiting to be handled, the paint after it. This splits those up.
import { api } from "./api";

const SLOW_MS = 500;

// Long tasks (main thread busy ≥50ms) of the last little while: how much of a slow Esc was the page being busy.
const longTasks: { start: number; end: number }[] = [];
try {
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) longTasks.push({ start: e.startTime, end: e.startTime + e.duration });
    if (longTasks.length > 100) longTasks.splice(0, longTasks.length - 100);
  }).observe({ type: "longtask" });
} catch {}

function busyBetween(a: number, b: number) {
  let ms = 0;
  for (const t of longTasks) ms += Math.max(0, Math.min(b, t.end) - Math.max(a, t.start));
  return ms;
}

const nextPaint = () => new Promise<number>((ok) => requestAnimationFrame(() => setTimeout(() => ok(performance.now()))));

/** api.escape, timed. Called from the composer's keydown handler, so `window.event` is the key press itself. */
export function timedEscape(id: string) {
  const ev = (window as { event?: Event }).event;
  const t0 = performance.now();
  // A key press's timeStamp is on the same clock as performance.now(): the gap is how long it waited to be handled
  const pressed = ev && ev.timeStamp > 0 && ev.timeStamp <= t0 ? ev.timeStamp : t0;
  const p = api.escape(id);
  p.then(
    async (r) => {
      const answered = performance.now();
      // The composer puts the words back in the same tick the promise settles; they show at the next paint
      const painted = await nextPaint();
      const total = painted - pressed;
      if (total < SLOW_MS) return;
      api
        .uiSlow("escape", id, {
          total,
          keyWait: t0 - pressed, // the key press queued behind other work
          request: answered - t0, // server + network + the response waiting to be handled
          paint: painted - answered, // words handed back → on screen
          busy: busyBetween(pressed, painted), // long tasks inside the whole span
          gave: r.content?.length ?? 0,
        })
        .catch(() => {});
    },
    () => {},
  );
  return p;
}
