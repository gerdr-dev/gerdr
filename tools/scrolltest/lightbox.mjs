// Image lightbox on a phone: quick swipes in a row run through the images (a finger landing mid-slide swipes on), and
// turning a page never moves an <img> in the DOM (taken out and put back, it drew nothing for a frame on Android).
//   node lightbox.mjs [--cpu=4]
// Opens the lightbox over 4 generated images (canvas data URLs, all loaded before the first swipe), then: 3 quick
// swipes forward (120ms apart, shorter than a slide), 3 back, and a wrap from the first image to the last.
import { args, launch, open } from "./lib.mjs";

const a = args({ cpu: 4 });
const { browser, page } = await launch({ width: 390, height: 844, mobile: true });
await open(page);
const cdp = await page.context().newCDPSession(page);
if (+a.cpu > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: +a.cpu });

const url = await page.evaluate(() => performance.getEntriesByType("resource").map((e) => e.name).find((n) => /packages\/chat\/src\/components\/Lightbox\.tsx/.test(n)));
if (!url) throw new Error("Lightbox module not found");
await page.evaluate(async (u) => {
  const items = ["#c33", "#3c3", "#33c", "#cc3"].map((c, i) => {
    const cv = document.createElement("canvas");
    cv.width = 600;
    cv.height = 900;
    const g = cv.getContext("2d");
    g.fillStyle = c;
    g.fillRect(0, 0, 600, 900);
    return { url: cv.toDataURL(), alt: `img${i + 1}` };
  });
  (await import(u)).openLightbox(items, 0);
  // DOM moves: an <img> removed and added back by the same mutation batch
  window.__moves = 0;
  new MutationObserver((recs) => {
    const removed = new Set();
    for (const r of recs) r.removedNodes.forEach((n) => removed.add(n));
    for (const r of recs) r.addedNodes.forEach((n) => removed.has(n) && window.__moves++);
  }).observe(document.body, { childList: true, subtree: true });
}, url);
await page.waitForTimeout(1200);
const caption = () => page.evaluate(() => document.querySelector("[role=dialog] .tabular-nums")?.parentElement?.textContent ?? "");

const y = 500;
const swipe = async (dir) => {
  const [x0, x1] = dir > 0 ? [330, 90] : [90, 330];
  await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: x0, y }] });
  for (let i = 1; i <= 6; i++) {
    await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: x0 + ((x1 - x0) * i) / 6, y }] });
    await page.waitForTimeout(16);
  }
  await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
};

let bad = 0;
const expect = async (want, what) => {
  await page.waitForTimeout(900);
  const got = await caption();
  const ok = got.startsWith(want);
  if (!ok) bad++;
  console.log(`${ok ? "ok  " : "FAIL"} ${what}: ${got} (want ${want})`);
};
console.log("start:", await caption());
for (let i = 0; i < 3; i++) (await swipe(1), await page.waitForTimeout(120));
await expect("4 / 4", "3 quick swipes forward");
for (let i = 0; i < 3; i++) (await swipe(-1), await page.waitForTimeout(120));
await expect("1 / 4", "3 quick swipes back");
await swipe(-1);
await expect("4 / 4", "wrap back from the first");
await swipe(1);
await expect("1 / 4", "wrap forward from the last");
const moves = await page.evaluate(() => window.__moves);
if (moves) bad++;
console.log(`${moves ? "FAIL" : "ok  "} images moved in the DOM: ${moves}`);
await browser.close();
process.exit(bad ? 1 : 0);
