// Reading mid-chat while a reply streams in below: nothing on screen may move, not even by a fraction of a pixel.
//   node midstream.mjs [--tab=2] [--at="something the user said"] [--up=600] [--dt=40] [--size=8] [--min=0.2]
// Cuts the session at a user message, scrolls --up px from the bottom, then feeds the turn after it in (UI side only,
// store.applyEvents) and records rAF and painted frames.
import { args, launch, open, readEvents, replay, startFrameLog, stopFrameLog, streamify, tab, useStore } from "./lib.mjs";

const a = args({ tab: 2, up: 600, dt: 40, size: 8, min: 0.2 });
const { browser, page } = await launch();
await open(page);
await useStore(page);
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
const vp = page.viewportSize();
await page.mouse.move(vp.width * 0.6, vp.height * 0.45);
for (let s = 0; s < a.up; s += 100) (await page.mouse.wheel(0, -100), await page.waitForTimeout(30));
await page.waitForTimeout(1200);
const at0 = await page.evaluate(() => { const sc = [...document.querySelectorAll("[data-chat-scroller]")].find((s) => !s.closest(".invisible")); return sc ? sc.scrollHeight - sc.clientHeight - sc.scrollTop : 0; });
if (at0 < a.up / 2) {
  // Shorter than a screen plus --up: nothing to read mid-chat, and the content sits at the bottom, pushed up by output
  console.log(`can't test: only ${at0}px above the bottom after scrolling ${a.up}px up (pick a later turn with --at)`);
  await browser.close();
  process.exit(2);
}
const rest = streamify(all.slice(cut + 1, end + 1), { size: a.size });
await startFrameLog(page, { ro: true });
await replay(page, sid, rest, { dt: a.dt });
await page.waitForTimeout(800);
const { frames, ro } = await stopFrameLog(page);
const moves = (fs) => {
  const out = [];
  for (let i = 1; i < fs.length; i++) {
    const p = fs[i - 1], c = fs[i];
    if (p.sc !== c.sc || c.hidden) continue;
    const ds = Object.keys(c.rows).filter((k) => k in p.rows).map((k) => +(c.rows[k][0] - p.rows[k][0]).toFixed(2));
    const big = ds.filter((d) => Math.abs(d) > a.min);
    if (big.length) out.push({ t: c.t, by: [...new Set(big)].slice(0, 3), st: [p.st, c.st], sh: [p.sh, c.sh] });
  }
  return out;
};
const mr = moves(frames), mp = moves(ro);
const f0 = frames[0];
console.log(`cut at event ${cut}, ${rest.length} events streamed, reading ${f0 ? f0.sh - f0.ch - f0.st : "?"}px above the bottom`);
console.log(`rows on screen moved (> ${a.min}px): rAF frames ${mr.length} of ${frames.length}, painted frames ${mp.length} of ${ro.length}`);
for (const m of mp.slice(0, 8)) console.log("  painted", JSON.stringify(m));
await browser.close();
process.exit(mp.length || mr.length ? 1 : 0);
