// Scrolling up from the bottom has to stick: small steps (a mouse wheel notch, a slow trackpad drag) starting at the
// bottom must not be pulled back. Anything that "keeps the chat at the bottom" when a size changes can eat these steps,
// because the first ~80px up still count as at the bottom and the virtual list mounts / unmounts rows on every step.
//   node scrollup.mjs [--tab=1] [--step=40] [--steps=25]
import { args, fromBottom, launch, open, tab, useStore } from "./lib.mjs";

const a = args({ tab: 1, step: 40, steps: 25 });
const { browser, page } = await launch();
await open(page);
await useStore(page);
await tab(page, a.tab);
await page.waitForTimeout(2500);
const vp = page.viewportSize();
await page.mouse.move(vp.width * 0.6, vp.height * 0.45);
const fb0 = (await fromBottom(page)).find((x) => x.visible);
for (let i = 0; i < a.steps; i++) {
  await page.mouse.wheel(0, -a.step);
  await page.waitForTimeout(60);
}
await page.waitForTimeout(800);
const fb = (await fromBottom(page)).find((x) => x.visible);
const want = a.step * a.steps * 0.5;
console.log(`started ${fb0?.fromBottom}px from bottom, scrolled up ${a.step * a.steps}px in ${a.steps} steps, ended ${fb?.fromBottom}px from bottom`);
await browser.close();
process.exit(fb && fb.fromBottom >= want ? 0 : 1);
