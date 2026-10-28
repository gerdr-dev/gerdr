// 打开会话那一下：抖没抖、最后停没停在最底下。
//   node open.mjs [--tab=1] [--reload] [--rounds=3]
// 默认走「切走再切回来」：keepTabs 设成 1（设置里「tab 不保留」），切回来就是重新挂载 —— 列表从头建、边挂边量高度，
// 最容易出事的就是这一段（量的过程中被推离底部、停在半中间；量完缩回时取整误差攒出来的一截）。要 ≥ 2 个 tab。
// --reload：整页重新加载（App 刚启动 / 刷新）。
import { analyze, args, fromBottom, launch, open, report, setConfig, startFrameLog, tab, useStore } from "./lib.mjs";

const a = args({ tab: 1, rounds: 3, settle: 2000 });
const { browser, page } = await launch({ logFromLoad: !!a.reload });
await open(page);
await useStore(page);
let bad = 0;
for (let r = 0; r < a.rounds; r++) {
  if (a.reload) {
    await page.reload();
    await page.waitForSelector("[data-index]");
  } else {
    await setConfig(page, { keepTabs: 1 });
    await tab(page, a.tab === 1 ? 2 : 1);
    await page.waitForTimeout(800);
    await startFrameLog(page);
    await tab(page, a.tab);
  }
  await page.waitForTimeout(a.settle);
  const { frames } = await page.evaluate(() => ({ frames: window.__frames }));
  const fb = (await fromBottom(page)).find((x) => x.visible);
  console.log(`== 第 ${r + 1} 次：${frames.length} 帧，最后离底部 ${fb?.fromBottom}px`);
  bad += report(analyze(frames));
  if (fb && fb.fromBottom > 2) bad++;
}
await browser.close();
process.exit(bad ? 1 : 0);
