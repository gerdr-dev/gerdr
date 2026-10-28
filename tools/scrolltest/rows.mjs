// 查「行自己的高度」两类问题 —— 它们会让虚拟列表的占位和实际对不上，翻起来就跳：
//   node rows.mjs [--tab=1] [--flings=20]
// 1. 挂上之后自己又变高矮：DOM 没变也会（content-visibility 的占位、图片 / 字体后到、异步高亮）。
//    一边甩着让行一次次挂上，记每一行（按 data-item-index 认，卸了再挂也认得）出现过的所有高度。
// 2. Virtuoso 记的高度（data-known-size）和真实高度对不上：取整之类，每行差一点，卸得越多攒得越多。
import { args, fling, launch, open, tab, useStore } from "./lib.mjs";

const a = args({ tab: 1, flings: 20 });
const { browser, page } = await launch();
await open(page);
await useStore(page);
await tab(page, a.tab);
await page.waitForTimeout(1500);
await page.evaluate(() => {
  const sc = [...document.querySelectorAll("[data-virtuoso-scroller]")].find((s) => !s.closest(".invisible"));
  window.__hist = {};
  window.__known = [];
  const look = () => {
    for (const it of sc.querySelectorAll("[data-item-index]")) {
      const key = it.dataset.itemIndex + " " + (it.textContent || "").slice(0, 80); // 同样文字的行很多（「跑了 2 条命令」），带上 item-index 才分得开
      const h = +it.getBoundingClientRect().height.toFixed(2);
      const hs = (window.__hist[key] ??= []);
      if (hs[hs.length - 1] !== h) hs.push(h);
      const k = +it.dataset.knownSize;
      if (k && Math.abs(k - h) > 0.01) window.__known.push([key.slice(0, 40), k, h]);
    }
  };
  new MutationObserver(look).observe(sc, { childList: true, subtree: true });
  new ResizeObserver(look).observe(sc.querySelector("[data-testid=virtuoso-item-list]"));
});
const vp = page.viewportSize();
await page.mouse.move(vp.width * 0.6, vp.height * 0.45);
for (let i = 0; i < a.flings; i++) await fling(page, -1);
for (let i = 0; i < a.flings; i++) await fling(page, 1);
await page.waitForTimeout(800);
const { hist, known } = await page.evaluate(() => ({ hist: window.__hist, known: window.__known }));
const changed = Object.entries(hist).filter(([, hs]) => hs.length > 1);
for (const [k, hs] of changed.slice(0, 30)) console.log("高度变过", hs.join(" → "), " ", JSON.stringify(k.slice(0, 50)));
for (const x of known.slice(0, 10)) console.log("记的和实际不一样", JSON.stringify(x));
console.log(`看过 ${Object.keys(hist).length} 行：挂上后高度变过的 ${changed.length} 行，记的和实际对不上 ${known.length} 次`);
console.log("（展开 / 收起、流式输出时变高是正常的；这里没在动，变了的就要查：去 DOM 里看那一行有什么）");
await browser.close();
process.exit(changed.length || known.length ? 1 : 0);
