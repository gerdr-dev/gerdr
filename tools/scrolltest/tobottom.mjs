// The jump-to-bottom button, from far up a long chat: the scroll in should be one smooth motion. User report (TanStack
// list, on a phone): it stuttered. Checks the frames: the scroll position only ever moves down, no frame stands still
// mid-way, no frame after the first moves more than --max px (the first may jump: both lists skip most of the way), no
// frame takes over 50ms (dropped frames), and it ends at the bottom. rAF frames, not --ro: a scroll resizes nothing.
//   node tobottom.mjs [--screens=20] [--cpu=1] [--mobile] [--max=400]
import { args, fromBottom, launch, open, startFrameLog, stopFrameLog, useStore } from "./lib.mjs";

const a = args({ screens: 20, cpu: 1, max: 400 });
const { browser, ctx, page } = await launch(a.mobile ? { width: 400, height: 860, dpr: 3, scrollbars: false, mobile: true } : {});
await open(page);
await useStore(page);
const cdp = await ctx.newCDPSession(page);
if (a.cpu > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: a.cpu });
const sc = () => [...document.querySelectorAll("[data-virtuoso-scroller], [data-chat-scroller]")].find((x) => !x.closest(".invisible"));
// Up a screen at a time, by wheel or (phone) a finger: input the list reads as "leaving the bottom"
await page.mouse.move(400, 300);
for (let i = 0; i < a.screens; i++) {
  if (a.mobile) await cdp.send("Input.synthesizeScrollGesture", { x: 200, y: 300, yDistance: 600, gestureSourceType: "touch", speed: 4000 });
  else await page.mouse.wheel(0, -700);
  await page.waitForTimeout(60);
}
await page.waitForTimeout(800);
const from = await page.evaluate(`(${sc})().scrollHeight - (${sc})().clientHeight - (${sc})().scrollTop`);
await startFrameLog(page);
await page.locator("button:has(svg.lucide-arrow-down)").first().click();
await page.waitForTimeout(2000);
const { frames } = await stopFrameLog(page);
const fb = (await fromBottom(page)).find((x) => x.visible)?.fromBottom;
const fr = frames.filter((f) => !f.hidden);
let back = 0, still = 0, big = 0, slow = 0, moving = false, done = false;
const steps = [];
for (let i = 1; i < fr.length; i++) {
  const d = fr[i].st - fr[i - 1].st;
  const end = fr[i].sh - fr[i].ch - fr[i].st <= 1;
  if (d) steps.push(d);
  if (d < -1) back++;
  if (d > a.max && moving) big++;
  if (moving && !done && fr[i].t - fr[i - 1].t > 50) slow++;
  if (d === 0 && moving && !done) still++;
  if (d > 0) moving = true;
  if (end) done = true;
}
console.log(`from ${Math.round(from)}px up: ${fr.length} painted frames, steps ${steps.slice(0, 30).join(" ")}${steps.length > 30 ? " …" : ""}`);
console.log(`moved back ${back}, stood still mid-way ${still}, steps over ${a.max}px ${big}, frames over 50ms ${slow}, ends ${fb}px from the bottom`);
await browser.close();
process.exit(back || still || big || slow || fb > 2 ? 1 : 0);
