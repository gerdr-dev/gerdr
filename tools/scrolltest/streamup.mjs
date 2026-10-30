// Scrolling up from the bottom while a reply streams in has to stick. The first 80px up still measure as "at the
// bottom", so if only distance decides, each chunk of output pulls a user who scrolls up in small steps back down.
//   node streamup.mjs [--at="something the user said"] [--tab=1] [--step=30] [--steps=5] [--gap=1500] [--dt=150] [--size=4]
// --gap: pause between steps. It has to outlast the 600ms after the last input in which nothing pulls to the bottom:
// the bug only showed once the reply grew after that (with fast steps, growth inside the window passed as the user
// leaving). --v prints each step.
// Without --at it takes the user message followed by the most agent text. Feeds that turn in on the UI side only
// (store.applyEvents) while wheeling up in small steps, then checks how far from the bottom it ended.
import { args, fromBottom, launch, open, readEvents, replay, streamify, tab, useStore } from "./lib.mjs";

const a = args({ tab: 1, step: 30, steps: 5, gap: 1500, dt: 150, size: 4 });
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
console.log(`cut at event ${cut}, streaming ${rest.length} events`);
await page.evaluate(([sid, base]) => window.__store.applyEvents(sid, base, true), [sid, all.slice(0, cut + 1)]);
await page.waitForTimeout(2500);
const vp = page.viewportSize();
await page.mouse.move(vp.width * 0.6, vp.height * 0.45);
const feeding = replay(page, sid, rest, { dt: a.dt });
await page.waitForTimeout(500); // let the reply get going first
const fb0 = (await fromBottom(page)).find((x) => x.visible);
for (let i = 0; i < a.steps; i++) {
  await page.mouse.wheel(0, -a.step);
  await page.waitForTimeout(a.gap);
  if (a.v) console.log(`  step ${i + 1}: ${(await fromBottom(page)).find((x) => x.visible)?.fromBottom}px from bottom`);
}
await page.waitForTimeout(800);
const fb = (await fromBottom(page)).find((x) => x.visible);
const want = a.step * a.steps * 0.5;
console.log(`started ${fb0?.fromBottom}px from bottom, scrolled up ${a.step * a.steps}px in ${a.steps} steps while streaming, ended ${fb?.fromBottom}px from bottom`);
await feeding.catch(() => {});
await browser.close();
process.exit(fb && fb.fromBottom >= want ? 0 : 1);
