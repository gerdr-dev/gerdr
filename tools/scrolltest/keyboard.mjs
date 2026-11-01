// Phone: typing while at the bottom of a chat that's being written. Each key can bring an IME candidate bar up or down,
// the keyboard changes height and so does the chat; it has to stay at the bottom in every frame drawn (the user saw it
// jump a line on every space typed). Plays a turn back like /replay and changes the viewport height meanwhile.
//   node keyboard.mjs [--rounds=12] [--step=40] [--cpu=1] [--type]
// --type: instead, type into the composer past its first line and keep typing spaces there (the composer measures its
// height on each key; that once clamped the chat a line short of the bottom). No reply playing: the chat sits still.
import { args, launch, open, startFrameLog, stopFrameLog, useStore } from "./lib.mjs";

const a = args({ rounds: 12, step: 40, cpu: 1 });
const W = 400;
const H = 600;
const { browser, ctx, page } = await launch({ width: W, height: H, dpr: 3, scrollbars: false, mobile: true });
await open(page);
await useStore(page);
const cdp = await ctx.newCDPSession(page);
if (a.cpu > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: a.cpu });
const sid = await page.evaluate(() => {
  const l = window.__store.get().layout;
  const w = l.workspaces.find((x) => x.id === l.active);
  return w.tabs.find((t) => t.id === w.activeTab)?.root.sessionId;
});
let clamped = 0;
if (a.type) {
  const box = page.locator("textarea[data-composer]").first();
  await box.click();
  await page.keyboard.type("这是一段很长的话，用来把输入框撑到第二行，看看对话会不会跟着跳。".repeat(2));
  await page.waitForTimeout(800);
  // The clamp itself: any scroll event that leaves the bottom while nobody scrolls. The list puts it back a frame later
  // (TanList's fallback), so frames alone barely show it; a phone painted it for a good while.
  await page.evaluate(() => {
    const sc = [...document.querySelectorAll("[data-chat-scroller], [data-virtuoso-scroller]")].find((x) => !x.closest(".invisible"));
    window.__clamps = [];
    sc.addEventListener("scroll", () => {
      const d = sc.scrollHeight - sc.clientHeight - sc.scrollTop;
      if (d > 1) window.__clamps.push(Math.round(d));
    });
  });
  await startFrameLog(page, { ro: true });
  for (let r = 0; r < a.rounds; r++) {
    await page.keyboard.type(r % 2 ? "x" : " ");
    await page.waitForTimeout(250);
  }
  await page.waitForTimeout(500);
  const clamps = await page.evaluate(() => window.__clamps);
  if (clamps.length) console.log(`  the chat's scroll position was clamped off the bottom ${clamps.length} times (${clamps.slice(0, 5).join(", ")}px)`);
  clamped = clamps.length;
} else {
  if (!(await page.evaluate((sid) => window.__store.replayLastTurn(sid), sid))) throw new Error("nothing to replay");
  await page.waitForTimeout(2500);
  await startFrameLog(page, { ro: true });
  for (let r = 0; r < a.rounds; r++) {
    await page.setViewportSize({ width: W, height: r % 2 ? H : H - a.step });
    await page.waitForTimeout(300);
  }
}
const { ro, frames } = await stopFrameLog(page);
// Painted frames (the late ResizeObserver's, docs/dev/SCROLL.md) short of the bottom
const off = (a.type ? frames : ro).filter((f) => !f.hidden && f.sh - f.ch - f.st > 1);
for (const f of off.slice(0, 10)) console.log(`  ${f.t}ms ${f.sh - f.ch - f.st}px from the bottom`);
console.log(`${(a.type ? frames : ro).length} frames, ${a.type ? `${a.rounds} keys typed on the composer's second line` : `the chat changed height ${a.rounds} times`}, ${off.length} short of the bottom`);
await browser.close();
process.exit(off.length || clamped ? 1 : 0);
