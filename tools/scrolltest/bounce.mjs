// Following new output at the bottom, the content may only move up. A row added at the end is placed at an estimate
// and measured a moment later: if the painted frames ever show the content moving back down, that estimate was drawn
// (a bounce). rAF frames run before ChatList measures (its ResizeObserver), so they can show an estimate that's never
// painted; the painted frames (a ResizeObserver created after ChatList's) are what counts.
//   node bounce.mjs [--tab=2] [--at="something the user said"] [--dt=40] [--size=8] [--width=1150] [--cpu=1]
import { args, launch, open, readEvents, replay, startFrameLog, stopFrameLog, streamify, tab, useStore } from "./lib.mjs";

const a = args({ tab: 2, dt: 40, size: 8, width: 1150, cpu: 1 });
const { browser, page } = await launch({ width: a.width });
await open(page);
await useStore(page);
if (a.cpu > 1) await (await page.context().newCDPSession(page)).send("Emulation.setCPUThrottlingRate", { rate: a.cpu });
await tab(page, a.tab);
await page.waitForTimeout(1500);
const sid = await page.evaluate(() => {
  const w = window.__store.get().layout.workspaces[0];
  return w.tabs.find((t) => t.id === w.activeTab)?.root.sessionId;
});
const all = readEvents(sid);
const isUser = (e) => e.kind === "user" || e.update?.sessionUpdate === "user_message_chunk";
let cut = a.at ? all.findIndex((e) => isUser(e) && JSON.stringify(e.content ?? e.update?.content ?? "").includes(a.at)) : -1;
if (cut < 0) for (let i = all.length - 1; i >= 0; i--) if (isUser(all[i]) && i < all.length - 5) { cut = i; break; }
let end = all.findIndex((e, i) => i > cut && e.kind === "turn_end");
if (end < 0) end = all.length - 1;
await page.evaluate(([sid, base]) => window.__store.applyEvents(sid, base, true), [sid, all.slice(0, cut + 1)]);
await page.waitForTimeout(2000);
const rest = streamify(all.slice(cut + 1, end + 1), { size: a.size });
console.log(`cut at event ${cut}, streaming ${rest.length} events`);
await startFrameLog(page, { ro: true });
await replay(page, sid, rest, { dt: a.dt });
await page.waitForTimeout(1000);
const { frames, ro } = await stopFrameLog(page);
// The content moving down between two frames while following at the bottom: a row on screen in both, its top going up
// the screen (negative) is following; going down (positive) is the bounce
function bounces(fs) {
  const out = [];
  for (let i = 1; i < fs.length; i++) {
    const p = fs[i - 1], c = fs[i];
    if (p.sc !== c.sc || c.hidden) continue;
    const common = Object.keys(c.rows).filter((k) => k in p.rows);
    if (!common.length) continue;
    const k = common[0];
    const vis = +(c.rows[k][0] - p.rows[k][0]).toFixed(1);
    if (vis > 0.5 && p.sh - p.ch - p.st < 80) out.push({ t: c.t, row: k, vis, sh: [p.sh, c.sh], st: [p.st, c.st] });
  }
  return out;
}
const br = bounces(frames), bp = bounces(ro);
console.log(`rAF frames ${frames.length}: content moved back down ${br.length} times${br.length ? ` (${br.slice(0, 5).map((x) => x.vis).join(", ")}px…)` : ""}`);
console.log(`painted frames ${ro.length}: content moved back down ${bp.length} times`);
for (const x of bp.slice(0, 10)) console.log("  painted", JSON.stringify(x));
await browser.close();
process.exit(bp.length ? 1 : 0);
