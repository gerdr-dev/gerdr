// Phone: reading a reply as it's written, the finger dragging up a bit, pausing, dragging again (user's screen
// recording: whole screens jumped back and forth). Plays the latest long turn back the way /replay does
// (store.replayLastTurn: UI side only), drags meanwhile, and checks every frame that the content only moved with the
// scroll (analyze, scrolling mode: a layout change that isn't compensated is a jump).
//   node touchscroll.mjs [--at="what the user said"] [--fling] [--tab=1] [--rounds=8] [--cpu=1] [--ro] [--v]
// --at: replay the turn of that user message (default: the latest long one, as /replay). --fling: quick flicks that
// carry on by momentum after the finger lifts, instead of slow drags with pauses.
import { analyze, args, launch, open, report, startFrameLog, stopFrameLog, useStore } from "./lib.mjs";

const a = args({ tab: 1, rounds: 8, cpu: 1, height: 860 });
const { browser, ctx, page } = await launch({ width: 400, height: a.height, dpr: 3, scrollbars: false, mobile: true });
await open(page);
await useStore(page);
const cdp = await ctx.newCDPSession(page);
if (a.cpu > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: a.cpu });
const sid = await page.evaluate(() => {
  const l = window.__store.get().layout;
  const w = l.workspaces.find((x) => x.id === l.active);
  return w.tabs.find((t) => t.id === w.activeTab)?.root.sessionId;
});
if (!(await page.evaluate(([sid, at]) => window.__store.replayLastTurn(sid, { at: at || undefined }), [sid, a.at]))) throw new Error("nothing to replay");
await page.waitForTimeout(3000);
const touch = (type, y) => cdp.send("Input.dispatchTouchEvent", { type, touchPoints: type === "touchEnd" ? [] : [{ x: 200, y }] });
// One drag: finger down at y, moved by dy in 16ms steps, then held still for `hold` ms, then lifted
async function drag(y, dy, hold) {
  await touch("touchStart", y);
  const n = Math.max(4, Math.round(Math.abs(dy) / 10));
  for (let i = 1; i <= n; i++) {
    await touch("touchMove", y + (dy * i) / n);
    await page.waitForTimeout(16);
  }
  await page.waitForTimeout(hold);
  await touch("touchEnd");
}
await startFrameLog(page, { ro: !!a.ro });
for (let r = 0; r < a.rounds; r++) {
  if (a.fling) {
    await drag(300, 250, 0); // flick down = scroll up, momentum after
    await page.waitForTimeout(1200);
    await drag(500, -150, 0);
    await page.waitForTimeout(1200);
    continue;
  }
  await drag(300, 160, 600); // finger down = scroll up
  await page.waitForTimeout(300);
  await drag(500, -60, 600); // a little back down
  await page.waitForTimeout(300);
}
const { frames: raf, ro } = await stopFrameLog(page);
// The whole chat mounted again mid-reply (ChatView's "measured" reset: every row rendered and measured anew). Headless
// Chrome on a laptop measures fast enough that no frame shows it; a phone jumped by whole screens (user's recording).
const fr = a.ro ? ro : raf;
let remounts = 0;
for (let i = 1; i < fr.length; i++) {
  if (fr[i].mounted - fr[i - 1].mounted <= 20) continue;
  remounts++;
  console.log(`  ${fr[i].t}ms the whole chat mounted again: ${fr[i - 1].mounted} -> ${fr[i].mounted} rows`);
}
const ev = analyze(fr, { scrolling: true, step: 40 });
const bad = report(ev, { limit: a.v ? 40 : 10 }) + remounts;
console.log(`whole-chat remounts mid-reply: ${remounts}`);
await browser.close();
process.exit(bad ? 1 : 0);
