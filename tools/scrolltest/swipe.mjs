// Phone overview: quick sideways swipes in a row, frame by frame. Counts frames where a page on screen is blank
// (no chat drawn, nor its snapshot): the flash of empty background between pages (OverviewPhone.tsx, Snap).
//   node swipe.mjs [--cpu=4] [--rounds=3]
import { args, launch, open } from "./lib.mjs";

const a = args({ cpu: 4, rounds: 3 });
const { browser, page } = await launch({ width: 390, height: 844, mobile: true });
await open(page);
const url = await page.evaluate(() => performance.getEntriesByType("resource").map((e) => e.name).find((n) => /\/src\/lib\/overview\.ts/.test(n)));
await page.evaluate(async (u) => (await import(u)).toggleOverview(true), url);
await page.waitForTimeout(2500);
const cdp = await page.context().newCDPSession(page);
if (+a.cpu > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: +a.cpu });

await page.evaluate(() => {
  window.__blank = [];
  window.__frames = 0;
  const tick = () => {
    window.__frames++;
    const w = innerWidth;
    for (const el of document.querySelectorAll(".absolute.inset-0.bg-background[style], .absolute.inset-0.bg-background:not([style])")) {
      const r = el.getBoundingClientRect();
      const vis = Math.min(r.right, w) - Math.max(r.left, 0);
      if (vis > 40 && !el.innerText.trim()) window.__blank.push({ f: window.__frames, vis: Math.round(vis) });
    }
    requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
});

const y = 500;
const swipe = async (dir) => {
  const [x0, x1] = dir > 0 ? [330, 60] : [60, 330];
  await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: x0, y }] });
  for (let i = 1; i <= 6; i++) {
    await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: x0 + ((x1 - x0) * i) / 6, y }] });
    await page.waitForTimeout(16);
  }
  await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
};
for (let r = 0; r < +a.rounds; r++) {
  for (let i = 0; i < 3; i++) (await swipe(1), await page.waitForTimeout(120));
  await page.waitForTimeout(700);
  for (let i = 0; i < 3; i++) (await swipe(-1), await page.waitForTimeout(120));
  await page.waitForTimeout(700);
}
const res = await page.evaluate(() => ({ frames: window.__frames, blank: window.__blank }));
const frames = new Set(res.blank.map((b) => b.f));
console.log(`frames ${res.frames}, frames with a blank page on screen: ${frames.size}`);
console.log("blank frames:", [...frames].join(" "));
await browser.close();
