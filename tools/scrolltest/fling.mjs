// 用触控板那样甩着滚（一串从大到小的滚轮），逐帧看内容有没有跳。
//   node fling.mjs [--tab=1] [--dir=up|down|both] [--flings=20] [--to-top]
// --to-top：一直往上甩到头 —— 会话是分段加载的，翻到顶会往前补一段（loadOlder），补的那一下最容易跳。
// 甩之前先停在哪：默认最底下；--from="某段文字" 先翻到那段话出现在屏幕上（用户说「就在这儿闪」时用）。
import { analyze, args, fling, launch, open, report, startFrameLog, stopFrameLog, tab, useStore } from "./lib.mjs";

const a = args({ tab: 1, dir: "both", flings: 20 });
const { browser, page } = await launch();
await open(page);
await useStore(page);
await tab(page, a.tab);
await page.waitForTimeout(1500);
const sid = await page.evaluate(() => window.__store.get().layout.workspaces[0].tabs.find((t) => t.id === window.__store.get().layout.workspaces[0].activeTab)?.root.sessionId);
const vp = page.viewportSize();
await page.mouse.move(vp.width * 0.6, vp.height * 0.45); // 对话区中间（左边是侧栏）
if (a.from) {
  for (let i = 0; i < 300; i++) {
    const seen = await page.evaluate((t) => [...document.querySelectorAll("[data-index]")].some((e) => e.textContent.includes(t) && e.getBoundingClientRect().top > 0 && e.getBoundingClientRect().top < innerHeight), a.from);
    if (seen) break;
    await page.mouse.wheel(0, -600);
    await page.waitForTimeout(50);
  }
}
await startFrameLog(page, { sid });
if (a["to-top"]) {
  for (let i = 0; i < 400; i++) {
    await fling(page, -1);
    const done = await page.evaluate(([sid]) => !window.__store.get().older[sid] && document.querySelector("[data-virtuoso-scroller]:not(.invisible *)")?.scrollTop < 5, [sid]);
    if (done) break;
  }
} else {
  if (a.dir !== "down") for (let i = 0; i < a.flings; i++) await fling(page, -1);
  if (a.dir !== "up") for (let i = 0; i < a.flings; i++) await fling(page, 1);
}
await page.waitForTimeout(800);
const { frames } = await stopFrameLog(page);
const loads = frames.filter((f, i) => i && f.older === "loading" && frames[i - 1].older !== "loading").length;
console.log(`${frames.length} 帧${loads ? `，往前补了 ${loads} 段` : ""}`);
const bad = report(analyze(frames, { scrolling: true }));
await browser.close();
process.exit(bad ? 1 : 0);
