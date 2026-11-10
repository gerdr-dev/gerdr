// Expanding a collapsed thing at the bottom: the chat grows below the screen. It must leave the bottom (button shows)
// and stay where it is, not snap to the end once the click's hold runs out (~1s later).
// Collapsing it again lands back on the end: following the bottom again, no button.
//   node expand.mjs [--tab=1]
import { args, fromBottom, launch, open, tab, useStore } from "./lib.mjs";

const a = args({ tab: 1 });
const { browser, page } = await launch();
await open(page);
await useStore(page);
await tab(page, a.tab);
await page.waitForTimeout(2500);
// The lowest collapsed toggle on screen
const ok = await page.evaluate(() => {
  const s = [...document.querySelectorAll("[data-chat-scroller]")].find((x) => x.offsetParent);
  const r = s.getBoundingClientRect();
  const ts = [...s.querySelectorAll('[aria-expanded="false"]')].filter((n) => {
    const b = n.getBoundingClientRect();
    return b.top > r.top && b.bottom < r.bottom && b.height > 0;
  });
  const t = ts.at(-1);
  if (!t) return false;
  t.setAttribute("data-exp-target", "");
  return true;
});
if (!ok) {
  console.log("no collapsed toggle on screen");
  process.exit(2);
}
const fb0 = (await fromBottom(page)).find((x) => x.visible);
await page.click("[data-exp-target]");
const samples = [];
for (const ms of [50, 300, 700, 1200, 2000]) {
  await page.waitForTimeout(ms - (samples.at(-1)?.ms ?? 0));
  const fb = (await fromBottom(page)).find((x) => x.visible);
  const btn = await page.evaluate(() => !!document.querySelector('[data-gerdr-chat] button[title*="底部"], [data-gerdr-chat] button[title*="bottom"]'));
  samples.push({ ms, fromBottom: fb?.fromBottom, btn });
}
// Collapse it again: the chat shrinks back to the end, so it must follow the bottom again (no button)
await page.click("[data-exp-target]");
await page.waitForTimeout(300);
const back = (await fromBottom(page)).find((x) => x.visible);
const backBtn = await page.evaluate(() => !!document.querySelector('[data-gerdr-chat] button[title*="底部"], [data-gerdr-chat] button[title*="bottom"]'));
console.log(`started ${fb0?.fromBottom}px from bottom`);
for (const s of samples) console.log(`${s.ms}ms: ${s.fromBottom}px from bottom, button ${s.btn}`);
console.log(`collapsed: ${back?.fromBottom}px from bottom, button ${backBtn}`);
await browser.close();
const first = samples[0].fromBottom;
process.exit(first > 2 && samples.every((s) => Math.abs(s.fromBottom - first) < 2 && s.btn) && back.fromBottom <= 2 && !backBtn ? 0 : 1);
