// Phone: a finger drags the chat a little while a reply streams in, then stays down without moving. What's under the
// finger must stay put; it used to bob up and down (the reply pulled it toward the bottom, the finger dragged it back).
//   node touchhold.mjs [--at="something the user said"] [--tab=1] [--up=40] [--hold=3000] [--dt=60] [--size=6] [--cpu=1] [--ro] [--height=860] [--creep] [--slow]
// --height=462: a phone with the keyboard up (the chat area is then shorter than the reply being written). --creep: the
// finger keeps inching down while held; the content may then only move down with it.
// --wait: ms of the reply streaming in before the finger lands (it must be a screen tall by then).
// --up: how far the finger drags down (= scrolls up), from the bottom. 40 stays inside the 80px that still count as "at
// the bottom"; 300+ is reading inside the reply being written. --hold: how long the finger then stays still.
// --up=-40: the finger pushes up at the bottom (nothing to scroll) and slips back a pixel as it lifts; or --up under the
// touch slop (~15px): either way nothing scrolls while the finger is down, so no jump-to-bottom button then; after it
// lifts the button must match the position (shown exactly when off the end).
// --slow: the drag goes a third of a pixel every 50ms. At the bottom each such step used to be put back on the end
// (and a step under 0.5px didn't count as scrolling up), so a slow drag never got off it.
// --taps=3 [--gap=80]: that gesture three times in quick succession. --back: after holding, the finger drags back
// down past the end and lifts; the chat must follow the bottom again.
// Without --at it takes the user message followed by the most agent text. Feeds that turn in on the UI side only.
import { args, launch, open, readEvents, replay, startFrameLog, stopFrameLog, streamify, tab, useStore } from "./lib.mjs";

const a = args({ tab: 1, wait: 2500, taps: 1, gap: 80, up: 40, hold: 3000, dt: 60, size: 6, cpu: 1, height: 860 });
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
const all = readEvents(sid);
const isUser = (e) => e.kind === "user" || e.update?.sessionUpdate === "user_message_chunk";
const textLen = (e) => (e.update?.sessionUpdate === "agent_message_chunk" ? (e.update.content?.text?.length ?? 0) : 0);
let cut = -1;
if (a.at) cut = all.findIndex((e) => isUser(e) && JSON.stringify(e.content ?? e.update?.content ?? "").includes(a.at));
else {
  let best = 0;
  for (let i = 0; i < all.length; i++) {
    if (!isUser(all[i])) continue;
    let n = 0;
    for (let j = i + 1; j < all.length && !isUser(all[j]); j++) n += textLen(all[j]);
    if (n > best) (best = n), (cut = i);
  }
}
if (cut < 0) throw new Error("no user message to cut at");
let end = all.findIndex((e, i) => i > cut && e.kind === "turn_end");
if (end < 0) end = all.length - 1;
const rest = streamify(all.slice(cut + 1, end + 1), { size: a.size });
await page.evaluate(([sid, base]) => window.__store.applyEvents(sid, base, true), [sid, all.slice(0, cut + 1)]);
await page.waitForTimeout(2500);
const feeding = replay(page, sid, rest, { dt: a.dt }).catch(() => {}); // rejects once the browser closes
await page.waitForTimeout(a.wait); // the reply gets going (and a screen tall) first
const touch = (type, y) => cdp.send("Input.dispatchTouchEvent", { type, touchPoints: type === "touchEnd" ? [] : [{ x: 200, y }] });
const scrollTop = () =>
  page.evaluate(() => [...document.querySelectorAll("[data-virtuoso-scroller], [data-chat-scroller]")].find((x) => !x.closest(".invisible")).scrollTop);
const st0 = await scrollTop();
const y0 = 300;
await touch("touchStart", y0);
// --slow: a third of a pixel every 50ms, a finger dragging slowly on a dpr-3 phone
const moves = a.slow ? Math.abs(a.up) * 3 : Math.max(4, Math.round(Math.abs(a.up) / 8));
for (let i = 1; i <= moves; i++) {
  await touch("touchMove", y0 + (a.up * i) / moves);
  await page.waitForTimeout(a.slow ? 50 : 16);
}
// --up negative: the finger pushes up at the bottom (nothing to scroll) and slips back a pixel as it lifts
if (a.up < 0) {
  await touch("touchMove", y0 + a.up + 1);
  await page.waitForTimeout(16);
}
await page.waitForTimeout(150);
// A drag shorter than the touch slop (~15px), or pushing up at the bottom, never scrolls up. By scrollTop, not the
// distance from the end: the chat may stop following while the finger is down
const offEnd = () =>
  page.evaluate(() => {
    const sc = [...document.querySelectorAll("[data-virtuoso-scroller], [data-chat-scroller]")].find((x) => !x.closest(".invisible"));
    return sc.scrollHeight - sc.clientHeight - sc.scrollTop;
  });
// scrollTop alone misses a drag while the chat was still following output (it kept growing past st0)
if ((await scrollTop()) >= st0 - 0.5 && (await offEnd()) <= 2) {
  // Nothing scrolled, so the chat must still be following the bottom once the finger lifts (a tap or a jitter at the
  // bottom used to leave it unstuck: the jump-to-bottom button up at the very bottom, new output not followed).
  // Nor may it leave the bottom while the finger is still down: the button flashed up on every jitter
  const whileDown = await page.evaluate(() => !!document.querySelector("button svg.lucide-arrow-down"));
  await touch("touchEnd");
  // --taps=N: N-1 more of the same right after (--gap ms apart), as when tapping around at the bottom. A touch landing
  // before the previous one's check (150ms after lifting) used to lose it for good
  for (let k = 1; k < a.taps; k++) {
    await page.waitForTimeout(a.gap);
    await touch("touchStart", y0);
    for (let i = 1; i <= moves; i++) {
      await touch("touchMove", y0 + (a.up * i) / moves);
      await page.waitForTimeout(16);
    }
    if (a.up < 0) await touch("touchMove", y0 + a.up + 1);
    await touch("touchEnd");
  }
  await page.waitForTimeout(1000);
  const [after, button] = await page.evaluate(() => {
    const sc = [...document.querySelectorAll("[data-virtuoso-scroller], [data-chat-scroller]")].find((x) => !x.closest(".invisible"));
    // The jump-to-bottom button (one chat open in the debug instance)
    return [sc.scrollHeight - sc.clientHeight - sc.scrollTop, !!document.querySelector("button svg.lucide-arrow-down")];
  });
  // The button says where the chat is: shown exactly when it's off the end (a finger slipping back as it lifts can
  // fling it up a few px, which is off the end too). Never while the finger is down, as nothing scrolled then
  // --slow drags well past the touch slop: it has to scroll
  const stuck = !whileDown && button === after > 2 && !a.slow;
  console.log(
    `--up=${a.up}: the drag didn't scroll; jump-to-bottom button ${whileDown ? "shown" : "hidden"} with the finger down; 1s after lifting it ${Math.round(after)}px off the bottom, button ${button ? "shown" : "hidden"}`,
  );
  await browser.close();
  await feeding.catch(() => {});
  process.exit(stuck ? 0 : 1);
}
if (a.v)
  await page.evaluate(() => {
    const sc = [...document.querySelectorAll("[data-virtuoso-scroller], [data-chat-scroller]")].find((x) => !x.closest(".invisible"));
    const d = Object.getOwnPropertyDescriptor(Element.prototype, "scrollTop");
    const who = () => new Error().stack.split("\n").slice(2, 6).map((l) => l.trim().replace(/\(?http[^)]*\/([^/?)]+)[^)]*\)?/, "@$1")).join(" < ");
    Object.defineProperty(sc, "scrollTop", { get() { return d.get.call(this); }, set(v) { console.log(`DEBUG set ${Math.round(performance.now())} ${v} from ${d.get.call(this)} ${who()}`); d.set.call(this, v); }, configurable: true });
    for (const m of ["scrollTo", "scrollBy"]) { const f = sc[m]; sc[m] = function (...x) { console.log(`DEBUG ${m} ${Math.round(performance.now())} ${JSON.stringify(x[0])} st ${d.get.call(sc)} ${who()}`); return f.apply(this, x); }; }
    sc.addEventListener("scroll", () => console.log(`DEBUG scroll ${Math.round(performance.now())} st ${d.get.call(sc)}`));
  });
await startFrameLog(page, { ro: !!a.ro });
if (a.creep) {
  // The finger keeps inching down (= reading further up), 1px every 100ms, as on the user's phone
  let y = y0 + a.up;
  for (let t = 0; t < a.hold; t += 100) {
    await touch("touchMove", ++y);
    await page.waitForTimeout(100);
  }
} else await page.waitForTimeout(a.hold);
const { frames: raf, ro } = await stopFrameLog(page);
// --back: the finger then drags the chat back down past the end before lifting; it must follow the bottom again
if (a.back) {
  const from = y0 + a.up + (a.creep ? a.hold / 100 : 0);
  // To near the top of the screen: the reply kept growing while the chat wasn't following it
  const n = Math.max(4, Math.round((from - 10) / 8));
  for (let i = 1; i <= n; i++) {
    await touch("touchMove", from - ((from - 10) * i) / n);
    await page.waitForTimeout(16);
  }
}
await touch("touchEnd");
await page.waitForTimeout(a.back ? 1000 : 500);
const [button, off] = await page.evaluate(() => {
  const sc = [...document.querySelectorAll("[data-virtuoso-scroller], [data-chat-scroller]")].find((x) => !x.closest(".invisible"));
  return [!!document.querySelector("button svg.lucide-arrow-down"), sc.scrollHeight - sc.clientHeight - sc.scrollTop];
});
// The finger did scroll up: lifting it must not put the chat back on the bottom; with --back it must
const left = a.back ? !button && off < 2 : button;
if (!left)
  console.log(
    a.back
      ? `dragged back down to the end and lifted: ${Math.round(off)}px off the bottom, jump-to-bottom button ${button ? "shown" : "hidden"}`
      : "lifting the finger stuck the chat to the bottom again (no jump-to-bottom button)",
  );
const frames = a.ro ? ro : raf;
// Finger down and still: no row on screen may move at all. Track the row nearest the top (what's being read).
let moved = 0, worst = 0, path = [];
for (let i = 1; i < frames.length; i++) {
  const p = frames[i - 1], c = frames[i];
  const common = Object.keys(c.rows).filter((k) => k in p.rows);
  if (!common.length) continue;
  const k = common[0];
  const vis = c.rows[k][0] - p.rows[k][0];
  // --creep: the finger scrolls up, so the content may move down with it, never up (text running on)
  if (a.creep ? vis < -1 : Math.abs(vis) > 1) {
    moved++;
    worst = Math.max(worst, Math.abs(vis));
    if (path.length < 15) path.push(`${c.t}ms row ${k} ${vis > 0 ? "+" : ""}${vis.toFixed(1)} (st ${p.st}->${c.st}, sh ${p.sh}->${c.sh})`);
  }
}
for (const l of path) console.log("  " + l);
console.log(`--up=${a.up}: ${frames.length} frames while holding still, content moved in ${moved} (worst ${worst.toFixed(1)}px)`);
await feeding.catch(() => {});
await browser.close();
process.exit(moved || !left ? 1 : 0);
